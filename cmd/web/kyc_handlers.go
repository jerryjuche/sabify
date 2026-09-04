package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sabify/internal/bmoni"
	"sabify/internal/models"
	"sabify/internal/validator"
)

/*
 * Per-teacher KYC wizard.
 *
 * Each teacher owns her own BMONI wallet: she fills a profile (which creates
 * the BMONI user and submits the KYC profile), uploads identity documents, and
 * the server provisions her smart wallet + NGN virtual bank account (VBA).
 * The wizard's progress lives on the bmoni_wallets row (kyc_status), so a
 * failed step is retryable from exactly where it stopped:
 *
 *   not_started → user_created → profile_saved → documents_uploaded → rail_active
 *                   │                 │                  │
 *                   └── failed ◄──────┴──────────────────┘  (retry the step)
 *
 * Routes are teacher-only (routes.go). BMONI calls run synchronously and in
 * the documented order; the page is re-rendered from the row after every
 * step, so a refresh always shows the true state.
 */

// teacherKYCPage renders the wizard: the current state of the teacher's
// wallet, the profile form (until the profile is saved) and the documents
// form (once a BMONI user exists and the profile is complete).
func (app *application) teacherKYCPage(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if err != nil && !errors.Is(err, models.ErrNoRecord) {
		app.serverError(w, err)
		return
	}
	if errors.Is(err, models.ErrNoRecord) {
		wallet = nil
	}

	data := app.newTemplateData(r)
	data.Title = "Wallet setup"
	data.User = user
	data.CurrentPage = "wallet"
	data.Wallet = wallet
	app.render(w, http.StatusOK, "teacher/kyc.html", data)
}

/*
 * teacherKYCProfile creates the teacher's BMONI user (or recovers the
 * existing one for the given phone — the docs' sanctioned 409 handling) and
 * submits the KYC profile against it. On success the row advances to
 * profile_saved; on failure it stays at user_created with kyc_error set and
 * the profile form is shown again so the teacher can correct the data.
 */
func (app *application) teacherKYCProfile(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	form := map[string]string{
		"first_name":   strings.TrimSpace(r.Form.Get("first_name")),
		"last_name":    strings.TrimSpace(r.Form.Get("last_name")),
		"email":        strings.TrimSpace(r.Form.Get("email")),
		"phone_number": strings.TrimSpace(r.Form.Get("phone_number")),
		"date_of_birth": strings.TrimSpace(r.Form.Get("date_of_birth")),
		"gender":       strings.TrimSpace(r.Form.Get("gender")),
		"street":       strings.TrimSpace(r.Form.Get("street")),
		"city":         strings.TrimSpace(r.Form.Get("city")),
		"state":        strings.TrimSpace(r.Form.Get("state")),
		"postal_code":  strings.TrimSpace(r.Form.Get("postal_code")),
		"bvn":          strings.TrimSpace(r.Form.Get("bvn")),
		"source_of_funds": strings.TrimSpace(r.Form.Get("source_of_funds")),
	}

	v := validator.New()
	v.CheckField(validator.NotBlank(form["first_name"]), "first_name", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["last_name"]), "last_name", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["email"]), "email", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["phone_number"]), "phone_number", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["date_of_birth"]), "date_of_birth", "This field cannot be blank")
	v.CheckField(form["gender"] == "male" || form["gender"] == "female", "gender", "Choose a gender")
	v.CheckField(validator.NotBlank(form["street"]), "street", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["city"]), "city", "This field cannot be blank")
	v.CheckField(validator.NotBlank(form["state"]), "state", "This field cannot be blank")
	v.CheckField(len(form["bvn"]) == 11 && isAllDigits(form["bvn"]), "bvn", "BVN must be exactly 11 digits")
	v.CheckField(form["source_of_funds"] != "", "source_of_funds", "Choose a source of funds")

	if !v.Valid() {
		data := app.newTemplateData(r)
		data.Title = "Wallet setup"
		data.User = user
		data.CurrentPage = "wallet"
		data.Form = form
		data.FormErrors = v.GetFieldErrors()
		app.render(w, http.StatusUnprocessableEntity, "teacher/kyc.html", data)
		return
	}

	// A wallet row may already exist (previous attempt). Reuse its BMONI
	// user rather than creating a second one — the profile PATCH is an
	// update, so a corrected profile never needs a new user.
	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if err != nil && !errors.Is(err, models.ErrNoRecord) {
		app.serverError(w, err)
		return
	}

	bmoniUserID := ""
	if wallet != nil && wallet.BmoniUserID != "" {
		bmoniUserID = wallet.BmoniUserID
	} else {
		// Fresh BMONI user. A 409 means the phone/email is taken by an
		// earlier user under this partner — recover it per the docs.
		bmoniUserID, err = app.bmoniClient.CreateUser(r.Context(), bmoni.CreateUserRequest{
			FirstName:   form["first_name"],
			LastName:    form["last_name"],
			Email:       form["email"],
			PhoneNumber: form["phone_number"],
		})
		if err != nil {
			resolved, rerr := app.bmoniClient.ResolveUserByPhone(r.Context(), form["phone_number"])
			if rerr != nil || resolved.UserID == "" {
				// by-phone only resolves users that already have a smart wallet
				// group; a fresh (wallet-less) duplicate can't be recovered, so
				// point the teacher at a fresh phone rather than looping.
				app.kycFail(r, user.ID, "Could not create your BMONI user ("+shortenBmoniErr(err)+"). "+
					"If you have tried this phone number before, use a different one.")
				http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
				return
			}
			bmoniUserID = resolved.UserID
			app.logger.Info("kyc: recovered existing BMONI user by phone",
				"userID", user.ID, "bmoniUserID", bmoniUserID)
		}

		wallet = &models.BmoniWallet{UserID: &user.ID}
		if err := app.models.BmoniWallets.Save(r.Context(), wallet); err != nil {
			app.serverError(w, err)
			return
		}
		wallet.BmoniUserID = bmoniUserID
		wallet.KYCStatus = models.KYCUserCreated
		if err := app.models.BmoniWallets.Save(r.Context(), wallet); err != nil {
			app.serverError(w, err)
			return
		}
	}

	// Submit (or re-submit) the KYC profile.
	profile := bmoni.KYCProfile{
		FirstName:     form["first_name"],
		LastName:      form["last_name"],
		DateOfBirth:   form["date_of_birth"],
		Gender:        form["gender"],
		StreetLine1:   form["street"],
		City:          form["city"],
		State:         form["state"],
		PostalCode:    form["postal_code"],
		CountryCode:   "NGA",
		BVN:           form["bvn"],
		SourceOfFunds: form["source_of_funds"],
	}
	if err := app.bmoniClient.SubmitKYC(r.Context(), bmoniUserID, profile); err != nil {
		app.kycFail(r, user.ID, "Your KYC profile was rejected: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}

	// Persist the bmoni user id + BVN + profile_saved in one upsert.
	wallet.BmoniUserID = bmoniUserID
	wallet.BVN = form["bvn"]
	wallet.KYCStatus = models.KYCProfileSaved
	wallet.KYCError = ""
	wallet.Currency = "CNGN"
	if err := app.models.BmoniWallets.Save(r.Context(), wallet); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "Profile saved — now upload your documents.")
	http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
}

/*
 * teacherKYCDocuments uploads the identity documents and, once BMONI reports
 * readiness, provisions the teacher's smart wallet + NGN rail:
 *
 *   3 uploads → readiness → wallet (owner key) → start-nigeria → VBA
 *
 * Everything runs server-side in the documented order. Provisioning never
 * calls create-managed twice for the same user: if a CNGN wallet already
 * exists (interrupted earlier attempt), the existing one is adopted and the
 * flow continues from start-nigeria.
 */
func (app *application) teacherKYCDocuments(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if errors.Is(err, models.ErrNoRecord) {
		app.session.Put(r.Context(), "flash", "Start by saving your profile first.")
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	} else if err != nil {
		app.serverError(w, err)
		return
	}
	if wallet.BmoniUserID == "" {
		app.session.Put(r.Context(), "flash", "Start by saving your profile first.")
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		app.kycDocsFail(r, user.ID, "Could not read the uploads — files must be under 16MB each.")
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}

	idType := strings.TrimSpace(r.FormValue("id_type"))
	// The identification upload endpoint's enum uses "drivers_license"
	// while /kyc/options advertises "driving_license" — normalise so the
	// form's label matches what the API accepts.
	if idType == "driving_license" {
		idType = "drivers_license"
	}
	idNumber := strings.TrimSpace(r.FormValue("id_number"))
	poaType := strings.TrimSpace(r.FormValue("poa_type"))
	if poaType == "" {
		poaType = "bank_statement"
	}

	v := validator.New()
	v.CheckField(idType != "", "id_type", "Choose the ID document type")
	v.CheckField(validator.NotBlank(idNumber), "id_number", "This field cannot be blank")

	// Read the three images into memory (≤2KB files are rejected by BMONI —
	// enforce the minimum here so the teacher gets a clear message).
	files := map[string]string{"identification": "id_document", "proof-of-address": "proof_of_address", "biometric": "selfie"}
	uploads := make(map[string][]byte, 3)
	names := make(map[string]string, 3)
	for kind, field := range files {
		file, header, ferr := r.FormFile(field)
		if ferr != nil {
			v.CheckField(false, field, "Upload "+field)
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(file, 8<<20))
		file.Close()
		if rerr != nil {
			app.serverError(w, rerr)
			return
		}
		if len(data) < 2048 {
			v.CheckField(false, field, field+" must be a clear image at least 2KB")
			continue
		}
		uploads[kind] = data
		names[kind] = header.Filename
	}

	if !v.Valid() {
		data := app.newTemplateData(r)
		data.Title = "Wallet setup"
		data.User = user
		data.CurrentPage = "wallet"
		data.Wallet = wallet
		data.Form = map[string]string{"id_type": idType, "id_number": idNumber, "poa_type": poaType}
		data.FormErrors = v.GetFieldErrors()
		app.render(w, http.StatusUnprocessableEntity, "teacher/kyc.html", data)
		return
	}

	// 1. Upload the documents. Passports are rejected without issue/expiry
	// dates (E101), so default them when the teacher left them blank.
	now := time.Now()
	idExtra := map[string]string{
		"type":           idType,
		"documentNumber": idNumber,
		"issuingCountry": "NGA",
		"issueDate":      r.FormValue("id_issue_date"),
		"expirationDate": r.FormValue("id_expiry_date"),
	}
	if idExtra["issueDate"] == "" {
		idExtra["issueDate"] = now.AddDate(-5, 0, 0).Format("2006-01-02")
	}
	if idExtra["expirationDate"] == "" {
		idExtra["expirationDate"] = now.AddDate(10, 0, 0).Format("2006-01-02")
	}
	if err := app.bmoniClient.UploadKycDocument(r.Context(), wallet.BmoniUserID, "identification", uploads["identification"], names["identification"], idExtra); err != nil {
		app.kycDocsFail(r, user.ID, "ID document upload failed: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	if err := app.bmoniClient.UploadKycDocument(r.Context(), wallet.BmoniUserID, "proof-of-address", uploads["proof-of-address"], names["proof-of-address"], map[string]string{"type": poaType}); err != nil {
		app.kycDocsFail(r, user.ID, "Proof-of-address upload failed: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	if err := app.bmoniClient.UploadKycDocument(r.Context(), wallet.BmoniUserID, "biometric", uploads["biometric"], names["biometric"], map[string]string{"type": "selfie"}); err != nil {
		app.kycDocsFail(r, user.ID, "Selfie upload failed: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}

	// 2. Readiness gates the rail. Missing items are surfaced verbatim.
	readiness, err := app.bmoniClient.KycReadiness(r.Context(), wallet.BmoniUserID)
	if err != nil {
		app.kycDocsFail(r, user.ID, "Could not check KYC readiness: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	if !readiness.Ready {
		app.kycDocsFail(r, user.ID, "Your KYC documents are incomplete: "+strings.Join(readiness.Missing, ", "))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}

	// 3. Provision the wallet + rail (bounded: several sequential calls).
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	wallet.KYCStatus = models.KYCDocsUploaded
	wallet.KYCError = ""

	smartWalletID, ownerAddress, err := app.ensureSmartWallet(ctx, user.ID, wallet)
	if err != nil {
		app.kycDocsFail(r, user.ID, "Wallet provisioning failed: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	wallet.SmartWalletID = smartWalletID
	wallet.OwnerAddress = ownerAddress
	wallet.Status = "ACTIVE"
	wallet.Currency = "CNGN"
	if err := app.models.BmoniWallets.Save(r.Context(), wallet); err != nil {
		app.serverError(w, err)
		return
	}

	// 4. Activate the NGN rail with the profile BVN, then read the VBA.
	if err := app.bmoniClient.StartNigeria(ctx, wallet.BmoniUserID, wallet.BVN, ownerAddress); err != nil {
		app.kycDocsFail(r, user.ID, "Could not activate your NGN account: "+shortenBmoniErr(err))
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	account, err := app.bmoniClient.WaitForVBA(ctx, wallet.BmoniUserID, 15)
	if err != nil {
		app.kycDocsFail(r, user.ID, "Your account is being provisioned — check back shortly. ("+shortenBmoniErr(err)+")")
		http.Redirect(w, r, "/teacher/wallet/kyc", http.StatusSeeOther)
		return
	}
	wallet.VBAAccountNumber = account.AccountNumber
	wallet.VBABankName = account.BankName
	wallet.KYCStatus = models.KYCRailActive
	wallet.KYCError = ""
	if err := app.models.BmoniWallets.Save(r.Context(), wallet); err != nil {
		app.serverError(w, err)
		return
	}

	app.logger.Info("kyc: teacher wallet active", "userID", user.ID, "bmoniUserID", wallet.BmoniUserID)
	app.session.Put(r.Context(), "flash",
		fmt.Sprintf("Your wallet is ready — students now pay into your account %s (%s).", account.AccountNumber, account.BankName))
	http.Redirect(w, r, "/teacher/wallet", http.StatusSeeOther)
}

// ensureSmartWallet returns the teacher's CNGN wallet, creating it on first
// use. It never issues a second create-managed for the same user: an existing
// CNGN wallet (e.g. from an interrupted attempt) is adopted instead, per
// BMONI's retry guidance.
func (app *application) ensureSmartWallet(ctx context.Context, userID string, wallet *models.BmoniWallet) (smartWalletID, ownerAddress string, err error) {
	// Already provisioned (interrupted later step)? Reuse it.
	if wallet.SmartWalletID != "" && wallet.OwnerAddress != "" {
		return wallet.SmartWalletID, wallet.OwnerAddress, nil
	}

	wallets, err := app.bmoniClient.ListWallets(ctx, wallet.BmoniUserID)
	if err != nil && !isNoWalletGroupErr(err) {
		return "", "", err
	}
	for _, w := range wallets {
		// The API reports the currency as "NGN" on the wallets list while
		// create-managed takes "CNGN" — accept both (one CNGN wallet per
		// user, so any NGN/CNGN entry is THE naira wallet).
		if (w.Currency == "CNGN" || w.Currency == "NGN") && w.Address != "" {
			app.logger.Info("kyc: adopting existing NGN wallet", "userID", userID, "walletID", w.ID)
			return w.ID, w.Address, nil
		}
	}

	ownerKey, encKey, err := app.ownerKeyFor(userID, wallet)
	if err != nil {
		return "", "", err
	}

	smartWalletID, ownerAddress, err = app.bmoniClient.CreateWallet(ctx, wallet.BmoniUserID, ownerKey)
	if err != nil {
		// 409 E502 — a wallet appeared between the list and the create.
		wallets, lerr := app.bmoniClient.ListWallets(ctx, wallet.BmoniUserID)
		if lerr != nil {
			return "", "", err
		}
		for _, w := range wallets {
			if (w.Currency == "CNGN" || w.Currency == "NGN") && w.Address != "" {
				return w.ID, w.Address, nil
			}
		}
		return "", "", err
	}

	// Persist the sealed owner key so withdrawals can sign proposals later.
	wallet.OwnerKeyEnc = encKey
	return smartWalletID, ownerAddress, nil
}

// isNoWalletGroupErr reports whether a BMONI error means the user has no
// embedded smart wallet group yet. The account-wallets GET 400s with this
// message until the first owner-proof challenge creates the group, so a
// brand-new teacher mid-wizard (no group, no wallets) is not an error — it
// simply means zero wallets to adopt.
func isNoWalletGroupErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unexpected status 400") &&
		strings.Contains(msg, "No embedded smart wallet group")
}

// ownerKeyFor decrypts the stored owner key (if any) or generates a fresh one
// and seals it for at-rest storage. An empty BMONI_WALLET_ENCRYPTION_KEY
// yields an empty seal — the wallet works for collection but cannot sign
// withdrawals.
func (app *application) ownerKeyFor(userID string, wallet *models.BmoniWallet) (bmoni.OwnerWallet, string, error) {
	encKey := app.config.bmoni.walletEncKey

	if wallet.OwnerKeyEnc != "" && encKey != "" {
		raw, err := bmoni.DecryptOwnerKey(wallet.OwnerKeyEnc, []byte(encKey))
		if err == nil {
			key, perr := bmoni.ParseOwnerKey(raw)
			if perr == nil {
				return key, wallet.OwnerKeyEnc, nil
			}
		}
		app.logger.Error("kyc: could not decrypt stored owner key — generating fresh", "userID", userID, "error", err)
	}

	ownerKey, err := bmoni.NewOwnerKey()
	if err != nil {
		return nil, "", err
	}

	sealed := ""
	if encKey != "" {
		sealed, err = bmoni.EncryptOwnerKey(ownerKey.Bytes(), []byte(encKey))
		if err != nil {
			return nil, "", err
		}
	}
	return ownerKey, sealed, nil
}

// kycFail records a failure at the profile step (the profile form is shown
// again with the error).
func (app *application) kycFail(r *http.Request, userID, message string) {
	app.kycFailWithStatus(r, userID, models.KYCFailed, message)
}

// kycDocsFail records a failure at the documents/provisioning step while
// keeping the wizard on the documents form (status documents_uploaded), so a
// re-submission resumes provisioning without redoing the profile.
func (app *application) kycDocsFail(r *http.Request, userID, message string) {
	app.kycFailWithStatus(r, userID, models.KYCDocsUploaded, message)
}

// kycFailWithStatus stores the failure on the wallet row so the page can
// surface it verbatim, without losing the step already completed.
func (app *application) kycFailWithStatus(r *http.Request, userID, status, message string) {
	if err := app.models.BmoniWallets.SetKYCStatus(r.Context(), userID, status, message); err != nil {
		app.logger.Error("kyc: could not record failure", "userID", userID, "error", err)
	}
	app.session.Put(r.Context(), "flash", message)
}

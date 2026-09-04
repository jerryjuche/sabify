package main

/*
 * End-to-end test for the per-teacher KYC wizard.
 *
 * A teacher registers, walks the in-app wizard (profile → documents), and the
 * app provisions her own BMONI wallet + NGN VBA. BMONI is stubbed with a tiny
 * HTTP server implementing the endpoints the wizard calls, so the state
 * machine (routes → handlers → models → BMONI client) runs end-to-end without
 * the sandbox.
 *
 * Run:  go test ./cmd/web/ -run TestTeacherKYCWizardE2E -v
 */

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sabify/internal/bmoni"
	"sabify/internal/models"
)

// e2eEncKey is the AES-256 key the test app seals teacher owner keys with.
const e2eEncKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// stubBmoniServer answers the BMONI calls the wizard makes. Each scenario
// endpoint returns a canned response; the test asserts the state the app
// reaches, not BMONI's internals.
func stubBmoniServer() *httptest.Server {
	mux := http.NewServeMux()

	// Create the BMONI user (profile step). Mirrors the live API shape:
	// the user is nested under `user` with `bmoniUserId` as the id the
	// path-scoped endpoints accept.
	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"user": map[string]string{"id": "kyc-user-1", "bmoniUserId": "kyc-user-1"},
		})
	})
	// Save the KYC profile (PATCH per the API).
	mux.HandleFunc("PATCH /v1/users/kyc-user-1/kyc", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"saved": true})
	})
	// Document uploads (identification | proof-of-address | biometric).
	mux.HandleFunc("POST /v1/users/kyc-user-1/kyc/documents/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"uploaded": true})
	})
	// Readiness gate.
	mux.HandleFunc("GET /v1/users/kyc-user-1/kyc/readiness", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"ready": true, "missing": []string{}})
	})
	// No wallet yet → the wizard must create one.
	mux.HandleFunc("GET /v1/users/kyc-user-1/smart-wallets/account/wallets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []interface{}{})
	})
	// Owner-proof challenge + create-managed.
	mux.HandleFunc("POST /v1/users/kyc-user-1/smart-wallets/owner-proof-challenges", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"challengeId": "challenge-1", "message": "Sabify KYC wizard e2e challenge"})
	})
	mux.HandleFunc("POST /v1/users/kyc-user-1/smart-wallets/create-managed", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"id": "wallet-kyc-1", "walletAddress": "0x00000000000000000000000000000000000abc1"})
	})
	// NGN rail + VBA.
	mux.HandleFunc("POST /v1/users/kyc-user-1/onboarding/start-nigeria", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/users/kyc-user-1/bank-accounts/deposit-accounts/NGN", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"accounts": []map[string]string{{"accountNumber": "7101234567", "bankName": "Kuda Bank"}},
		})
	})	// Balance read on the wallet page after activation.
	mux.HandleFunc("GET /v1/users/kyc-user-1/smart-wallets/account/balances", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"balances": []map[string]string{{ "currency": "NGN", "balance": "0.00" }},
		})
	})
	// Transaction history on the wallet page after activation (empty wallet).
	mux.HandleFunc("GET /v1/users/kyc-user-1/smart-wallets/wallet-kyc-1/transactions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"data": map[string]interface{}{"transactions": []interface{}{}},
		})
	})

	srv := httptest.NewServer(mux)
	return srv
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestTeacherKYCWizardE2E(t *testing.T) {
	pool := startTestPostgres(t)
	applyMigrations(t, pool)

	stub := stubBmoniServer()
	defer stub.Close()

	app := newE2EApp(t, pool)
	app.bmoniClient = bmoni.NewClient(stub.URL, "test-api-key")
	app.config.bmoni.walletEncKey = e2eEncKey
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	teacher := newSessionClient(t)
	register(t, teacher, srv.URL, "Bunch Dillon", "wizard@example.com", "teacher")
	login(t, teacher, srv.URL, "wizard@example.com")

	// ---------------------------------------------------------------------
	// 1. The wizard page starts at Step 1 (profile) with no wallet row.
	// ---------------------------------------------------------------------
	_, body := get(t, teacher, srv.URL, "/teacher/wallet/kyc")
	if !bytes.Contains(body, []byte("Step 1 · Your profile")) {
		t.Fatalf("wizard page missing profile form\n%s", truncate(body))
	}
	if bytes.Contains(body, []byte("Step 2 · Identity documents")) {
		t.Fatalf("wizard page shows documents form before any profile is saved\n%s", truncate(body))
	}

	// ---------------------------------------------------------------------
	// 2. Submit the profile → BMONI user created + profile saved.
	// ---------------------------------------------------------------------
	form := url.Values{
		"first_name":      {"Bunch"},
		"last_name":       {"Dillon"},
		"email":           {"wizard@example.com"},
		"phone_number":    {"+2348111111111"},
		"date_of_birth":   {"1990-01-01"},
		"gender":          {"male"},
		"street":          {"15 Admiralty Way"},
		"city":            {"Lagos"},
		"state":           {"Lagos"},
		"postal_code":     {"101241"},
		"bvn":             {"95888168924"},
		"source_of_funds": {"salary"},
	}
	resp, body := postForm(t, teacher, srv.URL, "/teacher/wallet/kyc/profile", form)
	requireStatus(t, resp, http.StatusSeeOther, "profile submit redirect")
	if loc := resp.Header.Get("Location"); loc != "/teacher/wallet/kyc" {
		t.Fatalf("profile submit redirected to %q\n%s", loc, truncate(body))
	}

	// 3. The wallet row now exists and the page offers Step 2.
	teacherRec, err := app.models.Users.FindByEmail(context.Background(), "wizard@example.com")
	if err != nil {
		t.Fatalf("load teacher: %v", err)
	}
	wallet, err := app.models.BmoniWallets.GetByUserID(context.Background(), teacherRec.ID)
	if err != nil {
		t.Fatalf("wallet row after profile: %v", err)
	}
	if wallet.BmoniUserID != "kyc-user-1" {
		t.Errorf("bmoni_user_id = %q, want kyc-user-1", wallet.BmoniUserID)
	}
	if wallet.KYCStatus != models.KYCProfileSaved {
		t.Errorf("kyc_status after profile = %q, want profile_saved", wallet.KYCStatus)
	}

	_, body = get(t, teacher, srv.URL, "/teacher/wallet/kyc")
	if !bytes.Contains(body, []byte("Step 2 · Identity documents")) {
		t.Fatalf("wizard page missing documents form after profile\n%s", truncate(body))
	}

	// ---------------------------------------------------------------------
	// 4. Submit the documents (3 images ≥2KB) → wallet + VBA provisioned.
	// ---------------------------------------------------------------------
	resp, body = postKYCUploads(t, teacher, srv.URL)
	requireStatus(t, resp, http.StatusSeeOther, "documents submit redirect")
	if loc := resp.Header.Get("Location"); loc != "/teacher/wallet" {
		t.Fatalf("documents submit redirected to %q, want /teacher/wallet\n%s", loc, truncate(body))
	}

	wallet, err = app.models.BmoniWallets.GetByUserID(context.Background(), teacherRec.ID)
	if err != nil {
		t.Fatalf("wallet row after documents: %v", err)
	}
	if wallet.KYCStatus != models.KYCRailActive {
		t.Fatalf("kyc_status after documents = %q, want rail_active (kyc_error: %s)", wallet.KYCStatus, wallet.KYCError)
	}
	if wallet.VBAAccountNumber != "7101234567" {
		t.Errorf("vba_account_number = %q, want 7101234567", wallet.VBAAccountNumber)
	}
	if wallet.SmartWalletID != "wallet-kyc-1" {
		t.Errorf("smart_wallet_id = %q, want wallet-kyc-1", wallet.SmartWalletID)
	}
	if wallet.OwnerKeyEnc == "" {
		t.Error("owner key was not sealed onto the wallet row")
	}

	// 5. The wallet page shows the teacher's account, activity and the
	//    withdraw form.
	_, body = get(t, teacher, srv.URL, "/teacher/wallet")
	for _, want := range []string{"7101234567", "Kuda Bank", "Recent activity", "No transactions yet", "Withdraw to a Nigerian bank", "Set up your wallet to receive payments"} {
		if want == "Set up your wallet to receive payments" {
			if bytes.Contains(body, []byte(want)) {
				t.Errorf("wallet page still shows the setup CTA after activation\n%s", truncate(body))
			}
			continue
		}
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("wallet page missing %q\n%s", want, truncate(body))
		}
	}

	// 6. The wizard page now shows the success panel, not the forms.
	_, body = get(t, teacher, srv.URL, "/teacher/wallet/kyc")
	if !bytes.Contains(body, []byte("Your wallet is ready")) {
		t.Errorf("kyc page missing success panel\n%s", truncate(body))
	}
}

// postKYCUploads posts three >2KB dummy images through the real multipart
// route the browser form uses.
func postKYCUploads(t *testing.T, client *http.Client, baseURL string) (*http.Response, []byte) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	writeFilePart := func(name, filename string) {
		t.Helper()
		part, err := w.CreateFormFile(name, filename)
		if err != nil {
			t.Fatalf("multipart field %s: %v", name, err)
		}
		// Random-looking bytes >2KB (BMONI rejects smaller files; the handler
		// enforces the same minimum).
		part.Write(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 1200))
	}
	writeFilePart("id_document", "id.png")
	writeFilePart("proof_of_address", "poa.png")
	writeFilePart("selfie", "selfie.png")
	for k, v := range map[string]string{
		"id_type": "passport", "id_number": "A01234567", "poa_type": "bank_statement",
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("multipart field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/teacher/wallet/kyc/documents", body)
	if err != nil {
		t.Fatalf("build documents POST: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("documents POST: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}

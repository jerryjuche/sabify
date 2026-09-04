package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/go-chi/chi/v5"

	"sabify/internal/bmoni"
	"sabify/internal/models"
)

/*
 * bmoniWebhook receives BMONI's server-to-server event deliveries.
 *
 * Registered outside the auth groups (public), it:
 *  1. reads the RAW body before any parsing (the HMAC is over the exact bytes),
 *  2. verifies X-Webhook-Signature when BMONI_WEBHOOK_SECRET is configured,
 *  3. de-duplicates on the stable event id via the webhook_events ledger, and
 *  4. resolves employee.deposit.completed into a PAID payment + ACTIVE access.
 *
 * Retry semantics (from BMONI's docs): 2xx = delivered, 5xx = retried, any
 * other 4xx = discarded forever. We therefore return 5xx on processing
 * failures and never 4xx for internal errors. The work here is a handful of
 * fast DB statements, comfortably inside BMONI's 10s delivery timeout, so we
 * process synchronously rather than acknowledging in a goroutine: a failure
 * then returns 500, BMONI retries, and — because the dedupe row is only
 * written after success — the retry actually re-processes.
 */

type bmoniWebhookEvent struct {
	ID        string `json:"id"`
	EventType string `json:"eventType"`
	Payload   struct {
		UserID string `json:"userId"`
		Amount string `json:"amount"`
	} `json:"payload"`
	Timestamp string `json:"timestamp"`
}

func (app *application) bmoniWebhook(w http.ResponseWriter, r *http.Request) {
	// 1. Capture the raw body — signature verification must run over the
	// exact bytes received, not a re-serialized payload.
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		app.logger.Error("bmoni webhook: read body", "error", err)
		app.clientError(w, http.StatusBadRequest)
		return
	}

	// 2. Verify the HMAC when a secret is configured. When it is not set the
	// webhook is open (dev mode) — log loudly so nobody ships that state.
	secret := app.config.bmoni.webhookSecret
	if secret == "" {
		app.logger.Warn("bmoni webhook: BMONI_WEBHOOK_SECRET is not set — accepting unverified deliveries (dev only)")
	} else {
		signature := r.Header.Get("X-Webhook-Signature")
		if !bmoni.VerifyWebhookSignature(rawBody, signature, secret) {
			app.logger.Warn("bmoni webhook: rejected delivery with invalid signature",
				"x-webhook-id", r.Header.Get("X-Webhook-Id"))
			app.clientError(w, http.StatusUnauthorized)
			return
		}
	}

	var event bmoniWebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		app.logger.Error("bmoni webhook: decode payload", "error", err)
		app.clientError(w, http.StatusBadRequest)
		return
	}

	if event.ID == "" {
		app.logger.Warn("bmoni webhook: delivery without event id; dedupe disabled for this event",
			"eventType", event.EventType)
	}

	// 3. Only deposits unlock courses today. All other event types are
	// recorded (for audit) and acknowledged.
	if event.EventType != "employee.deposit.completed" {
		if event.ID != "" {
			_, _ = app.models.WebhookEvents.InsertIgnore(r.Context(), event.ID, event.EventType, rawBody)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// 4. Resolve the deposit into a payment. Errors return 500 so BMONI
	// retries the delivery.
	if err := app.processBmoniDeposit(r, event, rawBody); err != nil {
		app.logger.Error("bmoni webhook: processing failed (will be retried by BMONI)",
			"eventType", event.EventType, "eventID", event.ID, "error", err)
		app.serverError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// processBmoniDeposit matches a completed deposit to the wallet that received
// it and unlocks the course. Under the per-teacher model the deposit's BMONI
// userId resolves to a local teacher's wallet row; pending payments are then
// matched among THAT teacher's courses, so deposits can never be mis-credited
// across teachers. A legacy platform row (user_id NULL) still resolves any
// pending payment, preserving pre-per-teacher behaviour. The dedupe ledger
// row is written only after successful processing so that a failure (which
// yields a 500, prompting a BMONI retry) is not swallowed by dedupe.
func (app *application) processBmoniDeposit(r *http.Request, event bmoniWebhookEvent, rawBody []byte) error {
	ctx := r.Context()

	// Resolve the receiving wallet from the deposit's BMONI user id. When the
	// payload carries no user id (older deliveries) fall back to the legacy
	// platform wallet.
	var wallet *models.BmoniWallet
	if event.Payload.UserID != "" {
		resolved, err := app.models.BmoniWallets.GetByBmoniUserID(ctx, event.Payload.UserID)
		if errors.Is(err, models.ErrNoRecord) {
			app.logger.Warn("bmoni webhook: deposit for unknown BMONI user ignored",
				"eventID", event.ID, "userId", event.Payload.UserID)
			return app.recordWebhookEvent(ctx, event, rawBody)
		} else if err != nil {
			return fmt.Errorf("load wallet by bmoni user: %w", err)
		}
		wallet = resolved
	} else {
		platform, err := app.models.BmoniWallets.GetPlatform(ctx)
		if errors.Is(err, models.ErrNoRecord) {
			app.logger.Warn("bmoni webhook: deposit received but no wallet provisioned",
				"eventID", event.ID, "userId", event.Payload.UserID)
			return app.recordWebhookEvent(ctx, event, rawBody)
		} else if err != nil {
			return fmt.Errorf("load platform wallet: %w", err)
		}
		wallet = platform
	}

	// Restrict matching to the owning teacher's courses; legacy platform rows
	// (no teacher) match any course.
	teacherID := ""
	if wallet.UserID != nil {
		teacherID = *wallet.UserID
	}

	// Find the oldest unresolved (PENDING payment + PENDING course access)
	// among that teacher's courses.
	payment, err := app.models.Payments.FindPendingForDeposit(ctx, teacherID)
	if err != nil {
		return fmt.Errorf("find pending payment: %w", err)
	}
	if payment == nil {
		app.logger.Info("bmoni webhook: deposit received but no pending payment to match",
			"eventID", event.ID, "amount", event.Payload.Amount)
		return app.recordWebhookEvent(ctx, event, rawBody)
	}

	// The deposit must match the pending payment's amount (kobo). This stops
	// unrelated wallet funding (e.g. sandbox test credits) or under/over
	// payments from unlocking a course.
	depositKobo, err := nairaToKobo(event.Payload.Amount)
	if err != nil {
		app.logger.Warn("bmoni webhook: unparseable deposit amount",
			"eventID", event.ID, "amount", event.Payload.Amount, "error", err)
		return app.recordWebhookEvent(ctx, event, rawBody)
	}
	if depositKobo != payment.AmountKobo {
		app.logger.Warn("bmoni webhook: deposit amount does not match pending payment — leaving locked",
			"eventID", event.ID, "depositKobo", depositKobo, "paymentKobo", payment.AmountKobo,
			"paymentID", payment.ID)
		return app.recordWebhookEvent(ctx, event, rawBody)
	}

	// Amount matched: mark paid, unlock access, and record the enrollment so
	// the student's course list shows it immediately.
	if err := app.models.Payments.MarkPaid(ctx, payment.ID, event.ID); err != nil {
		return fmt.Errorf("mark payment paid: %w", err)
	}
	if err := app.models.CourseAccess.SetActive(ctx, payment.StudentID, payment.CourseID, payment.ID); err != nil {
		return fmt.Errorf("activate course access: %w", err)
	}
	if err := app.models.Enrollments.Insert(ctx, payment.CourseID, payment.StudentID); err != nil {
		return fmt.Errorf("record enrollment: %w", err)
	}

	app.logger.Info("bmoni webhook: payment confirmed, course unlocked",
		"eventID", event.ID, "paymentID", payment.ID,
		"studentID", payment.StudentID, "courseID", payment.CourseID,
		"amountKobo", payment.AmountKobo)

	return app.recordWebhookEvent(ctx, event, rawBody)
}

// recordWebhookEvent persists the delivery for idempotency/audit.
func (app *application) recordWebhookEvent(ctx context.Context, event bmoniWebhookEvent, rawBody []byte) error {
	if event.ID == "" {
		return nil
	}
	_, err := app.models.WebhookEvents.InsertIgnore(ctx, event.ID, event.EventType, rawBody)
	return err
}

// nairaToKobo converts a BMONI decimal NGN amount (e.g. "2500.00") to kobo.
func nairaToKobo(naira string) (int64, error) {
	naira = strings.TrimSpace(naira)
	if naira == "" {
		return 0, errors.New("empty amount")
	}
	amount, err := strconv.ParseFloat(naira, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(amount * 100)), nil
}

/*
 * studentPay renders the deposit screen for an in-flight payment: the platform
 * VBA number, bank, exact amount, and the narration reference the student
 * should include in their transfer. Payment rows are owned by the student, so
 * another student's payment id yields a 404.
 */

func (app *application) studentPay(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	payment, err := app.models.Payments.FindByID(r.Context(), chi.URLParam(r, "paymentId"))
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	if payment.StudentID != user.ID {
		app.notFound(w)
		return
	}

	course, err := app.models.Courses.FindByIDWithTeacher(r.Context(), payment.CourseID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	// The deposit account is the course teacher's own wallet. A teacher who
	// has not completed the KYC wizard has no wallet yet — the template
	// renders a friendly "payments not activated" notice in that case.
	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), course.TeacherID)
	if err != nil && !errors.Is(err, models.ErrNoRecord) {
		app.serverError(w, err)
		return
	}
	if errors.Is(err, models.ErrNoRecord) {
		wallet = nil
	}

	data := app.newTemplateData(r)
	data.Title = "Pay for " + course.Title
	data.User = user
	data.CurrentPage = "courses"
	data.Payment = payment
	data.PaymentReference = payment.Reference
	data.Wallet = wallet
	data.Course = course
	data.CoursePriceNaira = payment.AmountKobo / 100

	app.render(w, http.StatusOK, "student/pay.html", data)
}

/*
 * studentPayStatus is the poll target for ui/static/js/pay-status.js. It
 * returns the payment's current status as JSON so the payment page can flip
 * to the confirmed state the moment the webhook unlocks the course.
 */

func (app *application) studentPayStatus(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	payment, err := app.models.Payments.FindByID(r.Context(), chi.URLParam(r, "paymentId"))
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	if payment.StudentID != user.ID {
		app.notFound(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  payment.Status,
		"paid_at": payment.PaidAt,
	})
}

/*
 * teacherWallet shows a teacher:
 *   - confirmed earnings (sum of PAID fees across their courses, from the DB),
 *   - the platform deposit account students pay into,
 *   - the LIVE CNGN balance of the platform wallet, read from BMONI
 *     (distinct from earnings: this is the actual money in the wallet).
 *
 * The live balance call is best-effort: a missing/unprovisioned wallet or a
 * BMONI outage degrades to "unavailable" rather than failing the page.
 */

func (app *application) teacherWallet(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	earningsKobo, err := app.models.Payments.SumPaidByTeacher(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	// The teacher's OWN wallet: her students pay into her VBA and she
	// withdraws with her own owner key. No wallet yet means she has not
	// completed the in-app KYC wizard.
	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if err != nil && !errors.Is(err, models.ErrNoRecord) {
		app.serverError(w, err)
		return
	}
	if errors.Is(err, models.ErrNoRecord) {
		wallet = nil
	}

	data := app.newTemplateData(r)
	data.Title = "Wallet"
	data.User = user
	data.CurrentPage = "wallet"
	data.TeacherEarnings = earningsKobo / 100
	data.Wallet = wallet

	if wallet != nil && wallet.BmoniUserID != "" {
		balances, err := app.bmoniClient.Balances(r.Context(), wallet.BmoniUserID)
		if err != nil {
			app.logger.Warn("bmoni: live balance unavailable", "userID", wallet.BmoniUserID, "error", err)
		} else {
			for _, b := range balances {
				// The API reports the currency as "NGN" on the balances endpoint
				// while the stablecoin is "CNGN" elsewhere — accept both. The
				// amount field is "balance" on this endpoint ("amount" elsewhere).
				if b.Currency == "CNGN" || b.Currency == "NGN" {
					amount := b.Value
					if amount == "" {
						amount = b.Amount
					}
					if kobo, err := nairaToKobo(amount); err == nil {
						data.BmoniBalanceNaira = kobo / 100
						data.BmoniBalanceSet = true
					}
				}
			}
		}

		// Transaction history — best-effort like the balance: an outage or
		// missing wallet degrades to an empty list, never a broken page.
		if wallet.SmartWalletID != "" {
			txns, txErr := app.bmoniClient.WalletTransactions(r.Context(), wallet.BmoniUserID, wallet.SmartWalletID)
			if txErr != nil {
				app.logger.Warn("bmoni: wallet transactions unavailable", "userID", wallet.BmoniUserID, "error", txErr)
			} else {
				data.WalletTxns = txns
			}
		}
	}

	app.render(w, http.StatusOK, "teacher/wallet.html", data)
}

/*
 * teacherWalletBanks serves the BMONI-supported Nigerian bank list for the
 * logged-in teacher's wallet, as JSON. The withdraw form populates its bank
 * selector from this endpoint so teachers never have to guess codes (the
 * sandbox uses codes that differ from CBN's, e.g. GTBank = 000013 there).
 */

func (app *application) teacherWalletBanks(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}
	if wallet.BmoniUserID == "" || wallet.KYCStatus != models.KYCRailActive {
		app.notFound(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	banks, err := app.bmoniClient.NigerianBanks(ctx, wallet.BmoniUserID)
	if err != nil {
		app.logger.Warn("bmoni: bank list unavailable", "userID", wallet.BmoniUserID, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "Bank list unavailable right now."})
		return
	}

	type bankDTO struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	out := make([]bankDTO, 0, len(banks))
	for _, b := range banks {
		out = append(out, bankDTO{Name: b.Name, Code: b.Code})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"banks": out})
}

/*
 * teacherWalletWithdraw runs the full BMONI payout path for the platform
 * wallet: verify → register → offramp proposal → approve → sign → submit,
 * then reports the terminal proposal status. Every step surfaces a readable
 * flash error instead of a stack trace, and the whole flow is bounded by a
 * context timeout so the request cannot hang.
 */

func (app *application) teacherWalletWithdraw(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	amountNaira := strings.TrimSpace(r.Form.Get("amount"))
	accountNumber := strings.TrimSpace(r.Form.Get("account_number"))
	bankCode := strings.TrimSpace(r.Form.Get("bank_code"))

	flash := func(msg string) {
		app.session.Put(r.Context(), "flash", msg)
		http.Redirect(w, r, "/teacher/wallet", http.StatusSeeOther)
	}

	amount, err := strconv.ParseInt(amountNaira, 10, 64)
	if err != nil || amount <= 0 {
		flash("Enter a whole number of Naira to withdraw.")
		return
	}
	if len(accountNumber) != 10 || !isAllDigits(accountNumber) {
		flash("Account number must be exactly 10 digits.")
		return
	}
	if bankCode == "" {
		flash("Select a bank from the supported list.")
		return
	}

	wallet, err := app.models.BmoniWallets.GetByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			flash("Set up your wallet first: complete the KYC wizard so your students have an account to pay into.")
		} else {
			app.serverError(w, err)
		}
		return
	}
	if wallet.KYCStatus != models.KYCRailActive {
		flash("Your wallet is not active yet — finish the KYC wizard before withdrawing.")
		return
	}

	// Signing the offramp proposal requires the owner key; it is only stored
	// when the bootstrap tool ran with BMONI_WALLET_ENCRYPTION_KEY set.
	encKey := app.config.bmoni.walletEncKey
	if wallet.OwnerKeyEnc == "" || encKey == "" {
		flash("Withdrawals are unavailable: your wallet's signing key is not stored. Ask an operator to set BMONI_WALLET_ENCRYPTION_KEY and re-run your KYC setup.")
		return
	}
	rawKey, err := bmoni.DecryptOwnerKey(wallet.OwnerKeyEnc, []byte(encKey))
	if err != nil {
		app.logger.Error("bmoni: decrypt owner key", "error", err)
		flash("Could not unlock the wallet signing key. Check BMONI_WALLET_ENCRYPTION_KEY.")
		return
	}
	ownerKey, err := bmoni.ParseOwnerKey(rawKey)
	if err != nil {
		app.logger.Error("bmoni: parse owner key", "error", err)
		flash("Stored signing key is corrupt.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	// 1. Resolve the bank name for the CBN code.
	banks, err := app.bmoniClient.NigerianBanks(ctx, wallet.BmoniUserID)
	if err != nil {
		app.logger.Warn("bmoni: bank list", "error", err)
		flash("Could not load the bank list from BMONI.")
		return
	}
	bankName := ""
	for _, b := range banks {
		if b.Code == bankCode {
			bankName = b.Name
			break
		}
	}
	if bankName == "" {
		flash("That bank code was not found in BMONI's supported list.")
		return
	}

	// 2. Verify the account resolves to a holder name.
	verified, err := app.bmoniClient.VerifyNigerianAccount(ctx, wallet.BmoniUserID, accountNumber, bankCode)
	if err != nil {
		flash(fmt.Sprintf("Bank account could not be verified (%s). Check the number and bank code.", shortenBmoniErr(err)))
		return
	}

	// 3. Register the withdrawal account.
	bankAccountID, err := app.bmoniClient.RegisterWithdrawalAccount(ctx, wallet.BmoniUserID, verified, accountNumber, bankCode, bankName)
	if err != nil {
		flash(fmt.Sprintf("Could not register the withdrawal account (%s).", shortenBmoniErr(err)))
		return
	}

	// 4. Create the offramp proposal (nothing moves yet).
	fromAmount := fmt.Sprintf("%d.00", amount)
	proposalID, err := app.bmoniClient.CreateOfframp(ctx, wallet.BmoniUserID, wallet.SmartWalletID, bankAccountID, fromAmount)
	if err != nil {
		flash(fmt.Sprintf("Payout could not be started (%s).", shortenBmoniErr(err)))
		return
	}

	// 5. Approve, then fetch the signing digest (available once the proposal
	// reaches PENDING_SIGNATURES — poll briefly rather than calling once).
	if err := app.bmoniClient.ApproveProposal(ctx, wallet.BmoniUserID, proposalID); err != nil {
		flash(fmt.Sprintf("Proposal created (%s) but approval failed (%s).", proposalID, shortenBmoniErr(err)))
		return
	}

	var payload bmoni.ProposalSignPayload
	for i := 0; i < 6; i++ {
		payload, err = app.bmoniClient.SignPayload(ctx, wallet.BmoniUserID, proposalID)
		if err == nil && payload.HashToSign != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil || payload.HashToSign == "" {
		flash(fmt.Sprintf("Proposal %s is pending approvals; no signing digest yet. Try again in a moment.", proposalID))
		return
	}

	// 6. Sign the digest (raw hash, v = 27/28) and submit.
	digest, err := hexutil.Decode(payload.HashToSign)
	if err != nil {
		app.logger.Error("bmoni: decode signing digest", "error", err)
		flash("Could not decode the signing digest.")
		return
	}
	signature, err := ownerKey.SignDigest(digest)
	if err != nil {
		app.logger.Error("bmoni: sign proposal", "error", err)
		flash("Could not sign the payout.")
		return
	}
	if err := app.bmoniClient.SubmitProposalSignature(ctx, wallet.BmoniUserID, proposalID, signature); err != nil {
		flash(fmt.Sprintf("Signature rejected (%s).", shortenBmoniErr(err)))
		return
	}

	// 7. Poll for the terminal status (COMPLETED / FAILED).
	status := "PENDING"
	for i := 0; i < 12; i++ {
		proposal, perr := app.bmoniClient.GetProposal(ctx, wallet.BmoniUserID, proposalID)
		if perr == nil && proposal.Status != "" {
			status = proposal.Status
			if status == "COMPLETED" || status == "FAILED" {
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	app.session.Put(r.Context(), "flash",
		fmt.Sprintf("Withdrawal of ₦%d submitted. Proposal %s · status: %s", amount, proposalID, status))
	http.Redirect(w, r, "/teacher/wallet", http.StatusSeeOther)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// shortenBmoniErr trims a BMONI error (which may include the full response
// body) to the first line so flash messages stay readable.
func shortenBmoniErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	return msg
}

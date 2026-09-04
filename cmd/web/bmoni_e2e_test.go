package main

/*
 * End-to-end test for the BMONI paid-enrollment flow.
 *
 * This walks the complete journey through the REAL HTTP stack (Chi router,
 * session middleware, auth handlers, models, and PostgreSQL) — no mocks:
 *
 *   register teacher + student → teacher prices a course → student enrolls →
 *   payment page renders the VBA/reference → a signed BMONI webhook lands →
 *   payment clears, access activates, the course unlocks, the poller reports
 *   PAID, and the teacher's wallet shows earnings.
 *
 * The test provisions a real PostgreSQL automatically via
 * github.com/fergusstrange/embedded-postgres (binaries are downloaded on
 * first run). On machines with an existing database, set TEST_DATABASE_URL
 * (e.g. postgres://user:pass@localhost:5432/sabify_test?sslmode=disable) and
 * the embedded instance is skipped. The schema is rebuilt from
 * migrations/*.sql on every run, so the test is repeatable.
 *
 * Run:  go test ./cmd/web/ -run TestBmoniPaidEnrollmentE2E -v
 */

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"sabify/internal/ai"
	"sabify/internal/bmoni"
	"sabify/internal/models"
)

// e2eWebhookSecret is the HMAC secret the test app is configured with; the
// simulated deliveries below are signed with it.
const e2eWebhookSecret = "e2e-test-secret-0123456789abcdef0123456789abcdef"

// platformWallet mirrors what tools/bmoni-bootstrap persists: the platform
// BMONI user id, its NGN VBA, and bank. Inserted directly so the flow does
// not depend on the sandbox being reachable from CI.
const (
	e2ePlatformUserID  = "bmoni-platform-user-e2e"
	e2ePlatformVBANum  = "9912345678"
	e2ePlatformVBABank = "Wema Bank"
)

func TestBmoniPaidEnrollmentE2E(t *testing.T) {
	pool := startTestPostgres(t)
	applyMigrations(t, pool)

	app := newE2EApp(t, pool)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	teacher := newSessionClient(t)
	student := newSessionClient(t)

	// ---------------------------------------------------------------------
	// 1. Register and log in a teacher and a student through the real auth
	//    flow (bcrypt hashing, sessions, role checks). A second teacher is
	//    used to prove deposits are never mis-credited across teachers.
	// ---------------------------------------------------------------------
	register(t, teacher, srv.URL, "Ada Teacher", "teacher@example.com", "teacher")
	register(t, student, srv.URL, "Bola Student", "student@example.com", "student")
	login(t, teacher, srv.URL, "teacher@example.com")
	login(t, student, srv.URL, "student@example.com")

	teacherRec, err := app.models.Users.FindByEmail(context.Background(), "teacher@example.com")
	if err != nil {
		t.Fatalf("load teacher: %v", err)
	}

	// Provision the teacher-owned wallet row (what the in-app KYC wizard
	// would save once rail_active). Students pay into THIS account.
	teacherID := teacherRec.ID
	if err := app.models.BmoniWallets.Save(context.Background(), &models.BmoniWallet{
		BmoniUserID:      e2ePlatformUserID,
		OwnerAddress:     "0x000000000000000000000000000000000000e2e",
		SmartWalletID:    "wallet-e2e-1",
		Currency:         "CNGN",
		Status:           "ACTIVE",
		VBAAccountNumber: e2ePlatformVBANum,
		VBABankName:      e2ePlatformVBABank,
		UserID:           &teacherID,
		KYCStatus:        models.KYCRailActive,
	}); err != nil {
		t.Fatalf("seed teacher wallet: %v", err)
	}

	// A second teacher whose deposits must never unlock the first teacher's
	// pending payments.
	register(t, teacher, srv.URL, "Chidi Teacher", "teacher2@example.com", "teacher")
	teacher2Rec, err := app.models.Users.FindByEmail(context.Background(), "teacher2@example.com")
	if err != nil {
		t.Fatalf("load teacher2: %v", err)
	}
	teacher2ID := teacher2Rec.ID
	if err := app.models.BmoniWallets.Save(context.Background(), &models.BmoniWallet{
		BmoniUserID:      "bmoni-teacher2-e2e",
		OwnerAddress:     "0x00000000000000000000000000000000000e2e2",
		SmartWalletID:    "wallet-e2e-2",
		Currency:         "CNGN",
		Status:           "ACTIVE",
		VBAAccountNumber: "9911223344",
		VBABankName:      "Wema Bank",
		UserID:           &teacher2ID,
		KYCStatus:        models.KYCRailActive,
	}); err != nil {
		t.Fatalf("seed teacher2 wallet: %v", err)
	}

	// ---------------------------------------------------------------------
	// 2. Teacher creates a paid course priced at ₦2,500 (250,000 kobo).
	// ---------------------------------------------------------------------
	courseID := createPaidCourse(t, teacher, srv.URL, "Intro to Go", 2500)

	// ---------------------------------------------------------------------
	// 3. Student enrolls → redirect to the payment page.
	// ---------------------------------------------------------------------
	resp, _ := postForm(t, student, srv.URL, "/student/courses/"+courseID+"/enroll", url.Values{})
	requireStatus(t, resp, http.StatusSeeOther, "enroll redirect")
	payPath := resp.Header.Get("Location")
	if !strings.HasPrefix(payPath, "/student/pay/") {
		t.Fatalf("enroll redirected to %q, want /student/pay/{id}", payPath)
	}
	paymentID := strings.TrimPrefix(payPath, "/student/pay/")

	// 4. The payment page shows the VBA, bank, exact amount and reference.
	_, body := get(t, student, srv.URL, payPath)
	for _, want := range []string{
		e2ePlatformVBANum,
		e2ePlatformVBABank,
		"₦2500",
		"SABIFY-",
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("payment page missing %q\n%s", want, truncate(body))
		}
	}

	// 5. Before the webhook lands, the DB is consistent: PENDING everywhere.
	assertPaymentStatus(t, app, paymentID, "PENDING")
	access := findAccess(t, app, courseID)
	if access == nil || access.Status != "PENDING" {
		t.Fatalf("course_access before webhook = %+v, want PENDING", access)
	}

	// ---------------------------------------------------------------------
	// 6. Negative webhook cases first — none of them may unlock the course.
	// ---------------------------------------------------------------------

	// 6a. Forged signature is rejected with 401.
	raw, _ := signedDelivery(e2eWebhookSecret, "evt-forged", "employee.deposit.completed", e2ePlatformUserID, "2500.00")
	req := newWebhookRequest(srv.URL, raw, "deadbeef")
	badResp := doRaw(t, req)
	requireStatus(t, badResp, http.StatusUnauthorized, "forged signature")
	if access = findAccess(t, app, courseID); access == nil || access.Status != "PENDING" {
		t.Fatalf("forged webhook changed access: %+v", access)
	}

	// 6b. Deposit for an unknown BMONI user is ignored.
	raw, sig := signedDelivery(e2eWebhookSecret, "evt-unknown-user", "employee.deposit.completed", "someone-else", "2500.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "unknown user deposit")
	if access = findAccess(t, app, courseID); access == nil || access.Status != "PENDING" {
		t.Fatalf("unknown-user deposit changed access: %+v", access)
	}

	// 6c. A deposit whose amount does not match the pending payment is left
	//     locked (this also stops sandbox test credits unlocking courses).
	raw, sig = signedDelivery(e2eWebhookSecret, "evt-wrong-amount", "employee.deposit.completed", e2ePlatformUserID, "100.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "wrong amount deposit")
	assertPaymentStatus(t, app, paymentID, "PENDING")
	if access = findAccess(t, app, courseID); access == nil || access.Status != "PENDING" {
		t.Fatalf("wrong-amount deposit changed access: %+v", access)
	}

	// 6d. A correctly-sized deposit into ANOTHER teacher's wallet must not
	//     clear this payment (per-teacher credit isolation).
	raw, sig = signedDelivery(e2eWebhookSecret, "evt-other-teacher", "employee.deposit.completed", "bmoni-teacher2-e2e", "2500.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "other teacher deposit")
	assertPaymentStatus(t, app, paymentID, "PENDING")
	if access = findAccess(t, app, courseID); access == nil || access.Status != "PENDING" {
		t.Fatalf("other-teacher deposit changed access: %+v", access)
	}

	// ---------------------------------------------------------------------
	// 7. The real delivery: correct amount, correct user, valid signature.
	// ---------------------------------------------------------------------
	raw, sig = signedDelivery(e2eWebhookSecret, "evt-deposit-1", "employee.deposit.completed", e2ePlatformUserID, "2500.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "valid deposit webhook")

	// Payment is PAID and carries the matching event id; access is ACTIVE.
	payment := assertPaymentStatus(t, app, paymentID, "PAID")
	if payment.MatchedEventID == nil || *payment.MatchedEventID != "evt-deposit-1" {
		t.Errorf("matched_event_id = %v, want evt-deposit-1", payment.MatchedEventID)
	}
	if payment.PaidAt == nil {
		t.Error("paid_at not set")
	}
	if access = findAccess(t, app, courseID); access == nil || access.Status != "ACTIVE" {
		t.Fatalf("course_access after webhook = %+v, want ACTIVE", access)
	}
	studentRec, err := app.models.Users.FindByEmail(context.Background(), "student@example.com")
	if err != nil {
		t.Fatalf("load student: %v", err)
	}
	enrolled, err := app.models.Enrollments.IsEnrolled(context.Background(), courseID, studentRec.ID)
	if err != nil {
		t.Fatalf("check enrollment: %v", err)
	}
	if !enrolled {
		t.Fatal("student is not enrolled after the payment webhook")
	}

	// 8. The poller endpoint flips to PAID.
	resp, body = get(t, student, srv.URL, "/student/pay/"+paymentID+"/status")
	requireStatus(t, resp, http.StatusOK, "pay status")
	if !bytes.Contains(body, []byte(`"PAID"`)) {
		t.Errorf("pay status body = %s, want {\"status\":\"PAID\"}", truncate(body))
	}

	// 9. The student's course list and course page show the unlocked course.
	_, body = get(t, student, srv.URL, "/student/courses")
	if !bytes.Contains(body, []byte("Intro to Go")) {
		t.Errorf("course list missing paid course after payment\n%s", truncate(body))
	}
	resp, body = get(t, student, srv.URL, "/student/courses/"+courseID)
	requireStatus(t, resp, http.StatusOK, "course detail after payment")

	// 10. The teacher's wallet shows ₦2,500 in confirmed earnings.
	_, body = get(t, teacher, srv.URL, "/teacher/wallet")
	if !bytes.Contains(body, []byte("₦2500")) {
		t.Errorf("teacher wallet missing earnings\n%s", truncate(body))
	}

	// 11. Replaying the same event id is idempotent: 200, nothing changes.
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "replayed event")
	assertPaymentStatus(t, app, paymentID, "PAID")
	if got := countRows(t, app, "course_enrollments"); got != 1 {
		t.Errorf("course_enrollments after replay = %d, want 1 (no double enrollment)", got)
	}

	// ---------------------------------------------------------------------
	// 12. A second purchase goes through the same flow and still resolves.
	// ---------------------------------------------------------------------
	course2ID := createPaidCourse(t, teacher, srv.URL, "Advanced Go", 5000)

	resp, _ = postForm(t, student, srv.URL, "/student/courses/"+course2ID+"/enroll", url.Values{})
	requireStatus(t, resp, http.StatusSeeOther, "second enroll redirect")
	payPath2 := resp.Header.Get("Location")
	payment2ID := strings.TrimPrefix(payPath2, "/student/pay/")

	// A wrong-amount deposit must not unlock the second course either…
	raw, sig = signedDelivery(e2eWebhookSecret, "evt-deposit-2-wrong", "employee.deposit.completed", e2ePlatformUserID, "4999.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "second wrong amount")
	assertPaymentStatus(t, app, payment2ID, "PENDING")

	// …then the exact amount clears it.
	raw, sig = signedDelivery(e2eWebhookSecret, "evt-deposit-2", "employee.deposit.completed", e2ePlatformUserID, "5000.00")
	requireStatus(t, doRaw(t, newWebhookRequest(srv.URL, raw, sig)), http.StatusOK, "second valid deposit")
	assertPaymentStatus(t, app, payment2ID, "PAID")

	// Teacher earnings are now ₦2,500 + ₦5,000 = ₦7,500.
	_, body = get(t, teacher, srv.URL, "/teacher/wallet")
	if !bytes.Contains(body, []byte("₦7500")) {
		t.Errorf("teacher wallet after second sale = %s, want ₦7500", truncate(body))
	}
}

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// startTestPostgres connects to TEST_DATABASE_URL when set, otherwise boots an
// embedded PostgreSQL on a dedicated port. The returned pool is a fresh
// schema-less database; applyMigrations builds the schema.
func startTestPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect TEST_DATABASE_URL: %v", err)
		}
		t.Cleanup(pool.Close)
		return pool
	}

	const port = 55432
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(port).
			Database("sabify_test").
			Username("postgres").
			Password("postgres"),
	)
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres (first run downloads binaries): %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/sabify_test?sslmode=disable", port)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect embedded postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyMigrations rebuilds the schema from migrations/*.sql. Multi-statement
// files (and DO $$ blocks) are executed through pgconn's simple protocol,
// which — unlike the extended protocol — accepts several statements per call.
func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	drop := `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`
	if err := execSimple(ctx, pool, drop); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	files := []string{
		"001_initial_schema.sql",
		"002_course_enrollments.sql",
		"002_quiz_retakes.sql",
		"003_bmoni.sql",
		"004_bmoni_owner_key.sql",
		"005_teacher_wallets.sql",
	}
	var combined strings.Builder
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(repoRoot(), "migrations", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		combined.Write(raw)
		combined.WriteString("\n")
	}
	if err := execSimple(ctx, pool, combined.String()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

func execSimple(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().PgConn().Exec(ctx, sql).ReadAll()
	return err
}

// newE2EApp builds a production-shaped *application with the real routes,
// template cache and models, but a disposable logger, an HTTP-only session
// (the test client speaks plain HTTP), and stub AI/BMONI HTTP clients that
// the tested flow never calls.
func newE2EApp(t *testing.T, pool *pgxpool.Pool) *application {
	t.Helper()
	if err := os.Chdir(repoRoot()); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}

	cache, err := newTemplateCache()
	if err != nil {
		t.Fatalf("template cache: %v", err)
	}

	session := scs.New()
	session.Lifetime = 24 * time.Hour
	session.Cookie.Secure = false // plain-HTTP test client
	session.Cookie.SameSite = http.SameSiteLaxMode

	var cfg config
	cfg.bmoni.webhookSecret = e2eWebhookSecret

	return &application{
		config:        cfg,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:        models.NewModels(pool),
		templateCache: cache,
		session:       session,
		aiClient:      ai.NewClient("http://127.0.0.1:1"),          // unused in this flow
		bmoniClient:   bmoni.NewClient("http://127.0.0.1:1", "key"), // unused in this flow
	}
}

// repoRoot resolves the repository root from this source file's location.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "../.."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// ---------------------------------------------------------------------------
// HTTP helpers (real client + cookie jar per user role)
// ---------------------------------------------------------------------------

func newSessionClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Assert redirect targets explicitly instead of following them.
			return http.ErrUseLastResponse
		},
	}
}

func register(t *testing.T, client *http.Client, baseURL, name, email, role string) {
	t.Helper()
	form := url.Values{
		"name":            {name},
		"email":           {email},
		"password":        {"password123"},
		"confirmPassword": {"password123"},
		"policy":          {"on"},
		"role":            {role},
	}
	resp, body := postForm(t, client, baseURL, "/register", form)
	requireStatus(t, resp, http.StatusSeeOther, "register "+role)
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("register %s redirected to %q, want /login\n%s", role, loc, truncate(body))
	}
}

func login(t *testing.T, client *http.Client, baseURL, email string) {
	t.Helper()
	form := url.Values{"email": {email}, "password": {"password123"}}
	resp, body := postForm(t, client, baseURL, "/login", form)
	requireStatus(t, resp, http.StatusSeeOther, "login "+email)
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("login redirected to %q, want /dashboard\n%s", loc, truncate(body))
	}
}

func createPaidCourse(t *testing.T, client *http.Client, baseURL, title string, priceNaira int) string {
	t.Helper()
	form := url.Values{
		"title":       {title},
		"description": {"E2E test course"},
		"price":       {fmt.Sprintf("%d", priceNaira)},
	}
	resp, body := postForm(t, client, baseURL, "/teacher/courses", form)
	requireStatus(t, resp, http.StatusSeeOther, "create course "+title)
	if loc := resp.Header.Get("Location"); loc != "/teacher/courses" {
		t.Fatalf("create course redirected to %q, want /teacher/courses\n%s", loc, truncate(body))
	}
	// The course id is recovered from the teacher's course list page — the
	// same route the UI uses, keeping the flow fully HTTP-driven.
	return locateCourseID(t, client, baseURL, title)
}

// locateCourseID finds a course id by title from the teacher's course list.
func locateCourseID(t *testing.T, client *http.Client, baseURL, title string) string {
	t.Helper()
	_, body := get(t, client, baseURL, "/teacher/courses")
	idx := bytes.Index(body, []byte(title))
	if idx == -1 {
		t.Fatalf("course %q not found in teacher course list", title)
	}
	// Walk back to the /teacher/courses/{id} link that precedes the title.
	window := body[:idx]
	last := bytes.LastIndex(window, []byte(`/teacher/courses/`))
	if last == -1 {
		t.Fatalf("no course link before title %q", title)
	}
	rest := window[last+len(`/teacher/courses/`):]
	end := bytes.IndexAny(rest, `"' <`) // id ends at the first HTML boundary
	if end == -1 {
		end = len(rest)
	}
	id := string(rest[:end])
	if id == "" {
		t.Fatalf("could not parse course id for %q", title)
	}
	return id
}

// ---------------------------------------------------------------------------
// Webhook simulation
// ---------------------------------------------------------------------------

// signedDelivery builds a BMONI-shaped delivery exactly as documented
// ({id, eventType, payload:{userId, amount}, timestamp}) and signs the raw
// bytes with the HMAC secret. Returns the raw body and the hex signature.
func signedDelivery(secret, eventID, eventType, userID, amount string) ([]byte, string) {
	delivery := map[string]interface{}{
		"id":        eventID,
		"eventType": eventType,
		"payload": map[string]string{
			"userId": userID,
			"amount": amount,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(delivery)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	return raw, hex.EncodeToString(mac.Sum(nil))
}

func newWebhookRequest(baseURL string, raw []byte, signature string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/webhooks/bmoni", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signature)
	return req
}

func doRaw(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func postForm(t *testing.T, client *http.Client, baseURL, path string, form url.Values) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func get(t *testing.T, client *http.Client, baseURL, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func requireStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status = %d, want %d", what, resp.StatusCode, want)
	}
}

func assertPaymentStatus(t *testing.T, app *application, paymentID, want string) *models.Payment {
	t.Helper()
	payment, err := app.models.Payments.FindByID(context.Background(), paymentID)
	if err != nil {
		t.Fatalf("load payment %s: %v", paymentID, err)
	}
	if payment.Status != want {
		t.Fatalf("payment %s status = %q, want %q", paymentID, payment.Status, want)
	}
	return payment
}

func findAccess(t *testing.T, app *application, courseID string) *models.CourseAccess {
	t.Helper()
	// Resolve the single student used in this test.
	student, err := app.models.Users.FindByEmail(context.Background(), "student@example.com")
	if err != nil {
		t.Fatalf("load student: %v", err)
	}
	access, err := app.models.CourseAccess.Find(context.Background(), student.ID, courseID)
	if err != nil {
		t.Fatalf("load course_access: %v", err)
	}
	return access
}

func countRows(t *testing.T, app *application, table string) int {
	t.Helper()
	var n int
	err := app.models.Users.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}

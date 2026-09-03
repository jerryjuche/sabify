# BMONI Paid Enrollment — Implementation

> **Status:** Implemented (2026-09-03).
> **Companion docs:** `doc/bmoni-feature-guide.md` (product/team view) ·
> `doc/bmoni-integration-plan.md` (BMONI API research) · `doc/bmoni-implementation.md` (this file).
> **This file records what actually shipped in code**, how to run it, and the
> caveats a future maintainer must know. It supersedes the "no code written yet"
> status of the research docs.

---

## 1. TL;DR

Paid course enrollment is now implemented end-to-end:

- A teacher can set an optional price on a course (blank = free).
- Free courses keep the existing instant-enroll flow, unchanged.
- Paid courses send a student through a **payment page** where they make a bank
  transfer to the platform's BMONI virtual account (VBA), then the course
  unlocks automatically once BMONI's webhook confirms the deposit.
- A teacher **Wallet** page shows confirmed earnings (sum of paid fees across
  the teacher's courses) plus the platform deposit account.

This follows the **platform-wallet** model chosen during design (one
platform-owned BMONI wallet collects all fees; students pay by bank transfer,
no per-student KYC, no wallet-to-wallet transfers).

---

## 2. Files added / changed

### New migrations
| File | Purpose |
|---|---|
| `migrations/003_bmoni.sql` | `courses.price_kobo`, `bmoni_wallets`, `payments`, `course_access`, `webhook_events` |

### New models (`internal/models/`)
| File | Contents |
|---|---|
| `courseaccess.go` | `CourseAccess` + `CourseAccessModel` (`Create`, `Find`, `SetActive`, `FindByStudent`) |
| `payments.go` | `Payment` + `PaymentModel` (`CreatePending`, `FindByID`, `MarkPaid`, `ListForStudent`, `FindPendingForDeposit`, `SumPaidByTeacher`) |
| `bmoniwallet.go` | `BmoniWallet` + `GetPlatform`, `Save` |
| `webhookevents.go` | `EventRecord` + `InsertIgnore` (dedupe gate) |

### New BMONI client (`internal/bmoni/`)
| File | Contents |
|---|---|
| `client.go` | `Client` HTTP wrapper + `x-api-key`, `Balances`, `DepositAccount` |
| `lifecycle.go` | `CreateUser`, `CreateWallet` (owner-proof → EIP-191 sign → `create-managed`), `StartNigeria`, `BVNLookup`, `ActivateKYC`, `waitForVBA` |
| `signing.go` | `OwnerKey` secp256k1 + EIP-191 / raw-digest signing (go-ethereum) |
| `webhook.go` | `VerifyWebhookSignature` (HMAC-SHA256 over raw body) |

### New handler file (`cmd/web/bmoni_handlers.go`)
| Handler | Route |
|---|---|
| `bmoniWebhook` | `POST /webhooks/bmoni` (public) |
| `studentPay` | `GET /student/pay/{paymentId}` |
| `studentPayStatus` | `GET /student/pay/{paymentId}/status` |
| `teacherWallet` | `GET /teacher/wallet` |

### New templates / assets
| File | Purpose |
|---|---|
| `ui/html/student/pay.html` | Deposit screen (account no., bank, amount, reference, status pill, copy buttons, "I've paid" button) |
| `ui/html/teacher/wallet.html` | Teacher earnings + platform deposit account |
| `ui/static/js/pay-status.js` | 3-second poller that flips the page to "Payment confirmed" and reloads |

### Modified files
- `internal/models/courses.go` — `PriceKobo *int64` + `UpdatePrice`, all queries/rows updated
- `internal/models/models.go` — registered `CourseAccess`, `Payments`, `BmoniWallets`, `WebhookEvents`
- `cmd/web/main.go` — `bmoni` config block + `bmoniClient` on app; `naira` template func
- `cmd/web/routes.go` — webhook (public) + student pay + teacher price/wallet routes
- `cmd/web/helpers.go` — `templateData` payment fields + `backgroundContext`
- `cmd/web/teacher_handlers.go` — `createCourse` price, `updateCoursePrice`, `parsePriceKobo`
- `cmd/web/student_handlers.go` — `enrollInCourse` paid path, `studentCourseDetail` paywall gating, `studentCourses` active-access handling
- `.env.example` — `BMONI_*` vars
- `ui/html/teacher/create-course.html`, `course-detail.html`, `courses.html` — price input/badges
- `ui/html/student/courses.html`, `course.html` — price badge + paywall states
- `ui/html/components/sidebar.html` — teacher "Wallet" nav link

### New tool
| File | Purpose |
|---|---|
| `tools/bmoni-bootstrap/main.go` | One-time platform-wallet provisioning (needs `BMONI_API_KEY`) |

`go.mod` gained `github.com/ethereum/go-ethereum` (crypto + accounts).

---

## 3. Data model

All amounts are stored as **kobo (`BIGINT`)**, never floats. `NULL` price = free.

```text
courses        . + price_kobo BIGINT NULL        -- optional price
bmoni_wallets  (platform singleton) id, bmoni_user_id UNIQUE, owner_address,
               smart_wallet_id, currency, status, vba_account_number,
               vba_bank_name, created_at
payments       id, student_id -> users, course_id -> courses, amount_kobo BIGINT,
               status PENDING|PAID|FAILED|MANUAL, reference, narration_hint,
               matched_event_id NULL, created_at, paid_at
course_access  id, student_id -> users, course_id -> courses, payment_id -> payments,
               status PENDING|ACTIVE, UNIQUE(student_id, course_id), created_at
webhook_events event_id TEXT PRIMARY KEY, event_type, payload JSONB, processed_at
```

`course_access` is deliberately separate from the existing **free**
`course_enrollments` relation. Free access = a row in `course_enrollments`;
paid access = a `ACTIVE` row in `course_access`. The student-facing views
consider a course "enrolled" if either exists.

---

## 4. The flow

```
Student                           SABIFY (Go)                    BMONI
   |                                  |                             |
   | 1. Enroll (paid course)          |                             |
   +--------------------------------->| 2. payments PENDING         |
   |                                  |    + course_access PENDING  |
   | 3. /student/pay/{id} shows       |                             |
   |    VBA + amount + reference      |                             |
   <----------------------------------+                             |
   | 4. bank transfer to platform VBA +---------------------------->|
   |                                  | 5. employee.deposit.completed|
   |                                  |<----------------------------+
   |                                  | 6. verify HMAC + dedupe     |
   |                                  | 7. payments -> PAID         |
   |                                  |    course_access -> ACTIVE  |
   | 8. poller (/status) sees PAID    |                             |
   <----------------------------------+                             |
   | 9. course unlocked               |                             |
```

- **Free course:** `enrollInCourse` calls `Enrollments.Insert` and redirects to
  the course immediately (unchanged from before).
- **Paid course:** creates a PENDING `payments` row (reference
  `SABIFY-<course>-<student>`) and PENDING `course_access`, then redirects to
  `/student/pay/{paymentID}`.
- `studentCourseDetail` renders a **paywall** (price + "Enroll for ₦X") for
  non-enrolled students on paid courses, and only loads materials/quizzes when
  access is granted (free enrollment OR active `course_access`).
- The webhook flips status and unlocks the course; the payment page's poller
  notices and reloads into the "Go to course" state.

---

## 5. Webhook handler notes

- Registered at top level in `routes.go` — **outside** the auth/session groups
  (BMONI posts server-to-server).
- Verifies `X-Webhook-Signature` (HMAC-SHA256 over the **raw body**) when
  `BMONI_WEBHOOK_SECRET` is configured; rejects forgery with `401`.
- De-duplicates on `event_id` via `webhook_events` (`ON CONFLICT DO NOTHING`);
  replays are acknowledged but not re-processed.
- Acknowledges `200` fast and processes `employee.deposit.completed` in a
  goroutine (BMONI times out deliveries at ~10s; a `4xx`/`5xx` here could
  trigger a duplicate retry).

### Matching caveat (known limitation)
BMONI's deposit webhook carries no per-payment reference, so deposits are
matched **first-come-first-served** against the oldest unresolved PENDING
payment (`FindPendingForDeposit`). The narration/reference shown to the student
is for human reconciliation. For a deterministic demo, keep only one in-flight
payment at a time. This mirrors the design risk R-3 recorded in the research
docs.

---

## 6. Configuration (`.env`)

Add to `.env` (currently optional — the app runs without them in dev, and
payment features surface a "not configured" path):

```
BMONI_BASE_URL=https://embedded-dev.bmoni.com
BMONI_API_KEY=...
BMONI_WEBHOOK_SECRET=...
BMONI_WALLET_ENCRYPTION_KEY=...
```

- `BMONI_BASE_URL` defaults to the sandbox origin if unset.
- `BMONI_WEBHOOK_SECRET` — when set, the webhook **requires** a valid HMAC.
- `BMONI_WALLET_ENCRYPTION_KEY` — reserved for at-rest encryption of the
  platform owner key (the bootstrap tool currently prints the address and
  leaves key storage to the operator).

---

## 7. Running it

### 1. Apply the migration
There is no migration tool; apply manually:

```bash
psql -U <user> -d sabify_db -f migrations/003_bmoni.sql
```

### 2. Provision the platform wallet (once)
Requires `BMONI_API_KEY` + these env vars for the DB; uses the sandbox
"Bunch Dillon" persona so KYC passes automatically:

```bash
BMONI_API_KEY=... go run ./tools/bmoni-bootstrap
```

The resulting VBA number + bank are stored in `bmoni_wallets` and shown on the
student payment page and teacher wallet page.

### 3. Run the app
```bash
make run
```
**Restart required** after editing any template (template cache is built at
startup).

### 4. Exercise the flow
1. Teacher sets a price on a course (create form, or the "Course price" card on
   the course-detail page).
2. Student opens the course → sees "Enroll for ₦X" → is taken to the payment
   page (VBA number, amount, reference).
3. Fund the platform wallet (sandbox test credits / bank transfer). The BMONI
   deposit webhook fires → course unlocks → payment page flips to
   "Payment confirmed".
4. Teacher opens **Wallet** in the sidebar to see confirmed earnings.

---

## 8. What was intentionally NOT done

- Per-student / per-teacher BMONI wallets and wallet-to-wallet transfers
  (requires per-user KYC — rejected by design).
- Teacher bank offramp / withdrawal to a Nigerian bank (documented as a future
  bonus beat; the wallet page shows balances, not withdrawal).
- Webhook balance-polling fallback ("Check now" currently just reloads and
  re-queries the local DB status; wiring it to BMONI `GET /balances` is a
  follow-up).
- Test files (repo still has zero `*_test.go`; `make test` passes vacuously per
  `AGENTS.md`).

---

## 9. Normalization / correctness notes for future work

- The deposit-matching heuristic (`FindPendingForDeposit`) is the main known
  fragility — improve by matching against `narration_hint`/amount when BMONI
  exposes a reference, or pair each student's payment with a manual-confirm
  admin path.
- `BMONI_WALLET_ENCRYPTION_KEY` is defined but the bootstrap tool does not yet
  encrypt the owner key at rest — encrypt before storing for production.
- The webhook only processes `employee.deposit.completed` today; other
  `employee.*` / `wallet.*` events are recorded in `webhook_events` but ignored.

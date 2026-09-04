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
  the teacher's courses), the **live** BMONI balance of the platform wallet,
  the platform deposit account, and (when the owner key is stored) a
  withdrawal form that pays out to any Nigerian bank via BMONI's
  verify → register → offramp → sign flow.

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
| `client.go` | `Client` HTTP wrapper + `x-api-key`, `Balances`, `DepositAccount`, `ResolveUserByPhone` (recovery), `BindVBA` (re-point deposits to a fresh wallet), `NigerianBanks`, `VerifyNigerianAccount`, `RegisterWithdrawalAccount`, `CreateOfframp`, `ApproveProposal`, `SignPayload`, `SubmitProposalSignature`, `GetProposal` |
| `lifecycle.go` | `CreateUser`, `CreateWallet` (owner-proof → EIP-191 sign → `create-managed`), `StartNigeria`, `LookupBVN` / `BVNLookup`, `ActivateKYC`, `waitForVBA` |
| `signing.go` | `OwnerKey` secp256k1 + EIP-191 / raw-digest signing (go-ethereum), `EncryptOwnerKey` / `DecryptOwnerKey` (AES-256-GCM at rest), `signing_test.go` (BMONI's published test vector) |
| `webhook.go` | `VerifyWebhookSignature` (HMAC-SHA256 over raw body) |

### New handler file (`cmd/web/bmoni_handlers.go`)
| Handler | Route |
|---|---|
| `bmoniWebhook` | `POST /webhooks/bmoni` (public) |
| `studentPay` | `GET /student/pay/{paymentId}` |
| `studentPayStatus` | `GET /student/pay/{paymentId}/status` |
| `teacherWallet` | `GET /teacher/wallet` |
| `teacherWalletWithdraw` | `POST /teacher/wallet/withdraw` |

### New templates / assets
| File | Purpose |
|---|---|
| `ui/html/student/pay.html` | Deposit screen (account no., bank, amount, reference, status pill, copy buttons, "I've paid" button) |
| `ui/html/teacher/wallet.html` | Teacher earnings + live BMONI balance + platform deposit account + withdrawal form |
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
- Processes `employee.deposit.completed` **synchronously** (the work is a
  handful of fast DB statements, comfortably inside BMONI's ~10s delivery
  timeout). A failure returns `500` so BMONI retries; because the dedupe row
  is written **only after** successful processing, a retry actually
  re-processes instead of being swallowed. Any other `4xx` would discard the
  delivery forever, so internal errors never return `4xx`.

### Matching caveat (improved in the shipped handlers)
BMONI's deposit webhook carries **no per-payment reference**, but it does
carry the deposit `amount` (`payload.amount`, decimal NGN) and the BMONI
`userId` it landed in. `processBmoniDeposit` therefore: (1) only considers
deposits into the platform wallet's BMONI user, and (2) matches the deposit
amount (converted to kobo) against the oldest unresolved PENDING payment
(`FindPendingForDeposit`). A mismatch leaves the payment locked and logs a
warning — this keeps unrelated wallet funding (e.g. sandbox test credits) or
under/over payments from unlocking a course. The narration/reference shown to
the student remains for human reconciliation. For a fully deterministic demo,
keep only one in-flight payment at a time.

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
- `BMONI_WALLET_ENCRYPTION_KEY` — AES-256-GCM key that seals the wallet
  owner private key at rest (`bmoni_wallets.owner_key_enc`). The bootstrap
  tool encrypts and stores it when this var is set; without it the owner key
  is not persisted and **teacher withdrawals are unavailable** (deposits and
  enrollment still work — money-in needs no signing).

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
BMONI_API_KEY=... BMONI_WALLET_ENCRYPTION_KEY=$(openssl rand -hex 32) go run ./tools/bmoni-bootstrap
```

The resulting VBA number + bank are stored in `bmoni_wallets` and shown on the
student payment page and teacher wallet page. Re-running is idempotent, and a
`409` from `POST /v1/users` (an earlier run already created the user) is
handled by recovering the existing user + NGN wallet via
`GET /v1/smart-wallets/by-phone` instead of forking wallet history — but a
recovered wallet's original owner key is unrecoverable, so withdrawals stay
disabled for it.

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
- A webhook balance-polling fallback is not needed in code: the payment page
  already polls local status, and the teacher wallet reads the **live** BMONI
  balance directly (best-effort; degrades to "unavailable" on API errors).
- ~~Test files~~ — **added**: `cmd/web/bmoni_e2e_test.go` walks the entire
  paid-enrollment flow through the real HTTP stack against an embedded
  PostgreSQL (see §9), and `internal/bmoni/signing_test.go` pins the signing
  implementation to BMONI's published test vector.

### Withdrawal (teacher payout) — shipped, with a caveat
`POST /teacher/wallet/withdraw` runs the full BMONI payout path for the
platform wallet: verify → register → offramp proposal → approve → sign
(raw-digest, v = 27/28) → submit → poll to terminal status. The form renders
only when `bmoni_wallets.owner_key_enc` is present; otherwise the wallet page
explains that the owner key was never stored and points at the bootstrap tool.
Sandbox note: the sandbox resolves **no test bank accounts** for
`verify-nigerian-account` (only the identity personas), so withdrawal
verification returns `400 E101` for any account number — correct sandbox
behavior, not a bug. **Updated 2026-09-04:** this was observed on the old
platform-wallet user; a *fresh persona teacher* passed verification and
account registration fine and was instead blocked at offramp creation with
`403 E503` because the sandbox wallet is unfunded (see
`doc/bmoni-teacher-kyc.md` §12).

**Why the current sandbox wallet cannot withdraw (verified live 2026-09-03):**
the platform wallet on the shared sandbox key was created on 2026-07-30,
before the owner-key encryption feature existed, so its key was never
persisted and is unrecoverable. Recovery attempts are all dead ends:

| Attempt | Result |
|---|---|
| Rotate the owner key | No such endpoint exists (checked the full OpenAPI spec) |
| Create a second CNGN wallet on the same user | `409 E502 This item already exists` — one wallet per currency per user |
| Delete the user, re-create with the persona phone | Docs: deletion does **not** produce a fresh account; identifiers stay bound |
| Create a new user with any other phone | `409 User already exists` — the shared key's namespace is shared/polluted |

**The sanctioned path to a withdrawal-capable wallet:** get a **dedicated
sandbox API key** from developers@bkey.me (fresh partner namespace), then run
the bootstrap tool on it **before** anyone else creates the persona user:

```bash
BMONI_API_KEY=<your-key> BMONI_WALLET_ENCRYPTION_KEY=$(openssl rand -hex 32) \
  go run ./tools/bmoni-bootstrap -persona bunch
```

The tool now supports `-persona bunch|samson`, pulls the persona's exact KYC
record via the fetch-only BVN lookup (the docs' recommended order, which
guarantees the name/DOB match), binds the VBA to the wallet, and stores the
owner key encrypted at rest. `bmoni_wallets` rows without a stored key are
removed after a fresh provision so the app cannot pick a locked wallet
(`GetPlatform` also prefers a wallet with `owner_key_enc` set).

---

## 9. End-to-end test

`cmd/web/bmoni_e2e_test.go` (`TestBmoniPaidEnrollmentE2E`) verifies the full
payment integration with **no mocks** — the real Chi router, session/auth
middleware, handlers, models and a real PostgreSQL:

1. Registers + logs in a teacher and student over HTTP.
2. Teacher creates a paid course (₦2,500); student enrolls and is redirected
   to the payment page, which shows the platform VBA, bank, amount and
   `SABIFY-*` reference.
3. Rejects a **forged webhook signature** (401) and ignores deposits for
   **unknown BMONI users** and **mismatched amounts** (course stays locked).
4. Delivers a correctly signed `employee.deposit.completed` → asserts
   `payments` PAID with `matched_event_id`, `course_access` ACTIVE, enrollment
   recorded, the poller returns `{"status":"PAID"}`, the course list unlocks,
   and the teacher wallet shows ₦2,500.
5. Asserts **idempotent replay** (same event id → no double enrollment) and
   completes a **second purchase** (₦5,000) incl. a wrong-amount rejection,
   ending with ₦7,500 total earnings.

The test provisions PostgreSQL automatically via
`github.com/fergusstrange/embedded-postgres` (binaries downloaded on first
run) and rebuilds the schema from `migrations/*` on every run. On machines
with an existing database, point `TEST_DATABASE_URL` at it and the embedded
instance is skipped:

```bash
go test ./cmd/web/ -run TestBmoniPaidEnrollmentE2E -v
TEST_DATABASE_URL=postgres://user:pass@localhost:5432/sabify_test?sslmode=disable \
  go test ./cmd/web/ -run TestBmoniPaidEnrollmentE2E -v
```

## 10. Normalization / correctness notes for future work

- The deposit-matching heuristic (`FindPendingForDeposit` + amount match in
  `processBmoniDeposit`) is the main known fragility — two in-flight payments
  for the same amount are indistinguishable. Improve by matching against
  `narration_hint`/reference when BMONI exposes one, or pair each student's
  payment with a manual-confirm admin path. Keep one in-flight payment at a
  time for a deterministic demo.
- The webhook only processes `employee.deposit.completed` today; other
  `employee.*` / `wallet.*` events are recorded in `webhook_events` but ignored.
- The balances endpoint reports currency as `"NGN"` (and amount as `"balance"`)
  while other endpoints use the `CNGN` stablecoin code — the client accepts
  both.
- The live-sandbox wallet created on 2026-07-30 predates the owner-key
  encryption feature; its key was never persisted, so withdrawals on it are
  disabled. Provisioning a **fresh** wallet with the tool (with
  `BMONI_WALLET_ENCRYPTION_KEY` set) is the path to a fully withdrawal-capable
demo.

---

## 11. Live sandbox walkthrough (2026-09-03)

Validated against the real sandbox (`https://embedded-dev.bmoni.com`, shared
key) and a real server on `:4000` with an embedded PostgreSQL:

1. Registered teacher + student over HTTP; logged both in (role checks OK).
2. Teacher created a paid course (₦2,500); student enrolled → redirected to
   `/student/pay/{id}` which rendered the **live VBA `7962860461`
   (PROVIDUS BANK)**, the amount, and reference `SABIFY-b9fe8ac7-d4e18bb5`.
3. Status endpoint reported `PENDING`; a **forged webhook signature → 401**.
4. A correctly HMAC-signed `employee.deposit.completed` (amount `2500.00`,
   platform `userId`) → `200`, payment → `PAID` (`matched_event_id` set),
   `course_access` → `ACTIVE`, `course_enrollments` +1, and the poller
   returned `{"status":"PAID"}`.
5. Replaying the same event id → `200`, no duplicate processing (ledger: 1
   row, payments: 1 row, enrollments: 1 row).
6. Teacher wallet rendered earnings `₦2,500`, the **live BMONI balance `₦0`**
   (wallet unfunded — sandbox test tokens are credited manually) and the VBA;
   the withdrawal form was correctly hidden (owner key not stored) and a
   direct `POST /teacher/wallet/withdraw` flashed the friendly explanation
   instead of erroring.
7. Live API checks: `nigerian-banks` returned the full CBN list;
   `verify-nigerian-account` correctly returned `400 E101` for a made-up
   account (sandbox has no test bank accounts, only identity personas);
   `by-phone` recovery returned the existing user + NGN wallet.

**Found and fixed during the walkthrough:** the client called the wrong
balances path (`/balances` → `/smart-wallets/account/balances`) and matched
currency `CNGN`/`NGN` and amount field `balance`/`amount` — the teacher
wallet previously showed "Balance unavailable" even though BMONI answered.

# BMONI Integration Plan — IntelliAI / SABIFY LMS

> **Status:** Research + design only. No application code was changed.
> **Date:** 3 September 2026 (exhibition: Friday 4 September 2026, 12:00, Pier Place, Yaba)
> **Repo reality:** Go 1.25 + Chi + pgx + `html/template` + scs sessions
> (Alex Edwards "Let's Go" layout — **not** NestJS/Next.js).
> **Primary sources:** BMONI docs at https://bkey.mintlify.app — every claim
> below is verified against: [API intro], [lifecycle], [use-cases],
> [integration-flow], [quickstart], [ngn-rails], [sandbox-test-data],
> [webhooks], [request-test-tokens], [no-app flow].

[API intro]: https://bkey.mintlify.app/api-reference/introduction
[lifecycle]: https://bkey.mintlify.app/lifecycle
[use-cases]: https://bkey.mintlify.app/use-cases
[integration-flow]: https://bkey.mintlify.app/api-reference/integration-flow
[quickstart]: https://bkey.mintlify.app/api-quickstart
[ngn-rails]: https://bkey.mintlify.app/api-reference/ngn-rails
[sandbox-test-data]: https://bkey.mintlify.app/api-reference/sandbox-test-data
[webhooks]: https://bkey.mintlify.app/api-reference/webhooks
[request-test-tokens]: https://bkey.mintlify.app/request-test-tokens
[no-app flow]: https://bkey.mintlify.app/api-reference/integration-flow-no-app

---

## 1. Executive summary

**The handoff brief's product goal — paid course enrollment confirmed by a
BMONI webhook that auto-enrolls the student — is achievable**, but three of its
premises must be corrected before anyone writes code:

| Handoff-brief premise | Reality | Consequence |
|---|---|---|
| NestJS backend / Next.js frontend | This repo is **Go + Chi + pgx** with server-side Go templates (`cmd/web`, `internal/models`) | All NestJS module structure and Next.js UI sketches in the brief are **not executable here**; the design must be translated to Go conventions |
| "Backend creates a payment intent and returns an NGN Virtual Account… per transaction with unique reference" | BMONI issues **one VBA per onboarded user's wallet**, not per invoice. The deposit webhook carries `{userId, amount}` — **no per-payment reference is documented** | The platform should collect into **one platform-owned NGN wallet** and match deposits to students by amount + narration locally. A "dynamic VBA per enrollment" scheme like Paystack's is not supported |
| "Mobile SDKs not required for web" | ✅ Confirmed — BMONI publishes **official Go snippets** in its quickstart | The entire lifecycle can run **server-side in Go**, including both signing steps |
| `employee.deposit.completed` webhook | ✅ Confirmed — but only for **partner-scoped** subscriptions (supply `partnerId`); money events are renamed `wallet.*` → `employee.*` | Subscribe with `partnerId` or the subscription silently never fires |
| "~80% complete core LMS" | Indexed on 3 Sept: student pages render; **teacher surface is empty/0-byte**, quiz-taking is unreachable, **there is no enrollment table** | Enrollment + payment is greenfield (good: no legacy constraint) — but the paid flow must be self-contained and demo-safe |

**BMONI Embedded is an embedded-finance wallet platform, not a Paystack-style
payment gateway.** Money always moves through a user's self-custodied
stablecoin wallet (`CNGN`, `USDB`, …) after a fixed 6-stage lifecycle:
**user → smart wallet → KYC → rail → fund → move money**. For this product the
practical shape is:

> **One platform-owned BMONI wallet (NGN) collects course fees.** Students
> stay ordinary local `users` — no per-student BMONI accounts, no per-student
> KYC. A student pays by bank transfer to the platform's NGN VBA; BMONI's
> `employee.deposit.completed` webhook (HMAC-verified, idempotent) marks the
> local payment paid; the course is unlocked. Teacher/learner **payouts** via
> the same wallet's offramp are a bonus demo beat, not the core.

---

## 2. What BMONI Embedded actually is (verified)

### Credentials & surface

| Item | Value |
|---|---|
| Sandbox base URL | `https://embedded-dev.bmoni.com` — **origin only, no `/v1`** (trailing `/v1` → `/v1/v1/...` 404) |
| Production base URL | `https://embedded.bmoni.com` |
| Auth | `x-api-key: <key>` header on every request |
| Shared sandbox key | `pk_a025cacbf33a_76fb864113f3540909de5b1da39cc146906e35b1c6d4d1e4` (dev base only; request your own for production) |
| Interactive reference | `https://embedded-dev.bmoni.com/docs` (Scalar) |
| Wallet currencies | **stablecoin codes** — `CNGN` (NGN), `USDB`, `CADC`, `EURe`, `GBPe`, `MEXe` |

### The six-stage lifecycle (order is fixed and enforced)

1. **User** — `POST /v1/users` → `bmoniUserId`. Persist it; recreating forks history. `409` on duplicate email/phone = recover, don't retry.
2. **Smart wallet** — `POST /v1/users/{id}/smart-wallets/owner-proof-challenges` returns an **EIP-191 message**; sign it with a secp256k1 key; `POST /v1/users/{id}/smart-wallets/create-managed` deploys the wallet. Challenge expires in **10 minutes**, single-use.
3. **KYC** — fixed order: lookups → document uploads (`identification`, `proof-of-address`; Nigeria skips `biometric`) → `PATCH /kyc` → `GET /kyc/readiness` → `POST /kyc/activate` (NGN: **omit** `sumsubLevelName`).
4. **Rail** — `POST /v1/users/{id}/onboarding/start-nigeria` with `{bvn, ngnWalletAddress, ngnWalletIndex}`. **This is what issues the NGN VBA.**
5. **Fund** — incoming bank transfers land on the VBA → wallet credited as `CNGN`. Sandbox funding is **manual** (see §7). Crypto top-up exists (`POST /deposit/wallet`) but needs real testnet coins.
6. **Move money** — offramp to Nigerian banks: `nigerian-banks` → `verify-nigerian-account` → `withdrawal-accounts/nigeria` → `offramp/nigeria`; or transfers via **proposal → approve → sign-payload → sign**.

### Signing (the #1 integration gotcha)

The quickstart is explicitly a **server-side walkthrough with official Go
snippets** (`github.com/ethereum/go-ethereum`). Two different signature styles:

| | Owner proof (stage 2) | Proposal sign (stage 6) |
|---|---|---|
| What you sign | challenge **text** | raw **32-byte digest** |
| EIP-191 prefix | **Yes** | **No** |
| Go | `crypto.Sign(accounts.TextHash([]byte(msg)), key)` | `crypto.Sign(digest, key)` |
| v-byte | add 27 (go-ethereum returns 0/1) | add 27 |

Output is always a `0x`-prefixed 130-hex signature. Wrong style = rejected
signature that "recovers to a different address".

### Webhooks (verified)

- Register: `POST /v1/webhooks/config` `{callbackUrl, events[], partnerId, active}`.
  Response carries `secretKey` (64 hex chars) — store in env, never the DB.
  One config per partner scope; second create → `409`; update with `PATCH`;
  rotate with `POST /v1/webhooks/config/rotate-secret`.
- Delivery: `POST` to your URL, header `X-Webhook-Signature` =
  **HMAC-SHA256 over the raw body bytes** keyed with `secretKey`, hex-encoded.
  Re-serializing parsed JSON breaks the digest. Compare with `hmac.Equal`
  (constant time) after a length check.
- **Must supply `partnerId`** → events arrive as `employee.*`
  (`employee.deposit.completed` etc.). Omitting it creates a legacy global
  subscription that only matches `wallet.*` names — a common silent failure.
- Retry rules: `2xx` = delivered; `5xx`/timeout/`408`/`429` = retried;
  **any other 4xx = permanent, never retried** (return 4xx only for real
  forgery, never internal errors). Delivery timeout: **10 s** — acknowledge
  fast, process afterwards.
- Dedupe on event `id` (stable across retries). Keep a processed-events table.
- Event payloads documented: `{userId, amount, …}` — **no per-payment
  reference field is documented** (see §4 risk R-3).
- `GET /v1/webhooks/events` inspects delivery history (status/attempts/error).

### Sandbox identities (must match exactly)

| Persona | BVN | NIN | Phone (→ E.164) | First/Last name |
|---|---|---|---|---|
| Bunch Dillon | `95888168924` | `63184876213` | `08000000000` → `+2348000000000` | `Bunch` / `Dillon` |
| Samson Jabo | `22222222222` | `18482561982` | `08000000001` → `+2348000000001` | `Samson` / `Jabo` |

Rule: **create the BMONI user with the persona's exact name + phone**, or
verification deliberately fails (that's the sandbox working). BVN look-up
(`GET /kyc/bvn-lookup/{bvn}`) is fetch-only — the cheapest plumbing test.

### What is NOT in the API (so we don't design against it)

- No per-invoice / per-transaction "dynamic VBA" issuance (only per-user VBAs).
- No split-payment / revenue-share endpoints.
- No `charge.success`-style events or `x-bmoni-signature` header (it's
  `X-Webhook-Signature` + `employee.*` events).
- No on-demand webhook test trigger yet ("tracked as platform work") — drive
  a real sandbox action instead.
- No browser SDK.

---

## 3. Integration model chosen for this repo

### Scope decision (settled after review)

| In scope for Friday | Out of scope |
|---|---|
| Platform-owned **NGN wallet** (collect fees) | Per-student BMONI wallets / per-student KYC |
| Local `enrollments` + `payments` (PENDING → PAID → ACTIVE) | Full self-custodied in-browser wallets |
| Student pays by transfer to platform VBA; webhook auto-enrolls | Dynamic per-invoice VBAs |
| Payment UI on the student course page (server-rendered) | Native mobile SDKs |
| HMAC-verified, idempotent webhook endpoint | Microservices / separate app |
| **(Bonus beat)** one-off teacher payout via offramp, if funded | Multi-party splits |

Rationale: matches the real API, keeps students out of KYC, is the smallest
thing that demoes "money in → verified → course unlocked", and leaves the core
learning loop untouched (per the brief's guiding principle).

### Collection model detail (the correction)

Because BMONI has **one VBA per wallet**, all fee deposits land on the platform
wallet's single account number. Local assignment happens as follows:

1. Student taps **Enroll (₦X)** on a course.
2. Handler creates local `payments` row `PENDING` with `amount_kobo`, a human
   reference (`SABIFY-<course>-<student>`), and `narration_hint`.
3. Handler renders deposit instructions: platform VBA account number + bank +
   exact amount + **reference to write in the transfer narration**.
4. `employee.deposit.completed` webhook arrives with `{userId, amount}` →
   we match `amount_kobo` against an unmatched `PENDING` payment for that
   course/student when narration is available, else by amount +
   first-come-first-served **with a manual-confirm admin fallback** (see R-3).
5. Payment → `PAID`; `enrollments` row → `ACTIVE`; course page unlocks.

This is demo-honest and auditable: every deposit is recorded on the platform
ledger whether or not the heuristic match succeeds.

---

## 4. Codebase mapping (Go — no code written)

All additions follow existing conventions: models own SQL in
`internal/models/`, handlers are methods on `*application` in `cmd/web/`,
`application` is wired in `cmd/web/main.go`, templates under `ui/html/`,
migration under `migrations/`, env in `.env`.

### 4.1 Configuration (`cmd/web/main.go` + `.env.example`)

| Env var | Default | Used by |
|---|---|---|
| `BMONI_BASE_URL` | `https://embedded-dev.bmoni.com` | client |
| `BMONI_API_KEY` | — | client `x-api-key` |
| `BMONI_WEBHOOK_SECRET` | — | webhook HMAC |
| `BMONI_WALLET_ENCRYPTION_KEY` | — | at-rest encryption of the platform owner key |

Add fields to the existing `config` struct; read once in `main()` alongside
`APP_PORT` handling. `.env` is already required by the Makefile and gitignored.

### 4.2 Dependency

`github.com/ethereum/go-ethereum` — only `crypto` + `accounts` are used
(keygen, `TextHash`, `crypto.Sign`). This is the exact library BMONI's own Go
examples import. (Vendored nothing else; `net/http` for the client.)

### 4.3 Migration `002_bmoni.sql` (single file, house style: UUID PKs, pgcrypto)

```text
bmoni_wallets   (id UUID PK, wallet_type 'platform'|'teacher', bmoni_user_id UNIQUE,
                 owner_address TEXT, smart_wallet_id TEXT, currency TEXT DEFAULT 'CNGN',
                 status TEXT, vba_account_number TEXT, vba_bank_name TEXT,
                 created_at)                                  -- the platform wallet row (singleton)
payments        (id UUID PK, student_id → users, course_id → courses,
                 amount_kobo BIGINT, status 'PENDING'|'PAID'|'FAILED'|'MANUAL',
                 reference TEXT, narration_hint TEXT,
                 matched_event_id TEXT NULL, created_at, paid_at)
enrollments     (id UUID PK, student_id → users, course_id → courses,
                 payment_id → payments NULL, status 'PENDING'|'ACTIVE',
                 UNIQUE(student_id, course_id), created_at)
webhook_events  (event_id TEXT PRIMARY KEY, event_type TEXT, payload JSONB,
                 processed_at TIMESTAMP)                      -- idempotency ledger
```

`webhook_events.event_id` PRIMARY KEY gives the `INSERT … ON CONFLICT DO
NOTHING` dedupe that replay-safe processing needs. Money stored as **kobo
(`BIGINT`), never float** — house rule for the ledger.

### 4.4 `internal/bmoni` client (new small package)

Mirrors the models-own-SQL ethos: one file `client.go` (HTTP + auth +
timeouts), one `lifecycle.go` (ordered helpers). Methods, in lifecycle order:

- `CreateUser(ctx, firstName, lastName, email, phoneE164) (bmoniUserID, error)`
- `CreateWallet(ctx, userID, currency, ownerKey) (smartWalletID, address, error)`
  — internally: challenge → EIP-191 sign → `create-managed` (handles the
  10-minute expiry by requesting a fresh challenge on retry)
- `PatchKYC(ctx, userID, profile)` + `BVNLookup` + `ActivateKYC` (NGN: no body)
- `StartNigeria(ctx, userID, bvn, walletAddress, walletIndex)` — issues the VBA
- `DepositAccount(ctx, userID) (accountNumber, bankName, error)` — `GET /bank-accounts/deposit-accounts/NGN` to display
- `Balances(ctx, userID) ([]Balance, error)`
- Payout path (bonus): `Banks`, `VerifyAccount`, `RegisterWithdrawalAccount`,
  `Offramp(ctx, userID, smartWalletID, bankAccountID, amount)` + proposal
  sign (raw digest — see §2)
- `VerifyWebhookSignature(rawBody []byte, header, secret string) bool` —
  `hmac.Equal` after length check (public utility for the webhook handler)

Platform owner key: generate once at wallet setup, store **encrypted at rest**
(`BMONI_WALLET_ENCRYPTION_KEY`; Go stdlib `crypto/aes`). Demo note: this is
custodial-by-design for the platform wallet — acceptable and disclosed.

### 4.5 New models in `internal/models/`

| File | Contents |
|---|---|
| `bmoniwallet.go` | `BmoniWallet` struct + `GetPlatform(ctx)`, `Save` |
| `payments.go` | `Payment` struct + `CreatePending`, `FindPendingByAmount`, `MarkPaid(ctx, id, eventID)`, `ListForStudent` |
| `enrollments.go` | `Enrollment` struct + `Create`, `Find(studentID, courseID)`, `SetActive` |
| `webhookevents.go` | `EventRecord` + `InsertIgnore(ctx, event)` (the dedupe gate) |

Register all four in the `Models` aggregate (`internal/models/models.go`).

### 4.6 Handlers & routes (`cmd/web/routes.go`, `handlers.go`, new `bmoni_handlers.go`)

| Route | Group | Behaviour |
|---|---|---|
| `POST /student/courses/{id}/enroll` | auth + student | Validates course exists; creates `enrollments` (PENDING) + `payments` (PENDING, amount = course price); redirects to payment page with flash |
| `GET /student/pay/{paymentID}` | auth + student | Renders deposit instructions: **platform VBA number + bank + amount + reference to write as narration**, live status via `payments.status`, and a "I've paid — refresh" polling button (client polls `GET` status endpoint every 3 s) |
| `GET /student/pay/{paymentID}/status` | auth + student | JSON `{status}` for the poller (uses existing session auth — no new middleware) |
| `POST /webhooks/bmoni` | **public, registered OUTSIDE the auth groups** | Raw-body capture → HMAC verify → 401 on failure; `INSERT webhook_events ON CONFLICT DO NOTHING`; if new and `employee.deposit.completed`: find best-match PENDING payment (R-3) → `MarkPaid` + `SetActive` enrollment; respond 200 **before** heavy work; log delivery history misses to `app.logger` |
| `GET /teacher/wallet` (bonus) | auth + teacher | Balance + deposit account + payout button (only if funded); also demonstrates the payout half of BMONI |

Webhook placement gotcha: `/webhooks/*` must **not** run `app.authenticate`,
`requireRole`, or rely on session cookies — BMONI posts server-to-server.
Keep global middleware (security headers, logging, recovery); add
`r.Post("/webhooks/bmoni", app.bmoniWebhook)` next to `/health`.

Enrollment gating: `studentCourseDetail` (in `student_handlers.go`) gains an
"Enroll / Unlocked" branch driven by `enrollments.status` — the only touch to
an existing working handler, and it stays optional in the template.

### 4.7 Templates (`ui/html/student/` + `ui/html/components/`)

- `student/course.html` (exists, 155 lines): add an enroll/paywall state block
  showing price + "Enroll now" → `/student/courses/{id}/enroll` (POST form),
  or "✓ Enrolled" when `ACTIVE`. Uses existing `.Course` + new `.Enrollment`
  templateData fields.
- New `student/pay.html`: deposit card — account number (monospace, copy
  button), bank name, amount, reference-with-narration, status pill, poller.
- New `teacher/wallet.html` (bonus): balance cards, deposit account, recent
  `payments` rows. (Today every teacher template except `course-detail.html`
  is 0-byte/missing — see `doc/codebase-index.md` §8 — so a real teacher page
  is also a credibility win for the demo.)
- Extend `templateData` in `cmd/web/helpers.go` with `Enrollment`, `Payment`,
  `PaymentStatus`, `Wallet` fields (additive only).

---

## 5. Demo-day runbook (Friday, 12:00)

Total new moving parts are deliberately small; the script below is what the
judges see.

1. **Teacher demo login** → opens course "Physics 101: Newton's Laws" set at
   ₦2,500 (course price is a new local column — set via an admin/seed step).
2. **Student login** → opens same course → sees the paywall → **Enroll** →
   deposit card appears: *"Transfer ₦2,500 to <Wema-style account> — write
   `SABIFY-<course>-<student>` in the narration."*
3. **Money arrives** — in sandbox the deposit is the **BMONI test-token credit
   to the platform wallet** (requested today, see §7). The resulting
   `employee.deposit.completed` webhook hits the demo endpoint (tunnel, see
   R-2). Page flips PENDING → PAID → **course unlocked** (enrollment ACTIVE).
4. **Fallback if the webhook is slow**: the status poller + a clearly-labeled
   "Check payment status" button that calls BMONI `GET balances` (their docs
   bless polling as the fallback) and the manual-confirm path (R-3) — the demo
   never hangs.
5. **(Bonus)** Teacher wallet page shows the ₦ balance and runs one offramp to
   a test bank account, or skips if unfunded.

Pre-seed (locally, in DB): the demo teacher, the demo student, the demo course,
the platform `bmoni_wallets` row — never re-run KYC on stage.

---

## 6. What the team must do TODAY (3 Sept) — before any code

Ordered by external latency — these are the critical path items:

1. **Email `developers@bkey.me` (and/or the Formspree form on the
   request-test-tokens page)** with the phone number of the persona user you
   will create → request the sandbox **₦1,000 + $10 test-token credit**.
   Turnaround "usually within one business day" — exhibition is tomorrow noon.
2. **Get your own sandbox API key** from the Bkey developer dashboard
   (shared key works but is shared).
3. **Create the platform BMONI user with the persona verbatim**
   (Bunch Dillon / `+2348000000000` / BVN `95888168924`) — wrong names
   deliberately fail KYC.
4. Decide the tunnel for the webhook (`cloudflared`/`ngrok`) and confirm the
   venue has internet for the laptop (webhooks + `GET balances` need it).
5. Only then start coding §4 (one person, ~4–6 focused hours).

---

## 7. Risks & mitigations

| # | Risk | Mitigation |
|---|---|---|
| R-1 | **Sandbox wallets start empty; funding is manual** (support credits ₦1,000/$10, ~1 business day) | Request tokens today for the persona phone; if late, demo with the payout-free deposit flow and/or recorded replay |
| R-2 | **Webhooks need a public HTTPS URL**; local dev box isn't reachable; deliveries time out at 10 s | Tunnel (cloudflared/ngrok) on the demo laptop; acknowledge 200 immediately; keep `GET balances` polling + manual-confirm as fallbacks |
| R-3 | **No per-payment reference on deposit events** (`{userId, amount}` only) → matching a deposit to a specific student is heuristic | Match on amount + narration when available; manual-confirm admin path; audit trail records every deposit even when unmatched; demo script uses one in-flight payment to stay deterministic |
| R-4 | No **self-serve sandbox deposit trigger** (no test webhook endpoint yet) | The test-token credit fires the deposit webhook — that is the sanctioned way to exercise the handler; request it early |
| R-5 | Two signature styles (EIP-191 vs raw digest) + v+27 — the classic silent failure | Implement both against the quickstart's Go snippets; unit-test recovery of `ownerAddress` from the signature before `create-managed` |
| R-6 | KYC persona mismatch fails silently-ish ("requested item could not be found") | Use `GET /kyc/bvn-lookup` first (fetch-only plumbing test), then exact persona values |
| R-7 | Base-URL trailing `/v1` → `/v1/v1` 404s; challenge expires in 10 min; duplicates → `409` fork history | Origin-only base URL constant; fresh challenge on retry; persist `bmoniUserId` in `bmoni_wallets` |
| R-8 | 4xx webhook response = **permanent** loss of the event | Only 401 for signature forgery; return 5xx for anything transient; dedupe on `event_id` |
| R-9 | Codebase teacher surface is currently broken/empty and quiz-taking unreachable | Scope guards the demo to auth + student course page + wallet pages; don't fix unrelated flows on Friday morning |

---

## 8. Open questions for BMONI support (`developers@bkey.me`)

1. Is a per-transaction/per-invoice NGN VBA or a deposit **reference** field
   planned/available outside the docs? (Would remove R-3's heuristic.)
2. Can the sandbox simulate an incoming bank transfer to a VBA (self-serve),
   or is the manual test-token credit the only funding path today?
3. Timeline to issue our own partner API key + `partnerId` for the webhook
   subscription.

---

## 9. Source list

All BMONI claims verified 3 Sept 2026 from https://bkey.mintlify.app:
[API intro], [lifecycle], [use-cases], [integration-flow], [quickstart],
[ngn-rails], [sandbox-test-data], [webhooks], [request-test-tokens],
[no-app flow]. Repo facts from `doc/codebase-index.md` (audit 2026-09-03).
The handoff brief's NestJS/Next.js specifics are recorded for reference but do
not match this repository and were translated to Go in §4.

---

*No application code was modified while producing this plan.*

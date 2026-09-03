# Paid Course Enrollment with BMONI — Feature & Team Guide

> **Audience:** Everyone on the team — backend, frontend, product, and anyone
> presenting on Friday 4 September.
> **Last updated:** 3 September 2026.
> **Companion docs:** `doc/bmoni-integration-plan.md` (technical deep-dive,
> endpoint-level) · `doc/codebase-index.md` (repo map).
> **Repo reality:** Go + Chi + PostgreSQL + server-side HTML templates. There is
> **no separate frontend app** — "frontend" here means the Go-rendered pages and
> small vanilla-JS touches inside the same codebase.

---

## 1. TL;DR — in plain words

**What we're adding:** students can pay for a course by bank transfer, and the
app automatically unlocks the course the moment the money arrives — no manual
checking, no admin flipping a switch.

**Who moves the money:** BMONI. They give us (the platform) a Nigerian bank
"virtual account" — a normal-looking account number that forwards any transfer
into our BMONI wallet. When money lands, BMONI sends our server a secure
notification (a *webhook*), we verify it came from BMONI, mark the student's
payment as paid, and grant course access.

**What the student sees:** a course that says "₦2,500 — Enroll", a screen with
the account number + amount + a reference to write in the transfer note, then
"Payment confirmed — course unlocked" a few seconds after they pay.

**What the teacher sees (bonus):** a wallet page showing the balance and what
students have paid — and later, one-click withdrawal of their earnings to a
Nigerian bank.

**Key fact to remember:** we are **not** building our own payment system, and
students are **not** forced to create BMONI accounts. BMONI is the bank-rail
plumbing behind one platform wallet; the LMS keeps its own records on top of it.

---

## 2. The upgrade, explained simply

### The problem we're solving

Right now anyone can view every course for free, and there is no way to charge.
For the BMONI hackathon — run by the *Learn2Earn* team — the standout story is
an LMS where **learning can be sold and can pay out**. That requires a real
money rail, which is exactly what BMONI provides.

### Why BMONI and not a normal payment button

BMONI Embedded is not a Paystack-style "card payment gateway". It is a
**stablecoin wallet platform**: money moves into a user's wallet through
Nigerian bank rails (or USD/SEPA/etc.), and out again to bank accounts. This
shapes how we integrate:

- **One platform wallet collects all fees** — simplest correct design.
- Students stay ordinary app users — no per-student KYC, no per-student
  wallets. (Giving every student a BMONI account would require identity
  verification per student — wrong for an LMS.)
- Teacher/learner **payouts** are then natural: the same wallet can send NGN to
  any Nigerian bank account (that's the "Learn2Earn" half of the story).

### Where BMONI's jargon meets ours (glossary at the end)

| BMONI term | Plain meaning |
|---|---|
| Virtual Bank Account (VBA) | The account number students transfer to; it forwards into our wallet |
| Webhook | BMONI → our server notification when money arrives (signed so we can trust it) |
| KYC / BVN | Identity verification BMONI legally needs before a wallet can hold money — done **once, for the platform wallet**, during setup (test values in sandbox) |
| CNGN | BMONI's Naira-backed stablecoin — the unit our NGN wallet holds internally |
| Offramp | Turning wallet balance into a real bank transfer to a Nigerian account |

---

## 3. What the team needs to know (decisions & ground rules)

1. **The stack is Go, full stop.** Earlier documents describe NestJS/Next.js —
   they don't match this repo and are not the plan. Everything below is in Go
   terms. (Recorded for context only.)
2. **Scope is deliberately small.** We add enrollment + payment around the
   *existing* student course page. We do **not** rebuild the teacher console or
   quiz-taking this week — the demo must not depend on fixing unrelated broken
   flows.
3. **One platform BMONI wallet**, created once in the sandbox with the official
   test persona (Bunch Dillon) so KYC passes automatically. Its VBA number is
   what students "pay to".
4. **Money = Naira, tracked as kobo (integers).** Never store amounts as
   decimals/floats — a ledger rule.
5. **Every incoming BMONI notification is recorded once** (event-id dedupe).
   If the same notification arrives twice, the student is enrolled once.
6. **We trust BMONI only after signature verification.** Their webhook is
   HMAC-signed; unverified payloads are rejected with 401.
7. **Sandbox money is fake but real-flow.** BMONI credits test funds manually
   (₦1,000 + $10 per request, ~1 business day) — request **today**.
8. **The demo has a no-hang guarantee.** Polling the balance + a manual confirm
   button back up the webhook, so the live demo can never get stuck waiting.
9. **Money-in is the core; payouts are the bonus beat.** Course payment →
   auto-enroll is Friday's must-work demo. Teacher withdrawal showcases BMONI's
   outbound rail if funded.

### Team checklist before/at kickoff

- [ ] Email `developers@bkey.me` — request sandbox test-token credit for
      persona phone `+2348000000000` (**today** — ~1 business day).
- [ ] Obtain our own sandbox API key.
- [ ] Create the platform BMONI user with persona values **verbatim**.
- [ ] Choose the webhook tunnel (cloudflared/ngrok) for the demo laptop.
- [ ] Read `doc/bmoni-integration-plan.md` §4 before writing code.

---

## 4. What changes in the app (by area)

All changes are additive; the core learning flow is untouched.

### Database (one new migration: `002_bmoni.sql`)

| New table | Purpose |
|---|---|
| `bmoni_wallets` | The platform's BMONI wallet record (user id, wallet address, VBA number, status) |
| `payments` | One row per enrollment attempt: student, course, amount (kobo), status `PENDING → PAID`, narration reference |
| `enrollments` | Student ↔ course access: `PENDING → ACTIVE` (this is the missing enrollment concept the app never had) |
| `webhook_events` | Replay-safe log of every BMONI notification we processed (dedupe key) |

Plus: a **price** (`price_kobo`, nullable) on `courses` so a course can be free
or paid.

### Backend (Go)

| Where | Change |
|---|---|
| `cmd/web/main.go` + `.env.example` | New config/env: `BMONI_BASE_URL`, `BMONI_API_KEY`, `BMONI_WEBHOOK_SECRET`, `BMONI_WALLET_ENCRYPTION_KEY` |
| `internal/bmoni/` (new package) | Thin HTTP client to BMONI + the two Ethereum signing helpers (official Go examples) — user/wallet/KYC/balances/offramp methods |
| `internal/models/` (new files) | `bmoniwallet.go`, `payments.go`, `enrollments.go`, `webhookevents.go` — each owns its SQL, house style |
| `cmd/web/bmoni_handlers.go` (new) | Enrollment + payment-status handlers + the **webhook receiver** |
| `cmd/web/routes.go` | Register `POST /webhooks/bmoni` **outside** the login-protected groups; add student pay/enroll routes |
| `cmd/web/helpers.go` | Extend `templateData` with `Payment`, `Enrollment`, `Wallet` fields (additive) |

### Frontend (server templates + small JS — same repo)

| Where | Change |
|---|---|
| `ui/html/student/course.html` | Paywall / "Enroll" state per course price; "✓ Enrolled" when active |
| `ui/html/student/pay.html` (new) | The deposit screen: account number (copy button), bank, amount, reference text, live status |
| `ui/html/teacher/wallet.html` (new) | Bonus: balance, deposit account, incoming payments ledger |
| `ui/static/js/pay-status.js` (new) | Tiny poller (every 3 s) that flips the page to "Payment confirmed" — no framework |

### Not changing

Auth, roles, quizzes, results, study groups, the AI story, the landing page —
all stay as-is.

---

## 5. Frontend (FR) & Backend (BE) requirements

Because this is a single Go app, "frontend" and "backend" here are layers, not
separate deployments. Handoff requirements:

### Frontend requirements (templates + JS layer)

| FR-# | Requirement | Acceptance |
|---|---|---|
| FR-1 | Course card/detail shows a price badge when a course is paid, and an **Enroll (₦X)** button for non-enrolled students | Visible on `student/course.html`; enrolled students see "✓ Enrolled" instead |
| FR-2 | Enroll click → POST that creates the local payment + enrollment (PENDING) and lands the student on the payment page | Server redirect; no JS required for this step |
| FR-3 | Payment page displays: platform **account number**, **bank name**, exact **amount**, and the **reference string** to write in the transfer narration — with a copy button | All four present and copyable (`student/pay.html`) |
| FR-4 | Payment page shows a live status pill: `PENDING → PAID` with a success message when confirmed | Poller (`pay-status.js`) checks a JSON status endpoint; no page refresh |
| FR-5 | Once PAID, course content is reachable; navigation shows the course as active | Enrollment state read from the DB on every page |
| FR-6 | Graceful states: "payment failed / expired", and a clearly-labelled **"I've paid — check now"** manual button | Works even if the webhook is delayed (calls BE status/balance fallback) |
| FR-7 | Teacher wallet page (bonus) shows NGN balance, deposit account, and payment history | Rendered server-side from `payments` + BMONI balance |

### Backend requirements (Go layer)

| BE-# | Requirement | Acceptance |
|---|---|---|
| BE-1 | BMONI client: `CreateUser`, wallet provisioning (owner-proof challenge → **EIP-191 signature** → `create-managed`), KYC (BVN lookup → PATCH → activate), `start-nigeria`, balances, VBA read | Follows BMONI's official Go snippets; base URL **origin-only**; persists `bmoniUserId` |
| BE-2 | Platform wallet bootstrap routine (run once): persona user → wallet → KYC → rail → store VBA | Idempotent — re-running recovers existing state instead of forking |
| BE-3 | `POST /student/courses/{id}/enroll`: validates price, creates `payments` (PENDING, kobo) + `enrollments` (PENDING), redirects to payment page | Unique reference `SABIFY-<course>-<student>`; concurrent clicks cannot double-charge |
| BE-4 | `GET /student/pay/{paymentID}/status` JSON | Returns `{status}` for the poller; auth-scoped to the owning student |
| BE-5 | **Webhook receiver** `POST /webhooks/bmoni`: read **raw body** → HMAC-SHA256 verify with `BMONI_WEBHOOK_SECRET` → 401 if bad → insert event-id (dedupe) → on `employee.deposit.completed`, match to PENDING payment → mark PAID + enrollment ACTIVE → return 200 fast | Never re-enrolls on replay; never 4xx on transient failures; acknowledged within 10 s |
| BE-6 | Payment → enrollment matching with audit trail: every deposit logged even if it matches nothing | Manual-confirm admin path resolves unmatched deposits |
| BE-7 | Money is kobo `BIGINT` end-to-end; course price column added | No float math anywhere in the ledger |
| BE-8 | Bonus: teacher payout — verify bank → register withdrawal account → offramp proposal → raw-digest signature | Same signing discipline as BE-1 |

---

## 6. How it works with the app — end to end

```
                        SABIFY (Go app)                    BMONI
   Student                  │                               │
      │                     │                               │
      │ 1. Enroll (₦2,500)  │                               │
      ├────────────────────►│ 2. create payments (PENDING)  │
      │                     │    + enrollments (PENDING)    │
      │ 3. "Pay ₦2,500 to   │                               │
      │    account 1234…"   │                               │
      ◄─────────────────────┤                               │
      │                     │                               │
      │ 4. Student transfers│ 5. bank rail forwards money   │
      │    from their bank ─┼──────────────────────────────►│
      │                     │    into platform NGN wallet   │
      │                     │                               │
      │                     │ 6. webhook: deposit.completed │
      │                     │◄──────────────────────────────┤
      │                     │ 7. verify HMAC + dedupe       │
      │                     │ 8. payments → PAID            │
      │                     │    enrollments → ACTIVE       │
      │ 9. poller: "Paid ✓" │                               │
      ◄─────────────────────┤                               │
      │ 10. course unlocked │                               │
```

Numbered steps, in words:

1. A student opens a paid course and taps **Enroll (₦2,500)**.
2. The Go backend records a pending payment and pending enrollment locally
   (this is also our audit record).
3. The app shows the payment screen: the **platform's BMONI virtual account
   number**, the bank name, the amount, and the reference to include.
4. The student makes a normal bank transfer from their banking app.
5. BMONI's rails credit our platform wallet (the transfer hits our VBA).
6. BMONI notifies our server: `employee.deposit.completed` with the amount.
7. Our server **verifies the HMAC signature** (proves it's really BMONI) and
   records the event id once (replays are ignored).
8. The matching pending payment flips to **PAID** and the enrollment flips to
   **ACTIVE**.
9. The student's open payment page notices the change (3-second poller) and
   shows **"Payment confirmed — course unlocked."**
10. Course materials/quizzes are now accessible to that student.

Fallbacks so the demo can never stall: if the webhook is delayed, step 9's
"Check now" button queries BMONI directly for the balance, and an admin
manual-confirm resolves anything unmatched. Every deposit — matched or not —
is stored, so the ledger always reconciles.

---

## 7. How a user uses it (walkthroughs)

### Student — buying a course

1. Log in → open a course (e.g., "Physics 101", price ₦2,500).
2. See **Enroll for ₦2,500** instead of instant free access.
3. Tap it → a screen appears: *"Transfer exactly ₦2,500 to account
   **0123456789** (Providus Bank). Write **SABIFY-Physics101-Amara** in the
   narration."* — with a copy button.
4. Open their banking app, send the transfer (real money in production; test
   funds in the sandbox demo).
5. Return to the tab (or it auto-refreshes): within seconds the status flips
   to **"Payment confirmed 🎉"** and the course is open.
6. Learn as before — quizzes, results, study groups are unchanged.

> No BMONI account, no extra signup, no card. It feels like paying for any
> course online.

### Teacher — seeing the money (bonus)

1. Log in → **Wallet** page in the teacher area.
2. See the NGN balance (what students have paid in), the deposit account
   number, and a list of student payments with statuses.
3. Later: enter a bank account, verify it, and request a withdrawal — BMONI
   sends the NGN to the bank. ("Learn2Earn": the platform can also push small
   rewards to high-achieving students the same way.)

### Admin/platform — setup (done once, before launch/demo)

1. Run the bootstrap once: BMONI user (sandbox persona) → wallet → KYC → rail.
2. The returned VBA number is what students pay into — stored in
   `bmoni_wallets` and displayed by the payment page.
3. Register the webhook URL + secret with BMONI; keep the secret in env.
4. Mark courses paid by setting a price.

---

## 8. Friday demo script (3–5 minutes)

1. **Teacher login** → show course "Physics 101: Newton's Laws" priced ₦2,500.
2. **Student login** → paywall visible → **Enroll** → deposit screen appears
   (account number, amount, reference).
3. **Money arrives** → in sandbox this is BMONI's test-token credit to the
   platform wallet (requested ahead). The webhook fires; the page flips to
   "Payment confirmed"; the course unlocks live.
4. If the webhook is slow, hit **"Check now"** (falls back to BMONI balance) —
   the demo never hangs.
5. **(Bonus)** Teacher wallet page → balance shows the ₦2,500 → one offramp to
   a test bank account.
6. **The story for judges:** *"We built a two-sided learning economy — students
   buy courses instantly via bank transfer with zero manual admin, teachers get
   paid automatically, and BMONI's rails power the money movement end to end."*

---

## 9. Roles & quick reference

| Role | Does what |
|---|---|
| Backend (Go) | BMONI client + signing, migration/002, models, webhook receiver, enroll/pay handlers (BE-1…BE-8) |
| Frontend (templates/JS) | Paywall on course page, payment screen, poller, teacher wallet page (FR-1…FR-7) |
| Product/presenter | §7 stories, §8 demo script, ensures test tokens requested |
| Everyone | Read §3 checklist; know the glossary below |

### Glossary (plain English)

| Term | Meaning |
|---|---|
| **API key** | The secret that says our app is allowed to talk to BMONI |
| **Sandbox** | BMONI's test environment — fake money, real flows |
| **VBA** (Virtual Bank Account) | The account number we show students; transfers to it land in our wallet |
| **Wallet** | BMONI's per-user balance store (ours holds CNGN, Naira-backed) |
| **KYC / BVN** | Identity checks BMONI must run before a wallet can receive money — done once for our platform wallet (sandbox persona makes it instant) |
| **CNGN** | BMONI's Naira stablecoin — internal unit of the NGN wallet |
| **Webhook** | A secure server-to-server notification BMONI sends us when money arrives |
| **HMAC signature** | The cryptographic stamp on each webhook that proves it came from BMONI |
| **Idempotent / dedupe** | Handling the same notification twice produces the same result once — no double enrollments |
| **Offramp / payout** | Turning wallet balance into a bank transfer to a real Nigerian account |
| **Reference / narration** | The note the student writes on their transfer so we can identify it |
| **Kobo** | 1/100 of a Naira — amounts stored as integer kobo, never decimals |

---

## 10. FAQ

**Q: Do students need to install anything?**
No app, no BMONI account, no card. They just make a normal bank transfer.

**Q: Is the platform wallet custodial?**
In this design, yes — BMONI's key stays in our backend (encrypted at rest).
That's the standard shape for a business receiving payments, and it's what the
sandbox quickstart demonstrates. Consumer-facing self-custody (their mobile
SDKs) is a separate, later product direction.

**Q: What if a student pays but the webhook never arrives?**
Two fallbacks: the payment page's "Check now" button queries BMONI directly,
and an admin can manually confirm with full audit trail. The demo cannot hang.

**Q: What if two students pay the same amount at the same time?**
Deposits without a matching narration fall to a manual-confirm queue; the
reference text on each payment page makes the human match trivial. (Flagged as
a known limitation of the current BMONI API — no per-invoice reference yet.)

**Q: Why don't we give every student a BMONI wallet?**
Each would need identity verification (BVN etc.) before holding money — wrong
friction for an LMS. One platform wallet keeps buying a course as simple as
paying for anything online.

**Q: Real money or fake?**
Sandbox only until production keys are issued. All flows are identical.

---

*Maintainers: update this guide when scope or decisions change. The endpoint
and payload-level truth lives in `doc/bmoni-integration-plan.md`.*

# Per-Teacher BMONI Wallets with In-App KYC

> **Status:** ✅ **Implemented** (2026-09-04). Migration `005`, model, BMONI
> client, handlers, routes, wallet/kyc UI and e2e coverage all landed.
> Implementation notes that differ from the design below are flagged inline.
>
> **Model change:** from one platform-owned wallet that collects every fee, to
> **each teacher owning her own BMONI wallet** — created via an in-app KYC
> wizard, funded directly by her students, and withdrawable to her own bank
> account.
>
> This page lists **everything that needs to be done** to build it. The core
> flow was exercised end-to-end against `embedded-dev.bmoni.com` during
> research; every endpoint, field name, and gotcha below is from that live
> run, not from guessing.

---

## 1. Why this model (and the one constraint to know first)

The current implementation uses a **platform wallet** (one BMONI user, one VBA,
every course fee lands there). That is simpler but has two problems: every
teacher shares one account number on the pay page, and payouts require a
platform-level owner key (which the current sandbox wallet cannot sign with —
see `doc/bmoni-implementation.md` §8).

**The per-teacher model fixes both:** a teacher's students see *her* account
number, and *she* withdraws with *her* owner key.

**The one constraint — read this before building anything:** BMONI's sandbox
verifies identity **only against two test personas** (Bunch Dillon / Samson
Jabo). Any name + BVN pair that is not a persona is *deliberately* failed —
that is the sandbox behaving correctly. It is **not** possible to complete a
real teacher's KYC in the sandbox today. Options for the demo:

1. **Create demo teachers whose identity matches a persona** (name "Bunch
   Dillon", BVN `95888168924`, a *fresh unique phone*). Verified working — the
   persona name+BVN is what matches, the phone just needs to be unused.
2. Request a **dedicated sandbox key** from `developers@bkey.me` — a fresh
   partner namespace still only resolves the two personas, but gives you clean
   users and room to re-run the wizard.
3. Production: the same code path works with real BVNs/names once BMONI
   provisions real identity verification for the production key.

**Everything in this doc is written so the demo uses option 1** and production
needs no structural change.

---

## 2. The end-to-end flow (verified live)

```
Teacher (in SABIFY)                    SABIFY backend                  BMONI sandbox
   | 1. fills KYC wizard:                  |                               |
   |    personal + address + BVN           |                               |
   |    uploads ID front/back, POA         |                               |
   +-------------------------------------->| 2. POST /v1/users             |
   |                                      +------------------------------->|
   |                                      |<-- bmoniUserId                |
   |                                      | 3. PATCH /v1/users/{id}/kyc    |
   |                                      +------------------------------->|
   |                                      |<-- saved (needs address key!) |
   |                                      | 4. 3x document uploads        |
   |                                      +------------------------------->|
   |                                      | 5. wallet: owner key + proof  |
   |                                      +------------------------------->|
   |                                      |<-- smartWalletId + address    |
   |                                      | 6. start-nigeria (bvn, addr)  |
   |                                      +------------------------------->|
   |                                      | 7. read VBA (poll)            |
   |   <-- "wallet ready: acct 6177…" ----+<-- VBA number                 |
```

Steps verified live, in this order, against the sandbox:

| # | Call | Notes from the live run |
|---|---|---|
| 1 | `POST /v1/users` | body `{firstName, lastName, email, phoneNumber, bvn}`. Use persona name + BVN and a **fresh phone** (a taken phone → `409`). |
| 2 | `PATCH /v1/users/{id}/kyc` | body `personalInfo` (firstName/lastName/dateOfBirth/gender), `address` (**not** `addressDetails` — the API rejects the latter; docs' curl examples are stale), `identificationNumbers: [{type:"bvn", number, issuingCountryCode:"NGA"}]`, `sourceOfFunds`. |
| 3 | `POST /kyc/documents/identification` | multipart field `files`; `type` from `GET /kyc/options` (`passport`, `national_id`, `driving_license`, …); `documentNumber`, `issuingCountry`, `expirationDate`, `issueDate`. File must be **≥ 2KB** (a 229-byte PNG was rejected). |
| 4 | `POST /kyc/documents/proof-of-address` | multipart field `files`; `type` ∈ `utility_bill|bank_statement|rental_agreement|tax_document|other`. |
| 5 | `POST /kyc/documents/biometric` | multipart field **`selfie`** (not `files`) + `type` (`selfie`, `liveness_check`, …). Readiness lists it as required even though the NGN doc calls it optional — upload it. |
| 6 | `GET /kyc/readiness` | must return `{"ready":true}`. Missing items are listed — fix them rather than pushing on. |
| 7 | `POST /smart-wallets/owner-proof-challenges` | response field is **`challengeId`** (the client originally read `id` — fixed). |
| 8 | `POST /smart-wallets/create-managed` | currency `CNGN`. Response field is **`walletAddress`** (client originally read `address` — fixed). One wallet per currency per user: a second CNGN wallet → `409 E502`. |
| 9 | `POST /onboarding/start-nigeria` | body `{bvn, ngnWalletAddress, ngnWalletIndex:0}`. **No `kyc/activate` needed** — verified: rail + VBA provisioned without it. |
| 10 | `GET /bank-accounts/deposit-accounts/NGN` | response is `{accounts:[…]}` (client originally read `{account:…}` — fixed); take the first NGN account. |

Result observed live: **VBA `6177463833` (9 Payment Service Bank)**, wallet
address `0xF8dD4b6E…`, onboarding `anchorStatus: active`.

---

## 3. Database changes (`migrations/005_teacher_wallets.sql`)

`bmoni_wallets` is currently a platform singleton. Make it per-teacher:

```sql
ALTER TABLE bmoni_wallets ADD COLUMN user_id uuid REFERENCES users(id);
ALTER TABLE bmoni_wallets ADD COLUMN kyc_status text NOT NULL DEFAULT 'not_started';
-- kyc_status: not_started | profile_saved | documents_uploaded | rail_active | failed
ALTER TABLE bmoni_wallets ADD COLUMN kyc_error text;
-- owner_key_enc now holds THIS teacher's key, sealed with the same
-- BMONI_WALLET_ENCRYPTION_KEY (one master key seals many teacher keys).
-- vba_account_number / vba_bank_name are THIS teacher's deposit account.

-- A teacher can only ever have one wallet row.
CREATE UNIQUE INDEX IF NOT EXISTS bmoni_wallets_user_uidx ON bmoni_wallets(user_id)
  WHERE user_id IS NOT NULL;
```

Keep the existing platform row semantics (`user_id IS NULL` = legacy) or drop
them once every wallet belongs to a teacher.

### Model changes (`internal/models/bmoniwallet.go`) — ✅ done
- `GetByUserID(ctx, userID)` — the teacher's wallet (new primary accessor).
- `GetByBmoniUserID(ctx, id)` — the webhook resolves a deposit's `userId` to
  the owning teacher's row.
- `GetPlatform` — kept only as the webhook fallback for legacy rows
  (`user_id IS NULL`), where deposits match any pending payment.
- `Save` — teacher rows (`user_id` set) upsert **on the teacher's `user_id`**
  (partial unique index), because a wizard retry carries a fresh BMONI user
  id; legacy platform rows keep the `bmoni_user_id` conflict target.
- New: `SetKYCStatus(ctx, userID, status, errText)`.
- Added a `bvn` column (migration `005`): the BVN is captured at the profile
  step and re-supplied to `start-nigeria` when the rail is provisioned after
  the document uploads.
- The `ORDER BY (owner_key_enc IS NOT NULL)` preference stays (a wallet that
  cannot sign is never picked over one that can).

---

## 4. BMONI client additions (`internal/bmoni/`)

Already landed during verification (used by the wizard + probes):

- `lifecycle.go`: `LookupBVN` (returns the holder record), `SubmitKYC`
  (fixed `address` shape + optional BVN identification number),
  `ActivateKYC(ctx, userID, sumsubLevelName)` (signature changed — a level is
  required even for NGN if you call it), `KycReadiness`,
  `UploadKycDocument`, `OnboardingStatus`.
- `client.go`: `ResolveUserByPhone`, `BindVBA`, `doMultipart`
  (`files` vs `selfie` field name handled per kind), `Balances`,
  `DepositAccount` (fixed `accounts[]` shape).

Client methods added for the teacher flow (all landed):

| Method | Endpoint | Purpose |
|---|---|---|
| `CreateUser` | exists | returns `bmoniUserId`; the wizard recovers a 409 via `ResolveUserByPhone` |
| `ListWallets` | `GET /v1/users/{id}/smart-wallets/account/wallets` | **new** — read before `create-managed` and adopt an existing CNGN wallet instead of retrying into a `409 E502` |
| `WaitForVBA` | wraps deposit-account polling | **new** — server-side poll of the NGN deposit account after `start-nigeria` |
| `UploadKycDocument` | exists | the three uploads (identification / proof-of-address / biometric) |
| `SubmitKYC` | PATCH `/kyc` | **extended** — now sends `sourceOfFunds` (the API silently drops free text; the wizard posts a code) |

`GetKycOptions` / `GetOccupations` were **not** added: the wizard's selects are
hardcoded to the documented enum values (genders, source-of-funds codes, ID
document types). Fetching options per page load would add a BMONI round-trip
to every render and force the e2e stub to serve them too, for zero benefit on
a static enum.

---

## 5. Handlers + routes (`cmd/web/kyc_handlers.go`) — ✅ done

New file `cmd/web/kyc_handlers.go`, all under the teacher role group:

| Handler | Route | Purpose |
|---|---|---|
| `teacherKYCPage` | `GET /teacher/wallet/kyc` | wizard entry: shows the current `kyc_status` and renders the form(s) for the next step |
| `teacherKYCProfile` | `POST /teacher/wallet/kyc/profile` | create the BMONI user (or recover via `ResolveUserByPhone` on 409) + submit the profile; store `bmoni_user_id`/`bvn`, advance to `profile_saved` |
| `teacherKYCDocuments` | `POST /teacher/wallet/kyc/documents` | multipart upload of ID + proof-of-address + selfie → `readiness` → **provision synchronously**: owner key → `ListWallets`/`create-managed` → `start-nigeria` → `WaitForVBA` → `rail_active` |
| `teacherWallet` | existing | now shows the **teacher's own** VBA + live balance + withdraw form |
| `teacherWalletWithdraw` | existing | loads **the teacher's own** wallet (`GetByUserID`) so signing uses her owner key |

**Deviation from the design:** provisioning runs synchronously inside the
documents POST (bounded by a 90s context), so no separate
`GET /teacher/wallet/kyc/status` poll endpoint was needed — `WaitForVBA`
polls BMONI server-side, and a refresh of the wizard/wallet page always shows
the true DB state. Failures keep the wizard on the right form: profile-step
errors leave `kyc_status='failed'` (profile form re-shown), documents-step
errors leave `documents_uploaded` + `kyc_error` (documents form re-shown,
re-submission resumes from the last completed call).

### Behavior rules
- A teacher without a wallet sees a **"Set up your wallet"** card on the
  wallet page instead of the balance/withdraw sections.
- KYC is **owned by the teacher in the app**: nothing happens on BMONI until
  she submits the wizard. A failed step leaves `kyc_status='failed'` +
  `kyc_error` (surface it verbatim) and lets her re-submit that step.
- The wizard must run server-side in order and never retry `create-managed`
  blindly (one CNGN wallet per user; a blind retry → second wallet attempt →
  `409 E502` — read the wallet list first, per the docs' "Retries and
  duplicates").

---

## 6. Payment + webhook changes (the actual money flow) — ✅ done

### Where the pay page gets the account number
`cmd/web/bmoni_handlers.go` `studentPay` loads **the course's teacher's**
wallet (`GetByUserID(course.TeacherID)`). A paid course whose teacher has no
wallet yet renders "This course's teacher hasn't activated payments yet"
instead of a broken page.

### Webhook deposit resolution
`processBmoniDeposit` resolves the deposit's BMONI user to a local teacher:

1. Find the `bmoni_wallets` row whose `bmoni_user_id = payload.userId`
   (`GetByBmoniUserID`); unknown users are ignored and recorded.
2. Among **that teacher's** unresolved PENDING payments (`FindPendingForDeposit`
   now filters by the wallet's owner), match `amount`.
3. Mark paid + activate access + record the enrollment (unchanged).

Legacy rows (`user_id IS NULL`) still match any pending payment, preserving
pre-per-teacher behaviour for the platform wallet until it is retired.

This is strictly better than the platform model: deposits can no longer be
mis-credited across teachers (covered by e2e case 6d), and the amount match
becomes a second check instead of the only one.

### Narration / reference
Keep the `SABIFY-<course>-<student>` reference for human reconciliation.
Still no per-payment reference on BMONI's side; one in-flight payment per
teacher keeps the demo deterministic (same caveat as §10 of the implementation
doc).

---

## 7. UI work — ✅ done

- **KYC wizard page** (`ui/html/teacher/kyc.html`): a single page driven by
  `kyc_status` — Step 1 profile form (personal + address + BVN + source of
  funds + phone), Step 2 documents form (ID type/number, ID image, proof of
  address, selfie — three files, all ≥2KB enforced server-side), an error
  card that surfaces `kyc_error` verbatim, and a success panel showing the
  provisioned account once `rail_active`.
- **Wallet page** (`ui/html/teacher/wallet.html`): no wallet → "Set up your
  wallet" CTA card linking to the wizard; wizard started but not finished →
  "Continue setup" card with the exact `kyc_status`/error; `rail_active` →
  the teacher's account number, live BMONI balance, a **recent-activity list**
  (BMONI `GET /smart-wallets/{id}/transactions`, best-effort like the
  balance), and the withdrawal form.
- **Pay page** (`ui/html/student/pay.html`): markup unchanged, but a course
  whose teacher has no wallet renders "This course's teacher hasn't activated
  payments yet" instead of the old platform message.
- No poller JS: provisioning is synchronous (see §5).

---

## 8. Withdrawal (unchanged mechanics, per-teacher key) — ✅ done

`teacherWalletWithdraw` implements verify → register → offramp → approve →
sign (raw digest, v=27/28) → submit → poll, and now loads **the teacher's own
wallet** (`GetByUserID`) instead of the platform one, so signing uses her
own owner key (sealed onto her `bmoni_wallets` row by the wizard). The wallet
must be `rail_active` before the form is reachable at all.

---

## 9. What the demo needs (checklist)

- [x] A teacher **created with persona identity** — done live 2026-09-04:
      teacher "Bunch Dillon" (BVN `95888168924`, fresh phone
      `+2348123456702`) and teacher "Samson Jabo" (BVN `22222222222`, DOB
      `1995-07-07`, fresh phone `+2348123456704`) both provisioned through
      the in-app wizard (see §12). Name does not need to be the persona's;
      the persona BVN is what the sandbox matches.
- [x] A second teacher persona for the "learn-to-earn / peer payout" demo —
      Samson Jabo is provisioned (same row as above).
- [x] Test images ≥2KB for the three document uploads — generate with
      `go run ./tools/livepass genimg` (writes ≥300KB PNGs to
      `tmp/livepass/imgs/`).
- [ ] The app can reach BMONI from wherever it runs (webhook URL for the
      deposit event; the docs require a public HTTPS `callbackUrl` — use
      cloudflared/ngrok on demo day).
- [ ] Sandbox test tokens are credited manually (~1 business day) — request
      them with the persona phone number that will actually receive the
      deposit, or drive the demo with the documented webhook simulation.

## 10. Build order — status

1. ✅ Migration `005` + model `GetByUserID`/`Save` changes (§3).
2. ✅ Handlers for the wizard state machine + routes (§5) with the client
   methods in §4.
3. ✅ `studentPay` + webhook to per-teacher resolution (§6).
4. ✅ Wallet page CTA + KYC wizard UI (§7).
5. ✅ e2e: `cmd/web/bmoni_e2e_test.go` now seeds a **teacher-owned** wallet and
   proves cross-teacher deposit isolation (6d); new `kyc_wizard_e2e_test.go`
   runs the whole wizard (register → profile → documents → provisioning)
   against a stub BMONI HTTP server and asserts the `rail_active` row + UI.
6. ✅ Manual live pass against the sandbox with a persona teacher — done
   2026-09-04 with two fresh persona teachers (wizard → deposit → withdraw),
   incl. three bugs found and fixed live. Evidence and sandbox notes in §12.

## 11. Verified live-run summary (evidence)

### Live wizard run (2026-09-04, shared sandbox key, teacher "Sabify Team")

The full in-app wizard was run against the real sandbox with the teacher
named **"Sabify Team"** and persona BVN `95888168924` (fresh phone):

| Step | Result |
|---|---|
| Create BMONI user | ❌→✅ **client bug fixed**: `POST /v1/users` returns `{user:{bmoniUserId,…}}`, the client decoded a top-level `id` → empty → PATCH hit `/v1/users//kyc` (404). Decode now reads `user.bmoniUserId` with `user.id` fallback |
| KYC profile PATCH | ✅ saved (name "Sabify Team" + persona BVN accepted) |
| Document uploads | ❌→✅ **client bug fixed**: multipart file part carried `application/octet-stream` → `E101`; the API requires a real image MIME type (curl sniffs `image/png`). `doMultipart` now sniffs PNG/JPEG magic bytes. Also: `passport` needs `issueDate`/`expirationDate` (E101 without) — the wizard now defaults them; upload enum is `drivers_license` (not `driving_license`) |
| Wallet creation | ❌→✅ **handler bug fixed**: `ListWallets` reports currency `NGN` while `create-managed` takes `CNGN` — the adoption check never matched, racing into a second wallet (`409 E502`). Adoption now accepts `NGN`/`CNGN` |
| start-nigeria + VBA | ✅ **real bank account issued: VBA `6177463833` @ 9 Payment Service Bank**, wallet `95ac77a9-…`, row `rail_active` |
| Wallet page | ✅ renders the VBA, live balance `₦0` (unfunded), activity list, and the withdraw section (owner-key notice, since the adopted wallet's key was created by a throwaway) |

**Takeaway for the demo:** the sandbox issues a real VBA when the profile
carries a persona BVN — the name does not need to be the persona's name.
Any non-persona BVN still fails verification, and a real production key would
resolve real BVNs with no code change.

### Probe evidence (research phase)

| Attempt | Result |
|---|---|
| Create user, persona name + fresh phone | ✅ user created, partner "BMONI Hackathon" |
| PATCH /kyc with `addressDetails` | ❌ `400 property addressDetails should not exist` |
| PATCH /kyc with `address` | ✅ `{"saved":{personalInfo,address,…}}` |
| Upload 3 documents (real PNGs ≥2KB) | ✅ all accepted; readiness `{"ready":true}` |
| `kyc/activate` with no body | ❌ requires `sumsubLevelName` |
| `kyc/activate` id-and-liveness | ✅ `{"activated":true}` (not required for NGN — see below) |
| Wallet + rail **without** activate | ✅ VBA provisioned — activate is optional for NGN |
| create-managed response field | `walletAddress` (client decoded `address` → empty — fixed) |
| Second CNGN wallet, same user | ❌ `409 E502` — one wallet per currency |
| `start-nigeria` with empty address | ❌ misleading error demands `usdWalletAddress` — it wants a real `ngnWalletAddress` |
| Final state | ✅ VBA `6177463833` (9 Payment Service Bank), wallet `0xF8dD4b6E…`, anchor active |

## 12. Live full pass (2026-09-04) — done

The whole wizard → deposit → withdraw flow was run end-to-end against the
real sandbox from a **real server on `:4000`** (embedded PostgreSQL, the real
app binary, no stubs). Bank transfers were simulated with correctly signed
`employee.deposit.completed` webhook deliveries, as in §6 of
`doc/bmoni-implementation.md`.

### Teachers provisioned live

| Teacher | Identity sent to BMONI | Result |
|---|---|---|
| "Bunch Dillon" (app email `bunch.livepass@example.com`) | persona BVN `95888168924`, fresh phone `+2348123456702` | first wizard run failed (see bug 1) → **re-run succeeded**: `rail_active`, owner key sealed, VBA issued |
| "Samson Jabo" (app email `samson.livepass@example.com`) | persona BVN `22222222222`, DOB `1995-07-07`, fresh phone `+2348123456704` | full wizard **succeeded first try**: `rail_active`, owner key sealed, VBA issued |

Both rows ended `rail_active` with the owner key encrypted at rest. Note: the
sandbox issued **the same VBA `6177463833` @ 9 Payment Service Bank** to both
users — it recycles the number across persona-matched users (see sandbox
notes below).

### The money flow (teacher Samson)

1. Wizard: profile PATCH ✅ → 3 document uploads ✅ → readiness `ready:true`
   ✅ → smart wallet ✅ → `start-nigeria` ✅ → VBA ✅.
2. Paid course ₦2,500 created; a student enrolled and was redirected to the
   pay page, which rendered the teacher's own VBA `6177463833`, bank, `₦2500`
   and the `SABIFY-*` reference. Status `PENDING`.
3. Webhook negatives all held live: **forged signature → `401`**; a deposit
   to the *other* teacher's BMONI user with the exact amount → `200`, payment
   stayed `PENDING` (cross-teacher isolation); a wrong-amount deposit to the
   right user → `200`, still `PENDING`.
4. Real signed delivery (correct BMONI user + `2500.00`) → `200`; payment
   `PAID` with `paid_at` set; **replaying the same event id → `200`** with no
   double-processing.
5. Samson's wallet page: confirmed earnings `₦2500`; Bunch's wallet: `₦0`.
6. Withdraw (Samson's own owner key, ₦100 → account `0123456789` @ bank code
   `000013`): bank list ✅ → **account verification ✅** → withdrawal-account
   registration ✅ → offramp proposal creation blocked by BMONI
   `403 E503 “not enough resources … check your balance”` — correct wall for
   an **unfunded** wallet (sandbox test tokens are credited manually).

### Bugs found and fixed during the pass

All three fixes are in the uncommitted per-teacher batch (see repo status):

1. **Brand-new teachers could never be provisioned.** BMONI's
   `GET /v1/users/{id}/smart-wallets/account/wallets` returns
   `400 “No embedded smart wallet group found for this user. Call POST
   …/owner-proof-challenges first.”` until the first owner-proof challenge
   creates the group; `ensureSmartWallet` treated that as fatal, so the
   wizard died before ever requesting a challenge. Fix: new
   `isNoWalletGroupErr` helper in `cmd/web/kyc_handlers.go` treats that 400
   as “zero wallets to adopt” and the flow proceeds to create the group.
2. **NigerianBank decode mismatch.** The client decoded `{name, code}` but
   the live API returns `{bankName, bankCode}` — every bank-code lookup
   failed (“That bank code was not found in BMONI's supported list”) and
   withdrawals died before verification. Fix: struct tags corrected in
   `internal/bmoni/client.go`. The e2e stub never serves `nigerian-banks`,
   which is why this slipped through.
3. **Wallet page swallowed flash messages.** `ui/html/teacher/wallet.html`
   never rendered `.Flash`, so withdraw/profile feedback was invisible after
   the redirect. Fix: top-level flash block on the wallet page.

### Sandbox notes (things that differ from the docs / earlier write-ups)

- The sandbox `nigerian-banks` list uses **BMONI's own 6-digit codes**, not
  CBN 3-digit codes: GTBANK PLC is `000013`, and `058` (the CBN code) is not
  in the list. The withdraw form's placeholder (“e.g. 058 for GTBank”) is
  misleading against the sandbox — production CBN codes should restore the
  hint's accuracy.
- `verify-nigerian-account` did **not** return `E101` for a made-up account
  on a fresh persona teacher (the 2026-09-03 platform-wallet note said it
  would): `0123456789` @ `000013` verified and registered fine, and the wall
  moved to offramp creation (`E503`, empty wallet).
- The sandbox **recycles the same VBA** (`6177463833` @ 9 PSB) across
  persona-matched users — both teachers in this pass hold the same account
  number. Deposits stay isolated per BMONI `userId` (the webhook resolves the
  teacher by user id, never by account number), but for the demo keep one
  funded persona active so account-number ambiguity never matters.

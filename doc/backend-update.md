# SABIFY Backend Setup & Documentation (Updated)

> **Supersedes:** `backend.md` (which reflected the original planned
> Next.js/NestJS/Python stack and pre-enrollment wiring). This update documents
> the **actual current setup**: Go + Chi + pgx + `html/template` + `scs`, a
> Docker Postgres gated behind `sudo`, a `make setup` one-shot bootstrap, and
> the new BMONI paid-enrollment flow.

This document explains how to set up, run, and contribute to the SABIFY backend locally.

## 1. Backend Stack

- Go (1.25)
- PostgreSQL 16 (Docker)
- Docker Compose (requires `sudo` in this environment)
- Chi Router (`github.com/go-chi/chi/v5`)
- pgx PostgreSQL driver (`github.com/jackc/pgx/v5`)
- Server-side Go Templates (`html/template`) — no frontend framework
- Session management via `scs/v2` (cookie sessions)
- `github.com/ethereum/go-ethereum` (BMONI owner-key signing)
- BMONI Embedded REST API (paid enrollment; sandbox via `embedded-dev.bmoni.com`)

There is **no** `services/` / `repositories/` layer — models own their DB methods
directly (Alex Edwards "Let's Go" pattern). There is **no** Python/AI service yet.

Architecture:

```text
Browser
   ↓
Go Router (Chi)
   ↓
Handlers (methods on *application)
   ↓
Models (own DB methods via pgxpool)
   ↓
PostgreSQL
```

## 2. Requirements

Install:

- Go 1.25+
- Git
- Docker
- Docker Compose

Check installations:

```bash
go version
git --version
docker --version
sudo docker compose version
```

> **Note for this environment:** the current user is **not** in the `docker`
> group and `sudo` requires a password. All `docker`/`psql` commands below use
> `sudo`. Docker gating means `make db_up`/`make migrate` prompt for your sudo
> password (cached for ~5 minutes per run).

## 3. Clone the Repository

```bash
git clone <REPOSITORY_URL>
cd sabify
```

## 4. Install Go Dependencies

```bash
go mod tidy
```

Key dependencies (see `go.mod`):

- `github.com/go-chi/chi/v5`
- `github.com/jackc/pgx/v5`
- `github.com/alexedwards/scs/v2`
- `github.com/ethereum/go-ethereum`

## 5. Environment Variables

Create `.env` in the project root (copy `.env.example`, then fill in values):

```env
APP_ENV=development
APP_PORT=8080

DB_HOST=localhost
DB_PORT=5432
DB_USER=sabify
DB_PASSWORD=sabify_password
DB_NAME=sabify_db
DB_SSLMODE=disable

AI_SERVICE_URL=http://localhost:8082

# BMONI (paid enrollment)
BMONI_BASE_URL=https://embedded-dev.bmoni.com
BMONI_API_KEY=pk_a025cacbf33a_76fb864113f3540909de5b1da39cc146906e35b1c6d4d1e4
BMONI_WEBHOOK_SECRET=<your secret>
BMONI_WALLET_ENCRYPTION_KEY=
```

- `BMONI_API_KEY` — BMONI sandbox key. The docs publish a **shared sandbox key**
  (`pk_a025...e4`) that works only against the dev base URL
  (`https://embedded-dev.bmoni.com`). Get your own key from `developers@bkey.me`
  before production.
- `BMONI_WEBHOOK_SECRET` — used to verify webhook HMAC signatures. **When set,
  the webhook requires a valid signature.** Generate one: `openssl rand -hex 32`.
- `BMONI_WALLET_ENCRYPTION_KEY` — reserved for at-rest encryption of the owner
  key (not yet used by `tools/bmoni-bootstrap`; leave empty for now).

Do not commit `.env` (it is already in `.gitignore`, which also ignores `*.env`).

The Makefile `include .env`, so `DB_*` and `BMONI_API_KEY` are available to make
targets.

## 6. Start PostgreSQL

The DB runs in Docker as `sabify-postgres` with the host port mapped to **5432**
(matching `.env` `DB_PORT=5432`).

```bash
sudo docker compose up -d
# or
make db_up            # target already prefixes docker with sudo
```

Check:

```bash
sudo docker ps
```

## 7. Verify PostgreSQL

```bash
sudo docker exec -it sabify-postgres psql -U sabify -d sabify_db
```

Inside PostgreSQL:

```sql
\dt
```

Exit:

```sql
\q
```

## 8. Database Migration

Migrations live in `migrations/`:

```text
migrations/
├── 001_initial_schema.sql
├── 002_course_enrollments.sql
├── 002_quiz_retakes.sql
└── 003_bmoni.sql        # price_kobo + bmoni_wallets/payments/course_access/webhook_events
```

There is no migration tool — run them through `psql` **inside** the container
(no local `psql` client required, no port to get wrong):

```bash
make migrate
```

Which expands to, for each file:

```bash
sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/001_initial_schema.sql
sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_course_enrollments.sql
sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_quiz_retakes.sql
sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/003_bmoni.sql
```

> The migration SQL is **not idempotent** (`CREATE TABLE`, `ADD COLUMN` without
> `IF NOT EXISTS`), so re-running `make migrate` against an already-migrated DB
> produces "already exists" errors. It is a one-time dev runner.

Verify:

```bash
sudo docker exec -it sabify-postgres psql -U sabify -d sabify_db
```

Then:

```sql
\dt
```

You should see (among others) `bmoni_wallets`, `payments`, `course_access`,
`webhook_events`, and the `price_kobo` column on `courses`.

## 9. Provision the Platform Wallet (once)

The `studentPay` page needs a platform wallet row in `bmoni_wallets`. Provision
it with the bootstrap tool (idempotent):

```bash
make bootstrap          # reads BMONI_API_KEY from .env; skips politely if unset
# or explicitly:
BMONI_API_KEY=... go run ./tools/bmoni-bootstrap
```

This creates the platform BMONI user → smart wallet → KYC → NGN deposit
account and saves the virtual account number/bank to `bmoni_wallets`.

Without a provisioned wallet, the payment page renders a "Payment account is not
configured yet" notice instead of erroring.

## 10. One-Shot Dev Setup (recommended)

Everything above, chained:

```bash
make setup    # db_up -> migrate -> bootstrap
```

`make setup` will prompt for your sudo password (a few times; sudo caches it
during the run), then finish with instructions to start the app.

## 11. Start SABIFY

```bash
make run        # go run ./cmd/web  (defaults to :4000 in code)
```

> **Port mismatch gotcha:** `.env.example`/`.env` say `APP_PORT=8080` but
> `cmd/web/main.go` defaults to `:4000`. `APP_PORT` overrides the default.
> `make run` uses the Go default (`:4000`).

Expected output:

```text
🚀 server running on :4000
```

Open:

```text
http://localhost:4000
```

Health check:

```text
http://localhost:4000/health
```

## 12. Current Project Structure

> This reflects the real "Let's Go"-style layout (not the older `backend.md`).

```text
sabify/
│
├── cmd/
│   └── web/                    # the one binary
│       ├── main.go             # config, DB pool, session, template cache, application struct
│       ├── routes.go           # Chi route groups
│       ├── helpers.go          # render/templateData, backgroundContext
│       ├── handlers.go         # common handlers + auth middleware helpers
│       ├── auth_handlers.go    # register/login/logout
│       ├── student_handlers.go # courses, enroll (free & paid), course detail
│       ├── teacher_handlers.go # course create/price, dashboard
│       ├── bmoni_handlers.go   # webhook, student pay, teacher wallet
│       └── ...
│
├── internal/
│   ├── models/                 # models own DB methods (no service/repo layers)
│   │   ├── models.go           # Models bundle + NewModels
│   │   ├── user.go
│   │   ├── courses.go          # has PriceKobo *int64
│   │   ├── courseaccess.go     # paid access (PENDING/ACTIVE)
│   │   ├── payments.go         # payment attempts (PENDING/PAID/...)
│   │   ├── bmoniwallet.go      # platform wallet singleton
│   │   ├── webhookevents.go    # webhook dedupe ledger
│   │   └── ...
│   ├── bmoni/                  # BMONI REST client + signing + webhook verify
│   │   ├── client.go
│   │   ├── lifecycle.go
│   │   ├── signing.go          # EIP-191 owner-proof signing (go-ethereum)
│   │   └── webhook.go          # HMAC-SHA256 signature verification
│   ├── validator/              # CheckField validation helper
│   └── middleware/             # logging, recover, security headers
│
├── migrations/                 # SQL applied via `make migrate`
│   ├── 001_initial_schema.sql
│   ├── 002_course_enrollments.sql
│   ├── 002_quiz_retakes.sql
│   └── 003_bmoni.sql
│
├── tools/
│   └── bmoni-bootstrap/        # provisions the platform wallet (needs BMONI_API_KEY)
│
├── ui/
│   ├── html/                   # Go templates (cache at startup — restart on edit)
│   │   ├── layouts/base.html
│   │   ├── pages/, components/, auth/, student/, teacher/
│   └── static/                 # vanilla CSS/JS, icons/images
│
├── doc/
│   ├── backend.md              # original (stale) doc
│   ├── backend-update.md       # this file
│   └── ...
│
├── .env                        # gitignored
├── .env.example
├── Makefile                    # build/run/test/lint + db_up/migrate/bootstrap/setup
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md                   # README likely stale (describes old stack)
```

## 13. Current Routes

Route groups defined in `cmd/web/routes.go`:

```text
# Public
GET  /health
GET  /                        (public home)
POST /webhooks/bmoni          (public — BMONI webhook, NO auth)

# Auth
GET  /register
POST /register
GET  /login
POST /login
POST /logout

# Auth required (redirect by role)
GET  /dashboard

# Teacher only
GET   /teacher/dashboard
GET   /teacher/courses
GET   /teacher/courses/new
POST  /teacher/courses                    (create — includes optional price)
GET   /teacher/courses/{id}
POST  /teacher/courses/{id}/price         (set/update price)
GET   /teacher/wallet                     (earnings + platform deposit account)

# Student only
GET   /student/courses
GET   /student/courses/{id}               (paywall gating for paid courses)
POST  /student/courses/{id}/enroll        (free = instant; paid = go pay)
GET   /student/pay/{paymentId}
GET   /student/pay/{paymentId}/status     (JSON poller)
```

## 14. Paid Enrollment Flow (BMONI)

Paid courses have a non-NULL `price_kobo`; free courses have `NULL`/0.

```text
Student                         SABIFY (Go)                    BMONI
   |  1. enroll (paid course)        |                             |
   +-------------------------------> | 2. payments PENDING          |
   |                                  |    + course_access PENDING  |
   |  3. /student/pay shows VBA +    |                             |
   |     amount + reference          |                             |
   <---------------------------------+                             |
   |  4. bank transfer to platform   +---------------------------->|
   |     VBA                          |                             |
   |                                  | 5. employee.deposit.completed |
   |                                  |<----------------------------+
   |                                  | 6. verify HMAC + dedupe     |
   |                                  | 7. payments -> PAID         |
   |                                  |    course_access -> ACTIVE  |
   |  8. poller sees PAID             |                             |
   <---------------------------------+                             |
   |  9. course unlocked              |                             |
```

- **Free course:** `enrollInCourse` → `Enrollments.Insert` → instant redirect.
- **Paid course:** creates PENDING `payments` (reference `SABIFY-<course>-<student>`)
  and PENDING `course_access`, redirects to `/student/pay/{id}`.
- `studentCourseDetail` renders a paywall for non-enrolled students on paid
  courses; content only loads when access is granted (free enrollment OR active
  `course_access`).
- **Webhook** (`POST /webhooks/bmoni`, public): verifies HMAC-SHA256 over the
  raw body when `BMONI_WEBHOOK_SECRET` is set; de-dupes on `event_id` via
  `webhook_events`; acknowledges fast and processes the deposit in a goroutine.
  Matching is **first-come-first-served** against the oldest PENDING payment
  (BMONI's deposit webhook carries no per-payment reference).

Money is stored in **kobo** (`BIGINT`), never floats. `naira` is a template func.

## 15. Authentication

Auth uses bcrypt for password hashing and `scs/v2` cookie sessions. Auth flow,
course creation, and paid enrollment are implemented. See `AGENTS.md` note: the
session cookie is `Secure: true` in production — use `APP_ENV=development` or
HTTPS over plain HTTP.

## 16. Useful Docker Commands

```bash
# Start / stop PostgreSQL
make db_up
make db_down

# Logs
sudo docker compose logs postgres

# Connect
sudo docker exec -it sabify-postgres psql -U sabify -d sabify_db

# Apply migrations (all files)
make migrate
```

## 17. Reset the Database

> WARNING: `sudo docker compose down -v` deletes the PostgreSQL volume and all
> local database data.

```bash
sudo docker compose down -v
```

Recreate and re-provision:

```bash
make setup          # db_up -> migrate -> bootstrap
```

## 18. Running Tests & Lint

```bash
make test       # go test -v ./...
make lint       # go vet ./...
make build      # go build -o ./tmp/sabify ./cmd/web
```

> `AGENTS.md` notes the repo currently has zero `*_test.go` files, so
> `make test` passes vacuously.

## 19. Development Workflow

```bash
git pull
go mod tidy
make setup          # or: make db_up && make migrate && make bootstrap
make run
```

After making changes:

```bash
make lint
make build
make test
```

**Template reminder:** templates are parsed once at startup — restart the app
after editing any file under `ui/html/`.

## 20. Git Workflow

Do not push directly to `main`.

Create a feature branch:

```bash
git checkout -b feature/your-feature-name
```

Commit and push:

```bash
git add .
git commit -m "feat: add login functionality"
git push origin feature/login
```

Create a Pull Request and wait for review. Do not commit `.env`.

## 21. Current Development Status

### Implemented

- [x] Go + Chi + pgx + html/template + scs project (Let's Go layout)
- [x] Docker PostgreSQL 16 on `sabify-postgres` (:5432)
- [x] Migrations 001/002(enrollments)/002(quiz retakes)/003(bmoni)
- [x] Models own their DB methods (`models.Course`, `PaymentModel`, `CourseAccessModel`, `BmoniWalletModel`, `WebhookEventsModel`)
- [x] Auth (register/login/logout) with bcrypt + scs sessions
- [x] Course creation; teacher price setting (`price_kobo`)
- [x] Paid enrollment: payments + `course_access` + student pay page + poller
- [x] BMONI webhook (verify + dedupe + unlock)
- [x] Teacher wallet page (earnings + platform deposit account)
- [x] BMONI client + EIP-191 signing + `tools/bmoni-bootstrap`
- [x] `make setup` one-shot dev bootstrap
- [x] Updated `.env` with sandbox BMONI API key + webhook secret

### In Progress / Future

- [ ] `*_test.go` tests (repo currently has none)
- [ ] BMONI wallet key encryption at rest (`BMONI_WALLET_ENCRYPTION_KEY` unused)
- [ ] Live webhook tunnel (cloudflared/ngrok) to exercise real webhooks
- [ ] Deterministic deposit matching (instead of first-come-first-served)
- [ ] Teacher bank offramp / withdrawal
- [ ] AI features (quiz generator, coach, etc.) — deferred

## 22. Quick Start

For a developer with requirements installed:

```bash
git clone <REPOSITORY_URL>
cd sabify
cp .env.example .env        # fill in BMONI vars
go mod tidy
make setup                  # db_up -> migrate -> bootstrap
make run
```

Then open:

```text
http://localhost:4000
```

Health check:

```text
http://localhost:4000/health
```

## 23. Important Development Rules

- **Do not push directly to `main`** — use feature branches + PRs.
- **Do not commit `.env`** — never commit passwords, API keys, or secrets.
- **Restart the app after template changes** (template cache is built at startup).
- **Docker is sudo-gated** here — use `make db_up`/`make migrate`/`make setup`.
- **Test/lint before pushing:** `make lint && make build && make test`.

# AGENTS.md

## What this is

Go web app (MVP) following Alex Edwards' "Let's Go" pattern. PostgreSQL backend, Chi router, server-side HTML templates via `html/template`, cookie sessions via `scs/v2`. No frontend framework — vanilla CSS/JS in `ui/static/`.

## Quick commands

```bash
make build    # go build -o ./tmp/sabify ./cmd/web
make run      # go run ./cmd/web (port 4000)
make test     # go test -v ./...
make lint     # go vet ./...
make db_up    # docker compose up -d (Postgres 16)
make db_down  # docker compose down
make migrate  # psql ... -f migrations/001_initial_schema.sql
```

Database requires `make db_up` before running the app. Schema is in `migrations/001_initial_schema.sql`.

## Architecture

**Entry point:** `cmd/web/main.go` — wires config, DB pool, session manager, template cache into a central `application` struct. All handlers are methods on `*application`.

**Route groups** (`cmd/web/routes.go`):
- Public: `/`, `/health`, `/register`, `/login`
- Auth required: `/dashboard` (redirects by role)
- Teacher only: `/teacher/*`
- Student only: `/student/*`

**Models** (`internal/models/`) own their DB methods directly (no repository/service layers). Models are accessed via `app.models.<Model>`.

**Templates** (`ui/html/`): Cached at startup in `map[string]*template.Template`. Layout in `layouts/base.html`, pages in `pages/*/`, components in `components/`. **Changes to templates require app restart.**

**Validation** (`internal/validator/`): Use `validator.New()` + `CheckField` pattern.

## Key gotchas

- **Port mismatch:** `.env.example` says 8080, but `cmd/web/main.go` defaults to `:4000`. `APP_PORT` env var overrides the default. Makefile `run` target uses the Go default (`:4000`).
- **Makefile includes `.env`:** The Makefile has `include .env` at the top. The `.env` file must exist or `make` targets will fail. Copy `.env.example` to `.env` if missing.
- **Template cache:** Templates are parsed once at startup. Edit templates → restart the app.
- **Tests:** `cmd/web/bmoni_e2e_test.go` covers the BMONI paid-enrollment flow end-to-end (per-teacher wallet resolution + cross-teacher isolation) and `cmd/web/kyc_wizard_e2e_test.go` runs the teacher KYC wizard against a stub BMONI server; both use an embedded PostgreSQL (`go test ./cmd/web/`; first run downloads the Postgres binaries). `internal/bmoni/signing_test.go` pins the signing code to BMONI's published vector. The rest of the repo has no unit tests yet. Note: the e2e binds port 55432 — stop any local dev DB on that port first.
- **BMONI payments are per-teacher:** each teacher completes an in-app KYC wizard (`GET /teacher/wallet/kyc`, handlers in `cmd/web/kyc_handlers.go`) that provisions her own BMONI user + smart wallet + VBA (`migrations/005_teacher_wallets.sql`). The pay page and webhook resolve the *course teacher's* wallet, never a shared platform wallet (a legacy `user_id IS NULL` row is still honoured by the webhook). KYC is only sandbox-verifiable with the two persona identities (see `doc/bmoni-teacher-kyc.md`).
- **CI is placeholder:** `.github/workflows/pr-check.yml` only echoes success — no real checks.
- **README is stale:** Describes planned Next.js/NestJS/Python stack. Actual stack is Go + Chi + pgx + Go templates.
- **Most handlers are stubs:** `student_handlers.go` and parts of `teacher_handlers.go` render templates with no data. Auth flow and course creation are the only implemented features.
- **Session cookie config:** `Secure: true` in production — will not work over plain HTTP unless you set `APP_ENV=development` or use HTTPS.

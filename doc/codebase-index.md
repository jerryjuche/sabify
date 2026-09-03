# SABIFY Codebase Index

> Professional reference map of the repository. Every directory, route, handler,
> model method, template, and database table — with implementation status
> markers so this document doubles as a work tracker.
>
> **Every claim below was verified against the working tree on 2026-09-03**
> (commit history ends 2026-08-24). Files marked with byte counts were checked
> directly; nothing is assumed.

**Status legend**

| Mark | Meaning |
|------|---------|
| ✅ | Fully implemented and wired |
| ◐ | Partially implemented (renders, but incomplete) |
| ⚠️ | Stub / empty shell / unrouted |
| ❌ | Broken in production as-is (500, blank page, dead link) |

---

## Table of contents

1. [Overview](#1-overview)
2. [Application bootstrap](#2-application-bootstrap)
3. [HTTP surface](#3-http-surface)
4. [Handlers reference](#4-handlers-reference)
5. [Domain layer (`internal/models`)](#5-domain-layer-internalmodels)
6. [Cross-cutting concerns](#6-cross-cutting-concerns)
7. [Database schema](#7-database-schema)
8. [Frontend assets](#8-frontend-assets)
9. [Tooling & infrastructure](#9-tooling--infrastructure)
10. [Known gaps & gotchas](#10-known-gaps--gotchas)
11. [Audit findings — 2026-09-03](#11-audit-findings--2026-09-03)

---

## 1. Overview

SABIFY is an AI-powered, two-sided learning management system (teachers +
students). This MVP follows Alex Edwards' *"Let's Go"* pattern: server-rendered
HTML via `html/template`, PostgreSQL persistence through `pgx`, cookie sessions
through `scs`. There is **no frontend framework** — vanilla CSS/JS under
`ui/static/`.

### Stack (verified from `go.mod` / source)

| Component | Technology | Version | Notes |
|-----------|------------|---------|-------|
| Language | Go | `go 1.25.13` | `go.mod` module name: `sabify` |
| Router | `go-chi/chi/v5` | 5.3.1 | Middleware chain + route groups |
| Database driver | `jackc/pgx/v5` | 5.10.0 | `pgxpool` connection pool |
| Sessions | `alexedwards/scs/v2` | 2.9.0 | Cookie store, 24 h lifetime |
| Validation | hand-rolled | — | `internal/validator`, *Let's Go* pattern |
| Passwords | `golang.org/x/crypto` | 0.55.0 | bcrypt in `UserModel` |
| Env config | `joho/godotenv` | 1.5.1 | `.env` loaded at startup (**exits if missing**) |
| Database | PostgreSQL | 16 | Via `docker-compose.yml`, host port **5434** |

### Request flow

```
Browser
   ↓
Chi router  (Recoverer → RealIP → StripSlashes)
   ↓
scs LoadAndSave  ·  SetSecurityHeaders  ·  LogRequest  ·  RecoverPanic
   ↓
Route groups (public / auth / teacher / student)
   ↓
Handlers (cmd/web/*_handlers.go)      ← render templates, manage sessions
   ↓
Models (internal/models)              ← SQL lives here, no repo/service layers
   ↓
pgxpool → PostgreSQL
```

### Directory tree (verified)

```
sabify/
├── cmd/web/                  # Application entry point + all HTTP handlers
│   ├── main.go               # Config, DB pool, sessions, template cache, shutdown
│   ├── routes.go             # Chi router + middleware chain + route groups
│   ├── handlers.go           # Home, health, register/login/logout, auth middlewares
│   ├── teacher_handlers.go   # Teacher-only endpoints (mostly shells — see §4)
│   ├── student_handlers.go   # Student-only endpoints (the functional half)
│   └── helpers.go            # templateData, render/error helpers, stats math
├── internal/
│   ├── models/               # Domain structs + SQL methods (one file per entity)
│   │   ├── models.go         # Models aggregate struct + constructor
│   │   ├── users.go          # Users, auth, sentinel errors
│   │   ├── courses.go        # Courses (+ join views with teacher + quiz count)
│   │   ├── quizzes.go        # Quizzes (+ join view with course + question count)
│   │   ├── questions.go      # Quiz questions (fixed 4-option MCQ shape)
│   │   ├── materials.go      # Course materials
│   │   ├── submissions.go    # Quiz submissions (+ join view with quiz)
│   │   └── studygroups.go    # Study groups + membership
│   ├── middleware/           # middleware.go — 3 wired + 1 unwired middleware
│   └── validator/            # validator.go — field-validation kit (1 stub rule)
├── migrations/
│   └── 001_initial_schema.sql  # Full schema (8 tables), applied manually
├── ui/
│   ├── html/                 # 26 files total — see §8 for per-file status
│   │   ├── layouts/          # base.html (real) + public.html (8 ln) + 2× 0-byte
│   │   ├── components/       # navbar, footer, sidebar real; 2× 0-byte cards
│   │   ├── pages/home/       # index.html — 2381-line landing page
│   │   ├── auth/             # login.html, register.html (parsed into every page)
│   │   ├── student/          # 6 real pages + 1× 0-byte (dashboard.html)
│   │   └── teacher/          # 1 real shell + 5× 0-byte + 1 missing file!
│   └── static/               # Served at /static/* (18 css, 6 js, 1 img)
├── tools/quiz-preview/       # Dev aid: offline template preview with fake data
├── doc/                      # This index + backend.md (stale) + frontend.md (empty)
├── .github/                  # pr-check.yml (placeholder) + impeccable skill suite
├── .impeccable/              # Design-tooling config (design.json, critique/)
├── docker-compose.yml        # Postgres 16 service
├── Dockerfile                # Multi-stage build (Go 1.23 builder — drift, see §9)
├── Makefile                  # build/run/test/lint/db_up/db_down/migrate
├── PRODUCT.md                # Product brief (two-sided AI LMS positioning)
├── DESIGN.md                 # Design system tokens (colors, typography)
├── AGENTS.md                 # Working conventions for coding agents
├── README.md                 # ❌ STALE — describes a Next.js/NestJS stack
├── command                   # Stray task-reminder scratch file (partially stale)
├── web.exe                   # ⚠️ Untracked compiled Windows binary at repo root
└── static/css/register.css   # ❌ Stray tracked duplicate, NOT served (see §11)
```

---

## 2. Application bootstrap

File: `cmd/web/main.go` (250 lines)

### Wiring (`application` struct)

All handlers are methods on `*application`, which carries:

| Field | Type | Purpose |
|-------|------|---------|
| `config` | `config` | Address + DB pool settings |
| `logger` | `*slog.Logger` | JSON handler → stdout, level Info |
| `models` | `models.Models` | Aggregate of all seven models |
| `templateCache` | `map[string]*template.Template` | Parsed once at startup |
| `session` | `*scs.SessionManager` | Cookie sessions, 24 h, `Secure: true`, Lax |

### Configuration precedence

1. CLI flags: `-addr` (default `:4000`), `-db-dsn`, `-db-max-open-conns` (25),
   `-db-max-idle-conns` (25), `-db-max-idle-time` (5 m).
2. If `-db-dsn` is empty it is assembled from env vars:
   `postgres://$DB_USER:$DB_PASSWORD@$DB_HOST:$DB_PORT/$DB_NAME?sslmode=$DB_SSLMODE`.
3. If `APP_PORT` env var is set it **overrides** `-addr` (`main.go:73–74`).
4. `godotenv.Load()` runs first and **exits the process** if `.env` is missing
   (`main.go:57–58`).

> Gotcha: `APP_ENV` appears in `.env.example` but is **never read by any Go
> code**. See §11-F-1 for the cookie implication.

### DB pool tuning

`openDB` (5 s ping context) maps flags onto the pgxpool config
(`main.go:147–150`):

| Flag | Pool setting |
|------|--------------|
| `-db-max-open-conns` | `MaxConns` |
| `-db-max-idle-conns` | `MinConns` |
| `-db-max-idle-time` | `MaxConnLifetime` **and** `MaxConnIdleTime` |

### Template cache (`newTemplateCache`)

For every page file it parses, in order:

1. `ui/html/layouts/base.html` (root template `"base"`)
2. All of `ui/html/components/*.html` (including the two 0-byte card files)
3. All of `ui/html/auth/*.html` (login/register baked into **every** page set)
4. The individual page file

Sources scanned: `pages/*/*.html`, `auth/*.html`, `student/*.html`,
`teacher/*.html`. Cache keys are paths relative to `ui/html` with forward
slashes (e.g. `student/courses.html`), so identically-named pages cannot
collide. A page file that **does not exist** is simply absent from the cache —
`render` then 500s with "the template X does not exist" (this is exactly what
happens on `GET /teacher/quizzes`, see §3/§11-C-1).

Custom template functions registered globally:

| Func | Behaviour |
|------|-----------|
| `add a b` | Integer addition |
| `initials name` | First + last initial, upper-cased (`"Ada Lovelace"` → `"AL"`; single name → first initial; empty → `"?"`) |
| `shortDate t` | `t.Format("Jan 2")` |

> Templates are parsed **once** — editing any file under `ui/html` requires an
> application restart.

### Server lifecycle

- `http.Server` timeouts: idle 1 m, read 5 s, write 10 s.
- Listens in a goroutine; SIGINT/SIGTERM trigger graceful `Shutdown` with a
  30 s budget.
- Session cookie: `Secure: true` hard-coded (`main.go:94`), `SameSite: Lax`,
  24 h lifetime.

---

## 3. HTTP surface

File: `cmd/web/routes.go`

Global middleware chain, in order:

1. `chi/middleware.Recoverer`
2. `chi/middleware.RealIP`
3. `chi/middleware.StripSlashes`
4. `app.session.LoadAndSave`
5. `middleware.SetSecurityHeaders` *(internal)*
6. `middleware.LogRequest` *(internal)*
7. `middleware.RecoverPanic` *(internal)*

Static assets: `/static/*` → filesystem `./ui/static`.

### Route table (verified handler-by-handler)

| Method | Path | Middlewares | Handler | Renders / result | Status |
|--------|------|-------------|---------|------------------|--------|
| GET | `/` | public | `home` | `pages/home/index.html` | ✅ |
| GET | `/health` | public | `healthCheck` | JSON `{"status":"ok","database":"connected"}` | ✅ |
| GET | `/register` | public | `showRegisterForm` | `auth/register.html` | ✅ |
| POST | `/register` | public | `register` | re-render 422 w/ form values | ✅ |
| GET | `/login` | public | `showLoginForm` | `auth/login.html` | ✅ |
| POST | `/login` | public | `login` | re-render 401/422 (**errors invisible**, see §11-C-3) | ◐ |
| POST | `/logout` | public | `logout` | redirect `/` | ✅ |
| GET | `/dashboard` | `authenticate` | `dashboard` | redirect by role | ✅ |
| GET | `/teacher/courses` | auth + teacher | `teacherCourses` | `teacher/courses.html` — **0-byte file → blank page** | ❌ |
| POST | `/teacher/courses` | auth + teacher | `createCourse` | inserts course; 422 re-renders 0-byte `teacher/create-course.html` | ◐ |
| GET | `/teacher/courses/{id}` | auth + teacher | `teacherCourseDetail` | `teacher/course-detail.html` — "coming soon" shell, ignores `{id}` | ⚠️ |
| GET | `/teacher/quizzes` | auth + teacher | `teacherQuizzes` | `teacher/quizzes.html` — **file does not exist → 500** | ❌ |
| POST | `/teacher/quizzes` | auth + teacher | `createQuiz` | renders 0-byte `teacher/create-quiz.html`; ignores body entirely | ❌ |
| GET | `/teacher/submissions` | auth + teacher | `teacherSubmissions` | 0-byte `teacher/submissions.html` → blank page | ❌ |
| GET | `/student/courses` | auth + student | `studentCourses` | `student/courses.html` — full dashboard | ✅ |
| GET | `/student/courses/{id}` | auth + student | `studentCourseDetail` | `student/course.html` | ✅ |
| GET | `/student/quizzes` | auth + student | `studentQuizzes` | `student/quizzes.html` — **"Open" links 404** (see below) | ◐ |
| POST | `/student/quizzes/{id}/submit` | auth + student | `submitQuiz` | flash + redirect; **persists nothing** | ⚠️ |
| GET | `/student/results` | auth + student | `studentResults` | `student/results.html` | ✅ |
| GET | `/student/study-groups` | auth + student | `studentStudyGroups` | `student/study-groups.html` | ✅ |

### Route-group observations

- `dashboard` redirects teachers → `/teacher/courses`, students →
  `/student/courses`, unknown roles → `/`.
- Auth middlewares are defined **inline in `handlers.go`**, not in
  `internal/middleware`.
- **There is no `GET /student/quizzes/{id}` route** — yet
  `student/quizzes.html` renders an "Open" link to exactly that URL for every
  quiz (`ui/html/student/quizzes.html:84`). Every click 404s. The interactive
  quiz flow is unreachable end-to-end.
- **There is no course-creation form anywhere.** `teacher/courses.html` and
  `teacher/create-course.html` are both 0-byte files; `POST /teacher/courses`
  can create courses, but no page ever renders a form to fill in.
- **No enrollment concept** exists anywhere (routes, models, schema).

---

## 4. Handlers reference

### `handlers.go` — public surface + auth plumbing

| Symbol | Kind | Notes |
|--------|------|-------|
| `home` | handler | 404s any non-`/` path explicitly |
| `healthCheck` | handler | 3 s timeout `Ping` on the user model's pool |
| `showRegisterForm` / `register` | handlers | Validates name/email/password≥8/confirm/role∈{student,teacher}/policy; pre-check `Users.Exists` then `Insert`; maps `ErrDuplicateEmail`; **repopulates form + errors on 422** (register.html reads `.Form.*` and `.FormErrors`); flash + redirect to login |
| `showLoginForm` / `login` | handlers | Blank checks → `Authenticate`; maps `ErrInvalidCredentials` to 401; stores `authenticatedUserID`, `userRole`, flash. **⚠️ 422/401 re-render `auth/login.html` which never displays `.Form` or `.FormErrors` → failed logins give zero feedback.** **⚠️ Login does not lowercase/trim email before `Authenticate` while register does — mixed-case registered emails cannot log in with the same typing (see §11-C-4).** |
| `logout` | handler | Removes session keys, flash, redirect `/` |
| `authenticate` | middleware | Redirects to `/login` when session has no user ID |
| `loadCurrentUser` | helper | Resolves session ID → `*models.User`; on stale/missing user clears session keys, flashes, redirects; callers must return on `nil` |
| `requireRole(role)` | middleware | Compares session `userRole`; mismatch → plain-text **403** |
| `dashboard` | handler | Role switch redirect (see §3) |

### `teacher_handlers.go` — the broken half of the product

| Handler | Status | Detail |
|---------|--------|--------|
| `teacherCourses` | ❌ | Renders `teacher/courses.html` which is **0 bytes** → blank page. Never queries `Courses.FindByTeacher` or `FindAllWithTeacher`. |
| `createCourse` | ◐ | Only functional teacher write path: validates title (required ≤255) + description, trims, inserts via `Courses.Insert` with teacher ID from session. But the 422 branch renders a **0-byte** template, so validation errors are invisible, and no page ever links to it. |
| `teacherCourseDetail` | ⚠️ | Renders the "coming soon" shell; **ignores `{id}` URL param entirely**. |
| `teacherQuizzes` | ❌ | Renders `teacher/quizzes.html` — **the file does not exist** → `render` 500s ("the template teacher/quizzes.html does not exist"). |
| `createQuiz` | ❌ | POST endpoint that only renders the 0-byte `teacher/create-quiz.html`; never calls `ParseForm` or any model. |
| `teacherSubmissions` | ❌ | Renders 0-byte `teacher/submissions.html` → blank page. |

### `student_handlers.go` — the functional half

Shared helper:

- `attemptedScoreMap(subs)` → `map[quizID]int` of **best** percentage achieved;
  negative percents skipped. Used to badge quiz listings with attempt state.

| Handler | Status | Detail |
|---------|--------|--------|
| `studentCourses` | ✅ | Loads current user, all courses w/ teacher + quiz count, own submissions, all quizzes w/ question count; derives up-to-3 unattempted "continue learning" quizzes, last 5 recent submissions, and stat cards |
| `studentCourseDetail` | ✅ | `Courses.FindByIDWithTeacher` (404 via `ErrNoRecord`), course quizzes, attempted map |
| `studentQuizzes` | ✅ | All quizzes w/ course + attempted badges — **but every quiz links to a nonexistent GET route** (§3) |
| `submitQuiz` | ⚠️ | Verifies quiz exists, then flashes "submission received" and redirects to results — **nothing is persisted**; grading needs `Questions.FindByQuiz` + `Submissions.Insert` |
| `studentResults` | ✅ | Own submissions w/ quiz info + recomputed stats (note: `CoursesAvailable` is passed as 0, so that stat card reads 0 even with data) |
| `studentStudyGroups` | ✅ | Read-only listing via `StudyGroups.FindAllForStudent` |

### `helpers.go`

- `templateData` — the single view-model passed to every template. Fields:
  `CurrentYear`, `Title`, `Description`, `Flash`, `Form`, `FormErrors`,
  authenticated-shell context (`User`, `CurrentPage`), dashboard payloads
  (`Courses`, `Course`, `CourseQuizzes`, `Quizzes`, `UpcomingQuizzes`,
  `Submissions`, `Groups`, `Stats`, `Attempted`).
  **⚠️ No `Quiz`, `Questions`, or `CorrectAnswers` fields** — but
  `student/quiz.html` (unrouted) references all three.
- `StudentStats` + `computeStudentStats` — courses available, quizzes taken,
  average/best score as **integer-truncated percentages** computed from
  submission rows; rows with `TotalQuestions <= 0` are skipped.
- Error helpers: `serverError` (logs error + stack via slog, generic 500),
  `clientError`, `notFound`.
- `render` — executes cached template's `"base"` into a buffer before writing,
  so partial renders never hit the wire; 500s on missing cache keys.
- `newTemplateData` — seeds year + pops flash message.

---

## 5. Domain layer (`internal/models`)

No repository/service layers — each model owns its SQL directly and holds a
`*pgxpool.Pool`. Aggregated in `Models` (see `internal/models/models.go`),
instantiated once in `main.go` via `NewModels(dbPool)`.

Sentinel errors (declared in `users.go`, used package-wide):

| Error | Meaning |
|-------|---------|
| `ErrNoRecord` | Query matched nothing (handlers map this to 404) |
| `ErrInvalidCredentials` | Email lookup failed or bcrypt comparison failed |
| `ErrDuplicateEmail` | Unique-constraint violation (23505) on `users.email` |

### Entities (verified field-for-field)

| Struct | File | Fields |
|--------|------|--------|
| `User` | users.go | ID, Name, Email, PasswordHash, Role, CreatedAt, UpdatedAt |
| `Course` | courses.go | ID, Title, Description, TeacherID, CreatedAt, UpdatedAt |
| `CourseWithTeacher` | courses.go | embeds `Course` + **TeacherName**, **QuizCount** |
| `Quiz` | quizzes.go | ID, CourseID, Title, Description, CreatedAt |
| `QuizWithCourse` | quizzes.go | embeds `Quiz` + **CourseTitle**, **QuestionCount** |
| `Question` | questions.go | ID, QuizID, QuestionText, OptionA–D, CorrectAnswer (A–D), CreatedAt |
| `Material` | materials.go | ID, CourseID, Title, Description, FileURL, CreatedAt |
| `Submission` | submissions.go | ID, QuizID, StudentID, Score, TotalQuestions, SubmittedAt |
| `SubmissionWithQuiz` | submissions.go | embeds `Submission` + QuizTitle, **Percent** (score/total×100, `-1` when `TotalQuestions <= 0`) |
| `StudyGroup` | studygroups.go | ID, Name, CourseID, CreatedAt |
| `StudyGroupWithMeta` | studygroups.go | embeds `StudyGroup` + CourseTitle, MemberCount, IsMember |

### Method inventory

**Users**
- `Insert(ctx, *User, plaintext)` — bcrypt-hashes password, maps `23505` → `ErrDuplicateEmail`
- `Authenticate(ctx, email, password)` — returns user or `ErrInvalidCredentials`
- `FindByEmail`, `FindByID` (both map no-rows → `ErrNoRecord`), `Exists(email)` (lowercases/trims)

**Courses**
- `Insert`, `FindByID`, `FindByTeacher`, `FindAll`, `Update` (sets `updated_at`), `Delete` (0 rows → `ErrNoRecord`)
- Joins: `FindByIDWithTeacher`, `FindAllWithTeacher` (both LEFT JOIN quizzes for `QuizCount`)

**Quizzes**
- `Insert`, `FindByID`, `FindByCourse`, `Delete` (0 rows → `ErrNoRecord`)
- Join: `FindAllWithCourse` (LEFT JOIN questions for `QuestionCount`)

**Questions**
- `Insert`, `FindByQuiz`, `DeleteByQuiz`

**Materials**
- `Insert`, `FindByCourse`

**Submissions**
- `Insert`, `FindByStudent`, `FindByQuiz`
- Join: `FindByStudentWithQuiz` (computes `Percent`)

**StudyGroups**
- `Insert`, `FindByID` (⚠️ returns raw pgx error on no-rows, **not** `ErrNoRecord`), `FindByCourse`, `AddMember`, `FindMembers`
- `FindAllForStudent(studentID)` — groups + course title, member count, `IsMember` via `BOOL_OR` (note: with a LEFT JOIN this returns a row per group; `IsMember` is accurate)

### Handler ↔ model usage matrix

Which models are actually exercised by live handlers today:

| Model | Used by handlers | Unused-but-available API |
|-------|------------------|--------------------------|
| Users | Exists, Insert, Authenticate, FindByID | FindByEmail |
| Courses | Insert, FindByIDWithTeacher, FindAllWithTeacher | FindByID, FindByTeacher, FindAll, Update, Delete |
| Quizzes | FindByID, FindByCourse, FindAllWithCourse | Insert, Delete |
| Submissions | FindByStudentWithQuiz | Insert, FindByStudent, FindByQuiz |
| StudyGroups | FindAllForStudent | everything else |
| Questions | — none — | entire API (quiz-taking will need it) |
| Materials | — none — | entire API |

---

## 6. Cross-cutting concerns

### `internal/middleware` (single file)

| Middleware | Wired in routes? | Behaviour |
|------------|------------------|-----------|
| `SetSecurityHeaders` | ✅ | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 0`, `Referrer-Policy: strict-origin-when-cross-origin` |
| `LogRequest` | ✅ | Plain-text line via `fmt.Printf` (method, path, proto, status, duration) — **bypasses the app's JSON slog** |
| `RecoverPanic` | ✅ | Sets `Connection: close`, 500, logs panic + stack (plain text) |
| `SecureHeaders` (CSP) | ❌ defined, not routed | Would set `Content-Security-Policy` allowing self + inline style/script |
| `responseWriter` | internal | Status-code capturing wrapper used by `LogRequest` |

### Session keys

| Key | Type | Written by | Read by |
|-----|------|------------|---------|
| `authenticatedUserID` | string (UUID) | `login`, cleared by `logout`/`loadCurrentUser` | `authenticate`, `loadCurrentUser`, `createCourse` |
| `userRole` | string | `login`, cleared on logout/stale | `requireRole`, `dashboard` |
| `flash` | string | register/login/logout/create-course/submit-quiz | `newTemplateData` (pop-on-read) |

### Validator (`internal/validator`)

Thread-safe (`sync.RWMutex`) field-error accumulator following the *Let's Go*
pattern:

- `New()`, `Valid()`, `CheckField(ok, field, msg)`, `AddFieldError`,
  `AddNonFieldError`, `GetFieldErrors`, `GetNonFieldErrors`
- Rule helpers: `NotBlank`, `MinChars`, `MaxChars`, `PermittedValue`
- ❌ `Matches(value, rx)` is a **stub returning `true`** (`validator.go:73`) —
  and no handler even calls it. **There is no email-format validation
  anywhere**; a form-level `<input type="email">` hint is the only guard.

---

## 7. Database schema

File: `migrations/001_initial_schema.sql` (single migration, applied manually —
no migration tool). Requires `CREATE EXTENSION IF NOT EXISTS pgcrypto` for
`gen_random_uuid()`.

```
users ──< courses ──< materials
              │──< quizzes ──< questions
              │        │──< submissions >── users
              └──< study_groups ──< study_group_members >── users
```

| Table | Key columns | Constraints / notes |
|-------|-------------|---------------------|
| `users` | id UUID PK, name, email UNIQUE, password_hash TEXT, role | `role IN ('student','teacher')` CHECK; timestamps |
| `courses` | id UUID PK, title, description, teacher_id → users | `ON DELETE CASCADE` from teacher |
| `materials` | id UUID PK, course_id → courses, title, description, file_url | cascade delete |
| `quizzes` | id UUID PK, course_id → courses, title, description | cascade delete |
| `questions` | id UUID PK, quiz_id → quizzes, question_text, option_a…d, correct_answer CHAR(1) | `correct_answer IN ('A','B','C','D')`; fixed four-option MCQ shape; cascade delete |
| `submissions` | id UUID PK, quiz_id → quizzes, student_id → users, score INT DEFAULT 0, total_questions INT | cascade delete both sides; **no UNIQUE(quiz_id, student_id)** — duplicate attempts allowed (intended for "best score" semantics) |
| `study_groups` | id UUID PK, name, course_id → courses (**nullable**) | cascade delete |
| `study_group_members` | (study_group_id, student_id) composite PK, joined_at | cascades both sides |

Schema ↔ model mapping is 1:1 with §5. `updated_at` exists only on `users` and
`courses`; nothing auto-updates it (no trigger — only `CourseModel.Update`
writes `CURRENT_TIMESTAMP` explicitly).

---

## 8. Frontend assets

Server-rendered only; served from `./ui/static` at `/static/*`.

### Templates (`ui/html/`) — 26 files, byte counts verified

| Area | File (lines) | Status |
|------|--------------|--------|
| `layouts/` | `base.html` (73) | ✅ Root `"base"` template — parsed into every page set, every page renders through it |
| | `public.html` (8) | ⚠️ Defines only `body`; parsed by nothing (app or preview tool) |
| | `student.html` (0), `teacher.html` (0) | ❌ 0-byte, unparsed |
| `components/` | `navbar.html` (167) | ✅ Used by home page (`pages/home/index.html:3`) |
| | `sidebar.html` (111) | ✅ Used by all student pages |
| | `footer.html` (112) | ⚠️ Parsed into every page set but **never invoked by any page** — dead weight |
| | `course-card.html` (0), `quiz-card.html` (0) | ❌ 0-byte — parse into every set but contribute nothing |
| `pages/home/` | `index.html` (2381) | ✅ Landing page, rendered by `home` |
| `auth/` | `register.html` (66), `login.html` (33) | ✅ Rendered by auth handlers; **also parsed into every other page set** |
| `student/` | `courses.html` (271) | ✅ Full dashboard (sidebar, stats, continue-learning, courses, recent activity) |
| | `course.html` (155) | ✅ Course detail + quiz list |
| | `quizzes.html` (125) | ✅ Quiz listing — but every "Open" link targets a missing GET route (§3) |
| | `results.html` (144) | ✅ Results history + stats |
| | `study-groups.html` (113) | ✅ Read-only group listing |
| | `quiz.html` (315) | ⚠️ Unrouted; header comment documents a frontend contract (`Quiz`, `Questions`, `CorrectAnswers`) that **does not exist in `templateData`**; only `tools/quiz-preview` ever renders it (with fake data) |
| | `dashboard.html` (0) | ❌ 0-byte, unrouted (missed by earlier index versions) |
| `teacher/` | `course-detail.html` (26) | ⚠️ The **only** non-empty teacher page — a "coming soon" empty state |
| | `courses.html` (0), `create-course.html` (0), `create-quiz.html` (0), `submissions.html` (0) | ❌ Routed to 0-byte files → blank pages |
| | `analytics.html` (0), `dashboard.html` (0) | ❌ 0-byte, unrouted |
| | `quizzes.html` | ❌ **File does not exist** — `GET /teacher/quizzes` 500s |

CSS loading: `base.html` links 16 of the 18 CSS files in its shared `<head>`.
The two it omits — `dashboard.css` and `quiz-take.css` — are loaded per-page
via each page's `{{ define "styles" }}` block (every student page adds
`dashboard.css`; only `quiz.html` adds `quiz-take.css`). So all CSS is
reachable, but only on pages that need it.

### Static assets (`ui/static/`)

- **CSS** (`css/`, 18 files): token layer (`variables.css`, `typography.css`,
  `reset.css`, `animations.css`, `responsive.css`), chrome (`layout.css`,
  `navbar.css`, `footer.css`, `hero.css`, `cta.css`), feature styles
  (`dashboard.css`, `personalised.css`, `intelligence.css`,
  `learning-loop.css`, `study-group.css`, `register.css`, `quiz-take.css`,
  `quiz-preview.css`). Design tokens mirror `DESIGN.md` (indigo primary
  `#4f46e5`, Inter).
- **JS** (`js/`, 6 files): `navbar.js`, `animation.js`, `intelligence.js`,
  `quiz-preview.js` (loaded globally by `base.html`); `dashboard.js` (student
  pages); `quiz-take.js` (client-side quiz taking — only on unrouted
  `quiz.html`).
- **img/**: `logo.svg`.

---

## 9. Tooling & infrastructure

### Makefile

> `include .env` at the top — every target fails if `.env` is absent
> (copy `.env.example`).

| Target | Command | Notes |
|--------|---------|-------|
| `build` | `go build -o ./tmp/sabify ./cmd/web` | |
| `run` | `go run ./cmd/web` | Uses Go default `:4000` unless `APP_PORT` set in `.env` |
| `test` | `go test -v ./...` | Passes vacuously — **zero test files exist** (verified) |
| `lint` | `go vet ./...` | |
| `clean` | removes `./tmp` | |
| `db_up` / `db_down` | docker compose up/down | Postgres 16, container `sabify-postgres`, host port **5434** |
| `migrate` | `psql -h localhost -p 5434 -U sabify -d sabify_db -f migrations/001_initial_schema.sql` | Manual, non-idempotent (second run errors on existing tables) |

### Dockerfile

Multi-stage: `golang:1.23-alpine` builder → `alpine:3.20` runtime, non-root
`sabify` user, copies binary + `ui/` + `migrations/`, exposes 4000.
❌ Builder image ships Go **1.23** while `go.mod` declares `go 1.25.13` — a
container build fails at module resolution until the tag is bumped.

### CI

`.github/workflows/pr-check.yml` — triggers on PRs to `main`, single step
echoes success. **Placeholder; no real gates.** Also present in `.github/`: an
installed **impeccable** design-tooling suite (`agents/`, `hooks/`,
`skills/impeccable/` with ~150 reference/script files) — tooling, not app code.

### Environment variables (`.env.example`)

| Var | Example | Consumed by |
|-----|---------|-------------|
| `APP_ENV` | development | ❌ nothing reads it (yet) |
| `APP_PORT` | 8080 | `main.go:73` — overrides listen address |
| `DB_HOST/PORT/USER/PASSWORD/NAME/SSLMODE` | localhost / 5434 / sabify / … | DSN assembly |

### Dev aids & meta files

| Path | Purpose |
|------|---------|
| `tools/quiz-preview/` | Standalone program (`main.go`): renders the **real** `ui/html` student templates with realistic fake data to an offline HTML bundle in the OS temp dir and opens the browser (`go run ./tools/quiz-preview`, `-open=false` to suppress). This is the **only** thing that renders `student/quiz.html` today. Ships no routes, touches no models. |
| `PRODUCT.md` | Product brief: two-sided AI LMS, closed teach→assess→analyze→personalize→collaborate loop |
| `DESIGN.md` | Design-system tokens (palette, type scale) consumed by the CSS layer |
| `doc/backend.md` | ❌ Stale — describes a Services/Repositories architecture that was never built, `cmd/server` entry point, port 8080 |
| `doc/frontend.md` | ❌ Empty file |
| `doc/restructure-plan.md` | Historical plan for the *Let's Go* structure — implemented |
| `AGENTS.md` | Coding-agent conventions (source of several gotchas repeated in §10) |
| `.impeccable/` | Design-tooling config (`design.json` tokens + component CSS, `critique/2026-08-20…home-index…md`) |
| `command` | Stray scratch file of task reminders; **partially stale** (item 1 claims auth templates are 0-byte — they are not anymore) |
| `web.exe` | ⚠️ Compiled Windows binary at repo root — **untracked** (gitignored via `*.exe`), stray local artifact |
| `static/css/register.css` | ❌ Stray **tracked** near-duplicate of `ui/static/css/register.css`; a genuinely different older version (body-scoped, 198 lines vs 175 scoped `.register-page`); **not served** (file server roots at `./ui/static`) |

---

## 10. Known gaps & gotchas

Consolidated from AGENTS.md plus direct code verification. Each item states
current reality, not intention.

1. **Template cache is startup-only** — edits under `ui/html` require an app
   restart (`main.go:newTemplateCache`).
2. **Session cookie is always `Secure: true`** (`main.go:94`) — login silently
   fails over plain HTTP. Despite AGENTS.md's advice, setting
   `APP_ENV=development` does **not** help because no code reads `APP_ENV`.
3. **Port confusion by design:** `.env.example` says `APP_PORT=8080`, the Go
   flag default is `:4000`, and env beats flag. `make run` therefore listens on
   whatever `APP_PORT` is in your `.env`.
4. **Zero tests.** `make test` passes vacuously (verified: no `*_test.go`).
5. **CI is a placeholder** — `pr-check.yml` echoes success.
6. **Stale docs:** `README.md` describes a Next.js/NestJS/Python stack;
   `doc/backend.md` shows a Services/Repositories architecture that was never
   built; `doc/frontend.md` is empty; `command` is partially stale.
7. **Teacher surface is effectively unusable:** `GET /teacher/courses` renders
   a **0-byte** template (blank page), `GET /teacher/quizzes` **500s** (missing
   template file), `POST /teacher/quizzes` ignores its body, and there is **no
   course-creation form UI anywhere** — `createCourse` is the only write path
   and nothing links to it.
8. **Quiz-taking is unreachable:** no `GET /student/quizzes/{id}` route, so
   every "Open" link on `/student/quizzes` 404s; `submitQuiz` persists nothing
   (needs `Questions.FindByQuiz` + `Submissions.Insert`); `student/quiz.html`
   references `Quiz`/`Questions`/`CorrectAnswers` fields that `templateData`
   does not define.
9. **Login feedback is broken twice over:** the login template never renders
   form values or field errors (failed logins silently re-render), and login
   doesn't normalize the email (lowercase/trim) the way register does, so
   mixed-case registrations can't log in.
10. **No email-format validation** — `validator.Matches` is a stub returning
    `true` and no handler calls it anyway.
11. **Logging inconsistency:** app logs JSON via slog, but
    `middleware.LogRequest` and `RecoverPanic` print plain text with
    `fmt.Printf`.
12. **Dockerfile/go.mod drift** — builder image is Go 1.23, module requires
    1.25.13.
13. **Dead weight:** two 0-byte role layouts, two 0-byte card components, four
    0-byte unrouted role pages (`student/dashboard.html`,
    `teacher/{analytics,dashboard}.html`), unwired CSP middleware, untracked
    `web.exe`, tracked stray `static/css/register.css`.
14. **Migration is manual and non-idempotent** — `make migrate` twice errors on
    existing tables.
15. **`Makefile` hard-requires `.env`** due to `include .env`.
16. **`studentResults` shows `CoursesAvailable: 0`** on its stat card (handler
    passes 0, not the real count).
17. **`StudyGroups.FindByID` doesn't map no-rows to `ErrNoRecord`** — a 404
    handler would need to special-case `pgx.ErrNoRows`.

---

## 11. Audit findings — 2026-09-03

Full working-tree audit performed on 2026-09-03 (last commit: 2026-08-24).
Each finding is verified; none are inferred from docs. **Items marked ❌ in the
route table (§3) and template table (§8) are the same findings as C-1/C-2 here.**

### Critical

| # | Finding | Evidence |
|---|---------|----------|
| C-1 | `GET /teacher/quizzes` always 500s — the rendered template `teacher/quizzes.html` **does not exist** on disk | `teacher_handlers.go:73`; `ui/html/teacher/` glob has no `quizzes.html` |
| C-2 | Six teacher routes render **0-byte** templates → blank pages with no UI: `teacher/courses.html`, `create-course.html`, `create-quiz.html`, `submissions.html`, `analytics.html`, `dashboard.html`; there is **no course-creation form anywhere** | `wc -l` on `ui/html/teacher/*.html` |
| C-3 | Quiz-taking is unreachable: `student/quizzes.html` links every quiz to `/student/quizzes/{id}` (GET) but no such route is registered | `routes.go:56–57` vs `ui/html/student/quizzes.html:84` |
| C-4 | Failed logins give zero feedback — `login` re-renders `auth/login.html` with `Form`/`FormErrors`, but the template renders neither | `handlers.go` `login`; `ui/html/auth/login.html` (no `.Form.*`/`.FormErrors` refs) |
| C-5 | Login is case-sensitive on email: register lowercases/trims before insert, login passes the raw value to `Authenticate` → mixed-case registrations cannot log in with the same text | `handlers.go` register vs login; `users.go` `Authenticate` |
| C-6 | Session cookie `Secure: true` is hard-coded — the whole app is unusable over plain HTTP, and `APP_ENV` (the documented workaround) is never read | `main.go:94`; grep for `APP_ENV` in Go = 0 hits |

### High

| # | Finding | Evidence |
|---|---------|----------|
| H-1 | `student/quiz.html` (315 lines, fully designed) references `Quiz`, `Questions`, `CorrectAnswers` — none exist in `templateData`; the template can only render via `tools/quiz-preview` with fake data | `helpers.go` `templateData`; header comment in `ui/html/student/quiz.html` |
| H-2 | `submitQuiz` validates the quiz exists then redirects — **no grading, no `Submissions.Insert`**; result screens can only ever show seed data | `student_handlers.go` `submitQuiz` |
| H-3 | Dockerfile builds with Go 1.23 while `go.mod` requires 1.25.13 — container builds fail | `Dockerfile` vs `go.mod` |
| H-4 | No email-format validation anywhere: `validator.Matches` is a stub and no handler calls it | `validator.go:73` |
| H-5 | `studentResults` passes `0` for `CoursesAvailable` → the stat card lies ("0 courses available") even with data | `student_handlers.go` `studentResults` |

### Medium

| # | Finding | Evidence |
|---|---------|----------|
| M-1 | `teacherCourseDetail` ignores the `{id}` URL param entirely — always renders the same "coming soon" shell | `teacher_handlers.go` `teacherCourseDetail` |
| M-2 | `StudyGroups.FindByID` returns raw `pgx.ErrNoRows` instead of `ErrNoRecord` (inconsistent with every other model) | `studygroups.go` `FindByID` |
| M-3 | `middleware.LogRequest`/`RecoverPanic` bypass the JSON slog logger (plain `fmt.Printf`) | `internal/middleware/middleware.go` |
| M-4 | `student/study-groups.html` shows `IsMember`/`MemberCount` via a LEFT JOIN + `BOOL_OR` — correct but relies on GROUP BY semantics; groups with zero members are fine, but the rowset is non-intuitive for future extension | `studygroups.go` `FindAllForStudent` |
| M-5 | `.env.example` carries a stray `[TEMPLATE]` header line (harmless — godotenv skips it — but untidy) | `.env.example` |

### Low / hygiene

| # | Finding | Evidence |
|---|---------|----------|
| L-1 | `web.exe` — untracked compiled binary sitting at repo root | `git ls-files` (absent) + `ls` (present) |
| L-2 | `static/css/register.css` — tracked stray duplicate, older body-scoped variant, never served | `git ls-files`; diff vs `ui/static/css/register.css` |
| L-3 | `doc/frontend.md` is empty; `doc/backend.md` and `README.md` describe stacks that don't exist | file contents |
| L-4 | `command` scratch file is partially stale (claims auth templates are 0-byte; they are 66/33 lines and parsed) | `command` vs `wc -l ui/html/auth/*.html` |
| L-5 | Two 0-byte component files (`course-card.html`, `quiz-card.html`) are parsed into every template set, plus four 0-byte role layouts/pages; `footer.html` is parsed everywhere but never invoked | `wc -l ui/html/**/*.html`; `grep 'template "footer"' ui/html` = 0 hits |
| L-6 | `PRODUCT.md` says the home page is 2404 lines; it is 2381 (trivial drift) | `wc -l ui/html/pages/home/index.html` |
| L-7 | Migration is single-file and non-idempotent; `updated_at` has defaults but no auto-maintenance | `migrations/001_initial_schema.sql` |

---

*Maintained as the canonical map of what exists versus what is planned. Update
this index whenever routes, models, templates, or schema change, and re-run the
§11 audit after any significant work.*
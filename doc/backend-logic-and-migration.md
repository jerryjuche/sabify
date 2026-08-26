# SABIFY Backend Logic and Migration Update

This document records the actual application logic and database changes that were implemented in the current codebase. It reflects the live Go handlers, models, and migration files rather than the older placeholder architecture notes.

## 1. Application architecture

The app is a Go monolith using:
- Chi router
- pgx connection pool
- SCS session manager
- Go HTML templates
- PostgreSQL

The main wiring is in `cmd/web/main.go` and the route registration lives in `cmd/web/routes.go`.

Key runtime setup:
- template cache is created once at startup
- PostgreSQL DSN is loaded from `.env`
- a session manager is configured with 24-hour lifetime
- security headers and request logging middleware are attached globally
- the app exposes static assets from `ui/static` via `/static/*`

## 2. Auth and session flow

The authentication logic is implemented in `cmd/web/handlers.go`.

### Registration
`POST /register` performs the following:
1. Parses the form
2. Validates name, email, password, confirmation, role, and policy checkbox
3. Rejects blank or invalid values with field-level errors
4. Checks whether the email already exists
5. Hashes the password using bcrypt
6. Inserts the user into the `users` table
7. Redirects to `/login` with a flash message

### Login
`POST /login` performs the following:
1. Validates email and password presence
2. Calls `Users.Authenticate()`
3. Rejects invalid credentials with an auth error message
4. Stores `authenticatedUserID` and `userRole` in the session
5. Redirects to `/dashboard`

### Authentication middleware
The app uses a session-backed `authenticate` middleware that checks for the user ID in the session.

If no authenticated session is present:
- the visitor is redirected to `/login`

The `loadCurrentUser()` helper resolves the current user from the session and clears stale sessions if the user record no longer exists.

## 3. Route structure and role access

The application route map in `cmd/web/routes.go` defines explicit public and protected groups.

### Public routes
- `/`
- `/health`
- `/register`
- `/login`
- `/logout`

### Authenticated shared route
- `/dashboard`

### Teacher-only routes
- `/teacher/dashboard`
- `/teacher/courses`
- `/teacher/courses/new`
- `/teacher/courses/{id}`
- `/teacher/quizzes`
- `/teacher/quizzes/new`
- `/teacher/quizzes/{id}/edit`
- `/teacher/quizzes/{id}/delete`
- `/teacher/submissions`

### Student-only routes
- `/student/courses`
- `/student/courses/{id}`
- `/student/quizzes`
- `/student/quizzes/{id}/submit`
- `/student/results`
- `/student/study-groups`

The role guard is enforced with the `requireRole("teacher")` and `requireRole("student")` middleware pattern.

## 4. Teacher-side logic

The teacher logic is centered in `cmd/web/teacher_handlers.go`.

### Teacher dashboard
`teacherDashboard()` gathers:
- teacher user record
- total number of courses
- number of enrolled students
- active quizzes with submissions
- average score across the teacher’s classes
- recent submissions
- students needing attention

It then builds AI-style insight cards from those values.

`generateInsights()` creates messages such as:
- course creapsql -h localhost -p 5434 -U sabify -d sabify_db -f migrations/002_course_enrollments.sqltion prompt
- strong class performance
- room for improvement
- students needing attention
- no submissions yet
- no active quiz for enrolled students

### Course creation
`createCourse()` supports both GET and POST:
- GET renders a create-course form
- POST validates title and length
- creates a `Course` record tied to the session user
- stores a success flash message
- redirects to `/teacher/courses`

### Teacher course detail
`teacherCourseDetail()`:
- loads the current teacher user
- fetches course by ID
- ensures the teacher owns the course
- loads all quizzes for that course
- renders a course detail page with quiz references

### Teacher quiz list and management
The teacher routes also support:
- listing quizzes
- creating new quizzes
- editing quiz metadata
- deleting quizzes
- viewing student submissions

These handlers use the model methods on `QuizModel` and `SubmissionModel` to get real data from Postgres.

## 5. Student-side logic

The student logic is implemented in `cmd/web/student_handlers.go`.

### Student dashboard summary
`studentCourses()` loads:
- all available courses with teacher metadata
- the student’s submissions
- all quizzes with course titles
- the student’s “continue learning” queue
- aggregate stats

It computes a map of attempted quiz scores using `attemptedScoreMap()`, which stores the best percentage for each quiz the student has already attempted.

### Student course detail
`studentCourseDetail()` loads a specific course and its quizzes, then marks quiz items as attempted or not based on prior submissions.

### Student quiz listing
`studentQuizzes()` returns all available quizzes and displays per-quiz attempt state based on the student’s history.

### Quiz submission
`submitQuiz()` validates the quiz exists and then currently acknowledges the submission with a flash message and redirects to `/student/results`.

This means the submission flow is intentionally stubbed at the moment; the real interactive answer-grading flow is not yet fully wired in.

### Student results
`studentResults()` loads the student’s submission history and computes stats via `computeStudentStats()`.

## 6. Data model logic

The core model layer is in `internal/models`.

### Users
`internal/models/users.go` implements:
- `Insert()` with bcrypt hashing
- `Authenticate()` with password verification
- `FindByEmail()` and `FindByID()`
- `Exists()` for duplicate-email checks

### Courses
`internal/models/courses.go` implements:
- insert and fetch by ID
- fetch by teacher
- fetch all courses
- update and delete
- count by teacher
- joined queries for course + teacher + quiz count

The `CourseWithTeacher` record carries the teacher name and number of quizzes in a single query, which supports the student course cards.

### Quizzes
`internal/models/quizzes.go` implements:
- insert, fetch by ID, fetch by course
- update and delete
- count active quizzes per teacher
- joined query returning course title and question count via `QuizWithCourse`

### Enrollments
`internal/models/enrollments.go` adds the `course_enrollments` table access layer.

It includes:
- `Insert()` with upsert semantics
- `FindByCourse()`
- `CountByTeacher()`
- `FindStudentsNeedingAttention()` to return students below the 50% threshold

This is used to power the teacher dashboard’s attention panel.

## 7. New migration added

The database schema includes a new enrollment table migration:

`migrations/002_course_enrollments.sql`

Contents:

```sql
CREATE TABLE course_enrollments (
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrolled_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (course_id, student_id)
);
```

This table supports the relationship between courses and students, allowing the app to:
- track which students are enrolled in which courses
- count total distinct students per teacher
- identify students needing attention
- determine course participation independently from quiz submissions

## 8. Initial schema and database state

The base schema is in `migrations/001_initial_schema.sql` and includes:
- `users`
- `courses`
- `materials`
- `quizzes`
- `questions`
- `submissions`
- `study_groups`
- `study_group_members`

The additional enrollment migration extends the schema for learner/course relationships.

## 9. Migration usage

Run the database setup in order:

```bash
docker compose up -d
psql -h localhost -U sabify -d sabify_db -f migrations/001_initial_schema.sql
psql -h localhost -U sabify -d sabify_db -f migrations/002_course_enrollments.sql
```

or, if using the dockerized database flow already described in the project:

```bash
docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/001_initial_schema.sql
docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_course_enrollments.sql
```

## 10. Important implementation notes

The app is materially more complete than the older stub documentation suggested:
- auth is actually implemented
- teacher and student dashboards render real data
- course and quiz creation logic is in place
- dashboard cards and insights are calculated from database results
- the enrollment model introduces a real many-to-many course/student link

Current gaps still remain in:
- full interactive quiz answer submission grading
- finalised faculty/student study-group workflows beyond the shell UI
- deeper quiz analytics beyond the current summary cards

These are the real application-level changes that were applied in the current branch and they are now reflected in the code and docs.

# SABIFY Backend Setup & Documentation

This document explains how to set up, run, and contribute to the SABIFY backend locally.

## 1. Backend Stack

- Go
- PostgreSQL
- Docker
- Docker Compose
- Chi Router
- pgx PostgreSQL driver
- Go Templates

Architecture:

```text
Browser
   ↓
Go Router
   ↓
Handlers
   ↓
Services
   ↓
Repositories
   ↓
PostgreSQL
```

## 2. Requirements

Install:

- Go 1.22+
- Git
- Docker
- Docker Compose

Check installations:

```bash
go version
git --version
docker --version
docker compose version
```

Make sure Docker is running.

## 3. Clone the Repository

```bash
git clone <REPOSITORY_URL>
cd sabify
```

## 4. Install Go Dependencies

```bash
go mod tidy
```

If dependencies are missing:

```bash
go get github.com/go-chi/chi/v5
go get github.com/jackc/pgx/v5
go get github.com/joho/godotenv
go get github.com/gorilla/sessions
go get golang.org/x/crypto/bcrypt
go mod tidy
```

## 5. Environment Variables

Create `.env` in the project root:

```env
APP_ENV=development
APP_PORT=8080

DB_HOST=localhost
DB_PORT=5432
DB_USER=sabify
DB_PASSWORD=sabify_password
DB_NAME=sabify_db
DB_SSLMODE=disable
```

Do not commit `.env`.

Add this to `.gitignore`:

```gitignore
.env
```

## 6. Start PostgreSQL

Make sure `docker-compose.yml` exists, then run:

```bash
docker compose up -d
```

Check:

```bash
docker ps
```

## 7. Verify PostgreSQL

```bash
docker exec -it sabify-postgres psql -U sabify -d sabify_db
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

Migration:

```text
internal/database/migrations/
└── 001_initial_schema.sql
```

Run:

```bash
docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/001_initial_schema.sql
docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_course_enrollments.sql
```

Verify:

```bash
docker exec -it sabify-postgres psql -U sabify -d sabify_db
```

Then:

```sql
\dt
```

Exit:

```sql
\q
```

## 9. Start SABIFY

```bash
go run ./cmd/server
```

Expected:

```text
✅ Connected to PostgreSQL
🚀 SABIFY running on http://localhost:8080
```

Open:

```text
http://localhost:8080
```

Health check:

```text
http://localhost:8080/health
```

Expected:

```text
SABIFY is running
```

## 10. Current Project Structure

```text
sabify/
│
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── database/
│   │   ├── postgres.go
│   │   └── migrations/
│   │       └── 001_initial_schema.sql
│   ├── models/
│   │   ├── user.go
│   │   ├── course.go
│   │   ├── material.go
│   │   ├── quiz.go
│   │   ├── question.go
│   │   ├── submission.go
│   │   └── study_group.go
│   ├── handlers/
│   │   ├── auth_handler.go
│   │   ├── course_handler.go
│   │   ├── home_handler.go
│   │   ├── material_handler.go
│   │   ├── quiz_handler.go
│   │   ├── student_handler.go
│   │   ├── teacher_handler.go
│   │   └── study_group_handler.go
│   ├── services/
│   │   ├── auth_service.go
│   │   └── course_service.go
│   ├── repositories/
│   │   ├── user_repository.go
│   │   ├── course_repository.go
│   │   ├── quiz_repository.go
│   │   └── submission_repository.go
│   ├── middleware/
│   │   ├── auth.go
│   │   └── logging.go
│   └── routes/
│       └── routes.go
│
├── templates/
├── static/
├── docs/
│   └── backend.md
├── .env
├── .gitignore
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

## 11. Backend Architecture

SABIFY uses a monolithic architecture.

```text
                     SABIFY
                        │
                        ▼
                  Go HTTP Server
                        │
                        ▼
                     Router
                        │
          ┌─────────────┴─────────────┐
          ▼                           ▼
      Handlers                    Templates
          │
          ▼
       Services
          │
          ▼
     Repositories
          │
          ▼
      PostgreSQL
```

The frontend is also built with Go Templates. There is no Next.js frontend.

## 12. Current Routes

### Home

```text
GET /
```

### Health Check

```text
GET /health
```

### Authentication

```text
GET  /register
POST /register

GET  /login
POST /login
```

### Courses

```text
GET  /courses
GET  /courses/{id}
POST /courses
```

Some routes currently contain placeholder responses.

## 13. Current Application Flow

```text
Browser
   ↓
Route
   ↓
Handler
   ↓
Service
   ↓
Repository
   ↓
PostgreSQL
```

Example:

```text
POST /register
      ↓
AuthHandler
      ↓
AuthService
      ↓
UserRepository
      ↓
PostgreSQL
```

## 14. Authentication

Authentication uses bcrypt for password hashing.

```text
User Password
      ↓
   bcrypt
      ↓
Password Hash
      ↓
 PostgreSQL
```

During login:

```text
Entered Password
      ↓
bcrypt comparison
      ↓
Stored Password Hash
      ↓
Valid / Invalid
```

Session management and complete authentication middleware will be implemented later.

## 15. Useful Docker Commands

Start PostgreSQL:

```bash
docker compose up -d
```

Stop PostgreSQL:

```bash
docker compose down
```

View PostgreSQL logs:

```bash
docker compose logs postgres
```

Check containers:

```bash
docker ps
```

Connect to PostgreSQL:

```bash
docker exec -it sabify-postgres psql -U sabify -d sabify_db
```

## 16. Reset the Database

To completely reset the local database:

```bash
docker compose down -v
```

Recreate it:

```bash
docker compose up -d
```

Run the migration:

```bash
docker exec -i sabify-postgres psql -U sabify -d sabify_db < internal/database/migrations/001_initial_schema.sql
```

> WARNING: `docker compose down -v` deletes the PostgreSQL volume and all local database data.

## 17. Running Tests

```bash
go test ./...
```

Build the project:

```bash
go build ./...
```

## 18. Development Workflow

```bash
git pull
go mod tidy
docker compose up -d
go run ./cmd/server
```

After making changes:

```bash
go test ./...
```

Before creating a Pull Request:

```bash
go test ./...
go build ./...
```

## 19. Git Workflow

Do not push directly to `main`.

Create a feature branch:

```bash
git checkout -b feature/your-feature-name
```

Example:

```bash
git checkout -b feature/login
```

Commit:

```bash
git add .
git commit -m "feat: add login functionality"
```

Push:

```bash
git push origin feature/login
```

Create a Pull Request and wait for review before merging into `main`.

## 20. Current Development Status

### Completed

- [x] Go project setup
- [x] Docker PostgreSQL setup
- [x] PostgreSQL connection
- [x] Initial database schema
- [x] Go models
- [x] Repository layer
- [x] Authentication service foundation
- [x] Course service foundation
- [x] Authentication handlers foundation
- [x] Course handlers foundation
- [x] Home handler
- [x] Chi routing
- [x] Main application wiring
- [x] SABIFY homepage
- [x] Static file serving

### In Progress / Future

- [ ] Complete registration
- [ ] Complete login
- [ ] Session management
- [ ] Authentication middleware
- [ ] Course management
- [ ] Learning materials
- [ ] Quiz system
- [ ] Automatic grading
- [ ] Student dashboard
- [ ] Teacher dashboard
- [ ] Study groups
- [ ] AI Quiz Generator
- [ ] AI Teacher Assistant
- [ ] AI Learning Coach
- [ ] AI Study Group Matcher
- [ ] Python AI service
- [ ] Hugging Face integration

## 21. Future AI Architecture

AI features will be added after the core LMS is completed.

```text
                    SABIFY
                       │
                       ▼
                    Go LMS
                       │
                    HTTP API
                       │
                       ▼
              Python AI Service
                       │
                       ▼
                 Hugging Face
```

The Go application remains the main LMS application.

Python will handle AI functionality.

Planned AI features:

```text
AI Quiz Generator
AI Teacher Assistant
AI Learning Coach
AI Study Group Matcher
```

## 22. Important Development Rules

### Do not push directly to main

Always create a feature branch and submit a Pull Request.

### Keep the architecture clean

- Handlers handle HTTP requests.
- Services contain business logic.
- Repositories handle database operations.

### Do not commit `.env`

Never commit passwords, API keys, or other secrets.

### Test before pushing

```bash
go test ./...
go build ./...
```

## 23. Quick Start

For a developer who already has the requirements installed:

```bash
git clone <REPOSITORY_URL>
cd sabify
go mod tidy
docker compose up -d
docker exec -i sabify-postgres psql -U sabify -d sabify_db < internal/database/migrations/001_initial_schema.sql
go run ./cmd/server
```

Then open:

```text
http://localhost:8080
```

Health check:

```text
http://localhost:8080/health
```

## 24. Backend Development Principle

The core LMS is being built first.

AI functionality will be integrated after the LMS foundation is stable.

Current priority:

```text
1. Authentication
2. Courses
3. Materials
4. Quizzes
5. Automatic Grading
6. Student Dashboard
7. Teacher Dashboard
8. Study Groups
9. AI Features
```

The AI layer will be added later without replacing the existing Go LMS architecture.
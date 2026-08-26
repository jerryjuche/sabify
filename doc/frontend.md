# SABIFY Frontend Update

This document records the UI work that has already been applied in the current codebase. It reflects the actual templates and styling in `ui/html` and `ui/static` as of the current build, not the earlier mockup plan.

## 1. Product direction

The frontend follows a single-page SaaS-style learning intelligence aesthetic with a strong dashboard shell for both teacher and student flows.

Core visual characteristics:
- clean, modern landing-page hero
- light dashboard surfaces with purple/indigo accents
- card-based metrics and activity panels
- app-shell layout for authenticated navigation
- focused quiz-taking views placed inside the dashboard frame
- mobile-friendly responsive styling

## 2. Layout and shell

### Base shell
The global app shell is defined in `ui/html/layouts/base.html`.

Included assets:
- `variables.css`
- `reset.css`
- `typography.css`
- `layout.css`
- `navbar.css`
- `hero.css`
- `animations.css`
- `learning-loop.css`
- `intelligence.css`
- `quiz-preview.css`
- `personalised.css`
- `responsive.css`
- `cta.css`
- `footer.css`
- `study-group.css`
- `register.css`

The base template also loads the shared JS files:
- `navbar.js`
- `animation.js`
- `intelligence.js`
- `quiz-preview.js`

### Public landing page
The main landing page is `ui/html/pages/home/index.html`.

Implemented sections include:
- hero headline and CTA area
- product preview mock dashboard visual
- metrics block for class intelligence
- learning loop and personalised experience sections
- CTA/footer combination

This page is designed to sell the learning-intelligence concept and route users to registration.

## 3. Navigation and auth UI

### Navbar and footer
The public navigation is provided by `ui/html/components/navbar.html` and `ui/html/components/footer.html`.

Included behavior:
- brand area and home link
- public nav links to features and login/register
- account actions in the right column
- responsive collapse behavior via `navbar.js`

### Registration flow
`ui/html/auth/register.html` implements the account creation screen.

Fields:
- signup role selector (student/teacher)
- full name
- email
- password
- confirm password
- institutional terms checkbox

Validation messaging is displayed inline under each input, and the page is styled to fit the dashboard aesthetic.

### Login flow
`ui/html/auth/login.html` follows the same style and includes the standard login form tied to the session-based auth flow.

## 4. Teacher dashboard UI

The teacher dashboard is implemented in `ui/html/teacher/dashboard.html`.

### Included sections
- top bar with menu button and page title
- welcome hero with teacher name
- metric cards for courses, students, active quizzes, and average performance
- recent submissions section
- AI insights panel
- students requiring attention panel
- footer

The `dashboard.css` stylesheet governs the full dashboard shell, including:
- cards
- stat tiles
- activity lists
- insight rows
- empty states
- responsive breakpoints

## 5. Student dashboard UI

Student dashboard markup is in `ui/html/student/courses.html`.

### Included sections
- greeting and progress summary
- animated stat cards for courses available, quizzes taken, average score, and best score
- “Continue learning” quiz tiles for unattempted work
- course grid with teacher metadata and quiz counts
- recent activity section
- empty-state content when no courses or attempts exist

This interface uses count-up animations and tile layouts to create a dashboard experience rather than a simple list view.

## 6. Course and quiz pages

The app includes dedicated templates for:
- `ui/html/student/course.html`
- `ui/html/student/quizzes.html`
- `ui/html/student/quiz.html`
- `ui/html/student/results.html`
- `ui/html/student/study-groups.html`
- `ui/html/teacher/courses.html`
- `ui/html/teacher/create-course.html`
- `ui/html/teacher/create-quiz.html`
- `ui/html/teacher/course-detail.html`
- `ui/html/teacher/quizzes.html`
- `ui/html/teacher/submissions.html`
- `ui/html/teacher/edit-quiz.html`

### Student pages
- course detail pages show course info and quizzes
- quiz list pages show current available quizzes and attempt status
- result pages show summary stats and prior attempts
- study-group page uses the shared dashboard styling with group cards and metadata

### Teacher pages
- course detail page displays quizzes attached to a course
- create course form is simple, validated, and session-aware
- create/edit quiz views are structured for quiz metadata and question setup
- submissions page is designed to review student activity

## 7. Styling system

The CSS in `ui/static/css` is split into focused modules:
- `dashboard.css` for authenticated app shells and grids
- `quiz-take.css` for the focus-mode quiz experience
- `quiz-preview.css` for landing page preview blocks
- `study-group.css` for study group views
- `register.css` for auth form styling
- `navbar.css`, `hero.css`, `intelligence.css`, `cta.css`, `footer.css` for public marketing site styling
- `responsive.css` for responsive adjustments

The visual language stays consistent with the product brand: dashboard surfaces, soft shadows, strong headings, indigo-violet accent colors, and card-based information density.

## 8. Interactive UI behaviour

The frontend includes frontend JS for:
- navbar mobile toggling
- scroll and reveal effects
- dashboard menu drawer behavior
- quiz preview interactions
- focused quiz-taking and question switching

The main JS entry points are:
- `ui/static/js/navbar.js`
- `ui/static/js/animation.js`
- `ui/static/js/intelligence.js`
- `ui/static/js/quiz-preview.js`
- `ui/static/js/quiz-take.js`

## 9. Applied frontend state

The current interface already applies the following concrete changes:
- public marketing homepage with dashboard mockup
- auth screens matching the product aesthetic
- authenticated teacher and student dashboard shells
- dynamic stats and attempted-quiz state
- course, quiz, and submission workflows designed around real app data
- responsive layout for all major flows

This is the working UI that the current Go template app renders, and it is the reference for future front-end refinements.

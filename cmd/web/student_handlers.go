package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"sabify/internal/models"
)

/*
 * attemptedScoreMap builds a quizID -> best score
 * percentage map from a student's submission history.
 */

func attemptedScoreMap(submissions []models.SubmissionWithQuiz) map[string]int {
	attempted := make(map[string]int)

	for _, s := range submissions {
		if s.Percent < 0 {
			continue
		}

		if s.Percent > attempted[s.QuizID] {
			attempted[s.QuizID] = s.Percent
		}
	}

	return attempted
}

func (app *application) studentCourses(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courses, err := app.models.Courses.FindAllWithTeacher(r.Context())
	if err != nil {
		app.serverError(w, err)
		return
	}

	submissions, err := app.models.Submissions.FindByStudentWithQuiz(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	quizzes, err := app.models.Quizzes.FindAllWithCourse(r.Context())
	if err != nil {
		app.serverError(w, err)
		return
	}

	/*
	 * "Continue learning" surfaces up to three
	 * quizzes the student has not attempted yet.
	 */

	attempted := attemptedScoreMap(submissions)
	upcoming := make([]models.QuizWithCourse, 0, 3)

	for _, q := range quizzes {
		if _, done := attempted[q.ID]; done {
			continue
		}
		upcoming = append(upcoming, q)
		if len(upcoming) == 3 {
			break
		}
	}

	recent := submissions
	if len(recent) > 5 {
		recent = recent[:5]
	}

	data := app.newTemplateData(r)
	data.Title = "My Courses"
	data.User = user
	data.CurrentPage = "courses"
	data.Courses = courses
	data.Submissions = recent
	data.UpcomingQuizzes = upcoming
	data.Stats = computeStudentStats(submissions, len(courses))

	app.render(w, http.StatusOK, "student/courses.html", data)
}

func (app *application) studentCourseDetail(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courseID := chi.URLParam(r, "id")

	course, err := app.models.Courses.FindByIDWithTeacher(r.Context(), courseID)
	if err != nil {
		if err == models.ErrNoRecord {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	quizzes, err := app.models.Quizzes.FindByCourse(r.Context(), courseID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	submissions, err := app.models.Submissions.FindByStudentWithQuiz(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = course.Title
	data.User = user
	data.CurrentPage = "courses"
	data.Course = course
	data.CourseQuizzes = quizzes
	data.Attempted = attemptedScoreMap(submissions)

	app.render(w, http.StatusOK, "student/course.html", data)
}

func (app *application) studentQuizzes(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quizzes, err := app.models.Quizzes.FindAllWithCourse(r.Context())
	if err != nil {
		app.serverError(w, err)
		return
	}

	submissions, err := app.models.Submissions.FindByStudentWithQuiz(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Available Quizzes"
	data.User = user
	data.CurrentPage = "quizzes"
	data.Quizzes = quizzes
	data.Attempted = attemptedScoreMap(submissions)

	app.render(w, http.StatusOK, "student/quizzes.html", data)
}

func (app *application) submitQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quizID := chi.URLParam(r, "id")

	_, err := app.models.Quizzes.FindByID(r.Context(), quizID)
	if err != nil {
		if err == models.ErrNoRecord {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	/*
	 * Interactive quiz-taking is not wired up yet;
	 * acknowledge the attempt and send the student
	 * to their results page.
	 */

	app.session.Put(r.Context(), "flash", "Your submission was received.")
	http.Redirect(w, r, "/student/results", http.StatusSeeOther)
}

func (app *application) studentResults(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	submissions, err := app.models.Submissions.FindByStudentWithQuiz(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "My Results"
	data.User = user
	data.CurrentPage = "results"
	data.Submissions = submissions
	data.Stats = computeStudentStats(submissions, 0)

	app.render(w, http.StatusOK, "student/results.html", data)
}

func (app *application) studentStudyGroups(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	groups, err := app.models.StudyGroups.FindAllForStudent(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Study Groups"
	data.User = user
	data.CurrentPage = "groups"
	data.Groups = groups

	app.render(w, http.StatusOK, "student/study-groups.html", data)
}

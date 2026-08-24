package main

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"sabify/internal/models"
)

type templateData struct {
	CurrentYear int
	Title       string
	Description string
	Flash       string
	Form        map[string]string
	FormErrors  map[string]string

	/*
	 * Authenticated-shell context.
	 */

	User        *models.User
	CurrentPage string

	/*
	 * Dashboard payloads.
	 */

	Courses         []models.CourseWithTeacher
	Course          *models.CourseWithTeacher
	CourseQuizzes   []models.Quiz
	Quizzes         []models.QuizWithCourse
	UpcomingQuizzes []models.QuizWithCourse
	Submissions     []models.SubmissionWithQuiz
	Groups          []models.StudyGroupWithMeta
	Stats           StudentStats

	/*
	 * QuizID -> best score percentage achieved
	 * by the current student, used to badge
	 * quiz listings with attempt state.
	 */

	Attempted map[string]int
}

/*
 * StudentStats summarises a student's quiz history
 * for the dashboard stat cards. All scores are
 * percentages.
 */

type StudentStats struct {
	CoursesAvailable int
	QuizzesTaken     int
	AverageScore     int
	BestScore        int
}

func computeStudentStats(submissions []models.SubmissionWithQuiz, coursesAvailable int) StudentStats {
	stats := StudentStats{
		CoursesAvailable: coursesAvailable,
		QuizzesTaken:     len(submissions),
	}

	total := 0
	counted := 0

	for _, s := range submissions {
		if s.TotalQuestions <= 0 {
			continue
		}

		pct := s.Score * 100 / s.TotalQuestions
		total += pct
		counted++

		if pct > stats.BestScore {
			stats.BestScore = pct
		}
	}

	if counted > 0 {
		stats.AverageScore = total / counted
	}

	return stats
}

func (app *application) serverError(w http.ResponseWriter, err error) {
	trace := fmt.Sprintf("%s\n%s", err.Error(), debug.Stack())
	app.logger.Error(trace)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (app *application) clientError(w http.ResponseWriter, status int) {
	http.Error(w, http.StatusText(status), status)
}

func (app *application) notFound(w http.ResponseWriter) {
	app.clientError(w, http.StatusNotFound)
}

func (app *application) render(w http.ResponseWriter, status int, page string, data templateData) {
	ts, ok := app.templateCache[page]
	if !ok {
		err := fmt.Errorf("the template %s does not exist", page)
		app.serverError(w, err)
		return
	}

	buf := new(bytes.Buffer)

	err := ts.ExecuteTemplate(buf, "base", data)
	if err != nil {
		app.serverError(w, err)
		return
	}

	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (app *application) newTemplateData(r *http.Request) templateData {
	return templateData{
		CurrentYear: time.Now().Year(),
		Flash:       app.session.PopString(r.Context(), "flash"),
	}
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	enrolledIDs, err := app.models.Enrollments.FindByStudent(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	allCourses, err := app.models.Courses.FindAllWithTeacher(r.Context())
	if err != nil {
		app.serverError(w, err)
		return
	}

	enrolledSet := make(map[string]bool, len(enrolledIDs))
	for _, id := range enrolledIDs {
		enrolledSet[id] = true
	}

	var enrolledCourses []models.CourseWithTeacher
	var availableCourses []models.CourseWithTeacher
	for _, c := range allCourses {
		if enrolledSet[c.ID] {
			enrolledCourses = append(enrolledCourses, c)
		} else {
			availableCourses = append(availableCourses, c)
		}
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
	data.Courses = enrolledCourses
	data.AvailableCourses = availableCourses
	data.Submissions = recent
	data.UpcomingQuizzes = upcoming
	data.Stats = computeStudentStats(submissions, len(enrolledCourses))

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

	enrolled, err := app.models.Enrollments.IsEnrolled(r.Context(), courseID, user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	if !enrolled {
		app.session.Put(r.Context(), "flash", "You are not enrolled in this course.")
		http.Redirect(w, r, "/student/courses", http.StatusSeeOther)
		return
	}

	quizzes, err := app.models.Quizzes.FindByCourse(r.Context(), courseID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	materials, err := app.models.Materials.FindByCourse(r.Context(), courseID)
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
	data.Materials = materials
	data.Enrolled = enrolled
	data.Attempted = attemptedScoreMap(submissions)

	app.render(w, http.StatusOK, "student/course.html", data)
}

func (app *application) enrollInCourse(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courseID := chi.URLParam(r, "id")

	_, err := app.models.Courses.FindByID(r.Context(), courseID)
	if err != nil {
		app.notFound(w)
		return
	}

	if err := app.models.Enrollments.Insert(r.Context(), courseID, user.ID); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "You have been enrolled in the course!")
	http.Redirect(w, r, "/student/courses/"+courseID, http.StatusSeeOther)
}

func (app *application) unenrollFromCourse(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courseID := chi.URLParam(r, "id")

	if err := app.models.Enrollments.Delete(r.Context(), courseID, user.ID); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "You have been unenrolled from the course.")
	http.Redirect(w, r, "/student/courses", http.StatusSeeOther)
}

func (app *application) studentQuizzes(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	available, taken, err := app.models.Retakes.FindQuizzesForStudent(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Quizzes"
	data.User = user
	data.CurrentPage = "quizzes"
	data.AvailableQuizzes = available
	data.TakenQuizzes = taken

	app.render(w, http.StatusOK, "student/quizzes.html", data)
}

func (app *application) takeQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quizID := chi.URLParam(r, "id")

	quiz, err := app.models.Quizzes.FindByIDWithCourse(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	submitted, err := app.models.Submissions.HasSubmitted(r.Context(), quizID, user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	if submitted {
		retakeAllowed, err := app.models.Retakes.IsAllowed(r.Context(), quizID, user.ID)
		if err != nil {
			app.serverError(w, err)
			return
		}

		if !retakeAllowed {
			app.session.Put(r.Context(), "flash", "You have already taken this quiz.")
			http.Redirect(w, r, "/student/quizzes", http.StatusSeeOther)
			return
		}
	}

	questions, err := app.models.Questions.FindByQuiz(r.Context(), quizID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	correctAnswers := make(map[string]string, len(questions))
	for _, q := range questions {
		correctAnswers[q.ID] = q.CorrectAnswer
	}

	data := app.newTemplateData(r)
	data.Title = quiz.Title
	data.User = user
	data.CurrentPage = "quizzes"
	data.Quiz = quiz
	data.Questions = questions
	data.CorrectAnswers = correctAnswers

	app.render(w, http.StatusOK, "student/quiz.html", data)
}

func (app *application) submitQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quizID := chi.URLParam(r, "id")

	quiz, err := app.models.Quizzes.FindByIDWithCourse(r.Context(), quizID)
	if err != nil {
		if err == models.ErrNoRecord {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	questions, err := app.models.Questions.FindByQuiz(r.Context(), quizID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	correct := 0
	for _, q := range questions {
		studentAnswer := strings.TrimSpace(r.FormValue("answer_" + q.ID))
		if studentAnswer == q.CorrectAnswer {
			correct++
		}
	}

	total := len(questions)

	submission := &models.Submission{
		QuizID:         quizID,
		StudentID:      user.ID,
		Score:          correct,
		TotalQuestions: total,
	}

	if err := app.models.Submissions.Insert(r.Context(), submission); err != nil {
		app.serverError(w, err)
		return
	}

	app.models.Retakes.RevokeIfAllowed(r.Context(), quizID, user.ID)

	attempt, _ := app.models.Submissions.CountByQuizStudent(r.Context(), quizID, user.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"score":          correct,
		"total":          total,
		"submission_id":  submission.ID,
		"submitted_at":   submission.SubmittedAt,
		"quiz_title":     quiz.Title,
		"course_id":      quiz.CourseID,
		"attempt":        attempt,
	})
}

func (app *application) studentResults(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	submissions, err := app.models.Submissions.FindByStudentWithAttempt(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "My Results"
	data.User = user
	data.CurrentPage = "results"
	data.StudentSubmissions = submissions

	var subs []models.SubmissionWithQuiz
	for _, s := range submissions {
		subs = append(subs, s.SubmissionWithQuiz)
	}
	data.Stats = computeStudentStats(subs, 0)

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

func (app *application) studentViewMaterial(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	materialID := chi.URLParam(r, "materialId")
	material, err := app.models.Materials.FindByID(r.Context(), materialID)
	if err != nil {
		app.notFound(w)
		return
	}

	courseID := chi.URLParam(r, "id")
	if material.CourseID != courseID {
		app.notFound(w)
		return
	}

	if material.FileURL == "" {
		app.notFound(w)
		return
	}

	filePath := "./ui" + material.FileURL
	f, err := os.Open(filePath)
	if err != nil {
		app.notFound(w)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		app.serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename="+filepath.Base(material.FileURL))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	io.Copy(w, f)
}

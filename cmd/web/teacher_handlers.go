package main

import (
	"fmt"
	"net/http"
	"strings"

	"sabify/internal/models"
	"sabify/internal/validator"
)

func (app *application) teacherDashboard(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Dashboard"

	teacherID := app.session.GetString(r.Context(), "authenticatedUserID")

	teacher, err := app.models.Users.FindByID(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.TeacherName = teacher.Name

	totalCourses, err := app.models.Courses.CountByTeacher(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.TotalCourses = totalCourses

	totalStudents, err := app.models.Enrollments.CountByTeacher(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.TotalStudents = totalStudents

	activeQuizzes, err := app.models.Quizzes.CountActiveByTeacher(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.ActiveQuizzes = activeQuizzes

	avgScore, err := app.models.Submissions.AverageScoreByTeacher(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.AveragePerformance = avgScore

	recentSubmissions, err := app.models.Submissions.RecentByTeacher(r.Context(), teacherID, 10)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.RecentSubmissions = recentSubmissions

	attentionStudents, err := app.models.Enrollments.FindStudentsNeedingAttention(r.Context(), teacherID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.AttentionStudents = attentionStudents

	data.AIInsights = app.generateInsights(totalCourses, totalStudents, activeQuizzes, avgScore, attentionStudents, recentSubmissions)

	app.render(w, http.StatusOK, "dashboard.html", data)
}

func (app *application) generateInsights(totalCourses, totalStudents, activeQuizzes int, avgScore float64, attentionStudents []models.AttentionStudent, recentSubmissions []models.SubmissionWithDetails) []AIInsight {
	var insights []AIInsight

	if totalCourses == 0 {
		insights = append(insights, AIInsight{
			Type:    "info",
			Title:   "Get started",
			Message: "Create your first course to begin tracking student performance.",
		})
	}

	if avgScore >= 75 {
		insights = append(insights, AIInsight{
			Type:    "strength",
			Title:   "Strong class performance",
			Message: fmt.Sprintf("Your classes are averaging %.1f%% — well above the 75%% benchmark.", avgScore),
		})
	} else if avgScore > 0 {
		insights = append(insights, AIInsight{
			Type:    "trend",
			Title:   "Room for improvement",
			Message: fmt.Sprintf("Your class average is %.1f%%. Consider revisiting topics where students scored lowest.", avgScore),
		})
	}

	if len(attentionStudents) > 0 {
		insights = append(insights, AIInsight{
			Type:    "warning",
			Title:   "Students need attention",
			Message: fmt.Sprintf("%d student(s) are scoring below 50%%. A targeted review session may help.", len(attentionStudents)),
		})
	}

	if activeQuizzes > 0 && len(recentSubmissions) == 0 {
		insights = append(insights, AIInsight{
			Type:    "info",
			Title:   "No recent activity",
			Message: "Your quizzes have no submissions yet. Students may need a reminder.",
		})
	}

	if totalStudents > 0 && activeQuizzes == 0 {
		insights = append(insights, AIInsight{
			Type:    "info",
			Title:   "Create assessments",
			Message: fmt.Sprintf("You have %d enrolled student(s) but no quizzes yet. Create one to start assessing.", totalStudents),
		})
	}

	return insights
}

func (app *application) teacherCourses(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "My Courses"

	// Populate teacher-specific stats shown on this view.
	teacherID := app.session.GetString(r.Context(), "authenticatedUserID")
	if teacherID != "" {
		teacher, err := app.models.Users.FindByID(r.Context(), teacherID)
		if err == nil {
			data.TeacherName = teacher.Name
		}

		totalCourses, err := app.models.Courses.CountByTeacher(r.Context(), teacherID)
		if err == nil {
			data.TotalCourses = totalCourses
		}

		totalStudents, err := app.models.Enrollments.CountByTeacher(r.Context(), teacherID)
		if err == nil {
			data.TotalStudents = totalStudents
		}

		activeQuizzes, err := app.models.Quizzes.CountActiveByTeacher(r.Context(), teacherID)
		if err == nil {
			data.ActiveQuizzes = activeQuizzes
		}

		avgScore, err := app.models.Submissions.AverageScoreByTeacher(r.Context(), teacherID)
		if err == nil {
			data.AveragePerformance = avgScore
		}
	}

	app.render(w, http.StatusOK, "teacher/courses.html", data)
}

func (app *application) createCourse(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	title := r.Form.Get("title")
	description := r.Form.Get("description")

	v := validator.New()
	v.CheckField(validator.NotBlank(title), "title", "This field cannot be blank")
	v.CheckField(validator.MaxChars(title, 255), "title", "This field must not be more than 255 characters long")

	if !v.Valid() {
		data := app.newTemplateData(r)
		data.Title = "Create Course"
		data.Form = map[string]string{
			"title":       title,
			"description": description,
		}
		data.FormErrors = v.GetFieldErrors()
		app.render(w, http.StatusUnprocessableEntity, "teacher/create-course.html", data)
		return
	}

	teacherID := app.session.GetString(r.Context(), "authenticatedUserID")

	course := &models.Course{
		Title:       strings.TrimSpace(title),
		Description: strings.TrimSpace(description),
		TeacherID:   teacherID,
	}

	err = app.models.Courses.Insert(r.Context(), course)
	if err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "Course created successfully!")
	http.Redirect(w, r, "/teacher/courses", http.StatusSeeOther)
}

func (app *application) teacherCourseDetail(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Course Detail"

	app.render(w, http.StatusOK, "teacher/course-detail.html", data)
}

func (app *application) teacherQuizzes(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "My Quizzes"

	app.render(w, http.StatusOK, "teacher/quizzes.html", data)
}

func (app *application) createQuiz(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Create Quiz"

	app.render(w, http.StatusOK, "teacher/create-quiz.html", data)
}

func (app *application) teacherSubmissions(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Submissions"

	app.render(w, http.StatusOK, "teacher/submissions.html", data)
}

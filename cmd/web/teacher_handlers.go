package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"sabify/internal/models"
	"sabify/internal/validator"
)

func (app *application) teacherDashboard(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Dashboard"
	data.CurrentPage = "dashboard"

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

	app.render(w, http.StatusOK, "teacher/dashboard.html", data)
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
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courses, err := app.models.Courses.FindByTeacher(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "My Courses"
	data.User = user
	data.CurrentPage = "courses"
	data.TeacherCourses = courses
	app.render(w, http.StatusOK, "teacher/courses.html", data)
}

func (app *application) createCourse(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		data := app.newTemplateData(r)
		data.Title = "Create Course"
		data.User = app.loadCurrentUser(w, r)
		if data.User == nil {
			return
		}
		app.render(w, http.StatusOK, "teacher/create-course.html", data)
		return
	}

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
		data.User = app.loadCurrentUser(w, r)
		if data.User == nil {
			return
		}
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
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courseID := chi.URLParam(r, "id")
	course, err := app.models.Courses.FindByIDWithTeacher(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
			return
		}
		app.serverError(w, err)
		return
	}
	if course.TeacherID != user.ID {
		app.clientError(w, http.StatusForbidden)
		return
	}

	quizzes, err := app.models.Quizzes.FindByCourse(r.Context(), courseID)
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
	app.render(w, http.StatusOK, "teacher/course-detail.html", data)
}

func (app *application) teacherQuizzes(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quizzes, err := app.models.Quizzes.FindAllWithCourse(r.Context())
	if err != nil {
		app.serverError(w, err)
		return
	}
	teacherQuizzes := make([]models.QuizWithCourse, 0, len(quizzes))
	for _, quiz := range quizzes {
		course, err := app.models.Courses.FindByID(r.Context(), quiz.CourseID)
		if err == nil && course.TeacherID == user.ID {
			teacherQuizzes = append(teacherQuizzes, quiz)
		}
	}

	data := app.newTemplateData(r)
	data.Title = "My Quizzes"
	data.User = user
	data.CurrentPage = "quizzes"
	data.Quizzes = teacherQuizzes
	app.render(w, http.StatusOK, "teacher/quizzes.html", data)
}

func (app *application) createQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courses, err := app.models.Courses.FindByTeacher(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			app.clientError(w, http.StatusBadRequest)
			return
		}

		title := strings.TrimSpace(r.Form.Get("title"))
		description := strings.TrimSpace(r.Form.Get("description"))
		courseID := r.Form.Get("course_id")
		v := validator.New()
		v.CheckField(validator.NotBlank(title), "title", "This field cannot be blank")
		v.CheckField(validator.MaxChars(title, 255), "title", "This field must not be more than 255 characters long")
		v.CheckField(validator.NotBlank(courseID), "course_id", "Choose a course")

		var selectedCourse *models.Course
		if courseID != "" {
			selectedCourse, err = app.models.Courses.FindByID(r.Context(), courseID)
			if err != nil || selectedCourse.TeacherID != user.ID {
				v.CheckField(false, "course_id", "Choose one of your courses")
			}
		}

		if !v.Valid() {
			data := app.newTemplateData(r)
			data.Title = "Create Quiz"
			data.User = user
			data.TeacherCourses = courses
			data.Form = map[string]string{"title": title, "description": description, "course_id": courseID}
			data.FormErrors = v.GetFieldErrors()
			app.render(w, http.StatusUnprocessableEntity, "teacher/create-quiz.html", data)
			return
		}

		quiz := &models.Quiz{CourseID: selectedCourse.ID, Title: title, Description: description}
		if err := app.models.Quizzes.Insert(r.Context(), quiz); err != nil {
			app.serverError(w, err)
			return
		}

		for index := 0; index < 50; index++ {
			questionText := strings.TrimSpace(r.Form.Get(fmt.Sprintf("question_text_%d", index)))
			if questionText == "" {
				continue
			}

			question := &models.Question{
				QuizID:        quiz.ID,
				QuestionText:  questionText,
				OptionA:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_a_%d", index))),
				OptionB:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_b_%d", index))),
				OptionC:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_c_%d", index))),
				OptionD:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_d_%d", index))),
				CorrectAnswer: strings.ToUpper(strings.TrimSpace(r.Form.Get(fmt.Sprintf("correct_answer_%d", index)))),
			}

			if question.OptionA == "" || question.OptionB == "" || question.OptionC == "" || question.OptionD == "" || question.CorrectAnswer < "A" || question.CorrectAnswer > "D" {
				app.session.Put(r.Context(), "flash", "Each multiple-choice question needs four options and a correct answer.")
				http.Redirect(w, r, "/teacher/quizzes/new", http.StatusSeeOther)
				return
			}

			if err := app.models.Questions.Insert(r.Context(), question); err != nil {
				app.serverError(w, err)
				return
			}
		}

		app.session.Put(r.Context(), "flash", "Quiz created successfully!")
		http.Redirect(w, r, "/teacher/quizzes", http.StatusSeeOther)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Create Quiz"
	data.User = user
	data.TeacherCourses = courses
	app.render(w, http.StatusOK, "teacher/create-quiz.html", data)
}

func (app *application) teacherOwnedQuiz(w http.ResponseWriter, r *http.Request, userID string) (*models.Quiz, error) {
	quizID := chi.URLParam(r, "id")
	quiz, err := app.models.Quizzes.FindByID(r.Context(), quizID)
	if err != nil {
		return nil, err
	}

	course, err := app.models.Courses.FindByID(r.Context(), quiz.CourseID)
	if err != nil {
		return nil, err
	}
	if course.TeacherID != userID {
		return nil, models.ErrForbidden
	}

	return quiz, nil
}

func (app *application) editQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	quiz, err := app.teacherOwnedQuiz(w, r, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else if errors.Is(err, models.ErrForbidden) {
			app.clientError(w, http.StatusForbidden)
		} else {
			app.serverError(w, err)
		}
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Edit Quiz"
	data.User = user
	data.CurrentPage = "quizzes"
	data.Quiz = quiz
	questions, err := app.models.Questions.FindByQuiz(r.Context(), quiz.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	data.Questions = questions

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			app.clientError(w, http.StatusBadRequest)
			return
		}

		quiz.Title = strings.TrimSpace(r.Form.Get("title"))
		quiz.Description = strings.TrimSpace(r.Form.Get("description"))
		questions = make([]models.Question, 0, 50)
		for index := 0; index < 50; index++ {
			questionText := strings.TrimSpace(r.Form.Get(fmt.Sprintf("question_text_%d", index)))
			if questionText == "" {
				continue
			}
			questions = append(questions, models.Question{
				QuestionText:  questionText,
				OptionA:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_a_%d", index))),
				OptionB:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_b_%d", index))),
				OptionC:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_c_%d", index))),
				OptionD:       strings.TrimSpace(r.Form.Get(fmt.Sprintf("option_d_%d", index))),
				CorrectAnswer: strings.ToUpper(strings.TrimSpace(r.Form.Get(fmt.Sprintf("correct_answer_%d", index)))),
			})
		}
		v := validator.New()
		v.CheckField(validator.NotBlank(quiz.Title), "title", "This field cannot be blank")
		v.CheckField(validator.MaxChars(quiz.Title, 255), "title", "This field must not be more than 255 characters long")
		if len(questions) == 0 {
			v.CheckField(false, "questions", "Add at least one question")
		}
		for _, question := range questions {
			if question.OptionA == "" || question.OptionB == "" || question.OptionC == "" || question.OptionD == "" || question.CorrectAnswer < "A" || question.CorrectAnswer > "D" {
				v.CheckField(false, "questions", "Each question needs four options and a correct answer")
				break
			}
		}
		if !v.Valid() {
			data.Form = map[string]string{"title": quiz.Title, "description": quiz.Description}
			data.Questions = questions
			data.FormErrors = v.GetFieldErrors()
			app.render(w, http.StatusUnprocessableEntity, "teacher/edit-quiz.html", data)
			return
		}

		if err := app.models.Quizzes.Update(r.Context(), quiz); err != nil {
			app.serverError(w, err)
			return
		}
		if err := app.models.Questions.ReplaceByQuiz(r.Context(), quiz.ID, questions); err != nil {
			app.serverError(w, err)
			return
		}
		app.session.Put(r.Context(), "flash", "Quiz updated successfully!")
		http.Redirect(w, r, "/teacher/quizzes", http.StatusSeeOther)
		return
	}

	data.Form = map[string]string{"title": quiz.Title, "description": quiz.Description}
	app.render(w, http.StatusOK, "teacher/edit-quiz.html", data)
}

func (app *application) deleteQuiz(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	if _, err := app.teacherOwnedQuiz(w, r, user.ID); err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else if errors.Is(err, models.ErrForbidden) {
			app.clientError(w, http.StatusForbidden)
		} else {
			app.serverError(w, err)
		}
		return
	}

	if err := app.models.Quizzes.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		app.serverError(w, err)
		return
	}
	app.session.Put(r.Context(), "flash", "Quiz deleted successfully!")
	http.Redirect(w, r, "/teacher/quizzes", http.StatusSeeOther)
}

func (app *application) teacherSubmissions(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	submissions, err := app.models.Submissions.RecentByTeacher(r.Context(), user.ID, 100)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Submissions"
	data.User = user
	data.CurrentPage = "results"
	data.RecentSubmissions = submissions
	app.render(w, http.StatusOK, "teacher/submissions.html", data)
}

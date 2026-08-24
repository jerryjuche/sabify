package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	sabifyMiddleware "sabify/internal/middleware"
)

func (app *application) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(middleware.StripSlashes)

	r.Use(app.session.LoadAndSave)
	r.Use(sabifyMiddleware.SetSecurityHeaders)
	r.Use(sabifyMiddleware.LogRequest)
	r.Use(sabifyMiddleware.RecoverPanic)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./ui/static"))))

	r.Get("/", app.home)
	r.Get("/health", app.healthCheck)

	r.Get("/register", app.showRegisterForm)
	r.Post("/register", app.register)
	r.Get("/login", app.showLoginForm)
	r.Post("/login", app.login)
	r.Post("/logout", app.logout)

	r.Group(func(r chi.Router) {
		r.Use(app.authenticate)
		r.Get("/dashboard", app.dashboard)
	})

	r.Group(func(r chi.Router) {
		r.Use(app.authenticate)
		r.Use(app.requireRole("teacher"))
		// r.Get("/teacher/dashboard", app.teacherDashboard)
		// r.Get("/teacher/courses", app.teacherDashboard)
		r.Get("/teacher/courses", app.teacherCourses)
		r.Post("/teacher/courses", app.createCourse)
		r.Get("/teacher/courses/{id}", app.teacherCourseDetail)
		r.Get("/teacher/quizzes", app.teacherQuizzes)
		r.Post("/teacher/quizzes", app.createQuiz)
		r.Get("/teacher/submissions", app.teacherSubmissions)
	})

	r.Group(func(r chi.Router) {
		r.Use(app.authenticate)
		r.Use(app.requireRole("student"))
		r.Get("/student/courses", app.studentCourses)
		r.Get("/student/courses/{id}", app.studentCourseDetail)
		r.Get("/student/quizzes", app.studentQuizzes)
		r.Post("/student/quizzes/{id}/submit", app.submitQuiz)
		r.Get("/student/results", app.studentResults)
		r.Get("/student/study-groups", app.studentStudyGroups)
	})

	return r
}

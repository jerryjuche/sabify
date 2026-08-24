package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"sabify/internal/models"
	"sabify/internal/validator"
)

func (app *application) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		app.notFound(w)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Home"

	app.render(w, http.StatusOK, "pages/home/index.html", data)
}

func (app *application) healthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	err := app.models.Users.DB.Ping(ctx)
	if err != nil {
		app.serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","database":"connected"}`))
}

func (app *application) showRegisterForm(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Register"

	app.render(w, http.StatusOK, "auth/register.html", data)
}

func (app *application) renderRegisterError(w http.ResponseWriter, r *http.Request, status int, name, email, role string, fieldErrors map[string]string) {
	data := app.newTemplateData(r)
	data.Title = "Register"
	data.Form = map[string]string{
		"name":  name,
		"email": email,
		"role":  role,
	}
	data.FormErrors = fieldErrors
	app.render(w, status, "auth/register.html", data)
}

func (app *application) register(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	name := r.Form.Get("name")
	email := r.Form.Get("email")
	password := r.Form.Get("password")
	confirmPassword := r.Form.Get("confirmPassword")
	policy := r.Form.Get("policy")
	role := r.Form.Get("role")

	v := validator.New()
	v.CheckField(validator.NotBlank(name), "name", "This field cannot be blank")
	v.CheckField(validator.NotBlank(email), "email", "This field cannot be blank")
	v.CheckField(validator.MinChars(password, 8), "password", "This field must be at least 8 characters long")
	v.CheckField(password == confirmPassword, "confirmPassword", "Passwords do not match")
	v.CheckField(validator.PermittedValue(role, "student", "teacher"), "role", "This field must be either student or teacher")
	v.CheckField(validator.NotBlank(policy), "policy", "You must agree to the terms before creating an account")

	if !v.Valid() {
		app.renderRegisterError(w, r, http.StatusUnprocessableEntity, name, email, role, v.GetFieldErrors())
		return
	}

	exists, err := app.models.Users.Exists(r.Context(), email)
	if err != nil {
		app.serverError(w, err)
		return
	}

	if exists {
		app.renderRegisterError(w, r, http.StatusUnprocessableEntity, name, email, role, map[string]string{
			"email": "An account with this email already exists",
		})
		return
	}

	user := &models.User{
		Name:  strings.TrimSpace(name),
		Email: strings.ToLower(strings.TrimSpace(email)),
		Role:  role,
	}

	err = app.models.Users.Insert(r.Context(), user, password)
	if err != nil {
		if errors.Is(err, models.ErrDuplicateEmail) {
			app.renderRegisterError(w, r, http.StatusUnprocessableEntity, name, email, role, map[string]string{
				"email": "An account with this email already exists",
			})
			return
		}
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "Your registration was successful. Please log in.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *application) showLoginForm(w http.ResponseWriter, r *http.Request) {
	data := app.newTemplateData(r)
	data.Title = "Login"

	app.render(w, http.StatusOK, "auth/login.html", data)
}

func (app *application) login(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	email := r.Form.Get("email")
	password := r.Form.Get("password")

	v := validator.New()
	v.CheckField(validator.NotBlank(email), "email", "This field cannot be blank")
	v.CheckField(validator.NotBlank(password), "password", "This field cannot be blank")

	if !v.Valid() {
		data := app.newTemplateData(r)
		data.Title = "Login"
		data.Form = map[string]string{"email": email}
		data.FormErrors = v.GetFieldErrors()
		app.render(w, http.StatusUnprocessableEntity, "auth/login.html", data)
		return
	}

	user, err := app.models.Users.Authenticate(r.Context(), email, password)
	if err != nil {
		if errors.Is(err, models.ErrInvalidCredentials) {
			data := app.newTemplateData(r)
			data.Title = "Login"
			data.Form = map[string]string{"email": email}
			data.FormErrors = map[string]string{"email": "Invalid email or password"}
			app.render(w, http.StatusUnauthorized, "auth/login.html", data)
		} else {
			app.serverError(w, err)
		}
		return
	}

	app.session.Put(r.Context(), "authenticatedUserID", user.ID)
	app.session.Put(r.Context(), "userRole", user.Role)
	app.session.Put(r.Context(), "flash", "You have been logged in successfully!")

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (app *application) logout(w http.ResponseWriter, r *http.Request) {
	app.session.Remove(r.Context(), "authenticatedUserID")
	app.session.Remove(r.Context(), "userRole")
	app.session.Put(r.Context(), "flash", "You have been logged out.")

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := app.session.GetString(r.Context(), "authenticatedUserID")

		if userID == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

/*
 * loadCurrentUser resolves the session's user ID into a
 * full user record for authenticated pages. If the session
 * is stale or the user no longer exists, the session is
 * cleared and the visitor is sent back to the login page;
 * the returned value is nil in that case and handlers must
 * simply return after calling it.
 */

func (app *application) loadCurrentUser(w http.ResponseWriter, r *http.Request) *models.User {
	userID := app.session.GetString(r.Context(), "authenticatedUserID")
	if userID == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}

	user, err := app.models.Users.FindByID(r.Context(), userID)
	if err != nil {
		app.session.Remove(r.Context(), "authenticatedUserID")
		app.session.Remove(r.Context(), "userRole")
		app.session.Put(r.Context(), "flash", "Please log in again.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}

	return user
}

func (app *application) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userRole := app.session.GetString(r.Context(), "userRole")

			if userRole != role {
				app.clientError(w, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (app *application) dashboard(w http.ResponseWriter, r *http.Request) {
	userRole := app.session.GetString(r.Context(), "userRole")

	switch userRole {
	case "teacher":
		http.Redirect(w, r, "/teacher/courses", http.StatusSeeOther)
	case "student":
		http.Redirect(w, r, "/student/courses", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

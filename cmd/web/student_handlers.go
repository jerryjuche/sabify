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
	"sabify/internal/validator"
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

	// Include paid courses the student has ACTIVE access to.
	accesses, err := app.models.CourseAccess.FindByStudent(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	for _, a := range accesses {
		if a.Status == "ACTIVE" {
			enrolledSet[a.CourseID] = true
		}
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

	accessStatus := ""
	if !enrolled {
		// Free courses: non-enrolled students are bounced back.
		// Paid courses: allow the paywall to render; access is granted by
		// course_access instead.
		if course.PriceKobo == nil || *course.PriceKobo == 0 {
			app.session.Put(r.Context(), "flash", "You are not enrolled in this course.")
			http.Redirect(w, r, "/student/courses", http.StatusSeeOther)
			return
		}

		access, aerr := app.models.CourseAccess.Find(r.Context(), user.ID, courseID)
		if aerr == nil && access != nil {
			accessStatus = access.Status
		}
	}

	// Gate course content behind enrollment / active access.
	canView := enrolled || accessStatus == "ACTIVE"

	var quizzes []models.Quiz
	var materials []models.Material
	var submissions []models.SubmissionWithQuiz

	if canView {
		quizzes, err = app.models.Quizzes.FindByCourse(r.Context(), courseID)
		if err != nil {
			app.serverError(w, err)
			return
		}

		materials, err = app.models.Materials.FindByCourse(r.Context(), courseID)
		if err != nil {
			app.serverError(w, err)
			return
		}

		submissions, err = app.models.Submissions.FindByStudentWithQuiz(r.Context(), user.ID)
		if err != nil {
			app.serverError(w, err)
			return
		}
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
	data.CourseAccessStatus = accessStatus

	if course.PriceKobo != nil {
		data.CoursePriceNaira = *course.PriceKobo / 100
	}

	if !canView {
		// Show the paywall-only view (no materials/quizzes). Preserve a
		// genuine PENDING status; otherwise mark it LOCKED for the paywall.
		if data.CourseAccessStatus != "PENDING" {
			data.CourseAccessStatus = "LOCKED"
		}
		app.render(w, http.StatusOK, "student/course.html", data)
		return
	}

	app.render(w, http.StatusOK, "student/course.html", data)
}

func (app *application) enrollInCourse(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	courseID := chi.URLParam(r, "id")

	course, err := app.models.Courses.FindByID(r.Context(), courseID)
	if err != nil {
		app.notFound(w)
		return
	}

	// Already enrolled (free) or already has active access?
	if enrolled, err := app.models.Enrollments.IsEnrolled(r.Context(), courseID, user.ID); err == nil && enrolled {
		app.session.Put(r.Context(), "flash", "You are already enrolled in this course.")
		http.Redirect(w, r, "/student/courses/"+courseID, http.StatusSeeOther)
		return
	}

	existing, err := app.models.CourseAccess.Find(r.Context(), user.ID, courseID)
	if err == nil && existing != nil {
		if existing.Status == "ACTIVE" {
			app.session.Put(r.Context(), "flash", "You already have access to this course.")
		} else {
			app.session.Put(r.Context(), "flash", "You already have a pending payment for this course.")
			payment, perr := app.models.Payments.FindByID(r.Context(), *existing.PaymentID)
			if perr == nil && payment != nil {
				http.Redirect(w, r, "/student/pay/"+payment.ID, http.StatusSeeOther)
				return
			}
		}
		http.Redirect(w, r, "/student/courses/"+courseID, http.StatusSeeOther)
		return
	}

	// Free course: instant enrollment, unchanged behaviour.
	if course.PriceKobo == nil || *course.PriceKobo == 0 {
		if err := app.models.Enrollments.Insert(r.Context(), courseID, user.ID); err != nil {
			app.serverError(w, err)
			return
		}
		app.session.Put(r.Context(), "flash", "You have been enrolled in the course!")
		http.Redirect(w, r, "/student/courses/"+courseID, http.StatusSeeOther)
		return
	}

	// Paid course: create a PENDING payment + course access, then go pay.
	reference := fmt.Sprintf("SABIFY-%s-%s", courseID[:8], user.ID[:8])
	narration := fmt.Sprintf("Sabify course payment for %s", course.Title)

	payment, err := app.models.Payments.CreatePending(r.Context(), user.ID, courseID, *course.PriceKobo, reference, narration)
	if err != nil {
		app.serverError(w, err)
		return
	}

	if _, err := app.models.CourseAccess.Create(r.Context(), user.ID, courseID, payment.ID); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "Complete your payment to unlock this course.")
	http.Redirect(w, r, "/student/pay/"+payment.ID, http.StatusSeeOther)
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
		"score":         correct,
		"total":         total,
		"submission_id": submission.ID,
		"submitted_at":  submission.SubmittedAt,
		"quiz_title":    quiz.Title,
		"course_id":     quiz.CourseID,
		"attempt":       attempt,
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

	// The student's own courses feed the "create group" course selector.
	enrolled, err := app.models.Courses.FindByStudent(r.Context(), user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Title = "Study Groups"
	data.User = user
	data.CurrentPage = "groups"
	data.Groups = groups
	for _, c := range enrolled {
		data.Courses = append(data.Courses, models.CourseWithTeacher{Course: c})
	}

	app.render(w, http.StatusOK, "student/study-groups.html", data)
}

/*
 * canStudyCourse reports whether a student may participate in a course's
 * study group: enrolled directly (free course) or holding ACTIVE access to a
 * paid course. PENDING access does not count — pay before collaborating.
 */

func (app *application) canStudyCourse(r *http.Request, courseID, studentID string) (bool, error) {
	enrolled, err := app.models.Enrollments.IsEnrolled(r.Context(), courseID, studentID)
	if err != nil {
		return false, err
	}
	if enrolled {
		return true, nil
	}

	access, err := app.models.CourseAccess.Find(r.Context(), studentID, courseID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			return false, nil
		}
		return false, err
	}
	return access != nil && access.Status == "ACTIVE", nil
}

// studentCreateGroup wires the study-groups create form. A group may be
// general (no course) or bound to one of the student's own courses.
func (app *application) studentCreateGroup(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		app.clientError(w, http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.Form.Get("name"))
	courseID := strings.TrimSpace(r.Form.Get("course_id"))

	v := validator.New()
	v.CheckField(validator.NotBlank(name), "name", "Give your group a name")
	v.CheckField(validator.MaxChars(name, 255), "name", "Group names must be 255 characters or fewer")

	if courseID != "" {
		canStudy, err := app.canStudyCourse(r, courseID, user.ID)
		if err != nil {
			app.serverError(w, err)
			return
		}
		if !canStudy {
			v.CheckField(false, "course_id", "Join or unlock this course before creating its study group")
		}
	}

	if !v.Valid() {
		msg := "Check the group details and try again."
		for _, field := range []string{"name", "course_id"} {
			if m, ok := v.GetFieldErrors()[field]; ok {
				msg = m
				break
			}
		}
		app.session.Put(r.Context(), "flash", msg)
		http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
		return
	}

	group := &models.StudyGroup{Name: name, CourseID: courseID}
	if err := app.models.StudyGroups.Insert(r.Context(), group); err != nil {
		app.serverError(w, err)
		return
	}

	// The creator joins automatically so the group starts with one member.
	if err := app.models.StudyGroups.AddMember(r.Context(), group.ID, user.ID); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "Study group created!")
	http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
}

// studentJoinGroup adds the student to a group. Course-bound groups require
// enrollment/active access; general groups are open to any student.
func (app *application) studentJoinGroup(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	groupID := chi.URLParam(r, "id")
	group, err := app.models.StudyGroups.FindByID(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	if group.CourseID != "" {
		canStudy, err := app.canStudyCourse(r, group.CourseID, user.ID)
		if err != nil {
			app.serverError(w, err)
			return
		}
		if !canStudy {
			app.session.Put(r.Context(), "flash", "Enroll in this group's course before joining.")
			http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
			return
		}
	}

	member, err := app.models.StudyGroups.IsMember(r.Context(), groupID, user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	if member {
		app.session.Put(r.Context(), "flash", "You are already a member of this group.")
		http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
		return
	}

	if err := app.models.StudyGroups.AddMember(r.Context(), groupID, user.ID); err != nil {
		app.serverError(w, err)
		return
	}

	app.session.Put(r.Context(), "flash", "You joined the study group.")
	http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
}

// studentLeaveGroup removes the student from a group.
func (app *application) studentLeaveGroup(w http.ResponseWriter, r *http.Request) {
	user := app.loadCurrentUser(w, r)
	if user == nil {
		return
	}

	groupID := chi.URLParam(r, "id")
	if _, err := app.models.StudyGroups.FindByID(r.Context(), groupID); err != nil {
		if errors.Is(err, models.ErrNoRecord) {
			app.notFound(w)
		} else {
			app.serverError(w, err)
		}
		return
	}

	member, err := app.models.StudyGroups.IsMember(r.Context(), groupID, user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	if !member {
		app.session.Put(r.Context(), "flash", "You are not a member of this group.")
		http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
		return
	}

	if err := app.models.StudyGroups.RemoveMember(r.Context(), groupID, user.ID); err != nil {
		app.serverError(w, err)
		return
	}

	// A group left empty disappears so the list stays meaningful.
	count, err := app.models.StudyGroups.CountMembers(r.Context(), groupID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	if count == 0 {
		if err := app.models.StudyGroups.Delete(r.Context(), groupID); err != nil {
			app.serverError(w, err)
			return
		}
	}

	app.session.Put(r.Context(), "flash", "You left the study group.")
	http.Redirect(w, r, "/student/study-groups", http.StatusSeeOther)
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

	// Course materials are paywalled content: only students who are enrolled
	// (free courses) or hold ACTIVE access (paid courses) may view the file.
	// The raw file is never served from /static/uploads, so this handler is
	// the only path to it.
	canView, err := app.canStudyCourse(r, courseID, user.ID)
	if err != nil {
		app.serverError(w, err)
		return
	}
	if !canView {
		app.session.Put(r.Context(), "flash", "Enroll in this course to view its materials.")
		http.Redirect(w, r, "/student/courses/"+courseID, http.StatusSeeOther)
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

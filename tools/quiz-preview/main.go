/*
	tools/quiz-preview
	==================

	Generates a clickable offline preview of every student page
	using the REAL templates from ui/html plus realistic fake
	data — no database and no running server required.

	Usage (from anywhere inside the repo):

	    go run ./tools/quiz-preview

	Output lands in <os.TempDir>/sabify-preview and courses.html
	is opened in your browser automatically (disable with
	-open=false).

	This is a development aid only: it ships no routes, touches
	no models, and is never exercised by the application itself.
*/

package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type fakeCourse struct {
	ID          string
	Title       string
	Description string
	TeacherName string
	QuizCount   int
	CreatedAt   time.Time
}

type fakeQuizWithCourse struct {
	ID            string
	CourseID      string
	Title         string
	Description   string
	CreatedAt     time.Time
	CourseTitle   string
	QuestionCount int
}

type fakeQuiz struct {
	ID          string
	CourseID    string
	Title       string
	Description string
	CreatedAt   time.Time
}

type fakeQuestion struct {
	ID           string
	QuestionText string
	OptionA      string
	OptionB      string
	OptionC      string
	OptionD      string
}

type fakeSubmission struct {
	ID             string
	QuizID         string
	StudentID      string
	Score          int
	TotalQuestions int
	SubmittedAt    time.Time
	QuizTitle      string
	Percent        int
}

type fakeGroup struct {
	ID          string
	Name        string
	CourseID    string
	CreatedAt   time.Time
	CourseTitle string
	MemberCount int
	IsMember    bool
}

type fakeStats struct {
	CoursesAvailable int
	QuizzesTaken     int
	AverageScore     int
	BestScore        int
}

var (
	courses = []fakeCourse{
		{ID: "c1", Title: "Biology 101",
			Description: "Cells, genetics and the chemistry of life — a first dive into living systems.",
			TeacherName: "Dr. Sarah Mensah", QuizCount: 2, CreatedAt: time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)},
		{ID: "c2", Title: "Introduction to Calculus",
			Description: "Limits, derivatives and the art of change. Build intuition before formulas.",
			TeacherName: "Mr. David Chen", QuizCount: 2, CreatedAt: time.Date(2026, 8, 5, 14, 30, 0, 0, time.UTC)},
		{ID: "c3", Title: "World History: The Renaissance",
			Description: "Art, science and revolution in Europe between 1400 and 1700.",
			TeacherName: "Prof. Amina Diallo", QuizCount: 0, CreatedAt: time.Date(2026, 7, 28, 11, 15, 0, 0, time.UTC)},
	}

	quizzes = []fakeQuizWithCourse{
		{ID: "q-bio-cells", CourseID: "c1", Title: "Cell Biology Fundamentals",
			Description: "Organelles, transport and membrane structure.",
			CreatedAt:   time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC), CourseTitle: "Biology 101", QuestionCount: 4},
		{ID: "q-bio-genetics", CourseID: "c1", Title: "Genetics Basics",
			Description: "Dominant vs recessive traits and Punnett squares.",
			CreatedAt:   time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC), CourseTitle: "Biology 101", QuestionCount: 3},
		{ID: "q-calc-limits", CourseID: "c2", Title: "Limits & Continuity",
			Description: "A quick check of your limit intuition.",
			CreatedAt:   time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC), CourseTitle: "Introduction to Calculus", QuestionCount: 2},
		{ID: "q-calc-deriv", CourseID: "c2", Title: "Derivatives in Practice",
			Description: "Power rule, product rule and real-world rates of change.",
			CreatedAt:   time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC), CourseTitle: "Introduction to Calculus", QuestionCount: 5},
	}

	submissions = []fakeSubmission{
		{ID: "s1", QuizID: "q-calc-limits", Score: 2, TotalQuestions: 2,
			SubmittedAt: time.Date(2026, 8, 21, 16, 42, 0, 0, time.UTC), QuizTitle: "Limits & Continuity", Percent: 100},
		{ID: "s2", QuizID: "q-bio-genetics", Score: 2, TotalQuestions: 3,
			SubmittedAt: time.Date(2026, 8, 19, 9, 15, 0, 0, time.UTC), QuizTitle: "Genetics Basics", Percent: 67},
		{ID: "s3", QuizID: "q-bio-genetics", Score: 1, TotalQuestions: 3,
			SubmittedAt: time.Date(2026, 8, 16, 20, 3, 0, 0, time.UTC), QuizTitle: "Genetics Basics", Percent: 33},
	}

	groups = []fakeGroup{
		{ID: "g1", Name: "Bio Buddies", CourseID: "c1", CreatedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
			CourseTitle: "Biology 101", MemberCount: 5, IsMember: true},
		{ID: "g2", Name: "Calc Crew", CourseID: "c2", CreatedAt: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
			CourseTitle: "Introduction to Calculus", MemberCount: 3, IsMember: false},
		{ID: "g3", Name: "History Circle", CourseID: "", CreatedAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			CourseTitle: "", MemberCount: 4, IsMember: false},
	}
)

func attempted() map[string]int {
	m := map[string]int{}
	for _, s := range submissions {
		if s.Percent > m[s.QuizID] {
			m[s.QuizID] = s.Percent
		}
	}
	return m
}

func statsFor(coursesAvailable int) fakeStats {
	st := fakeStats{CoursesAvailable: coursesAvailable, QuizzesTaken: len(submissions)}
	total := 0
	for _, s := range submissions {
		total += s.Percent
		if s.Percent > st.BestScore {
			st.BestScore = s.Percent
		}
	}
	if len(submissions) > 0 {
		st.AverageScore = total / len(submissions)
	}
	return st
}

func base(title, page string) map[string]any {
	return map[string]any{
		"CurrentYear": time.Now().Year(),
		"Title":       title,
		"Flash":       "",
		"User":        map[string]any{"Name": "Amara Okafor", "Role": "student"},
		"CurrentPage": page,
	}
}

/*
 * findRepoRoot walks upwards from the current working
 * directory until it finds ui/html/layouts/base.html.
 */

func findRepoRoot() (string, error) {

	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}

	for {
		probe := filepath.Join(dir, "ui", "html", "layouts", "base.html")
		if _, statErr := os.Stat(probe); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"could not locate ui/html/layouts/base.html — run this tool from within the repository")
		}

		dir = parent
	}
}

/*
 * staticFileURL turns <root>/ui/static into a file:// URL
 * that browsers resolve on any operating system.
 */

func staticFileURL(root string) string {

	p := filepath.ToSlash(filepath.Join(root, "ui", "static"))
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	u := url.URL{Scheme: "file", Path: p}

	return u.String() + "/"
}

func openInBrowser(target string) {

	switch runtime.GOOS {

	case "windows":
		_ = exec.Command("cmd", "/c", "start", "", target).Start()

	case "darwin":
		_ = exec.Command("open", target).Start()

	default:
		_ = exec.Command("xdg-open", target).Start()
	}
}

func main() {

	openBrowser := flag.Bool("open", true, "open courses.html in your browser when done")
	flag.Parse()

	root, err := findRepoRoot()
	must(err)

	htmlRoot := filepath.Join(root, "ui", "html")
	outDir := filepath.Join(os.TempDir(), "sabify-preview")
	staticBase := staticFileURL(root)

	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"initials": func(name string) string {
			parts := strings.Fields(strings.TrimSpace(name))
			if len(parts) == 0 {
				return "?"
			}
			first := strings.ToUpper(parts[0][:1])
			if len(parts) > 1 {
				return first + strings.ToUpper(parts[len(parts)-1][:1])
			}
			return first
		},
		"shortDate": func(t time.Time) string { return t.Format("Jan 2") },
	}

	ts, err := template.New("base").Funcs(funcs).ParseFiles(filepath.Join(htmlRoot, "layouts", "base.html"))
	must(err)
	ts, err = ts.ParseGlob(filepath.Join(htmlRoot, "components", "*.html"))
	must(err)
	ts, err = ts.ParseGlob(filepath.Join(htmlRoot, "auth", "*.html"))
	must(err)

	upcoming := []fakeQuizWithCourse{quizzes[0], quizzes[3]}
	recent := submissions[:2]

	bioQuizzes := []fakeQuiz{
		{ID: "q-bio-cells", CourseID: "c1", Title: "Cell Biology Fundamentals",
			Description: "Organelles, transport and membrane structure.", CreatedAt: quizzes[0].CreatedAt},
		{ID: "q-bio-genetics", CourseID: "c1", Title: "Genetics Basics",
			Description: "Dominant vs recessive traits and Punnett squares.", CreatedAt: quizzes[1].CreatedAt},
	}

	pages := []struct {
		src    string
		out    string
		render func() map[string]any
	}{
		{
			src: filepath.Join(htmlRoot, "student", "courses.html"), out: "courses.html",
			render: func() map[string]any {
				d := base("My Courses", "courses")
				d["Courses"] = courses
				d["Submissions"] = recent
				d["UpcomingQuizzes"] = upcoming
				d["Stats"] = statsFor(len(courses))
				return d
			},
		},
		{
			src: filepath.Join(htmlRoot, "student", "course.html"), out: "course.html",
			render: func() map[string]any {
				d := base("Biology 101", "courses")
				d["Course"] = &courses[0]
				d["CourseQuizzes"] = bioQuizzes
				d["Attempted"] = attempted()
				return d
			},
		},
		{
			src: filepath.Join(htmlRoot, "student", "quizzes.html"), out: "quizzes.html",
			render: func() map[string]any {
				d := base("Available Quizzes", "quizzes")
				d["Quizzes"] = quizzes
				d["Attempted"] = attempted()
				return d
			},
		},
		{
			src: filepath.Join(htmlRoot, "student", "quiz.html"), out: "quiz.html",
			render: func() map[string]any {
				d := base("Take Quiz", "quizzes")
				d["Quiz"] = quizzes[0]
				d["Questions"] = []fakeQuestion{
					{ID: "q1", QuestionText: "Which organelle is known as the powerhouse of the cell?",
						OptionA: "Ribosome", OptionB: "Mitochondrion", OptionC: "Golgi apparatus", OptionD: "Lysosome"},
					{ID: "q2", QuestionText: "Which process moves water across a semi-permeable membrane from low to high solute concentration?",
						OptionA: "Active transport", OptionB: "Diffusion", OptionC: "Osmosis", OptionD: "Endocytosis"},
					{ID: "q3", QuestionText: "Where does protein synthesis take place?",
						OptionA: "Nucleolus", OptionB: "Ribosome", OptionC: "Vacuole", OptionD: "Cell wall"},
					{ID: "q4", QuestionText: "The fluid mosaic model describes the structure of which cellular component?",
						OptionA: "Cytoplasm", OptionB: "Nuclear envelope", OptionC: "Plasma membrane", OptionD: "Cytoskeleton"},
				}
				d["CorrectAnswers"] = map[string]string{"q1": "B", "q2": "C", "q3": "B", "q4": "C"}
				return d
			},
		},
		{
			src: filepath.Join(htmlRoot, "student", "results.html"), out: "results.html",
			render: func() map[string]any {
				d := base("My Results", "results")
				d["Submissions"] = submissions
				d["Stats"] = statsFor(0)
				return d
			},
		},
		{
			src: filepath.Join(htmlRoot, "student", "study-groups.html"), out: "study-groups.html",
			render: func() map[string]any {
				d := base("Study Groups", "groups")
				d["Groups"] = groups
				return d
			},
		},
	}

	var linkRules = []struct {
		re   *regexp.Regexp
		repl string
	}{
		{regexp.MustCompile(`href="/student/quizzes/[^"]*"`), `href="quiz.html"`},
		{regexp.MustCompile(`href="/student/courses/[^"]*"`), `href="course.html"`},
		{regexp.MustCompile(`href="/student/results[^"]*"`), `href="results.html"`},
		{regexp.MustCompile(`href="/student/study-groups[^"]*"`), `href="study-groups.html"`},
		{regexp.MustCompile(`href="/student/quizzes[^"]*"`), `href="quizzes.html"`},
		{regexp.MustCompile(`href="/student/courses[^"]*"`), `href="courses.html"`},
		{regexp.MustCompile(`href="/logout[^"]*"`), `href="#"`},
	}

	must(os.MkdirAll(outDir, 0755))

	var entry string

	for _, p := range pages {

		pageTs, err := ts.Clone()
		must(err)
		pageTs, err = pageTs.ParseFiles(p.src)
		must(err)

		var buf bytes.Buffer
		must(pageTs.ExecuteTemplate(&buf, "base", p.render()))

		html := buf.String()

		for _, rule := range linkRules {
			html = rule.re.ReplaceAllString(html, rule.repl)
		}

		html = strings.ReplaceAll(html, `href="/"`, `href="courses.html"`)
		html = strings.ReplaceAll(html, "/static/", staticBase)

		target := filepath.Join(outDir, p.out)
		must(os.WriteFile(target, []byte(html), 0644))

		fmt.Println("wrote", target)

		if p.out == "courses.html" {
			entry = target
		}
	}

	fmt.Println("\npreview ready:", outDir)

	if *openBrowser && entry != "" {
		openInBrowser(entry)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

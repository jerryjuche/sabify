package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Enrollment struct {
	CourseID   string
	StudentID  string
	EnrolledAt time.Time
}

type EnrollmentModel struct {
	DB *pgxpool.Pool
}

func (m *EnrollmentModel) Insert(ctx context.Context, courseID, studentID string) error {
	query := `
		INSERT INTO course_enrollments (course_id, student_id)
		VALUES ($1, $2)
		ON CONFLICT (course_id, student_id) DO NOTHING
	`

	_, err := m.DB.Exec(ctx, query, courseID, studentID)
	return err
}

func (m *EnrollmentModel) FindByCourse(ctx context.Context, courseID string) ([]Enrollment, error) {
	query := `
		SELECT course_id, student_id, enrolled_at
		FROM course_enrollments
		WHERE course_id = $1
		ORDER BY enrolled_at ASC
	`

	rows, err := m.DB.Query(ctx, query, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var enrollments []Enrollment

	for rows.Next() {
		var e Enrollment
		if err := rows.Scan(&e.CourseID, &e.StudentID, &e.EnrolledAt); err != nil {
			return nil, err
		}
		enrollments = append(enrollments, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return enrollments, nil
}

func (m *EnrollmentModel) CountByTeacher(ctx context.Context, teacherID string) (int, error) {
	query := `
		SELECT COUNT(DISTINCT ce.student_id)
		FROM course_enrollments ce
		INNER JOIN courses c ON ce.course_id = c.id
		WHERE c.teacher_id = $1
	`

	var count int
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&count)
	return count, err
}

type AttentionStudent struct {
	StudentID   string
	StudentName string
	AvgScore    float64
	QuizCount   int
}

func (m *EnrollmentModel) FindStudentsNeedingAttention(ctx context.Context, teacherID string) ([]AttentionStudent, error) {
	query := `
		SELECT
			u.id AS student_id,
			u.name AS student_name,
			ROUND(AVG(s.score::numeric / NULLIF(s.total_questions, 0) * 100), 1) AS avg_score,
			COUNT(DISTINCT s.quiz_id) AS quiz_count
		FROM course_enrollments ce
		INNER JOIN courses c ON ce.course_id = c.id
		INNER JOIN users u ON ce.student_id = u.id
		LEFT JOIN submissions s ON s.student_id = u.id
			AND s.quiz_id IN (
				SELECT q2.id FROM quizzes q2
				WHERE q2.course_id = c.id
			)
		WHERE c.teacher_id = $1
			AND s.id IS NOT NULL
		GROUP BY u.id, u.name
		HAVING AVG(s.score::numeric / NULLIF(s.total_questions, 0) * 100) < 50
		ORDER BY avg_score ASC
	`

	rows, err := m.DB.Query(ctx, query, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var students []AttentionStudent

	for rows.Next() {
		var s AttentionStudent
		if err := rows.Scan(&s.StudentID, &s.StudentName, &s.AvgScore, &s.QuizCount); err != nil {
			return nil, err
		}
		students = append(students, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return students, nil
}

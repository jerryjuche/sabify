package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Retake struct {
	ID        string
	QuizID    string
	StudentID string
	GrantedBy string
	CreatedAt time.Time
}

type RetakeModel struct {
	DB *pgxpool.Pool
}

func (m *RetakeModel) IsAllowed(ctx context.Context, quizID, studentID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM quiz_retakes
			WHERE quiz_id = $1 AND student_id = $2
		)
	`

	var allowed bool
	err := m.DB.QueryRow(ctx, query, quizID, studentID).Scan(&allowed)
	return allowed, err
}

func (m *RetakeModel) Grant(ctx context.Context, quizID, studentID, teacherID string) error {
	query := `
		INSERT INTO quiz_retakes (quiz_id, student_id, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (quiz_id, student_id) DO NOTHING
	`
	_, err := m.DB.Exec(ctx, query, quizID, studentID, teacherID)
	return err
}

func (m *RetakeModel) Revoke(ctx context.Context, quizID, studentID string) error {
	query := `DELETE FROM quiz_retakes WHERE quiz_id = $1 AND student_id = $2`
	_, err := m.DB.Exec(ctx, query, quizID, studentID)
	return err
}

func (m *RetakeModel) FindPendingByTeacher(ctx context.Context, teacherID string) ([]Retake, error) {
	query := `
		SELECT r.id, r.quiz_id, r.student_id, r.granted_by, r.created_at
		FROM quiz_retakes r
		INNER JOIN quizzes q ON q.id = r.quiz_id
		INNER JOIN courses c ON c.id = q.course_id
		WHERE c.teacher_id = $1
		ORDER BY r.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var retakes []Retake
	for rows.Next() {
		var r Retake
		if err := rows.Scan(
			&r.ID, &r.QuizID, &r.StudentID,
			&r.GrantedBy, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		retakes = append(retakes, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return retakes, nil
}

func (m *RetakeModel) RevokeIfAllowed(ctx context.Context, quizID, studentID string) (bool, error) {
	tag, err := m.DB.Exec(ctx,
		`DELETE FROM quiz_retakes WHERE quiz_id = $1 AND student_id = $2`,
		quizID, studentID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

/*
 * FindQuizzesForStudent splits all course quizzes into two
 * buckets: available (not yet taken, or retake granted) and
 * taken (previously submitted, no retake pending).
 */

type QuizWithAttempt struct {
	QuizWithCourse
	BestScore     int
	AttemptCount  int
	RetakeAllowed bool
}

func (m *RetakeModel) FindQuizzesForStudent(ctx context.Context, studentID string) (available []QuizWithAttempt, taken []QuizWithAttempt, err error) {
	query := `
		SELECT
			q.id, q.course_id, q.title, q.description, q.created_at,
			COALESCE(c.title, '') AS course_title,
			COUNT(DISTINCT qn.id) AS question_count,
			COALESCE(MAX(
				CASE WHEN s.total_questions > 0
				THEN s.score * 100 / s.total_questions
				ELSE 0 END
			), -1) AS best_score,
			COUNT(DISTINCT s.id) AS attempt_count,
			EXISTS(
				SELECT 1 FROM quiz_retakes r
				WHERE r.quiz_id = q.id AND r.student_id = $1
			) AS retake_allowed
		FROM quizzes q
		INNER JOIN courses c ON c.id = q.course_id
		LEFT JOIN questions qn ON qn.quiz_id = q.id
		LEFT JOIN submissions s ON s.quiz_id = q.id AND s.student_id = $1
		GROUP BY q.id, c.title
		ORDER BY q.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var qa QuizWithAttempt
		var bestScore int

		if err := rows.Scan(
			&qa.ID, &qa.CourseID, &qa.Title,
			&qa.Description, &qa.CreatedAt,
			&qa.CourseTitle, &qa.QuestionCount,
			&bestScore, &qa.AttemptCount,
			&qa.RetakeAllowed,
		); err != nil {
			return nil, nil, err
		}

		qa.BestScore = bestScore

		if qa.AttemptCount == 0 || qa.RetakeAllowed {
			available = append(available, qa)
		} else {
			taken = append(taken, qa)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return available, taken, nil
}

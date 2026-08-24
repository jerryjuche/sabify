package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Submission struct {
	ID             string
	QuizID         string
	StudentID      string
	Score          int
	TotalQuestions int
	SubmittedAt    time.Time
}

type SubmissionWithDetails struct {
	Submission
	StudentName string
	QuizTitle   string
	CourseTitle string
}

type SubmissionModel struct {
	DB *pgxpool.Pool
}

func (m *SubmissionModel) Insert(ctx context.Context, submission *Submission) error {
	query := `
		INSERT INTO submissions (quiz_id, student_id, score, total_questions)
		VALUES ($1, $2, $3, $4)
		RETURNING id, submitted_at
	`

	return m.DB.QueryRow(
		ctx, query,
		submission.QuizID, submission.StudentID,
		submission.Score, submission.TotalQuestions,
	).Scan(&submission.ID, &submission.SubmittedAt)
}

func (m *SubmissionModel) FindByStudent(ctx context.Context, studentID string) ([]Submission, error) {
	query := `
		SELECT id, quiz_id, student_id, score, total_questions, submitted_at
		FROM submissions
		WHERE student_id = $1
		ORDER BY submitted_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []Submission

	for rows.Next() {
		var s Submission
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID,
			&s.Score, &s.TotalQuestions, &s.SubmittedAt,
		); err != nil {
			return nil, err
		}
		submissions = append(submissions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return submissions, nil
}

func (m *SubmissionModel) FindByQuiz(ctx context.Context, quizID string) ([]Submission, error) {
	query := `
		SELECT id, quiz_id, student_id, score, total_questions, submitted_at
		FROM submissions
		WHERE quiz_id = $1
		ORDER BY submitted_at DESC
	`

	rows, err := m.DB.Query(ctx, query, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []Submission

	for rows.Next() {
		var s Submission
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID,
			&s.Score, &s.TotalQuestions, &s.SubmittedAt,
		); err != nil {
			return nil, err
		}
		submissions = append(submissions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return submissions, nil
}

/*
 * SubmissionWithQuiz pairs a submission with its
 * quiz title for student-facing history views.
 * Percent is derived (score/total*100, -1 when
 * the total is unknown).
 */

type SubmissionWithQuiz struct {
	Submission
	QuizTitle string
	Percent   int
}

func (m *SubmissionModel) FindByStudentWithQuiz(ctx context.Context, studentID string) ([]SubmissionWithQuiz, error) {
	query := `
		SELECT
			s.id, s.quiz_id, s.student_id,
			s.score, s.total_questions, s.submitted_at,
			q.title AS quiz_title
		FROM submissions s
		INNER JOIN quizzes q ON q.id = s.quiz_id
		WHERE s.student_id = $1
		ORDER BY s.submitted_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []SubmissionWithQuiz

	for rows.Next() {
		var s SubmissionWithQuiz
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID,
			&s.Score, &s.TotalQuestions, &s.SubmittedAt,
			&s.QuizTitle,
		); err != nil {
			return nil, err
		}

		s.Percent = -1
		if s.TotalQuestions > 0 {
			s.Percent = s.Score * 100 / s.TotalQuestions
		}

		submissions = append(submissions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return submissions, nil
}

func (m *SubmissionModel) AverageScoreByTeacher(ctx context.Context, teacherID string) (float64, error) {
	query := `
		SELECT COALESCE(
			ROUND(AVG(s.score::numeric / NULLIF(s.total_questions, 0) * 100), 1),
			0
		)
		FROM submissions s
		INNER JOIN quizzes q ON s.quiz_id = q.id
		INNER JOIN courses c ON q.course_id = c.id
		WHERE c.teacher_id = $1
	`

	var avg float64
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&avg)
	return avg, err
}

func (m *SubmissionModel) RecentByTeacher(ctx context.Context, teacherID string, limit int) ([]SubmissionWithDetails, error) {
	query := `
		SELECT
			s.id, s.quiz_id, s.student_id, s.score, s.total_questions, s.submitted_at,
			u.name AS student_name,
			q.title AS quiz_title,
			c.title AS course_title
		FROM submissions s
		INNER JOIN quizzes q ON s.quiz_id = q.id
		INNER JOIN courses c ON q.course_id = c.id
		INNER JOIN users u ON s.student_id = u.id
		WHERE c.teacher_id = $1
		ORDER BY s.submitted_at DESC
		LIMIT $2
	`

	rows, err := m.DB.Query(ctx, query, teacherID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []SubmissionWithDetails

	for rows.Next() {
		var s SubmissionWithDetails
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID, &s.Score, &s.TotalQuestions, &s.SubmittedAt,
			&s.StudentName, &s.QuizTitle, &s.CourseTitle,
		); err != nil {
			return nil, err
		}
		submissions = append(submissions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return submissions, nil
}

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

func (m *SubmissionModel) HasSubmitted(ctx context.Context, quizID, studentID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM submissions
			WHERE quiz_id = $1 AND student_id = $2
		)
	`

	var exists bool
	err := m.DB.QueryRow(ctx, query, quizID, studentID).Scan(&exists)
	return exists, err
}

func (m *SubmissionModel) CountByQuizStudent(ctx context.Context, quizID, studentID string) (int, error) {
	query := `
		SELECT COUNT(*) FROM submissions
		WHERE quiz_id = $1 AND student_id = $2
	`

	var count int
	err := m.DB.QueryRow(ctx, query, quizID, studentID).Scan(&count)
	return count, err
}

func (m *SubmissionModel) FindByQuizStudentAll(ctx context.Context, quizID, studentID string) ([]Submission, error) {
	query := `
		SELECT id, quiz_id, student_id, score, total_questions, submitted_at
		FROM submissions
		WHERE quiz_id = $1 AND student_id = $2
		ORDER BY submitted_at ASC
	`

	rows, err := m.DB.Query(ctx, query, quizID, studentID)
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

type SubmissionWithAttempt struct {
	Submission
	StudentName string
	QuizTitle   string
	CourseTitle string
	Attempt     int
}

type StudentSubmissionWithAttempt struct {
	SubmissionWithQuiz
	Attempt int
}

func (m *SubmissionModel) FindByStudentWithAttempt(ctx context.Context, studentID string) ([]StudentSubmissionWithAttempt, error) {
	query := `
		SELECT
			s.id, s.quiz_id, s.student_id,
			s.score, s.total_questions, s.submitted_at,
			q.title AS quiz_title,
			(
				SELECT COUNT(*) FROM submissions s2
				WHERE s2.quiz_id = s.quiz_id AND s2.student_id = s.student_id
				AND s2.submitted_at <= s.submitted_at
			) AS attempt
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

	var submissions []StudentSubmissionWithAttempt

	for rows.Next() {
		var s StudentSubmissionWithAttempt
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID,
			&s.Score, &s.TotalQuestions, &s.SubmittedAt,
			&s.QuizTitle, &s.Attempt,
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

func (m *SubmissionModel) FindByTeacherAllAttempts(ctx context.Context, teacherID string) ([]SubmissionWithAttempt, error) {
	query := `
		SELECT
			s.id, s.quiz_id, s.student_id, s.score, s.total_questions, s.submitted_at,
			u.name AS student_name,
			q.title AS quiz_title,
			c.title AS course_title,
			(
				SELECT COUNT(*) FROM submissions s2
				WHERE s2.quiz_id = s.quiz_id AND s2.student_id = s.student_id
				AND s2.submitted_at <= s.submitted_at
			) AS attempt
		FROM submissions s
		INNER JOIN users u ON u.id = s.student_id
		INNER JOIN quizzes q ON q.id = s.quiz_id
		INNER JOIN courses c ON c.id = q.course_id
		WHERE c.teacher_id = $1
		ORDER BY s.submitted_at DESC
	`

	rows, err := m.DB.Query(ctx, query, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []SubmissionWithAttempt

	for rows.Next() {
		var s SubmissionWithAttempt
		if err := rows.Scan(
			&s.ID, &s.QuizID, &s.StudentID,
			&s.Score, &s.TotalQuestions, &s.SubmittedAt,
			&s.StudentName, &s.QuizTitle, &s.CourseTitle, &s.Attempt,
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

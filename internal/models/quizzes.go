package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Quiz struct {
	ID          string
	CourseID    string
	Title       string
	Description string
	CreatedAt   time.Time
}

type QuizModel struct {
	DB *pgxpool.Pool
}

func (m *QuizModel) Insert(ctx context.Context, quiz *Quiz) error {
	query := `
		INSERT INTO quizzes (course_id, title, description)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`

	return m.DB.QueryRow(
		ctx, query,
		quiz.CourseID, quiz.Title, quiz.Description,
	).Scan(&quiz.ID, &quiz.CreatedAt)
}

func (m *QuizModel) FindByID(ctx context.Context, id string) (*Quiz, error) {
	var quiz Quiz

	query := `
		SELECT id, course_id, title, description, created_at
		FROM quizzes
		WHERE id = $1
	`

	err := m.DB.QueryRow(ctx, query, id).Scan(
		&quiz.ID, &quiz.CourseID, &quiz.Title,
		&quiz.Description, &quiz.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &quiz, nil
}

func (m *QuizModel) FindByCourse(ctx context.Context, courseID string) ([]Quiz, error) {
	query := `
		SELECT id, course_id, title, description, created_at
		FROM quizzes
		WHERE course_id = $1
		ORDER BY created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var quizzes []Quiz

	for rows.Next() {
		var quiz Quiz
		if err := rows.Scan(
			&quiz.ID, &quiz.CourseID, &quiz.Title,
			&quiz.Description, &quiz.CreatedAt,
		); err != nil {
			return nil, err
		}
		quizzes = append(quizzes, quiz)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return quizzes, nil
}

func (m *QuizModel) Update(ctx context.Context, quiz *Quiz) error {
	query := `
		UPDATE quizzes
		SET title = $1, description = $2
		WHERE id = $3
		RETURNING created_at
	`

	return m.DB.QueryRow(ctx, query, quiz.Title, quiz.Description, quiz.ID).Scan(&quiz.CreatedAt)
}

func (m *QuizModel) CountByTeacher(ctx context.Context, teacherID string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM quizzes q
		INNER JOIN courses c ON q.course_id = c.id
		WHERE c.teacher_id = $1
	`

	var count int
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&count)
	return count, err
}

func (m *QuizModel) CountActiveByTeacher(ctx context.Context, teacherID string) (int, error) {
	query := `
		SELECT COUNT(DISTINCT q.id)
		FROM quizzes q
		INNER JOIN courses c ON q.course_id = c.id
		INNER JOIN submissions s ON s.quiz_id = q.id
		WHERE c.teacher_id = $1
	`

	var count int
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&count)
	return count, err
}

func (m *QuizModel) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM quizzes WHERE id = $1`

	result, err := m.DB.Exec(ctx, query, id)
	if err != nil {
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrNoRecord
	}

	return nil
}

/*
 * QuizWithCourse pairs a quiz with its parent
 * course title for student-facing listings.
 */

type QuizWithCourse struct {
	Quiz
	CourseTitle   string
	QuestionCount int
}

func (m *QuizModel) FindAllWithCourse(ctx context.Context) ([]QuizWithCourse, error) {
	query := `
		SELECT
			q.id, q.course_id, q.title, q.description, q.created_at,
			c.title AS course_title,
			COUNT(qn.id) AS question_count
		FROM quizzes q
		INNER JOIN courses c ON c.id = q.course_id
		LEFT JOIN questions qn ON qn.quiz_id = q.id
		GROUP BY q.id, c.title
		ORDER BY q.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var quizzes []QuizWithCourse

	for rows.Next() {
		var q QuizWithCourse
		if err := rows.Scan(
			&q.ID, &q.CourseID, &q.Title,
			&q.Description, &q.CreatedAt,
			&q.CourseTitle, &q.QuestionCount,
		); err != nil {
			return nil, err
		}
		quizzes = append(quizzes, q)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return quizzes, nil
}

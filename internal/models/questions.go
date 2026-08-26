package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Question struct {
	ID            string
	QuizID        string
	QuestionText  string
	OptionA       string
	OptionB       string
	OptionC       string
	OptionD       string
	CorrectAnswer string
	CreatedAt     time.Time
}

type QuestionModel struct {
	DB *pgxpool.Pool
}

func (m *QuestionModel) Insert(ctx context.Context, question *Question) error {
	query := `
		INSERT INTO questions (quiz_id, question_text, option_a, option_b, option_c, option_d, correct_answer)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`

	return m.DB.QueryRow(
		ctx, query,
		question.QuizID, question.QuestionText,
		question.OptionA, question.OptionB, question.OptionC, question.OptionD,
		question.CorrectAnswer,
	).Scan(&question.ID, &question.CreatedAt)
}

func (m *QuestionModel) FindByQuiz(ctx context.Context, quizID string) ([]Question, error) {
	query := `
		SELECT id, quiz_id, question_text, option_a, option_b, option_c, option_d, correct_answer, created_at
		FROM questions
		WHERE quiz_id = $1
		ORDER BY created_at ASC
	`

	rows, err := m.DB.Query(ctx, query, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var questions []Question

	for rows.Next() {
		var q Question
		if err := rows.Scan(
			&q.ID, &q.QuizID, &q.QuestionText,
			&q.OptionA, &q.OptionB, &q.OptionC, &q.OptionD,
			&q.CorrectAnswer, &q.CreatedAt,
		); err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return questions, nil
}

func (m *QuestionModel) DeleteByQuiz(ctx context.Context, quizID string) error {
	query := `DELETE FROM questions WHERE quiz_id = $1`
	_, err := m.DB.Exec(ctx, query, quizID)
	return err
}

func (m *QuestionModel) ReplaceByQuiz(ctx context.Context, quizID string, questions []Question) error {
	if err := m.DeleteByQuiz(ctx, quizID); err != nil {
		return err
	}

	for index := range questions {
		questions[index].QuizID = quizID
		if err := m.Insert(ctx, &questions[index]); err != nil {
			return err
		}
	}

	return nil
}

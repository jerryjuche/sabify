package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CourseAccess struct {
	ID        string
	StudentID string
	CourseID  string
	PaymentID *string
	Status    string
	CreatedAt time.Time
}

type CourseAccessModel struct {
	DB *pgxpool.Pool
}

func (m *CourseAccessModel) Create(ctx context.Context, studentID, courseID, paymentID string) (*CourseAccess, error) {
	query := `
		INSERT INTO course_access (student_id, course_id, payment_id, status)
		VALUES ($1, $2, $3, 'PENDING')
		RETURNING id, student_id, course_id, payment_id, status, created_at
	`

	var a CourseAccess
	err := m.DB.QueryRow(ctx, query, studentID, courseID, paymentID).Scan(
		&a.ID, &a.StudentID, &a.CourseID, &a.PaymentID, &a.Status, &a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &a, nil
}

func (m *CourseAccessModel) Find(ctx context.Context, studentID, courseID string) (*CourseAccess, error) {
	query := `
		SELECT id, student_id, course_id, payment_id, status, created_at
		FROM course_access
		WHERE student_id = $1 AND course_id = $2
	`

	var a CourseAccess
	err := m.DB.QueryRow(ctx, query, studentID, courseID).Scan(
		&a.ID, &a.StudentID, &a.CourseID, &a.PaymentID, &a.Status, &a.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &a, nil
}

func (m *CourseAccessModel) SetActive(ctx context.Context, studentID, courseID, paymentID string) error {
	query := `
		UPDATE course_access
		SET status = 'ACTIVE', payment_id = COALESCE($3, payment_id)
		WHERE student_id = $1 AND course_id = $2
	`

	result, err := m.DB.Exec(ctx, query, studentID, courseID, paymentID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrNoRecord
	}

	return nil
}

func (m *CourseAccessModel) FindByStudent(ctx context.Context, studentID string) ([]CourseAccess, error) {
	query := `
		SELECT id, student_id, course_id, payment_id, status, created_at
		FROM course_access
		WHERE student_id = $1
		ORDER BY created_at ASC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accesses []CourseAccess

	for rows.Next() {
		var a CourseAccess
		if err := rows.Scan(
			&a.ID, &a.StudentID, &a.CourseID, &a.PaymentID, &a.Status, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		accesses = append(accesses, a)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return accesses, nil
}

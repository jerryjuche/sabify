package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Payment struct {
	ID             string
	StudentID      string
	CourseID       string
	AmountKobo     int64
	Status         string
	Reference      string
	NarrationHint  string
	MatchedEventID *string
	CreatedAt      time.Time
	PaidAt         *time.Time
}

type PaymentModel struct {
	DB *pgxpool.Pool
}

func (m *PaymentModel) CreatePending(ctx context.Context, studentID, courseID string, amountKobo int64, reference, narrationHint string) (*Payment, error) {
	query := `
		INSERT INTO payments (student_id, course_id, amount_kobo, status, reference, narration_hint)
		VALUES ($1, $2, $3, 'PENDING', $4, $5)
		RETURNING id, student_id, course_id, amount_kobo, status, reference, narration_hint, matched_event_id, created_at, paid_at
	`

	var p Payment
	err := m.DB.QueryRow(ctx, query, studentID, courseID, amountKobo, reference, narrationHint).Scan(
		&p.ID, &p.StudentID, &p.CourseID, &p.AmountKobo, &p.Status,
		&p.Reference, &p.NarrationHint, &p.MatchedEventID, &p.CreatedAt, &p.PaidAt,
	)
	if err != nil {
		return nil, err
	}

	return &p, nil
}

func (m *PaymentModel) FindByID(ctx context.Context, id string) (*Payment, error) {
	query := `
		SELECT id, student_id, course_id, amount_kobo, status, reference, narration_hint, matched_event_id, created_at, paid_at
		FROM payments
		WHERE id = $1
	`

	var p Payment
	err := m.DB.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.StudentID, &p.CourseID, &p.AmountKobo, &p.Status,
		&p.Reference, &p.NarrationHint, &p.MatchedEventID, &p.CreatedAt, &p.PaidAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &p, nil
}

func (m *PaymentModel) MarkPaid(ctx context.Context, id, eventID string) error {
	query := `
		UPDATE payments
		SET status = 'PAID', matched_event_id = $2, paid_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := m.DB.Exec(ctx, query, id, eventID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return ErrNoRecord
	}

	return nil
}

func (m *PaymentModel) ListForStudent(ctx context.Context, studentID string) ([]Payment, error) {
	query := `
		SELECT id, student_id, course_id, amount_kobo, status, reference, narration_hint, matched_event_id, created_at, paid_at
		FROM payments
		WHERE student_id = $1
		ORDER BY created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var payments []Payment

	for rows.Next() {
		var p Payment
		if err := rows.Scan(
			&p.ID, &p.StudentID, &p.CourseID, &p.AmountKobo, &p.Status,
			&p.Reference, &p.NarrationHint, &p.MatchedEventID, &p.CreatedAt, &p.PaidAt,
		); err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return payments, nil
}

// FindPendingForDeposit returns the most recent PENDING payment that still has
// an unresolved (PENDING) course_access, so a BMONI deposit can be matched to
// a student/course. Returns (nil, nil) when there is nothing to match.
func (m *PaymentModel) FindPendingForDeposit(ctx context.Context) (*Payment, error) {
	query := `
		SELECT p.id, p.student_id, p.course_id, p.amount_kobo, p.status,
		       p.reference, p.narration_hint, p.matched_event_id, p.created_at, p.paid_at
		FROM payments p
		INNER JOIN course_access ca ON ca.payment_id = p.id
		WHERE p.status = 'PENDING' AND ca.status = 'PENDING'
		ORDER BY p.created_at ASC
		LIMIT 1
	`

	var p Payment
	err := m.DB.QueryRow(ctx, query).Scan(
		&p.ID, &p.StudentID, &p.CourseID, &p.AmountKobo, &p.Status,
		&p.Reference, &p.NarrationHint, &p.MatchedEventID, &p.CreatedAt, &p.PaidAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	return &p, nil
}

func (m *PaymentModel) SumPaidByTeacher(ctx context.Context, teacherID string) (int64, error) {
	query := `
		SELECT COALESCE(SUM(p.amount_kobo), 0)
		FROM payments p
		INNER JOIN courses c ON c.id = p.course_id
		WHERE c.teacher_id = $1 AND p.status = 'PAID'
	`

	var total int64
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&total)
	return total, err
}

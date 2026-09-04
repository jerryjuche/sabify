package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Course struct {
	ID          string
	Title       string
	Description string
	TeacherID   string
	PriceKobo   *int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CourseModel struct {
	DB *pgxpool.Pool
}

func (m *CourseModel) Insert(ctx context.Context, course *Course) error {
	query := `
		INSERT INTO courses (title, description, teacher_id, price_kobo)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`

	return m.DB.QueryRow(
		ctx, query,
		course.Title, course.Description, course.TeacherID, course.PriceKobo,
	).Scan(&course.ID, &course.CreatedAt, &course.UpdatedAt)
}

func (m *CourseModel) FindByID(ctx context.Context, id string) (*Course, error) {
	var course Course

	query := `
		SELECT id, title, description, teacher_id, price_kobo, created_at, updated_at
		FROM courses
		WHERE id = $1
	`

	err := m.DB.QueryRow(ctx, query, id).Scan(
		&course.ID, &course.Title, &course.Description,
		&course.TeacherID, &course.PriceKobo, &course.CreatedAt, &course.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &course, nil
}

func (m *CourseModel) FindByTeacher(ctx context.Context, teacherID string) ([]Course, error) {
	query := `
		SELECT id, title, description, teacher_id, price_kobo, created_at, updated_at
		FROM courses
		WHERE teacher_id = $1
		ORDER BY created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var courses []Course

	for rows.Next() {
		var course Course
		if err := rows.Scan(
			&course.ID, &course.Title, &course.Description,
			&course.TeacherID, &course.PriceKobo, &course.CreatedAt, &course.UpdatedAt,
		); err != nil {
			return nil, err
		}
		courses = append(courses, course)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return courses, nil
}

func (m *CourseModel) FindAll(ctx context.Context) ([]Course, error) {
	query := `
		SELECT id, title, description, teacher_id, price_kobo, created_at, updated_at
		FROM courses
		ORDER BY created_at DESC
	`

	rows, err := m.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var courses []Course

	for rows.Next() {
		var course Course
		if err := rows.Scan(
			&course.ID, &course.Title, &course.Description,
			&course.TeacherID, &course.PriceKobo, &course.CreatedAt, &course.UpdatedAt,
		); err != nil {
			return nil, err
		}
		courses = append(courses, course)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return courses, nil
}

func (m *CourseModel) Update(ctx context.Context, course *Course) error {
	query := `
		UPDATE courses
		SET title = $1, description = $2, price_kobo = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
		RETURNING updated_at
	`

	return m.DB.QueryRow(
		ctx, query,
		course.Title, course.Description, course.PriceKobo, course.ID,
	).Scan(&course.UpdatedAt)
}

func (m *CourseModel) UpdatePrice(ctx context.Context, id string, priceKobo *int64) error {
	query := `
		UPDATE courses
		SET price_kobo = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
		RETURNING updated_at
	`

	var updatedAt time.Time
	return m.DB.QueryRow(ctx, query, priceKobo, id).Scan(&updatedAt)
}

// FindByStudent returns the courses a student can study: everything they
// enrolled in directly (free courses) plus paid courses with ACTIVE access.
// Study-group creation uses this to offer only courses the student belongs to.
func (m *CourseModel) FindByStudent(ctx context.Context, studentID string) ([]Course, error) {
	query := `
		SELECT DISTINCT c.id, c.title, c.description, c.teacher_id,
		       c.price_kobo, c.created_at, c.updated_at
		FROM courses c
		LEFT JOIN enrollments e ON e.course_id = c.id AND e.student_id = $1
		LEFT JOIN course_access a ON a.course_id = c.id AND a.student_id = $1 AND a.status = 'ACTIVE'
		WHERE e.student_id = $1 OR a.course_id IS NOT NULL
		ORDER BY c.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var courses []Course
	for rows.Next() {
		var course Course
		if err := rows.Scan(
			&course.ID, &course.Title, &course.Description,
			&course.TeacherID, &course.PriceKobo, &course.CreatedAt, &course.UpdatedAt,
		); err != nil {
			return nil, err
		}
		courses = append(courses, course)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return courses, nil
}

func (m *CourseModel) CountByTeacher(ctx context.Context, teacherID string) (int, error) {
	query := `SELECT COUNT(*) FROM courses WHERE teacher_id = $1`

	var count int
	err := m.DB.QueryRow(ctx, query, teacherID).Scan(&count)
	return count, err
}

func (m *CourseModel) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM courses WHERE id = $1`

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
 * CourseWithTeacher pairs a course with its teacher's
 * name and a quiz count — everything a student-facing
 * course card needs in a single round trip.
 */

type CourseWithTeacher struct {
	Course
	TeacherName string
	QuizCount   int
}

func (m *CourseModel) FindByIDWithTeacher(ctx context.Context, id string) (*CourseWithTeacher, error) {
	var c CourseWithTeacher

	query := `
		SELECT
			c.id, c.title, c.description, c.teacher_id,
			c.price_kobo, c.created_at, c.updated_at,
			u.name AS teacher_name,
			COUNT(q.id) AS quiz_count
		FROM courses c
		INNER JOIN users u ON u.id = c.teacher_id
		LEFT JOIN quizzes q ON q.course_id = c.id
		WHERE c.id = $1
		GROUP BY c.id, u.name
	`

	err := m.DB.QueryRow(ctx, query, id).Scan(
		&c.ID, &c.Title, &c.Description, &c.TeacherID,
		&c.PriceKobo, &c.CreatedAt, &c.UpdatedAt,
		&c.TeacherName, &c.QuizCount,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &c, nil
}

func (m *CourseModel) FindAllWithTeacher(ctx context.Context) ([]CourseWithTeacher, error) {
	query := `
		SELECT
			c.id, c.title, c.description, c.teacher_id,
			c.price_kobo, c.created_at, c.updated_at,
			u.name AS teacher_name,
			COUNT(q.id) AS quiz_count
		FROM courses c
		INNER JOIN users u ON u.id = c.teacher_id
		LEFT JOIN quizzes q ON q.course_id = c.id
		GROUP BY c.id, u.name
		ORDER BY c.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var courses []CourseWithTeacher

	for rows.Next() {
		var c CourseWithTeacher
		if err := rows.Scan(
			&c.ID, &c.Title, &c.Description, &c.TeacherID,
			&c.PriceKobo, &c.CreatedAt, &c.UpdatedAt,
			&c.TeacherName, &c.QuizCount,
		); err != nil {
			return nil, err
		}
		courses = append(courses, c)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return courses, nil
}

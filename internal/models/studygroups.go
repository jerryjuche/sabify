package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type StudyGroup struct {
	ID        string
	Name      string
	CourseID  string
	CreatedAt time.Time
}

type StudyGroupModel struct {
	DB *pgxpool.Pool
}

func (m *StudyGroupModel) Insert(ctx context.Context, group *StudyGroup) error {
	query := `
		INSERT INTO study_groups (name, course_id)
		VALUES ($1, $2)
		RETURNING id, created_at
	`

	return m.DB.QueryRow(
		ctx, query,
		group.Name, group.CourseID,
	).Scan(&group.ID, &group.CreatedAt)
}

func (m *StudyGroupModel) FindByID(ctx context.Context, id string) (*StudyGroup, error) {
	var group StudyGroup

	query := `
		SELECT id, name, course_id, created_at
		FROM study_groups
		WHERE id = $1
	`

	err := m.DB.QueryRow(ctx, query, id).Scan(
		&group.ID, &group.Name, &group.CourseID, &group.CreatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &group, nil
}

func (m *StudyGroupModel) FindByCourse(ctx context.Context, courseID string) ([]StudyGroup, error) {
	query := `
		SELECT id, name, course_id, created_at
		FROM study_groups
		WHERE course_id = $1
		ORDER BY created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []StudyGroup

	for rows.Next() {
		var g StudyGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CourseID, &g.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return groups, nil
}

func (m *StudyGroupModel) AddMember(ctx context.Context, groupID, studentID string) error {
	query := `
		INSERT INTO study_group_members (study_group_id, student_id)
		VALUES ($1, $2)
	`

	_, err := m.DB.Exec(ctx, query, groupID, studentID)
	return err
}

func (m *StudyGroupModel) FindMembers(ctx context.Context, groupID string) ([]User, error) {
	query := `
		SELECT u.id, u.name, u.email, u.role, u.created_at, u.updated_at
		FROM users u
		INNER JOIN study_group_members sgm ON u.id = sgm.student_id
		WHERE sgm.study_group_id = $1
		ORDER BY sgm.joined_at ASC
	`

	rows, err := m.DB.Query(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []User

	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Name, &u.Email, &u.Role,
			&u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, err
		}
		members = append(members, u)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return members, nil
}

/*
 * StudyGroupWithMeta is a student-facing view of a
 * study group: its course title, member count and
 * whether the current student belongs to it.
 */

type StudyGroupWithMeta struct {
	StudyGroup
	CourseTitle string
	MemberCount int
	IsMember    bool
}

func (m *StudyGroupModel) FindAllForStudent(ctx context.Context, studentID string) ([]StudyGroupWithMeta, error) {
	query := `
		SELECT
			g.id, g.name, g.course_id, g.created_at,
			COALESCE(c.title, ''),
			COUNT(m.student_id) AS member_count,
			BOOL_OR(m.student_id = $1) AS is_member
		FROM study_groups g
		LEFT JOIN courses c ON c.id = g.course_id
		LEFT JOIN study_group_members m ON m.study_group_id = g.id
		GROUP BY g.id, c.title
		ORDER BY g.created_at DESC
	`

	rows, err := m.DB.Query(ctx, query, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []StudyGroupWithMeta

	for rows.Next() {
		var g StudyGroupWithMeta
		if err := rows.Scan(
			&g.ID, &g.Name, &g.CourseID, &g.CreatedAt,
			&g.CourseTitle, &g.MemberCount, &g.IsMember,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return groups, nil
}

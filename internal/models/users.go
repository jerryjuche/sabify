package models

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNoRecord           = errors.New("models: no matching record found")
	ErrInvalidCredentials = errors.New("models: invalid credentials")
	ErrDuplicateEmail     = errors.New("models: duplicate email")
)

type User struct {
	ID           string
	Name         string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type UserModel struct {
	DB *pgxpool.Pool
}

func (m *UserModel) Insert(ctx context.Context, user *User, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO users (name, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`

	err = m.DB.QueryRow(
		ctx, query,
		user.Name, user.Email, string(hash), user.Role,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {

		/*
		 * A unique-constraint violation means the email
		 * was taken between the Exists() pre-check and
		 * this insert — surface it as a friendly error
		 * instead of an internal server error.
		 */

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateEmail
		}

		return err
	}

	return nil
}

func (m *UserModel) Authenticate(ctx context.Context, email, password string) (*User, error) {
	var user User

	query := `
		SELECT id, name, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE email = $1
	`

	err := m.DB.QueryRow(ctx, query, email).Scan(
		&user.ID, &user.Name, &user.Email, &user.PasswordHash,
		&user.Role, &user.CreatedAt, &user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	} else if err != nil {
		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	return &user, nil
}

func (m *UserModel) FindByEmail(ctx context.Context, email string) (*User, error) {
	var user User

	query := `
		SELECT id, name, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE email = $1
	`

	err := m.DB.QueryRow(ctx, query, email).Scan(
		&user.ID, &user.Name, &user.Email, &user.PasswordHash,
		&user.Role, &user.CreatedAt, &user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &user, nil
}

func (m *UserModel) FindByID(ctx context.Context, id string) (*User, error) {
	var user User

	query := `
		SELECT id, name, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	err := m.DB.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.Name, &user.Email, &user.PasswordHash,
		&user.Role, &user.CreatedAt, &user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &user, nil
}

func (m *UserModel) Exists(ctx context.Context, email string) (bool, error) {
	var exists bool

	query := `
		SELECT EXISTS (
			SELECT 1 FROM users WHERE email = $1
		)
	`

	err := m.DB.QueryRow(ctx, query, strings.ToLower(strings.TrimSpace(email))).Scan(&exists)
	return exists, err
}

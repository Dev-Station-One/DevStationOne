// Package auth implements user accounts, password hashing, and browser sessions.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// ErrEmailTaken is returned when creating a user whose email already exists.
var ErrEmailTaken = errors.New("email already registered")

// User is an authenticated account.
type User struct {
	ID           string
	Email        string
	IsAdmin      bool
	CreatedAt    time.Time
	TerminalFont string
	Theme        string
}

// ValidTerminalFont reports whether font is a supported users.terminal_font
// value ("system" keeps the browser default stack).
func ValidTerminalFont(font string) bool {
	return font == "system" || font == "jetbrainsmono-nerd"
}

// ValidTheme reports whether theme is a supported users.theme value.
func ValidTheme(theme string) bool {
	return theme == "dark" || theme == "light"
}

// HashPassword bcrypt-hashes a plaintext password for storage.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// CreateUser inserts a new user with the given email and plaintext password.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email, password string, isAdmin bool) (User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}

	var u User
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, is_admin)
		VALUES ($1, $2, $3)
		RETURNING id, email, is_admin, created_at, terminal_font, theme
	`, email, hash, isAdmin).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "users_email_key" {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	return u, nil
}

// CountUsers returns the total number of user accounts.
func CountUsers(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// Authenticate verifies email/password and returns the matching user.
func Authenticate(ctx context.Context, pool *pgxpool.Pool, email, password string) (User, error) {
	var u User
	var hash string
	err := pool.QueryRow(ctx, `
		SELECT id, email, password_hash, is_admin, created_at, terminal_font, theme FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &hash, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme)
	if errors.Is(err, pgx.ErrNoRows) {
		// Run a hash comparison anyway so login timing doesn't reveal whether the email exists.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidsaltinvalidsaltinvalidsaltinvalidsal"), []byte(password))
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

// SetTerminalFont persists a user's terminal font preference.
func SetTerminalFont(ctx context.Context, pool *pgxpool.Pool, userID, font string) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET terminal_font = $1 WHERE id = $2`, font, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTheme persists a user's UI theme preference ("dark" or "light").
func SetTheme(ctx context.Context, pool *pgxpool.Pool, userID, theme string) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET theme = $1 WHERE id = $2`, theme, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

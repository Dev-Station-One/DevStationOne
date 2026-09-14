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
	// DisplayName is the optional friendly name shown instead of the
	// email (journal creator column; empty means fall back to email).
	DisplayName string
	// EditorFont/FontSize are the Code tab's editor font preference (and
	// the shared terminal+editor size, 12-18px).
	EditorFont string
	FontSize   int
}

// ValidTerminalFont reports whether font is a supported users.terminal_font
// value ("system" keeps the browser default stack). The plain coding faces
// lose the Nerd Font's glyph icons — an accepted trade
// (docs/CODE_TAB_REQUIREMENTS.md).
func ValidTerminalFont(font string) bool {
	switch font {
	case "system", "jetbrainsmono-nerd", "fira-code", "cascadia-code", "source-code-pro":
		return true
	}
	return false
}

// ValidEditorFont reports whether font is a supported users.editor_font
// value for the Code tab ("system" keeps the browser default stack).
// JetBrains Mono is terminal-only by decision.
func ValidEditorFont(font string) bool {
	switch font {
	case "system", "fira-code", "cascadia-code", "source-code-pro":
		return true
	}
	return false
}

// ValidFontSize reports whether n is a supported users.font_size value.
func ValidFontSize(n int) bool {
	return n >= 12 && n <= 18
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
		RETURNING id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size
	`, email, hash, isAdmin).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "users_email_key" {
			return User{}, ErrEmailTaken
		}
		return User{}, err
	}
	return u, nil
}

// UpsertAdmin creates an administrator account for email, or promotes an
// existing account with that email to administrator and resets its
// password. Returns whether a new account was created (false means an
// existing account was updated).
func UpsertAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string) (User, bool, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, false, err
	}

	var u User
	var created bool
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, is_admin)
		VALUES ($1, $2, true)
		ON CONFLICT (email) DO UPDATE SET password_hash = $2, is_admin = true
		RETURNING id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, (xmax = 0) AS inserted
	`, email, hash).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &created)
	if err != nil {
		return User{}, false, err
	}
	return u, created, nil
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
		SELECT id, email, password_hash, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &hash, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize)
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

// SetEditorFont persists a user's editor font preference.
func SetEditorFont(ctx context.Context, pool *pgxpool.Pool, userID, font string) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET editor_font = $1 WHERE id = $2`, font, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetFontSize persists a user's shared terminal+editor font size.
func SetFontSize(ctx context.Context, pool *pgxpool.Pool, userID string, size int) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET font_size = $1 WHERE id = $2`, size, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDisplayName persists a user's display name (trimmed; empty clears
// it, falling back to the email wherever a name is shown).
func SetDisplayName(ctx context.Context, pool *pgxpool.Pool, userID, name string) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET display_name = $1 WHERE id = $2`, name, userID)
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

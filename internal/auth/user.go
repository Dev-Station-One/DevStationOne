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
	// EnabledLanguages gates which CodeMirror language-data entries
	// participate in the Code tab's filename-based auto-detect (docs/CODE_TAB_REQUIREMENTS.md).
	// Names match @codemirror/language-data's LanguageDescription.name; not
	// validated against that list server-side (ponytail: no real trust
	// boundary here — an unrecognized name just never matches client-side;
	// add a whitelist if that stops being true).
	EnabledLanguages []string
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

// ValidEnabledLanguages reports whether langs is an acceptable
// users.enabled_languages value: a reasonably-sized list of reasonably-sized
// names. Not checked against the actual language-data table (see the
// EnabledLanguages field comment).
func ValidEnabledLanguages(langs []string) bool {
	if len(langs) > 200 {
		return false
	}
	for _, l := range langs {
		if l == "" || len(l) > 64 {
			return false
		}
	}
	return true
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
		RETURNING id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages
	`, email, hash, isAdmin).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages)
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
		RETURNING id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages, (xmax = 0) AS inserted
	`, email, hash).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages, &created)
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
		SELECT id, email, password_hash, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &hash, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages)
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

// SetEnabledLanguages persists which languages participate in the Code
// tab's auto-detect.
func SetEnabledLanguages(ctx context.Context, pool *pgxpool.Pool, userID string, langs []string) error {
	tag, err := pool.Exec(ctx, `UPDATE users SET enabled_languages = $1 WHERE id = $2`, langs, userID)
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

// AdminUser is an account as the user-manager sees it: profile plus the
// live last-activity timestamp derived from the user's auth sessions.
type AdminUser struct {
	ID             string
	Email          string
	DisplayName    string
	IsAdmin        bool
	CreatedAt      time.Time
	LastAccessedAt *time.Time
}

// ListAdminUsers returns every account with its last activity
// (max sessions.last_seen_at; null when the user has no auth sessions).
func ListAdminUsers(ctx context.Context, pool *pgxpool.Pool) ([]AdminUser, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.id, u.email, u.display_name, u.is_admin, u.created_at,
		       MAX(s.last_seen_at)
		FROM users u
		LEFT JOIN sessions s ON s.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.IsAdmin, &u.CreatedAt, &u.LastAccessedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AdminUpdate applies a partial administrative update to a user account:
// only the non-nil fields change. Password, when given, is plaintext and
// bcrypt-hashed here.
func AdminUpdate(ctx context.Context, pool *pgxpool.Pool, id string, displayName, password *string, isAdmin *bool) (User, error) {
	var hash *string
	if password != nil {
		h, err := HashPassword(*password)
		if err != nil {
			return User{}, err
		}
		hash = &h
	}

	var u User
	err := pool.QueryRow(ctx, `
		UPDATE users SET
			display_name  = COALESCE($2, display_name),
			password_hash = COALESCE($3, password_hash),
			is_admin      = COALESCE($4, is_admin)
		WHERE id = $1
		RETURNING id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages`,
		id, displayName, hash, isAdmin,
	).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// CountAdmins returns how many administrator accounts exist.
func CountAdmins(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin`).Scan(&n)
	return n, err
}

// DeleteUser removes a user account; auth sessions and per-user preference
// rows cascade, while ownership-blocked resources (projects, runtimes) are
// checked by the caller first.
func DeleteUser(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetUserByID looks a user up by ID regardless of authentication; callers
// are responsible for authorizing what they do with the result.
func GetUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// GetUserByEmail looks a user up by email without checking a password —
// used by the 2FA recovery-request flow, which must know whether an
// account exists before deciding whether to send a recovery email.
func GetUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT id, email, is_admin, created_at, terminal_font, theme, display_name, editor_font, font_size, enabled_languages FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.TerminalFont, &u.Theme, &u.DisplayName, &u.EditorFont, &u.FontSize, &u.EnabledLanguages)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

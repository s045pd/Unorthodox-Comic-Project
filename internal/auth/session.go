package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserInactive       = errors.New("user is inactive")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrUserNotFound       = errors.New("user not found")
	ErrLastAdmin          = errors.New("cannot remove or demote the last admin")
	ErrPasswordTooShort   = errors.New("password must be at least 4 characters")
)

// MinPasswordLen is the minimum length we accept on user-supplied passwords.
// Internally-generated passwords from GenerateRandomPassword are always 64+.
const MinPasswordLen = 4

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type User struct {
	ID        int64
	Username  string
	Role      string
	Active    bool
	CreatedAt time.Time
}

func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin && u.Active }

type Session struct {
	Token     string
	UserID    int64
	Username  string
	Role      string
	ExpiresAt time.Time
}

func (s *Session) IsAdmin() bool { return s != nil && s.Role == RoleAdmin }

// EnsureFirstRunAdmin creates the bootstrap admin user with a random password
// if no users exist yet. Returns (password, created, error). If created==true,
// the password is also written to <volDir>/first-run-password.txt.
func (s *Store) EnsureFirstRunAdmin(ctx context.Context, volDir string) (string, bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}
	pw, err := GenerateRandomPassword()
	if err != nil {
		return "", false, err
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return "", false, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, role, active, created_at) VALUES(?,?,?,?,?)`,
		"admin", hash, RoleAdmin, 1, time.Now().Unix()); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(volDir, 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(filepath.Join(volDir, "first-run-password.txt"),
		[]byte(pw+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write password file: %w", err)
	}
	return pw, true, nil
}

func (s *Store) Authenticate(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash string
	var active int
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, active, created_at FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &hash, &u.Role, &active, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
	if active == 0 {
		return nil, ErrUserInactive
	}
	u.Active = true
	u.CreatedAt = time.Unix(created, 0)
	return &u, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, expires_at, created_at) VALUES(?,?,?,?)`,
		token, userID, now.Add(ttl).Unix(), now.Unix())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) LookupSession(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var expiresAt int64
	var active int
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token, s.user_id, s.expires_at, u.username, u.role, u.active
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token=? AND s.expires_at > ?`,
		token, time.Now().Unix()).
		Scan(&sess.Token, &sess.UserID, &expiresAt, &sess.Username, &sess.Role, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if active == 0 {
		// account deactivated mid-session — treat as logged out.
		return nil, nil
	}
	sess.ExpiresAt = time.Unix(expiresAt, 0)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

// ===== user management =====

// ListUsers returns every user ordered by id (admin first, since the bootstrap
// admin has the smallest id).
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, role, active, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var active int
		var created int64
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &active, &created); err != nil {
			return nil, err
		}
		u.Active = active == 1
		u.CreatedAt = time.Unix(created, 0)
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser adds a new user. If password is empty, a random 64-char password
// is generated. Returns the plain password (only time it's shown — admin must
// capture if auto-generated).
func (s *Store) CreateUser(ctx context.Context, username, role, password string) (string, error) {
	username = trimSpace(username)
	if username == "" {
		return "", errors.New("username cannot be empty")
	}
	if role != RoleAdmin && role != RoleUser {
		role = RoleUser
	}
	pw := password
	if pw == "" {
		gen, err := GenerateRandomPassword()
		if err != nil {
			return "", err
		}
		pw = gen
	} else if len(pw) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, role, active, created_at) VALUES(?,?,?,?,?)`,
		username, hash, role, 1, time.Now().Unix()); err != nil {
		if isUniqueViolation(err) {
			return "", ErrUsernameTaken
		}
		return "", err
	}
	return pw, nil
}

// ResetPassword sets the user's password. If password is empty, a random 64-char
// password is generated. Either way, all existing sessions for the user are
// invalidated.
func (s *Store) ResetPassword(ctx context.Context, userID int64, password string) (string, error) {
	pw := password
	if pw == "" {
		gen, err := GenerateRandomPassword()
		if err != nil {
			return "", err
		}
		pw = gen
	} else if len(pw) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=? WHERE id=?`, hash, userID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrUserNotFound
	}
	// Wipe existing sessions for this user — force re-login on next request.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	return pw, nil
}

// SetActive flips the active flag. Refuses to deactivate the last active admin.
func (s *Store) SetActive(ctx context.Context, userID int64, active bool) error {
	if !active {
		// Guard: ensure at least one active admin remains.
		var role string
		var isActive int
		err := s.db.QueryRowContext(ctx,
			`SELECT role, active FROM users WHERE id=?`, userID).Scan(&role, &isActive)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if role == RoleAdmin && isActive == 1 {
			var others int
			_ = s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM users WHERE role='admin' AND active=1 AND id != ?`,
				userID).Scan(&others)
			if others == 0 {
				return ErrLastAdmin
			}
		}
	}
	val := 0
	if active {
		val = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET active=? WHERE id=?`, val, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	if !active {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	}
	return nil
}

// SetRole changes role. Refuses to demote the last admin.
func (s *Store) SetRole(ctx context.Context, userID int64, role string) error {
	if role != RoleAdmin && role != RoleUser {
		return errors.New("invalid role")
	}
	if role == RoleUser {
		var others int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE role='admin' AND active=1 AND id != ?`,
			userID).Scan(&others)
		if others == 0 {
			return ErrLastAdmin
		}
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET role=? WHERE id=?`, role, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes a user and cascades sessions/bookmarks. Refuses last admin.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if role == RoleAdmin {
		var others int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE role='admin' AND id != ?`,
			userID).Scan(&others)
		if others == 0 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID)
	return err
}

// ===== helpers =====

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc/sqlite returns "constraint failed: UNIQUE constraint failed: users.username (2067)"
	for _, needle := range []string{"UNIQUE constraint failed", "constraint failed: UNIQUE"} {
		if containsCI(msg, needle) {
			return true
		}
	}
	return false
}

func containsCI(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if eqASCIIFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func eqASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

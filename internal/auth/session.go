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

var ErrInvalidCredentials = errors.New("invalid credentials")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type User struct {
	ID       int64
	Username string
}

type Session struct {
	Token     string
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

// EnsureFirstRunAdmin creates admin user with a random password if none exists.
// Returns (password, created, error). If created==true, the password is also
// written to <volDir>/first-run-password.txt.
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
		`INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`,
		"admin", hash, time.Now().Unix()); err != nil {
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
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
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
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token, s.user_id, s.expires_at, u.username
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token=? AND s.expires_at > ?`,
		token, time.Now().Unix()).
		Scan(&sess.Token, &sess.UserID, &expiresAt, &sess.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
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

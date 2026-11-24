package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User represents an admin account.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	CreatedAt    int64  `json:"created_at"`
}

// Session represents an authenticated UI session.
type Session struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"`
}

// HasAdmin returns true if at least one admin user exists.
func (s *Store) HasAdmin() (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateAdmin creates an admin user with a bcrypt-hashed password.
func (s *Store) CreateAdmin(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 6 {
		return nil, errors.New("username required and password must be at least 6 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:           newID("usr"),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().Unix(),
	}
	_, err = s.db.Exec(
		`INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// VerifyAdmin verifies username and password and returns the matching user.
func (s *Store) VerifyAdmin(username, password string) (*User, error) {
	var u User
	err := s.db.QueryRow(
		`SELECT id, username, password_hash, created_at FROM users WHERE username = ?`,
		strings.TrimSpace(username),
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invalid credentials")
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}
	return &u, nil
}

// UpdatePassword updates an admin user's password hash.
func (s *Store) UpdatePassword(userID, newPassword string) error {
	if len(newPassword) < 6 {
		return errors.New("password must be at least 6 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID)
	return err
}

// CreateSession stores a new opaque session token valid for ttl.
func (s *Store) CreateSession(userID string, ttl time.Duration) (string, error) {
	tok := randomToken("sess_", 24)
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()
	_, err := s.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(tok), userID, exp, now,
	)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// GetSession resolves a raw session token if it exists and has not expired.
func (s *Store) GetSession(rawToken string) (*Session, error) {
	if rawToken == "" {
		return nil, sql.ErrNoRows
	}
	var sess Session
	err := s.db.QueryRow(
		`SELECT s.token, s.user_id, u.username, s.expires_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`,
		hashToken(rawToken), time.Now().Unix(),
	).Scan(&sess.Token, &sess.UserID, &sess.Username, &sess.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// DeleteSession invalidates a session token.
func (s *Store) DeleteSession(rawToken string) error {
	if rawToken == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, hashToken(rawToken))
	return err
}

// PruneSessions deletes expired sessions.
func (s *Store) PruneSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

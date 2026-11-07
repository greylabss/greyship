// Package auth implements sign-in with email and password (decisions.md 053), sessions, API tokens and the
// browser approval behind `grey login`.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/store"
)

const (
	sessionTTL     = 30 * 24 * time.Hour
	sessionRefresh = time.Hour
)

var (
	ErrInvalidEmail = errors.New("invalid email address")
	ErrNoSession    = errors.New("not signed in")
)

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many requests, retry after %s", e.RetryAfter)
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// ownerEmail, when set, is the only email that may create the first account (GREYSHIP_OWNER_EMAIL).
	ownerEmail string
}

func NewService(pool *pgxpool.Pool, now func() time.Time, ownerEmail string) *Service {
	return &Service{pool: pool, now: now, ownerEmail: strings.ToLower(strings.TrimSpace(ownerEmail))}
}

// NormalizeEmail lowercases and validates a bare address (no display name).
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func (s *Service) createSession(ctx context.Context, q *store.Queries, userID, userAgent string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	err = q.CreateSession(ctx, store.CreateSessionParams{UserID: userID, TokenHash: hashToken(token), UserAgent: userAgent, ExpiresAt: s.now().Add(sessionTTL)})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// SessionUser resolves a session cookie and slides its expiry at most once an hour.
func (s *Service) SessionUser(ctx context.Context, token string) (store.User, error) {
	if token == "" {
		return store.User{}, ErrNoSession
	}
	q := store.New(s.pool)
	now := s.now()
	sess, err := q.GetSession(ctx, store.GetSessionParams{TokenHash: hashToken(token), ExpiresAt: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, fmt.Errorf("load session: %w", err)
	}
	if now.Sub(sess.LastSeenAt) > sessionRefresh {
		if err := q.TouchSession(ctx, store.TouchSessionParams{ID: sess.ID, LastSeenAt: now, ExpiresAt: now.Add(sessionTTL)}); err != nil {
			return store.User{}, fmt.Errorf("refresh session: %w", err)
		}
	}
	user, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return store.User{}, fmt.Errorf("load session user: %w", err)
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := store.New(s.pool).DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Service) Teams(ctx context.Context, userID string) ([]store.ListUserTeamsRow, error) {
	teams, err := store.New(s.pool).ListUserTeams(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return teams, nil
}

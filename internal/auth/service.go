// Package auth implements passwordless login (decisions.md 023): emailed codes, Google and GitHub OAuth, and sessions.
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

	gsmail "greyship.tech/internal/mail"
	"greyship.tech/internal/store"
)

const (
	codeTTL        = 10 * time.Minute
	maxCodeTries   = 5
	emailCooldown  = 60 * time.Second
	ipWindow       = time.Hour
	maxCodesPerIP  = 10
	sessionTTL     = 30 * 24 * time.Hour
	sessionRefresh = time.Hour
)

var (
	ErrInvalidEmail   = errors.New("invalid email address")
	ErrInvalidCode    = errors.New("invalid or expired code")
	ErrNoSession      = errors.New("not signed in")
	ErrEmailNotProven = errors.New("the provider did not confirm this email address")
	ErrNotInvited     = errors.New("this email isn't invited yet")
	// ErrInviteCheck means the beta list couldn't be asked; a new account isn't made without it.
	ErrInviteCheck = errors.New("couldn't check the invite list")
)

// Allowlist limits who may create an account: exact emails or "@domain" entries. Empty lets everyone in.
// Existing users always sign in, whatever it says.
type Allowlist []string

// ParseAllowlist reads a comma-separated list, as in GREYSHIP_SIGNUP_ALLOWLIST.
func ParseAllowlist(raw string) Allowlist {
	var out Allowlist
	for _, e := range strings.Split(raw, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func (a Allowlist) allows(email string) bool {
	if len(a) == 0 {
		return true
	}
	at := email[strings.LastIndex(email, "@"):]
	for _, e := range a {
		if e == email || e == at {
			return true
		}
	}
	return false
}

// Testers is the private-beta list (internal/beta).
type Testers interface {
	IsTester(ctx context.Context, email string) (bool, error)
}

// Signup decides who may create an account (decisions.md 049, 050). The first account on an instance always may:
// it becomes the owner. After that nobody new may unless Open is set, the email is in the allowlist, or the beta
// list names it.
type Signup struct {
	// Open lets anyone sign up (GREYSHIP_SIGNUP_OPEN). Development and open instances only.
	Open    bool
	Allow   Allowlist
	Testers Testers // nil: no beta list
	// PlansOn gives new teams the Free plan; without it they get Unlimited (decisions.md 050).
	PlansOn bool
	// OwnerEmail may always sign up and is made an owner (GREYSHIP_OWNER_EMAIL).
	OwnerEmail string
}

// check reports whether email may sign up and whether it's a beta tester.
func (s Signup) check(ctx context.Context, email string) (allowed, tester bool, err error) {
	if s.Open || (s.OwnerEmail != "" && email == s.OwnerEmail) {
		return true, s.isTester(ctx, email), nil
	}
	if s.Testers == nil {
		return len(s.Allow) > 0 && s.Allow.allows(email), false, nil
	}
	tester, err = s.Testers.IsTester(ctx, email)
	listed := len(s.Allow) > 0 && s.Allow.allows(email)
	switch {
	case err != nil && listed:
		return true, false, nil
	case err != nil:
		return false, false, fmt.Errorf("%w: %w", ErrInviteCheck, err)
	}
	return tester || listed, tester, nil
}

// isTester asks the beta list, if there is one. A list that can't answer counts as no.
func (s Signup) isTester(ctx context.Context, email string) bool {
	if s.Testers == nil {
		return false
	}
	ok, err := s.Testers.IsTester(ctx, email)
	return err == nil && ok
}

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many requests, retry after %s", e.RetryAfter)
}

type Mailer interface {
	Send(ctx context.Context, m gsmail.Message) error
}

type Service struct {
	pool   *pgxpool.Pool
	mailer Mailer
	now    func() time.Time
	signup Signup
}

func NewService(pool *pgxpool.Pool, mailer Mailer, now func() time.Time, signup Signup) *Service {
	return &Service{pool: pool, mailer: mailer, now: now, signup: signup}
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

// StartEmail sends a login code. It behaves the same whether or not the account exists.
func (s *Service) StartEmail(ctx context.Context, rawEmail, ip string) error {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	q := store.New(s.pool)
	now := s.now()

	recent, err := q.CountCodesForEmailSince(ctx, store.CountCodesForEmailSinceParams{Email: email, CreatedAt: now.Add(-emailCooldown)})
	if err != nil {
		return fmt.Errorf("count codes for email: %w", err)
	}
	if recent > 0 {
		return &RateLimitError{RetryAfter: emailCooldown}
	}
	fromIP, err := q.CountCodesForIPSince(ctx, store.CountCodesForIPSinceParams{Ip: ip, CreatedAt: now.Add(-ipWindow)})
	if err != nil {
		return fmt.Errorf("count codes for ip: %w", err)
	}
	if fromIP >= maxCodesPerIP {
		return &RateLimitError{RetryAfter: ipWindow}
	}

	code, err := newCode()
	if err != nil {
		return err
	}
	if err := q.CreateLoginCode(ctx, store.CreateLoginCodeParams{Email: email, CodeHash: hashCode(email, code), Ip: ip, ExpiresAt: now.Add(codeTTL)}); err != nil {
		return fmt.Errorf("store login code: %w", err)
	}
	msg := gsmail.Message{
		To:      email,
		Subject: fmt.Sprintf("%s is your Greyship code", code),
		Text:    fmt.Sprintf("Your Greyship login code is %s\n\nIt expires in 10 minutes and works once.\nIf you didn't try to sign in, you can ignore this email.", code),
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send login code: %w", err)
	}
	return nil
}

// VerifyEmail checks a code and, on success, returns a new session token for the (possibly new) user.
func (s *Service) VerifyEmail(ctx context.Context, rawEmail, code, userAgent string) (string, store.User, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil || len(code) != codeDigits {
		return "", store.User{}, ErrInvalidCode
	}

	var token string
	var user store.User
	var wrong bool
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.LatestLoginCode(ctx, store.LatestLoginCodeParams{Email: email, ExpiresAt: s.now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return fmt.Errorf("load login code: %w", err)
		}
		attempts, err := q.RecordCodeAttempt(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("record code attempt: %w", err)
		}
		if attempts > maxCodeTries || !codeMatches(row.CodeHash, email, code) {
			// Commit so the failed attempt counts toward the limit.
			wrong = true
			return nil
		}
		if err := q.ConsumeLoginCode(ctx, store.ConsumeLoginCodeParams{ID: row.ID, ConsumedAt: ptr(s.now())}); err != nil {
			return fmt.Errorf("consume login code: %w", err)
		}
		if user, err = findOrCreateUser(ctx, q, s.signup, email, "", ""); err != nil {
			return err
		}
		token, err = s.createSession(ctx, q, user.ID, userAgent)
		return err
	})
	if err != nil {
		return "", store.User{}, err
	}
	if wrong {
		return "", store.User{}, ErrInvalidCode
	}
	return token, user, nil
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

func ptr[T any](v T) *T { return &v }

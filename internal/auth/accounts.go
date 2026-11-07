package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/store"
)

// Accounts have an email and a password. The first is made by whoever opens a new instance (Setup); the admin makes
// the rest (decisions.md 051, 053).

const (
	pbkdf2Iterations = 600_000 // OWASP's figure for PBKDF2-HMAC-SHA256
	minPasswordLen   = 10
	maxPasswordLen   = 128

	failuresPerEmail = 8
	failuresPerIP    = 30
	failureWindow    = 15 * time.Minute
)

var (
	ErrWeakPassword  = errors.New("a password needs 10 to 128 characters")
	ErrInvalidLogin  = errors.New("wrong email or password")
	ErrUserExists    = errors.New("an account with that email already exists")
	ErrLastAdmin     = errors.New("there must be at least one admin")
	ErrWrongPassword = errors.New("the current password is wrong")
	ErrNoUser        = errors.New("no such user")
	ErrSetupDone     = errors.New("this instance already has an owner")
	ErrNotOwnerEmail = errors.New("this instance only accepts its owner's email for the first account")
)

// hashPassword returns "pbkdf2-sha256$<iterations>$<salt>$<hash>", so the cost can change later without breaking old
// hashes.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("make salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	enc := base64.RawStdEncoding
	return "pbkdf2-sha256$" + strconv.Itoa(pbkdf2Iterations) + "$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(key), nil
}

func passwordMatches(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func checkPassword(password string) error {
	if n := utf8.RuneCountInString(password); n < minPasswordLen || n > maxPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// LoginPassword signs in an account that has a password. Wrong email and wrong password look the same, cost the same
// time, and count toward the same limits.
func (s *Service) LoginPassword(ctx context.Context, rawEmail, password, ip, userAgent string) (string, store.User, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return "", store.User{}, ErrInvalidLogin
	}
	q := store.New(s.pool)
	since := s.now().Add(-failureWindow)
	byEmail, err := q.CountPasswordFailuresForEmail(ctx, store.CountPasswordFailuresForEmailParams{Email: email, CreatedAt: since})
	if err != nil {
		return "", store.User{}, fmt.Errorf("count failures for email: %w", err)
	}
	byIP, err := q.CountPasswordFailuresForIP(ctx, store.CountPasswordFailuresForIPParams{Ip: ip, CreatedAt: since})
	if err != nil {
		return "", store.User{}, fmt.Errorf("count failures for ip: %w", err)
	}
	if byEmail >= failuresPerEmail || byIP >= failuresPerIP {
		return "", store.User{}, &RateLimitError{RetryAfter: failureWindow}
	}

	user, err := q.GetUserByEmail(ctx, email)
	known := err == nil && user.PasswordHash != ""
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", store.User{}, fmt.Errorf("load user: %w", err)
	}
	ok := false
	if known {
		ok = passwordMatches(user.PasswordHash, password)
	} else if _, herr := hashPassword(password); herr != nil { // the same work, so timing doesn't say which emails exist
		return "", store.User{}, herr
	}
	if !ok {
		if err := q.RecordPasswordFailure(ctx, store.RecordPasswordFailureParams{Email: email, Ip: ip}); err != nil {
			return "", store.User{}, fmt.Errorf("record failure: %w", err)
		}
		return "", store.User{}, ErrInvalidLogin
	}
	if err := q.ClearPasswordFailures(ctx, email); err != nil {
		return "", store.User{}, fmt.Errorf("clear failures: %w", err)
	}
	token, err := s.createSession(ctx, q, user.ID, userAgent)
	if err != nil {
		return "", store.User{}, err
	}
	return token, user, nil
}

// NewUser is an account an admin creates.
type NewUser struct {
	Email    string
	Name     string
	Password string
	Admin    bool
}

// CreateUser makes an account with its own personal team. Callers check that the requester is an admin.
func (s *Service) CreateUser(ctx context.Context, in NewUser) (store.User, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return store.User{}, err
	}
	var user store.User
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		user, err = s.createUser(ctx, store.New(tx), email, in)
		return err
	})
	return user, err
}

func (s *Service) createUser(ctx context.Context, q *store.Queries, email string, in NewUser) (store.User, error) {
	name := strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(name) > 80 {
		return store.User{}, errors.New("a name is at most 80 characters")
	}
	if err := checkPassword(in.Password); err != nil {
		return store.User{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return store.User{}, err
	}
	if _, err := q.GetUserByEmail(ctx, email); err == nil {
		return store.User{}, ErrUserExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, fmt.Errorf("load user: %w", err)
	}
	user, err := createAccount(ctx, q, email, name)
	if err != nil {
		return store.User{}, err
	}
	if err := q.SetPasswordHash(ctx, store.SetPasswordHashParams{ID: user.ID, PasswordHash: hash}); err != nil {
		return store.User{}, fmt.Errorf("set password: %w", err)
	}
	if in.Admin {
		if err := q.SetOwner(ctx, store.SetOwnerParams{ID: user.ID, IsOwner: true}); err != nil {
			return store.User{}, fmt.Errorf("make admin: %w", err)
		}
		user.IsOwner = true
	}
	return user, nil
}

// SetPassword changes your own password; the current one must match.
func (s *Service) SetPassword(ctx context.Context, userID, current, next string) error {
	if err := checkPassword(next); err != nil {
		return err
	}
	q := store.New(s.pool)
	user, err := q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoUser
	}
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if !passwordMatches(user.PasswordHash, current) {
		return ErrWrongPassword
	}
	return s.storePassword(ctx, q, userID, next)
}

// AdminSetPassword sets someone's password without the old one (an admin resetting it). Callers check the requester.
func (s *Service) AdminSetPassword(ctx context.Context, userID, next string) error {
	if err := checkPassword(next); err != nil {
		return err
	}
	q := store.New(s.pool)
	if _, err := q.GetUserByID(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNoUser
	} else if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	return s.storePassword(ctx, q, userID, next)
}

func (s *Service) storePassword(ctx context.Context, q *store.Queries, userID, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := q.SetPasswordHash(ctx, store.SetPasswordHashParams{ID: userID, PasswordHash: hash}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// SetAdmin makes someone an admin or takes it away. The last admin can't be removed.
func (s *Service) SetAdmin(ctx context.Context, userID string, admin bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		user, err := q.GetUserByID(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if !admin && user.IsOwner {
			if n, err := q.CountOwners(ctx); err != nil {
				return fmt.Errorf("count admins: %w", err)
			} else if n <= 1 {
				return ErrLastAdmin
			}
		}
		return q.SetOwner(ctx, store.SetOwnerParams{ID: userID, IsOwner: admin})
	})
}

// NeedsSetup reports whether the instance has no accounts yet, so its first visitor creates the owner.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := store.New(s.pool).UserCount(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return n == 0, nil
}

// Setup creates the owner account of a new instance and signs it in. It works once: after the first account
// exists, only an admin makes accounts.
func (s *Service) Setup(ctx context.Context, in NewUser, userAgent string) (string, store.User, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return "", store.User{}, err
	}
	if s.ownerEmail != "" && email != s.ownerEmail {
		return "", store.User{}, ErrNotOwnerEmail
	}
	var token string
	var user store.User
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Two people opening a new instance at once: the second waits here and then sees an owner.
		if _, err := tx.Exec(ctx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		n, err := q.UserCount(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if n > 0 {
			return ErrSetupDone
		}
		in.Admin = true
		if user, err = s.createUser(ctx, q, email, in); err != nil {
			return err
		}
		token, err = s.createSession(ctx, q, user.ID, userAgent)
		return err
	})
	if err != nil {
		return "", store.User{}, err
	}
	return token, user, nil
}

// ResetPassword gives an account a new random password and signs it out everywhere. It is for the server's
// operator (greyship-admin reset-password), who proves who they are by having shell access.
func (s *Service) ResetPassword(ctx context.Context, rawEmail string) (string, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return "", err
	}
	q := store.New(s.pool)
	user, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoUser
	}
	if err != nil {
		return "", fmt.Errorf("load user: %w", err)
	}
	password, err := RandomPassword()
	if err != nil {
		return "", err
	}
	if err := s.storePassword(ctx, q, user.ID, password); err != nil {
		return "", err
	}
	if err := q.DeleteUserSessions(ctx, user.ID); err != nil {
		return "", fmt.Errorf("end sessions: %w", err)
	}
	if err := q.ClearPasswordFailures(ctx, email); err != nil {
		return "", fmt.Errorf("clear failures: %w", err)
	}
	return password, nil
}

// RandomPassword is 20 characters from an alphabet without look-alikes.
func RandomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random password: %w", err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

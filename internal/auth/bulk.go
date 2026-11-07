package auth

import (
	"context"
	"errors"
)

// BulkResult is what happened to one email in CreateMany.
type BulkResult struct {
	Email string
	// Created is true for a new account. Err is nil then, ErrUserExists for an account that was already there, or
	// why the email or password was refused.
	Created bool
	Err     error
}

// CreateMany makes an account for each email, all with the same password (the admin tells people to change it).
// A refused email doesn't stop the rest. Callers check that the requester is an admin.
func (s *Service) CreateMany(ctx context.Context, password string, emails []string) []BulkResult {
	out := make([]BulkResult, 0, len(emails))
	for _, email := range emails {
		_, err := s.CreateUser(ctx, NewUser{Email: email, Password: password})
		out = append(out, BulkResult{Email: email, Created: err == nil, Err: err})
		if err != nil && !errors.Is(err, ErrUserExists) && !errors.Is(err, ErrInvalidEmail) && !errors.Is(err, ErrWeakPassword) {
			break // something is wrong with the database, not with an address
		}
	}
	return out
}

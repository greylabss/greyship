package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"greyship.tech/internal/slug"
	"greyship.tech/internal/store"
)

// createAccount makes a user with a personal team they own.
func createAccount(ctx context.Context, q *store.Queries, email, name string) (store.User, error) {
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: email, Name: name})
	if err != nil {
		return store.User{}, fmt.Errorf("create user: %w", err)
	}
	slug, err := freeSlug(ctx, q, slugFrom(email))
	if err != nil {
		return store.User{}, err
	}
	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: slug, Name: slug})
	if err != nil {
		return store.User{}, fmt.Errorf("create personal team: %w", err)
	}
	if err := q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: user.ID, Role: "owner"}); err != nil {
		return store.User{}, fmt.Errorf("add team owner: %w", err)
	}
	return user, nil
}

// Team slugs are the first path segment of dashboard URLs (/<team>) and appear in default domains,
// so words taken by site pages or subdomains stay unavailable.
var reservedSlugs = map[string]bool{
	"admin": true, "api": true, "app": true, "www": true,
	"new": true, "settings": true, "billing": true, "usage": true, "deployments": true,
	"login": true, "signin": true, "signup": true, "device": true, "maintenance": true, "continue": true, "v1": true,
	"blog": true, "features": true, "pricing": true, "but": true,
}

// slugFrom turns the local part of an email into a team slug: lowercase letters, digits and dashes.
func slugFrom(email string) string {
	local, _, _ := strings.Cut(email, "@")
	local, _, _ = strings.Cut(local, "+")
	s := slug.Make(local, 30)
	if s == "" || reservedSlugs[s] || s[0] >= '0' && s[0] <= '9' {
		s = strings.TrimRight("team-"+s, "-")
	}
	return s
}

func freeSlug(ctx context.Context, q *store.Queries, base string) (string, error) {
	for i := 1; i <= 20; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		taken, err := q.TeamSlugTaken(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("check team slug: %w", err)
		}
		if !taken {
			return candidate, nil
		}
	}
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate slug suffix: %w", err)
	}
	return base + "-" + hex.EncodeToString(b), nil
}

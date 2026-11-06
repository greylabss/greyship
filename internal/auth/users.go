package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/plan"
	"greyship.tech/internal/slug"
	"greyship.tech/internal/store"
)

// findOrCreateUser returns the user for an email, creating them with a personal team on first login if signup
// lets them in. Beta testers get their badge at signup, or at a later login if they joined the list afterwards.
func findOrCreateUser(ctx context.Context, q *store.Queries, signup Signup, email, name, avatarURL string) (store.User, error) {
	user, err := q.GetUserByEmail(ctx, email)
	if err == nil {
		if name != "" || avatarURL != "" {
			if err := q.FillUserProfile(ctx, store.FillUserProfileParams{ID: user.ID, Name: name, AvatarUrl: avatarURL}); err != nil {
				return store.User{}, fmt.Errorf("update profile: %w", err)
			}
		}
		if !user.BetaTester && signup.Testers != nil {
			// Best effort: the badge can wait for the next login, signing in can't.
			if tester, err := signup.Testers.IsTester(ctx, email); err == nil && tester {
				if err := q.MarkBetaTester(ctx, user.ID); err != nil {
					return store.User{}, fmt.Errorf("mark beta tester: %w", err)
				}
				user.BetaTester = true
			}
		}
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, fmt.Errorf("load user: %w", err)
	}
	// With no owner email configured, the first account on an instance may always sign up and owns it. With one,
	// only that email may (it passes the check below), so nobody can claim an instance before its owner does.
	n, err := q.UserCount(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("count users: %w", err)
	}
	first := n == 0 && signup.OwnerEmail == ""
	tester := false
	if first {
		tester = signup.isTester(ctx, email) // only for the badge; the first account is let in either way
	} else {
		allowed, t, err := signup.check(ctx, email)
		if err != nil {
			return store.User{}, err
		}
		if !allowed {
			return store.User{}, ErrNotInvited
		}
		tester = t
	}

	user, err = q.CreateUser(ctx, store.CreateUserParams{Email: email, Name: name, AvatarUrl: avatarURL})
	if err != nil {
		return store.User{}, fmt.Errorf("create user: %w", err)
	}
	if tester {
		if err := q.MarkBetaTester(ctx, user.ID); err != nil {
			return store.User{}, fmt.Errorf("mark beta tester: %w", err)
		}
		user.BetaTester = true
	}
	if signup.OwnerEmail != "" && email == signup.OwnerEmail {
		if err := q.MakeOwnerByEmail(ctx, email); err != nil {
			return store.User{}, fmt.Errorf("make owner: %w", err)
		}
		user.IsOwner = true
	} else if first {
		made, err := q.MakeOwnerIfFirst(ctx, user.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return store.User{}, fmt.Errorf("make owner: %w", err)
		}
		user.IsOwner = made
	}
	slug, err := freeSlug(ctx, q, slugFrom(email))
	if err != nil {
		return store.User{}, err
	}
	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: slug, Name: slug, Plan: plan.Default(signup.PlansOn)})
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

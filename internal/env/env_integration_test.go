//go:build integration

package env_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"greyship.tech/internal/env"
	"greyship.tech/internal/project"
	"greyship.tech/internal/secrets"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

func TestEnvVars(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	member := func(email, role string) project.Caller {
		u, err := q.CreateUser(ctx, store.CreateUserParams{Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: role}); err != nil {
			t.Fatal(err)
		}
		return project.Caller{UserID: u.ID}
	}
	owner, viewer := member("owner@acme.test", "owner"), member("viewer@acme.test", "viewer")
	projects := project.NewService(pool)
	p, err := projects.Create(ctx, owner, "acme", "shop")
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := secrets.NewKeyring("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	svc := env.NewService(pool, keyring, projects)

	change, err := svc.Apply(ctx, owner, p.ID, "production", map[string]string{"DATABASE_URL": "postgres://secret", "STRIPE_KEY": "sk_1"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Added) != 2 {
		t.Fatalf("change = %+v", change)
	}
	// Setting the same value again is not a change; a new value is.
	change, _ = svc.Apply(ctx, owner, p.ID, "production", map[string]string{"DATABASE_URL": "postgres://secret", "STRIPE_KEY": "sk_2"}, nil, true)
	if len(change.Added) != 0 || len(change.Updated) != 1 || change.Updated[0] != "STRIPE_KEY" {
		t.Fatalf("second change = %+v", change)
	}

	var stored []byte
	if err := pool.QueryRow(ctx, "SELECT ciphertext FROM env_vars WHERE key = 'DATABASE_URL'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("postgres://secret")) {
		t.Fatal("a value is stored in plain text")
	}

	vars, err := svc.List(ctx, viewer, p.ID, "production")
	if err != nil || len(vars) != 2 || vars[0].Hash != secrets.Hash(p.ID, "DATABASE_URL", "postgres://secret") {
		t.Fatalf("viewer list = %+v, %v", vars, err)
	}
	if _, err := svc.Values(ctx, viewer, p.ID, "production"); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("viewer pull = %v, want ErrReadOnly: viewers see keys, not secrets", err)
	}
	values, err := svc.Values(ctx, owner, p.ID, "production")
	if err != nil || values["STRIPE_KEY"] != "sk_2" {
		t.Fatalf("owner pull = %v, %v", values, err)
	}

	for _, bad := range []string{"PORT", "GREYSHIP_SERVICE", "1BAD", "with space"} {
		var ke *env.KeyError
		if _, err := svc.Apply(ctx, owner, p.ID, "production", map[string]string{bad: "x"}, nil, true); !errors.As(err, &ke) {
			t.Errorf("setting %q = %v, want a KeyError", bad, err)
		}
	}

	var restarts int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'env.changed'").Scan(&restarts)
	if restarts != 2 {
		t.Fatalf("env.changed jobs = %d, want one per change", restarts)
	}
	if _, err := svc.Apply(ctx, owner, p.ID, "production", map[string]string{"NEW": "1"}, nil, false); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'env.changed'").Scan(&restarts)
	if restarts != 2 {
		t.Fatal("restart=false (a deploy) must not ask for a separate restart")
	}

	// Effective view: main minus exclusions, overrides win, never another service's overrides (F-44–F-47).
	api, _ := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: p.ID, Name: "api", Path: "./cmd/api", Type: "web"})
	payments, _ := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: p.ID, Name: "payments", Path: "./cmd/payments", Type: "web"})
	pk, _ := q.GetProjectKey(ctx, p.ID)
	key, _ := keyring.ProjectKey(p.ID, pk)
	override, _ := key.Encrypt("production", "STRIPE_KEY", "sk_payments_only")
	if _, err := pool.Exec(ctx, "INSERT INTO env_vars (project_id, service_id, env, key, ciphertext, value_hash) VALUES ($1, $2, 'production', 'STRIPE_KEY', $3, $4)", p.ID, payments.ID, override, secrets.Hash(p.ID, "STRIPE_KEY", "sk_payments_only")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO env_excludes (service_id, env, key) VALUES ($1, 'production', 'DATABASE_URL')", payments.ID); err != nil {
		t.Fatal(err)
	}
	apiEnv, err := svc.Effective(ctx, p.ID, api.ID, "production")
	if err != nil || apiEnv["STRIPE_KEY"] != "sk_2" || apiEnv["DATABASE_URL"] != "postgres://secret" {
		t.Fatalf("api env = %v, %v", apiEnv, err)
	}
	payEnv, _ := svc.Effective(ctx, p.ID, payments.ID, "production")
	if payEnv["STRIPE_KEY"] != "sk_payments_only" {
		t.Fatalf("payments override lost: %v", payEnv)
	}
	if _, ok := payEnv["DATABASE_URL"]; ok {
		t.Fatal("an excluded key reached the service")
	}
}

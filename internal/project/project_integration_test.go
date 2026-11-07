//go:build integration

package project_test

import (
	"context"
	"errors"
	"testing"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type member struct {
	userID string
	teamID string
}

func newTeam(t *testing.T, q *store.Queries, slug string) string {
	t.Helper()
	team, err := q.CreateTeam(context.Background(), store.CreateTeamParams{Slug: slug, Name: slug})
	if err != nil {
		t.Fatal(err)
	}
	return team.ID
}

func addMember(t *testing.T, q *store.Queries, email, teamID, role string) member {
	t.Helper()
	ctx := context.Background()
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: email})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddMember(ctx, store.AddMemberParams{TeamID: teamID, UserID: u.ID, Role: role}); err != nil {
		t.Fatal(err)
	}
	return member{userID: u.ID, teamID: teamID}
}

func TestProjects(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	svc := project.NewService(pool)

	acme, other := newTeam(t, q, "acme"), newTeam(t, q, "other")
	owner := addMember(t, q, "owner@acme.test", acme, "owner")
	viewer := addMember(t, q, "viewer@acme.test", acme, "viewer")
	outsider := addMember(t, q, "owner@other.test", other, "owner")
	asOwner := project.Caller{UserID: owner.userID}

	created, err := svc.Create(ctx, asOwner, "acme", "Acme API")
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "acme-api" || created.Name != "Acme API" || created.TeamSlug != "acme" {
		t.Fatalf("created = %+v", created)
	}

	var taken *project.TakenError
	if _, err := svc.Create(ctx, asOwner, "acme", "acme api"); !errors.As(err, &taken) || taken.Existing.ID != created.ID {
		t.Fatalf("duplicate create err = %v, want TakenError pointing at %s", err, created.ID)
	}
	if _, err := svc.Create(ctx, project.Caller{UserID: outsider.userID}, "other", "Acme API"); err != nil {
		t.Fatalf("same slug in another team should work: %v", err)
	}

	list, err := svc.List(ctx, project.Caller{UserID: viewer.userID}, "acme")
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("viewer list = %+v, %v", list, err)
	}

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"viewer can't create", func() error {
			_, err := svc.Create(ctx, project.Caller{UserID: viewer.userID}, "acme", "Payments")
			return err
		}, project.ErrReadOnly},
		{"viewer can't rename", func() error {
			_, err := svc.Rename(ctx, project.Caller{UserID: viewer.userID}, created.ID, "Payments")
			return err
		}, project.ErrReadOnly},
		{"outsider can't list", func() error {
			_, err := svc.List(ctx, project.Caller{UserID: outsider.userID}, "acme")
			return err
		}, project.ErrNotFound},
		{"outsider can't get", func() error {
			_, err := svc.Get(ctx, project.Caller{UserID: outsider.userID}, created.ID)
			return err
		}, project.ErrNotFound},
		{"bad id", func() error {
			_, err := svc.Get(ctx, asOwner, "not-a-uuid")
			return err
		}, project.ErrNotFound},
		{"token for another team", func() error {
			_, err := svc.Get(ctx, project.Caller{UserID: owner.userID, TokenTeamID: outsider.teamID}, created.ID)
			return err
		}, project.ErrWrongTeam},
		{"token for another team can't create", func() error {
			_, err := svc.Create(ctx, project.Caller{UserID: owner.userID, TokenTeamID: outsider.teamID}, "acme", "Payments")
			return err
		}, project.ErrWrongTeam},
		{"token for own team", func() error {
			_, err := svc.Get(ctx, project.Caller{UserID: owner.userID, TokenTeamID: owner.teamID}, created.ID)
			return err
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	renamed, err := svc.Rename(ctx, asOwner, created.ID, "Acme Orders")
	if err != nil || renamed.ID != created.ID || renamed.Slug != "acme-orders" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if _, err := svc.Create(ctx, asOwner, "acme", "Payments"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rename(ctx, asOwner, created.ID, "payments"); !errors.As(err, &taken) {
		t.Fatalf("rename onto a taken slug err = %v, want TakenError", err)
	}
}

// A deleted project disappears at once and a project.delete job finishes the work; only a member who can write may
// ask, and only with the project's slug.
func TestDeleteProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	svc := project.NewService(pool)
	acme, other := newTeam(t, q, "acme"), newTeam(t, q, "other")
	owner := addMember(t, q, "owner@acme.test", acme, "owner")
	viewer := addMember(t, q, "viewer@acme.test", acme, "viewer")
	outsider := addMember(t, q, "owner@other.test", other, "owner")
	asOwner := project.Caller{UserID: owner.userID}

	p, err := svc.Create(ctx, asOwner, "acme", "Acme API")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		who     project.Caller
		confirm string
		want    error
	}{
		"viewer":             {project.Caller{UserID: viewer.userID}, p.Slug, project.ErrReadOnly},
		"outsider":           {project.Caller{UserID: outsider.userID}, p.Slug, project.ErrNotFound},
		"another team token": {project.Caller{UserID: owner.userID, TokenTeamID: other}, p.Slug, project.ErrWrongTeam},
		"wrong slug":         {asOwner, "acme-apo", project.ErrConfirm},
		"not a project":      {asOwner, "x", project.ErrNotFound},
	} {
		id := p.ID
		if name == "not a project" {
			id = "00000000-0000-4000-8000-000000000000"
		}
		if err := svc.Delete(ctx, tc.who, id, tc.confirm); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	var jobs int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'project.delete'").Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("a refused delete queued %d jobs", jobs)
	}

	if err := svc.Delete(ctx, asOwner, p.ID, p.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, asOwner, p.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("a deleted project still loads: %v", err)
	}
	if list, _ := svc.List(ctx, asOwner, "acme"); len(list) != 0 {
		t.Fatalf("a deleted project is still listed: %+v", list)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'project.delete' AND payload->>'project_id' = $1", p.ID).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("delete jobs = %d, want 1", jobs)
	}
	if err := svc.Delete(ctx, asOwner, p.ID, p.Slug); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleting twice = %v, want ErrNotFound", err)
	}
}

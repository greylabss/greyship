//go:build integration

package domains_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"greyship.tech/internal/domains"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type dns map[string][]string

func (d dns) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := d[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func (d dns) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	if h, ok := d["NS "+name]; ok {
		return []*net.NS{{Host: h[0]}}, nil
	}
	return nil, errors.New("no such zone")
}

func (d dns) LookupTXT(context.Context, string) ([]string, error) { return nil, errors.New("none") }

func TestCustomDomainsOfAProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	other, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "other", Name: "other"})
	owner, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "o@example.com"})
	viewer, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "v@example.com"})
	stranger, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "s@example.com"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: owner.ID, Role: "owner"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: viewer.ID, Role: "viewer"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: other.ID, UserID: stranger.ID, Role: "owner"})
	projects := project.NewService(pool)
	asOwner := project.Caller{UserID: owner.ID}
	p, err := projects.Create(ctx, asOwner, "acme", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	svc := &domains.Service{Pool: pool, Projects: projects, AppsDomain: "greyshipapp.tech", PlatformDomain: "app.greyship.tech",
		Resolver: dns{"domain-check.greyshipapp.tech": {"203.0.113.7"}, "shop.example.com": {"203.0.113.7"}}}

	// A project that was never deployed has no web service to point at.
	if _, err := svc.Add(ctx, asOwner, p.ID, "shop.example.com", ""); !errors.Is(err, domains.ErrNoWebService) {
		t.Fatalf("add before any deploy: %v", err)
	}
	api, _ := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: p.ID, Name: "api", Path: "./cmd/api", Type: "web"})
	web, _ := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: p.ID, Name: "web", Path: "./cmd/web", Type: "web"})
	for id, sub := range map[string]string{api.ID: "api-shop", web.ID: "web-shop"} {
		if _, err := q.SetServiceSubdomain(ctx, store.SetServiceSubdomainParams{ID: id, Subdomain: &sub}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.Add(ctx, asOwner, p.ID, "shop.example.com", ""); !errors.Is(err, domains.ErrServiceRequired) {
		t.Fatalf("two web services and no choice: %v", err)
	}
	if _, err := svc.Add(ctx, asOwner, p.ID, "shop.example.com", "nope"); !errors.Is(err, domains.ErrUnknownService) {
		t.Fatalf("an unknown service: %v", err)
	}
	if _, err := svc.Add(ctx, project.Caller{UserID: viewer.ID}, p.ID, "shop.example.com", "api"); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("a viewer adds a domain: %v", err)
	}
	if _, err := svc.Add(ctx, project.Caller{UserID: stranger.ID}, p.ID, "shop.example.com", "api"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("an outsider adds a domain: %v", err)
	}
	var invalid *domains.InvalidError
	if _, err := svc.Add(ctx, asOwner, p.ID, "https://shop.example.com", "api"); !errors.As(err, &invalid) {
		t.Fatalf("a URL: %v", err)
	}
	if _, err := svc.Add(ctx, asOwner, p.ID, "api-shop.greyshipapp.tech", "api"); !errors.Is(err, domains.ErrReserved) {
		t.Fatalf("a name under the apps domain: %v", err)
	}

	e, err := svc.Add(ctx, asOwner, p.ID, "Shop.Example.com", "api")
	if err != nil || e.Hostname != "shop.example.com" || e.Service != "api" || e.DNS != domains.Pointing {
		t.Fatalf("add = %+v, %v", e, err)
	}
	if _, err := svc.Add(ctx, asOwner, p.ID, "shop.example.com", "web"); !errors.Is(err, domains.ErrTaken) {
		t.Fatalf("a hostname already in use: %v", err)
	}
	if len(e.Records) != 1 || e.Records[0] != (domains.Record{Type: "CNAME", Name: "shop", Value: "api-shop.greyshipapp.tech"}) {
		t.Fatalf("records = %+v", e.Records)
	}
	v, err := svc.Verify(ctx, asOwner, p.ID, "Shop.Example.com.")
	if err != nil || v.DNS != domains.Pointing {
		t.Fatalf("verify = %+v, %v", v, err)
	}
	if _, err := svc.Verify(ctx, project.Caller{UserID: viewer.ID}, p.ID, "shop.example.com"); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("a viewer verifies: %v", err)
	}
	if _, err := svc.Verify(ctx, project.Caller{UserID: stranger.ID}, p.ID, "shop.example.com"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("an outsider verifies: %v", err)
	}
	if _, err := svc.Verify(ctx, asOwner, p.ID, "nope.example.com"); !errors.Is(err, domains.ErrNotFound) {
		t.Fatalf("verifying what isn't there: %v", err)
	}
	if _, err := svc.Connect(ctx, asOwner, p.ID, "shop.example.com"); !errors.Is(err, domains.ErrNoAutoSetup) {
		t.Fatalf("connect without a _domainconnect record: %v", err)
	}

	// The routers load it, as the label of the service it points at.
	rows, err := q.RoutingDomains(ctx)
	if err != nil || len(rows) != 1 || rows[0].Hostname != "shop.example.com" || rows[0].Subdomain != "api-shop" {
		t.Fatalf("routing domains = %+v %v", rows, err)
	}

	list, err := svc.List(ctx, asOwner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, en := range list {
		seen = append(seen, en.Hostname+"/"+en.Service)
	}
	if got := strings.Join(seen, " "); got != "api-shop.greyshipapp.tech/api web-shop.greyshipapp.tech/web shop.example.com/api" {
		t.Fatalf("list = %s", got)
	}
	if list, err := svc.List(ctx, project.Caller{UserID: viewer.ID}, p.ID); err != nil || len(list) != 3 {
		t.Fatalf("a viewer may look: %d %v", len(list), err)
	}

	if err := svc.Remove(ctx, project.Caller{UserID: viewer.ID}, p.ID, "shop.example.com"); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("a viewer removes a domain: %v", err)
	}
	if err := svc.Remove(ctx, asOwner, p.ID, "nope.example.com"); !errors.Is(err, domains.ErrNotFound) {
		t.Fatalf("removing what isn't there: %v", err)
	}
	if err := svc.Remove(ctx, asOwner, p.ID, "Shop.Example.com."); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.List(ctx, asOwner, p.ID); len(list) != 2 {
		t.Fatalf("after removal: %d entries", len(list))
	}

	// Deleting a service or the project takes its hostnames with it.
	if _, err := svc.Add(ctx, asOwner, p.ID, "again.example.com", "web"); err != nil {
		t.Fatal(err)
	}
	if err := projects.Delete(ctx, asOwner, p.ID, p.Slug); err != nil {
		t.Fatal(err)
	}
	if rows, _ := q.RoutingDomains(ctx); len(rows) != 0 {
		t.Fatalf("a deleted project's hostname is still routed: %+v", rows)
	}
}

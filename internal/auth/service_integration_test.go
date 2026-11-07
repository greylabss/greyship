//go:build integration

package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/testdb"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func setup(t *testing.T) (*auth.Service, *clock, *pgxpool.Pool) {
	pool := testdb.New(t)
	clk := &clock{t: time.Now()}
	return auth.NewService(pool, clk.now, ""), clk, pool
}

func signedInUser(t *testing.T, svc *auth.Service, email string) string {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), auth.NewUser{Email: email, Password: "a long enough password"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// The first account is made by whoever opens a new instance, once; after that only an admin makes accounts.
func TestSetupCreatesTheOwnerOnce(t *testing.T) {
	ctx := context.Background()
	svc, _, pool := setup(t)
	if need, err := svc.NeedsSetup(ctx); err != nil || !need {
		t.Fatalf("a new instance needs setup: %v %v", need, err)
	}
	if _, _, err := svc.Setup(ctx, auth.NewUser{Email: "boss@example.com", Password: "short"}, "test"); !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("weak password = %v", err)
	}
	token, owner, err := svc.Setup(ctx, auth.NewUser{Email: "Boss@Example.com", Name: "Boss", Password: "a long enough password"}, "test")
	if err != nil || token == "" || !owner.IsOwner || owner.Email != "boss@example.com" {
		t.Fatalf("setup = %q %+v %v", token, owner, err)
	}
	if got, err := svc.SessionUser(ctx, token); err != nil || got.ID != owner.ID {
		t.Fatalf("setup should sign the owner in: %+v %v", got, err)
	}
	if need, _ := svc.NeedsSetup(ctx); need {
		t.Fatal("setup is still offered after the first account")
	}
	if _, _, err := svc.Setup(ctx, auth.NewUser{Email: "intruder@example.com", Password: "a long enough password"}, "test"); !errors.Is(err, auth.ErrSetupDone) {
		t.Fatalf("a second setup = %v, want ErrSetupDone", err)
	}
	var teams, users int
	_ = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM teams), (SELECT count(*) FROM users)").Scan(&teams, &users)
	if teams != 1 || users != 1 {
		t.Fatalf("teams = %d, users = %d; want 1 and 1", teams, users)
	}
}

func TestSetupOnlyForTheOwnerEmail(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := auth.NewService(pool, time.Now, "Boss@Example.com")
	if _, _, err := svc.Setup(ctx, auth.NewUser{Email: "stranger@example.com", Password: "a long enough password"}, "test"); !errors.Is(err, auth.ErrNotOwnerEmail) {
		t.Fatalf("a stranger claiming the instance = %v", err)
	}
	if _, owner, err := svc.Setup(ctx, auth.NewUser{Email: "boss@example.com", Password: "a long enough password"}, "test"); err != nil || !owner.IsOwner {
		t.Fatalf("the owner's own email = %+v %v", owner, err)
	}
}

func TestUserLimits(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := setup(t)
	u, err := svc.CreateUser(ctx, auth.NewUser{Email: "beta@example.com", Password: "a long enough password"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Usage(ctx, u.ID)
	if err != nil || got.MemoryMB != nil || got.CPUMillis != nil || got.ConcurrentBuilds != nil {
		t.Fatalf("a new user has no limits: %+v %v", got, err)
	}
	mem, cpu, builds := int32(512), int32(500), int32(1)
	if err := svc.SetLimits(ctx, u.ID, auth.Limits{MemoryMB: &mem, CPUMillis: &cpu, ConcurrentBuilds: &builds}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Usage(ctx, u.ID)
	if got.MemoryMB == nil || *got.MemoryMB != 512 || *got.CPUMillis != 500 || *got.ConcurrentBuilds != 1 {
		t.Fatalf("limits = %+v", got)
	}
	if err := svc.SetLimits(ctx, u.ID, auth.Limits{MemoryMB: &mem}); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Usage(ctx, u.ID); got.CPUMillis != nil || got.ConcurrentBuilds != nil {
		t.Fatalf("setting limits replaces all three, so the others are cleared: %+v", got)
	}
	zero := int32(0)
	if err := svc.SetLimits(ctx, u.ID, auth.Limits{MemoryMB: &zero}); !errors.Is(err, auth.ErrInvalidLimits) {
		t.Fatalf("a zero limit = %v", err)
	}
	if err := svc.SetLimits(ctx, "00000000-0000-4000-8000-000000000000", auth.Limits{}); !errors.Is(err, auth.ErrNoUser) {
		t.Fatalf("limits for nobody = %v", err)
	}
}

// A forgotten password is recovered on the server: a new random password, and every session ends.
func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := setup(t)
	token, owner, err := svc.Setup(ctx, auth.NewUser{Email: "boss@example.com", Password: "a long enough password"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		_, _, _ = svc.LoginPassword(ctx, "boss@example.com", "wrong wrong wrong", "10.0.0.9", "test")
	}
	password, err := svc.ResetPassword(ctx, "BOSS@example.com")
	if err != nil || len(password) != 20 {
		t.Fatalf("reset = %q, %v", password, err)
	}
	if _, err := svc.SessionUser(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("the old session survived a reset: %v", err)
	}
	if _, _, err := svc.LoginPassword(ctx, "boss@example.com", "a long enough password", "10.0.0.8", "test"); !errors.Is(err, auth.ErrInvalidLogin) {
		t.Fatalf("the old password still works: %v", err)
	}
	if _, u, err := svc.LoginPassword(ctx, "boss@example.com", password, "10.0.0.8", "test"); err != nil || u.ID != owner.ID {
		t.Fatalf("login with the new password (a reset also clears the lockout): %v", err)
	}
	if _, err := svc.ResetPassword(ctx, "nobody@example.com"); !errors.Is(err, auth.ErrNoUser) {
		t.Fatalf("reset of nobody = %v", err)
	}
}

func TestAdminCreatedAccounts(t *testing.T) {
	pool := testdb.New(t)
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	svc := auth.NewService(pool, clk.now, "")

	admin, err := svc.CreateUser(ctx, auth.NewUser{Email: "Boss@Example.com", Name: "Boss", Password: "a long enough password", Admin: true})
	if err != nil || !admin.IsOwner || admin.Email != "boss@example.com" {
		t.Fatalf("create admin = %+v, %v", admin, err)
	}
	beta, err := svc.CreateUser(ctx, auth.NewUser{Email: "beta@example.com", Name: "beta", Password: "greylabs-greylabs"})
	if err != nil || beta.IsOwner {
		t.Fatalf("create user = %+v, %v; want a non-admin", beta, err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "beta@example.com", Password: "another long password"}); !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "x@example.com", Password: "short"}); !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("weak password = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "not an email", Password: "a long enough password"}); !errors.Is(err, auth.ErrInvalidEmail) {
		t.Fatalf("bad email = %v", err)
	}

	token, u, err := svc.LoginPassword(ctx, "BETA@example.com", "greylabs-greylabs", "10.0.0.1", "test")
	if err != nil || token == "" || u.ID != beta.ID {
		t.Fatalf("login = %q %+v, %v", token, u, err)
	}
	if got, err := svc.SessionUser(ctx, token); err != nil || got.ID != beta.ID {
		t.Fatalf("session = %+v, %v", got, err)
	}
	for _, c := range []struct{ email, pw string }{{"beta@example.com", "wrong password here"}, {"ghost@example.com", "greylabs-greylabs"}, {"beta@example.com", ""}} {
		if _, _, err := svc.LoginPassword(ctx, c.email, c.pw, "10.0.0.2", "test"); !errors.Is(err, auth.ErrInvalidLogin) {
			t.Fatalf("login %v = %v, want ErrInvalidLogin (same for every failure)", c, err)
		}
	}

	// Too many failures lock the email for a while, even for the right password.
	for i := 0; i < 8; i++ {
		_, _, _ = svc.LoginPassword(ctx, "boss@example.com", "nope nope nope", "10.0.0.3", "test")
	}
	var rl *auth.RateLimitError
	if _, _, err := svc.LoginPassword(ctx, "boss@example.com", "a long enough password", "10.0.0.4", "test"); !errors.As(err, &rl) {
		t.Fatalf("after 8 failures = %v, want a rate limit", err)
	}
	clk.advance(20 * time.Minute)
	if _, _, err := svc.LoginPassword(ctx, "boss@example.com", "a long enough password", "10.0.0.4", "test"); err != nil {
		t.Fatalf("after the window: %v", err)
	}

	// Own password: the current one is required once there is one.
	if err := svc.SetPassword(ctx, beta.ID, "not the current one", "another long password"); !errors.Is(err, auth.ErrWrongPassword) {
		t.Fatalf("change with the wrong current = %v", err)
	}
	if err := svc.SetPassword(ctx, beta.ID, "greylabs-greylabs", "another long password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.LoginPassword(ctx, "beta@example.com", "another long password", "10.0.0.5", "test"); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if err := svc.AdminSetPassword(ctx, beta.ID, "reset by the admin!"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.LoginPassword(ctx, "beta@example.com", "reset by the admin!", "10.0.0.5", "test"); err != nil {
		t.Fatalf("login after a reset: %v", err)
	}
	if err := svc.AdminSetPassword(ctx, "00000000-0000-4000-8000-000000000000", "a long enough password"); !errors.Is(err, auth.ErrNoUser) {
		t.Fatalf("reset of nobody = %v", err)
	}

	// Roles, and the last admin stays.
	if err := svc.SetAdmin(ctx, admin.ID, false); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("demoting the only admin = %v", err)
	}
	if err := svc.SetAdmin(ctx, beta.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAdmin(ctx, admin.ID, false); err != nil {
		t.Fatalf("demoting one of two admins: %v", err)
	}
	var admins int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE is_owner").Scan(&admins)
	if admins != 1 {
		t.Fatalf("admins = %d, want 1", admins)
	}
}

func TestDeviceLoginAndTokens(t *testing.T) {
	ctx := context.Background()
	svc, clk, _ := setup(t)
	userID := signedInUser(t, svc, "haki@acme.dev")

	start, err := svc.StartDevice(ctx, "grey on mac", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PollDevice(ctx, start.DeviceCode); !errors.Is(err, auth.ErrPending) {
		t.Fatalf("first poll = %v, want pending", err)
	}
	if _, _, err := svc.PollDevice(ctx, start.DeviceCode); !errors.Is(err, auth.ErrSlowDown) {
		t.Fatalf("immediate second poll = %v, want slow_down", err)
	}

	// The person types the code loosely; it still matches.
	loose := strings.ToLower(strings.ReplaceAll(start.UserCode, "-", " "))
	if info, err := svc.DeviceInfo(ctx, loose); err != nil || info.ClientName != "grey on mac" {
		t.Fatalf("device info = %+v, %v", info, err)
	}
	if err := svc.DecideDevice(ctx, userID, start.UserCode, "someone-elses-team", true); !errors.Is(err, auth.ErrNoTeam) {
		t.Fatalf("approve for a foreign team = %v", err)
	}
	if err := svc.DecideDevice(ctx, userID, loose, "", true); err != nil {
		t.Fatal(err)
	}

	clk.advance(6 * time.Second)
	token, user, err := svc.PollDevice(ctx, start.DeviceCode)
	if err != nil || user.ID != userID || !strings.HasPrefix(token, "gs_") {
		t.Fatalf("approved poll = %q %+v %v", token, user, err)
	}
	clk.advance(6 * time.Second)
	if _, _, err := svc.PollDevice(ctx, start.DeviceCode); !errors.Is(err, auth.ErrExpiredDev) {
		t.Fatalf("token issued twice: %v", err)
	}

	got, tok, err := svc.Authenticate(ctx, token)
	if err != nil || got.ID != userID {
		t.Fatalf("authenticate = %+v %v", got, err)
	}
	list, _ := svc.ListTokens(ctx, userID)
	if len(list) != 1 || list[0].Name != "grey login · grey on mac" || list[0].TeamSlug != "haki" {
		t.Fatalf("tokens = %+v", list)
	}
	if err := svc.RevokeToken(ctx, userID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("revoked token still works: %v", err)
	}
}

func TestDeviceDeniedAndExpired(t *testing.T) {
	ctx := context.Background()
	svc, clk, _ := setup(t)
	userID := signedInUser(t, svc, "haki@acme.dev")

	denied, _ := svc.StartDevice(ctx, "grey", "1.2.3.4")
	if err := svc.DecideDevice(ctx, userID, denied.UserCode, "", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.PollDevice(ctx, denied.DeviceCode); !errors.Is(err, auth.ErrDenied) {
		t.Fatalf("denied poll = %v", err)
	}
	if err := svc.DecideDevice(ctx, userID, denied.UserCode, "", true); !errors.Is(err, auth.ErrNoDevice) {
		t.Fatalf("approving after a denial = %v", err)
	}

	late, _ := svc.StartDevice(ctx, "grey", "1.2.3.4")
	clk.advance(11 * time.Minute)
	if err := svc.DecideDevice(ctx, userID, late.UserCode, "", true); !errors.Is(err, auth.ErrNoDevice) {
		t.Fatalf("approving an expired code = %v", err)
	}
	if _, _, err := svc.PollDevice(ctx, late.DeviceCode); !errors.Is(err, auth.ErrExpiredDev) {
		t.Fatalf("polling an expired code = %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, "gs_made-up"); !errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("made-up token = %v", err)
	}
}

//go:build integration

package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type inbox struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (i *inbox) Send(_ context.Context, m mail.Message) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.msgs = append(i.msgs, m)
	return nil
}

// lastCode reads the code from the subject line ("123456 is your Greyship code").
func (i *inbox) lastCode(t *testing.T) string {
	t.Helper()
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.msgs) == 0 {
		t.Fatal("no email sent")
	}
	code, _, _ := strings.Cut(i.msgs[len(i.msgs)-1].Subject, " ")
	return code
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func setup(t *testing.T) (*auth.Service, *inbox, *clock, *pgxpool.Pool) {
	pool := testdb.New(t)
	box := &inbox{}
	clk := &clock{t: time.Now()}
	return auth.NewService(pool, box, clk.now, auth.Signup{Open: true}), box, clk, pool
}

func TestEmailLoginFlow(t *testing.T) {
	svc, box, clk, _ := setup(t)
	ctx := context.Background()

	if err := svc.StartEmail(ctx, "Haki@Acme.dev", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	token, user, err := svc.VerifyEmail(ctx, "haki@acme.dev", box.lastCode(t), "test")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if user.Email != "haki@acme.dev" {
		t.Fatalf("user email = %q", user.Email)
	}
	teams, _ := svc.Teams(ctx, user.ID)
	if len(teams) != 1 || teams[0].Slug != "haki" || teams[0].Role != "owner" {
		t.Fatalf("personal team = %+v", teams)
	}
	if got, err := svc.SessionUser(ctx, token); err != nil || got.ID != user.ID {
		t.Fatalf("session user = %+v, %v", got, err)
	}

	// Second login reuses the account and team.
	clk.advance(2 * time.Minute)
	if err := svc.StartEmail(ctx, "haki@acme.dev", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	_, again, err := svc.VerifyEmail(ctx, "haki@acme.dev", box.lastCode(t), "test")
	if err != nil || again.ID != user.ID {
		t.Fatalf("second login = %+v, %v", again, err)
	}
	if teams, _ := svc.Teams(ctx, user.ID); len(teams) != 1 {
		t.Fatalf("second login created another team: %+v", teams)
	}

	if err := svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SessionUser(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("after logout err = %v", err)
	}
}

func TestSignupAllowlist(t *testing.T) {
	pool := testdb.New(t)
	box := &inbox{}
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	open := auth.NewService(pool, box, clk.now, auth.Signup{Open: true})
	closed := auth.NewService(pool, box, clk.now, auth.Signup{Allow: auth.ParseAllowlist("@greylabs.dev")})

	login := func(svc *auth.Service, email string) error {
		clk.advance(2 * time.Minute) // past the per-email cooldown
		if err := svc.StartEmail(ctx, email, "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
		_, _, err := svc.VerifyEmail(ctx, email, box.lastCode(t), "test")
		return err
	}
	if err := login(open, "early@acme.dev"); err != nil {
		t.Fatal(err)
	}
	if err := login(closed, "stranger@acme.dev"); !errors.Is(err, auth.ErrNotInvited) {
		t.Fatalf("uninvited signup = %v, want ErrNotInvited", err)
	}
	if err := login(closed, "early@acme.dev"); err != nil {
		t.Fatalf("an existing user must still sign in: %v", err)
	}
	if err := login(closed, "haki@greylabs.dev"); err != nil {
		t.Fatalf("invited domain: %v", err)
	}
	var users int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = 'stranger@acme.dev'").Scan(&users)
	if users != 0 {
		t.Fatal("an uninvited email must not create an account")
	}
}

// With an owner email configured, nobody else can be the first account.
func TestOwnerEmailClaimsTheInstance(t *testing.T) {
	pool := testdb.New(t)
	box := &inbox{}
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	svc := auth.NewService(pool, box, clk.now, auth.Signup{OwnerEmail: "boss@example.com"})
	login := func(email string) (store.User, error) {
		clk.advance(2 * time.Minute)
		if err := svc.StartEmail(ctx, email, "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
		_, u, err := svc.VerifyEmail(ctx, email, box.lastCode(t), "test")
		return u, err
	}
	if _, err := login("intruder@example.com"); !errors.Is(err, auth.ErrNotInvited) {
		t.Fatalf("a stranger signing up first = %v, want ErrNotInvited", err)
	}
	if u, err := login("boss@example.com"); err != nil || !u.IsOwner {
		t.Fatalf("the owner = %+v, %v; want the owner", u, err)
	}
	if _, err := login("intruder@example.com"); !errors.Is(err, auth.ErrNotInvited) {
		t.Fatalf("a stranger after the owner = %v, want ErrNotInvited", err)
	}
}

// The first account owns the instance; after that signup is closed unless opened (decisions.md 050).
func TestFirstOwnerAndClosedSignup(t *testing.T) {
	pool := testdb.New(t)
	box := &inbox{}
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	svc := auth.NewService(pool, box, clk.now, auth.Signup{})
	login := func(svc *auth.Service, email string) (store.User, error) {
		clk.advance(2 * time.Minute)
		if err := svc.StartEmail(ctx, email, "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
		_, u, err := svc.VerifyEmail(ctx, email, box.lastCode(t), "test")
		return u, err
	}
	first, err := login(svc, "ada@example.com")
	if err != nil || !first.IsOwner {
		t.Fatalf("first account = %+v, %v; want the owner", first, err)
	}
	var plan string
	_ = pool.QueryRow(ctx, "SELECT t.plan FROM teams t JOIN memberships m ON m.team_id = t.id WHERE m.user_id = $1", first.ID).Scan(&plan)
	if plan != "unlimited" {
		t.Fatalf("plan without plans on = %q, want unlimited", plan)
	}
	if _, err := login(svc, "bob@example.com"); !errors.Is(err, auth.ErrNotInvited) {
		t.Fatalf("second signup on a closed instance = %v, want ErrNotInvited", err)
	}
	open := auth.NewService(pool, box, clk.now, auth.Signup{Open: true, PlansOn: true})
	u, err := login(open, "carol@example.com")
	if err != nil || u.IsOwner {
		t.Fatalf("open signup = %+v, %v; want an ordinary user", u, err)
	}
	_ = pool.QueryRow(ctx, "SELECT t.plan FROM teams t JOIN memberships m ON m.team_id = t.id WHERE m.user_id = $1", u.ID).Scan(&plan)
	if plan != "free" {
		t.Fatalf("plan with plans on = %q, want free", plan)
	}
	var owners int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE is_owner").Scan(&owners)
	if owners != 1 {
		t.Fatalf("owners = %d, want 1 (the first account)", owners)
	}
}

// Accounts an admin creates sign in with a password, with limits and a guard on the last admin.
func TestAdminCreatedAccounts(t *testing.T) {
	pool := testdb.New(t)
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	svc := auth.NewService(pool, &inbox{}, clk.now, auth.Signup{})

	admin, err := svc.CreateUser(ctx, auth.NewUser{Email: "Boss@Example.com", Name: "Boss", Password: "a long enough password", Admin: true})
	if err != nil || !admin.IsOwner || admin.Email != "boss@example.com" {
		t.Fatalf("create admin = %+v, %v", admin, err)
	}
	beta, err := svc.CreateUser(ctx, auth.NewUser{Email: "beta@example.com", Name: "beta", Password: "greylabs-greylabs"})
	if err != nil || beta.IsOwner {
		t.Fatalf("create user = %+v, %v; want a non-admin", beta, err)
	}
	var plan string
	_ = pool.QueryRow(ctx, "SELECT t.plan FROM teams t JOIN memberships m ON m.team_id = t.id WHERE m.user_id = $1", beta.ID).Scan(&plan)
	if plan != "unlimited" {
		t.Fatalf("a created user's plan = %q", plan)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "beta@example.com"}); !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "x@example.com", Password: "short"}); !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("weak password = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "not an email"}); !errors.Is(err, auth.ErrInvalidEmail) {
		t.Fatalf("bad email = %v", err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "nopass@example.com"}); err != nil {
		t.Fatalf("an account without a password: %v", err)
	}

	token, u, err := svc.LoginPassword(ctx, "BETA@example.com", "greylabs-greylabs", "10.0.0.1", "test")
	if err != nil || token == "" || u.ID != beta.ID {
		t.Fatalf("login = %q %+v, %v", token, u, err)
	}
	if got, err := svc.SessionUser(ctx, token); err != nil || got.ID != beta.ID {
		t.Fatalf("session = %+v, %v", got, err)
	}
	for _, c := range []struct{ email, pw string }{{"beta@example.com", "wrong password here"}, {"ghost@example.com", "greylabs-greylabs"}, {"nopass@example.com", "greylabs-greylabs"}, {"beta@example.com", ""}} {
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

type testers struct {
	list map[string]bool
	err  error
}

func (f *testers) IsTester(_ context.Context, email string) (bool, error) {
	return f.list[email], f.err
}

// With a beta list, its people sign up and get the badge; others need the allowlist (decisions.md 049).
func TestBetaTesters(t *testing.T) {
	pool := testdb.New(t)
	box := &inbox{}
	clk := &clock{t: time.Now()}
	ctx := context.Background()
	list := &testers{list: map[string]bool{"ada@example.com": true}}
	svc := auth.NewService(pool, box, clk.now, auth.Signup{Allow: auth.ParseAllowlist("founder@greyship.tech"), Testers: list})
	login := func(email string) (store.User, error) {
		clk.advance(2 * time.Minute)
		if err := svc.StartEmail(ctx, email, "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
		_, u, err := svc.VerifyEmail(ctx, email, box.lastCode(t), "test")
		return u, err
	}
	if u, err := login("ada@example.com"); err != nil || !u.BetaTester {
		t.Fatalf("tester signup = %+v, %v; want an account with the badge", u, err)
	}
	if _, err := login("stranger@example.com"); !errors.Is(err, auth.ErrNotInvited) {
		t.Fatalf("not on the list = %v, want ErrNotInvited", err)
	}
	if u, err := login("founder@greyship.tech"); err != nil || u.BetaTester {
		t.Fatalf("allowlisted = %+v, %v; want an account without the badge", u, err)
	}
	// The founder joins the beta list later: the badge comes with the next login.
	list.list["founder@greyship.tech"] = true
	if u, err := login("founder@greyship.tech"); err != nil || !u.BetaTester {
		t.Fatalf("later tester = %+v, %v; want the badge now", u, err)
	}
	// The list is down: nobody new gets in, unless allowlisted; existing people still sign in.
	list.err = errors.New("beta app unreachable")
	if _, err := login("new@example.com"); !errors.Is(err, auth.ErrInviteCheck) {
		t.Fatalf("new signup with the list down = %v, want ErrInviteCheck", err)
	}
	if _, err := login("ada@example.com"); err != nil {
		t.Fatalf("an existing tester must sign in with the list down: %v", err)
	}
}

func TestCodeRules(t *testing.T) {
	ctx := context.Background()

	t.Run("five wrong attempts lock the code", func(t *testing.T) {
		svc, box, _, _ := setup(t)
		_ = svc.StartEmail(ctx, "a@b.co", "1.1.1.1")
		code := box.lastCode(t)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		for range 5 {
			if _, _, err := svc.VerifyEmail(ctx, "a@b.co", wrong, ""); !errors.Is(err, auth.ErrInvalidCode) {
				t.Fatalf("wrong code err = %v", err)
			}
		}
		if _, _, err := svc.VerifyEmail(ctx, "a@b.co", code, ""); !errors.Is(err, auth.ErrInvalidCode) {
			t.Fatalf("right code after 5 failures err = %v, want locked", err)
		}
	})

	t.Run("a code works once", func(t *testing.T) {
		svc, box, _, _ := setup(t)
		_ = svc.StartEmail(ctx, "a@b.co", "1.1.1.1")
		code := box.lastCode(t)
		if _, _, err := svc.VerifyEmail(ctx, "a@b.co", code, ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.VerifyEmail(ctx, "a@b.co", code, ""); !errors.Is(err, auth.ErrInvalidCode) {
			t.Fatalf("reuse err = %v", err)
		}
	})

	t.Run("codes expire after 10 minutes", func(t *testing.T) {
		svc, box, clk, _ := setup(t)
		_ = svc.StartEmail(ctx, "a@b.co", "1.1.1.1")
		clk.advance(11 * time.Minute)
		if _, _, err := svc.VerifyEmail(ctx, "a@b.co", box.lastCode(t), ""); !errors.Is(err, auth.ErrInvalidCode) {
			t.Fatalf("expired err = %v", err)
		}
	})

	t.Run("one code per email per minute", func(t *testing.T) {
		svc, _, _, _ := setup(t)
		_ = svc.StartEmail(ctx, "a@b.co", "1.1.1.1")
		var rl *auth.RateLimitError
		if err := svc.StartEmail(ctx, "a@b.co", "2.2.2.2"); !errors.As(err, &rl) {
			t.Fatalf("second start err = %v", err)
		}
	})

	t.Run("ten codes per IP per hour", func(t *testing.T) {
		svc, _, _, _ := setup(t)
		for i := range 10 {
			if err := svc.StartEmail(ctx, string(rune('a'+i))+"@b.co", "3.3.3.3"); err != nil {
				t.Fatalf("start %d: %v", i, err)
			}
		}
		var rl *auth.RateLimitError
		if err := svc.StartEmail(ctx, "z@b.co", "3.3.3.3"); !errors.As(err, &rl) {
			t.Fatalf("11th start err = %v", err)
		}
	})
}

func TestOAuthLinking(t *testing.T) {
	ctx := context.Background()
	svc, _, _, pool := setup(t)

	gh := auth.Identity{Provider: "github", Subject: "42", Email: "Ada@x.io", EmailVerified: true, Name: "Ada", AvatarURL: "https://a/x.png"}
	_, first, err := svc.CompleteOAuth(ctx, gh, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Email != "ada@x.io" || first.Name != "Ada" {
		t.Fatalf("created user = %+v", first)
	}

	_, again, err := svc.CompleteOAuth(ctx, gh, "")
	if err != nil || again.ID != first.ID {
		t.Fatalf("same identity = %+v, %v", again, err)
	}

	google := auth.Identity{Provider: "google", Subject: "g-1", Email: "ada@x.io", EmailVerified: true}
	_, linked, err := svc.CompleteOAuth(ctx, google, "")
	if err != nil || linked.ID != first.ID {
		t.Fatalf("verified google email should link: %+v, %v", linked, err)
	}

	tests := []struct {
		name string
		id   auth.Identity
	}{
		{"unverified email of an existing user", auth.Identity{Provider: "github", Subject: "99", Email: "ada@x.io"}},
		{"unverified email of a new user", auth.Identity{Provider: "github", Subject: "100", Email: "new@x.io"}},
		{"no email at all", auth.Identity{Provider: "github", Subject: "101", EmailVerified: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := svc.CompleteOAuth(ctx, tt.id, ""); !errors.Is(err, auth.ErrEmailNotProven) {
				t.Fatalf("err = %v, want ErrEmailNotProven", err)
			}
		})
	}

	var count int
	if err := pool.QueryRow(ctx, "select count(*) from users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("users = %d, %v; want exactly 1", count, err)
	}
}

func signedInUser(t *testing.T, svc *auth.Service, box *inbox, email string) string {
	t.Helper()
	ctx := context.Background()
	if err := svc.StartEmail(ctx, email, "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	_, u, err := svc.VerifyEmail(ctx, email, box.lastCode(t), "")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestDeviceLoginAndTokens(t *testing.T) {
	ctx := context.Background()
	svc, box, clk, _ := setup(t)
	userID := signedInUser(t, svc, box, "haki@acme.dev")

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
	svc, box, clk, _ := setup(t)
	userID := signedInUser(t, svc, box, "haki@acme.dev")

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

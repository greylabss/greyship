//go:build integration

package github_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/github"
	"greyship.tech/internal/secrets"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

func testCredentials(t *testing.T, id int64, slug string) github.Credentials {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return github.Credentials{AppID: id, Slug: slug, Name: "Greyship " + slug, HTMLURL: "https://github.com/apps/" + slug, ClientID: "Iv1." + slug, ClientSecret: "client-secret-" + slug, WebhookSecret: "webhook-secret-" + slug, PrivateKeyPEM: pemKey}
}

// Every team has its own app, stored sealed, and a team never gets another team's.
func TestAppsArePerTeamAndSealed(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	keys, err := secrets.NewKeyring(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	apps := &github.Apps{Pool: pool, Keys: keys, Now: time.Now, Logger: slog.New(slog.DiscardHandler)}
	one, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "one", Name: "one"})
	two, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "two", Name: "two"})
	user, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "u@example.com"})

	if _, err := apps.ForTeam(ctx, one.ID); !errors.Is(err, github.ErrOff) {
		t.Fatalf("no app yet: %v, want ErrOff", err)
	}
	if _, err := apps.WebhookSecret(ctx, one.ID); !errors.Is(err, github.ErrOff) {
		t.Fatalf("webhook secret with no app: %v", err)
	}
	if st, err := apps.Status(ctx, one.ID); err != nil || st.Configured {
		t.Fatalf("status with no app = %+v %v", st, err)
	}
	if err := apps.Save(ctx, one.ID, user.ID, github.Credentials{AppID: 1, PrivateKeyPEM: "not a key"}); err == nil {
		t.Fatal("a key that can't be read was saved")
	}

	if err := apps.Save(ctx, one.ID, user.ID, testCredentials(t, 77, "one")); err != nil {
		t.Fatal(err)
	}
	if err := apps.Save(ctx, two.ID, user.ID, testCredentials(t, 88, "two")); err != nil {
		t.Fatal(err)
	}

	// Applied at once, and each team gets its own.
	a1, err := apps.ForTeam(ctx, one.ID)
	if err != nil || a1.ID != 77 || a1.ClientSecret != "client-secret-one" {
		t.Fatalf("team one's app = %+v %v", a1, err)
	}
	a2, _ := apps.ForTeam(ctx, two.ID)
	if a2.ID != 88 {
		t.Fatalf("team two got app %d", a2.ID)
	}
	if hook, err := apps.WebhookSecret(ctx, one.ID); err != nil || hook != "webhook-secret-one" {
		t.Fatalf("team one's webhook secret = %q %v", hook, err)
	}
	if hook, _ := apps.WebhookSecret(ctx, two.ID); hook != "webhook-secret-two" {
		t.Fatalf("team two's webhook secret = %q", hook)
	}

	// The secrets are not in the database in plain text.
	var cs, pk, ws []byte
	if err := pool.QueryRow(ctx, "SELECT client_secret_enc, private_key_enc, webhook_secret_enc FROM github_apps WHERE team_id = $1", one.ID).Scan(&cs, &pk, &ws); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string][]byte{"client secret": cs, "private key": pk, "webhook secret": ws} {
		if bytes.Contains(v, []byte("client-secret-one")) || bytes.Contains(v, []byte("webhook-secret-one")) || bytes.Contains(v, []byte("PRIVATE KEY")) {
			t.Fatalf("the %s is stored in plain text", name)
		}
	}

	// The installation tells which team's app serves a call: Dynamic finds it through the installation.
	_ = q.AddInstallation(ctx, store.AddInstallationParams{ID: 501, TeamID: one.ID, AccountLogin: "one"})
	_ = q.AddInstallation(ctx, store.AddInstallationParams{ID: 502, TeamID: two.ID, AccountLogin: "two"})
	dyn := github.Dynamic{Apps: apps}
	if _, err := dyn.Repos(ctx, 999); !errors.Is(err, github.ErrOff) {
		t.Fatalf("an unknown installation = %v, want ErrOff", err)
	}
	if _, err := dyn.InstallURL(ctx, one.ID, "s"); err != nil {
		t.Fatalf("install url for a team with an app: %v", err)
	}
	three, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "three", Name: "three"})
	if _, err := dyn.InstallURL(ctx, three.ID, "s"); !errors.Is(err, github.ErrOff) {
		t.Fatalf("install url for a team without an app = %v, want ErrOff", err)
	}

	// A wrong master key can't open it.
	wrong, _ := secrets.NewKeyring(strings.Repeat("cd", 32))
	bad := &github.Apps{Pool: pool, Keys: wrong, Now: time.Now, Logger: slog.New(slog.DiscardHandler)}
	if _, err := bad.ForTeam(ctx, one.ID); err == nil || errors.Is(err, github.ErrOff) {
		t.Fatalf("a wrong master key = %v, want a decryption error", err)
	}

	// Deleting removes the app and its installations, and only for that team.
	if err := apps.Delete(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.ForTeam(ctx, one.ID); !errors.Is(err, github.ErrOff) {
		t.Fatalf("after delete: %v", err)
	}
	var n1, n2 int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM github_installations WHERE team_id = $1", one.ID).Scan(&n1)
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM github_installations WHERE team_id = $1", two.ID).Scan(&n2)
	if n1 != 0 || n2 != 1 {
		t.Fatalf("installations after deleting team one's app: one=%d two=%d, want 0 and 1", n1, n2)
	}
	if _, err := apps.ForTeam(ctx, two.ID); err != nil {
		t.Fatalf("team two lost its app: %v", err)
	}
}

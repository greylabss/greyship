package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"greyship.tech/internal/store"
)

// Identity is what a provider tells us about the person who signed in.
type Identity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

type Provider struct {
	Name     string
	Config   oauth2.Config
	Identify func(ctx context.Context, client *http.Client) (Identity, error)
}

// Endpoints are overridable so tests can point providers at a fake server.
type Endpoints struct {
	AuthURL  string
	TokenURL string
	APIURL   string
}

// Public provider URLs, not credentials.
var (
	GoogleEndpoints = Endpoints{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", APIURL: "https://openidconnect.googleapis.com"} //nolint:gosec // public endpoints
	GitHubEndpoints = Endpoints{AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", APIURL: "https://api.github.com"}           //nolint:gosec // public endpoints
)

func Google(clientID, secret, redirectURL string, ep Endpoints) Provider {
	return Provider{
		Name: "google",
		Config: oauth2.Config{
			ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL,
			Endpoint: oauth2.Endpoint{AuthURL: ep.AuthURL, TokenURL: ep.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
			Scopes:   []string{"openid", "email", "profile"},
		},
		Identify: func(ctx context.Context, c *http.Client) (Identity, error) {
			var u struct {
				Sub           string `json:"sub"`
				Email         string `json:"email"`
				EmailVerified bool   `json:"email_verified"`
				Name          string `json:"name"`
				Picture       string `json:"picture"`
			}
			if err := getJSON(ctx, c, ep.APIURL+"/v1/userinfo", &u); err != nil {
				return Identity{}, err
			}
			return Identity{Provider: "google", Subject: u.Sub, Email: u.Email, EmailVerified: u.EmailVerified, Name: u.Name, AvatarURL: u.Picture}, nil
		},
	}
}

func GitHub(clientID, secret, redirectURL string, ep Endpoints) Provider {
	return Provider{
		Name: "github",
		Config: oauth2.Config{
			ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL,
			Endpoint: oauth2.Endpoint{AuthURL: ep.AuthURL, TokenURL: ep.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
			Scopes:   []string{"read:user", "user:email"},
		},
		Identify: func(ctx context.Context, c *http.Client) (Identity, error) {
			var u struct {
				ID        int64  `json:"id"`
				Login     string `json:"login"`
				Name      string `json:"name"`
				AvatarURL string `json:"avatar_url"`
			}
			if err := getJSON(ctx, c, ep.APIURL+"/user", &u); err != nil {
				return Identity{}, err
			}
			// The profile email can be unset or unverified; only the primary verified address counts.
			var emails []struct {
				Email    string `json:"email"`
				Primary  bool   `json:"primary"`
				Verified bool   `json:"verified"`
			}
			if err := getJSON(ctx, c, ep.APIURL+"/user/emails", &emails); err != nil {
				return Identity{}, err
			}
			id := Identity{Provider: "github", Subject: strconv.FormatInt(u.ID, 10), Name: u.Name, AvatarURL: u.AvatarURL}
			if id.Name == "" {
				id.Name = u.Login
			}
			for _, e := range emails {
				if e.Primary {
					id.Email, id.EmailVerified = e.Email, e.Verified
				}
			}
			return id, nil
		},
	}
}

func getJSON(ctx context.Context, c *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// CompleteOAuth signs in the person behind an identity, linking or creating the account, and returns a session token.
func (s *Service) CompleteOAuth(ctx context.Context, id Identity, userAgent string) (string, store.User, error) {
	if id.Subject == "" {
		return "", store.User{}, errors.New("provider returned no account id")
	}
	var token string
	var user store.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		existing, err := q.GetIdentity(ctx, store.GetIdentityParams{Provider: id.Provider, Subject: id.Subject})
		switch {
		case err == nil:
			if user, err = q.GetUserByID(ctx, existing.UserID); err != nil {
				return fmt.Errorf("load linked user: %w", err)
			}
		case errors.Is(err, pgx.ErrNoRows):
			// Linking by email is only safe when the provider has proven the person owns that address.
			if !id.EmailVerified {
				return ErrEmailNotProven
			}
			email, err := NormalizeEmail(id.Email)
			if err != nil {
				return ErrEmailNotProven
			}
			if user, err = findOrCreateUser(ctx, q, s.signup, email, id.Name, id.AvatarURL); err != nil {
				return err
			}
			if err := q.CreateIdentity(ctx, store.CreateIdentityParams{Provider: id.Provider, Subject: id.Subject, UserID: user.ID, Email: email}); err != nil {
				return fmt.Errorf("link %s identity: %w", id.Provider, err)
			}
		default:
			return fmt.Errorf("load identity: %w", err)
		}
		token, err = s.createSession(ctx, q, user.ID, userAgent)
		return err
	})
	if err != nil {
		return "", store.User{}, err
	}
	return token, user, nil
}

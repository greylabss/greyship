package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/store"
)

const (
	tokenPrefix      = "gs_"
	deviceTTL        = 10 * time.Minute
	devicePollEvery  = 5 * time.Second
	tokenTouchEvery  = time.Minute
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ" // consonants only (RFC 8628 §6.1): no vowels, no lookalikes
)

var (
	ErrNoToken    = errors.New("invalid or revoked token")
	ErrNoTeam     = errors.New("team not found or not a member")
	ErrNoDevice   = errors.New("unknown or expired device code")
	ErrPending    = errors.New("authorization_pending")
	ErrSlowDown   = errors.New("slow_down")
	ErrDenied     = errors.New("access_denied")
	ErrExpiredDev = errors.New("expired_token")
)

// newAPIToken has a recognisable prefix so secret scanners (GitHub, gitleaks) can spot leaked tokens.
func newAPIToken() (token, display string, err error) {
	raw, err := newToken()
	if err != nil {
		return "", "", err
	}
	token = tokenPrefix + raw
	return token, token[:len(tokenPrefix)+6], nil
}

// Authenticate resolves a bearer token to its user and team.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, store.Token, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return store.User{}, store.Token{}, ErrNoToken
	}
	q := store.New(s.pool)
	t, err := q.GetTokenByHash(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, store.Token{}, ErrNoToken
	}
	if err != nil {
		return store.User{}, store.Token{}, fmt.Errorf("load token: %w", err)
	}
	now := s.now()
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > tokenTouchEvery {
		if err := q.TouchToken(ctx, store.TouchTokenParams{ID: t.ID, LastUsedAt: &now}); err != nil {
			return store.User{}, store.Token{}, fmt.Errorf("touch token: %w", err)
		}
	}
	user, err := q.GetUserByID(ctx, t.UserID)
	if err != nil {
		return store.User{}, store.Token{}, fmt.Errorf("load token user: %w", err)
	}
	return user, t, nil
}

// teamFor returns the named team if the user belongs to it, or their first (personal) team when slug is empty.
func teamFor(ctx context.Context, q *store.Queries, userID, slug string) (string, error) {
	if slug == "" {
		teams, err := q.ListUserTeams(ctx, userID)
		if err != nil {
			return "", fmt.Errorf("list teams: %w", err)
		}
		if len(teams) == 0 {
			return "", ErrNoTeam
		}
		return teams[0].ID, nil
	}
	t, err := q.GetMembership(ctx, store.GetMembershipParams{UserID: userID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoTeam
	}
	if err != nil {
		return "", fmt.Errorf("load membership: %w", err)
	}
	return t.ID, nil
}

func (s *Service) createToken(ctx context.Context, q *store.Queries, userID, teamID, name string) (string, store.Token, error) {
	raw, display, err := newAPIToken()
	if err != nil {
		return "", store.Token{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "token"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	t, err := q.CreateToken(ctx, store.CreateTokenParams{TeamID: teamID, UserID: userID, Name: name, TokenHash: hashToken(raw), Prefix: display})
	if err != nil {
		return "", store.Token{}, fmt.Errorf("create token: %w", err)
	}
	return raw, t, nil
}

// CreateToken issues a token for one of the user's teams; the raw value is returned once and never stored.
func (s *Service) CreateToken(ctx context.Context, userID, teamSlug, name string) (string, store.Token, error) {
	q := store.New(s.pool)
	teamID, err := teamFor(ctx, q, userID, teamSlug)
	if err != nil {
		return "", store.Token{}, err
	}
	return s.createToken(ctx, q, userID, teamID, name)
}

func (s *Service) ListTokens(ctx context.Context, userID string) ([]store.ListTokensRow, error) {
	rows, err := store.New(s.pool).ListTokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	return rows, nil
}

func (s *Service) RevokeToken(ctx context.Context, userID, tokenID string) error {
	n, err := store.New(s.pool).DeleteToken(ctx, store.DeleteTokenParams{ID: tokenID, UserID: userID})
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if n == 0 {
		return ErrNoToken
	}
	return nil
}

type DeviceStart struct {
	DeviceCode string
	UserCode   string
	ExpiresIn  time.Duration
	Interval   time.Duration
}

func newUserCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate user code: %w", err)
	}
	out := make([]byte, 0, 9)
	for i, v := range b {
		if i == 4 {
			out = append(out, '-')
		}
		// 256 % 20 != 0 leaves a tiny bias; irrelevant for a 10-minute, rate-limited code.
		out = append(out, userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return string(out), nil
}

// NormalizeUserCode accepts "kqwd7hxp", "KQWD 7HXP" or "KQWD-7HXP".
func NormalizeUserCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if strings.ContainsRune(userCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	c := b.String()
	if len(c) != 8 {
		return ""
	}
	return c[:4] + "-" + c[4:]
}

// StartDevice begins `grey login`: the CLI keeps DeviceCode, the person approves UserCode in the browser.
func (s *Service) StartDevice(ctx context.Context, clientName, ip string) (DeviceStart, error) {
	device, err := newToken()
	if err != nil {
		return DeviceStart{}, err
	}
	userCode, err := newUserCode()
	if err != nil {
		return DeviceStart{}, err
	}
	if len(clientName) > 80 {
		clientName = clientName[:80]
	}
	err = store.New(s.pool).CreateDeviceCode(ctx, store.CreateDeviceCodeParams{
		DeviceCodeHash: hashToken(device), UserCode: userCode, ClientName: clientName, Ip: ip, ExpiresAt: s.now().Add(deviceTTL),
	})
	if err != nil {
		return DeviceStart{}, fmt.Errorf("store device code: %w", err)
	}
	return DeviceStart{DeviceCode: device, UserCode: userCode, ExpiresIn: deviceTTL, Interval: devicePollEvery}, nil
}

func (s *Service) DeviceInfo(ctx context.Context, userCode string) (store.DeviceCode, error) {
	code := NormalizeUserCode(userCode)
	if code == "" {
		return store.DeviceCode{}, ErrNoDevice
	}
	d, err := store.New(s.pool).GetDeviceByUserCode(ctx, store.GetDeviceByUserCodeParams{UserCode: code, ExpiresAt: s.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.DeviceCode{}, ErrNoDevice
	}
	if err != nil {
		return store.DeviceCode{}, fmt.Errorf("load device code: %w", err)
	}
	return d, nil
}

// DecideDevice records the signed-in person's approval or denial of a pending CLI login.
func (s *Service) DecideDevice(ctx context.Context, userID, userCode, teamSlug string, approve bool) error {
	code := NormalizeUserCode(userCode)
	if code == "" {
		return ErrNoDevice
	}
	q := store.New(s.pool)
	status, team := "denied", (*string)(nil)
	if approve {
		teamID, err := teamFor(ctx, q, userID, teamSlug)
		if err != nil {
			return err
		}
		status, team = "approved", &teamID
	}
	n, err := q.DecideDevice(ctx, store.DecideDeviceParams{UserCode: code, Status: status, UserID: &userID, TeamID: team, ExpiresAt: s.now()})
	if err != nil {
		return fmt.Errorf("decide device code: %w", err)
	}
	if n == 0 {
		return ErrNoDevice
	}
	return nil
}

// PollDevice is called by the CLI every few seconds until the person approves, denies, or the code expires.
func (s *Service) PollDevice(ctx context.Context, deviceCode string) (string, store.User, error) {
	var token string
	var user store.User
	var result error
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		d, err := q.GetDeviceByHash(ctx, hashToken(deviceCode))
		if errors.Is(err, pgx.ErrNoRows) {
			result = ErrExpiredDev
			return nil
		}
		if err != nil {
			return fmt.Errorf("load device code: %w", err)
		}
		now := s.now()
		if now.After(d.ExpiresAt) || d.Status == "consumed" {
			result = ErrExpiredDev
			return nil
		}
		tooSoon := d.LastPolledAt != nil && now.Sub(*d.LastPolledAt) < devicePollEvery-time.Second
		if err := q.SetDevicePolled(ctx, store.SetDevicePolledParams{ID: d.ID, LastPolledAt: &now}); err != nil {
			return fmt.Errorf("record poll: %w", err)
		}
		switch {
		case d.Status == "denied":
			result = ErrDenied
			return nil
		case d.Status == "pending" && tooSoon:
			result = ErrSlowDown
			return nil
		case d.Status == "pending":
			result = ErrPending
			return nil
		}
		// Approved: issue the token exactly once.
		if user, err = q.GetUserByID(ctx, *d.UserID); err != nil {
			return fmt.Errorf("load approving user: %w", err)
		}
		if token, _, err = s.createToken(ctx, q, *d.UserID, *d.TeamID, "grey login · "+d.ClientName); err != nil {
			return err
		}
		return q.ConsumeDevice(ctx, d.ID)
	})
	if err != nil {
		return "", store.User{}, err
	}
	if result != nil {
		return "", store.User{}, result
	}
	return token, user, nil
}

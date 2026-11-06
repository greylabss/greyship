// Package env stores a project's environment variables encrypted (spec F-40–F-48, decisions.md 018–020).
// The CLI works on main (shared) variables; the node agent asks for one service's effective set at instance start.
package env

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/project"
	"greyship.tech/internal/secrets"
	"greyship.tech/internal/store"
)

const (
	maxValue = 32 << 10
	maxVars  = 500
)

var (
	keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)
	ErrBadEnv  = errors.New("env must be production or preview")
	ErrTooMany = fmt.Errorf("a project can have at most %d variables per environment", maxVars)
)

// KeyError names a variable that can't be set and why.
type KeyError struct{ Key, Reason string }

func (e *KeyError) Error() string { return e.Key + ": " + e.Reason }

type Projects interface {
	Access(ctx context.Context, c project.Caller, id string, write bool) (project.Project, error)
}

type Service struct {
	pool     *pgxpool.Pool
	keyring  *secrets.Keyring
	projects Projects
}

func NewService(pool *pgxpool.Pool, keyring *secrets.Keyring, projects Projects) *Service {
	return &Service{pool: pool, keyring: keyring, projects: projects}
}

type Var struct {
	Key       string
	Hash      string // secrets.Hash of the value: lets the CLI find changes without either side sending values
	UpdatedAt time.Time
}

// Change reports what Apply did, by key only.
type Change struct {
	Added   []string
	Updated []string
	Removed []string
}

func (c Change) Empty() bool { return len(c.Added)+len(c.Updated)+len(c.Removed) == 0 }

func validEnv(e string) error {
	if e != "production" && e != "preview" {
		return ErrBadEnv
	}
	return nil
}

// List returns the main variables' keys and value hashes, never values. Viewers may list.
func (s *Service) List(ctx context.Context, c project.Caller, projectID, environment string) ([]Var, error) {
	if err := validEnv(environment); err != nil {
		return nil, err
	}
	p, err := s.projects.Access(ctx, c, projectID, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.pool).ListMainEnv(ctx, store.ListMainEnvParams{ProjectID: p.ID, Env: environment})
	if err != nil {
		return nil, fmt.Errorf("list env of %s: %w", p.ID, err)
	}
	out := make([]Var, 0, len(rows))
	for _, r := range rows {
		out = append(out, Var{Key: r.Key, Hash: r.ValueHash, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

// Values decrypts the main variables. It is the one way values leave the store for a person (grey env pull),
// so it needs write access: viewers can see keys, not secrets.
func (s *Service) Values(ctx context.Context, c project.Caller, projectID, environment string) (map[string]string, error) {
	if err := validEnv(environment); err != nil {
		return nil, err
	}
	p, err := s.projects.Access(ctx, c, projectID, true)
	if err != nil {
		return nil, err
	}
	q := store.New(s.pool)
	rows, err := q.ListMainEnvValues(ctx, store.ListMainEnvValuesParams{ProjectID: p.ID, Env: environment})
	if err != nil {
		return nil, fmt.Errorf("read env of %s: %w", p.ID, err)
	}
	if len(rows) == 0 {
		return map[string]string{}, nil
	}
	key, err := s.projectKey(ctx, q, p.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if out[r.Key], err = key.Decrypt(environment, r.Key, r.Ciphertext); err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", r.Key, err)
		}
	}
	return out, nil
}

// CheckKey reports why a variable name can't be used, or nil.
func CheckKey(k string) error {
	switch {
	case !keyPattern.MatchString(k):
		return &KeyError{Key: k, Reason: "use letters, digits and underscores, not starting with a digit"}
	case k == "PORT" || strings.HasPrefix(k, "GREYSHIP_"):
		return &KeyError{Key: k, Reason: "PORT and GREYSHIP_* are set by Greyship"}
	}
	return nil
}

// Apply sets and removes main variables in one transaction. With restart, production services that receive
// the variables are rolled onto them (F-42); a deploy passes false because it rolls everything anyway.
func (s *Service) Apply(ctx context.Context, c project.Caller, projectID, environment string, set map[string]string, unset []string, restart bool) (Change, error) {
	if err := validEnv(environment); err != nil {
		return Change{}, err
	}
	for k, v := range set {
		if err := CheckKey(k); err != nil {
			return Change{}, err
		}
		if len(v) > maxValue {
			return Change{}, &KeyError{Key: k, Reason: "values can be at most 32 KB"}
		}
	}
	for _, k := range unset {
		if err := CheckKey(k); err != nil {
			return Change{}, err
		}
	}
	p, err := s.projects.Access(ctx, c, projectID, true)
	if err != nil {
		return Change{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Change{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	existing, err := q.ListMainEnv(ctx, store.ListMainEnvParams{ProjectID: p.ID, Env: environment})
	if err != nil {
		return Change{}, fmt.Errorf("list env of %s: %w", p.ID, err)
	}
	hashes := map[string]string{}
	for _, v := range existing {
		hashes[v.Key] = v.ValueHash
	}
	key, err := s.projectKey(ctx, q, p.ID)
	if err != nil {
		return Change{}, err
	}

	var change Change
	for _, k := range sortedKeys(set) {
		h := secrets.Hash(p.ID, k, set[k])
		old, had := hashes[k]
		if had && old == h {
			continue
		}
		ct, err := key.Encrypt(environment, k, set[k])
		if err != nil {
			return Change{}, err
		}
		if err := q.SetMainEnv(ctx, store.SetMainEnvParams{ProjectID: p.ID, Env: environment, Key: k, Ciphertext: ct, ValueHash: h}); err != nil {
			return Change{}, fmt.Errorf("set %s: %w", k, err)
		}
		if had {
			change.Updated = append(change.Updated, k)
		} else {
			change.Added = append(change.Added, k)
		}
	}
	for _, k := range unset {
		n, err := q.DeleteMainEnv(ctx, store.DeleteMainEnvParams{ProjectID: p.ID, Env: environment, Key: k})
		if err != nil {
			return Change{}, fmt.Errorf("remove %s: %w", k, err)
		}
		if n > 0 {
			change.Removed = append(change.Removed, k)
		}
	}
	if count, err := q.CountMainEnv(ctx, store.CountMainEnvParams{ProjectID: p.ID, Env: environment}); err != nil {
		return Change{}, err
	} else if count > maxVars {
		return Change{}, ErrTooMany
	}
	if restart && environment == "production" && !change.Empty() {
		if _, err := bus.Enqueue(ctx, q, bus.EnvChanged, map[string]string{"project_id": p.ID}); err != nil {
			return Change{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Change{}, fmt.Errorf("commit env of %s: %w", p.ID, err)
	}
	return change, nil
}

// Effective is the decrypted environment one service receives at instance start: main variables minus the
// service's exclusions, with its overrides on top. Only the node agent calls it.
func (s *Service) Effective(ctx context.Context, projectID, serviceID, environment string) (map[string]string, error) {
	q := store.New(s.pool)
	rows, err := q.EffectiveEnv(ctx, store.EffectiveEnvParams{ProjectID: projectID, Env: environment, ServiceID: serviceID})
	if err != nil {
		return nil, fmt.Errorf("load env of %s: %w", serviceID, err)
	}
	out := map[string]string{}
	if len(rows) == 0 {
		return out, nil
	}
	key, err := s.projectKey(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	// Rows come main first, overrides last, so an override replaces the main value.
	for _, r := range rows {
		if out[r.Key], err = key.Decrypt(environment, r.Key, r.Ciphertext); err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", r.Key, err)
		}
	}
	return out, nil
}

// projectKey loads the project's data key, creating it the first time a variable is stored.
func (s *Service) projectKey(ctx context.Context, q *store.Queries, projectID string) (*secrets.ProjectKey, error) {
	wrapped, err := q.GetProjectKey(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		fresh, gerr := s.keyring.NewProjectKey(projectID)
		if gerr != nil {
			return nil, gerr
		}
		wrapped, err = q.CreateProjectKey(ctx, store.CreateProjectKeyParams{ProjectID: projectID, WrappedKey: fresh})
	}
	if err != nil {
		return nil, fmt.Errorf("load data key of %s: %w", projectID, err)
	}
	return s.keyring.ProjectKey(projectID, wrapped)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

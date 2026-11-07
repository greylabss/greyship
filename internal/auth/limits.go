package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/store"
)

var ErrInvalidLimits = errors.New("limits must be positive numbers within a sane range")

const (
	maxMemoryMB         = 1 << 20 // 1 TiB, far past any server
	maxCPUMillis        = 1024 * 1000
	maxConcurrentBuilds = 64
)

// Limits is what an admin lets one user's apps use (decisions.md 053). nil means no limit.
type Limits struct {
	MemoryMB         *int32
	CPUMillis        *int32
	ConcurrentBuilds *int32
}

func inRange(v *int32, max int32) bool { return v == nil || (*v > 0 && *v <= max) }

// SetLimits replaces a user's limits. Callers check that the requester is an admin.
func (s *Service) SetLimits(ctx context.Context, userID string, l Limits) error {
	if !inRange(l.MemoryMB, maxMemoryMB) || !inRange(l.CPUMillis, maxCPUMillis) || !inRange(l.ConcurrentBuilds, maxConcurrentBuilds) {
		return ErrInvalidLimits
	}
	q := store.New(s.pool)
	if _, err := q.GetUserByID(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNoUser
	} else if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	err := q.SetUserLimits(ctx, store.SetUserLimitsParams{MaxMemoryMb: l.MemoryMB, MaxCpuMillis: l.CPUMillis, MaxConcurrentBuilds: l.ConcurrentBuilds, UserID: userID})
	if err != nil {
		return fmt.Errorf("set limits: %w", err)
	}
	return nil
}

// Usage is what a user's apps hold now next to their limits.
type Usage struct {
	MemoryUsedMB  int64
	CPUUsedMillis int64
	Limits
}

func (s *Service) Usage(ctx context.Context, userID string) (Usage, error) {
	q := store.New(s.pool)
	team, err := q.UserOwnedTeam(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Usage{}, nil
	}
	if err != nil {
		return Usage{}, fmt.Errorf("load team of %s: %w", userID, err)
	}
	use, err := q.TeamResourceUse(ctx, team.ID)
	if err != nil {
		return Usage{}, fmt.Errorf("load resource use: %w", err)
	}
	return Usage{MemoryUsedMB: use.MemoryMb, CPUUsedMillis: use.CpuMillis, Limits: Limits{MemoryMB: team.MaxMemoryMb, CPUMillis: team.MaxCpuMillis, ConcurrentBuilds: team.MaxConcurrentBuilds}}, nil
}

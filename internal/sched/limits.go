package sched

import (
	"context"
	"fmt"

	"greyship.tech/internal/store"
)

// LimitError says an instance can't start because its team's admin-set limit would be passed (decisions.md 053).
type LimitError struct {
	Resource string // "memory" or "CPU"
	Message  string
}

func (e *LimitError) Error() string { return e.Message }

// checkLimits refuses an instance that would take a team over its memory or CPU limit. A team with no limit set
// is never refused. It runs in the transaction that records the instance, after locking the team, so concurrent
// starts can't both fit under the limit.
func checkLimits(ctx context.Context, q *store.Queries, svc store.Service) error {
	// The release command (migrations) runs once, alone and for a bounded time, before the new version starts, so it
	// isn't counted: a limit shouldn't have to leave room for it.
	if svc.Type == "release" {
		return nil
	}
	team, err := q.ProjectTeamLimits(ctx, svc.ProjectID)
	if err != nil {
		return fmt.Errorf("load limits of project %s: %w", svc.ProjectID, err)
	}
	if team.MaxMemoryMb == nil && team.MaxCpuMillis == nil {
		return nil
	}
	if err := q.LockTeam(ctx, team.ID); err != nil {
		return fmt.Errorf("lock team %s: %w", team.ID, err)
	}
	use, err := q.TeamResourceUse(ctx, team.ID)
	if err != nil {
		return fmt.Errorf("load resource use of team %s: %w", team.ID, err)
	}
	if max := team.MaxMemoryMb; max != nil && use.MemoryMb+int64(svc.MemoryMb) > int64(*max) {
		return &LimitError{Resource: "memory", Message: fmt.Sprintf(
			"memory limit reached: your apps use %d of %d MB and %s needs %d MB more. Ask the instance admin to raise your limit, or stop something",
			use.MemoryMb, *max, svc.Name, svc.MemoryMb)}
	}
	if max := team.MaxCpuMillis; max != nil && use.CpuMillis+int64(svc.CpuMillis) > int64(*max) {
		return &LimitError{Resource: "CPU", Message: fmt.Sprintf(
			"CPU limit reached: your apps use %s of %s cores and %s needs %s more. Ask the instance admin to raise your limit, or stop something",
			cores(use.CpuMillis), cores(int64(*max)), svc.Name, cores(int64(svc.CpuMillis)))}
	}
	return nil
}

func cores(millis int64) string {
	return fmt.Sprintf("%.2g", float64(millis)/1000)
}

// Package plan holds what each plan allows (spec §6–7). It is configuration in code: plans change with a release.
//
// Greyship is self-hosted (decisions.md 050): by default every team is on Unlimited, which has no build-minute
// limit. The other plans only exist on an instance that sets GREYSHIP_PLANS=on, such as the founder's.
package plan

import "time"

// Build is what a plan gets for server builds (decisions.md 039).
type Build struct {
	MachineCPUs      int // dedicated (performance) CPUs on the build machine
	MachineMemoryMB  int
	IncludedMinutes  int // per month; local builds never count
	Concurrent       int // builds of one team at a time
	Priority         int // higher is claimed first
	Timeout          time.Duration
	OverageCentsPerM float64 // 0: no overage, builds are refused past the included minutes
	CacheVolume      bool    // a per-team cache volume; without one every build starts cold
	Unlimited        bool    // no build-minute limit
}

// Unlimited is the plan of every team on an instance that doesn't use plans, and its name in teams.plan.
const Unlimited = "unlimited"

// Default is the plan a new team gets: Free where plans are on, Unlimited elsewhere.
func Default(plansOn bool) string {
	if plansOn {
		return "free"
	}
	return Unlimited
}

var builds = map[string]Build{
	"free":       {MachineCPUs: 2, MachineMemoryMB: 4096, IncludedMinutes: 100, Concurrent: 1, Priority: 0, Timeout: 10 * time.Minute},
	"pro":        {MachineCPUs: 4, MachineMemoryMB: 8192, IncludedMinutes: 500, Concurrent: 2, Priority: 10, Timeout: 15 * time.Minute, OverageCentsPerM: 1, CacheVolume: true},
	"team":       {MachineCPUs: 8, MachineMemoryMB: 16384, IncludedMinutes: 1500, Concurrent: 4, Priority: 20, Timeout: 15 * time.Minute, OverageCentsPerM: 1.5, CacheVolume: true},
	Unlimited:    {MachineCPUs: 8, MachineMemoryMB: 16384, Concurrent: 2, Priority: 0, Timeout: 30 * time.Minute, CacheVolume: true, Unlimited: true},
	"enterprise": {MachineCPUs: 8, MachineMemoryMB: 16384, IncludedMinutes: 10000, Concurrent: 8, Priority: 30, Timeout: 15 * time.Minute, OverageCentsPerM: 1.5, CacheVolume: true},
}

// Builds returns a plan's build limits; an unknown plan gets Free's.
func Builds(name string) Build {
	if b, ok := builds[name]; ok {
		return b
	}
	return builds["free"]
}

// Size is the Fly machine size name for the plan's build machine, e.g. "performance-4x".
func (b Build) Size() string {
	switch b.MachineCPUs {
	case 2:
		return "performance-2x"
	case 4:
		return "performance-4x"
	default:
		return "performance-8x"
	}
}

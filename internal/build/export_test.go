package build

import "context"

// MakeSeedForTest builds a seed right away, for integration tests.
func (s *Sandbox) MakeSeedForTest(ctx context.Context, goVersion, goarch string) error {
	return s.makeSeed(ctx, goVersion, goarch)
}

//go:build !linux

package guestinit

import "os/exec"

// wait is the plain version for running tests on macOS; instances always run on Linux.
func wait(cmd *exec.Cmd) int {
	_ = cmd.Wait()
	return exitCode(cmd.ProcessState)
}

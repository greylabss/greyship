package guestinit

import (
	"os"
	"os/exec"
	"syscall"
)

// wait returns the app's exit code. As PID 1 it also reaps every orphan the app leaves behind: it waits for
// any child, keeps going until the app itself is reaped, and drops the others.
func wait(cmd *exec.Cmd) int {
	if os.Getpid() != 1 {
		_ = cmd.Wait()
		return exitCode(cmd.ProcessState)
	}
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return 1
		}
		if pid != cmd.Process.Pid {
			continue
		}
		if ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ws.ExitStatus()
	}
}

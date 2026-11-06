package guestinit

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadConfig(t *testing.T) {
	cfg, err := ReadConfig(strings.NewReader(`{"env":{"PORT":"8080","SECRET":"s"},"args":["-v"]}` + "\n" + "ignored"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env["PORT"] != "8080" || len(cfg.Args) != 1 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if got := cfg.environ(); strings.Join(got, ",") != "PORT=8080,SECRET=s" {
		t.Fatalf("environ = %v", got)
	}
	for _, bad := range []string{"", "not json\n", `{"env":` + "\n"} {
		if _, err := ReadConfig(strings.NewReader(bad)); err == nil {
			t.Errorf("ReadConfig(%q) accepted bad input", bad)
		}
	}
}

// The test binary plays the app: TestMain below turns it into a small program when GUESTINIT_APP is set.
func TestMain(m *testing.M) {
	switch os.Getenv("GUESTINIT_APP") {
	case "env":
		_, _ = os.Stdout.WriteString(os.Getenv("HELLO") + "|" + os.Getenv("HOME") + "\n")
		os.Exit(3)
	case "wait-signal":
		_, _ = os.Stdout.WriteString("ready\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunPassesOnlyConfigEnvAndExitCode(t *testing.T) {
	t.Setenv("HOME", "/leaked")
	var out bytes.Buffer
	code, err := Run(os.Args[0], Config{Env: map[string]string{"GUESTINIT_APP": "env", "HELLO": "world"}}, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if out.String() != "world|\n" {
		t.Fatalf("app saw %q; it must get the config env and nothing else", out.String())
	}
}

func TestRunForwardsStopSignal(t *testing.T) {
	// Run the whole init as a child process so the signal can be sent to it without hitting the test itself.
	if os.Getenv("GUESTINIT_INIT") == "1" {
		code, _ := Run(os.Args[0], Config{Env: map[string]string{"GUESTINIT_APP": "wait-signal"}}, os.Stdout, os.Stderr)
		os.Exit(code)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunForwardsStopSignal$")
	cmd.Env = append(os.Environ(), "GUESTINIT_INIT=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("app never started: %q %v", line, err)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128+int(syscall.SIGTERM) {
		t.Fatalf("init exited with %v, want 143 (the app stopped by the forwarded SIGTERM)", err)
	}
}

func TestRunMissingApp(t *testing.T) {
	code, err := Run("/no/such/app", Config{}, os.Stdout, os.Stderr)
	if err == nil || code != 127 {
		t.Fatalf("code = %d, err = %v", code, err)
	}
}

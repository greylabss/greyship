// Package cli implements the grey command-line tool. cmd/greyship only wires it to the real OS.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// App holds everything the commands touch outside themselves, so tests can replace it.
type App struct {
	Out, Err    io.Writer
	In          io.Reader
	Interactive bool   // stdin is a terminal, so commands may ask questions
	Dir         string // the folder grey runs in
	ConfigPath  string
	Getenv      func(string) string
	HTTP        *http.Client
	Sleep       func(context.Context, time.Duration) error
	OpenBrowser func(url string) error
	Hostname    string
	Version     string
	GoTool      string // path to the go command; empty when Go isn't installed, so deploys build on Greyship
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// OpenBrowser opens a URL with the OS's default handler; arguments are passed separately, never through a shell.
// The URL comes from our own API (verification_uri_complete).
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start() //nolint:gosec // fixed binary, URL passed as one argument
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() //nolint:gosec // fixed binary, URL passed as one argument
	default:
		return exec.Command("xdg-open", url).Start() //nolint:gosec // fixed binary, URL passed as one argument
	}
}

// IsTerminal reports whether f is an interactive terminal rather than a pipe or file.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func DefaultApp(out, errw io.Writer, getenv func(string) string, configPath, hostname, version string) *App {
	goTool, _ := exec.LookPath("go")
	return &App{
		Out: out, Err: errw, In: os.Stdin, Interactive: IsTerminal(os.Stdin), ConfigPath: configPath, Getenv: getenv,
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Sleep: sleepCtx, OpenBrowser: OpenBrowser, Hostname: hostname, Version: version, GoTool: goTool,
	}
}

type session struct {
	cfg    Config
	api    *client
	fromCI bool
}

// session resolves the API URL and token: GREYSHIP_* env vars (CI) win over the saved config.
func (a *App) session() (session, error) {
	cfg, err := loadConfig(a.ConfigPath)
	if err != nil {
		return session{}, err
	}
	base := cfg.APIURL
	if v := a.Getenv("GREYSHIP_API_URL"); v != "" {
		base = v
	}
	if base == "" {
		base = DefaultAPIURL
	}
	token, fromCI := cfg.Token, false
	if v := a.Getenv("GREYSHIP_TOKEN"); v != "" {
		token, fromCI = v, true
	}
	return session{cfg: cfg, fromCI: fromCI, api: &client{base: base, token: token, http: a.HTTP, agent: "grey/" + a.Version}}, nil
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func NewRoot(a *App) *cobra.Command {
	var asJSON bool
	root := &cobra.Command{
		Use:           "grey",
		Short:         "Deploy Go backends to Greyship",
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.AddCommand(loginCmd(a, &asJSON), logoutCmd(a, &asJSON), whoamiCmd(a, &asJSON), initCmd(a, &asJSON), projectsCmd(a, &asJSON), deployCmd(a, &asJSON), envCmd(a, &asJSON), logsCmd(a, &asJSON), githubCmd(a, &asJSON), rollbackCmd(a, &asJSON), redeployCmd(a, &asJSON), notificationsCmd(a, &asJSON), serverCmd(a, &asJSON))
	return root
}

// friendly turns API and network errors into the one line a person needs.
func friendly(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Code == "invalid_token" || apiErr.Code == "unauthenticated") {
		return fmt.Errorf("you're not logged in (or the token was revoked). Run: grey login")
	}
	return err
}

func clientName(host string) string {
	host = strings.TrimSuffix(host, ".local")
	if host == "" {
		host = "unknown host"
	}
	return fmt.Sprintf("grey on %s (%s)", host, runtime.GOOS)
}

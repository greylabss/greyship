package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type user struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Teams []struct {
		Slug string `json:"slug"`
		Plan string `json:"plan"`
		Role string `json:"role"`
	} `json:"teams"`
	TokenTeam string `json:"token_team,omitempty"`
}

func loginCmd(a *App, asJSON *bool) *cobra.Command {
	var noBrowser, force bool
	cmd := &cobra.Command{
		Use:   "login [server]",
		Short: "Log in by approving this device in your browser",
		Long: "Logs this machine in. With no argument it uses the Greyship you last logged in to, or greyship.tech. " +
			"Give the address of a self-hosted Greyship to log in to it instead, e.g. grey login https://greyship.example.com; " +
			"grey remembers it for every later command.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if s.fromCI {
				return errors.New("GREYSHIP_TOKEN is set, so grey already uses that token; unset it to log in interactively")
			}
			switched := false
			if len(args) == 1 {
				base, err := normalizeServer(args[0])
				if err != nil {
					return err
				}
				if base != s.api.base {
					s.api.base, s.api.token, switched = base, "", true
				}
			}
			if s.api.token != "" && !force && !switched {
				var me struct{ User user }
				if err := s.api.do(ctx, "GET", "/v1/me", nil, &me); err == nil {
					fmt.Fprintf(a.Out, "Already logged in as %s. Use --force to log in again.\n", me.User.Email)
					return nil
				}
			}

			var start struct {
				DeviceCode      string `json:"device_code"`
				UserCode        string `json:"user_code"`
				VerificationURI string `json:"verification_uri"`
				Complete        string `json:"verification_uri_complete"`
				ExpiresIn       int    `json:"expires_in"`
				Interval        int    `json:"interval"`
			}
			s.api.token = ""
			if err := s.api.do(ctx, "POST", "/v1/auth/device/code", map[string]string{"client_name": clientName(a.Hostname)}, &start); err != nil {
				return friendly(err)
			}

			fmt.Fprintf(a.Out, "Your login code: %s\n", start.UserCode)
			opened := false
			if !noBrowser {
				opened = a.OpenBrowser(start.Complete) == nil
			}
			if opened {
				fmt.Fprintf(a.Out, "Opened %s\nCheck the code matches, then approve.\n", start.Complete)
			} else {
				fmt.Fprintf(a.Out, "Open %s and enter the code above.\n", start.VerificationURI)
			}
			fmt.Fprintln(a.Out, "Waiting for approval…")

			token, me, err := a.waitForApproval(ctx, s.api, start.DeviceCode, time.Duration(start.Interval)*time.Second, time.Duration(start.ExpiresIn)*time.Second)
			if err != nil {
				return err
			}
			s.cfg.Token, s.cfg.Email, s.cfg.APIURL = token, me.Email, s.api.base
			if s.cfg.APIURL == DefaultAPIURL {
				s.cfg.APIURL = ""
			}
			if err := saveConfig(a.ConfigPath, s.cfg); err != nil {
				return err
			}
			if *asJSON {
				return a.printJSON(map[string]any{"logged_in": true, "email": me.Email})
			}
			fmt.Fprintf(a.Out, "✓ Logged in as %s on %s\n", me.Email, s.api.base)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	cmd.Flags().BoolVar(&force, "force", false, "log in again even if already logged in")
	return cmd
}

// waitForApproval polls the token endpoint as RFC 8628 describes: back off on slow_down, stop on denial or expiry.
func (a *App) waitForApproval(ctx context.Context, api *client, deviceCode string, interval, expiresIn time.Duration) (string, user, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, expiresIn+5*time.Second)
	defer cancel()
	for {
		if err := a.Sleep(ctx, interval); err != nil {
			return "", user{}, errors.New("login timed out; run grey login again")
		}
		var out struct {
			AccessToken string `json:"access_token"`
			User        user   `json:"user"`
		}
		err := api.do(ctx, "POST", "/v1/auth/device/token", map[string]string{"device_code": deviceCode}, &out)
		var apiErr *APIError
		switch {
		case err == nil:
			return out.AccessToken, out.User, nil
		case errors.As(err, &apiErr) && apiErr.Code == "authorization_pending":
			continue
		case errors.As(err, &apiErr) && apiErr.Code == "slow_down":
			interval += 5 * time.Second
		case errors.As(err, &apiErr) && apiErr.Code == "access_denied":
			return "", user{}, errors.New("login denied in the browser")
		case errors.As(err, &apiErr) && apiErr.Code == "expired_token":
			return "", user{}, errors.New("the login code expired; run grey login again")
		default:
			return "", user{}, err
		}
	}
}

func logoutCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke this machine's token and forget it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			if s.fromCI {
				return errors.New("GREYSHIP_TOKEN is set; logout only removes saved logins. Revoke CI tokens in the dashboard")
			}
			if s.cfg.Token == "" {
				fmt.Fprintln(a.Out, "Not logged in.")
				return nil
			}
			// Revoke on the server first; if that fails (offline), still forget it locally.
			revokeErr := s.api.do(cmd.Context(), "DELETE", "/v1/tokens/current", nil, nil)
			s.cfg.Token, s.cfg.Email = "", ""
			if err := saveConfig(a.ConfigPath, s.cfg); err != nil {
				return err
			}
			if *asJSON {
				return a.printJSON(map[string]any{"logged_out": true, "revoked": revokeErr == nil})
			}
			if revokeErr != nil {
				fmt.Fprintf(a.Out, "Logged out locally. Couldn't revoke the token on the server (%v); revoke it in the dashboard.\n", revokeErr)
				return nil
			}
			fmt.Fprintln(a.Out, "✓ Logged out and token revoked")
			return nil
		},
	}
}

func whoamiCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who you're logged in as",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			if s.api.token == "" {
				return errors.New("you're not logged in. Run: grey login")
			}
			var me struct{ User user }
			if err := s.api.do(cmd.Context(), "GET", "/v1/me", nil, &me); err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(me.User)
			}
			teams := make([]string, 0, len(me.User.Teams))
			for _, t := range me.User.Teams {
				teams = append(teams, fmt.Sprintf("%s (%s, %s)", t.Slug, t.Role, t.Plan))
			}
			fmt.Fprintf(a.Out, "%s on %s\nteams: %s\n", me.User.Email, s.api.base, strings.Join(teams, ", "))
			return nil
		},
	}
}

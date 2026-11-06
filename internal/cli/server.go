package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func serverCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "server [address]",
		Short: "Show or set which Greyship server grey talks to",
		Long: "Without an address, shows the server grey uses. With one, e.g. grey server http://203.0.113.7:8000 or " +
			"grey server https://greyship.example.com, points grey at that server without logging in; grey login then logs in " +
			"to it. The installer a self-hosted Greyship hands out does this for you.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(a.ConfigPath)
			if err != nil {
				return err
			}
			current := cfg.APIURL
			if current == "" {
				current = DefaultAPIURL
			}
			if len(args) == 0 {
				if *asJSON {
					return a.printJSON(map[string]any{"server": current, "logged_in": cfg.Token != ""})
				}
				state := "not logged in"
				if cfg.Token != "" {
					state = "logged in as " + cfg.Email
				}
				fmt.Fprintf(a.Out, "%s (%s)\n", current, state)
				return nil
			}
			base, err := normalizeServer(args[0])
			if err != nil {
				return err
			}
			if base != current {
				cfg.Token, cfg.Email = "", ""
			}
			cfg.APIURL = base
			if base == DefaultAPIURL {
				cfg.APIURL = ""
			}
			if err := saveConfig(a.ConfigPath, cfg); err != nil {
				return err
			}
			if *asJSON {
				return a.printJSON(map[string]any{"server": base})
			}
			fmt.Fprintf(a.Out, "grey now uses %s. Next: grey login\n", base)
			if unencrypted(base) {
				fmt.Fprintln(a.Err, "warning: this server isn't using HTTPS, so your login token will cross the network in the clear. Add a domain on the server to fix it: greyship-admin domain")
			}
			return nil
		},
	}
}

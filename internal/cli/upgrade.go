package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

const installerURL = "https://raw.githubusercontent.com/greylabss/greyship/main/deploy/cli/install.sh"

func upgradeCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Update grey to the latest release",
		Long: "Downloads the latest grey release, checks its checksum and replaces this program with it. Your login and the " +
			"server grey uses are kept. Give a version, e.g. grey upgrade v0.1.2, to install that one instead. To update the " +
			"server itself, run greyship-admin upgrade on it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version := ""
			if len(args) == 1 {
				version = args[0]
			}
			if !*asJSON {
				fmt.Fprintf(a.Out, "grey %s: looking for the latest release\n", a.Version)
			}
			if err := a.Upgrade(cmd.Context(), version); err != nil {
				return fmt.Errorf("upgrade grey: %w", err)
			}
			if *asJSON {
				return a.printJSON(map[string]any{"upgraded": true})
			}
			return nil
		},
	}
}

// upgradeGrey runs the same installer as a first install, into the folder this program lives in. The folder and
// version go in as environment variables, never into a shell command.
func upgradeGrey(ctx context.Context, version string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this program: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return fmt.Errorf("find this program: %w", err)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", `curl -fsSL "$GREY_INSTALLER_URL" | sh`) //nolint:gosec // fixed command; values arrive as environment variables
	cmd.Env = append(os.Environ(), "GREY_INSTALLER_URL="+installerURL, "GREY_INSTALL_DIR="+filepath.Dir(self))
	if version != "" {
		cmd.Env = append(cmd.Env, "GREY_VERSION="+version)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

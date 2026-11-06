package cli

import (
	"context"
	"fmt"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type notifySetting struct {
	Kind  string `json:"kind"`
	Email bool   `json:"email"`
	Push  bool   `json:"push"`
}

var notifyEvents = []string{"deploy.succeeded", "deploy.failed", "project.created"}

func notificationsCmd(a *App, asJSON *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "Show which notifications you get by email and browser push",
		Long: "Shows your notification settings. Change them with grey notifications email|push on|off [event], where event is " +
			"deploy.succeeded, deploy.failed or project.created (all of them when left out). Browser push is turned on in the dashboard.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.notifications(cmd.Context(), *asJSON, "", "", "") },
	}
	for _, channel := range []string{"email", "push"} {
		cmd.AddCommand(&cobra.Command{
			Use:       channel + " on|off [event]",
			Short:     "Turn " + channel + " notifications on or off",
			Args:      cobra.RangeArgs(1, 2),
			ValidArgs: []string{"on", "off"},
			RunE: func(cmd *cobra.Command, args []string) error {
				event := ""
				if len(args) == 2 {
					event = args[1]
				}
				return a.notifications(cmd.Context(), *asJSON, channel, args[0], event)
			},
		})
	}
	return cmd
}

func (a *App) notifications(ctx context.Context, asJSON bool, channel, state, event string) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	var out struct {
		Settings      []notifySetting `json:"settings"`
		PushAvailable bool            `json:"push_available"`
	}
	if err := s.api.do(ctx, "GET", "/v1/me/notifications/settings", nil, &out); err != nil {
		return friendly(err)
	}
	if channel != "" {
		if state != "on" && state != "off" {
			return fmt.Errorf("say on or off, e.g. grey notifications %s off", channel)
		}
		if event != "" && !slices.Contains(notifyEvents, event) {
			return fmt.Errorf("unknown event %q; use deploy.succeeded, deploy.failed or project.created", event)
		}
		for i := range out.Settings {
			if event != "" && out.Settings[i].Kind != event {
				continue
			}
			if channel == "email" {
				out.Settings[i].Email = state == "on"
			} else {
				out.Settings[i].Push = state == "on"
			}
		}
		if err := s.api.do(ctx, "PATCH", "/v1/me/notifications/settings", map[string]any{"settings": out.Settings}, &out); err != nil {
			return friendly(err)
		}
	}
	if asJSON {
		return a.printJSON(out)
	}
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EVENT\tEMAIL\tPUSH")
	for _, st := range out.Settings {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", st.Kind, onOff(st.Email), onOff(st.Push))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !out.PushAvailable {
		fmt.Fprintln(a.Out, "\nBrowser push isn't set up on this Greyship yet.")
	} else {
		fmt.Fprintln(a.Out, "\nPush reaches the browsers where you turned notifications on in the dashboard.")
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

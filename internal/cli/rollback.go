package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func rollbackCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback [deployment-id]",
		Short: "Go back to an earlier deployment's binaries",
		Long: "Puts an earlier deployment's binaries live again, with the project's current env vars. Nothing is rebuilt and " +
			"the release command doesn't run. Without an id, goes back to the deployment that was live before the current one.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return a.redeploy(cmd.Context(), *asJSON, target, true)
		},
	}
}

func redeployCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "redeploy",
		Short: "Restart the live deployment on fresh instances",
		Long:  "Starts the live deployment's binaries again on fresh instances, picking up env var changes. Nothing is rebuilt.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.redeploy(cmd.Context(), *asJSON, "", false)
		},
	}
}

func (a *App) redeploy(ctx context.Context, asJSON bool, target string, rollback bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, _, err := a.linkedProject()
	if err != nil {
		return err
	}
	var list struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := s.api.do(ctx, "GET", "/v1/projects/"+url.PathEscape(l.ID)+"/deployments?limit=100", nil, &list); err != nil {
		return friendly(err)
	}
	from, err := pickDeployment(list.Deployments, target, rollback)
	if err != nil {
		return err
	}
	var out struct{ Deployment deployment }
	if err := s.api.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(from.ID)+"/redeploy", nil, &out); err != nil {
		return friendly(err)
	}
	if !asJSON {
		verb := "Redeploying"
		if rollback {
			verb = "Rolling back to"
		}
		what := short(from.ID)
		if sha := from.CommitSHA; sha != nil && *sha != "" {
			what += " (commit " + (*sha)[:min(7, len(*sha))] + ")"
		}
		fmt.Fprintf(a.Out, "%s %s\nDeployment %s queued:\n", verb, what, out.Deployment.ID)
	}
	return a.followDeploy(ctx, s.api, out.Deployment.ID, asJSON)
}

// pickDeployment finds what to redeploy in a newest-first list: the live one, the one given by id (or id prefix),
// or for a bare rollback the newest one that went live before the current one.
func pickDeployment(list []deployment, target string, rollback bool) (deployment, error) {
	if target != "" {
		var found []deployment
		for _, d := range list {
			if strings.HasPrefix(d.ID, target) {
				found = append(found, d)
			}
		}
		switch len(found) {
		case 0:
			return deployment{}, fmt.Errorf("no recent deployment starts with %s", target)
		case 1:
			return found[0], nil
		default:
			return deployment{}, fmt.Errorf("%s matches %d deployments; give more of the id", target, len(found))
		}
	}
	live := -1
	for i, d := range list {
		if d.Status == "ready" {
			live = i
			break
		}
	}
	if live < 0 {
		return deployment{}, errors.New("nothing is live yet; deploy first with grey deploy")
	}
	if !rollback {
		return list[live], nil
	}
	for _, d := range list[live+1:] {
		if d.Status == "superseded" {
			return d, nil
		}
	}
	return deployment{}, errors.New("there's no earlier deployment to go back to")
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

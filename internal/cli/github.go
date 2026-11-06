package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func githubCmd(a *App, asJSON *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "github", Short: "Deploy from GitHub: every push to the connected branch deploys"}

	install := &cobra.Command{
		Use:   "install",
		Short: "Open the page that installs the Greyship GitHub app for your team",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.githubInstall(cmd.Context(), *asJSON)
		},
	}
	repos := &cobra.Command{
		Use:   "repos",
		Short: "List the repositories the Greyship GitHub app can read",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.githubRepos(cmd.Context(), *asJSON)
		},
	}
	var branch string
	connect := &cobra.Command{
		Use:   "connect OWNER/REPO",
		Short: "Deploy this folder's project on every push to a branch of OWNER/REPO",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.githubConnect(cmd.Context(), *asJSON, args[0], branch)
		},
	}
	connect.Flags().StringVar(&branch, "branch", "", "branch that deploys (default: the repository's default branch)")
	var importBranch string
	importCmd := &cobra.Command{
		Use:   "import URL",
		Short: "Deploy a public GitHub repository from its link; later commits deploy with grey github deploy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.githubImportURL(cmd.Context(), *asJSON, args[0], importBranch)
		},
	}
	importCmd.Flags().StringVar(&importBranch, "branch", "", "branch to deploy (default: the repository's default branch)")
	deployLatest := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the latest commit of this project's connected GitHub branch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.githubDeployLatest(cmd.Context(), *asJSON)
		},
	}
	cmd.AddCommand(install, repos, connect, importCmd, deployLatest)
	return cmd
}

func (a *App) githubInstall(ctx context.Context, asJSON bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	team, err := tokenTeam(ctx, s.api)
	if err != nil {
		return err
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := s.api.do(ctx, "GET", "/v1/teams/"+url.PathEscape(team)+"/github/install", nil, &out); err != nil {
		return friendly(err)
	}
	if asJSON {
		return a.printJSON(out)
	}
	fmt.Fprintf(a.Out, "Install the Greyship GitHub app for %s and pick the repositories it may read:\n  %s\n", team, out.URL)
	if a.OpenBrowser != nil && a.Interactive {
		_ = a.OpenBrowser(out.URL)
	}
	return nil
}

func (a *App) githubRepos(ctx context.Context, asJSON bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	team, err := tokenTeam(ctx, s.api)
	if err != nil {
		return err
	}
	var out struct {
		Repos []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		} `json:"repos"`
	}
	if err := s.api.do(ctx, "GET", "/v1/teams/"+url.PathEscape(team)+"/github/repos", nil, &out); err != nil {
		return friendly(err)
	}
	if asJSON {
		return a.printJSON(out)
	}
	if len(out.Repos) == 0 {
		fmt.Fprintln(a.Out, "The app can't read any repositories yet; add some in its GitHub settings (grey github install).")
		return nil
	}
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, r := range out.Repos {
		vis := "public"
		if r.Private {
			vis = "private"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.FullName, r.DefaultBranch, vis)
	}
	return tw.Flush()
}

func (a *App) githubConnect(ctx context.Context, asJSON bool, repo, branch string) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, _, err := a.linkedProject()
	if err != nil {
		return err
	}
	var out struct {
		Repo struct {
			FullName string `json:"full_name"`
			Branch   string `json:"branch"`
		} `json:"repo"`
	}
	if err := s.api.do(ctx, "POST", "/v1/projects/"+url.PathEscape(l.ID)+"/github", map[string]string{"repo": repo, "branch": branch}, &out); err != nil {
		return friendly(err)
	}
	if asJSON {
		return a.printJSON(out)
	}
	fmt.Fprintf(a.Out, "✓ %s/%s deploys on every push to %s of %s\n", l.Team, l.Project, out.Repo.Branch, out.Repo.FullName)
	return nil
}

func (a *App) githubImportURL(ctx context.Context, asJSON bool, link, branch string) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	team, err := tokenTeam(ctx, s.api)
	if err != nil {
		return err
	}
	var out struct {
		Project      project `json:"project"`
		DeploymentID string  `json:"deployment_id"`
	}
	if err := s.api.do(ctx, "POST", "/v1/teams/"+url.PathEscape(team)+"/github/import-url", map[string]string{"url": link, "branch": branch}, &out); err != nil {
		return friendly(err)
	}
	if _, _, err := findLink(a.Dir); errors.Is(err, errNoLink) {
		if _, err := writeLink(a.Dir, Link{Team: out.Project.Team, Project: out.Project.Slug, ID: out.Project.ID}); err != nil {
			return err
		}
	}
	if !asJSON {
		fmt.Fprintf(a.Out, "Created %s/%s from %s\nDeployment %s queued:\n", out.Project.Team, out.Project.Slug, link, out.DeploymentID)
	}
	return a.followDeploy(ctx, s.api, out.DeploymentID, asJSON)
}

func (a *App) githubDeployLatest(ctx context.Context, asJSON bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, _, err := a.linkedProject()
	if err != nil {
		return err
	}
	var out struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := s.api.do(ctx, "POST", "/v1/projects/"+url.PathEscape(l.ID)+"/github/deploy", nil, &out); err != nil {
		return friendly(err)
	}
	if !asJSON {
		fmt.Fprintf(a.Out, "Deployment %s queued:\n", out.DeploymentID)
	}
	return a.followDeploy(ctx, s.api, out.DeploymentID, asJSON)
}

// followDeploy follows a deployment that someone else's source started (GitHub), the way grey deploy does.
func (a *App) followDeploy(ctx context.Context, api *client, id string, asJSON bool) error {
	end, err := a.followLog(ctx, api, id, !asJSON)
	if err != nil {
		return err
	}
	if asJSON {
		return a.printJSON(map[string]any{"deployment_id": id, "build": end})
	}
	return a.printBuildEnd(end)
}

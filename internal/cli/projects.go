package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

const linkPath = ".greyship/project.json"

// Link is .greyship/project.json (spec F-10b). It holds no secrets, so it is meant to be committed.
type Link struct {
	Team    string `json:"team"`
	Project string `json:"project"`
	ID      string `json:"id"`
}

var (
	errNoLink   = errors.New("this folder isn't linked to a project; run grey init")
	errNoAnswer = errors.New("no answer given")
)

// findLink looks for .greyship/project.json in dir and its parents, like git looks for .git.
func findLink(dir string) (Link, string, error) {
	for {
		path := filepath.Join(dir, linkPath)
		b, err := os.ReadFile(path) //nolint:gosec // path is built from the working directory, not request input
		if err == nil {
			var l Link
			if err := json.Unmarshal(b, &l); err != nil || l.ID == "" {
				return Link{}, path, fmt.Errorf("%s is not a valid project link; delete it and run grey init", path)
			}
			return l, path, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return Link{}, path, fmt.Errorf("read %s: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Link{}, "", errNoLink
		}
		dir = parent
	}
}

func writeLink(dir string, l Link) (string, error) {
	path := filepath.Join(dir, linkPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // committed project folder, readable like the rest of the repo
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode project link: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil { //nolint:gosec // no secrets inside; meant to be committed
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

type project struct {
	ID        string `json:"id"`
	Team      string `json:"team"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
}

// tokenTeam is the team the current token works in; projects are created and listed there.
func tokenTeam(ctx context.Context, api *client) (string, error) {
	if api.token == "" {
		return "", errors.New("you're not logged in. Run: grey login")
	}
	var me struct{ User user }
	if err := api.do(ctx, "GET", "/v1/me", nil, &me); err != nil {
		return "", friendly(err)
	}
	if me.User.TokenTeam == "" {
		return "", errors.New("couldn't tell which team this token belongs to; run grey login again")
	}
	return me.User.TokenTeam, nil
}

func initCmd(a *App, asJSON *bool) *cobra.Command {
	var name string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a project for this folder and link the folder to it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if l, path, err := findLink(a.Dir); err == nil {
				if *asJSON {
					return a.printJSON(map[string]any{"created": false, "link": l, "path": path})
				}
				fmt.Fprintf(a.Out, "Already linked to %s/%s (%s).\n", l.Team, l.Project, path)
				return nil
			} else if !errors.Is(err, errNoLink) {
				return err
			}

			s, err := a.session()
			if err != nil {
				return err
			}
			team, err := tokenTeam(ctx, s.api)
			if err != nil {
				return err
			}
			in := bufio.NewReader(a.In)
			root := moduleRoot(a.Dir)
			if name == "" {
				name = inferName(root)
				if a.Interactive && !yes {
					answer, err := ask(a, in, fmt.Sprintf("Project name (%s): ", name))
					if err != nil && !errors.Is(err, errNoAnswer) {
						return err
					}
					if answer != "" {
						name = answer
					}
				}
			}

			p, created, err := a.createOrLink(ctx, s.api, in, team, name, yes)
			if err != nil {
				return err
			}
			path, err := writeLink(root, Link{Team: p.Team, Project: p.Slug, ID: p.ID})
			if err != nil {
				return err
			}
			if *asJSON {
				return a.printJSON(map[string]any{"created": created, "project": p, "path": path})
			}
			if created {
				fmt.Fprintf(a.Out, "✓ Created project %s in team %s\n", p.Slug, p.Team)
			}
			fmt.Fprintf(a.Out, "✓ Linked this folder to %s/%s\n  %s holds no secrets; commit it so teammates and CI deploy to the same project.\n", p.Team, p.Slug, linkPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (default: from go.mod, else the folder name)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask; link to an existing project with the same name")
	return cmd
}

// createProject creates the project, or returns the team's existing project when the name is taken.
func createProject(ctx context.Context, api *client, team, name string) (created, existing project, err error) {
	var out struct{ Project project }
	err = api.do(ctx, "POST", "/v1/teams/"+url.PathEscape(team)+"/projects", map[string]string{"name": name}, &out)
	if err == nil {
		return out.Project, project{}, nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "slug_taken" {
		return project{}, project{}, friendly(err)
	}
	var conflict struct{ Project project }
	if err := apiErr.Decode(&conflict); err != nil || conflict.Project.ID == "" {
		return project{}, project{}, apiErr
	}
	return project{}, conflict.Project, nil
}

// createOrLink is grey init's flow: the person chose the name, so linking to an existing project with it is the likely intent.
func (a *App) createOrLink(ctx context.Context, api *client, in *bufio.Reader, team, name string, yes bool) (project, bool, error) {
	created, existing, err := createProject(ctx, api, team, name)
	if err != nil || existing.ID == "" {
		return created, err == nil, err
	}
	if yes {
		return existing, false, nil
	}
	notAsked := fmt.Errorf("%s/%s already exists; run grey init --yes to link this folder to it, or --name to pick another name", existing.Team, existing.Slug)
	if !a.Interactive {
		return project{}, false, notAsked
	}
	answer, err := ask(a, in, fmt.Sprintf("%s/%s already exists. Link this folder to it? [Y/n] ", existing.Team, existing.Slug))
	if errors.Is(err, errNoAnswer) {
		return project{}, false, notAsked
	}
	if err != nil {
		return project{}, false, err
	}
	if ans := strings.ToLower(answer); ans != "" && ans != "y" && ans != "yes" {
		return project{}, false, errors.New("nothing changed; pick another name with grey init --name")
	}
	return existing, false, nil
}

// ensureProject is grey deploy's flow (decision 026): use the folder's link, or create the project from the inferred
// name without asking. A name clash never deploys into the existing project silently.
func (a *App) ensureProject(ctx context.Context, api *client, in *bufio.Reader) (Link, bool, error) {
	l, _, err := findLink(a.Dir)
	if err == nil || !errors.Is(err, errNoLink) {
		return l, false, err
	}
	team, err := tokenTeam(ctx, api)
	if err != nil {
		return Link{}, false, err
	}
	root := moduleRoot(a.Dir)
	name := inferName(root)
	created, existing, err := createProject(ctx, api, team, name)
	if err != nil {
		return Link{}, false, err
	}
	p, isNew := created, true
	if existing.ID != "" {
		if p, isNew, err = a.resolveClash(ctx, api, in, team, name, existing); err != nil {
			return Link{}, false, err
		}
	}
	l = Link{Team: p.Team, Project: p.Slug, ID: p.ID}
	if _, err := writeLink(root, l); err != nil {
		return Link{}, false, err
	}
	return l, isNew, nil
}

// resolveClash asks, defaulting to no; no means a new project with the first free numbered name.
func (a *App) resolveClash(ctx context.Context, api *client, in *bufio.Reader, team, name string, existing project) (project, bool, error) {
	notAsked := fmt.Errorf("%s/%s already exists and this folder isn't linked to it; run grey init --yes to deploy to it, or grey init --name to pick another name", existing.Team, existing.Slug)
	if !a.Interactive {
		return project{}, false, notAsked
	}
	answer, err := ask(a, in, fmt.Sprintf("%s/%s already exists. Deploy to it? [y/N] ", existing.Team, existing.Slug))
	if errors.Is(err, errNoAnswer) {
		return project{}, false, notAsked
	}
	if err != nil {
		return project{}, false, err
	}
	if ans := strings.ToLower(answer); ans == "y" || ans == "yes" {
		return existing, false, nil
	}
	for i := 2; i <= 20; i++ {
		created, taken, err := createProject(ctx, api, team, fmt.Sprintf("%s-%d", name, i))
		if err != nil {
			return project{}, false, err
		}
		if taken.ID == "" {
			return created, true, nil
		}
	}
	return project{}, false, errors.New("couldn't find a free project name; run grey init --name")
}

// moduleRoot is the nearest folder at or above dir with a go.mod; the project link lives there. Without one, dir itself.
func moduleRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// inferName takes the last element of the go.mod module path, skipping a /vN major-version suffix (F-10a).
// Without a readable module line it falls back to the folder name.
func inferName(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // the user's own go.mod, not request input
	if err != nil {
		return filepath.Base(root)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		parts := strings.Split(strings.Trim(fields[1], `"`+"`"), "/")
		last := parts[len(parts)-1]
		if len(parts) > 1 && majorVersion.MatchString(last) {
			last = parts[len(parts)-2]
		}
		if last != "" {
			return last
		}
	}
	return filepath.Base(root)
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// ask reads one line. End of input (stdin closed or /dev/null, which also looks like a terminal) is errNoAnswer, so callers fall back to their non-interactive behaviour.
func ask(a *App, in *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(a.Out, prompt)
	line, err := in.ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		fmt.Fprintln(a.Out)
		return "", errNoAnswer
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func projectsCmd(a *App, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "List the projects in your team",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.session()
			if err != nil {
				return err
			}
			team, err := tokenTeam(ctx, s.api)
			if err != nil {
				return err
			}
			var out struct{ Projects []project }
			if err := s.api.do(ctx, "GET", "/v1/teams/"+url.PathEscape(team)+"/projects", nil, &out); err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(out.Projects)
			}
			if len(out.Projects) == 0 {
				fmt.Fprintf(a.Out, "No projects in %s yet. Run grey init in a project folder.\n", team)
				return nil
			}
			linked, _, _ := findLink(a.Dir)
			tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSLUG\tCREATED\t")
			for _, p := range out.Projects {
				mark := ""
				if p.ID == linked.ID {
					mark = "← this folder"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, p.Slug, shortDate(p.CreatedAt), mark)
			}
			return tw.Flush()
		},
	}
}

func shortDate(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Local().Format("2006-01-02")
}

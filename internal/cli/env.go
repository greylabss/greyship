package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"greyship.tech/internal/dotenv"
	"greyship.tech/internal/secrets"
)

const prodEnvFile = ".env.prod"

type storedVar struct {
	Key       string `json:"key"`
	Hash      string `json:"hash"`
	UpdatedAt string `json:"updated_at"`
}

// envDiff compares local values with stored ones by hash; no value is sent or received to compute it.
type envDiff struct {
	Added      []string
	Changed    []string
	OnlyStored []string
	Same       int
}

func (d envDiff) changes(prune bool) bool {
	return len(d.Added)+len(d.Changed) > 0 || prune && len(d.OnlyStored) > 0
}

func diffEnv(projectID string, local map[string]string, stored []storedVar) envDiff {
	have := map[string]string{}
	for _, v := range stored {
		have[v.Key] = v.Hash
	}
	var d envDiff
	for _, k := range sortedKeys(local) {
		h, ok := have[k]
		switch {
		case !ok:
			d.Added = append(d.Added, k)
		case h != secrets.Hash(projectID, k, local[k]):
			d.Changed = append(d.Changed, k)
		default:
			d.Same++
		}
	}
	for _, v := range stored {
		if _, ok := local[v.Key]; !ok {
			d.OnlyStored = append(d.OnlyStored, v.Key)
		}
	}
	return d
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printDiff shows keys only (F-40a), never values.
func (a *App) printDiff(file string, d envDiff, prune bool) {
	fmt.Fprintf(a.Out, "%s vs production:\n", file)
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, k := range d.Added {
		fmt.Fprintf(tw, "  + %s\tonly in file → will be added\n", k)
	}
	for _, k := range d.Changed {
		fmt.Fprintf(tw, "  ~ %s\tdifferent → will be updated\n", k)
	}
	if d.Same > 0 {
		fmt.Fprintf(tw, "  = %d keys match\t\n", d.Same)
	}
	for _, k := range d.OnlyStored {
		if prune {
			fmt.Fprintf(tw, "  - %s\tonly stored → will be removed\n", k)
		} else {
			fmt.Fprintf(tw, "  · %s\tonly stored → kept\n", k)
		}
	}
	_ = tw.Flush()
}

func listEnv(ctx context.Context, api *client, projectID, environment string) ([]storedVar, error) {
	var out struct{ Vars []storedVar }
	err := api.do(ctx, "GET", "/v1/projects/"+url.PathEscape(projectID)+"/env?env="+url.QueryEscape(environment), nil, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, fmt.Errorf("this folder's project (%s) isn't visible to your login; it may be in another team. Delete %s to deploy it as a new project", projectID, linkPath)
	}
	return out.Vars, friendly(err)
}

type applied struct {
	Added   []string `json:"added"`
	Updated []string `json:"updated"`
	Removed []string `json:"removed"`
}

func applyEnv(ctx context.Context, api *client, projectID, environment string, set map[string]string, unset []string, restart bool) (applied, error) {
	var out applied
	body := map[string]any{"env": environment, "set": set, "unset": unset, "restart": restart}
	err := api.do(ctx, "PATCH", "/v1/projects/"+url.PathEscape(projectID)+"/env", body, &out)
	return out, friendly(err)
}

// confirm asks y/N; --yes answers for CI, and without a terminal there is no default yes.
func (a *App) confirm(in *bufio.Reader, prompt string, yes bool) error {
	if yes {
		return nil
	}
	if !a.Interactive {
		return errors.New("this changes production variables; run again with --yes to confirm")
	}
	answer, err := ask(a, in, prompt+" [y/N] ")
	if errors.Is(err, errNoAnswer) {
		return errors.New("not confirmed; nothing changed (pass --yes to confirm without a prompt)")
	}
	if err != nil {
		return err
	}
	if a := strings.ToLower(answer); a != "y" && a != "yes" {
		return errors.New("nothing changed")
	}
	return nil
}

// warnTrackedEnv implements F-40b: env files committed to git are a leak waiting to happen.
func (a *App) warnTrackedEnv(ctx context.Context, root string) {
	for _, name := range []string{".env", ".env.local", prodEnvFile} {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", name) //nolint:gosec // fixed binary and names
		if cmd.Run() == nil {
			fmt.Fprintf(a.Err, "⚠ %s is tracked by git: its secrets are in your repository history. Add it to .gitignore and rotate what's in it.\n", name)
		}
	}
}

func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the user's own env file
	if err != nil {
		return nil, err
	}
	values, err := dotenv.Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return values, nil
}

func writeEnvFile(path string, values map[string]string) error {
	return os.WriteFile(path, []byte(dotenv.Format(values)), 0o600)
}

func (a *App) linkedProject() (Link, string, error) {
	l, _, err := findLink(a.Dir)
	if errors.Is(err, errNoLink) {
		return Link{}, "", errors.New("this folder isn't linked to a project yet; run grey deploy (or grey init) first")
	}
	return l, moduleRoot(a.Dir), err
}

func envCmd(a *App, asJSON *bool) *cobra.Command {
	var environment string
	cmd := &cobra.Command{Use: "env", Short: "Manage the project's environment variables"}
	cmd.PersistentFlags().StringVar(&environment, "env", "production", "production or preview")

	set := &cobra.Command{
		Use:   "set KEY=VALUE...",
		Short: "Set variables; services that use them restart with the new values",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values := map[string]string{}
			for _, arg := range args {
				k, v, ok := strings.Cut(arg, "=")
				if !ok || k == "" {
					return fmt.Errorf("%q: use KEY=VALUE", arg)
				}
				values[k] = v
			}
			return a.envApply(cmd.Context(), *asJSON, environment, values, nil)
		},
	}
	rm := &cobra.Command{
		Use:   "rm KEY...",
		Short: "Remove variables",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.envApply(cmd.Context(), *asJSON, environment, nil, args)
		},
	}
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List variable names (values stay hidden)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.session()
			if err != nil {
				return err
			}
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			vars, err := listEnv(cmd.Context(), s.api, l.ID, environment)
			if err != nil {
				return err
			}
			if *asJSON {
				return a.printJSON(map[string]any{"vars": vars})
			}
			if len(vars) == 0 {
				fmt.Fprintf(a.Out, "No %s variables yet. Set one with grey env set KEY=VALUE.\n", environment)
				return nil
			}
			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			for _, v := range vars {
				fmt.Fprintf(tw, "%s\tupdated %s\n", v.Key, v.UpdatedAt)
			}
			return tw.Flush()
		},
	}
	var yes bool
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Download the stored values into .env.prod (the only command that downloads secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.envPull(cmd.Context(), environment, yes)
		},
	}
	pull.Flags().BoolVar(&yes, "yes", false, "overwrite .env.prod without asking")
	var prune bool
	push := &cobra.Command{
		Use:   "push [file]",
		Short: "Load a whole file (default .env.prod), after showing which keys change",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := prodEnvFile
			if len(args) == 1 {
				file = args[0]
			}
			return a.envPush(cmd.Context(), *asJSON, environment, file, yes, prune)
		},
	}
	push.Flags().BoolVar(&yes, "yes", false, "don't ask before changing variables")
	push.Flags().BoolVar(&prune, "prune", false, "also remove stored variables that aren't in the file")
	cmd.AddCommand(set, rm, ls, pull, push)
	return cmd
}

func (a *App) envApply(ctx context.Context, asJSON bool, environment string, set map[string]string, unset []string) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, _, err := a.linkedProject()
	if err != nil {
		return err
	}
	res, err := applyEnv(ctx, s.api, l.ID, environment, set, unset, true)
	if err != nil {
		return err
	}
	if asJSON {
		return a.printJSON(res)
	}
	a.printApplied(res, environment)
	return nil
}

func (a *App) printApplied(res applied, environment string) {
	n := len(res.Added) + len(res.Updated) + len(res.Removed)
	if n == 0 {
		fmt.Fprintln(a.Out, "Nothing changed.")
		return
	}
	for _, k := range res.Added {
		fmt.Fprintf(a.Out, "+ %s\n", k)
	}
	for _, k := range res.Updated {
		fmt.Fprintf(a.Out, "~ %s\n", k)
	}
	for _, k := range res.Removed {
		fmt.Fprintf(a.Out, "- %s\n", k)
	}
	if environment == "production" {
		fmt.Fprintln(a.Out, "✓ Saved. Running services restart with the new values; nothing is rebuilt.")
	} else {
		fmt.Fprintln(a.Out, "✓ Saved.")
	}
}

func (a *App) envPull(ctx context.Context, environment string, yes bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, root, err := a.linkedProject()
	if err != nil {
		return err
	}
	path := filepath.Join(root, prodEnvFile)
	if _, err := os.Stat(path); err == nil {
		if err := a.confirm(bufio.NewReader(a.In), path+" exists. Overwrite it?", yes); err != nil {
			return err
		}
	}
	var out struct{ Values map[string]string }
	if err := s.api.do(ctx, "GET", "/v1/projects/"+url.PathEscape(l.ID)+"/env/values?env="+url.QueryEscape(environment), nil, &out); err != nil {
		return friendly(err)
	}
	if err := writeEnvFile(path, out.Values); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "✓ Wrote %d variables to %s (readable only by you). Keep it out of git.\n", len(out.Values), path)
	a.warnTrackedEnv(ctx, root)
	return nil
}

func (a *App) envPush(ctx context.Context, asJSON bool, environment, file string, yes, prune bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, root, err := a.linkedProject()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(a.Dir, file)
	}
	local, err := readEnvFile(file)
	if err != nil {
		return err
	}
	stored, err := listEnv(ctx, s.api, l.ID, environment)
	if err != nil {
		return err
	}
	d := diffEnv(l.ID, local, stored)
	if !d.changes(prune) {
		fmt.Fprintln(a.Out, "Nothing to change: the stored variables already match.")
		return nil
	}
	a.printDiff(filepath.Base(file), d, prune)
	if err := a.confirm(bufio.NewReader(a.In), "Continue?", yes); err != nil {
		return err
	}
	set := map[string]string{}
	for _, k := range append(append([]string{}, d.Added...), d.Changed...) {
		set[k] = local[k]
	}
	var unset []string
	if prune {
		unset = d.OnlyStored
	}
	res, err := applyEnv(ctx, s.api, l.ID, environment, set, unset, true)
	if err != nil {
		return err
	}
	a.warnTrackedEnv(ctx, root)
	if asJSON {
		return a.printJSON(res)
	}
	a.printApplied(res, environment)
	return nil
}

// syncDeployEnv is the env part of grey deploy (F-40a–F-40c). It compares .env.prod with the stored variables,
// shows a key-only diff and sends only added and changed values, without restarting: the deploy rolls everything.
// With no .env.prod it offers, once and only in a terminal, to download the stored ones.
func (a *App) syncDeployEnv(ctx context.Context, api *client, l Link, root string, created, yes, prune bool, in *bufio.Reader) error {
	a.warnTrackedEnv(ctx, root)
	path := filepath.Join(root, prodEnvFile)
	local, err := readEnvFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if created || yes || !a.Interactive {
			return nil
		}
		stored, err := listEnv(ctx, api, l.ID, "production")
		if err != nil || len(stored) == 0 {
			return err
		}
		answer, err := ask(a, in, fmt.Sprintf("No %s here. Download the %d production variables to it? [y/N] ", prodEnvFile, len(stored)))
		if err != nil || (strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes") {
			return nil
		}
		return a.envPull(ctx, "production", true)
	}
	if err != nil {
		return err
	}
	stored, err := listEnv(ctx, api, l.ID, "production")
	if err != nil {
		return err
	}
	d := diffEnv(l.ID, local, stored)
	if !d.changes(prune) {
		if d.Same > 0 {
			fmt.Fprintf(a.Out, "%s: %d keys match production.\n\n", prodEnvFile, d.Same)
		}
		return nil
	}
	a.printDiff(prodEnvFile, d, prune)
	if err := a.confirm(in, "Continue?", yes); err != nil {
		return err
	}
	set := map[string]string{}
	for _, k := range append(append([]string{}, d.Added...), d.Changed...) {
		set[k] = local[k]
	}
	var unset []string
	if prune {
		unset = d.OnlyStored
	}
	if _, err := applyEnv(ctx, api, l.ID, "production", set, unset, false); err != nil {
		return err
	}
	fmt.Fprintln(a.Out)
	return nil
}

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type logLine struct {
	ID       int64  `json:"id"`
	Time     string `json:"ts"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Stream   string `json:"stream"`
	Line     string `json:"line"`
	Ping     bool   `json:"ping"`
}

func logsCmd(a *App, asJSON *bool) *cobra.Command {
	var follow bool
	var service string
	var tail int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show what your services print (add -f to follow)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.logs(cmd.Context(), *asJSON, follow, service, tail)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep streaming new lines")
	cmd.Flags().StringVar(&service, "service", "", "only this service")
	cmd.Flags().IntVarP(&tail, "tail", "n", 100, "how many recent lines to start with")
	return cmd
}

func (a *App) logs(ctx context.Context, asJSON, follow bool, service string, tail int) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	l, _, err := a.linkedProject()
	if err != nil {
		return err
	}
	q := url.Values{"tail": {fmt.Sprint(tail)}}
	if service != "" {
		q.Set("service", service)
	}
	if follow {
		q.Set("follow", "1")
	}
	resp, err := s.api.stream(ctx, "/v1/projects/"+url.PathEscape(l.ID)+"/logs?"+q.Encode())
	if err != nil {
		return friendly(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ln logLine
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil || ln.Ping {
			continue
		}
		if asJSON {
			fmt.Fprintln(a.Out, sc.Text())
			continue
		}
		fmt.Fprintln(a.Out, formatLogLine(ln))
	}
	if ctx.Err() != nil {
		return nil
	}
	return sc.Err()
}

// formatLogLine prints "15:04:05 api │ text". A JSON log line (slog, zap, zerolog...) is shown as
// "LEVEL message key=value ..." (F-60).
func formatLogLine(ln logLine) string {
	ts := ln.Time
	if t, err := time.Parse(time.RFC3339Nano, ln.Time); err == nil {
		ts = t.Local().Format("15:04:05")
	}
	text := ln.Line
	if pretty, ok := prettyJSON(ln.Line); ok {
		text = pretty
	}
	if ln.Stream == "system" {
		text = "[greyship] " + text
	}
	return fmt.Sprintf("%s %s │ %s", ts, ln.Service, text)
}

func prettyJSON(line string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return "", false
	}
	take := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				delete(m, k)
				return fmt.Sprint(v)
			}
		}
		return ""
	}
	take("time", "ts", "timestamp")
	level := strings.ToUpper(take("level", "lvl", "severity"))
	msg := take("msg", "message")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	if level != "" {
		parts = append(parts, level)
	}
	if msg != "" {
		parts = append(parts, msg)
	}
	for _, k := range keys {
		v, _ := json.Marshal(m[k])
		parts = append(parts, k+"="+strings.Trim(string(v), `"`))
	}
	return strings.Join(parts, " "), true
}

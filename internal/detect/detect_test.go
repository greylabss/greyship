package detect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expected is testdata/<case>/expected.json. Reasons are matched as substrings so wording can improve without churn.
type expected struct {
	Project  string `json:"project"`
	Go       string `json:"go"`
	Services []struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Type     string `json:"type"`
		Schedule string `json:"schedule"`
		Reason   string `json:"reason"`
	} `json:"services"`
	Release string   `json:"release"`
	Skipped []string `json:"skipped"`
	Files   []string `json:"files"`
	Error   string   `json:"error"`
}

// TestCorpus runs detection on every realistic repo in testdata/ (architecture.md §2.2). Accuracy must stay at 100%:
// every misdetection found in the wild becomes a new folder here.
func TestCorpus(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	passed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ok := t.Run(e.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", e.Name())
			b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want expected
			if err := json.Unmarshal(b, &want); err != nil {
				t.Fatalf("expected.json: %v", err)
			}
			got, err := Detect(os.DirFS(dir), want.Project)
			check(t, want, got, err)
		})
		if ok {
			passed++
		}
	}
	t.Logf("detection %d/%d", passed, len(entries))
}

func check(t *testing.T, want expected, got Result, err error) {
	t.Helper()
	if want.Error != "" {
		var cfgErr *ConfigError
		if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), want.Error) {
			t.Fatalf("err = %v, want a ConfigError containing %q", err, want.Error)
		}
		return
	}
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if want.Go != "" && got.GoVersion != want.Go {
		t.Errorf("go version = %q, want %q", got.GoVersion, want.Go)
	}
	if len(got.Services) != len(want.Services) {
		t.Fatalf("services = %s\nwant %d services", dump(got.Services), len(want.Services))
	}
	for i, w := range want.Services {
		g := got.Services[i]
		if g.Name != w.Name || g.Path != w.Path || g.Type != w.Type || g.Schedule != w.Schedule || !strings.Contains(g.Reason, w.Reason) {
			t.Errorf("service %d = %s\nwant %+v", i, dump(g), w)
		}
	}
	release := ""
	if got.Release != nil {
		release = got.Release.Path
	}
	if release != want.Release {
		t.Errorf("release = %q, want %q", release, want.Release)
	}
	var skipped []string
	for _, s := range got.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %s has no reason", s.Path)
		}
		skipped = append(skipped, s.Path)
	}
	if strings.Join(skipped, ",") != strings.Join(want.Skipped, ",") {
		t.Errorf("skipped = %v, want %v", got.Skipped, want.Skipped)
	}
	if strings.Join(got.Files, ",") != strings.Join(want.Files, ",") {
		t.Errorf("files = %v, want %v", got.Files, want.Files)
	}
}

func dump(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

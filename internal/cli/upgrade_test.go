package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUpgrade(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fail        error
		wantVersion string
		wantErr     string
	}{
		{"latest", []string{"upgrade"}, nil, "", ""},
		{"a version", []string{"upgrade", "v0.1.2"}, nil, "v0.1.2", ""},
		{"the installer fails", []string{"upgrade"}, errors.New("exit status 1"), "", "upgrade grey: exit status 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			var gotVersion string
			var called bool
			a := &App{
				Out: &out, Err: &out, Version: "test", Getenv: func(string) string { return "" },
				Upgrade: func(_ context.Context, v string) error { called, gotVersion = true, v; return tt.fail },
			}
			root := NewRoot(a)
			root.SetArgs(tt.args)
			err := root.ExecuteContext(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !called || gotVersion != tt.wantVersion || !strings.Contains(out.String(), "looking for the latest release") {
				t.Fatalf("err = %v called = %v version = %q out = %q", err, called, gotVersion, out.String())
			}
		})
	}
}

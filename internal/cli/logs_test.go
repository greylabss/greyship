package cli

import "testing"

func TestFormatLogLine(t *testing.T) {
	tests := []struct {
		line, stream, want string
	}{
		{"plain text", "stdout", "plain text"},
		{`{"time":"2026-10-29T10:00:00Z","level":"info","msg":"listening","addr":":8080","n":3}`, "stdout", "INFO listening addr=:8080 n=3"},
		{`{"severity":"ERROR","message":"db down"}`, "stderr", "ERROR db down"},
		{"{not json", "stdout", "{not json"},
		{"instance stopped (exit code 2)", "system", "[greyship] instance stopped (exit code 2)"},
	}
	for _, tt := range tests {
		got := formatLogLine(logLine{Time: "bad", Service: "api", Stream: tt.stream, Line: tt.line})
		if want := "bad api │ " + tt.want; got != want {
			t.Errorf("formatLogLine(%q) = %q, want %q", tt.line, got, want)
		}
	}
}

package slug

import "testing"

func TestMake(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"Acme API", 40, "acme-api"},
		{"  --Hello__World!! ", 40, "hello-world"},
		{"Café Orders", 40, "caf-orders"},
		{"abcdef-ghij", 7, "abcdef"},
		{"!!!", 40, ""},
		{"payments-v2", 40, "payments-v2"},
	}
	for _, tt := range tests {
		if got := Make(tt.in, tt.max); got != tt.want {
			t.Errorf("Make(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
}

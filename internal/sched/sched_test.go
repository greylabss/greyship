package sched

import (
	"errors"
	"testing"

	"greyship.tech/internal/store"
)

func TestPlace(t *testing.T) {
	node := func(name string, total, used int32) store.ListNodeLoadRow {
		return store.ListNodeLoadRow{ID: name, Name: name, TotalMb: total, UsedMb: used}
	}
	tests := []struct {
		name  string
		nodes []store.ListNodeLoadRow
		mem   int32
		want  string
	}{
		{"single node", []store.ListNodeLoadRow{node("a", 2048, 0)}, 128, "a"},
		{"most free memory wins", []store.ListNodeLoadRow{node("a", 2048, 1500), node("b", 2048, 200)}, 128, "b"},
		{"skips a full node", []store.ListNodeLoadRow{node("a", 2048, 2000), node("b", 1024, 800)}, 128, "b"},
		{"exactly fits", []store.ListNodeLoadRow{node("a", 256, 128)}, 128, "a"},
		{"nothing fits", []store.ListNodeLoadRow{node("a", 256, 200)}, 128, ""},
		{"no nodes", nil, 128, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := place(tt.nodes, tt.mem)
			if tt.want == "" {
				if !errors.Is(err, ErrNoCapacity) {
					t.Fatalf("err = %v, want ErrNoCapacity", err)
				}
				return
			}
			if err != nil || got.Name != tt.want {
				t.Fatalf("placed on %q (%v), want %q", got.Name, err, tt.want)
			}
		})
	}
}

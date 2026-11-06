package agent

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestEnv(t *testing.T) {
	tests := []struct {
		name string
		in   Instance
		want map[string]string
		gone []string
	}{
		{
			name: "web on the free plan",
			in:   Instance{DeploymentID: "d1", Service: "api", Type: "web", MemoryMB: 128, CPU: 0.25},
			want: map[string]string{"PORT": "8080", "GOMAXPROCS": "1", "GOMEMLIMIT": "115MiB", "GREYSHIP_SERVICE": "api", "GREYSHIP_DEPLOYMENT_ID": "d1", "GREYSHIP_REGION": "dev"},
		},
		{
			name: "worker gets no port",
			in:   Instance{Service: "worker", Type: "worker", MemoryMB: 1024, CPU: 2},
			want: map[string]string{"GOMAXPROCS": "2", "GOMEMLIMIT": "921MiB"},
			gone: []string{"PORT"},
		},
		{
			name: "fractional CPU rounds up",
			in:   Instance{Type: "web", MemoryMB: 512, CPU: 1.5},
			want: map[string]string{"GOMAXPROCS": "2", "GOMEMLIMIT": "460MiB"},
		},
		{
			name: "user tuning wins, platform keys don't",
			in: Instance{Service: "api", Type: "web", MemoryMB: 128, CPU: 0.25, Env: map[string]string{
				"GOMAXPROCS": "4", "GOMEMLIMIT": "100MiB", "PORT": "3000", "GREYSHIP_SERVICE": "evil", "DATABASE_URL": "postgres://x",
			}},
			want: map[string]string{"GOMAXPROCS": "4", "GOMEMLIMIT": "100MiB", "PORT": "8080", "GREYSHIP_SERVICE": "api", "DATABASE_URL": "postgres://x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Env(tt.in, "dev")
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
			for _, k := range tt.gone {
				if _, ok := got[k]; ok {
					t.Errorf("%s should not be set", k)
				}
			}
		})
	}
}

func TestListening(t *testing.T) {
	// A server that accepts and waits, like any HTTP server: healthy.
	waits, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = waits.Close() }()
	go func() {
		for {
			c, err := waits.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(time.Second); _ = c.Close() }()
		}
	}()
	if !listening(waits.Addr().String()) {
		t.Fatal("a server holding the connection open is listening")
	}

	// What a port proxy does when nothing is behind it: accept, then close straight away.
	closes, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = closes.Close() }()
	go func() {
		for {
			c, err := closes.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if listening(closes.Addr().String()) {
		t.Fatal("a connection closed at once means nothing is listening behind the proxy")
	}

	if listening("127.0.0.1:1") {
		t.Fatal("a refused connection is not listening")
	}
}

type tagImages struct{ tag string }

func (t *tagImages) EnsureImage(_ context.Context, tag string, _ func() ([]byte, error)) error {
	t.tag = tag
	return nil
}

// A release ships a new guest init; images built with the old one must not be reused.
func TestImageNameCoversInit(t *testing.T) {
	art := Artifact{ObjectKey: "k", SHA256: "ab"}
	name := func(init string) string {
		imgs := &tagImages{}
		a := &Agent{Images: imgs, Init: []byte(init)}
		if _, err := a.image(context.Background(), art, nil); err != nil {
			t.Fatal(err)
		}
		return imgs.tag
	}
	first, again, other := name("init v1"), name("init v1"), name("init v2")
	if first == other {
		t.Fatal("the same app got the same image name with a different guest init")
	}
	if first != again {
		t.Fatal("the image name isn't stable")
	}
}

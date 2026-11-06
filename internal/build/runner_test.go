package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"
)

type memStore map[string][]byte

func (m memStore) Get(_ context.Context, key string, _ int64) ([]byte, error) { return m[key], nil }

func (m memStore) Put(_ context.Context, key string, body io.Reader, _ int64) error {
	b, err := io.ReadAll(body)
	m[key] = b
	return err
}

type fakeCompiler struct {
	bins map[string][]byte
	err  error
}

func (f fakeCompiler) Run(context.Context, Spec, []byte, func(string, string)) (map[string][]byte, error) {
	return f.bins, f.err
}

func TestLocalRunnerStoresWhatItBuilt(t *testing.T) {
	store := memStore{}
	r := &LocalRunner{Compiler: fakeCompiler{bins: map[string][]byte{"api": []byte("ELF api")}}, Storage: store, Now: time.Now}
	job := Job{Spec: Spec{Targets: []Target{{Name: "api", Path: "./cmd/api"}}}, OutputKeys: map[string]string{"api": "artifacts/d1/api"}}
	res, err := r.Run(context.Background(), job, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store["artifacts/d1/api"], []byte("ELF api")) || res.Binaries["api"].Size != 7 || res.Binaries["api"].SHA256 != sum([]byte("ELF api")) {
		t.Fatalf("result %+v, stored %q", res, store["artifacts/d1/api"])
	}

	// A compile error must reach the builder unwrapped, so it fails the deployment instead of retrying.
	_, err = (&LocalRunner{Compiler: fakeCompiler{err: &exec.ExitError{}}, Storage: store, Now: time.Now}).Run(context.Background(), job, func(string, string) {})
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want the compiler's exit error", err)
	}
}

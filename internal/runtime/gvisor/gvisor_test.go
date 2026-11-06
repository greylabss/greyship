package gvisor

import (
	"slices"
	"strings"
	"testing"

	"greyship.tech/internal/runtime"
)

func TestArgsLockTheInstanceDown(t *testing.T) {
	d := &Driver{Shared: "/var/lib/greyship-sandbox"}
	args := d.args(runtime.Spec{ID: "11111111-2222-4333-8444-555555555555", Image: "gs-app:abc", Port: 8080, MemoryMB: 128, CPU: 0.25})
	for _, want := range []string{"--runtime=runsc", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=128m", "--memory-swap=128m", "--cpus=0.25", "--pids-limit=256", "127.0.0.1::8080", "65534:65534", "gs-app:abc", "/greyship-init"} {
		if !slices.Contains(args, want) {
			t.Errorf("args missing %s", want)
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-e") || strings.HasPrefix(a, "--env") || strings.Contains(a, "privileged") || a == "host" || strings.HasPrefix(a, "0.0.0.0") {
			t.Fatalf("unsafe or secret-leaking argument %q", a)
		}
	}
	if i := slices.Index(args, "--network"); i < 0 || args[i+1] != "gs-i-11111111-2222-4333-8444-555555555555" {
		t.Fatal("instance must be on its own network")
	}
	for _, want := range []string{
		"type=bind,source=/var/lib/greyship-sandbox/resolv.conf,target=/etc/resolv.conf,readonly",
		"type=bind,source=/var/lib/greyship-sandbox/ca-certificates.crt,target=/etc/ssl/certs/ca-certificates.crt,readonly",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("args missing read-only mount %s: apps need DNS and CA certificates", want)
		}
	}
	worker := d.args(runtime.Spec{ID: "11111111-2222-4333-8444-555555555555", Image: "gs-app:abc", MemoryMB: 128, CPU: 0.25})
	if slices.Contains(worker, "-p") || slices.Contains(worker, "--workdir") {
		t.Fatal("a worker publishes no port, and without files the working folder is left alone")
	}
	withFiles := d.args(runtime.Spec{ID: "11111111-2222-4333-8444-555555555555", Image: "gs-app:abc", MemoryMB: 128, CPU: 0.25, WorkDir: "/srv"})
	if i := slices.Index(withFiles, "--workdir"); i < 0 || withFiles[i+1] != "/srv" || slices.Index(withFiles, "gs-app:abc") < i {
		t.Fatalf("args = %v, want --workdir /srv before the image", withFiles)
	}
}

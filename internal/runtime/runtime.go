// Package runtime is the boundary between the node agent and whatever isolates instances: gVisor containers
// now, Firecracker microVMs later (architecture §5, §8). Snapshot and Restore join Driver with Firecracker.
package runtime

import "context"

type Spec struct {
	ID       string // instance id; names the sandbox
	Image    string // the instance image: guest init plus the app binary, nothing else
	Port     int    // the app's port inside the sandbox; 0 when nothing listens (workers)
	WorkDir  string // the app's working folder; empty for the image's root
	MemoryMB int
	CPU      float64
	// Config is the one JSON line guest init reads on stdin: env (including decrypted secrets) and args.
	// It never touches the sandbox's on-disk configuration.
	Config []byte
}

// Exit is how an instance ended.
type Exit struct {
	Code int
	Err  error
}

type Instance struct {
	ID     string
	Addr   string      // host:port the node reaches the app on; empty without a Port or if the app already exited
	Exited <-chan Exit // receives once, when the app stops
}

// LogFunc receives each line the app writes; stream is "stdout" or "stderr".
type LogFunc func(stream, line string)

type Driver interface {
	Start(ctx context.Context, spec Spec, logs LogFunc) (*Instance, error)
	Stop(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
}

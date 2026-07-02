// Package runtime abstracts the container engine behind a small interface
// so the engine and CLI can be tested without podman, and so Docker support
// can be added later.
package runtime

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound is returned by Inspect (and wrapped by other calls) when the
// named container does not exist.
var ErrNotFound = errors.New("container not found")

// ExitError carries the exit code of a command executed in a container so
// callers can propagate it to the bothy process exit status.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.Code)
}

// Mount is a bind mount in a container spec.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// CreateSpec describes a container to create. Field order here mirrors the
// order flags are emitted in, which the tests assert.
type CreateSpec struct {
	Name                 string
	Hostname             string
	Labels               map[string]string
	Userns               string // e.g. "keep-id"
	User                 string // e.g. "root:root"
	SecurityLabelDisable bool   // --security-opt label=disable
	UlimitHost           bool   // --ulimit host
	Network              string // "" means the engine default
	Env                  map[string]string
	Mounts               []Mount
	ExtraArgs            []string
	Image                string
	Entrypoint           []string // command and args placed after the image
}

// ExecSpec describes a command to run in an existing container.
type ExecSpec struct {
	Container   string
	User        string
	WorkDir     string
	Env         map[string]string
	Interactive bool
	TTY         bool
	Command     []string
}

// Container is a summary of an existing container.
type Container struct {
	ID      string
	Name    string
	Image   string
	Running bool
	Status  string
	Labels  map[string]string
}

// Runtime is the container engine abstraction.
type Runtime interface {
	// Preflight verifies the engine is present and usable, with a
	// user-facing error if not.
	Preflight(ctx context.Context) error
	// Create creates a container and returns its ID.
	Create(ctx context.Context, spec CreateSpec) (string, error)
	Start(ctx context.Context, container string) error
	Stop(ctx context.Context, container string) error
	Remove(ctx context.Context, container string, force bool) error
	// Exec runs a command in a running container with stdio attached to the
	// bothy process. A non-zero exit is returned as *ExitError.
	Exec(ctx context.Context, spec ExecSpec) error
	// List returns containers matching all given label filters.
	List(ctx context.Context, labelFilters map[string]string) ([]Container, error)
	// Inspect returns a single container, or ErrNotFound.
	Inspect(ctx context.Context, container string) (*Container, error)
}

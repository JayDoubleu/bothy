package runtime

import (
	"context"
	"fmt"
)

// Fake is an in-memory Runtime for tests. It records every call and serves
// scripted responses.
type Fake struct {
	PreflightErr error
	CreateErr    error
	CreateID     string
	ExecErr      error

	// Containers is the scripted state served by Inspect and List, keyed by
	// container name.
	Containers map[string]*Container

	CreatedSpecs []CreateSpec
	Started      []string
	Stopped      []string
	Removed      []string
	ExecSpecs    []ExecSpec
}

// NewFake returns an empty Fake whose Create returns a fixed ID.
func NewFake() *Fake {
	return &Fake{
		CreateID:   "fakecontainerid",
		Containers: map[string]*Container{},
	}
}

func (f *Fake) Preflight(context.Context) error {
	return f.PreflightErr
}

func (f *Fake) Create(_ context.Context, spec CreateSpec) (string, error) {
	f.CreatedSpecs = append(f.CreatedSpecs, spec)
	if f.CreateErr != nil {
		return "", f.CreateErr
	}
	f.Containers[spec.Name] = &Container{
		ID:     f.CreateID,
		Name:   spec.Name,
		Image:  spec.Image,
		Status: "created",
		Labels: spec.Labels,
	}
	return f.CreateID, nil
}

func (f *Fake) Start(_ context.Context, container string) error {
	f.Started = append(f.Started, container)
	if c, ok := f.Containers[container]; ok {
		c.Running = true
		c.Status = "running"
	}
	return nil
}

func (f *Fake) Stop(_ context.Context, container string) error {
	f.Stopped = append(f.Stopped, container)
	if c, ok := f.Containers[container]; ok {
		c.Running = false
		c.Status = "exited"
	}
	return nil
}

func (f *Fake) Remove(_ context.Context, container string, _ bool) error {
	f.Removed = append(f.Removed, container)
	delete(f.Containers, container)
	return nil
}

func (f *Fake) Exec(_ context.Context, spec ExecSpec) error {
	f.ExecSpecs = append(f.ExecSpecs, spec)
	return f.ExecErr
}

func (f *Fake) List(context.Context, map[string]string) ([]Container, error) {
	containers := make([]Container, 0, len(f.Containers))
	for _, c := range f.Containers {
		containers = append(containers, *c)
	}
	return containers, nil
}

func (f *Fake) Inspect(_ context.Context, container string) (*Container, error) {
	c, ok := f.Containers[container]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, container)
	}
	copied := *c
	return &copied, nil
}

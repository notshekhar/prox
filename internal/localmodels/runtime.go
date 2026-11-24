package localmodels

import (
	"context"
	"io"
)

// Runtime is how a family of models runs: what install puts on disk and how a
// model is loaded. Julia 2 Flash runs in a Python worker; Julia 2 Nano runs
// inside prox. A chat runtime would be one more implementation.
type Runtime interface {
	Name() string
	// install puts the runtime and the model's files on disk.
	install(ctx context.Context, in *Installer, log io.Writer, m Model) error
	// ready reports whether the runtime's own files are still there.
	ready(l Layout, m Model) bool
	// start loads a model so it can answer.
	start(ctx context.Context, l Layout, m Model) (session, error)
}

// session is one loaded model.
type session interface {
	decide(ctx context.Context, req Request) (Result, error)
	device() string // where it runs, for the dashboard: "mps", "cuda", "cpu"
	alive() bool
	stop(ctx context.Context)
}

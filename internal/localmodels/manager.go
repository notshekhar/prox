package localmodels

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// workerStartTimeout bounds loading a model from disk; Julia 2 Flash takes
// about 4 s, so a minute means it is stuck, not slow.
const workerStartTimeout = time.Minute

// workerStopGrace is how long a worker gets to exit after its stdin closes.
const workerStopGrace = 5 * time.Second

// DeviceEnv overrides where models run: "cpu", "mps" or "cuda". The default
// picks the GPU when there is one.
const DeviceEnv = "PROX_LOCAL_DEVICE"

var ErrUnknownModel = errors.New("unknown local model")

// NotInstalledError names the command that installs the missing model.
type NotInstalledError struct{ Model Model }

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("%s is not installed; run `prox local install %s` or install it from the dashboard", e.Model.ID, e.Model.ID)
}

// InstallProgress is the state of a background install started from the dashboard.
type InstallProgress struct {
	Active bool   `json:"active"`
	Step   string `json:"step"`
	Error  string `json:"error,omitempty"`
}

// Status is one catalog entry as it stands on this machine.
type Status struct {
	Model     Model
	Installed bool
	Running   bool
	Device    string
	Install   *InstallProgress
}

// Manager owns the local models of one prox server: it starts a model's
// worker on first use, restarts it if it dies and stops them all on Close.
type Manager struct {
	layout  Layout
	command func(Model) *exec.Cmd

	ctx    context.Context // cancelled by Close; ends background installs
	cancel context.CancelFunc

	startMu  sync.Mutex // one model loads at a time; loading is memory-heavy
	mu       sync.Mutex
	workers  map[string]*worker
	installs map[string]*InstallProgress
}

// NewManager serves the models installed in layout.
func NewManager(layout Layout) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	mg := &Manager{layout: layout, ctx: ctx, cancel: cancel, workers: map[string]*worker{}, installs: map[string]*InstallProgress{}}
	mg.command = mg.workerCommand
	return mg
}

// Resolve finds an installed model by name.
func (mg *Manager) Resolve(name string) (Model, error) {
	m, ok := Find(name)
	if !ok {
		return Model{}, fmt.Errorf("%w %q", ErrUnknownModel, name)
	}
	if !mg.layout.Installed(m) {
		return Model{}, &NotInstalledError{Model: m}
	}
	return m, nil
}

// Decide answers one request on model m.
func (mg *Manager) Decide(ctx context.Context, m Model, req Request) (Result, error) {
	w, err := mg.workerFor(m)
	if err != nil {
		return Result{}, err
	}
	return w.call(ctx, req)
}

func (mg *Manager) workerFor(m Model) (*worker, error) {
	if w := mg.running(m.ID); w != nil {
		return w, nil
	}
	mg.startMu.Lock()
	defer mg.startMu.Unlock()
	if w := mg.running(m.ID); w != nil {
		return w, nil
	}
	if mg.ctx.Err() != nil {
		return nil, errors.New("prox is shutting down")
	}
	w, err := mg.start(m)
	if err != nil {
		return nil, err
	}
	mg.mu.Lock()
	mg.workers[m.ID] = w
	mg.mu.Unlock()
	return w, nil
}

func (mg *Manager) running(id string) *worker {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	if w := mg.workers[id]; w != nil && w.alive() {
		return w
	}
	return nil
}

func (mg *Manager) start(m Model) (*worker, error) {
	if err := os.WriteFile(mg.layout.WorkerScript(m.Runtime), m.Runtime.Worker, 0o644); err != nil {
		return nil, fmt.Errorf("write the %s worker: %w", m.Runtime.Name, err)
	}
	ctx, cancel := context.WithTimeout(mg.ctx, workerStartTimeout)
	defer cancel()
	began := time.Now()
	w, err := startWorker(ctx, mg.command(m))
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", m.ID, err)
	}
	log.Infof("local model %s ready on %s in %s", m.ID, w.device, time.Since(began).Round(time.Millisecond))
	return w, nil
}

func (mg *Manager) workerCommand(m Model) *exec.Cmd {
	device := os.Getenv(DeviceEnv)
	if device == "" {
		device = "auto"
	}
	// -I keeps the worker from importing modules out of its working folder.
	cmd := exec.Command(mg.layout.Python(m.Runtime), "-I", mg.layout.WorkerScript(m.Runtime), mg.layout.ModelDir(m), device)
	cmd.Dir = mg.layout.RuntimeDir(m.Runtime)
	return cmd
}

// Statuses reports every catalog model on this machine.
func (mg *Manager) Statuses() []Status {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	out := make([]Status, 0, len(catalog))
	for _, m := range catalog {
		st := Status{Model: m, Installed: mg.layout.Installed(m)}
		if w := mg.workers[m.ID]; w != nil && w.alive() {
			st.Running, st.Device = true, w.device
		}
		if p := mg.installs[m.ID]; p != nil {
			copied := *p
			st.Install = &copied
		}
		out = append(out, st)
	}
	return out
}

// StartInstall installs a model in the background; Statuses shows its progress.
func (mg *Manager) StartInstall(name string) error {
	m, ok := Find(name)
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownModel, name)
	}
	mg.mu.Lock()
	defer mg.mu.Unlock()
	if p := mg.installs[m.ID]; p != nil && p.Active {
		return nil
	}
	progress := &InstallProgress{Active: true, Step: StepPython}
	mg.installs[m.ID] = progress
	go mg.runInstall(m, progress)
	return nil
}

func (mg *Manager) runInstall(m Model, progress *InstallProgress) {
	installer := NewInstaller(mg.layout, func(step string) {
		mg.mu.Lock()
		progress.Step = step
		mg.mu.Unlock()
	})
	err := installer.Install(mg.ctx, m)
	mg.mu.Lock()
	defer mg.mu.Unlock()
	progress.Active = false
	if err != nil {
		progress.Error = err.Error()
		log.Warnf("install local model %s: %v", m.ID, err)
	}
}

// Remove stops a model and deletes it from disk.
func (mg *Manager) Remove(ctx context.Context, name string) error {
	m, ok := Find(name)
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownModel, name)
	}
	mg.mu.Lock()
	if p := mg.installs[m.ID]; p != nil && p.Active {
		mg.mu.Unlock()
		return errors.New("wait for the install to finish first")
	}
	w := mg.workers[m.ID]
	delete(mg.workers, m.ID)
	delete(mg.installs, m.ID)
	mg.mu.Unlock()
	if w != nil {
		mg.stopWorker(ctx, w)
	}
	return mg.layout.Remove(m)
}

// Close stops every worker and cancels running installs.
func (mg *Manager) Close(ctx context.Context) {
	mg.cancel()
	mg.mu.Lock()
	workers := mg.workers
	mg.workers = map[string]*worker{}
	mg.mu.Unlock()
	for _, w := range workers {
		mg.stopWorker(ctx, w)
	}
}

func (mg *Manager) stopWorker(ctx context.Context, w *worker) {
	ctx, cancel := context.WithTimeout(ctx, workerStopGrace)
	defer cancel()
	w.stop(ctx)
}

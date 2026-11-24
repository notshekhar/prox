package localmodels

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// workerStartTimeout bounds loading a model; Julia 2 Flash takes about 5 s,
// so a minute means it is stuck, not slow.
const workerStartTimeout = time.Minute

// workerStopGrace is how long a worker gets to exit after its stdin closes.
const workerStopGrace = 5 * time.Second

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

// Manager owns the local models of one prox server: it loads a model on first
// use, reloads it if its process dies and stops them all on Close.
type Manager struct {
	layout Layout
	load   func(ctx context.Context, m Model) (session, error)

	ctx    context.Context // cancelled by Close; ends background installs
	cancel context.CancelFunc

	startMu  sync.Mutex // one model loads at a time; loading is memory-heavy
	mu       sync.Mutex
	sessions map[string]session
	installs map[string]*InstallProgress
}

// NewManager serves the models installed in layout.
func NewManager(layout Layout) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	mg := &Manager{layout: layout, ctx: ctx, cancel: cancel, sessions: map[string]session{}, installs: map[string]*InstallProgress{}}
	mg.load = func(ctx context.Context, m Model) (session, error) { return m.Runtime.start(ctx, layout, m) }
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
	s, err := mg.sessionFor(m)
	if err != nil {
		return Result{}, err
	}
	return s.decide(ctx, req)
}

func (mg *Manager) sessionFor(m Model) (session, error) {
	if s := mg.running(m.ID); s != nil {
		return s, nil
	}
	mg.startMu.Lock()
	defer mg.startMu.Unlock()
	if s := mg.running(m.ID); s != nil {
		return s, nil
	}
	if mg.ctx.Err() != nil {
		return nil, errors.New("prox is shutting down")
	}
	s, err := mg.start(m)
	if err != nil {
		return nil, err
	}
	mg.mu.Lock()
	mg.sessions[m.ID] = s
	mg.mu.Unlock()
	return s, nil
}

func (mg *Manager) running(id string) session {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	if s := mg.sessions[id]; s != nil && s.alive() {
		return s
	}
	return nil
}

func (mg *Manager) start(m Model) (session, error) {
	ctx, cancel := context.WithTimeout(mg.ctx, workerStartTimeout)
	defer cancel()
	began := time.Now()
	s, err := mg.load(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", m.ID, err)
	}
	log.Infof("local model %s ready on %s in %s", m.ID, s.device(), time.Since(began).Round(time.Millisecond))
	return s, nil
}

// Statuses reports every catalog model on this machine.
func (mg *Manager) Statuses() []Status {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	out := make([]Status, 0, len(catalog))
	for _, m := range catalog {
		st := Status{Model: m, Installed: mg.layout.Installed(m)}
		if s := mg.sessions[m.ID]; s != nil && s.alive() {
			st.Running, st.Device = true, s.device()
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
	s := mg.sessions[m.ID]
	delete(mg.sessions, m.ID)
	delete(mg.installs, m.ID)
	mg.mu.Unlock()
	if s != nil {
		stopSession(ctx, s)
	}
	return mg.layout.Remove(m)
}

// Close stops every model and cancels running installs.
func (mg *Manager) Close(ctx context.Context) {
	mg.cancel()
	mg.mu.Lock()
	sessions := mg.sessions
	mg.sessions = map[string]session{}
	mg.mu.Unlock()
	for _, s := range sessions {
		stopSession(ctx, s)
	}
}

func stopSession(ctx context.Context, s session) {
	ctx, cancel := context.WithTimeout(ctx, workerStopGrace)
	defer cancel()
	s.stop(ctx)
}

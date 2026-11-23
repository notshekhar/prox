package localmodels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Install steps, in order. The dashboard and the CLI show them as progress.
const (
	StepPython   = "Setting up Python"
	StepPackages = "Installing the runtime"
	StepDownload = "Downloading the model"
	StepDone     = "Installed"
)

// ErrNoUV means uv, which installs Python and the runtime, is missing.
var ErrNoUV = errors.New("uv is not installed; install it with `brew install uv` or from https://docs.astral.sh/uv/")

// CommandRunner runs one install command, sending its output to log.
type CommandRunner interface {
	Run(ctx context.Context, log io.Writer, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, log io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run()
}

// Installer puts a model and its runtime into a Layout.
type Installer struct {
	Layout   Layout
	Runner   CommandRunner
	FindUV   func() (string, error)
	Progress func(step string)
}

// NewInstaller installs with real commands.
func NewInstaller(layout Layout, progress func(string)) *Installer {
	return &Installer{Layout: layout, Runner: execRunner{}, FindUV: findUV, Progress: progress}
}

// Install sets up the runtime, downloads the model and marks it installed.
// Running it again resumes: the environment is reused and the download picks
// up the files already there.
func (in *Installer) Install(ctx context.Context, m Model) (err error) {
	log, err := in.openLog(m)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := log.Close(); errClose != nil && err == nil {
			err = errClose
		}
	}()
	if err = in.ensureRuntime(ctx, log, m.Runtime); err != nil {
		return fmt.Errorf("%w (details in %s)", err, in.Layout.InstallLog(m))
	}
	if err = in.download(ctx, log, m); err != nil {
		return fmt.Errorf("download %s: %w (details in %s)", m.Repo, err, in.Layout.InstallLog(m))
	}
	in.report(StepDone)
	return nil
}

func (in *Installer) openLog(m Model) (*os.File, error) {
	path := in.Layout.InstallLog(m)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

func (in *Installer) ensureRuntime(ctx context.Context, log io.Writer, r *Runtime) error {
	uv, err := in.FindUV()
	if err != nil {
		return err
	}
	if !exists(in.Layout.Python(r)) {
		in.report(StepPython)
		if err = in.Runner.Run(ctx, log, uv, "venv", "--python", r.Python, in.Layout.Venv(r)); err != nil {
			return fmt.Errorf("create the Python environment: %w", err)
		}
	}
	in.report(StepPackages)
	args := append([]string{"pip", "install", "--python", in.Layout.Python(r)}, r.Packages...)
	if err = in.Runner.Run(ctx, log, uv, args...); err != nil {
		return fmt.Errorf("install the runtime packages: %w", err)
	}
	return nil
}

func (in *Installer) download(ctx context.Context, log io.Writer, m Model) error {
	in.report(StepDownload)
	dir := in.Layout.ModelDir(m)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	hf := filepath.Join(in.Layout.Venv(m.Runtime), "bin", "hf")
	args := []string{"download", m.Repo, "--local-dir", dir}
	for _, pattern := range m.Exclude {
		// One --exclude per pattern with "=": a bare list after --exclude is read as file names.
		args = append(args, "--exclude="+pattern)
	}
	if err := in.Runner.Run(ctx, log, hf, args...); err != nil {
		return err
	}
	stamp := fmt.Sprintf("%s\n%s\n", m.Repo, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(filepath.Join(dir, installedMarker), []byte(stamp), 0o644)
}

func (in *Installer) report(step string) {
	if in.Progress != nil {
		in.Progress(step)
	}
}

// uvLocations covers installs that are not on PATH: a GUI or launchd-started
// prox gets an almost empty PATH.
var uvLocations = []string{"/opt/homebrew/bin/uv", "/usr/local/bin/uv", "~/.local/bin/uv", "~/.cargo/bin/uv"}

func findUV() (string, error) {
	if path, err := exec.LookPath("uv"); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range uvLocations {
		if len(candidate) > 1 && candidate[0] == '~' {
			candidate = filepath.Join(home, candidate[2:])
		}
		if exists(candidate) {
			return candidate, nil
		}
	}
	return "", ErrNoUV
}

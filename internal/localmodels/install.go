package localmodels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// HubURL is where model files come from.
const HubURL = "https://huggingface.co"

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

// Installer puts a model and its runtime into a Layout. Each runtime decides
// what that takes; the installer supplies the tools and marks the result.
type Installer struct {
	Layout   Layout
	Runner   CommandRunner
	FindUV   func() (string, error)
	HTTP     *http.Client
	HubURL   string
	Progress func(step string)
}

// NewInstaller installs with real commands and downloads.
func NewInstaller(layout Layout, progress func(string)) *Installer {
	return &Installer{Layout: layout, Runner: execRunner{}, FindUV: findUV, HTTP: http.DefaultClient, HubURL: HubURL, Progress: progress}
}

// Install sets up the runtime, downloads the model and marks it installed.
// Running it again resumes: what is already there is reused.
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
	if err = m.Runtime.install(ctx, in, log, m); err != nil {
		return fmt.Errorf("%w (details in %s)", err, in.Layout.InstallLog(m))
	}
	if err = in.markInstalled(m); err != nil {
		return err
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

// markInstalled is the last step, so an install cut short never looks finished.
func (in *Installer) markInstalled(m Model) error {
	stamp := fmt.Sprintf("%s@%s\n%s\n", m.Repo, m.Revision, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(filepath.Join(in.Layout.ModelDir(m), installedMarker), []byte(stamp), 0o644)
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

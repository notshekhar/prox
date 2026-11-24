package localmodels

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records commands and creates what the real ones would.
type fakeRunner struct {
	commands []string
	failOn   string
}

func (f *fakeRunner) Run(_ context.Context, _ io.Writer, name string, args ...string) error {
	line := filepath.Base(name) + " " + strings.Join(args, " ")
	f.commands = append(f.commands, line)
	if f.failOn != "" && strings.Contains(line, f.failOn) {
		return errors.New("exit status 1")
	}
	if len(args) > 0 && args[0] == "venv" {
		python := filepath.Join(args[len(args)-1], "bin", "python")
		_ = os.MkdirAll(filepath.Dir(python), 0o755)
		return os.WriteFile(python, nil, 0o755)
	}
	return nil
}

func newTestInstaller(t *testing.T) (*Installer, *fakeRunner, *[]string) {
	layout := Layout{Root: t.TempDir()}
	runner := &fakeRunner{}
	var steps []string
	in := &Installer{Layout: layout, Runner: runner, FindUV: func() (string, error) { return "/bin/uv", nil }, Progress: func(s string) { steps = append(steps, s) }}
	return in, runner, &steps
}

func TestInstallRunsTheStepsAndMarksTheModel(t *testing.T) {
	in, runner, steps := newTestInstaller(t)
	m, _ := Find("julia-2-flash")
	if err := in.Install(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if !in.Layout.Installed(m) {
		t.Fatal("model not marked installed")
	}
	if want := []string{StepPython, StepPackages, StepDownload, StepDone}; strings.Join(*steps, "|") != strings.Join(want, "|") {
		t.Fatalf("steps = %v", *steps)
	}
	download := runner.commands[len(runner.commands)-1]
	for _, want := range []string{"hf download SupersonicLabs/Julia-2-Flash --revision " + m.Revision, "--exclude=image.safetensors", "--exclude=audio.safetensors"} {
		if !strings.Contains(download, want) {
			t.Fatalf("download command %q is missing %q", download, want)
		}
	}
}

func TestInstallAgainReusesThePythonEnvironment(t *testing.T) {
	in, runner, _ := newTestInstaller(t)
	m, _ := Find("julia-2-flash")
	_ = in.Install(context.Background(), m)
	runner.commands = nil
	if err := in.Install(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(runner.commands[0], "uv venv") {
		t.Fatalf("recreated the environment: %v", runner.commands)
	}
}

func TestFailedDownloadLeavesTheModelUninstalled(t *testing.T) {
	in, runner, _ := newTestInstaller(t)
	runner.failOn = "hf download"
	m, _ := Find("julia-2-flash")
	err := in.Install(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), in.Layout.InstallLog(m)) {
		t.Fatalf("err = %v, want it to point at the install log", err)
	}
	if in.Layout.Installed(m) {
		t.Fatal("a failed download looks installed")
	}
}

func TestInstallWithoutUV(t *testing.T) {
	in, _, _ := newTestInstaller(t)
	in.FindUV = func() (string, error) { return "", ErrNoUV }
	m, _ := Find("julia-2-flash")
	if err := in.Install(context.Background(), m); !errors.Is(err, ErrNoUV) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveDeletesModelAndUnusedRuntime(t *testing.T) {
	in, _, _ := newTestInstaller(t)
	m, _ := Find("julia-2-flash")
	_ = in.Install(context.Background(), m)
	if err := in.Layout.Remove(m); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{in.Layout.ModelDir(m), in.Layout.RuntimeDir(m.Runtime)} {
		if exists(dir) {
			t.Fatalf("%s still exists", dir)
		}
	}
}

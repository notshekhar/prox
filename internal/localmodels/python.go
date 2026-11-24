package localmodels

import (
	"context"
	_ "embed"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// DeviceEnv overrides where Python models run: "cpu", "mps" or "cuda". The
// default picks the GPU when there is one.
const DeviceEnv = "PROX_LOCAL_DEVICE"

// pythonRuntime runs models in a Python worker process: uv creates a shared
// environment under local/runtimes/<name>, and the worker script speaks JSON
// lines (see julia2_worker.py).
type pythonRuntime struct {
	name     string
	python   string   // version uv installs, e.g. "3.12"
	packages []string // pip requirements, including the Hugging Face downloader
	worker   []byte
}

// The model's own runtime/ folder holds the julia2 package; the worker adds it
// to sys.path, so only its dependencies are installed here.
//
//go:embed julia2_worker.py
var julia2Worker []byte

var julia2Runtime = &pythonRuntime{
	name:     "julia2",
	python:   "3.12",
	packages: []string{"torch>=2.6", "safetensors", "tokenizers", "numpy", "pillow", "soundfile", "scipy", "huggingface_hub"},
	worker:   julia2Worker,
}

func (r *pythonRuntime) Name() string { return r.name }

func (r *pythonRuntime) venv(l Layout) string { return filepath.Join(l.RuntimeDir(r), "venv") }

func (r *pythonRuntime) pythonPath(l Layout) string { return filepath.Join(r.venv(l), "bin", "python") }

func (r *pythonRuntime) workerScript(l Layout) string {
	return filepath.Join(l.RuntimeDir(r), "worker.py")
}

func (r *pythonRuntime) ready(l Layout, _ Model) bool { return exists(r.pythonPath(l)) }

func (r *pythonRuntime) install(ctx context.Context, in *Installer, log io.Writer, m Model) error {
	if err := r.ensureEnvironment(ctx, in, log); err != nil {
		return err
	}
	if err := r.download(ctx, in, log, m); err != nil {
		return fmt.Errorf("download %s: %w", m.Repo, err)
	}
	return nil
}

// ensureEnvironment creates the environment once and brings its packages up
// to date on every install.
func (r *pythonRuntime) ensureEnvironment(ctx context.Context, in *Installer, log io.Writer) error {
	uv, err := in.FindUV()
	if err != nil {
		return err
	}
	if !exists(r.pythonPath(in.Layout)) {
		in.report(StepPython)
		if err = in.Runner.Run(ctx, log, uv, "venv", "--python", r.python, r.venv(in.Layout)); err != nil {
			return fmt.Errorf("create the Python environment: %w", err)
		}
	}
	in.report(StepPackages)
	args := append([]string{"pip", "install", "--python", r.pythonPath(in.Layout)}, r.packages...)
	if err = in.Runner.Run(ctx, log, uv, args...); err != nil {
		return fmt.Errorf("install the runtime packages: %w", err)
	}
	return nil
}

func (r *pythonRuntime) download(ctx context.Context, in *Installer, log io.Writer, m Model) error {
	in.report(StepDownload)
	dir := in.Layout.ModelDir(m)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	hf := filepath.Join(r.venv(in.Layout), "bin", "hf")
	args := []string{"download", m.Repo, "--revision", m.Revision, "--local-dir", dir}
	for _, pattern := range m.Exclude {
		// One --exclude per pattern with "=": a bare list after --exclude is read as file names.
		args = append(args, "--exclude="+pattern)
	}
	return in.Runner.Run(ctx, log, hf, args...)
}

func (r *pythonRuntime) start(ctx context.Context, l Layout, m Model) (session, error) {
	if err := os.WriteFile(r.workerScript(l), r.worker, 0o644); err != nil {
		return nil, fmt.Errorf("write the %s worker: %w", r.name, err)
	}
	w, err := startWorker(ctx, r.command(l, m))
	if err != nil {
		return nil, err
	}
	return juliaSession{w}, nil
}

// juliaSession turns a request's assets into the Julia runtime's state parts
// before handing it to the worker.
type juliaSession struct{ *worker }

func (s juliaSession) decide(ctx context.Context, req Request) (Result, error) {
	state, err := juliaState(req)
	if err != nil {
		return Result{}, &RequestError{Message: err.Error()}
	}
	req.State, req.Assets = state, nil
	return s.worker.decide(ctx, req)
}

// juliaState is the state as the Julia runtime reads media: one part per
// image or clip, as a data URL, then the state text with each reference
// replaced by its label, e.g. "[image 1]".
func juliaState(req Request) (jsontext.Value, error) {
	if len(req.Assets) == 0 {
		return req.State, nil
	}
	refs := FindReferences(req)
	if len(refs.Elsewhere) > 0 {
		return nil, errors.New(`Julia 2 Flash reads images and audio only as whole values in the state, like "state": {"photo": "<asset id>"}; not inside instructions, criteria or longer text`)
	}
	var parts []any
	seen := map[string]bool{}
	for _, id := range refs.StateWhole {
		if !seen[id] {
			seen[id] = true
			parts = append(parts, mediaPart(req.Assets[id]))
		}
	}
	text, err := ReplaceWholeReferences(req.State, req.Assets)
	if err != nil {
		return nil, err
	}
	parts = append(parts, map[string]string{"type": "text", "text": StateText(text)})
	return json.Marshal(parts)
}

func mediaPart(a Asset) map[string]string {
	if a.Kind == MediaAudio {
		return map[string]string{"type": "input_audio", "input_audio": a.DataURL()}
	}
	return map[string]string{"type": "image", "image": a.DataURL()}
}

func (r *pythonRuntime) command(l Layout, m Model) *exec.Cmd {
	device := os.Getenv(DeviceEnv)
	if device == "" {
		device = "auto"
	}
	// -I keeps the worker from importing modules out of its working folder.
	cmd := exec.Command(r.pythonPath(l), "-I", r.workerScript(l), l.ModelDir(m), device)
	cmd.Dir = l.RuntimeDir(r)
	return cmd
}

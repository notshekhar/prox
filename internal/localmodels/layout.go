package localmodels

import (
	"errors"
	"os"
	"path/filepath"
)

// installedMarker is written last by the installer, so a download cut short
// never looks like an installed model.
const installedMarker = ".prox-installed"

// Layout is where local models live, normally ~/.prox/local:
//
//	models/<id>/          the downloaded repository
//	runtimes/<name>/venv  the Python environment a runtime shares across models
//	runtimes/<name>/worker.py
//	logs/<id>-install.log
type Layout struct{ Root string }

// LayoutIn puts the local models folder inside prox's home folder.
func LayoutIn(proxHome string) Layout { return Layout{Root: filepath.Join(proxHome, "local")} }

func (l Layout) ModelDir(m Model) string { return filepath.Join(l.Root, "models", m.ID) }

func (l Layout) RuntimeDir(r *Runtime) string { return filepath.Join(l.Root, "runtimes", r.Name) }

func (l Layout) Venv(r *Runtime) string { return filepath.Join(l.RuntimeDir(r), "venv") }

func (l Layout) Python(r *Runtime) string { return filepath.Join(l.Venv(r), "bin", "python") }

func (l Layout) WorkerScript(r *Runtime) string { return filepath.Join(l.RuntimeDir(r), "worker.py") }

func (l Layout) InstallLog(m Model) string { return filepath.Join(l.Root, "logs", m.ID+"-install.log") }

// Installed reports whether the model finished installing and its runtime is still there.
func (l Layout) Installed(m Model) bool {
	return exists(filepath.Join(l.ModelDir(m), installedMarker)) && exists(l.Python(m.Runtime))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Remove deletes a model, and its runtime too once no installed model uses it.
func (l Layout) Remove(m Model) error {
	if err := os.RemoveAll(l.ModelDir(m)); err != nil {
		return err
	}
	if l.runtimeInUse(m.Runtime) {
		return nil
	}
	err := os.RemoveAll(l.RuntimeDir(m.Runtime))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l Layout) runtimeInUse(r *Runtime) bool {
	for _, other := range catalog {
		if other.Runtime == r && exists(filepath.Join(l.ModelDir(other), installedMarker)) {
			return true
		}
	}
	return false
}

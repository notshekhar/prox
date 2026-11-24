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
//	models/<id>/          the downloaded repository files
//	runtimes/<name>/      what a runtime shares across models, e.g. a Python venv
//	logs/<id>-install.log
type Layout struct{ Root string }

// LayoutIn puts the local models folder inside prox's home folder.
func LayoutIn(proxHome string) Layout { return Layout{Root: filepath.Join(proxHome, "local")} }

func (l Layout) ModelDir(m Model) string { return filepath.Join(l.Root, "models", m.ID) }

func (l Layout) RuntimeDir(r Runtime) string { return filepath.Join(l.Root, "runtimes", r.Name()) }

func (l Layout) InstallLog(m Model) string { return filepath.Join(l.Root, "logs", m.ID+"-install.log") }

// Installed reports whether the model finished installing, has every file this
// version needs, and its runtime is still there.
func (l Layout) Installed(m Model) bool {
	return l.markedInstalled(m) && l.hasRequired(m) && m.Runtime.ready(l, m)
}

func (l Layout) markedInstalled(m Model) bool {
	return exists(filepath.Join(l.ModelDir(m), installedMarker))
}

func (l Layout) hasRequired(m Model) bool {
	for _, rel := range m.Required {
		if !exists(filepath.Join(l.ModelDir(m), filepath.FromSlash(rel))) {
			return false
		}
	}
	return true
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

func (l Layout) runtimeInUse(r Runtime) bool {
	for _, other := range catalog {
		if other.Runtime == r && l.markedInstalled(other) {
			return true
		}
	}
	return false
}

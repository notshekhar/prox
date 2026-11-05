package proxcli

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// panelFileName is the name the server looks for under MANAGEMENT_STATIC_PATH.
const panelFileName = "management.html"

// panelHTML is the dashboard's Vite build. Rebuild with: cd dashboard && bun run build
//
//go:embed webdist/management.html
var panelHTML []byte

// installPanel gives the server the dashboard built into this binary. The
// server serves it from disk, so it is written out, and rewritten only when an
// upgrade changed it.
func installPanel(p Paths) error {
	path := filepath.Join(p.Home, "static", panelFileName)
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, panelHTML) {
		if err = os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
			return fmt.Errorf("create dashboard folder: %w", err)
		}
		if err = os.WriteFile(path, panelHTML, privateFile); err != nil {
			return fmt.Errorf("write dashboard: %w", err)
		}
	}
	if err := os.Setenv("MANAGEMENT_STATIC_PATH", path); err != nil {
		return fmt.Errorf("point server at dashboard: %w", err)
	}
	return nil
}

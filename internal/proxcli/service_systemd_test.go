package proxcli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sandboxPaths(t *testing.T) Paths {
	t.Helper()
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestLinuxServiceWritesAPrivateEnvFileAndEnablesTheUnit(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	calls := filepath.Join(s.home, "systemctl-calls")
	s.fakeTool(t, "systemctl", `echo "$@" >> `+calls)
	p := sandboxPaths(t)
	cfg, err := loadSettings(p)
	if err != nil {
		t.Fatal(err)
	}

	if err = turnServiceOn(p, cfg, "linux", io.Discard); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(p.ServiceEnv)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != privateFile {
		t.Fatalf("password file mode = %o", info.Mode().Perm())
	}
	unit, _ := os.ReadFile(p.SystemdUnit)
	if strings.Contains(string(unit), sandboxPassword) {
		t.Fatal("password in the world-readable unit file")
	}
	if !strings.Contains(string(unit), "EnvironmentFile="+p.ServiceEnv) {
		t.Fatalf("unit = %s", unit)
	}
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), "--user enable --now prox.service") {
		t.Fatalf("systemctl calls = %s", got)
	}
}

func TestLinuxServiceRefusesAWrongPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	s.fakeTool(t, "systemctl", "exit 0")
	t.Setenv(masterPasswordEnv, "not-the-password")
	p := sandboxPaths(t)
	cfg, _ := loadSettings(p)

	if err := turnServiceOn(p, cfg, "linux", io.Discard); err == nil {
		t.Fatal("service turned on with a wrong password")
	}
	if _, err := os.Stat(p.ServiceEnv); err == nil {
		t.Fatal("wrong password saved")
	}
}

func TestLinuxServiceOffRemovesThePassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	s.fakeTool(t, "systemctl", "exit 0")
	p := sandboxPaths(t)
	cfg, _ := loadSettings(p)
	if err := turnServiceOn(p, cfg, "linux", io.Discard); err != nil {
		t.Fatal(err)
	}

	if err := turnServiceOff(p, "linux"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p.ServiceEnv, p.SystemdUnit} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s left behind", path)
		}
	}
}

func TestUnsupportedSystemSaysSo(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	p := sandboxPaths(t)
	cfg, _ := loadSettings(p)
	if err := turnServiceOn(p, cfg, "plan9", io.Discard); err == nil {
		t.Fatal("no error for an unsupported system")
	}
}

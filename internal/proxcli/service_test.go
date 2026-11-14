package proxcli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServiceStartGivesUpQuietlyWhenAlreadyRunning(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	t.Setenv(serviceEnv, "1")

	outcome, _, errOut := run("start")
	if !outcome.Done || outcome.ExitCode != 0 || !strings.Contains(errOut, "already running") {
		t.Fatalf("outcome = %+v, %q", outcome, errOut)
	}
}

func TestServiceStartGivesUpQuietlyOnAWrongSavedPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	t.Setenv(serviceEnv, "1")
	t.Setenv(masterPasswordEnv, "not-the-password")

	outcome, _, errOut := run("start")
	if !outcome.Done || outcome.ExitCode != 0 || !strings.Contains(errOut, "wrong master password") {
		t.Fatalf("outcome = %+v, %q", outcome, errOut)
	}
}

func TestLaunchAgentOnlyRestartsAFailedStart(t *testing.T) {
	s := newSandbox(t)
	s.fakeTool(t, "launchctl", "exit 0")
	p := sandboxPaths(t)

	if err := (launchdService{}).enable(p, "/opt/prox & co/prox"); err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile(p.LaunchAgent)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>SuccessfulExit</key><false/>",
		"<key>" + serviceEnv + "</key><string>1</string>",
		"/opt/prox &amp; co/prox",
	} {
		if !strings.Contains(string(plist), want) {
			t.Fatalf("missing %q in:\n%s", want, plist)
		}
	}
	if lint, errLook := exec.LookPath("/usr/bin/plutil"); errLook == nil {
		if output, errLint := exec.Command(lint, "-lint", p.LaunchAgent).CombinedOutput(); errLint != nil {
			t.Fatalf("invalid plist: %s", output)
		}
	}
}

func TestSystemdUnitQuotesPathsAndEscapesSpecifiers(t *testing.T) {
	s := newSandbox(t)
	s.fakeTool(t, "systemctl", "exit 0")
	p := sandboxPaths(t)
	p.Home = "/home/me/my prox%1"

	if err := (systemdService{}).enable(p, "/opt/my tools/$prox"); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(p.SystemdUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ExecStart="/opt/my tools/$$prox" start`,
		`Environment="PROX_HOME=/home/me/my prox%%1"`,
		"Environment=" + serviceEnv + "=1",
	} {
		if !strings.Contains(string(unit), want) {
			t.Fatalf("missing %q in:\n%s", want, unit)
		}
	}
}

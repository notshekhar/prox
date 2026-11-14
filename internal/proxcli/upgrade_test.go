package proxcli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
)

// fakeReleases serves a redirect to latestTag and an installer that records its
// environment, so no test can run the real one.
func fakeReleases(t *testing.T, s sandbox, latestTag, installerBody string) string {
	t.Helper()
	record := filepath.Join(s.home, "installer-ran")
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/raw/{ref}/install.sh", func(w http.ResponseWriter, r *http.Request) {
		body := strings.ReplaceAll(installerBody, "RECORD", record)
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "REF", r.PathValue("ref"))))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	oldLatest, oldInstaller, oldVersion := latestReleaseURL, installerURLTemplate, buildinfo.Version
	t.Cleanup(func() {
		latestReleaseURL, installerURLTemplate, buildinfo.Version = oldLatest, oldInstaller, oldVersion
	})
	latestReleaseURL, installerURLTemplate = server.URL+"/latest", server.URL+"/raw/%s/install.sh"
	return record
}

const recordingInstaller = `echo "force=$PROX_FORCE path=$PROX_NO_MODIFY_PATH ref=REF version=$PROX_VERSION" > RECORD`

func TestUpgradeRunsTheInstallerThatShippedWithTheRelease(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	record := fakeReleases(t, s, "v0.4.0", recordingInstaller)
	buildinfo.Version = "v0.3.0"

	if outcome, _, errOut := run("upgrade"); outcome.ExitCode != 0 {
		t.Fatalf("exit %d: %s", outcome.ExitCode, errOut)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("installer did not run: %v", err)
	}
	if strings.TrimSpace(string(got)) != "force= path=1 ref=v0.4.0 version=v0.4.0" {
		t.Fatalf("installer env = %q", got)
	}
}

func TestUpgradeSkipsTheInstallerWhenCurrent(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	record := fakeReleases(t, s, "v0.3.0", recordingInstaller)
	buildinfo.Version = "v0.3.0"

	outcome, out, _ := run("upgrade")
	if outcome.ExitCode != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("exit %d, out %q", outcome.ExitCode, out)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("installer ran with nothing to upgrade")
	}
}

func TestUpgradeForceReinstallsTheSameVersion(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	record := fakeReleases(t, s, "v0.3.0", recordingInstaller)
	buildinfo.Version = "v0.3.0"

	run("upgrade", "--force")
	if got, _ := os.ReadFile(record); !strings.Contains(string(got), "force=1") {
		t.Fatalf("installer env = %q", got)
	}
}

func TestUpgradeReportsAFailedInstaller(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	fakeReleases(t, s, "v0.4.0", "exit 3")
	buildinfo.Version = "v0.3.0"

	outcome, _, errOut := run("upgrade")
	if outcome.ExitCode == 0 || !strings.Contains(errOut, "Upgrade failed") {
		t.Fatalf("exit %d, stderr %q", outcome.ExitCode, errOut)
	}
}

func TestUpgradeSaysToRestartARunningServer(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	fakeReleases(t, s, "v0.4.0", recordingInstaller)
	buildinfo.Version = "v0.3.0"

	if _, out, _ := run("upgrade"); !strings.Contains(out, "Restart") {
		t.Fatalf("out = %q", out)
	}
}

func TestUpgradeStillRunsTheInstallerWhenTheLookupFails(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	record := fakeReleases(t, s, "not-a-version", recordingInstaller)
	buildinfo.Version = "v0.3.0"

	run("upgrade")
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("installer did not run: %v", err)
	}
	if !strings.Contains(string(got), "ref=main version=\n") {
		t.Fatalf("installer env = %q", got)
	}
}

func TestIsNewerRelease(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.0", "v0.3.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.9", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.3.0", "v0.4.0", false},
		{"v0.3.0", "v0.3.0-dirty", true},
		{"v0.3.0", "v0.2.0-dirty", true},
		{"v0.3.0", "v0.4.0-dirty", false},
		{"v0.3.0", "dev", true},
		{"junk", "v0.3.0", false},
	} {
		if got := isNewerRelease(tc.latest, tc.current); got != tc.want {
			t.Errorf("isNewerRelease(%q, %q) = %v", tc.latest, tc.current, got)
		}
	}
}

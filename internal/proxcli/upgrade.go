package proxcli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
)

const releaseLookupTimeout = 10 * time.Second

// Variables so tests can point them at a fake server: the real installer
// rewrites the system's prox link.
var (
	latestReleaseURL = "https://github.com/notshekhar/prox/releases/latest"
	installerURL     = "https://raw.githubusercontent.com/notshekhar/prox/main/install.sh"
)

var versionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(.*)$`)

// upgrade runs the installer when a newer release exists. A failed lookup still
// runs it, because the installer does its own lookup and reports its own errors.
func upgrade(cfg settings, args []string, out, errOut io.Writer) int {
	force := slices.Contains(args, "--force") || slices.Contains(args, "-f")
	latest := fetchLatestTag()
	if !force && latest != "" && !isNewerRelease(latest, buildinfo.Version) {
		fmt.Fprintf(out, "prox is up to date (%s)\n", buildinfo.Version)
		return 0
	}
	if err := runInstaller(force, out, errOut); err != nil {
		fmt.Fprintf(errOut, "Upgrade failed: %v\n", err)
		return 1
	}
	if isReachable(cfg) {
		fmt.Fprintln(out, "prox is still running the old version. Restart it to use the new one.")
	}
	return 0
}

// fetchLatestTag reads the tag from the releases/latest redirect, which isn't
// rate limited the way GitHub's anonymous API is.
func fetchLatestTag() string {
	ctx, cancel := context.WithTimeout(context.Background(), releaseLookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleaseURL, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	tag := path.Base(resp.Request.URL.Path)
	if !versionPattern.MatchString(tag) {
		return ""
	}
	return tag
}

// isNewerRelease treats a build it can't read, such as "dev", as older, and a
// suffixed build, such as v0.3.0-dirty, as older than the release itself.
func isNewerRelease(latest, current string) bool {
	latestParts, _, ok := parseVersion(latest)
	if !ok {
		return false
	}
	currentParts, suffix, ok := parseVersion(current)
	if !ok {
		return true
	}
	order := slices.CompareFunc(latestParts, currentParts, cmp.Compare[int])
	return order > 0 || (order == 0 && suffix != "")
}

func parseVersion(version string) (parts []int, suffix string, ok bool) {
	match := versionPattern.FindStringSubmatch(version)
	if match == nil {
		return nil, "", false
	}
	for _, digits := range match[1:4] {
		number, err := strconv.Atoi(digits)
		if err != nil {
			return nil, "", false
		}
		parts = append(parts, number)
	}
	return parts, match[4], true
}

// runInstaller leaves the shell rc alone: a first install already set up PATH.
func runInstaller(force bool, out, errOut io.Writer) error {
	cmd := exec.Command("bash", "-c", `set -o pipefail; curl -fsSL "$0" | bash`, installerURL)
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.Env = append(os.Environ(), "PROX_NO_MODIFY_PATH=1")
	if force {
		cmd.Env = append(cmd.Env, "PROX_FORCE=1")
	}
	return cmd.Run()
}

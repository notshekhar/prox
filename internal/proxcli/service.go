package proxcli

import (
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const serviceLabel = "dev.notshekhar.prox"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>start</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PROX_HOME</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func serviceTarget() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func serviceOn(p Paths, out, errOut io.Writer) int {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintf(errOut, "find prox binary: %v\n", err)
		return 1
	}
	body := fmt.Sprintf(plistTemplate, serviceLabel, html.EscapeString(exe), html.EscapeString(p.Home),
		html.EscapeString(p.Log), html.EscapeString(p.Log))
	if err = os.MkdirAll(filepath.Dir(p.LaunchAgent), 0o755); err != nil {
		fmt.Fprintf(errOut, "create LaunchAgents folder: %v\n", err)
		return 1
	}
	if err = os.WriteFile(p.LaunchAgent, []byte(body), 0o644); err != nil {
		fmt.Fprintf(errOut, "write service file: %v\n", err)
		return 1
	}
	// Bootout first so turning it on twice picks up a moved binary instead of failing.
	_ = exec.Command("launchctl", "bootout", serviceTarget()+"/"+serviceLabel).Run()
	if output, errBoot := exec.Command("launchctl", "bootstrap", serviceTarget(), p.LaunchAgent).CombinedOutput(); errBoot != nil {
		fmt.Fprintf(errOut, "start service: %v: %s\n", errBoot, output)
		return 1
	}
	fmt.Fprintf(out, "prox now runs in the background and starts at login. Logs: %s\n", p.Log)
	return 0
}

func serviceOff(p Paths, out, errOut io.Writer) int {
	_ = exec.Command("launchctl", "bootout", serviceTarget()+"/"+serviceLabel).Run()
	if err := os.Remove(p.LaunchAgent); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(errOut, "remove service file: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "prox no longer runs in the background.")
	return 0
}

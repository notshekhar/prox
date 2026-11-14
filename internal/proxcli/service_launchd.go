package proxcli

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const serviceLabel = "dev.notshekhar.prox"

// plistTemplate restarts prox only after a failed exit: a start that gives up
// (see giveUp) exits 0, or launchd would retry it every few seconds forever.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>start</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PROX_HOME</key><string>%s</string>
    <key>%s</key><string>1</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

// launchdService runs prox as a macOS LaunchAgent, unlocked from the Keychain.
type launchdService struct{}

func serviceTarget() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func (launchdService) savePassword(_ Paths, password string) error {
	return saveKeychainPassword(password)
}

func (launchdService) forgetPassword(Paths) { deleteKeychainPassword() }

func (launchdService) enable(p Paths, exe string) error {
	body := fmt.Sprintf(plistTemplate, serviceLabel, html.EscapeString(exe), html.EscapeString(p.Home), serviceEnv,
		html.EscapeString(p.Log), html.EscapeString(p.Log))
	if err := os.MkdirAll(filepath.Dir(p.LaunchAgent), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents folder: %w", err)
	}
	if err := os.WriteFile(p.LaunchAgent, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	// Bootout first so turning it on twice picks up a moved binary instead of failing.
	_ = exec.Command("launchctl", "bootout", serviceTarget()+"/"+serviceLabel).Run()
	if output, err := exec.Command("launchctl", "bootstrap", serviceTarget(), p.LaunchAgent).CombinedOutput(); err != nil {
		return fmt.Errorf("start service: %w: %s", err, output)
	}
	return nil
}

func (launchdService) disable(p Paths) error {
	_ = exec.Command("launchctl", "bootout", serviceTarget()+"/"+serviceLabel).Run()
	if err := removeIfPresent(p.LaunchAgent); err != nil {
		return fmt.Errorf("remove service file: %w", err)
	}
	return nil
}

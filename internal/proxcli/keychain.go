package proxcli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// keychainAccount names the login Keychain item the background service unlocks
// itself with, since nobody is at a terminal when it starts at login.
const keychainAccount = "prox-master-password"

func readKeychainPassword() (string, error) {
	output, err := exec.Command("security", "find-generic-password", "-a", keychainAccount, "-s", serviceLabel, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("read Keychain: %w", err)
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

// saveKeychainPassword feeds the command through stdin, because an argument
// would show the password to anyone running ps.
func saveKeychainPassword(password string) error {
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -a %s -s %s -w %s\n",
		keychainAccount, serviceLabel, doubleQuoted(password)))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("save to Keychain: %w: %s", err, output)
	}
	// Interactive mode exits 0 even when a command inside it failed.
	if saved, err := readKeychainPassword(); err != nil || saved != password {
		return errors.New("save to Keychain: the password did not stick")
	}
	return nil
}

func deleteKeychainPassword() {
	_ = exec.Command("security", "delete-generic-password", "-a", keychainAccount, "-s", serviceLabel).Run()
}

// doubleQuoted suits both `security -i` and a systemd environment file, which
// read quotes and backslashes the same way.
func doubleQuoted(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

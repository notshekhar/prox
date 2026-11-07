package proxcli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// masterPasswordEnv supplies the password where no terminal can ask for it.
const masterPasswordEnv = "PROX_MASTER_PASSWORD"

// terminalAttempts allows for typos; a fixed answer can't change between tries.
const terminalAttempts = 3

// passwordInput is where a command gets the master password from.
type passwordInput struct {
	ask      func(prompt string) (string, error)
	attempts int
}

func fixedPassword(password string) passwordInput {
	return passwordInput{
		ask:      func(string) (string, error) { return password, nil },
		attempts: 1,
	}
}

func terminalPassword(errOut io.Writer) passwordInput {
	return passwordInput{
		ask: func(prompt string) (string, error) {
			fmt.Fprint(errOut, prompt)
			password, err := term.ReadPassword(os.Stdin.Fd())
			// The Enter key isn't echoed, so end the prompt's line here.
			fmt.Fprintln(errOut)
			if err != nil {
				return "", fmt.Errorf("read password: %w", err)
			}
			return string(password), nil
		},
		attempts: terminalAttempts,
	}
}

func terminalOrEnvPassword(errOut io.Writer) passwordInput {
	if password := os.Getenv(masterPasswordEnv); password != "" {
		return fixedPassword(password)
	}
	return terminalPassword(errOut)
}

// startPasswordInput falls back to the Keychain only without a terminal: that
// is the background service, and a person at a prompt must always type it.
func startPasswordInput(errOut io.Writer) (passwordInput, error) {
	if os.Getenv(masterPasswordEnv) != "" || term.IsTerminal(os.Stdin.Fd()) {
		return terminalOrEnvPassword(errOut), nil
	}
	if password, err := readKeychainPassword(); err == nil {
		return fixedPassword(password), nil
	}
	return passwordInput{}, errors.New("prox needs its master password: run it in a terminal, or set " + masterPasswordEnv)
}

package proxcli

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"golang.org/x/crypto/bcrypt"
)

const (
	minMasterPasswordBytes = 8
	// maxMasterPasswordBytes is bcrypt's limit: it refuses anything longer.
	maxMasterPasswordBytes = 72
)

var masterPasswordConfigPath = []string{"management", "secret-key"}

var errInvalidNewPassword = errors.New("invalid password")

// requireMasterPassword returns the master password once it is proven, or
// creates it when none has been chosen. The same password signs in to the
// dashboard, because it is stored as the server's management secret.
func requireMasterPassword(p Paths, cfg settings, input passwordInput, errOut io.Writer) (string, error) {
	if masterPasswordChosen(p, cfg) {
		return verifyMasterPassword(cfg, input, errOut)
	}
	return chooseMasterPassword(p, input, errOut)
}

// masterPasswordChosen is false for a new install, and for an older one that
// still has the generated key file: nobody picked that key.
func masterPasswordChosen(p Paths, cfg settings) bool {
	if cfg.Management.SecretKey == "" {
		return false
	}
	_, err := os.Stat(p.ManagementKey)
	return errors.Is(err, os.ErrNotExist)
}

func verifyMasterPassword(cfg settings, input passwordInput, errOut io.Writer) (string, error) {
	for attempt := 0; attempt < input.attempts; attempt++ {
		password, err := input.ask("Master password: ")
		if err != nil {
			return "", err
		}
		if matchesMasterPassword(cfg.Management.SecretKey, password) {
			return password, nil
		}
		fmt.Fprintln(errOut, "Wrong password.")
	}
	return "", errors.New("wrong master password")
}

// matchesMasterPassword also accepts plain text, because a secret edited into
// the config by hand stays unhashed until the server next starts.
func matchesMasterPassword(stored, password string) bool {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(password)) == 1
}

func chooseMasterPassword(p Paths, input passwordInput, errOut io.Writer) (string, error) {
	fmt.Fprintln(errOut, "Choose a master password. You need it to start prox and to open the dashboard.")
	for attempt := 0; attempt < input.attempts; attempt++ {
		password, err := askNewPassword(input)
		if err == nil {
			return password, saveMasterPassword(p, password)
		}
		if !errors.Is(err, errInvalidNewPassword) {
			return "", err
		}
		fmt.Fprintln(errOut, err)
	}
	return "", errors.New("no master password set")
}

func askNewPassword(input passwordInput) (string, error) {
	password, err := input.ask("New master password: ")
	if err != nil {
		return "", err
	}
	if len(password) < minMasterPasswordBytes || len(password) > maxMasterPasswordBytes {
		return "", fmt.Errorf("%w: use %d to %d characters", errInvalidNewPassword, minMasterPasswordBytes, maxMasterPasswordBytes)
	}
	repeated, err := input.ask("Type it again: ")
	if err != nil {
		return "", err
	}
	if repeated != password {
		return "", fmt.Errorf("%w: the two did not match", errInvalidNewPassword)
	}
	return password, nil
}

// saveMasterPassword keeps only a hash, then drops the old generated key, which
// no longer opens anything.
func saveMasterPassword(p Paths, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash master password: %w", err)
	}
	if err = config.SaveConfigPreserveCommentsUpdateNestedScalar(p.Config, masterPasswordConfigPath, string(hash)); err != nil {
		return fmt.Errorf("save master password: %w", err)
	}
	if err = os.Remove(p.ManagementKey); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old management key: %w", err)
	}
	return nil
}

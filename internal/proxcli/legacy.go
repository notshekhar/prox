package proxcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// importLegacyAccounts copies logins from the folder used before prox had its
// own home. It runs once, and only from `prox start` after confirming no proxy
// is running: a live proxy rotates refresh tokens in the old folder, which
// would leave any copy unusable. The originals are left in place.
func importLegacyAccounts(p Paths, cfg settings) (int, error) {
	if _, err := os.Stat(p.ImportMarker); err == nil {
		return 0, nil
	}
	dest, err := authDir(p, cfg)
	if err != nil {
		return 0, err
	}
	logins, err := filepath.Glob(filepath.Join(p.LegacyAuth, "*.json"))
	if err != nil {
		return 0, fmt.Errorf("find old accounts: %w", err)
	}
	if err = os.MkdirAll(dest, privateDir); err != nil {
		return 0, fmt.Errorf("create %s: %w", dest, err)
	}
	copied := 0
	for _, login := range logins {
		wrote, errCopy := copyLogin(login, filepath.Join(dest, filepath.Base(login)))
		if errCopy != nil {
			return copied, errCopy
		}
		if wrote {
			copied++
		}
	}
	if err = os.WriteFile(p.ImportMarker, nil, privateFile); err != nil {
		return copied, fmt.Errorf("record account import: %w", err)
	}
	return copied, nil
}

// copyLogin never replaces a login prox already has, which may be newer.
func copyLogin(from, to string) (bool, error) {
	if _, err := os.Stat(to); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check account: %w", err)
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		return false, fmt.Errorf("read old account: %w", err)
	}
	if err = os.WriteFile(to, raw, privateFile); err != nil {
		return false, fmt.Errorf("write account: %w", err)
	}
	return true, nil
}

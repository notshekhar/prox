package localmodels

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// checksumsFile lists "<sha256>  <path>" for a repository's model files.
const checksumsFile = "SHA256SUMS"

// fetchVerified downloads files from the model's repository at its pinned
// revision. A file the checksums list must match, or nothing is written.
func (in *Installer) fetchVerified(ctx context.Context, log io.Writer, m Model, files []string) error {
	sums, err := in.checksums(ctx, m)
	if err != nil {
		return err
	}
	for _, file := range files {
		fmt.Fprintf(log, "download %s\n", file)
		if err = in.fetchFile(ctx, m, file, sums[file]); err != nil {
			return fmt.Errorf("download %s: %w", file, err)
		}
	}
	return nil
}

func (in *Installer) checksums(ctx context.Context, m Model) (map[string]string, error) {
	body, err := in.get(ctx, m, checksumsFile)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", checksumsFile, err)
	}
	defer body.Close()
	sums := map[string]string{}
	lines := bufio.NewScanner(body)
	for lines.Scan() {
		if fields := strings.Fields(lines.Text()); len(fields) == 2 {
			sums[fields[1]] = fields[0]
		}
	}
	return sums, lines.Err()
}

// fetchFile writes next to the target and renames, so a cut download never
// replaces a good file.
func (in *Installer) fetchFile(ctx context.Context, m Model, file, wantSum string) error {
	target := filepath.Join(in.Layout.ModelDir(m), filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body, err := in.get(ctx, m, file)
	if err != nil {
		return err
	}
	defer body.Close()
	partial := target + ".partial"
	got, err := writeHashed(partial, body)
	if err == nil && wantSum != "" && got != wantSum {
		err = fmt.Errorf("checksum mismatch: got %s, want %s", got, wantSum)
	}
	if err != nil {
		_ = os.Remove(partial)
		return err
	}
	return os.Rename(partial, target)
}

func writeHashed(path string, r io.Reader) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), r)
	if errClose := f.Close(); err == nil {
		err = errClose
	}
	return hex.EncodeToString(h.Sum(nil)), err
}

func (in *Installer) get(ctx context.Context, m Model, file string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/%s/resolve/%s/%s", in.HubURL, m.Repo, m.Revision, file)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s %s", url, resp.Status, bytes.TrimSpace(snippet))
	}
	return resp.Body, nil
}

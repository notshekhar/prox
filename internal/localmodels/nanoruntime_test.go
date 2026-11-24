package localmodels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHub serves a repository at the model's pinned revision. sums overrides
// what SHA256SUMS claims for a file.
func fakeHub(t *testing.T, m Model, files map[string]string, sums map[string]string) *httptest.Server {
	t.Helper()
	var listing strings.Builder
	for name, body := range files {
		sum := sha256.Sum256([]byte(body))
		want := hex.EncodeToString(sum[:])
		if override, ok := sums[name]; ok {
			want = override
		}
		if strings.HasSuffix(name, ".bin") {
			fmt.Fprintf(&listing, "%s  %s\n", want, name)
		}
	}
	prefix := "/" + m.Repo + "/resolve/" + m.Revision + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name == checksumsFile {
			fmt.Fprint(w, listing.String())
			return
		}
		body, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func nanoInstaller(t *testing.T, hub *httptest.Server) *Installer {
	return &Installer{Layout: Layout{Root: t.TempDir()}, HTTP: hub.Client(), HubURL: hub.URL}
}

func nanoFiles() map[string]string {
	return map[string]string{
		nanoTextWeights: "JNT3 weights", nanoImageWeights: "JNI1 weights", nanoSpeechWeights: "JNS1 weights",
		nanoImageLabels: `["golf ball"]`, nanoSpeechLabels: `["balance"]`, "LICENSE": "Apache-2.0", "NOTICE": "Supersonic Labs",
	}
}

func TestNanoInstallsWithoutPython(t *testing.T) {
	m, _ := Find("julia-2-nano")
	in := nanoInstaller(t, fakeHub(t, m, nanoFiles(), nil))
	in.FindUV = func() (string, error) { return "", ErrNoUV } // proves uv is never needed
	if err := in.Install(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if !in.Layout.Installed(m) {
		t.Fatal("not marked installed")
	}
	got, _ := os.ReadFile(filepath.Join(in.Layout.ModelDir(m), "text", "nano_text.bin"))
	if string(got) != "JNT3 weights" {
		t.Fatalf("weights = %q", got)
	}
}

func TestNanoInstallRejectsATamperedFile(t *testing.T) {
	m, _ := Find("julia-2-nano")
	hub := fakeHub(t, m, nanoFiles(), map[string]string{nanoTextWeights: strings.Repeat("0", 64)})
	in := nanoInstaller(t, hub)
	err := in.Install(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if in.Layout.Installed(m) {
		t.Fatal("a tampered download looks installed")
	}
	weights := filepath.Join(in.Layout.ModelDir(m), "text", "nano_text.bin")
	for _, path := range []string{weights, weights + ".partial"} {
		if exists(path) {
			t.Fatalf("%s was left behind", path)
		}
	}
}

func TestNanoInstallReportsAMissingFile(t *testing.T) {
	m, _ := Find("julia-2-nano")
	files := nanoFiles()
	delete(files, "NOTICE")
	err := nanoInstaller(t, fakeHub(t, m, files, nil)).Install(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

// nanoDir is a downloaded Julia-2-Nano repository (see nano/model_test.go).
const nanoDirEnv = "PROX_NANO_DIR"

// realNanoManager serves Nano from the real download, linked into a temp layout.
func realNanoManager(t *testing.T) (*Manager, Model) {
	t.Helper()
	dir := os.Getenv(nanoDirEnv)
	if dir == "" {
		t.Skipf("set %s to a Julia-2-Nano download", nanoDirEnv)
	}
	m, _ := Find("julia-2-nano")
	layout := Layout{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(layout.ModelDir(m)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, layout.ModelDir(m)); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, installedMarker)
	if !exists(marker) {
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(marker) })
	}
	mg := NewManager(layout)
	t.Cleanup(func() { mg.Close(context.Background()) })
	if _, err := mg.Resolve("julia-2-nano"); err != nil {
		t.Fatal(err)
	}
	return mg, m
}

// The manager serves Nano in process, and its answers follow the reference C's choice.
func TestNanoAnswersThroughTheManager(t *testing.T) {
	mg, m := realNanoManager(t)
	req := Request{
		State: []byte(`"Help! My payouts have been failing for 3 days."`),
		Questions: []Question{
			{Name: "team", Type: TypeChoice, Instructions: []byte(`"Route to which team?"`), Criteria: []byte(`{"billing":"Payments, invoicing, refunds","technical":"Bugs, outages, integrations","sales":"Pricing, upgrades, new accounts"}`)},
			{Name: "urgent", Type: TypeNoul, Instructions: []byte(`"Is this urgent?"`)},
			{Name: "level", Type: TypeScore, Criteria: []byte(`["Can wait","This week","Today"]`)},
		},
	}
	res, err := mg.Decide(context.Background(), m, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Answers["team"].Choice; got != "billing" {
		t.Fatalf("team = %q, the reference C chose billing", got)
	}
	if p := res.Answers["urgent"].Noul; p == nil || *p <= 0 || *p >= 1 {
		t.Fatalf("urgent = %v", p)
	}
	assertDistribution(t, res.Answers["level"].Probabilities, 3)
	if res.InputTokens == 0 {
		t.Fatal("no tokens counted")
	}
	if st := mg.Statuses()[1]; !st.Running || st.Device != "cpu" || len(st.Labels[MediaImage]) != 112 || len(st.Labels[MediaAudio]) != 14 {
		t.Fatalf("status = running %v device %q labels %d/%d", st.Running, st.Device, len(st.Labels[MediaImage]), len(st.Labels[MediaAudio]))
	}
}

func assertDistribution(t *testing.T, probs map[string]float64, n int) {
	t.Helper()
	total := 0.0
	for _, p := range probs {
		total += p
	}
	if len(probs) != n || total < 0.999 || total > 1.001 {
		t.Fatalf("probabilities = %v, want %d summing to 1", probs, n)
	}
}

func TestNanoRejectsABadQuestion(t *testing.T) {
	s := &nanoSession{}
	_, err := s.decide(context.Background(), Request{State: []byte(`"x"`), Questions: []Question{{Name: "q", Type: "box"}}})
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v, want a RequestError", err)
	}
}

func TestSoftmax(t *testing.T) {
	p := softmax([]float32{5.8, -3.3})
	if p[0] < 0.999 || p[0]+p[1] < 0.9999 || p[0]+p[1] > 1.0001 {
		t.Fatalf("softmax = %v", p)
	}
	if p := softmax([]float32{1000, 1000}); p[0] != 0.5 {
		t.Fatalf("large equal scores = %v, want an even split without overflow", p)
	}
}

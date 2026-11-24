package localmodels

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake model worker speaking the real protocol,
// so these tests drive a real subprocess over real pipes.
const fakeWorkerEnv = "PROX_FAKE_LOCAL_WORKER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeWorkerEnv); mode != "" {
		os.Exit(runFakeWorker(mode))
	}
	os.Exit(m.Run())
}

func runFakeWorker(mode string) int {
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		line, _ := json.Marshal(v)
		out.Write(append(line, '\n'))
		out.Flush()
	}
	switch mode {
	case "fail-load":
		fmt.Fprintln(os.Stderr, "Traceback: weights missing")
		send(map[string]any{"error": "could not load the model: FileNotFoundError"})
		return 1
	case "die-load":
		fmt.Fprintln(os.Stderr, "Segmentation fault in torch")
		return 139
	}
	send(map[string]any{"ready": true, "model": "julia-2-flash", "device": "cpu"})
	lines := bufio.NewScanner(os.Stdin)
	lines.Buffer(make([]byte, 1<<20), maxLine)
	for lines.Scan() {
		var req struct {
			ID    int64          `json:"id"`
			State jsontext.Value `json:"state"`
		}
		_ = json.Unmarshal(lines.Bytes(), &req)
		var state string
		_ = json.Unmarshal(req.State, &state)
		switch state {
		case "crash":
			return 1
		case "bad":
			send(map[string]any{"id": req.ID, "error": "ValueError: bad question"})
			continue
		case "slow":
			time.Sleep(300 * time.Millisecond)
		}
		send(map[string]any{"id": req.ID, "answers": map[string]any{"q": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.8}}, "input_tokens": len(state)})
	}
	return 0
}

func fakeCommand(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), fakeWorkerEnv+"="+mode)
	return cmd
}

func textRequest(state string) Request {
	s, _ := json.Marshal(state)
	return Request{State: s, Questions: []Question{{Name: "q", Type: TypeNoul}}}
}

func startFake(t *testing.T, mode string) *worker {
	t.Helper()
	w, err := startWorker(context.Background(), fakeCommand(mode))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { w.stop(context.Background()) })
	return w
}

func TestWorkerAnswersAndReportsDevice(t *testing.T) {
	w := startFake(t, "ok")
	if w.device() != "cpu" {
		t.Fatalf("device = %q", w.device())
	}
	res, err := w.decide(context.Background(), textRequest("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := *res.Answers["q"].Noul; got != 0.9 || res.InputTokens != 5 {
		t.Fatalf("answer = %v tokens = %d", got, res.InputTokens)
	}
}

func TestWorkerLoadFailureCarriesTheReason(t *testing.T) {
	for mode, want := range map[string]string{"fail-load": "FileNotFoundError", "die-load": "Segmentation fault"} {
		cmd := fakeCommand(mode)
		_, err := startWorker(context.Background(), cmd)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want it to mention %q", mode, err, want)
		}
		if cmd.ProcessState == nil {
			t.Fatalf("%s: the failed worker was never reaped", mode)
		}
	}
}

func TestWorkerRequestErrorKeepsTheModelUp(t *testing.T) {
	w := startFake(t, "ok")
	_, err := w.decide(context.Background(), textRequest("bad"))
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v, want a RequestError", err)
	}
	if _, err = w.decide(context.Background(), textRequest("after")); err != nil {
		t.Fatalf("next call failed: %v", err)
	}
}

// A caller that gives up must not leave its late reply to be read by the next caller.
func TestWorkerAbandonedCallDoesNotShiftReplies(t *testing.T) {
	w := startFake(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := w.decide(ctx, textRequest("slow"))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the slow request reach the worker first
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned call err = %v", err)
	}
	res, err := w.decide(context.Background(), textRequest("second"))
	if err != nil {
		t.Fatal(err)
	}
	if res.InputTokens != len("second") {
		t.Fatalf("got the reply for another request: tokens = %d", res.InputTokens)
	}
}

func TestWorkerCrashFailsPendingCall(t *testing.T) {
	w := startFake(t, "ok")
	_, err := w.decide(context.Background(), textRequest("crash"))
	if err == nil || !strings.Contains(err.Error(), "the model stopped") {
		t.Fatalf("err = %v", err)
	}
	if w.alive() {
		t.Fatal("worker still reported alive after exiting")
	}
}

func TestWorkerConcurrentCalls(t *testing.T) {
	w := startFake(t, "ok")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := strings.Repeat("x", i+1)
			res, err := w.decide(context.Background(), textRequest(state))
			if err != nil || res.InputTokens != len(state) {
				t.Errorf("call %d: tokens %d err %v", i, res.InputTokens, err)
			}
		}()
	}
	wg.Wait()
}

// installedLayout fakes a finished install so the manager will serve the model.
func installedLayout(t *testing.T, m Model) Layout {
	t.Helper()
	layout := Layout{Root: t.TempDir()}
	if err := touchAll(layout.ModelDir(m), append([]string{installedMarker}, m.Required...)); err != nil {
		t.Fatal(err)
	}
	if err := touchAll(filepath.Dir(julia2Runtime.pythonPath(layout)), []string{"python"}); err != nil {
		t.Fatal(err)
	}
	return layout
}

// fakeManager serves julia-2-flash from the fake worker.
func fakeManager(t *testing.T) (*Manager, Model) {
	m, _ := Find("julia-2-flash")
	mg := NewManager(installedLayout(t, m))
	mg.load = func(ctx context.Context, _ Model) (session, error) { return startWorker(ctx, fakeCommand("ok")) }
	return mg, m
}

func TestManagerRestartsADeadWorker(t *testing.T) {
	mg, m := fakeManager(t)
	defer mg.Close(context.Background())

	if _, err := mg.Decide(context.Background(), m, textRequest("crash")); err == nil {
		t.Fatal("expected the crash to fail the call")
	}
	res, err := mg.Decide(context.Background(), m, textRequest("again"))
	if err != nil || res.InputTokens != len("again") {
		t.Fatalf("after restart: %v %v", res, err)
	}
}

// The real Python command runs: the test binary stands in for the venv's
// python and answers as the fake worker.
func TestPythonRuntimeStartsItsWorker(t *testing.T) {
	m, _ := Find("julia-2-flash")
	layout := installedLayout(t, m)
	python := julia2Runtime.pythonPath(layout)
	if err := os.Remove(python); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(os.Args[0], python); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeWorkerEnv, "ok")
	s, err := julia2Runtime.start(context.Background(), layout, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop(context.Background())
	if _, err = s.decide(context.Background(), textRequest("hi")); err != nil {
		t.Fatal(err)
	}
	if script, _ := os.ReadFile(julia2Runtime.workerScript(layout)); string(script) != string(julia2Worker) {
		t.Fatal("worker script was not written next to the runtime")
	}
}

func TestManagerCloseStopsWorkers(t *testing.T) {
	mg, m := fakeManager(t)
	if _, err := mg.Decide(context.Background(), m, textRequest("hi")); err != nil {
		t.Fatal(err)
	}
	w := mg.running(m.ID)
	mg.Close(context.Background())
	if w.alive() {
		t.Fatal("worker still running after Close")
	}
	if _, err := mg.Decide(context.Background(), m, textRequest("hi")); err == nil {
		t.Fatal("a closed manager started a new worker")
	}
}

func TestManagerResolve(t *testing.T) {
	m, _ := Find("julia-2-flash")
	mg := NewManager(Layout{Root: t.TempDir()})
	var notInstalled *NotInstalledError
	if _, err := mg.Resolve("supersonic/julia-2-flash"); !errors.As(err, &notInstalled) {
		t.Fatalf("err = %v, want NotInstalledError", err)
	}
	if _, err := mg.Resolve("gpt-5"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v, want ErrUnknownModel", err)
	}
	layout := installedLayout(t, m)
	mg = NewManager(layout)
	if got, err := mg.Resolve("Julia-2-Flash"); err != nil || got.ID != m.ID {
		t.Fatalf("resolve = %v %v", got.ID, err)
	}
	// An install from before images and audio lacks their files.
	if err := os.Remove(filepath.Join(layout.ModelDir(m), "image.safetensors")); err != nil {
		t.Fatal(err)
	}
	_, err := mg.Resolve("julia-2-flash")
	if !errors.As(err, &notInstalled) || !notInstalled.Outdated || !strings.Contains(err.Error(), "prox local install julia-2-flash") {
		t.Fatalf("outdated install: err = %v", err)
	}
}

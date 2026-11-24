package localmodels

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

type workerReply struct {
	ID int64 `json:"id"`
	Result
	Error string `json:"error"`
}

type readyLine struct {
	Ready  bool   `json:"ready"`
	Model  string `json:"model"`
	Device string `json:"device"`
	Error  string `json:"error"`
}

// maxLine bounds one protocol line read back from the worker; answers are
// small, but a 255-option choice with long keys is a few hundred KB.
const maxLine = 16 << 20

// stderrTail is how much of the worker's stderr is kept to explain a crash.
const stderrTail = 4 << 10

// worker is one running model process. Calls may overlap: each waits for the
// reply carrying its own id, so a caller that gives up never shifts the
// replies of the next one.
type worker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	runsOn string // the device the worker reported when ready

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan workerReply
	exited  chan struct{}
	exitErr error
	stderr  *tailBuffer
}

// startWorker runs cmd and waits for the model to load.
func startWorker(ctx context.Context, cmd *exec.Cmd) (*worker, error) {
	w := &worker{cmd: cmd, pending: map[int64]chan workerReply{}, exited: make(chan struct{}), stderr: &tailBuffer{max: stderrTail}}
	stdout, err := w.pipes()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the model: %w", err)
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 64<<10), maxLine)
	if err = w.awaitReady(ctx, lines); err != nil {
		w.kill()
		return nil, err
	}
	go w.readReplies(lines)
	return w, nil
}

func (w *worker) pipes() (io.Reader, error) {
	stdin, err := w.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := w.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	w.stdin = stdin
	w.cmd.Stderr = w.stderr
	return stdout, nil
}

func (w *worker) awaitReady(ctx context.Context, lines *bufio.Scanner) error {
	got := make(chan error, 1)
	go func() { got <- w.readReady(lines) }()
	select {
	case err := <-got:
		return err
	case <-ctx.Done():
		return fmt.Errorf("the model did not load in time: %w", ctx.Err())
	}
}

func (w *worker) readReady(lines *bufio.Scanner) error {
	if !lines.Scan() {
		return w.describeExit("the model exited while loading")
	}
	var ready readyLine
	if err := json.Unmarshal(lines.Bytes(), &ready); err != nil {
		return fmt.Errorf("the model sent an unreadable first line: %q", lines.Text())
	}
	if !ready.Ready {
		// The worker exits right after reporting why; reap it so no zombie stays behind.
		w.kill()
		_ = w.cmd.Wait()
		return errors.New(ready.Error)
	}
	w.runsOn = ready.Device
	return nil
}

func (w *worker) readReplies(lines *bufio.Scanner) {
	for lines.Scan() {
		var reply workerReply
		if err := json.Unmarshal(lines.Bytes(), &reply); err != nil {
			continue
		}
		w.deliver(reply)
	}
	w.exitErr = w.describeExit("the model stopped")
	close(w.exited)
}

func (w *worker) deliver(reply workerReply) {
	w.mu.Lock()
	ch := w.pending[reply.ID]
	delete(w.pending, reply.ID)
	w.mu.Unlock()
	if ch != nil {
		ch <- reply
	}
}

// describeExit waits for the process and folds the end of its stderr into the error.
func (w *worker) describeExit(what string) error {
	waitErr := w.cmd.Wait()
	detail := strings.TrimSpace(w.stderr.String())
	if detail == "" && waitErr != nil {
		detail = waitErr.Error()
	}
	if detail == "" {
		return errors.New(what)
	}
	return fmt.Errorf("%s: %s", what, lastLines(detail, 6))
}

func (w *worker) device() string { return w.runsOn }

// decide sends one request and waits for its reply.
func (w *worker) decide(ctx context.Context, req Request) (Result, error) {
	id, ch := w.register()
	defer w.unregister(id)
	if err := w.send(id, req); err != nil {
		return Result{}, err
	}
	select {
	case reply := <-ch:
		if reply.Error != "" {
			return Result{}, &RequestError{Message: reply.Error}
		}
		return reply.Result, nil
	case <-w.exited:
		return Result{}, w.exitErr
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (w *worker) register() (int64, chan workerReply) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	ch := make(chan workerReply, 1)
	w.pending[w.nextID] = ch
	return w.nextID, ch
}

func (w *worker) unregister(id int64) {
	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
}

func (w *worker) send(id int64, req Request) error {
	questions, err := questionsJSON(req.Questions)
	if err != nil {
		return err
	}
	line, err := json.Marshal(struct {
		ID        int64          `json:"id"`
		State     jsontext.Value `json:"state"`
		Questions jsontext.Value `json:"questions"`
	}{id, req.State, questions})
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if _, err = w.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("send to the model: %w", err)
	}
	return nil
}

func (w *worker) alive() bool {
	select {
	case <-w.exited:
		return false
	default:
		return true
	}
}

// stop closes stdin, which ends the worker's read loop, and kills it if it
// is still running when ctx ends.
func (w *worker) stop(ctx context.Context) {
	_ = w.stdin.Close()
	select {
	case <-w.exited:
	case <-ctx.Done():
		w.kill()
		<-w.exited
	}
}

func (w *worker) kill() {
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
}

// RequestError is a request the model could not answer; the model itself is fine.
type RequestError struct{ Message string }

func (e *RequestError) Error() string { return e.Message }

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

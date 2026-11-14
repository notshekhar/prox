package proxcli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// waitForFile polls instead of sleeping a fixed time, so a slow machine only
// makes the test slower, not flaky.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func TestClaudeIsStoppedWhenProxIsTerminated(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	started, stopped := filepath.Join(s.home, "started"), filepath.Join(s.home, "stopped")
	s.fakeTool(t, "claude", fmt.Sprintf(
		`trap 'echo > %s; exit 143' TERM; echo > %s; while :; do sleep 0.05; done`, stopped, started))

	exit := make(chan int, 1)
	go func() {
		outcome, _, _ := run("claude")
		exit <- outcome.ExitCode
	}()
	waitForFile(t, started)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-exit:
		if code != 143 {
			t.Fatalf("exit = %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("claude kept running after prox was terminated")
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatal("claude never received SIGTERM")
	}
}

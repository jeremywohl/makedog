// Test suite for live control: a real watch session commanded remotely —
// status, restart, signal, stop, start — plus registry hygiene on exit.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLiveControl drives one watch session through the whole remote surface.
func TestLiveControl(t *testing.T) {
	script := "#!/bin/sh\n" +
		"trap 'echo got-usr1' USR1\n" +
		"trap 'exit 0' TERM\n" +
		"echo serving\n" +
		"while :; do sleep 0.1; done\n"
	cmd, snapshot, childPid := makedogSession(t, script)

	// The control verbs run as separate processes against the same store.
	ctl := func(args ...string) (string, int) {
		c := exec.Command(makedogBinary(t), args...)
		c.Dir = cmd.Dir
		c.Env = append(os.Environ(), "MAKEDOG_STATE_DIR="+filepath.Join(cmd.Dir, "state"))
		out, _ := c.CombinedOutput()
		return string(out), c.ProcessState.ExitCode()
	}

	// The socket comes up with the session; give it a moment.
	deadline := time.Now().Add(5 * time.Second)
	var out string
	var code int
	for {
		out, code = ctl("status", "--json")
		if code == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var st ctrlResponse
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("status: exit %d, output %q", code, out)
	}
	if !st.Running || st.Run != 1 || st.ChildPid != childPid {
		t.Fatalf("status = %+v; want run 1 with child %d", st, childPid)
	}

	// A "serving" echo proves the child's traps are installed before we signal.
	waitForOutput(t, cmd, snapshot, "child readiness", func(s string) bool {
		return strings.Contains(s, "serving")
	})

	// signal: the child's USR1 trap must echo through the run log path.
	if out, code = ctl("signal", "USR1"); code != 0 {
		t.Fatalf("signal: exit %d, output %q", code, out)
	}
	waitForOutput(t, cmd, snapshot, "USR1 echo", func(s string) bool {
		return strings.Contains(s, "got-usr1")
	})

	// restart: a new run begins and the response reports it.
	if out, code = ctl("restart"); code != 0 || !strings.Contains(out, "run 2 running") {
		t.Fatalf("restart: exit %d, output %q", code, out)
	}
	waitForOutput(t, cmd, snapshot, "run 2 start", func(s string) bool {
		return strings.Contains(s, "start 2")
	})

	// stop, then start: the binary halts and resumes as run 3.
	if out, code = ctl("stop"); code != 0 || !strings.Contains(out, "stopped (last run 2)") {
		t.Fatalf("stop: exit %d, output %q", code, out)
	}
	if out, code = ctl("start"); code != 0 || !strings.Contains(out, "run 3 running") {
		t.Fatalf("start: exit %d, output %q", code, out)
	}

	// Clean exit deregisters: status finds nothing afterward.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitForExit(t, cmd, childPidFrom(t, snapshot(), 3))

	if _, code = ctl("status"); code != 1 {
		t.Errorf("status after exit: code %d; want 1 (no live instances)", code)
	}
}

// childPidFrom digs a given run's child pid out of the session transcript.
func childPidFrom(t *testing.T, transcript string, run int) int {
	t.Helper()
	re := regexp.MustCompile(fmt.Sprintf(`start %d \S+ \(pid (\d+)`, run))
	m := re.FindStringSubmatch(transcript)
	if m == nil {
		t.Fatalf("no start message for run %d in:\n%s", run, transcript)
	}
	pid, _ := strconv.Atoi(m[1])
	return pid
}

// TestControlSurvivesStalledTerminal reproduces the undrained-pty case: with
// nobody reading makedog's terminal, its banners fill the tiny pty buffer and
// a synchronous writer would wedge the monitor loop mid-restart, timing out
// the control reply. The async terminal writer must keep control live.
func TestControlSurvivesStalledTerminal(t *testing.T) {
	cmd, snapshot, _ := makedogSession(t, "#!/bin/sh\ntrap 'exit 0' TERM\necho serving\nwhile :; do sleep 0.1; done\n")
	waitForOutput(t, cmd, snapshot, "child readiness", func(s string) bool {
		return strings.Contains(s, "serving")
	})

	// Stall the terminal: stop draining the pty for the whole exchange.
	pauseDrain(t, cmd, true)
	defer pauseDrain(t, cmd, false)

	ctl := exec.Command(makedogBinary(t), "restart")
	ctl.Dir = cmd.Dir
	ctl.Env = append(os.Environ(), "MAKEDOG_STATE_DIR="+filepath.Join(cmd.Dir, "state"))

	done := make(chan struct{})
	var out []byte
	go func() { out, _ = ctl.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		ctl.Process.Kill()
		t.Fatal("restart against a stalled terminal never returned")
	}
	if code := ctl.ProcessState.ExitCode(); code != 0 || !strings.Contains(string(out), "run 2 running") {
		t.Errorf("restart under stall: exit %d, output %q", code, out)
	}

	// Resume the terminal; the queued banners must flow before we can read
	// run 2's pid out of the transcript.
	pauseDrain(t, cmd, false)
	waitForOutput(t, cmd, snapshot, "run 2 banner", func(s string) bool {
		return strings.Contains(s, "start 2")
	})
	cmd.Process.Signal(syscall.SIGTERM)
	waitForExit(t, cmd, childPidFrom(t, snapshot(), 2))
}

// TestLiveInstancesPrunesStale verifies dead registry entries are removed.
func TestLiveInstancesPrunesStale(t *testing.T) {
	s := &binaryStore{dir: t.TempDir()}

	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	s.registerInstance(instanceInfo{Pid: dead.Process.Pid, Socket: "/nonexistent", Binary: "x", Started: time.Now()})
	s.registerInstance(instanceInfo{Pid: os.Getpid(), Socket: "/nonexistent", Binary: "x", Started: time.Now()})

	live := s.liveInstances()
	if len(live) != 1 || live[0].Pid != os.Getpid() {
		t.Errorf("liveInstances = %+v; want just this process", live)
	}
	if _, err := os.Stat(s.instancePath(dead.Process.Pid)); !os.IsNotExist(err) {
		t.Error("stale record not pruned")
	}
}

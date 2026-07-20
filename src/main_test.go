// Test suite for core makedog functionality.
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestHandleKeypress(t *testing.T) {
	w := &Makedog{binaryPath: "/bin/echo"}

	tests := []struct {
		name           string
		key            byte
		expectStop     bool
		expectStart    bool
		expectExit     bool
		expectFnNotNil bool
	}{
		{"ctrl-c", 3, true, false, true, false},
		{"h key", 'h', false, false, false, false},
		{"c key", 'c', false, false, false, false},
		{"m key", 'm', true, true, false, true},
		{"q key", 'q', true, false, true, false},
		{"unknown key", 'z', false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := w.handleKeypress(tt.key)

			if s.stopBinary != tt.expectStop {
				t.Errorf("stopBinary = %v; want %v", s.stopBinary, tt.expectStop)
			}
			if s.startBinary != tt.expectStart {
				t.Errorf("startBinary = %v; want %v", s.startBinary, tt.expectStart)
			}
			if s.exitAfter != tt.expectExit {
				t.Errorf("exitAfter = %v; want %v", s.exitAfter, tt.expectExit)
			}
			if (s.action != nil) != tt.expectFnNotNil {
				t.Errorf("fn != nil = %v; want %v", s.action != nil, tt.expectFnNotNil)
			}
		})
	}
}

func TestHandleKeypressRestart(t *testing.T) {
	tests := []struct {
		name              string
		setupProcess      func(t *testing.T) *exec.Cmd
		expectStop        bool
		expectStart       bool
		expectExit        bool
		expectSpinCleared bool
	}{
		{
			name: "restart with running process",
			setupProcess: func(t *testing.T) *exec.Cmd {
				cmd := exec.Command("sleep", "10")
				if err := cmd.Start(); err != nil {
					t.Fatalf("Failed to start test process: %v", err)
				}
				t.Cleanup(func() { cmd.Process.Kill() })

				// Verify ProcessState is nil (process still running, not Wait()ed)
				if cmd.ProcessState != nil {
					t.Fatal("ProcessState should be nil for running process")
				}
				return cmd
			},
			expectStop:        true,
			expectStart:       true,
			expectExit:        false,
			expectSpinCleared: true,
		},
		{
			name: "restart with no running process",
			setupProcess: func(t *testing.T) *exec.Cmd {
				return nil
			},
			expectStop:        false,
			expectStart:       false,
			expectExit:        false,
			expectSpinCleared: false,
		},
		{
			name: "restart with exited process",
			setupProcess: func(t *testing.T) *exec.Cmd {
				cmd := exec.Command("echo", "test")
				if err := cmd.Run(); err != nil {
					t.Fatalf("Failed to run test process: %v", err)
				}

				// Verify ProcessState is not nil (process has exited and been Wait()ed)
				if cmd.ProcessState == nil {
					t.Fatal("ProcessState should not be nil for exited process")
				}
				return cmd
			},
			expectStop:        false,
			expectStart:       false,
			expectExit:        false,
			expectSpinCleared: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupProcess(t)
			var r *run
			if cmd != nil {
				r = &run{cmd: cmd}
			}
			w := &Makedog{
				binaryPath:   "/bin/echo",
				run:          r,
				restartTimes: []time.Time{time.Now()},
			}

			s := w.handleKeypress('r')

			if s.stopBinary != tt.expectStop {
				t.Errorf("stopBinary = %v; want %v", s.stopBinary, tt.expectStop)
			}
			if s.startBinary != tt.expectStart {
				t.Errorf("startBinary = %v; want %v", s.startBinary, tt.expectStart)
			}
			if s.exitAfter != tt.expectExit {
				t.Errorf("exitAfter = %v; want %v", s.exitAfter, tt.expectExit)
			}

			if tt.expectSpinCleared && len(w.restartTimes) != 0 {
				t.Error("Expected spin tracking to be cleared")
			}
			if !tt.expectSpinCleared && len(w.restartTimes) == 0 {
				t.Error("Expected spin tracking to be preserved")
			}
		})
	}
}

func TestHandleKeypressStartStop(t *testing.T) {
	tests := []struct {
		name         string
		setupProcess func(t *testing.T) *exec.Cmd
		expectStop   bool
		expectStart  bool
		expectExit   bool
	}{
		{
			name: "stop running process",
			setupProcess: func(t *testing.T) *exec.Cmd {
				cmd := exec.Command("sleep", "10")
				if err := cmd.Start(); err != nil {
					t.Fatalf("Failed to start test process: %v", err)
				}
				t.Cleanup(func() { cmd.Process.Kill() })
				return cmd
			},
			expectStop:  true,
			expectStart: false,
			expectExit:  false,
		},
		{
			name: "start when no process running",
			setupProcess: func(t *testing.T) *exec.Cmd {
				return nil
			},
			expectStop:  false,
			expectStart: true,
			expectExit:  false,
		},
		{
			name: "start with exited process",
			setupProcess: func(t *testing.T) *exec.Cmd {
				cmd := exec.Command("echo", "test")
				if err := cmd.Run(); err != nil {
					t.Fatalf("Failed to run test process: %v", err)
				}
				return cmd
			},
			expectStop:  false,
			expectStart: true,
			expectExit:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupProcess(t)
			var r *run
			if cmd != nil {
				r = &run{cmd: cmd}
			}
			w := &Makedog{
				binaryPath: "/bin/echo",
				run:        r,
			}

			s := w.handleKeypress('x')

			if s.stopBinary != tt.expectStop {
				t.Errorf("stopBinary = %v; want %v", s.stopBinary, tt.expectStop)
			}
			if s.startBinary != tt.expectStart {
				t.Errorf("startBinary = %v; want %v", s.startBinary, tt.expectStart)
			}
			if s.exitAfter != tt.expectExit {
				t.Errorf("exitAfter = %v; want %v", s.exitAfter, tt.expectExit)
			}
		})
	}
}

func TestCheckFileModification(t *testing.T) {
	// Create a temporary binary file for testing
	tmpDir := t.TempDir()
	tmpBinary := filepath.Join(tmpDir, "test-binary")

	// Create initial file
	if err := os.WriteFile(tmpBinary, []byte("v1"), 0755); err != nil {
		t.Fatalf("Failed to create test binary: %v", err)
	}

	// Create makedog instance
	w := &Makedog{binaryPath: tmpBinary}

	// Get initial mtime
	initialMtime, err := w.getMtime()
	if err != nil {
		t.Fatalf("Failed to get initial mtime: %v", err)
	}
	w.lastMtime = initialMtime

	// Test 1: No modification - should return empty step
	s := w.checkFileModification()
	if s.stopBinary || s.startBinary || s.exitAfter || s.action != nil {
		t.Error("Expected empty step when file not modified")
	}

	// Wait to ensure mtime changes (some filesystems have 1-second granularity)
	time.Sleep(1100 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(tmpBinary, []byte("v2"), 0755); err != nil {
		t.Fatalf("Failed to modify test binary: %v", err)
	}

	// Test 2: File modified - should return restart step
	s = w.checkFileModification()
	if !s.stopBinary {
		t.Error("Expected stopBinary when file modified")
	}
	if !s.startBinary {
		t.Error("Expected startBinary when file modified")
	}
	if s.action == nil {
		t.Error("Expected fn to be set when file modified")
	}
	if s.exitAfter {
		t.Error("Did not expect exitAfter when file modified")
	}
}

func TestGetMtime(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-file")

	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	w := &Makedog{binaryPath: tmpFile}

	// Get mtime
	mtime1, err := w.getMtime()
	if err != nil {
		t.Fatalf("Failed to get mtime: %v", err)
	}

	if mtime1 == 0 {
		t.Error("Expected non-zero mtime")
	}

	// Wait and modify (some filesystems have 1-second granularity)
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(tmpFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Get new mtime
	mtime2, err := w.getMtime()
	if err != nil {
		t.Fatalf("Failed to get second mtime: %v", err)
	}

	if mtime2 <= mtime1 {
		t.Errorf("Expected mtime to increase after modification: %d <= %d", mtime2, mtime1)
	}
}

func TestGetMtimeNonexistent(t *testing.T) {
	w := &Makedog{binaryPath: "/nonexistent/file"}

	_, err := w.getMtime()
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

// Integration test: Start and stop a real process
func TestStartStopBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a simple test script that runs indefinitely
	tmpDir := t.TempDir()
	tmpScript := filepath.Join(tmpDir, "test-script.sh")
	scriptContent := `#!/bin/bash
while true; do
  echo "running"
  sleep 0.1
done
`
	if err := os.WriteFile(tmpScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}

	w := &Makedog{binaryPath: tmpScript}

	// Start the binary
	if err := w.startBinary(); err != nil {
		t.Fatalf("Failed to start binary: %v", err)
	}

	// Verify process is running
	if w.run == nil || w.run.cmd.Process == nil {
		t.Fatal("Expected process to be running")
	}

	pid := w.run.cmd.Process.Pid

	// Verify the process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	// Stop the binary
	w.stopBinary()

	// Wait for process to exit
	time.Sleep(100 * time.Millisecond)

	// Verify process is stopped (sending signal 0 checks if process exists)
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		t.Error("Expected process to be stopped")
	}
}

// Mock implementation of processState for testing
type mockProcessState struct {
	exitCode int
	signaled bool
	signal   syscall.Signal
	maxrss   int64
	utime    int64 // nanoseconds
	stime    int64 // nanoseconds
}

func (m *mockProcessState) ExitCode() int {
	return m.exitCode
}

func (m *mockProcessState) Sys() interface{} {
	return mockWaitStatus{
		signaled: m.signaled,
		signal:   m.signal,
	}
}

func (m *mockProcessState) SysUsage() interface{} {
	// Convert nanoseconds to Timeval (sec + usec)
	utimeSec := m.utime / 1_000_000_000
	utimeUsec := (m.utime % 1_000_000_000) / 1_000
	stimeSec := m.stime / 1_000_000_000
	stimeUsec := (m.stime % 1_000_000_000) / 1_000

	return &syscall.Rusage{
		Maxrss: m.maxrss,
		Utime:  syscall.Timeval{Sec: utimeSec, Usec: int32(utimeUsec)},
		Stime:  syscall.Timeval{Sec: stimeSec, Usec: int32(stimeUsec)},
	}
}

type mockWaitStatus struct {
	signaled bool
	signal   syscall.Signal
}

func (w mockWaitStatus) Signaled() bool {
	return w.signaled
}

func (w mockWaitStatus) Signal() syscall.Signal {
	return w.signal
}

func TestHandleProcessExitMessage(t *testing.T) {
	// Save original stdout and restore after test
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()

	baseTime := time.Now()
	wallTime := 3 * time.Second

	tests := []struct {
		name             string
		state            *mockProcessState
		startTime        time.Time
		makedogInitiated bool
		expectedInOutput string
	}{
		{
			name: "external exit code 0",
			state: &mockProcessState{
				exitCode: 0,
				signaled: false,
				maxrss:   54 * 1024 * 1024,
				utime:    1_500_000_000,
				stime:    1_000_000_000,
			},
			startTime:        baseTime.Add(-wallTime),
			makedogInitiated: false,
			expectedInOutput: "stop  135 /test/binary [exit code 0, 54MB memory, 2s cpu time, 3s wall time]",
		},
		{
			name: "external exit code 1",
			state: &mockProcessState{
				exitCode: 1,
				signaled: false,
				maxrss:   128 * 1024 * 1024,
				utime:    500_000_000,
				stime:    500_000_000,
			},
			startTime:        baseTime.Add(-2 * time.Second),
			makedogInitiated: false,
			expectedInOutput: "stop  135 /test/binary [exit code 1, 128MB memory, 1s cpu time, 2s wall time]",
		},
		{
			name: "killed by SIGTERM",
			state: &mockProcessState{
				exitCode: 143,
				signaled: true,
				signal:   syscall.SIGTERM,
				maxrss:   256 * 1024 * 1024,
				utime:    2_000_000_000,
				stime:    1_000_000_000,
			},
			startTime:        baseTime.Add(-5 * time.Second),
			makedogInitiated: false,
			expectedInOutput: "stop  135 /test/binary [killed by TERM, 256MB memory, 3s cpu time, 5s wall time]",
		},
		{
			name: "killed by SIGKILL",
			state: &mockProcessState{
				exitCode: 137,
				signaled: true,
				signal:   syscall.SIGKILL,
				maxrss:   512 * 1024 * 1024,
				utime:    1_000_000_000,
				stime:    1_000_000_000,
			},
			startTime:        baseTime.Add(-10 * time.Second),
			makedogInitiated: false,
			expectedInOutput: "stop  135 /test/binary [killed by KILL, 512MB memory, 2s cpu time, 10s wall time]",
		},
		{
			name: "makedog-initiated stop (no exit desc)",
			state: &mockProcessState{
				exitCode: 0,
				signaled: false,
				maxrss:   64 * 1024 * 1024,
				utime:    500_000_000,
				stime:    500_000_000,
			},
			startTime:        baseTime.Add(-1 * time.Second),
			makedogInitiated: true,
			expectedInOutput: "stop  135 /test/binary [64MB memory, 1s cpu time, 1s wall time]",
		},
		{
			name: "long running with complex times",
			state: &mockProcessState{
				exitCode: 0,
				signaled: false,
				maxrss:   1024 * 1024 * 1024,
				utime:    5*60*1_000_000_000 + 30*1_000_000_000, // 5m30s
				stime:    2*60*1_000_000_000 + 15*1_000_000_000, // 2m15s
			},
			startTime:        baseTime.Add(-15 * time.Minute),
			makedogInitiated: false,
			expectedInOutput: "stop  135 /test/binary [exit code 0, 1.0GB memory, 7m:45s cpu time, 15m wall time]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a pipe to capture stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stdout = w

			// Call _printExitDetails (internal testable function)
			_printExitDetails(tt.state, 135, tt.startTime, time.Time{}, "/test/binary", tt.makedogInitiated, "")

			// Close write end and read captured output
			w.Close()
			var buf [4096]byte
			n, _ := r.Read(buf[:])
			output := string(buf[:n])
			r.Close()

			// Restore stdout for test output
			os.Stdout = oldStdout

			// Check that expected message appears in output
			if !strings.Contains(output, tt.expectedInOutput) {
				t.Errorf("Expected output to contain %q, got:\n%s", tt.expectedInOutput, output)
			}
		})
	}
}

// Test printExitDetails output formatting and makedogInitiated behavior
func TestHandleProcessExit(t *testing.T) {
	// Save original stdout and restore after test
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()

	tests := []struct {
		name              string
		cmdArgs           []string
		killWithSignal    bool
		makedogInitiated  bool
		expectExitCode    bool
		expectSignal      bool
		expectedCodeOrSig string
	}{
		{
			name:              "external exit code 0",
			cmdArgs:           []string{"sh", "-c", "exit 0"},
			makedogInitiated:  false,
			expectExitCode:    true,
			expectedCodeOrSig: "exit code 0",
		},
		{
			name:              "external exit code 1",
			cmdArgs:           []string{"sh", "-c", "exit 1"},
			makedogInitiated:  false,
			expectExitCode:    true,
			expectedCodeOrSig: "exit code 1",
		},
		{
			name:             "makedog-initiated stop",
			cmdArgs:          []string{"echo", "test"},
			makedogInitiated: true,
			expectExitCode:   false,
			expectSignal:     false,
		},
		{
			name:              "external signal termination",
			cmdArgs:           []string{"sh", "-c", "sleep 10"},
			killWithSignal:    true,
			makedogInitiated:  false,
			expectSignal:      true,
			expectedCodeOrSig: "killed by TERM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a pipe to capture stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stdout = w

			// Create and run command
			cmd := exec.Command(tt.cmdArgs[0], tt.cmdArgs[1:]...)

			if tt.killWithSignal {
				// Start the command and kill it
				if err := cmd.Start(); err != nil {
					t.Fatalf("Failed to start command: %v", err)
				}
				time.Sleep(50 * time.Millisecond)
				cmd.Process.Signal(syscall.SIGTERM)
				cmd.Wait()
			} else {
				// Just run to completion
				cmd.Run() // Ignore error, we're testing different exit codes
			}

			// Create makedog instance
			makedog := &Makedog{
				binaryPath: tt.cmdArgs[0],
				run: &run{
					cmd:       cmd,
					startTime: time.Now().Add(-2 * time.Second), // Simulate 2s runtime
				},
			}

			// Call the method (which calls _printExitDetails internally)
			makedog.printExitDetails(tt.makedogInitiated)

			// Close write end and read captured output
			w.Close()
			var buf [4096]byte
			n, _ := r.Read(buf[:])
			output := string(buf[:n])
			r.Close()

			// Restore stdout for test output
			os.Stdout = oldStdout

			// Verify output contains expected elements
			if !strings.Contains(output, "stop  ") {
				t.Errorf("Expected output to contain 'stop  ', got: %s", output)
			}

			if !strings.Contains(output, "memory") {
				t.Errorf("Expected output to contain 'memory', got: %s", output)
			}

			if !strings.Contains(output, "cpu time") {
				t.Errorf("Expected output to contain 'cpu time', got: %s", output)
			}

			if !strings.Contains(output, "wall time") {
				t.Errorf("Expected output to contain 'wall time', got: %s", output)
			}

			// Verify exit code/signal behavior
			if tt.expectExitCode && !strings.Contains(output, tt.expectedCodeOrSig) {
				t.Errorf("Expected output to contain '%s', got: %s", tt.expectedCodeOrSig, output)
			}

			if tt.expectSignal && !strings.Contains(output, tt.expectedCodeOrSig) {
				t.Errorf("Expected output to contain '%s', got: %s", tt.expectedCodeOrSig, output)
			}

			if tt.makedogInitiated {
				if strings.Contains(output, "exit code") || strings.Contains(output, "killed by") {
					t.Errorf("makedog-initiated stop should not show exit code/signal, got: %s", output)
				}
			}
		})
	}
}

func TestCheckForSpin(t *testing.T) {
	t.Run("less than 3 restarts", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now()},
		}

		// First restart
		if w.checkForSpin() {
			t.Error("Expected no spin with 1 restart")
		}

		// Second restart
		if w.checkForSpin() {
			t.Error("Expected no spin with 2 restarts")
		}
	})

	t.Run("3 rapid restarts within 5 seconds", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now()},
		}

		// Simulate 3 rapid restarts
		w.checkForSpin()
		w.checkForSpin()
		if !w.checkForSpin() {
			t.Error("Expected spin detected with 3 rapid restarts")
		}
	})

	t.Run("3 restarts spread over more than 5 seconds", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now()},
		}

		// First restart at T-6s
		w.restartTimes = []time.Time{time.Now().Add(-6 * time.Second)}

		// Second restart at T-3s
		w.restartTimes = append(w.restartTimes, time.Now().Add(-3*time.Second))

		// Third restart now
		if w.checkForSpin() {
			t.Error("Expected no spin when restarts spread over >5 seconds")
		}
	})

	t.Run("sliding window keeps only last 5", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now()},
		}

		// Add 7 restarts
		for i := 0; i < 7; i++ {
			w.checkForSpin()
		}

		if len(w.restartTimes) != 5 {
			t.Errorf("Expected restartTimes length = 5, got %d", len(w.restartTimes))
		}
	})

	t.Run("old restarts don't count", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now()},
		}

		// Add 2 old restarts (>5 seconds ago)
		w.restartTimes = []time.Time{
			time.Now().Add(-10 * time.Second),
			time.Now().Add(-8 * time.Second),
		}

		// Add 2 recent restarts
		w.checkForSpin()
		if w.checkForSpin() {
			t.Error("Expected no spin: only 2 restarts within 5 seconds (2 old ones shouldn't count)")
		}
	})

	t.Run("successful run clears spin tracking", func(t *testing.T) {
		w := &Makedog{
			binaryPath: "/bin/echo",
			run:        &run{startTime: time.Now().Add(-15 * time.Second)}, // ran for 15 seconds
		}

		// Add some restart times
		w.restartTimes = []time.Time{time.Now(), time.Now(), time.Now()}

		// Check for spin - should clear tracking since process ran >= 10 seconds
		if w.checkForSpin() {
			t.Error("Expected no spin when process ran >= 10 seconds")
		}

		if w.restartTimes != nil {
			t.Error("Expected spin tracking to be cleared after successful run")
		}
	})
}

func TestClearSpinTracking(t *testing.T) {
	w := &Makedog{binaryPath: "/bin/echo"}

	// Add some restart times
	w.restartTimes = []time.Time{time.Now(), time.Now(), time.Now()}

	// Clear tracking
	w.clearSpinTracking()

	if w.restartTimes != nil {
		t.Error("Expected restartTimes to be nil after clearing")
	}
}

// Integration harness: run a real makedog under a pty against a shell-script
// child, observing its terminal output.

var (
	makedogBinOnce sync.Once
	makedogBinPath string
	makedogBinErr  error
	drainPaused    sync.Map // *exec.Cmd -> *atomic.Bool
)

// pauseDrain stalls or resumes a session's pty reader, simulating a terminal
// that has stopped consuming output.
func pauseDrain(t *testing.T, cmd *exec.Cmd, paused bool) {
	t.Helper()
	flag, _ := drainPaused.LoadOrStore(cmd, &atomic.Bool{})
	flag.(*atomic.Bool).Store(paused)
}

// makedogBinary builds the makedog binary once per test run and returns its path.
func makedogBinary(t *testing.T) string {
	t.Helper()
	makedogBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "makedog-test")
		if err != nil {
			makedogBinErr = err
			return
		}
		makedogBinPath = filepath.Join(dir, "makedog")
		if out, err := exec.Command("go", "build", "-o", makedogBinPath, ".").CombinedOutput(); err != nil {
			makedogBinErr = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if makedogBinErr != nil {
		t.Fatalf("building makedog: %v", makedogBinErr)
	}
	return makedogBinPath
}

// makedogSession starts makedog under a pty running the given child script.
// Returns the makedog command, a snapshot func for the ANSI-stripped output so
// far, and the child pid parsed from the start message.
func makedogSession(t *testing.T, script string) (*exec.Cmd, func() string, int) {
	t.Helper()

	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	if err := os.WriteFile(child, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(makedogBinary(t), child)
	cmd.Dir = dir
	// Isolate the run store: tests must not write into the user's real one.
	cmd.Env = append(os.Environ(), "MAKEDOG_STATE_DIR="+filepath.Join(dir, "state"))
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("starting makedog: %v", err)
	}
	t.Cleanup(func() { ptmx.Close() })

	// Drain the pty continuously, accumulating output for snapshots. Styling
	// escapes are stripped so tests can match plain text. Tests may stall the
	// drain (pauseDrain) to simulate a wedged terminal.
	ansiRe := regexp.MustCompile("\x1b\\[[0-9;]*m")
	var mu sync.Mutex
	var output []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			if p, ok := drainPaused.Load(cmd); ok && p.(*atomic.Bool).Load() {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			n, err := ptmx.Read(buf)
			mu.Lock()
			output = append(output, buf[:n]...)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return ansiRe.ReplaceAllString(string(output), "")
	}

	// Wait for the start message and extract the child pid from it.
	pidRe := regexp.MustCompile(`pid (\d+)`)
	var childPid int
	waitForOutput(t, cmd, snapshot, "start message", func(s string) bool {
		m := pidRe.FindStringSubmatch(s)
		if m == nil {
			return false
		}
		childPid, _ = strconv.Atoi(m[1])
		return true
	})

	return cmd, snapshot, childPid
}

// waitForOutput polls the session output until cond matches, failing the test
// (and killing makedog) after a timeout.
func waitForOutput(t *testing.T, cmd *exec.Cmd, snapshot func() string, desc string, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond(snapshot()) {
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatalf("never saw %s; output:\n%s", desc, snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForExit requires makedog to exit cleanly within a timeout.
func waitForExit(t *testing.T, cmd *exec.Cmd, childPid int) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("makedog exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		syscall.Kill(childPid, syscall.SIGKILL)
		t.Fatal("makedog did not exit within 5s")
	}
}

// TestExternalSignalStopsChildAndExits sends makedog an external SIGTERM while
// its child is producing output. Makedog must stop the child and exit promptly.
// Guards the signal-through-monitor-loop path: a handler that exits from a side
// goroutine instead deadlocks on outputWg.Wait (the still-running child keeps
// the pty open) and orphans the child.
func TestExternalSignalStopsChildAndExits(t *testing.T) {
	cmd, _, childPid := makedogSession(t, "#!/bin/sh\nwhile :; do echo tick; sleep 0.1; done\n")

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signaling makedog: %v", err)
	}
	waitForExit(t, cmd, childPid)

	// Signal 0 probes existence: the child must be gone once makedog has exited.
	if err := syscall.Kill(childPid, 0); err == nil {
		syscall.Kill(childPid, syscall.SIGKILL)
		t.Errorf("child (pid %d) still running after makedog exit", childPid)
	}
}

// TestLongLineDoesNotStopCapture verifies an oversized output line doesn't kill
// capture. A Scanner-based reader hits its 64KB token limit on the long line and
// silently stops relaying output — the ticks that follow never appear.
func TestLongLineDoesNotStopCapture(t *testing.T) {
	script := "#!/bin/sh\n" +
		"head -c 80000 /dev/zero | tr '\\0' 'x'; echo\n" +
		"while :; do echo tick; sleep 0.1; done\n"
	cmd, snapshot, childPid := makedogSession(t, script)

	waitForOutput(t, cmd, snapshot, "ticks after the long line", func(s string) bool {
		return strings.Contains(s, strings.Repeat("x", 1000)) && strings.Contains(s, "tick")
	})

	cmd.Process.Signal(syscall.SIGTERM)
	waitForExit(t, cmd, childPid)
}

// TestRunLogRecorded verifies a run is persisted end to end: a store lineage
// appears under MAKEDOG_STATE_DIR, opened by a meta header carrying the child
// pid, holding the child's output, and sealed by an exit trailer.
func TestRunLogRecorded(t *testing.T) {
	cmd, snapshot, childPid := makedogSession(t, "#!/bin/sh\necho hello from child\nwhile :; do sleep 0.1; done\n")

	waitForOutput(t, cmd, snapshot, "child output", func(s string) bool {
		return strings.Contains(s, "hello from child")
	})

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signaling makedog: %v", err)
	}
	waitForExit(t, cmd, childPid)

	logs, err := filepath.Glob(filepath.Join(cmd.Dir, "state", "*", "*", "runs", "000001.jsonl"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("expected one run log, got %v (err %v)", logs, err)
	}

	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected meta, output, and exit records, got:\n%s", data)
	}

	var header, trailer record
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("parsing header: %v", err)
	}
	if header.T != recMeta || header.Run != 1 || header.Pid != childPid {
		t.Errorf("header = %+v; want meta record for run 1, pid %d", header, childPid)
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &trailer); err != nil {
		t.Fatalf("parsing trailer: %v", err)
	}
	if trailer.T != recExit || trailer.Reason == "" {
		t.Errorf("trailer = %+v; want exit record with a reason", trailer)
	}
	if !strings.Contains(string(data), "hello from child") {
		t.Errorf("child output missing from run log:\n%s", data)
	}
}

// TestGracefulShutdownOutputCaptured verifies output the child writes while
// handling SIGTERM is relayed rather than lost. Closing the pty at signal time
// (instead of after exit) discards these final lines.
func TestGracefulShutdownOutputCaptured(t *testing.T) {
	// The bye is delayed so it lands well after the signal: closing the pty at
	// signal time then loses it deterministically rather than racily.
	script := "#!/bin/sh\n" +
		"trap 'sleep 0.3; echo graceful bye; exit 0' TERM\n" +
		"while :; do echo tick; sleep 0.1; done\n"
	cmd, snapshot, childPid := makedogSession(t, script)

	// A tick proves the child's TERM trap is installed before we signal.
	waitForOutput(t, cmd, snapshot, "first tick", func(s string) bool {
		return strings.Contains(s, "tick")
	})

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signaling makedog: %v", err)
	}
	waitForExit(t, cmd, childPid)

	if !strings.Contains(snapshot(), "graceful bye") {
		t.Errorf("graceful shutdown output lost; output:\n%s", snapshot())
	}
}

// Test suite for core makedog functionality.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
		{"unknown key", 'x', false, false, false, false},
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
			if (s.fn != nil) != tt.expectFnNotNil {
				t.Errorf("fn != nil = %v; want %v", s.fn != nil, tt.expectFnNotNil)
			}
		})
	}
}

func TestHandleKeypressRestart(t *testing.T) {
	t.Run("restart with running process", func(t *testing.T) {
		cmd := exec.Command("sleep", "10")
		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start test process: %v", err)
		}
		defer cmd.Process.Kill()

		// Verify ProcessState is nil (process still running, not Wait()ed)
		if cmd.ProcessState != nil {
			t.Fatal("ProcessState should be nil for running process")
		}

		w := &Makedog{
			binaryPath:   "/bin/echo",
			cmd:          cmd,
			restartTimes: []time.Time{time.Now()},
		}

		s := w.handleKeypress('r')

		if !s.stopBinary {
			t.Error("Expected stopBinary = true when process is running")
		}
		if !s.startBinary {
			t.Error("Expected startBinary = true")
		}
		if s.exitAfter {
			t.Error("Expected exitAfter = false")
		}
		if len(w.restartTimes) != 0 {
			t.Error("Expected spin tracking to be cleared")
		}
	})

	t.Run("restart with no running process", func(t *testing.T) {
		w := &Makedog{
			binaryPath:   "/bin/echo",
			cmd:          nil,
			restartTimes: []time.Time{time.Now()},
		}

		s := w.handleKeypress('r')

		if s.stopBinary {
			t.Error("Expected stopBinary = false when process is not running")
		}
		if !s.startBinary {
			t.Error("Expected startBinary = true")
		}
		if s.exitAfter {
			t.Error("Expected exitAfter = false")
		}
		if len(w.restartTimes) != 0 {
			t.Error("Expected spin tracking to be cleared")
		}
	})

	t.Run("restart with exited process", func(t *testing.T) {
		cmd := exec.Command("echo", "test")
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to run test process: %v", err)
		}

		// Verify ProcessState is not nil (process has exited and been Wait()ed)
		if cmd.ProcessState == nil {
			t.Fatal("ProcessState should not be nil for exited process")
		}

		w := &Makedog{
			binaryPath:   "/bin/echo",
			cmd:          cmd,
			restartTimes: []time.Time{time.Now()},
		}

		s := w.handleKeypress('r')

		if s.stopBinary {
			t.Error("Expected stopBinary = false when process has already exited")
		}
		if !s.startBinary {
			t.Error("Expected startBinary = true")
		}
		if s.exitAfter {
			t.Error("Expected exitAfter = false")
		}
		if len(w.restartTimes) != 0 {
			t.Error("Expected spin tracking to be cleared")
		}
	})
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
	if s.stopBinary || s.startBinary || s.exitAfter || s.fn != nil {
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
	if s.fn == nil {
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
	if w.cmd == nil || w.cmd.Process == nil {
		t.Fatal("Expected process to be running")
	}

	pid := w.cmd.Process.Pid

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
			expectedInOutput: "stop run 135 [exit code 0, 54MB memory, 2s cpu time, 3s wall time]",
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
			expectedInOutput: "stop run 135 [exit code 1, 128MB memory, 1s cpu time, 2s wall time]",
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
			expectedInOutput: "stop run 135 [killed by TERM, 256MB memory, 3s cpu time, 5s wall time]",
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
			expectedInOutput: "stop run 135 [killed by KILL, 512MB memory, 2s cpu time, 10s wall time]",
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
			expectedInOutput: "stop run 135 [64MB memory, 1s cpu time, 1s wall time]",
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
			expectedInOutput: "stop run 135 [exit code 0, 1.0GB memory, 7m:45s cpu time, 15m wall time]",
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
			_printExitDetails(tt.state, tt.startTime, tt.makedogInitiated)

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
				cmd:        cmd,
				startTime:  time.Now().Add(-2 * time.Second), // Simulate 2s runtime
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
			if !strings.Contains(output, "stop run") {
				t.Errorf("Expected output to contain 'stop run', got: %s", output)
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
			startTime:  time.Now(),
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
			startTime:  time.Now(),
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
			startTime:  time.Now(),
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
			startTime:  time.Now(),
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
			startTime:  time.Now(),
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
			startTime:  time.Now().Add(-15 * time.Second), // Process ran for 15 seconds
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

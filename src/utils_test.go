// Test suite for utility functions.
package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestFormatMemory(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1KB"},
		{1536, "1KB"},
		{2048, "2KB"},
		{1024 * 1024, "1MB"},
		{54 * 1024 * 1024, "54MB"},
		{1024 * 1024 * 1024, "1.0GB"},
		{1536 * 1024 * 1024, "1.5GB"},
		{5 * 1024 * 1024 * 1024, "5.0GB"},
	}

	for _, tt := range tests {
		result := formatMemory(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatMemory(%d) = %s; want %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	const (
		Second = 1_000_000_000
		Minute = 60 * Second
		Hour   = 60 * Minute
		Day    = 24 * Hour
	)

	tests := []struct {
		ns       int64
		expected string
	}{
		{0, "0s"},
		{1 * Second, "1s"},
		{30 * Second, "30s"},
		{1 * Minute, "1m"},
		{1*Minute + 30*Second, "1m:30s"},
		{1 * Hour, "1h"},
		{2*Hour + 5*Minute + 3*Second, "2h:5m:3s"},
		{1 * Day, "1d"},
		{2*Day + 5*Hour + 3*Minute + 2*Second, "2d:5h:3m:2s"},
		{999_999_999, "0s"}, // Just under 1 second
	}

	for _, tt := range tests {
		result := formatDuration(tt.ns)
		if result != tt.expected {
			t.Errorf("formatDuration(%d) = %s; want %s", tt.ns, result, tt.expected)
		}
	}
}

func TestSignalName(t *testing.T) {
	tests := []struct {
		signal   syscall.Signal
		expected string
	}{
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGINT, "SIGINT"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.SIGQUIT, "SIGQUIT"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGPIPE, "SIGPIPE"},
		{syscall.SIGUSR1, "SIGUSR1"},
		{syscall.SIGUSR2, "SIGUSR2"},
		{syscall.Signal(999), "signal 999"}, // Unknown signal
	}

	for _, tt := range tests {
		result := signalName(tt.signal)
		if result != tt.expected {
			t.Errorf("signalName(%v) = %s; want %s", tt.signal, result, tt.expected)
		}
	}
}

func TestRunCommand(t *testing.T) {
	// Save original stdout and restore after test
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()

	tests := []struct {
		name           string
		command        string
		args           []string
		expectError    bool
		expectedOutput []string
		notExpected    []string
	}{
		{
			name:    "successful command with output",
			command: "echo",
			args:    []string{"hello", "world"},
			expectedOutput: []string{
				"echo hello world",
				"hello world",
			},
		},
		{
			name:    "successful command no output",
			command: "true",
			args:    []string{},
			expectedOutput: []string{
				"true",
				">> no output <<",
			},
		},
		{
			name:        "failing command",
			command:     "false",
			args:        []string{},
			expectError: true,
			expectedOutput: []string{
				"false",
				">> no output <<",
				"failed",
			},
		},
		{
			name:    "command with multiple output lines",
			command: "sh",
			args:    []string{"-c", "echo line1; echo line2; echo line3"},
			expectedOutput: []string{
				"sh -c",
				"line1",
				"line2",
				"line3",
			},
		},
		{
			name:        "nonexistent command",
			command:     "nonexistent-command-12345",
			args:        []string{},
			expectError: true,
			expectedOutput: []string{
				"nonexistent-command-12345",
				"failed to start command",
			},
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

			// Run the command
			cmdErr := runCommand(tt.command, tt.args...)

			// Close write end and read captured output
			w.Close()
			var buf [4096]byte
			n, _ := r.Read(buf[:])
			output := string(buf[:n])
			r.Close()

			// Restore stdout for test output
			os.Stdout = oldStdout

			// Check error expectation
			if tt.expectError && cmdErr == nil {
				t.Errorf("Expected error but got nil")
			}
			if !tt.expectError && cmdErr != nil {
				t.Errorf("Expected no error but got: %v", cmdErr)
			}

			// Check expected output
			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("Expected output to contain %q, got:\n%s", expected, output)
				}
			}

			// Check not expected output
			for _, notExpected := range tt.notExpected {
				if strings.Contains(output, notExpected) {
					t.Errorf("Expected output to NOT contain %q, got:\n%s", notExpected, output)
				}
			}
		})
	}
}

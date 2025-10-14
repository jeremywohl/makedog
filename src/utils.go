// Utilities
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

var oldTermState *term.State

// Set stdin to raw mode.
func setRawTerm() error {
	var err error
	oldTermState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	return nil
}

// restoreTerm restores the terminal to its original state.
func restoreTerm() {
	if oldTermState != nil {
		term.Restore(int(os.Stdin.Fd()), oldTermState)
	}
}

// banner prints text centered with a fill character.
func banner(text string, fill rune, tight bool) {
	cols := 80
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cols = width
	}

	padded := text
	if !tight {
		padded = " " + text + " "
	}

	padding := cols - len(padded)
	if padding < 0 {
		padding = 0
	}

	leftPad := padding / 2
	rightPad := padding - leftPad

	printf("%s%s%s\n", strings.Repeat(string(fill), leftPad), padded, strings.Repeat(string(fill), rightPad))
}

func herald(format string, args ...interface{}) {
	printf("--> \033[1m"+format+"\033[0m\n", args...)
}

func line() {
	banner("", '-', true)
}

// printf prints formatted output with proper line endings for raw terminal mode.
// In raw mode, the terminal doesn't automatically convert \n to \r\n, so we must
// use \r\n explicitly for carriage return + line feed.
func printf(format string, args ...interface{}) {
	format = strings.ReplaceAll(format, "\n", "\r\n")
	output := fmt.Sprintf(format, args...)
	fmt.Print(output)
}

// println prints a blank line for raw terminal mode.
func println() {
	fmt.Print("\r\n")
}

// clearScreen wipes the terminal and moves the cursor to the home position.
func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

// formatMemory converts bytes to a human-readable memory string.
// Returns format like "54MB", "1.2GB", "128KB".
func formatMemory(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%dMB", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%dKB", bytes/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// formatDuration converts nanoseconds to a human-readable duration string.
// Returns format like "2d:5h:3m:2s", showing only necessary segments.
func formatDuration(ns int64) string {
	const (
		Second = 1_000_000_000
		Minute = 60 * Second
		Hour   = 60 * Minute
		Day    = 24 * Hour
	)

	if ns == 0 {
		return "0s"
	}

	var parts []string

	days := ns / Day
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
		ns %= Day
	}

	hours := ns / Hour
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		ns %= Hour
	}

	minutes := ns / Minute
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
		ns %= Minute
	}

	seconds := ns / Second
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	return strings.Join(parts, ":")
}

// runCommand runs a command, with display similar to our subprocess.
func runCommand(name string, args ...string) error {
	herald("%s %s", name, strings.Join(args, " "))
	line()

	cmd := exec.Command(name, args...)

	// Get pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		herald("failed to create stdout pipe: %v", err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		herald("failed to create stderr pipe: %v", err)
		return err
	}

	// Start command
	if err := cmd.Start(); err != nil {
		herald("failed to start command: %v", err)
		return err
	}

	// Print output
	merged := io.MultiReader(stdout, stderr)
	var hadOutput bool

	scanner := bufio.NewScanner(merged)
	for scanner.Scan() {
		printf("%s\n", scanner.Text())
		hadOutput = true
	}

	if !hadOutput {
		printf(">> no output <<\n")
	}

	// Wait for command to complete
	err = cmd.Wait()

	// Print footer
	line()

	if err != nil {
		herald(fmt.Sprintf("%s failed: %v", name, err))
	}

	return err
}

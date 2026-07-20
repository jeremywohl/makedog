// Utilities
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
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

// getTermSize returns the terminal width and height.
func getTermSize() (width, height int, err error) {
	return term.GetSize(int(os.Stdout.Fd()))
}

// flagline prints text centered with a fill character.
func flagline(text string, fill rune, tight bool) {
	cols := 80
	if width, _, err := getTermSize(); err == nil {
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

func line() {
	flagline("", '-', true)
}

// Watch-mode terminal writes flow through a dedicated goroutine: a stalled
// terminal (Ctrl-S flow control, an undrained pty) must never wedge the
// monitor loop, whose liveness restart-on-change, keypresses, and control
// replies all depend on. The deep queue absorbs transient stalls; a sustained
// one eventually applies backpressure rather than growing without bound.
// Until startTermWriter runs (read verbs, tests), writes are direct.
var (
	termWrites chan string
	termFlush  chan chan struct{}
)

// startTermWriter begins asynchronous terminal output for watch mode.
func startTermWriter() {
	termWrites = make(chan string, 4_096)
	termFlush = make(chan chan struct{})

	go func() {
		for {
			select {
			case s := <-termWrites:
				os.Stdout.WriteString(s)
			case ack := <-termFlush:
				for {
					select {
					case s := <-termWrites:
						os.Stdout.WriteString(s)
						continue
					default:
					}
					break
				}
				close(ack)
			}
		}
	}()
}

// flushTerm waits briefly for queued output to reach the terminal, so exits
// don't drop tail output; the bound keeps a stalled terminal from wedging
// the exit itself.
func flushTerm() {
	if termFlush == nil {
		return
	}
	ack := make(chan struct{})
	select {
	case termFlush <- ack:
		select {
		case <-ack:
		case <-time.After(2 * time.Second):
		}
	case <-time.After(2 * time.Second):
	}
}

// printf prints formatted output with proper line endings for raw terminal mode.
// In raw mode, the terminal doesn't automatically convert \n to \r\n, so we must
// use \r\n explicitly for carriage return + line feed.
func printf(format string, args ...interface{}) {
	format = strings.ReplaceAll(format, "\n", "\r\n")
	output := fmt.Sprintf(format, args...)
	if termWrites != nil {
		termWrites <- output
		return
	}
	fmt.Print(output)
}

// println prints a blank line for raw terminal mode.
func println() {
	if termWrites != nil {
		termWrites <- "\r\n"
		return
	}
	fmt.Print("\r\n")
}

// clearScreen wipes the terminal and moves the cursor to the home position.
func clearScreen() {
	fmt.Print(ansi.EraseEntireDisplay + ansi.CursorHomePosition + ansi.EraseEntireScreen)
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
	text := name
	if len(args) != 0 {
		text += " " + strings.Join(args, " ")
	}
	out.Command("%s", text)
	line()

	cmd := exec.Command(name, args...)

	// Get pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out.Error("failed to create stdout pipe: %v", err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		out.Error("failed to create stderr pipe: %v", err)
		return err
	}

	// Start command
	if err := cmd.Start(); err != nil {
		out.Error("failed to start command: %v", err)
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
		out.Error("%s failed: %v", name, err)
	}

	return err
}

// getBinaryHash computes the SHA256 hash of a binary file.
func getBinaryHash(binaryPath string) (string, error) {
	file, err := os.Open(binaryPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	return hash, nil
}

// getGitInfo retrieves both the current git branch name and full commit hash in a single call.
// Returns (branch, commit, error).
func getGitInfo() (string, string, error) {
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short) %(objectname)", "--points-at", "HEAD", "refs/heads")
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) != 2 {
		return "", "", nil
	}

	return parts[0], parts[1], nil
}

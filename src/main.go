package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: makedog <binary-path>\n")
		os.Exit(1)
	}

	// validate binary
	binaryPath := os.Args[1]
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "makedog: binary '%s' not found", binaryPath)
		os.Exit(1)
	}

	// set raw terminal input
	if err := setRawTerm(); err != nil {
		fmt.Fprintf(os.Stderr, "makedog: error setting raw mode: %v\n", err)
		os.Exit(1)
	}

	// run loop
	makedog := NewMakedog(binaryPath)
	if err := makedog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "makedog: error: %v\n", err)
		makedog.exitCleanly(1)
	}
}

// Makedog manages the lifecycle of watching and restarting a binary.
type Makedog struct {
	binaryPath string
	process    *os.Process
	lastMtime  int64
}

// NewMakedog creates a new Makedog instance for the given binary path.
func NewMakedog(binaryPath string) (*Makedog) {
	w := &Makedog{
		binaryPath: binaryPath,
	}

	w.setupSignalHandlers()

	return w
}

// Run starts the main watch and restart loop.
func (w *Makedog) Run() error {
	printKeypressInstructions()

	if err := w.startBinary(); err != nil {
		return err
	}

	var err error
	w.lastMtime, err = w.getMtime()
	if err != nil {
		return err
	}

	return w.monitor()
}

// exitCleanly performs cleanup and exits the program.
func (w *Makedog) exitCleanly(status int) {
	w.stopBinary()
	restoreTerm()
	os.Exit(status)
}

// setupSignalHandlers configures handlers for INT and TERM signals.
func (w *Makedog) setupSignalHandlers() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		w.exitCleanly(0)
	}()
}

// startBinary starts the monitored binary.
func (w *Makedog) startBinary() error {
	banner("start", '-', false)

	cmd := exec.Command(w.binaryPath)

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return err
	}

	// Process output with timestamps
	go processOutput(stdout, " | ")
	go processOutput(stderr, " ! ")

	w.process = cmd.Process
	return nil
}

// processOutput reads from a pipe and prints each line with a timestamp and separator.
func processOutput(reader io.Reader, separator string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		timestamp := time.Now().Format("[2006-01-02 15:04:05.000]")
		printf("%s%s%s\n", timestamp, separator, scanner.Text())
	}
}

// stopBinary terminates the monitored binary.
func (w *Makedog) stopBinary() {
	if w.process == nil {
		return
	}

	banner("stop", '-', false)
	println()

	w.process.Signal(syscall.SIGTERM)
	w.process.Wait()

	w.process = nil
}

// getMtime retrieves the modification time of the binary.
func (w *Makedog) getMtime() (int64, error) {
	info, err := os.Stat(w.binaryPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().Unix(), nil
}

// monitor runs the main loop, for file changes and keypresses.
func (w *Makedog) monitor() error {
	// Create a channel for keypress events
	keyChan := make(chan byte, 1)

	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			keyChan <- buf[0]
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case key := <-keyChan:
			w.handleKeypress(key)
		case <-ticker.C:
			w.checkFileModification()
		}
	}
}

// handleKeypress processes keypress events.
func (w *Makedog) handleKeypress(key byte) {
	// Handle Ctrl-C (ASCII 3)
	if key == 3 {
		w.exitCleanly(0)
		return
	}

	if handler, ok := defaultKeys[key]; ok {
		handler.fn(w)
	}
}

// checkFileModification checks if the binary has been modified and restarts if needed.
func (w *Makedog) checkFileModification() {
	currentMtime, err := w.getMtime()
	if err != nil {
		return
	}

	if currentMtime != w.lastMtime {
		w.stopBinary()
		time.Sleep(500 * time.Millisecond)
		w.startBinary()
		w.lastMtime = currentMtime
	}
}

// keypressHandler holds a handler function and its description.
type keypressHandler struct {
	fn   func(*Makedog)
	desc string
}

// defaultKeys maps built-in keys to their handler functions and descriptions.
var defaultKeys map[byte]keypressHandler

func init() {
	defaultKeys = map[byte]keypressHandler{
		'h': {(*Makedog).keypressHelp, "print this help"},
		'm': {(*Makedog).keypressMake, "run make"},
		'q': {(*Makedog).keypressQuit, "quit"},
	}
}

// printKeypressInstructions prints available keypress handlers.
func printKeypressInstructions() {
	// Collect keys in sorted order
	keys := make([]byte, 0, len(defaultKeys))
	for k := range defaultKeys {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

	var descriptions []string
	for _, k := range keys {
		descriptions = append(descriptions, fmt.Sprintf("'%c' to %s", k, defaultKeys[k].desc))
	}

	printf("keys: %s\n", strings.Join(descriptions, ", "))
}

// keypressHelp prints the help message showing available keys.
func (w *Makedog) keypressHelp() {
	printKeypressInstructions()
}

// keypressQuit exits the program cleanly.
func (w *Makedog) keypressQuit() {
	w.exitCleanly(0)
}

// keypressMake runs the make command and restarts the binary if successful.
func (w *Makedog) keypressMake() {
	w.stopBinary()

	banner("make", '=', false)
	println()

	cmd := exec.Command("make")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		println()
		banner("make failed - press any key to continue", '!', false)
		buf := make([]byte, 1)
		os.Stdin.Read(buf)
		println()
	} else {
		println()
		w.startBinary()
		w.lastMtime, _ = w.getMtime()
	}
}

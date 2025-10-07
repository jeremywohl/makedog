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
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
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
	cmd        *exec.Cmd
	pty        *os.File
	lastMtime  int64
	childExit   chan error
	startTime  time.Time
	outputWg   sync.WaitGroup
}

// NewMakedog creates a new Makedog instance for the given binary path.
func NewMakedog(binaryPath string) *Makedog {
	w := &Makedog{
		binaryPath: binaryPath,
	}

	w.setupSignalHandlers()

	return w
}

// Run starts the main watch and restart loop.
func (w *Makedog) Run() error {
	printKeypressInstructions()
	println()

	if err := w.startBinary(); err != nil {
		return err
	}

	return w.monitor()
}

// exitCleanly performs cleanup and exits the program.
func (w *Makedog) exitCleanly(status int) {
	w.outputWg.Wait()
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
	var err error
	w.lastMtime, err = w.getMtime()
	if err != nil {
		return err
	}

	cmd := exec.Command(w.binaryPath)

	w.startTime = time.Now()

	// Start with pty for real-time output
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}

	printf("--> \033[1mstart %s (pid %d)\033[0m\n", w.binaryPath, cmd.Process.Pid)
	banner("", '-', true)

	// Process output (stdout and stderr merged)
	go w.processOutput(ptmx)

	w.cmd = cmd
	w.pty = ptmx
	w.childExit = make(chan error, 1)

	// Monitor process exit
	go func() {
		err := cmd.Wait()
		w.childExit <- err
	}()

	return nil
}

// processOutput reads from a pty and prints each line with a timestamp and separator.
// Uses buffered reading since pty provides line-buffered output from child process.
func (w *Makedog) processOutput(reader io.Reader) {
	w.outputWg.Add(1)
	defer w.outputWg.Done()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		timestamp := time.Now().Format("[2006-01-02 15:04:05.000]")
		printf("%s  %s\n", timestamp, scanner.Text())
	}
}

// stopBinary terminates the monitored binary.
func (w *Makedog) stopBinary() {
	if w.cmd == nil || w.cmd.Process == nil {
		return
	}

	w.cmd.Process.Signal(syscall.SIGTERM)

	// Close pty to signal EOF to processOutput goroutine
	if w.pty != nil {
		w.pty.Close()
		w.pty = nil
	}

	// TODO: need a process here for stubborn procs
	// TODO: avoid this sleep for handle to commence, let's make this an exact handoff
}

// getMtime retrieves the modification time of the binary.
func (w *Makedog) getMtime() (int64, error) {
	info, err := os.Stat(w.binaryPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().Unix(), nil
}

type Action struct {
	stopBinary  bool
	fn          func()
	startBinary bool
	exitAfter   bool
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
		var action Action

		select {
		case key := <-keyChan:
			action = w.handleKeypress(key)
		case <-ticker.C:
			action = w.checkFileModification()
		case <-w.childExit:
			w.handleProcessExit(false)
			action = Action{startBinary: true}
		}

		if action.stopBinary {
			w.stopBinary()
			<-w.childExit
			w.handleProcessExit(true)
		}

		if action.fn != nil {
			action.fn()
		}

		if action.exitAfter {
			w.exitCleanly(0)
		}

		if action.startBinary {
			w.startBinary()
		}
	}
}

// handleKeypress processes keypress events.
func (w *Makedog) handleKeypress(key byte) Action {
	// Handle Ctrl-C (ASCII 3)
	if key == 3 {
		return Action{stopBinary: true, exitAfter: true}
	}

	if handler, ok := defaultKeys[key]; ok {
		return Action{stopBinary: handler.stopProc, fn: func() { handler.fn(w) }, startBinary: handler.stopProc, exitAfter: handler.exitAfter}
	}

	return Action{}
}

// checkFileModification checks if the binary has been modified and restarts if needed.
func (w *Makedog) checkFileModification() Action {
	currentMtime, err := w.getMtime()
	if err != nil {
		return Action{}
	}

	if currentMtime != w.lastMtime {
		return Action{stopBinary: true, fn: func() { time.Sleep(500 * time.Millisecond) }, startBinary: true}
	}

	return Action{}
}

// handleProcessExit handles the child process exiting.
func (w *Makedog) handleProcessExit(makedogInitiated bool) {
	banner("", '-', true)

	exitCode := w.cmd.ProcessState.ExitCode()
	waitStatus := w.cmd.ProcessState.Sys().(syscall.WaitStatus)
	sysUsage := w.cmd.ProcessState.SysUsage().(*syscall.Rusage)

	// Calculate times
	cpuTime := sysUsage.Utime.Nano() + sysUsage.Stime.Nano()
	wallTime := time.Since(w.startTime).Nanoseconds()

	var exitDesc string
	if !makedogInitiated {
		if waitStatus.Signaled() {
			exitDesc = fmt.Sprintf("killed by %s, ", signalName(waitStatus.Signal()))
		} else {
			exitDesc = fmt.Sprintf("exit code %d, ", exitCode)
		}
	}

	runnum := 135
	stopstr := fmt.Sprintf(
		"stop run %d [%s%s memory, %s cpu time, %s wall time]",
		runnum,
		exitDesc,
		formatMemory(sysUsage.Maxrss),
		formatDuration(cpuTime),
		formatDuration(wallTime),
	)

	herald(stopstr)
	println()
}

// keypressHandler holds a handler function, its description, and whether the process is running during the keypress handler.
type keypressHandler struct {
	fn        func(*Makedog)
	desc      string
	stopProc  bool
	exitAfter bool
}

// defaultKeys maps built-in keys to their handler functions and descriptions.
var defaultKeys map[byte]keypressHandler

func init() {
	defaultKeys = map[byte]keypressHandler{
		'h': {(*Makedog).keypressHelp, "for this help", false, false},
		'm': {(*Makedog).keypressMake, "to run make", true, false},
		'q': {(*Makedog).keypressQuit, "to quit", true, true},
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
		descriptions = append(descriptions, fmt.Sprintf("'%c' %s", k, defaultKeys[k].desc))
	}

	printf("keys: %s\n", strings.Join(descriptions, ", "))
}

// keypressHelp prints the help message showing available keys.
func (w *Makedog) keypressHelp() {
	printKeypressInstructions()
}

// keypressQuit exits the program cleanly.
func (w *Makedog) keypressQuit() {
}

// keypressMake runs the make command and restarts the binary if successful.
func (w *Makedog) keypressMake() {
	runCommand("make")
	println()
}

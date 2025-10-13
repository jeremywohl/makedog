// Keypress handlers for interactive makedog controls.
package main

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
)

// keypressHandler holds a handler function and its description.
type keypressHandler struct {
	action          func(*Makedog) step
	description     string
	requiresProcess bool
}

// defaultKeys maps built-in keys to their handler functions and descriptions.
var defaultKeys map[byte]keypressHandler

func init() {
	defaultKeys = map[byte]keypressHandler{
		'c': {(*Makedog).keypressClear, "to clear screen", false},
		'g': {(*Makedog).keypressSignal, "to send signal", true},
		'h': {(*Makedog).keypressHelp, "for this help", false},
		'm': {(*Makedog).keypressMake, "to run make", false},
		'q': {(*Makedog).keypressQuit, "to quit", false},
		'r': {(*Makedog).keypressRestart, "to restart", false},
	}
}

// printKeypressInstructions prints available keypress handlers.
func (w *Makedog) printKeypressInstructions() {
	// Collect keys in sorted order
	keys := make([]byte, 0, len(defaultKeys))
	for k := range defaultKeys {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

	var descriptions []string
	running := w.processRunning()

	for _, k := range keys {
		handler := defaultKeys[k]
		if handler.requiresProcess && !running {
			continue
		}
		description := fmt.Sprintf("'%c' %s", k, handler.description)
		descriptions = append(descriptions, description)
	}

	printf("keys: %s\n", strings.Join(descriptions, ", "))
}

// keypressHelp prints the help message showing available keys.
func (w *Makedog) keypressHelp() step {
	w.printKeypressInstructions()
	return step{}
}

// keypressClear clears the terminal screen and reprints the key help.
func (w *Makedog) keypressClear() step {
	clearScreen()
	w.printKeypressInstructions()
	println()
	return step{}
}

// keypressQuit exits the program cleanly.
func (w *Makedog) keypressQuit() step {
	return step{stopBinary: true, exitAfter: true}
}

// keypressMake runs the make command and restarts the binary if successful.
func (w *Makedog) keypressMake() step {
	return step{
		stopBinary:  true,
		action:      func() { runCommand("make"); println() },
		startBinary: true,
	}
}

// keypressRestart restarts the binary.
func (w *Makedog) keypressRestart() step {
	w.clearSpinTracking()
	return step{stopBinary: true, startBinary: true}
}

// Signal mappings for signal menu.
var signalRow1 = []struct {
	key    byte
	name   string
	signal syscall.Signal
}{
	{'1', "HUP", syscall.SIGHUP},
	{'2', "TERM", syscall.SIGTERM},
	{'3', "INT", syscall.SIGINT},
	{'4', "QUIT", syscall.SIGQUIT},
	{'5', "USR1", syscall.SIGUSR1},
	{'6', "USR2", syscall.SIGUSR2},
	{'7', "WINCH", syscall.SIGWINCH},
	{'8', "TTOU", syscall.SIGTTOU},
	{'9', "TTIN", syscall.SIGTTIN},
}

var signalRow2 = []struct {
	key    string
	name   string
	signal syscall.Signal
}{
	{"a1", "KILL", syscall.SIGKILL},
	{"a2", "STOP", syscall.SIGSTOP},
	{"a3", "CONT", syscall.SIGCONT},
	{"a4", "TSTP", syscall.SIGTSTP},
	{"a5", "ALRM", syscall.SIGALRM},
	{"a6", "CHLD", syscall.SIGCHLD},
	{"a7", "PIPE", syscall.SIGPIPE},
	{"a8", "ABRT", syscall.SIGABRT},
}

// keypressSignal enters signal selection mode.
func (w *Makedog) keypressSignal() step {
	// Check if process is running
	if w.cmd == nil || w.cmd.Process == nil {
		printf("no process running\n")
		return step{}
	}

	// Print signal menu
	printSignalMenu()

	// Wait for signal selection from keyChan
	firstKey := <-w.keyChan

	// ESC key - return to main loop
	if firstKey == 27 {
		printf("signal cancelled\n")
		return step{}
	}

	// Check first row (single digit keys)
	for _, sig := range signalRow1 {
		if firstKey == sig.key {
			w.sendSignal(sig.name, sig.signal)
			return step{}
		}
	}

	// Check if it's 'a' for second row
	if firstKey == 'a' {
		// Read the second character
		secondKey := <-w.keyChan

		// Build the full key string
		fullKey := string([]byte{'a', secondKey})

		// Find matching signal in second row
		for _, sig := range signalRow2 {
			if fullKey == sig.key {
				w.sendSignal(sig.name, sig.signal)
				return step{}
			}
		}
	}

	printf("invalid signal selection\n")
	return step{}
}

// printSignalMenu displays the signal selection menu.
func printSignalMenu() {
	// First row
	var row1Items []string
	for _, sig := range signalRow1 {
		row1Items = append(row1Items, fmt.Sprintf(" %c: %-5s", sig.key, sig.name))
	}
	printf("  %s  (ESC to cancel)\n", strings.Join(row1Items, " "))

	// Second row
	var row2Items []string
	for _, sig := range signalRow2 {
		row2Items = append(row2Items, fmt.Sprintf("%2s: %-5s", sig.key, sig.name))
	}
	printf("  %s\n", strings.Join(row2Items, " "))
}

// sendSignal sends a signal to the child process.
func (w *Makedog) sendSignal(name string, sig syscall.Signal) {
	if w.cmd == nil || w.cmd.Process == nil {
		printf("no process running\n")
		return
	}

	herald("sending %s to pid %d", name, w.cmd.Process.Pid)
	err := w.cmd.Process.Signal(sig)
	if err != nil {
		printf("error sending signal: %v\n", err)
	}
}

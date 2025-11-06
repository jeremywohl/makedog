// Keypress handlers for interactive makedog controls.
package main

import (
	"fmt"
	"sort"
	"strings"
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
		'h': {(*Makedog).keypressHelp, "for this help", false},
		'm': {(*Makedog).keypressMake, "to run make", false},
		'q': {(*Makedog).keypressQuit, "to quit", false},
		'r': {(*Makedog).keypressRestart, "to restart", true},
		's': {(*Makedog).keypressSignal, "to send signal", true},
		't': {(*Makedog).keypressMakeTargets, "to run make targets", false},
		'x': {(*Makedog).keypressStartStop, "to start/stop", false},
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
	return step{stopBinary: true, exitAfter: true, stopReason: "quit requested"}
}

// keypressMake runs the make command and restarts the binary if successful.
func (w *Makedog) keypressMake() step {
	return step{
		stopBinary:  true,
		action:      func() { runCommand("make"); println() },
		startBinary: true,
		stopReason:  "make requested",
	}
}

// keypressRestart restarts the binary.
func (w *Makedog) keypressRestart() step {
	w.clearSpinTracking()
	return step{stopBinary: true, startBinary: true, stopReason: "restart requested"}
}

// keypressSignal enters signal selection mode.
func (w *Makedog) keypressSignal() step {
	return w.handleSignalMenu()
}

// keypressMakeTargets enters make target selection mode.
func (w *Makedog) keypressMakeTargets() step {
	return w.handleMakeTargetMenu()
}

// keypressStartStop starts the binary, if stopped, and stops the binary, if running.
func (w *Makedog) keypressStartStop() step {
	if w.processRunning() {
		return step{stopBinary: true, stopReason: "manual stop"}
	} else {
		return step{startBinary: true}
	}
}

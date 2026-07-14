// Signal handling and menu display for makedog.
package main

import (
	"fmt"
	"syscall"
)

// signalMenuItem represents a single signal option in the menu.
type signalMenuItem struct {
	key    string
	name   string
	signal syscall.Signal
}

// GetKey implements MenuItem interface.
func (s signalMenuItem) GetKey() string {
	return s.key
}

// GetDisplayName implements MenuItem interface.
func (s signalMenuItem) GetDisplayName() string {
	return s.name
}

// defaultSignals returns the standard set of signals when no config is provided.
func defaultSignals() []signalMenuItem {
	return []signalMenuItem{
		{"1", "HUP", syscall.SIGHUP},
		{"2", "TERM", syscall.SIGTERM},
		{"3", "INT", syscall.SIGINT},
		{"4", "QUIT", syscall.SIGQUIT},
		{"5", "USR1", syscall.SIGUSR1},
		{"6", "USR2", syscall.SIGUSR2},
		{"7", "WINCH", syscall.SIGWINCH},
		{"8", "TTOU", syscall.SIGTTOU},
		{"9", "TTIN", syscall.SIGTTIN},
		{"a1", "KILL", syscall.SIGKILL},
		{"a2", "STOP", syscall.SIGSTOP},
		{"a3", "CONT", syscall.SIGCONT},
		{"a4", "TSTP", syscall.SIGTSTP},
		{"a5", "ALRM", syscall.SIGALRM},
		{"a6", "CHLD", syscall.SIGCHLD},
		{"a7", "PIPE", syscall.SIGPIPE},
		{"a8", "ABRT", syscall.SIGABRT},
	}
}

// parseSignalName converts a signal name string to syscall.Signal.
func parseSignalName(name string) (syscall.Signal, bool) {
	signals := map[string]syscall.Signal{
		"HUP":   syscall.SIGHUP,
		"TERM":  syscall.SIGTERM,
		"INT":   syscall.SIGINT,
		"QUIT":  syscall.SIGQUIT,
		"USR1":  syscall.SIGUSR1,
		"USR2":  syscall.SIGUSR2,
		"WINCH": syscall.SIGWINCH,
		"TTOU":  syscall.SIGTTOU,
		"TTIN":  syscall.SIGTTIN,
		"KILL":  syscall.SIGKILL,
		"STOP":  syscall.SIGSTOP,
		"CONT":  syscall.SIGCONT,
		"TSTP":  syscall.SIGTSTP,
		"ALRM":  syscall.SIGALRM,
		"CHLD":  syscall.SIGCHLD,
		"PIPE":  syscall.SIGPIPE,
		"ABRT":  syscall.SIGABRT,
	}

	sig, ok := signals[name]
	return sig, ok
}

// buildSignalMenu constructs the signal menu from config or defaults.
func buildSignalMenu(config *Config) []signalMenuItem {
	// If no signals configured, use defaults
	if len(config.Signals) == 0 {
		return defaultSignals()
	}

	// Build menu from config
	var items []signalMenuItem
	keyIndex := 1

	for _, sc := range config.Signals {
		sig, ok := parseSignalName(sc.Signal)
		if !ok {
			// Skip invalid signal names
			continue
		}

		// Use custom name if provided, otherwise use signal name
		displayName := sc.Signal
		if sc.Name != "" {
			displayName = sc.Name
		}

		items = append(items, signalMenuItem{
			key:    assignMenuKey(keyIndex),
			name:   displayName,
			signal: sig,
		})

		keyIndex++
	}

	return items
}

// printSignalMenu displays the signal selection menu.
func printSignalMenu(items []signalMenuItem) {
	// Build keys, descriptions, and calculate max width
	keys := make([]string, len(items))
	descriptions := make([]string, len(items))
	maxDescWidth := 0

	for i, item := range items {
		keys[i] = item.key
		sigName := signalName(item.signal)
		if item.name == sigName {
			// Default signal name
			descriptions[i] = item.name
		} else {
			// Custom description from config - show both signal name and description
			descriptions[i] = fmt.Sprintf("%s (%s)", item.name, sigName)
		}
		if len(descriptions[i]) > maxDescWidth {
			maxDescWidth = len(descriptions[i])
		}
	}

	printMenuRows(keys, descriptions, maxDescWidth)
}

// handleSignalMenu displays the signal menu and handles user selection.
func (w *Makedog) handleSignalMenu() step {
	// Check if process is running
	if w.cmd == nil || w.cmd.Process == nil {
		printf("no process running\n")
		return step{}
	}

	// Build and print signal menu
	out.Event("Choose a signal (or ESC to cancel)")
	items := buildSignalMenu(w.config)
	printSignalMenu(items)

	// Handle user selection
	selected, ok := handleMenuKeySelection(items, w.keyChan)
	if !ok {
		out.Event("signal cancelled")
		return step{}
	}

	w.sendSignal(selected.name, selected.signal)
	return step{}
}

// sendSignal sends a signal to the child process.
func (w *Makedog) sendSignal(name string, sig syscall.Signal) {
	if w.cmd == nil || w.cmd.Process == nil {
		printf("no process running\n")
		return
	}

	var nativeName string
	if signalName(sig) != name {
		nativeName = " (" + signalName(sig) + ")"
	}
	out.Event("sending %s%s to pid %d", name, nativeName, w.cmd.Process.Pid)

	err := w.cmd.Process.Signal(sig)
	if err != nil {
		printf("error sending signal: %v\n", err)
	}
}

// signalName converts a signal to its name (e.g., TERM, KILL).
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGABRT:
		return "ABRT"
	case syscall.SIGALRM:
		return "ALRM"
	case syscall.SIGBUS:
		return "BUS"
	case syscall.SIGCHLD:
		return "CHLD"
	case syscall.SIGCONT:
		return "CONT"
	case syscall.SIGFPE:
		return "FPE"
	case syscall.SIGHUP:
		return "HUP"
	case syscall.SIGILL:
		return "ILL"
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGIO:
		return "IO"
	case syscall.SIGKILL:
		return "KILL"
	case syscall.SIGPIPE:
		return "PIPE"
	case syscall.SIGPROF:
		return "PROF"
	case syscall.SIGQUIT:
		return "QUIT"
	case syscall.SIGSEGV:
		return "SEGV"
	case syscall.SIGSTOP:
		return "STOP"
	case syscall.SIGSYS:
		return "SYS"
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGTRAP:
		return "TRAP"
	case syscall.SIGTSTP:
		return "TSTP"
	case syscall.SIGTTIN:
		return "TTIN"
	case syscall.SIGTTOU:
		return "TTOU"
	case syscall.SIGURG:
		return "URG"
	case syscall.SIGUSR1:
		return "USR1"
	case syscall.SIGUSR2:
		return "USR2"
	case syscall.SIGVTALRM:
		return "VTALRM"
	case syscall.SIGWINCH:
		return "WINCH"
	case syscall.SIGXCPU:
		return "XCPU"
	case syscall.SIGXFSZ:
		return "XFSZ"
	default:
		return fmt.Sprintf("signal %d", sig)
	}
}

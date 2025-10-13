// Signal echo test program for makedog testing.
// Traps signals and prints when received.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fmt.Println("Test fixture started, waiting for signals...")

	// Create channel to receive signals
	sigChan := make(chan os.Signal, 1)

	// Register to receive all signals we can trap
	signal.Notify(sigChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGWINCH,
		syscall.SIGALRM,
		syscall.SIGCHLD,
		syscall.SIGCONT,
		syscall.SIGTSTP,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
		syscall.SIGPIPE,
	)

	counter := 0
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigChan:
			counter++
			fmt.Printf("[%d] Test fixture received signal: %v (%s)\n",
				counter, sig, signalName(sig.(syscall.Signal)))
			if sig == syscall.SIGTERM {
				fmt.Printf("If makedog wants me to quit now, expect escalation to KILL\n")
			}
		case <-ticker.C:
			fmt.Printf("Still running... (received %d signals so far)\n", counter)
		}
	}
}

// signalName converts a signal to its name without SIG prefix.
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "HUP"
	case syscall.SIGINT:
		return "INT"
	case syscall.SIGQUIT:
		return "QUIT"
	case syscall.SIGTERM:
		return "TERM"
	case syscall.SIGUSR1:
		return "USR1"
	case syscall.SIGUSR2:
		return "USR2"
	case syscall.SIGWINCH:
		return "WINCH"
	case syscall.SIGALRM:
		return "ALRM"
	case syscall.SIGCHLD:
		return "CHLD"
	case syscall.SIGCONT:
		return "CONT"
	case syscall.SIGTSTP:
		return "TSTP"
	case syscall.SIGTTIN:
		return "TTIN"
	case syscall.SIGTTOU:
		return "TTOU"
	case syscall.SIGPIPE:
		return "PIPE"
	default:
		return fmt.Sprintf("signal %d", sig)
	}
}

// Makedog: watch a server binary and restart it on change, with interactive
// controls, signals, make targets, and per-run reporting. Runs are recorded
// to a durable store and replayable via the read verbs (show, runs).
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Build identity, stamped by the Makefile via -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = ""
)

// main dispatches on the first argument: a verb, a run reference (show
// sugar), or a slash-bearing or flag-led token (watch sugar). Bare words in
// first position belong to makedog; a binary is always reachable as ./name.
func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "makedog: no binary?\n\n")
		usage()
		os.Exit(1)
	}

	switch first := args[0]; {
	case first == "-h" || first == "--help":
		usage()
		os.Exit(0)
	case first == "watch":
		watchMain(args[1:])
	case first == "show":
		if len(args) < 2 {
			fatal("show: which run? (a number, or latest)")
		}
		showMain(args[1], args[2:])
	case first == "runs":
		runsMain(args[1:])
	case first == "next":
		nextMain(args[1:])
	case first == "tail":
		tailMain(args[1:])
	case first == "search" || first == "grep":
		searchMain(args[1:])
	case first == "path":
		pathMain(args[1:])
	case first == "info":
		infoMain(args[1:])
	case first == "diff":
		diffMain(args[1:])
	case first == "status":
		statusMain(args[1:])
	case first == "restart" || first == "stop" || first == "start":
		controlMain(ctrlRequest{Cmd: first}, args[1:])
	case first == "signal":
		signalMain(args[1:])
	case isRunRef(first):
		showMain(first, args[1:])
	case strings.HasPrefix(first, "-") || strings.ContainsRune(first, '/'):
		// Flag-led or slash-bearing: the traditional watch invocation.
		watchMain(args)
	default:
		fatal("unknown verb '%s' (to watch a binary named that, use ./%s)", first, first)
	}
}

// fatal reports an error and exits.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "makedog: "+format+"\n", args...)
	os.Exit(1)
}

// watchMain implements the watch verb: supervise a binary under the monitor loop.
func watchMain(args []string) {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {} // prevent library's usage display
	configPath := fs.String("c", "", "path to config file")
	fs.StringVar(configPath, "config", "", "path to config file")
	showHelp := fs.Bool("h", false, "print help")
	fs.BoolVar(showHelp, "help", false, "print help")

	if err := fs.Parse(args); err != nil {
		errMsg := regexp.MustCompile(`-(\w{2,})`).ReplaceAllString(err.Error(), `--$1`)
		fmt.Fprintf(os.Stderr, "makedog: %s\n\n", errMsg)
		usage()
		os.Exit(1)
	}

	if *showHelp {
		usage()
		os.Exit(0)
	}

	if fs.NArg() < 1 {
		fmt.Fprint(os.Stderr, "makedog: no binary?\n\n")
		usage()
		os.Exit(1)
	}

	// validate binary
	binaryPath := fs.Arg(0)
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fatal("binary '%s' not found", binaryPath)
	}

	// create before raw mode, so store/config warnings print normally
	makedog := NewMakedog(binaryPath, *configPath)

	// set raw terminal input, with monitor-decoupled output
	if err := setRawTerm(); err != nil {
		fatal("error setting raw mode: %v", err)
	}
	startTermWriter()

	// run loop
	if err := makedog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "makedog: error: %v\n", err)
		makedog.exitCleanly(1)
	}
}

// Makedog manages the lifecycle of watching and restarting a binary.
type Makedog struct {
	binaryPath   string
	run          *run         // current or most recent run; nil before the first start
	runCount     int          // last run number; session-local fallback when store is nil
	store        *binaryStore // durable run archive, nil when unavailable
	retention    retentionPolicy
	ctrlChan     chan *ctrlRequest // remote commands into the monitor loop; nil disables
	ctrlPending  chan ctrlResponse // parked reply for an in-flight state change
	ctrlListener net.Listener      // this instance's control socket
	ctrlSocket   string
	lastMtime    int64
	keyChan      chan byte
	extSignal    chan os.Signal
	restartTimes []time.Time
	config       *Config
}

// NewMakedog creates a new Makedog instance for the given binary path.
// A store failure disables run logging but never blocks watching.
func NewMakedog(binaryPath, configPath string) *Makedog {
	w := &Makedog{
		binaryPath: binaryPath,
		config:     loadConfig(configPath),
	}

	var err error
	if w.store, err = openBinaryStore(binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "makedog: run logs disabled: %v\n", err)
	}
	w.retention = retentionFromConfig(w.config.Logs)

	w.setupSignalHandlers()

	return w
}

// Run starts the main watch and restart loop.
func (w *Makedog) Run() error {
	w.printKeypressInstructions()
	println()

	if err := w.startBinary(); err != nil {
		return err
	}

	if w.store != nil {
		go w.maintainStore()
		w.startControl()
	}

	return w.monitor()
}

// exitCleanly waits for output to complete, performs cleanup, and exits the program.
func (w *Makedog) exitCleanly(status int) {
	if w.run != nil {
		w.run.drainOutput()
		w.run.finishLog()
	}
	w.stopControl()
	flushTerm()
	restoreTerm()
	os.Exit(status)
}

// setupSignalHandlers routes INT and TERM into the monitor loop, so external
// signals follow the same stop → drain → exit sequence as a keypress quit.
func (w *Makedog) setupSignalHandlers() {
	w.extSignal = make(chan os.Signal, 1)
	signal.Notify(w.extSignal, syscall.SIGINT, syscall.SIGTERM)
}

// startBinary begins a new run of the monitored binary, minting a durable run
// number and log file when the store is available.
func (w *Makedog) startBinary() error {
	var err error
	w.lastMtime, err = w.getMtime()
	if err != nil {
		return err
	}

	number := w.runCount + 1
	var logFile *os.File
	if w.store != nil {
		if n, f, err := w.store.beginRun(); err != nil {
			out.Error("run log unavailable: %v", err)
		} else {
			number, logFile = n, f
		}
	}

	r, err := startRun(w.binaryPath, number, logFile)
	if err != nil {
		if logFile != nil {
			logFile.Close()
			os.Remove(logFile.Name())
		}
		return err
	}

	w.runCount = number
	w.run = r
	return nil
}

// stopBinary terminates the monitored binary.
func (w *Makedog) stopBinary() {
	if !w.processRunning() {
		return
	}
	w.run.stop()
}

// exitChan returns the current run's exit channel, or nil (never ready) when
// there is no run.
func (w *Makedog) exitChan() chan error {
	if w.run == nil {
		return nil
	}
	return w.run.exit
}

// processRunning reports whether the child process is currently active.
func (w *Makedog) processRunning() bool {
	return w.run != nil && w.run.cmd.Process != nil && w.run.cmd.ProcessState == nil
}

// getMtime retrieves the modification time of the binary.
func (w *Makedog) getMtime() (int64, error) {
	info, err := os.Stat(w.binaryPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().Unix(), nil
}

// step sets the operations to perform in response to monitor events (keypresses, file changes, process exits).
type step struct {
	stopBinary  bool   // do we stop the binary prior to running an optional function?
	action      func() // do we run said function?
	startBinary bool   // do we start the binary after said function?
	exitAfter   bool   // do we exit makedog altogether after we complete the above?
	stopReason  string // why did the binary stop, whether internal or external?
}

// monitor runs the main loop, for file changes and keypresses.
func (w *Makedog) monitor() error {
	// Create a channel for keypress events
	w.keyChan = make(chan byte, 1)

	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return // stdin closed; no further keypresses possible
			}
			if n == 0 {
				continue
			}
			w.keyChan <- buf[0]
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		var s step

		select {
		case key := <-w.keyChan:
			s = w.handleKeypress(key)
		case req := <-w.ctrlChan:
			s = w.handleControl(req)
		case sig := <-w.extSignal:
			s = step{stopBinary: true, exitAfter: true, stopReason: "external signal " + signalName(sig.(syscall.Signal))}
		case <-ticker.C:
			s = w.checkFileModification()
		case <-w.exitChan():
			w.run.stopTime = time.Now()
			w.run.drainOutput()
			w.printExitDetails(false)
			w.run.finishLog()

			// Check for spinning process
			if w.checkForSpin() {
				out.EventAt(time.Now(), "pausing for spinning process")
				w.printKeypressInstructions()
				println()
				s = step{}
			} else {
				s = step{startBinary: true}
			}
		}

		if s.stopBinary {
			if w.processRunning() {
				w.run.stopTime = time.Now()
				w.run.stopReason = s.stopReason
				w.stopBinary()
				w.printExitDetails(true)
				w.run.finishLog()
			} else if w.run != nil {
				// The child may have exited on its own an instant before this
				// step; report the buffered exit rather than dropping it.
				select {
				case <-w.run.exit:
					w.run.stopTime = time.Now()
					w.run.drainOutput()
					w.printExitDetails(false)
					w.run.finishLog()
				default:
				}
			}
		}

		if s.action != nil {
			s.action()
		}

		if s.exitAfter {
			w.exitCleanly(0)
		}

		if s.startBinary {
			if err := w.startBinary(); err != nil {
				out.Error("failed to start %s: %v", w.binaryPath, err)
			}
		}

		w.flushControlReply()
	}
}

// handleKeypress processes keypress events.
func (w *Makedog) handleKeypress(key byte) step {
	// Handle Ctrl-C (ASCII 3)
	if key == 3 {
		return step{stopBinary: true, exitAfter: true, stopReason: "manual interruption"}
	}

	if handler, ok := defaultKeys[key]; ok {
		if handler.requiresProcess && !w.processRunning() {
			printf("'%c' unavailable: no process running\n", key)
			return step{}
		}
		return handler.action(w)
	}

	return step{}
}

// checkFileModification checks if the binary has been modified and restarts if needed.
func (w *Makedog) checkFileModification() step {
	currentMtime, err := w.getMtime()
	if err != nil {
		return step{}
	}

	if currentMtime != w.lastMtime {
		return step{
			stopBinary:  true,
			action:      func() { time.Sleep(500 * time.Millisecond) },
			startBinary: true,
			stopReason:  "binary modified",
		}
	}

	return step{}
}

// checkForSpin detects if the process is spinning (exits rapidly after start).
// Records the current restart time and returns true if ≥3 restarts occurred within 5 seconds.
// Clears spin tracking if process ran successfully (≥10 seconds).
func (w *Makedog) checkForSpin() bool {
	// Check if process ran long enough to be considered successful
	runDuration := time.Since(w.run.startTime)
	if runDuration >= 10*time.Second {
		w.clearSpinTracking()
		return false
	}

	now := time.Now()
	w.restartTimes = append(w.restartTimes, now)

	// Keep only last 5 restart times
	if len(w.restartTimes) > 5 {
		w.restartTimes = w.restartTimes[len(w.restartTimes)-5:]
	}

	// Need at least 3 restarts to detect spinning
	if len(w.restartTimes) < 3 {
		return false
	}

	// Check if we have ≥3 restarts within 5 seconds
	threshold := now.Add(-5 * time.Second)
	recentRestarts := 0
	for _, t := range w.restartTimes {
		if t.After(threshold) {
			recentRestarts++
		}
	}

	return recentRestarts >= 3
}

// clearSpinTracking clears the restart tracking, called when process runs successfully.
func (w *Makedog) clearSpinTracking() {
	w.restartTimes = nil
}

// waitStatus abstracts syscall.WaitStatus for mocking.
type waitStatus interface {
	Signaled() bool
	Signal() syscall.Signal
}

// processState abstracts os.ProcessState for mocking.
type processState interface {
	ExitCode() int
	Sys() interface{}      // Returns waitStatus
	SysUsage() interface{} // Returns *syscall.Rusage
}

// printExitDetails handles the child process exiting, keeping the derived
// stop reason on the run so the log's exit record carries it.
func (w *Makedog) printExitDetails(makedogInitiated bool) {
	r := w.run
	r.stopReason = _printExitDetails(r.cmd.ProcessState, r.number, r.startTime, r.stopTime, w.binaryPath, makedogInitiated, r.stopReason)
}

// _printExitDetails handles the child process exiting (internal, testable
// function). Returns the stop reason, which it may derive from the exit state.
func _printExitDetails(state processState, number int, startTime, stopTime time.Time, binaryPath string, makedogInitiated bool, stopReason string) string {
	exitCode := state.ExitCode()
	waitStatus := state.Sys().(waitStatus)
	sysUsage := state.SysUsage().(*syscall.Rusage)

	// Calculate times
	cpuTime := sysUsage.Utime.Nano() + sysUsage.Stime.Nano()
	wallTime := time.Since(startTime).Nanoseconds()

	// How did our death go down
	var exitDesc string
	if !makedogInitiated {
		if waitStatus.Signaled() {
			exitDesc = fmt.Sprintf("killed by %s, ", signalName(waitStatus.Signal()))
			stopReason = "exited after external signal " + signalName(waitStatus.Signal())
		} else {
			exitDesc = fmt.Sprintf("exit code %d, ", exitCode)
		}
	}

	out.Command(
		"stop  %d %s [%s%s memory, %s cpu time, %s wall time]",
		number,
		binaryPath,
		exitDesc,
		formatMemory(sysUsage.Maxrss),
		formatDuration(cpuTime),
		formatDuration(wallTime),
	)

	println()

	if stopReason != "" {
		out.EventAt(stopTime, "%s", stopReason)
	}

	println()

	return stopReason
}

// One execution of the monitored binary: its process, pty, output reader, and
// exit notification. Every start creates a fresh run, so no state bleeds from
// one run into the next.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// run holds the state of one execution of the monitored binary.
type run struct {
	number     int
	cmd        *exec.Cmd
	pty        *os.File
	log        *runLog       // this run's log file, nil when persistence is unavailable
	exit       chan error    // receives the cmd.Wait result, once
	outputDone chan struct{} // closed when the output reader finishes
	startTime  time.Time
	stopTime   time.Time
	stopReason string // why the run ended, whether internal or external
}

// startRun launches the binary under a pty and begins relaying its output,
// recording into logFile (which may be nil) via the sink.
func startRun(binaryPath string, number int, logFile *os.File) (*run, error) {
	cmd := exec.Command(binaryPath)

	// Start with pty for real-time output
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	r := &run{
		number:     number,
		cmd:        cmd,
		pty:        ptmx,
		exit:       make(chan error, 1),
		outputDone: make(chan struct{}),
		startTime:  time.Now(),
	}

	// Collect metadata
	binaryHash, _ := getBinaryHash(binaryPath)
	gitBranch, gitCommit, _ := getGitInfo()

	// Open the run log with its header, then attach the sink so everything
	// from the start message on is recorded.
	if logFile != nil {
		cwd, _ := os.Getwd()
		r.log = &runLog{f: logFile}
		r.log.write(record{
			T: recMeta, TS: r.startTime, Run: number, Binary: binaryPath, Cwd: cwd,
			Pid: cmd.Process.Pid, Hash: binaryHash, GitBranch: gitBranch, GitCommit: gitCommit,
			Makedog: Version,
		})
		out.attach(r.log)
	}

	// Build the start message
	msg := fmt.Sprintf("start %d %s (pid %d", number, binaryPath, cmd.Process.Pid)
	if binaryHash != "" {
		msg += fmt.Sprintf(", hash %s", binaryHash[:7])
	}
	if git := gitLabel(gitBranch, gitCommit); git != "" {
		msg += fmt.Sprintf(", git %s", git)
	}
	msg += ")"

	out.Command("%s", msg)

	// Relay output (stdout and stderr merged), and observe the exit.
	go r.processOutput(ptmx)
	go func() { r.exit <- cmd.Wait() }()

	return r, nil
}

// processOutput relays each line of run output through the sink.
// Reads lines directly rather than through a Scanner: a Scanner's token limit would
// silently end capture on the first oversized line. A final unterminated line still prints.
func (r *run) processOutput(reader io.Reader) {
	defer close(r.outputDone)

	buffered := bufio.NewReader(reader)
	for {
		line, err := buffered.ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line != "" || err == nil {
			out.ChildLine(time.Now(), line)
		}
		if err != nil {
			return
		}
	}
}

// stop terminates the run's process.
func (r *run) stop() {
	// Ask the child to exit gracefully.
	_ = r.cmd.Process.Signal(syscall.SIGTERM)

	// If the child ignores SIGTERM, escalate after a short grace period so we
	// don't hang waiting on the exit.
	const gracefulShutdownTimeout = 2 * time.Second
	timer := time.NewTimer(gracefulShutdownTimeout)
	defer timer.Stop()

	var exited bool
	select {
	case <-r.exit:
		exited = true
	case <-timer.C:
		out.Event("process unresponsive after SIGTERM, sending SIGKILL")
		if err := r.cmd.Process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			printf("error sending SIGKILL: %v\n", err)
		}
	}

	if !exited {
		<-r.exit
	}

	// The child is gone; let the reader drain its final output (e.g. graceful
	// shutdown messages) before the pty closes.
	r.drainOutput()
}

// finishLog seals the run's log with an exit record and detaches it from the
// sink, so stragglers after the drain can't land past the trailer. Idempotent.
func (r *run) finishLog() {
	if r.log == nil {
		return
	}
	out.detach()

	rec := record{T: recExit, TS: r.stopTime, Reason: r.stopReason}
	if state := r.cmd.ProcessState; state != nil {
		code := state.ExitCode()
		rec.ExitCode = &code
		if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			rec.Signal = signalName(ws.Signal())
		}
		if ru, ok := state.SysUsage().(*syscall.Rusage); ok {
			rec.MaxRSS = ru.Maxrss
			rec.CPUNs = ru.Utime.Nano() + ru.Stime.Nano()
		}
	}
	if !r.stopTime.IsZero() {
		rec.WallNs = r.stopTime.Sub(r.startTime).Nanoseconds()
	}

	r.log.write(rec)
	r.log.close()
	r.log = nil
}

// drainOutput waits briefly for the output reader to finish, then closes the pty.
// The wait is bounded: orphaned descendants of the child can hold the pty open
// indefinitely, and we won't hang on them.
func (r *run) drainOutput() {
	select {
	case <-r.outputDone:
	case <-time.After(1 * time.Second):
	}

	if r.pty != nil {
		r.pty.Close()
		r.pty = nil
	}
}

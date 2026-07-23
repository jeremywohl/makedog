// Live control: command a running makedog from another terminal. Each watch
// instance listens on a unix socket (in a short tmpdir path — sun_path caps
// at ~104 bytes, which the store's deep directories exceed) and registers
// itself in its lineage's instances directory. The status, restart, stop,
// start, and signal verbs discover instances there, prune the stale, and
// speak one JSON request/response per connection. Requests are serviced by
// the monitor loop itself, so remote commands obey exactly the keypress
// paths' state discipline; mutating verbs demand --instance when several
// instances are live rather than guessing which process to touch.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

// ctrlRequest is one command to a live instance.
type ctrlRequest struct {
	Cmd    string `json:"cmd"`              // status | restart | stop | start | signal
	Signal string `json:"signal,omitempty"` // for cmd signal, e.g. "HUP"
	reply  chan ctrlResponse
}

// ctrlResponse reports an instance's state after the command.
type ctrlResponse struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Pid      int       `json:"pid,omitempty"` // the makedog instance
	Binary   string    `json:"binary,omitempty"`
	Run      int       `json:"run,omitempty"`
	Running  bool      `json:"running"`
	ChildPid int       `json:"child_pid,omitempty"`
	Started  time.Time `json:"started,omitzero"` // current run's start
}

// instanceInfo is one live watch instance's registry record.
type instanceInfo struct {
	Pid     int       `json:"pid"`
	Socket  string    `json:"socket"`
	Binary  string    `json:"binary"`
	Started time.Time `json:"started"` // instance start, not run start
}

// ---- server side: the watch instance ----

// startControl opens this instance's socket and registers it. Failure warns
// and leaves the instance uncontrollable but otherwise healthy.
func (w *Makedog) startControl() {
	dir := filepath.Join(os.TempDir(), "makedog-ctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		out.Error("remote control disabled: %v", err)
		return
	}
	w.ctrlSocket = filepath.Join(dir, fmt.Sprintf("%d.sock", os.Getpid()))
	os.Remove(w.ctrlSocket) // stale socket from a recycled pid

	ln, err := net.Listen("unix", w.ctrlSocket)
	if err != nil {
		out.Error("remote control disabled: %v", err)
		return
	}
	w.ctrlListener = ln
	w.ctrlChan = make(chan *ctrlRequest)

	w.store.registerInstance(instanceInfo{
		Pid: os.Getpid(), Socket: w.ctrlSocket, Binary: w.binaryPath, Started: time.Now(),
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed on shutdown
			}
			go w.serveControl(conn)
		}
	}()
}

// stopControl tears the socket and registry entry down.
func (w *Makedog) stopControl() {
	if w.ctrlListener == nil {
		return
	}
	w.ctrlListener.Close()
	os.Remove(w.ctrlSocket)
	w.store.deregisterInstance(os.Getpid())
	w.ctrlListener = nil
}

// serveControl handles one connection: decode a request, hand it to the
// monitor loop, relay the reply. Bounded waits keep a busy or wedged monitor
// (a long make target, say) from hanging the caller.
func (w *Makedog) serveControl(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	enc := json.NewEncoder(conn)

	var req ctrlRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	req.reply = make(chan ctrlResponse, 1)

	select {
	case w.ctrlChan <- &req:
	case <-time.After(5 * time.Second):
		enc.Encode(ctrlResponse{Error: "makedog is busy"})
		return
	}
	select {
	case resp := <-req.reply:
		enc.Encode(resp)
	case <-time.After(20 * time.Second):
		enc.Encode(ctrlResponse{Error: "timed out awaiting makedog"})
	}
}

// handleControl services a request inside the monitor loop. Immediate
// commands reply here; state-changing ones return the step and park the
// reply, which the loop sends once the step has run.
func (w *Makedog) handleControl(req *ctrlRequest) step {
	running := w.processRunning()
	defer_ := func(s step) step {
		w.ctrlPending = req.reply
		return s
	}

	switch req.Cmd {
	case "status":
		req.reply <- w.controlStatus()
	case "signal":
		sig, ok := parseSignalName(strings.TrimPrefix(req.Signal, "SIG"))
		switch {
		case !ok:
			req.reply <- ctrlResponse{Error: fmt.Sprintf("unknown signal %q", req.Signal)}
		case !running:
			req.reply <- ctrlResponse{Error: "no process running"}
		default:
			w.sendSignal(strings.TrimPrefix(req.Signal, "SIG"), sig)
			req.reply <- w.controlStatus()
		}
	case "stop":
		if !running {
			req.reply <- w.controlStatus()
			break
		}
		return defer_(step{stopBinary: true, stopReason: "remote stop requested"})
	case "start":
		if running {
			req.reply <- w.controlStatus()
			break
		}
		return defer_(step{startBinary: true})
	case "restart":
		w.clearSpinTracking()
		if !running {
			return defer_(step{startBinary: true})
		}
		return defer_(step{stopBinary: true, startBinary: true, stopReason: "remote restart requested"})
	default:
		req.reply <- ctrlResponse{Error: fmt.Sprintf("unknown command %q", req.Cmd)}
	}
	return step{}
}

// flushControlReply completes a parked state-changing request with the
// post-step state.
func (w *Makedog) flushControlReply() {
	if w.ctrlPending != nil {
		w.ctrlPending <- w.controlStatus()
		w.ctrlPending = nil
	}
}

// controlStatus snapshots the instance for a response.
func (w *Makedog) controlStatus() ctrlResponse {
	resp := ctrlResponse{OK: true, Pid: os.Getpid(), Binary: w.binaryPath, Running: w.processRunning()}
	if w.run != nil {
		resp.Run = w.run.number
		resp.Started = w.run.startTime
		if resp.Running {
			resp.ChildPid = w.run.cmd.Process.Pid
		}
	}
	return resp
}

// ---- instance registry, in the lineage directory ----

func (s *binaryStore) instancesDir() string { return filepath.Join(s.dir, "instances") }

func (s *binaryStore) instancePath(pid int) string {
	return filepath.Join(s.instancesDir(), fmt.Sprintf("%d.json", pid))
}

func (s *binaryStore) registerInstance(inst instanceInfo) {
	if err := os.MkdirAll(s.instancesDir(), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(inst)
	if err != nil {
		return
	}
	os.WriteFile(s.instancePath(inst.Pid), append(b, '\n'), 0o644)
}

func (s *binaryStore) deregisterInstance(pid int) {
	os.Remove(s.instancePath(pid))
}

// liveInstances lists this lineage's live instances, pruning records whose
// process is gone (an unclean death).
func (s *binaryStore) liveInstances() []instanceInfo {
	entries, err := os.ReadDir(s.instancesDir())
	if err != nil {
		return nil
	}
	var live []instanceInfo
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(s.instancesDir(), e.Name()))
		if err != nil {
			continue
		}
		var inst instanceInfo
		if json.Unmarshal(b, &inst) != nil || inst.Pid == 0 {
			continue
		}
		if syscall.Kill(inst.Pid, 0) != nil {
			os.Remove(filepath.Join(s.instancesDir(), e.Name()))
			continue
		}
		live = append(live, inst)
	}
	return live
}

// ---- client side: the control verbs ----

// controlCall performs one request against an instance's socket.
func controlCall(socket string, req ctrlRequest) (ctrlResponse, error) {
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return ctrlResponse{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(25 * time.Second))

	if err := json.NewEncoder(conn).Encode(struct {
		Cmd    string `json:"cmd"`
		Signal string `json:"signal,omitempty"`
	}{req.Cmd, req.Signal}); err != nil {
		return ctrlResponse{}, err
	}
	var resp ctrlResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return ctrlResponse{}, err
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

// findInstances gathers live instances: one lineage's under --binary, the
// whole project's otherwise.
func findInstances(dirFlag, binaryFlag string) ([]instanceInfo, error) {
	var stores []*binaryStore
	if binaryFlag != "" {
		s, err := openLineage(dirFlag, binaryFlag)
		if err != nil {
			return nil, err
		}
		stores = []*binaryStore{s}
	} else {
		var err error
		if stores, err = projectLineages(dirFlag); err != nil {
			return nil, err
		}
	}
	var live []instanceInfo
	for _, s := range stores {
		live = append(live, s.liveInstances()...)
	}
	return live, nil
}

// statusMain implements `makedog status`: every live instance in the project.
func statusMain(args []string) {
	fs := newVerbFlags("status")
	jsonOut := fs.Bool("json", false, "emit one JSON object per instance")
	fs.BoolVar(jsonOut, "jsonl", false, "emit one JSON object per instance")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, args)

	instances, err := findInstances(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	if len(instances) == 0 {
		fatal("no live makedog instances")
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !*jsonOut {
		fmt.Fprintln(tw, "PID\tBINARY\tRUN\tSTATE\tCHILD\tUPTIME")
	}
	enc := json.NewEncoder(os.Stdout)
	for _, inst := range instances {
		resp, err := controlCall(inst.Socket, ctrlRequest{Cmd: "status"})
		if err != nil {
			resp = ctrlResponse{Pid: inst.Pid, Binary: inst.Binary, Error: err.Error()}
		}
		if *jsonOut {
			enc.Encode(resp)
			continue
		}
		state, child, uptime := "stopped", "", ""
		if resp.Error != "" {
			state = "unreachable"
		} else if resp.Running {
			state = "running"
			child = fmt.Sprintf("%d", resp.ChildPid)
			uptime = formatDuration(time.Since(resp.Started).Nanoseconds())
		}
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\n", resp.Pid, resp.Binary, resp.Run, state, child, uptime)
	}
	tw.Flush()
}

// controlMain implements the mutating verbs restart, stop, and start, plus
// signal (with its name in req.Signal). Ambiguity is an error, never a guess.
func controlMain(req ctrlRequest, args []string) {
	fs := newVerbFlags(req.Cmd)
	jsonOut := fs.Bool("json", false, "emit the instance's response as JSON")
	fs.BoolVar(jsonOut, "jsonl", false, "emit the instance's response as JSON")
	instance := fs.Int("instance", 0, "target this makedog pid, when several are live")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, args)

	instances, err := findInstances(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	if *instance != 0 {
		kept := instances[:0]
		for _, inst := range instances {
			if inst.Pid == *instance {
				kept = append(kept, inst)
			}
		}
		instances = kept
	}
	switch {
	case len(instances) == 0:
		fatal("no live makedog instance to %s", req.Cmd)
	case len(instances) > 1:
		var names []string
		for _, inst := range instances {
			names = append(names, fmt.Sprintf("%d (%s)", inst.Pid, inst.Binary))
		}
		fatal("several live instances: %s; target one with --instance <pid>", strings.Join(names, ", "))
	}

	resp, err := controlCall(instances[0].Socket, req)
	if err != nil {
		fatal("%s: %v", req.Cmd, err)
	}
	if *jsonOut {
		b, _ := json.Marshal(resp)
		fmt.Println(string(b))
		return
	}

	switch {
	case req.Cmd == "signal":
		fmt.Printf("sent %s to run %d (child pid %d)\n", req.Signal, resp.Run, resp.ChildPid)
	case resp.Running:
		fmt.Printf("run %d running (child pid %d)\n", resp.Run, resp.ChildPid)
	default:
		fmt.Printf("stopped (last run %d)\n", resp.Run)
	}
}

// signalMain implements `makedog signal <name>`.
func signalMain(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fatal("signal: which signal? (e.g. HUP, USR1)")
	}
	name := strings.TrimPrefix(args[0], "SIG")
	if _, ok := parseSignalName(name); !ok {
		fatal("unknown signal %q", args[0])
	}
	controlMain(ctrlRequest{Cmd: "signal", Signal: name}, args[1:])
}

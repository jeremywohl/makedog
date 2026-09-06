// Dynamic log levels: relay a level word to the supervised binary by whatever
// transport the project's [loglevel] config declares. Makedog defines no
// levels of its own — named entries map words to complete pokes (an env
// assignment, a signal, a command) and close the vocabulary; only a config
// with no named levels passes arbitrary words through its top-level
// transport. Every application is ephemeral: an env level
// spawns one run at that level, a signal or command touches only the live
// run, and the next restart reverts to baseline; permanent levels belong in
// the project's own configuration.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/term"
)

// LoglevelConfig is the parsed [loglevel] table: optional passthrough
// transports plus named levels. See parseLoglevel for the schema.
type LoglevelConfig struct {
	Env     string                // passthrough: `loglevel <word>` becomes <Env>=<word>
	Command string                // passthrough: <word> substituted at %s
	Levels  map[string]levelEntry // named levels, each a complete poke
	order   []string              // declaration order, for listing and errors
}

// levelEntry is one named level's poke; exactly one field is set.
type levelEntry struct {
	Env     string `toml:"env"` // value for the top-level Env variable
	Signal  string `toml:"signal"`
	Command string `toml:"command"`
}

// poke is a resolved level: the one action that applies it.
type poke struct {
	level   string
	env     string // a complete VAR=value assignment for the next run
	signal  string // a signal name, e.g. "USR1"
	command string // a complete shell command
}

// parseLoglevel interprets the raw [loglevel] table. The reserved keys env
// and command name transports (at most one); every other key is a level name
// whose entry sets exactly one of env, signal, or command. Alone, a
// transport is a passthrough — `loglevel <word>` becomes DEBUG=<word> —
// while named levels close the vocabulary, absent words erroring and env
// serving only to name the variable:
//
//	[loglevel]
//	env          = "DEBUG"
//	debug.env    = "app:*,http:*"
//	up.signal    = "USR1"
//	mute.command = "curl -sX PUT localhost:8080/log/level -d off"
func parseLoglevel(md toml.MetaData, table map[string]toml.Primitive) (*LoglevelConfig, error) {
	if len(table) == 0 {
		return nil, nil
	}
	lc := &LoglevelConfig{Levels: map[string]levelEntry{}}

	// md.Keys carries file order, which the table map loses.
	seen := map[string]bool{}
	for _, key := range md.Keys() {
		if len(key) < 2 || key[0] != "loglevel" || seen[key[1]] {
			continue
		}
		name := key[1]
		seen[name] = true
		switch name {
		case "env":
			if err := md.PrimitiveDecode(table[name], &lc.Env); err != nil {
				return nil, fmt.Errorf("env must name a variable (a string)")
			}
		case "command":
			if err := md.PrimitiveDecode(table[name], &lc.Command); err != nil {
				return nil, fmt.Errorf("command must be a string")
			}
			if !strings.Contains(lc.Command, "%s") {
				return nil, fmt.Errorf("passthrough command needs a %%s to carry the level")
			}
		default:
			var entry levelEntry
			if err := md.PrimitiveDecode(table[name], &entry); err != nil {
				return nil, fmt.Errorf("level %s: want %s.env, %s.signal, or %s.command", name, name, name, name)
			}
			set := 0
			for _, v := range []string{entry.Env, entry.Signal, entry.Command} {
				if v != "" {
					set++
				}
			}
			if set != 1 {
				return nil, fmt.Errorf("level %s must set exactly one of env, signal, command", name)
			}
			if entry.Signal != "" {
				entry.Signal = strings.TrimPrefix(entry.Signal, "SIG")
				if _, ok := parseSignalName(entry.Signal); !ok {
					return nil, fmt.Errorf("level %s: unknown signal %q", name, entry.Signal)
				}
			}
			lc.Levels[name] = entry
			lc.order = append(lc.order, name)
		}
	}

	if lc.Env != "" && lc.Command != "" {
		return nil, fmt.Errorf("env and command are both transports; declare one")
	}
	// Named levels preclude passthrough, so a transport must either stand
	// alone or be consumed by the levels; anything else is dead config.
	if len(lc.order) > 0 && lc.Command != "" {
		return nil, fmt.Errorf("command passthrough is unreachable once levels are named")
	}
	envUsed := false
	for _, name := range lc.order {
		if lc.Levels[name].Env != "" {
			if lc.Env == "" {
				return nil, fmt.Errorf("level %s sets an env value but no top-level env names the variable", name)
			}
			envUsed = true
		}
	}
	if len(lc.order) > 0 && lc.Env != "" && !envUsed {
		return nil, fmt.Errorf("env is unused: named levels preclude passthrough and none sets an env value")
	}
	return lc, nil
}

// passthroughWord bounds levels substituted into the passthrough command.
// The control socket is same-user, so this guards against quoting accidents,
// not adversaries.
var passthroughWord = regexp.MustCompile(`^[\w.,:*=@/-]+$`)

// resolve maps a level word to its poke: a named entry verbatim; only when
// no levels are named does the passthrough transport carry the word itself.
func (lc *LoglevelConfig) resolve(level string) (poke, error) {
	if entry, ok := lc.Levels[level]; ok {
		switch {
		case entry.Env != "":
			return poke{level: level, env: lc.Env + "=" + entry.Env}, nil
		case entry.Signal != "":
			return poke{level: level, signal: entry.Signal}, nil
		default:
			return poke{level: level, command: entry.Command}, nil
		}
	}
	if len(lc.order) > 0 {
		return poke{}, fmt.Errorf("unknown level %q; config defines: %s", level, strings.Join(lc.order, ", "))
	}
	if lc.Command != "" {
		if !passthroughWord.MatchString(level) {
			return poke{}, fmt.Errorf("level %q has characters unsafe to substitute into a command", level)
		}
		return poke{level: level, command: strings.ReplaceAll(lc.Command, "%s", level)}, nil
	}
	return poke{level: level, env: lc.Env + "=" + level}, nil
}

// levelListing is one row of `loglevel --list`; name "*" is the passthrough.
type levelListing struct {
	Name   string `json:"name"`
	Via    string `json:"via"` // env | signal | command
	Detail string `json:"detail,omitempty"`
}

// listing renders the config as rows: named levels in file order, or the
// lone passthrough transport when none are named.
func (lc *LoglevelConfig) listing() []levelListing {
	var rows []levelListing
	for _, name := range lc.order {
		entry := lc.Levels[name]
		switch {
		case entry.Env != "":
			rows = append(rows, levelListing{name, "env", lc.Env + "=" + entry.Env})
		case entry.Signal != "":
			rows = append(rows, levelListing{name, "signal", entry.Signal})
		default:
			rows = append(rows, levelListing{name, "command", entry.Command})
		}
	}
	if len(rows) == 0 {
		switch {
		case lc.Env != "":
			rows = append(rows, levelListing{"*", "env", lc.Env + "=<level>"})
		case lc.Command != "":
			rows = append(rows, levelListing{"*", "command", lc.Command})
		}
	}
	return rows
}

// ---- interactive menu: the l key ----

// loglevelMenuItem is one named level in the l menu.
type loglevelMenuItem struct {
	key   string
	label string // name plus a mechanism hint
	name  string
}

func (i loglevelMenuItem) GetKey() string         { return i.key }
func (i loglevelMenuItem) GetDisplayName() string { return i.label }

// buildLoglevelMenu lists the named levels in config order.
func buildLoglevelMenu(lc *LoglevelConfig) []loglevelMenuItem {
	var items []loglevelMenuItem
	for i, name := range lc.order {
		entry := lc.Levels[name]
		var label string
		switch {
		case entry.Signal != "":
			label = fmt.Sprintf("%s (%s)", name, entry.Signal)
		case entry.Command != "":
			label = name + " (cmd)"
		default:
			label = name + " (env)"
		}
		items = append(items, loglevelMenuItem{assignMenuKey(i + 1), label, name})
	}
	return items
}

// handleLoglevelMenu displays the named levels and applies the selection.
// An env level restarts into the level — the deliberate keypress is the
// consent a remote caller gives with --restart; signal and command levels
// poke the live run, as ever.
func (w *Makedog) handleLoglevelMenu() step {
	lc := w.config.Loglevel
	switch {
	case lc == nil:
		printf("no [loglevel] section in this project's config\n")
		return step{}
	case len(lc.order) == 0:
		printf("passthrough only; use 'makedog loglevel <level>' from another terminal\n")
		return step{}
	}

	out.Event("Choose a log level (or ESC to cancel)")
	items := buildLoglevelMenu(lc)
	keys := make([]string, len(items))
	labels := make([]string, len(items))
	maxWidth := 0
	for i, item := range items {
		keys[i], labels[i] = item.key, item.label
		maxWidth = max(maxWidth, len(item.label))
	}
	printMenuRows(keys, labels, maxWidth)

	selected, ok := handleMenuKeySelection(items, w.keyChan)
	if !ok {
		out.Event("loglevel cancelled")
		return step{}
	}

	p, _ := lc.resolve(selected.name) // a named entry always resolves
	switch {
	case p.signal != "":
		sig, _ := parseSignalName(p.signal)
		out.Event("loglevel %s", p.level)
		w.sendSignal(p.signal, sig)
	case p.command != "":
		if !w.processRunning() {
			printf("no process running\n")
			return step{}
		}
		out.Event("loglevel %s: %s", p.level, p.command)
		if err := runLoglevelCommand(p.command); err != nil {
			out.Error("loglevel command: %v", err)
		}
	default:
		w.clearSpinTracking()
		w.nextRunEnv, w.nextRunLevel = []string{p.env}, p.level
		if w.processRunning() {
			return step{stopBinary: true, startBinary: true, stopReason: "loglevel " + p.level + " requested"}
		}
		return step{startBinary: true}
	}
	return step{}
}

// ---- server side: applying a level inside the monitor loop ----

// handleLoglevel services the loglevel control verb. Signal and command pokes
// touch the live run and reply here; an env poke needs a restart, which the
// caller must have acknowledged (req.Restart), and parks its reply like any
// state-changing verb.
func (w *Makedog) handleLoglevel(req *ctrlRequest) step {
	lc := w.config.Loglevel
	if lc == nil {
		req.reply <- ctrlResponse{Error: "no [loglevel] section in this project's config"}
		return step{}
	}
	p, err := lc.resolve(req.Level)
	if err != nil {
		req.reply <- ctrlResponse{Error: err.Error()}
		return step{}
	}

	switch {
	case p.signal != "":
		if !w.processRunning() {
			req.reply <- ctrlResponse{Error: "no process running"}
			return step{}
		}
		sig, _ := parseSignalName(p.signal)
		out.Event("loglevel %s", p.level)
		w.sendSignal(p.signal, sig)
		req.reply <- w.controlStatus()

	case p.command != "":
		if !w.processRunning() {
			req.reply <- ctrlResponse{Error: "no process running"}
			return step{}
		}
		out.Event("loglevel %s: %s", p.level, p.command)
		if err := runLoglevelCommand(p.command); err != nil {
			out.Error("loglevel command: %v", err)
			req.reply <- ctrlResponse{Error: fmt.Sprintf("loglevel command: %v", err)}
			return step{}
		}
		req.reply <- w.controlStatus()

	default: // env: spawn one run at this level; later restarts revert
		if !req.Restart {
			variable, _, _ := strings.Cut(p.env, "=")
			req.reply <- ctrlResponse{NeedsRestart: true,
				Error: fmt.Sprintf("level %q applies via %s and restarts the binary", p.level, variable)}
			return step{}
		}
		w.clearSpinTracking()
		w.nextRunEnv, w.nextRunLevel = []string{p.env}, p.level
		w.ctrlPending = req
		if w.processRunning() {
			return step{stopBinary: true, startBinary: true, stopReason: "loglevel " + p.level + " requested"}
		}
		return step{startBinary: true}
	}
	return step{}
}

// loglevelCommandTimeout bounds a poke command; it runs inside the monitor
// loop, so a hung endpoint must not wedge the session for long.
const loglevelCommandTimeout = 10 * time.Second

// runLoglevelCommand executes a poke command through the shell, relaying its
// output as events so the run log records the poke's effect.
func runLoglevelCommand(command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), loglevelCommandTimeout)
	defer cancel()
	outb, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
	for line := range strings.SplitSeq(strings.TrimRight(string(outb), "\n"), "\n") {
		if line != "" {
			out.Event("  %s", line)
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s", loglevelCommandTimeout)
	}
	return err
}

// loglevelListing answers the loglevel-list verb with the config's levels.
func (w *Makedog) loglevelListing() ctrlResponse {
	if w.config.Loglevel == nil {
		return ctrlResponse{Error: "no [loglevel] section in this project's config"}
	}
	resp := w.controlStatus()
	resp.Levels = w.config.Loglevel.listing()
	return resp
}

// ---- client side: `makedog loglevel [level]` ----

// loglevelMain implements the loglevel verb: with a level, apply it to the
// live instance; bare or with --list, show what its config defines.
func loglevelMain(args []string) {
	level := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		level, args = args[0], args[1:]
	}
	fs := newVerbFlags("loglevel")
	list := fs.Bool("list", false, "list the levels the config defines")
	restart := fs.Bool("restart", false, "permit the restart an env level requires, without asking")
	jsonOut := fs.Bool("json", false, "emit the instance's response as JSON")
	fs.BoolVar(jsonOut, "jsonl", false, "emit the instance's response as JSON")
	instance := fs.Int("instance", 0, "target this makedog pid, when several are live")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, args)

	if *list || level == "" {
		listLoglevels(*dir, *binary, *jsonOut)
		return
	}

	inst := pickInstance(*dir, *binary, *instance, "loglevel")
	req := ctrlRequest{Cmd: "loglevel", Level: level, Restart: *restart}
	resp, err := controlCall(inst.Socket, req)
	if err != nil && resp.NeedsRestart {
		confirmRestart(level, err)
		req.Restart = true
		resp, err = controlCall(inst.Socket, req)
	}
	if err != nil {
		fatal("loglevel: %v", err)
	}
	if *jsonOut {
		b, _ := json.Marshal(resp)
		fmt.Println(string(b))
		return
	}
	if resp.Running {
		fmt.Printf("loglevel %s: run %d (child pid %d)\n", level, resp.Run, resp.ChildPid)
	} else {
		fmt.Printf("loglevel %s: stopped (last run %d)\n", level, resp.Run)
	}
}

// confirmRestart obtains consent for an env level's restart: a y/N prompt on
// a terminal, a --restart pointer otherwise, so scripts and agents fail fast
// rather than hang on a hidden question.
func confirmRestart(level string, cause error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fatal("%v; rerun with --restart", cause)
	}
	fmt.Printf("loglevel %s restarts the binary; proceed? [y/N] ", level)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return
	}
	fatal("loglevel %s declined", level)
}

// listLoglevels prints each live instance's configured levels.
func listLoglevels(dir, binary string, jsonOut bool) {
	instances, err := findInstances(dir, binary)
	if err != nil {
		fatal("%v", err)
	}
	if len(instances) == 0 {
		fatal("no live makedog instances")
	}
	enc := json.NewEncoder(os.Stdout)
	for _, inst := range instances {
		resp, err := controlCall(inst.Socket, ctrlRequest{Cmd: "loglevel-list"})
		if err != nil {
			if len(instances) == 1 && !jsonOut {
				fatal("loglevel: %v", err)
			}
			resp = ctrlResponse{Pid: inst.Pid, Binary: inst.Binary, Error: err.Error()}
		}
		if jsonOut {
			enc.Encode(resp)
			continue
		}
		if len(instances) > 1 {
			fmt.Printf("%d %s:\n", inst.Pid, inst.Binary)
		}
		if resp.Error != "" {
			fmt.Printf("  %s\n", resp.Error)
			continue
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "LEVEL\tVIA\tDETAIL")
		for _, row := range resp.Levels {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Name, row.Via, row.Detail)
		}
		tw.Flush()
	}
}

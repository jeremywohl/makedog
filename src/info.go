// Plumbing verbs that expose what run resolution already knows: `path`
// prints a run log's on-disk location (or the lineage directory) for
// composition with other tools, and `info` presents one run's metadata card —
// header, trailer, and file facts — without replaying its log.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

// splitRef peels an optional leading run reference off a verb's arguments.
func splitRef(args []string) (string, []string) {
	if len(args) > 0 && isRunRef(args[0]) {
		return args[0], args[1:]
	}
	return "", args
}

// lineageFlags declares the flags path and info share, returning the getters.
func lineageFlags(fs *flag.FlagSet) (binary, dir *string) {
	return fs.String("binary", "", "which binary's runs"),
		fs.String("C", "", "project directory (default: current)")
}

// pathMain implements `makedog path [ref]`: the run log's file path, or the
// lineage directory when no ref is given.
func pathMain(args []string) {
	ref, rest := splitRef(args)
	fs := newVerbFlags("path")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, rest)
	if fs.NArg() > 0 {
		fatal("path: bad run reference '%s' (a number, or latest[~N])", fs.Arg(0))
	}

	store, err := openLineage(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	if ref == "" {
		fmt.Println(store.dir)
		return
	}

	r, _ := parseRunRef(ref)
	number, err := store.resolveRef(r)
	if err != nil {
		fatal("%v", err)
	}
	path, _, err := store.runFilePath(number)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(path)
}

// runCard is one run's metadata, merged from its header and trailer records
// and the log file itself.
type runCard struct {
	Run        int       `json:"run"`
	Binary     string    `json:"binary"`
	Status     string    `json:"status"` // sealed | live | unclean
	ExitCode   *int      `json:"exit_code,omitempty"`
	Signal     string    `json:"signal,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Start      time.Time `json:"start,omitzero"`
	WallNs     int64     `json:"wall_ns,omitempty"`
	CPUNs      int64     `json:"cpu_ns,omitempty"`
	MaxRSS     int64     `json:"maxrss,omitempty"`
	Pid        int       `json:"pid,omitempty"`
	Hash       string    `json:"hash,omitempty"`
	GitBranch  string    `json:"git_branch,omitempty"`
	GitCommit  string    `json:"git_commit,omitempty"`
	Makedog    string    `json:"makedog,omitempty"`
	LogPath    string    `json:"log_path"`
	LogSize    int64     `json:"log_size"`
	Compressed bool      `json:"compressed"`
}

// exitLabel compresses a card's outcome to one word: the exit code,
// "sig NAME", or the bare status when the run never sealed.
func (c runCard) exitLabel() string {
	switch {
	case c.Signal != "":
		return "sig " + c.Signal
	case c.ExitCode != nil:
		return strconv.Itoa(*c.ExitCode)
	case c.Status != "":
		return c.Status
	}
	return "?"
}

// infoMain implements `makedog info [ref]`, defaulting to latest.
func infoMain(args []string) {
	ref, rest := splitRef(args)
	if ref == "" {
		ref = "latest"
	}
	fs := newVerbFlags("info")
	jsonOut := fs.Bool("json", false, "emit the card as one JSON object")
	fs.BoolVar(jsonOut, "jsonl", false, "emit the card as one JSON object")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, rest)
	if fs.NArg() > 0 {
		fatal("info: bad run reference '%s' (a number, or latest[~N])", fs.Arg(0))
	}

	store, err := openLineage(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	r, _ := parseRunRef(ref)
	number, err := store.resolveRef(r)
	if err != nil {
		fatal("%v", err)
	}

	card, err := buildRunCard(store, number)
	if err != nil {
		fatal("%v", err)
	}

	if *jsonOut {
		b, err := json.Marshal(card)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(string(b))
		return
	}
	renderRunCard(card)
}

// buildRunCard assembles a run's card from its header and trailer records
// plus the log file's own facts.
func buildRunCard(store *binaryStore, number int) (runCard, error) {
	card := runCard{Run: number, Binary: store.meta.Binary}

	path, compressed, err := store.runFilePath(number)
	if err != nil {
		return card, err
	}
	card.LogPath, card.Compressed = path, compressed
	if fi, err := os.Stat(path); err == nil {
		card.LogSize = fi.Size()
	}

	first, last, err := firstAndLastLines(store, number)
	if err != nil {
		return card, err
	}
	var meta, tail record
	json.Unmarshal([]byte(first), &meta)
	json.Unmarshal([]byte(last), &tail)

	if meta.T == recMeta {
		card.Start = meta.TS
		card.Pid = meta.Pid
		card.Hash = meta.Hash
		card.GitBranch, card.GitCommit = meta.GitBranch, meta.GitCommit
		card.Makedog = meta.Makedog
	}

	switch {
	case tail.T == recExit:
		card.Status = "sealed"
		card.ExitCode, card.Signal, card.Reason = tail.ExitCode, tail.Signal, tail.Reason
		card.WallNs, card.CPUNs, card.MaxRSS = tail.WallNs, tail.CPUNs, tail.MaxRSS
	case card.Pid != 0 && syscall.Kill(card.Pid, 0) == nil:
		card.Status = "live"
		if !card.Start.IsZero() {
			card.WallNs = time.Since(card.Start).Nanoseconds()
		}
	default:
		card.Status = "unclean"
	}
	return card, nil
}

// renderRunCard prints the human form: aligned keys, empty facts omitted.
func renderRunCard(c runCard) {
	row := func(key, format string, args ...any) {
		fmt.Printf("%-8s %s\n", key, fmt.Sprintf(format, args...))
	}

	row("run", "%d  %s", c.Run, c.Binary)

	status := c.Status
	switch {
	case c.Signal != "":
		status += " (killed by " + c.Signal + ")"
	case c.ExitCode != nil:
		status += " (exit " + strconv.Itoa(*c.ExitCode) + ")"
	}
	row("status", "%s", status)

	if !c.Start.IsZero() {
		row("start", "%s", c.Start.Format("2006-01-02 15:04:05"))
	}
	if c.WallNs > 0 {
		usage := "wall " + formatDuration(c.WallNs)
		if c.CPUNs > 0 {
			usage += "   cpu " + formatDuration(c.CPUNs)
		}
		if c.MaxRSS > 0 {
			usage += "   memory " + formatMemory(c.MaxRSS)
		}
		row("usage", "%s", usage)
	}
	if c.Reason != "" {
		row("reason", "%s", c.Reason)
	}
	if c.Pid != 0 {
		row("pid", "%d", c.Pid)
	}
	if c.Hash != "" {
		row("hash", "%s", c.Hash[:min(7, len(c.Hash))])
	}
	if git := gitLabel(c.GitBranch, c.GitCommit); git != "" {
		row("git", "%s", git)
	}
	if c.Makedog != "" {
		row("makedog", "%s", c.Makedog)
	}

	form := "plain"
	if c.Compressed {
		form = "compressed"
	}
	row("log", "%s (%s, %s)", c.LogPath, formatMemory(c.LogSize), form)
}

// Run log records: the JSONL schema shared by the write side (the run log
// sink) and the read side (show, runs). One record per line: "meta" opens a
// log, "exit" seals it, and child output plus lifecycle notes flow between.
// A log whose final record is not "exit" was truncated by an unclean death.
package main

import "time"

// Record types, in the T field.
const (
	recMeta    = "meta"    // header: run identity and provenance
	recLine    = "line"    // one line of child output, ANSI preserved
	recCommand = "command" // lifecycle commands: start, stop, make
	recEvent   = "event"   // notable events and state changes
	recError   = "error"   // makedog-reported errors
	recExit    = "exit"    // trailer: how the run ended
)

// record is one line of a run log. Fields beyond T/TS/S apply only to the
// record types noted; omitzero keeps each line to its own vocabulary.
type record struct {
	T  string    `json:"t"`
	TS time.Time `json:"ts,omitzero"`
	S  string    `json:"s,omitempty"`

	// meta
	Run       int    `json:"run,omitempty"`
	Binary    string `json:"binary,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Pid       int    `json:"pid,omitempty"`
	Hash      string `json:"hash,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`
	Makedog   string `json:"makedog,omitempty"`

	// exit
	ExitCode *int   `json:"exit_code,omitempty"`
	Signal   string `json:"signal,omitempty"`
	Reason   string `json:"reason,omitempty"`
	MaxRSS   int64  `json:"maxrss,omitempty"`
	CPUNs    int64  `json:"cpu_ns,omitempty"`
	WallNs   int64  `json:"wall_ns,omitempty"`
}

// Usage and help text formatting for makedog.
package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	boldStyle      = lipgloss.NewStyle().Bold(true)
	boldUnderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
)

// usage prints the help message.
func usage() {
	printStderr("A runner for server binaries: restart on builds, log every run, send signals, run make targets, and more\n\n")

	printStderr("%s\n", boldUnderStyle.Render("Usage:"))
	printStderr("  %s watch [OPTIONS] <binary-path>\n", boldStyle.Render("makedog"))
	printStderr("  %s <run#|latest[~N]> [OPTIONS]\n", boldStyle.Render("makedog"))
	printStderr("  %s runs|current|next|tail [OPTIONS]\n", boldStyle.Render("makedog"))
	printStderr("  %s search <regex> [OPTIONS]\n\n", boldStyle.Render("makedog"))

	printStderr("%s\n", boldUnderStyle.Render("Verbs:"))
	printOption("watch <binary-path>", "Supervise a binary (or script); a path argument implies watch")
	printOption("<run#>, latest[~N]", "Replay a recorded run (longhand: show <ref>)")
	printOption("runs", "List this directory's recorded runs; --long for full cards")
	printOption("current", "The run of the binary as built on disk, waiting for it if needed")
	printOption("next", "Wait for the next run to begin and stream it from its start")
	printOption("tail", "Follow live output across restarts, endlessly")
	printOption("search <regex>", "Grep recorded runs (alias: grep); exit 1 when nothing matches")
	printOption("info [ref]", "One run's metadata card, default latest; --json for one object")
	printOption("path [ref]", "A run log's file path; the lineage directory with no ref")
	printOption("diff [a [b]]", "Unified diff of two runs' output, default latest~1 vs latest")
	printOption("status", "List live makedog instances for the project")
	printOption("restart, stop, start", "Command a live instance's binary remotely")
	printOption("quit", "Stop the binary and exit the live instance remotely")
	printOption("signal <name>", "Send the binary a signal remotely, e.g. HUP or USR1")
	printOption("loglevel [level]", "Set the binary's log level per config; bare or --list shows levels")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Watch options:"))
	printOption("-c, --config <path>", "Path to specific configuration, rather than search for one")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Read options (show, current, next, tail):"))
	printOption("-f, --follow", "Keep streaming until the run exits (show, current)")
	printOption("-n <count>", "Only the last N records")
	printOption("--since <age>", "Only records within this age, like 30s or 5m (show, current)")
	printOption("--until <regex>", "Stream until a line matches: exit 0 matched, 1 run ended first")
	printOption("--timeout <dur>", "Give up after this duration (e.g. 30s), exit 2")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Runs options:"))
	printOption("--long", "Full metadata cards, as info shows")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Search options:"))
	printOption("--runs <a..b>", "Limit to a run number or range")
	printOption("--since <age>", "Only runs active within this age, like 2d or 6h")
	printOption("--all-binaries", "Search every binary recorded for the project")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Control options:"))
	printOption("--instance <pid>", "Target this makedog pid, when several are live")
	printOption("--restart", "Permit the restart an env-applied loglevel requires")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Shared options (all but watch):"))
	printOption("--json, --jsonl", "Raw JSONL records")
	printOption("--plain", "Strip ANSI styling")
	printOption("--binary <path>", "Which binary's runs, when several are recorded")
	printOption("-C <dir>", "Another project directory's runs")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Options:"))
	printOption("-h, --help", "Print help")
	printOption("--version", "Print version")
}

func printStderr(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func printOption(flags, description string) {
	styledFlags := boldStyle.Render(flags)
	padding := 25 - len(flags)
	if padding < 0 {
		padding = 0
	}
	printStderr("  %s%*s %s\n", styledFlags, padding, "", description)
}

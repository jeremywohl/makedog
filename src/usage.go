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
	printStderr("A development tool for server binaries: watch and restart, record and replay runs, run make targets, and more\n\n")

	printStderr("%s\n", boldUnderStyle.Render("Usage:"))
	printStderr("  %s watch [OPTIONS] <binary-path>\n", boldStyle.Render("makedog"))
	printStderr("  %s <run#|latest[~N]> [OPTIONS]\n", boldStyle.Render("makedog"))
	printStderr("  %s runs [OPTIONS]\n\n", boldStyle.Render("makedog"))

	printStderr("%s\n", boldUnderStyle.Render("Verbs:"))
	printOption("watch <binary-path>", "Supervise a binary (or script); a path argument implies watch")
	printOption("<run#>, latest[~N]", "Replay a recorded run (longhand: show <ref>)")
	printOption("runs", "List this directory's recorded runs")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Watch options:"))
	printOption("-c, --config <path>", "Path to specific configuration, rather than search for one")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Show options:"))
	printOption("-f, --follow", "Keep streaming until the run exits")
	printOption("-n <count>", "Only the last N records")
	printOption("--json", "Raw JSONL records")
	printOption("--plain", "Strip ANSI styling")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Show and runs options:"))
	printOption("--binary <path>", "Which binary's runs, when several are recorded")
	printOption("-C <dir>", "Another project directory's runs")
	printStderr("\n")

	printStderr("%s\n", boldUnderStyle.Render("Options:"))
	printOption("-h, --help", "Print help")
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

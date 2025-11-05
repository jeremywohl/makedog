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
	printStderr("A development tool for server binaries: watch and restart, log, run make targets, and more\n\n")

	printStderr("%s", boldUnderStyle.Render("Usage:"))
	printStderr(" %s [OPTIONS] <binary-path>\n\n", boldStyle.Render("makedog"))

	printStderr("%s\n", boldUnderStyle.Render("Arguments:"))
	printStderr("  <binary-path>  a binary (or script) to watch and restart\n\n")

	printStderr("%s\n", boldUnderStyle.Render("Options:"))
	printOption("-c, --config <path>", "Path to specific configuration, rather than search for one")
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

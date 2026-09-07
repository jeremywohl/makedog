// Usage and help text: the overview page for makedog itself, and one page
// per verb generated from its flag declarations plus a hand-written doc.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	boldStyle      = lipgloss.NewStyle().Bold(true)
	boldUnderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
)

const optionColumn = 25  // description column in Options and Verbs
const exampleColumn = 46 // note column in Examples

// usage prints the overview: verbs by job, the universal options, and the
// examples that cover the main workflows. Per-verb options live on the verb
// pages.
func usage() {
	printStderr("A runner for server binaries: restart on builds, log every run, send signals, run make targets, and more\n\n")

	printHeading("Usage:")
	printStderr("  %s [watch] [-c <config>] <binary-path>\n", boldStyle.Render("makedog"))
	printStderr("  %s <verb> [OPTIONS] [ARGS]\n", boldStyle.Render("makedog"))
	printStderr("  %s <verb> --help\n\n", boldStyle.Render("makedog"))

	printHeading("Watch:")
	printOption("watch <binary-path>", "Supervise a binary (or script); a path argument implies watch")
	printStderr("\n")

	printHeading("Read runs:")
	printOption("<run#>, latest[~N]", "Replay a recorded run (longhand: show <ref>)")
	printOption("runs", "List this directory's recorded runs")
	printOption("current", "The run of the binary as built on disk, waiting for it if needed")
	printOption("next", "Wait for the next run to begin and stream it from its start")
	printOption("tail", "Follow live output across restarts, endlessly")
	printOption("search <regex>", "Grep recorded runs (alias: grep)")
	printOption("info [ref]", "One run's metadata card")
	printOption("diff [a [b]]", "Unified diff of two runs' output")
	printOption("crash", "The runs behind a spin stop, back to back (alias: spin)")
	printOption("path [ref]", "A run log's file path")
	printStderr("\n")

	printHeading("Control a live instance:")
	printOption("status", "List live makedog instances for the project")
	printOption("restart, stop, start", "Command the binary")
	printOption("quit", "Stop the binary and exit makedog")
	printOption("signal <name>", "Send the binary a signal, e.g. HUP or USR1")
	printOption("loglevel [level]", "Set the binary's log level per config")
	printStderr("\n")

	printHeading("Options:")
	printOption("-C <dir>", "Another project directory (every verb but watch)")
	printOption("--binary <path>", "Which binary, when several are recorded (every verb but watch)")
	printOption("--json", "Machine-readable output (most verbs)")
	printOption("-h, --help", "A verb's full options and examples: makedog <verb> --help")
	printOption("--version", "Print version")
	printStderr("\n")

	printHeading("Examples:")
	printExamples([]example{
		{"makedog bin/server", "supervise; rebuilds restart it"},
		{"makedog latest~1 --plain", "the previous run, ANSI stripped"},
		{"make && makedog current --until 'listening' --timeout 30s", "block until the new build is up"},
		{"makedog search 'ERROR|panic' --since 2d", "grep the last two days of runs"},
		{"makedog diff", "what changed between the last two runs?"},
		{"makedog signal HUP", "from another terminal"},
		{"makedog loglevel debug --restart", "debug logging for one run"},
	})
}

// verbDoc is the hand-written half of a verb's help page; the Options
// section comes from the verb's flag declarations.
type verbDoc struct {
	summary  string    // one line, after "makedog <verb>:"
	usage    []string  // synopsis lines, without the leading "makedog"
	body     string    // a short paragraph, or empty
	exits    []string  // "0  meaning" rows, or nil for plain success/failure
	examples []example // at least one
}

type example struct{ cmd, note string }

const readExits = "0  --until matched, or the run was shown\n1  the run ended before --until matched\n2  --timeout elapsed, or no such run"

var instanceBody = "With several instances live, --instance <pid> picks one; status lists them."

// verbDocs is keyed by flag set name. The control verbs share flags but
// each get their own summary.
var verbDocs = map[string]verbDoc{
	"watch": {
		summary: "Supervise a binary (or script), restarting it when the file changes",
		usage:   []string{"watch [OPTIONS] <binary-path>", "[OPTIONS] <binary-path>"},
		body: "Restarts the binary when its modification time changes, records every " +
			"run to the store, and takes single keys: r restart, x stop/start, " +
			"s signal menu, t make targets, m make, l log level, k mark the log, " +
			"c clear screen, h key help, q quit. A path argument implies watch; a " +
			"bare name is a verb, so give a binary named like one as ./name.",
		examples: []example{
			{"makedog bin/server", ""},
			{"makedog -c deploy/makedog.toml bin/server", "a config elsewhere than .makedog.toml"},
			{"makedog ./status", "a binary whose name is a verb"},
		},
	},
	"show": {
		summary: "Replay a recorded run, live or sealed",
		usage:   []string{"<run#> [OPTIONS]", "latest[~N] [OPTIONS]", "show <ref> [OPTIONS]"},
		body: "latest~1 is the run before latest, and so on. A snapshot pages on a " +
			"terminal and streams raw when piped; it returns at once, so tool callers " +
			"never hang unless they ask to --follow.",
		exits: strings.Split(readExits, "\n"),
		examples: []example{
			{"makedog latest", "the latest run, live or not"},
			{"makedog latest~1 --plain", "the previous run, ANSI stripped"},
			{"makedog 42 -n 100", "the last 100 records of run 42"},
			{"makedog latest -f --until 'ready' --timeout 30s", ""},
			{"makedog latest --json | jq -r 'select(.t==\"line\").s'", ""},
		},
	},
	"current": {
		summary: "The run of the binary as built on disk, waiting for it if needed",
		usage:   []string{"current [OPTIONS]"},
		body: "Keys on the binary's hash: returns at once when that build's run is " +
			"already recorded, live or exited, and waits for it to start otherwise. " +
			"Safe to call at any point after a build, so make && makedog current " +
			"never races the restart.",
		exits: strings.Split(readExits, "\n"),
		examples: []example{
			{"make && makedog current", "output so far, waiting for the restart"},
			{"make && makedog current --until 'listening on' --timeout 30s", ""},
			{"makedog current -f --json | jq -r 'select(.t==\"line\").s'", ""},
		},
	},
	"next": {
		summary: "Wait for the next run to begin and stream it from its start",
		usage:   []string{"next [OPTIONS]"},
		body: "The pure event form: whatever run starts next, from whatever build. " +
			"After a rebuild prefer current, which cannot miss a restart that already " +
			"happened.",
		exits: []string{
			"0  the run ended, or --until matched",
			"1  the run ended before --until matched",
			"2  --timeout elapsed",
		},
		examples: []example{
			{"makedog next", "catch the coming run from its first line"},
			{"makedog next --until 'listening on' --timeout 30s", ""},
		},
	},
	"tail": {
		summary: "Follow live output across restarts, endlessly",
		usage:   []string{"tail [OPTIONS]"},
		body: "Starts with the last N records of the current run, then rolls into " +
			"each successor from its first line. Ends only on an --until match, a " +
			"--timeout, or an interrupt.",
		exits: []string{
			"0  --until matched",
			"2  --timeout elapsed",
		},
		examples: []example{
			{"makedog tail", ""},
			{"makedog tail -n 0 --plain | tee session.log", "new output only, unstyled"},
		},
	},
	"runs": {
		summary: "List this directory's recorded runs",
		usage:   []string{"runs [OPTIONS]"},
		examples: []example{
			{"makedog runs", ""},
			{"makedog runs --long", "full metadata cards, as info shows"},
			{"makedog runs --json | jq .run", ""},
		},
	},
	"search": {
		summary: "Grep recorded runs for a regex (alias: grep)",
		usage:   []string{"search <regex> [OPTIONS]"},
		body: "Scans every recorded run of the binary, oldest first, printing hits " +
			"with their run number. Lifecycle records match too: commands, events, " +
			"and errors, marked as the replay marks them.",
		exits: []string{
			"0  something matched",
			"1  nothing matched",
		},
		examples: []example{
			{"makedog search 'ERROR|panic' --since 2d", ""},
			{"makedog search 'GET /api' --runs 30..34", ""},
			{"makedog grep panic --all-binaries --json", "every binary of the project"},
		},
	},
	"info": {
		summary: "One run's metadata card",
		usage:   []string{"info [ref] [OPTIONS]"},
		body: "Pid, binary hash, git state, start and end times, exit status, and " +
			"whether the run is live. Default latest.",
		examples: []example{
			{"makedog info", ""},
			{"makedog info latest~1", ""},
			{"makedog info --json | jq .hash", ""},
		},
	},
	"path": {
		summary: "A run log's file path",
		usage:   []string{"path [ref] [OPTIONS]"},
		body:    "With no ref, the lineage directory holding every run of the binary.",
		examples: []example{
			{"makedog path latest", ""},
			{"ls -l $(makedog path)", "the whole lineage"},
		},
	},
	"diff": {
		summary: "Unified diff of two runs' output",
		usage:   []string{"diff [a [b]] [OPTIONS]"},
		body: "Default latest~1 vs latest: what changed since the last run? Only " +
			"the binary's own output lines are compared, ANSI stripped.",
		exits: []string{
			"0  identical",
			"1  differ",
			"2  trouble, like a bad reference",
		},
		examples: []example{
			{"makedog diff", ""},
			{"makedog diff 40 42", ""},
			{"makedog diff --plain | less", ""},
		},
	},
	"crash": {
		summary: "The runs that tripped the spin stop, back to back (alias: spin)",
		usage:   []string{"crash [OPTIONS]", "spin [OPTIONS]"},
		body: fmt.Sprintf("Watch pauses instead of restarting once the binary exits on its own "+
			"%d times within %s. crash replays that rule over the recorded runs and prints "+
			"the ones that tripped it, oldest first, each under a header that leads with "+
			"its exit status. A live newest run is skipped, so it still answers after the "+
			"restart. With no spin in recent history it prints the last %d runs and says so.",
			spinMinExits, spinWindow, spinMinExits),
		exits: []string{
			"0  a spin was found",
			"1  no spin; the last runs were shown instead",
		},
		examples: []example{
			{"makedog crash", ""},
			{"makedog crash -n 20 --plain", "the tail of each run, unstyled"},
			{"makedog crash --json | jq -c 'select(.t==\"exit\")'", "just the trailers"},
		},
	},
	"status": {
		summary: "List live makedog instances for the project",
		usage:   []string{"status [OPTIONS]"},
		body: "Each watching instance registers on a unix socket; status finds those " +
			"for this project's binaries.",
		examples: []example{
			{"makedog status", ""},
			{"makedog status --json", "one object per instance"},
		},
	},
	"restart": {
		summary: "Restart a live instance's binary remotely",
		usage:   []string{"restart [OPTIONS]"},
		body:    instanceBody,
		examples: []example{
			{"makedog restart", ""},
			{"makedog restart --instance 30934", ""},
		},
	},
	"stop": {
		summary: "Stop a live instance's binary remotely; makedog keeps watching",
		usage:   []string{"stop [OPTIONS]"},
		body:    instanceBody,
		examples: []example{
			{"makedog stop", ""},
			{"makedog -C ../api stop", "another project directory"},
		},
	},
	"start": {
		summary: "Start a stopped binary under a live instance",
		usage:   []string{"start [OPTIONS]"},
		body:    instanceBody,
		examples: []example{
			{"makedog start", ""},
			{"makedog start --json", ""},
		},
	},
	"quit": {
		summary: "Stop the binary and exit the live makedog instance",
		usage:   []string{"quit [OPTIONS]"},
		body:    instanceBody,
		examples: []example{
			{"makedog quit", ""},
			{"makedog quit --instance 30934", ""},
		},
	},
	"signal": {
		summary: "Send the binary a signal remotely",
		usage:   []string{"signal <name> [OPTIONS]"},
		body: "Any signal makedog knows, with or without the SIG prefix; the " +
			"interactive menu's config scoping does not apply. It delivers whatever " +
			"the app wired the signal to, so know the handler before sending. " +
			instanceBody,
		examples: []example{
			{"makedog signal HUP", "reload config, typically"},
			{"makedog signal USR1 --instance 30934", ""},
		},
	},
	"loglevel": {
		summary: "Set the binary's log level, per the project's [loglevel] config",
		usage:   []string{"loglevel <level> [OPTIONS]", "loglevel [--list]"},
		body: "Levels are whatever the config defines. An env level restarts the " +
			"binary with the assignment in its environment, asking first unless " +
			"--restart; a signal or command level pokes the live run. Every change " +
			"is temporary: the next restart reverts to baseline. " + instanceBody,
		examples: []example{
			{"makedog loglevel --list", "what this project defines"},
			{"makedog loglevel debug --restart", "one run at debug, no prompt"},
			{"makedog loglevel mute", ""},
		},
	},
}

// verbUsage prints one verb's help page: its doc, then the options it
// declared, in declaration order.
func verbUsage(fs *verbFlags) {
	name := fs.Name()
	doc, ok := verbDocs[name]
	if !ok {
		usage()
		return
	}
	printStderr("%s: %s\n\n", boldStyle.Render("makedog "+name), doc.summary)

	printHeading("Usage:")
	for _, u := range doc.usage {
		printStderr("  %s %s\n", boldStyle.Render("makedog"), u)
	}
	printStderr("\n")

	if doc.body != "" {
		printStderr("%s\n\n", wrap(doc.body, 78))
	}

	if len(fs.rows) > 0 {
		printHeading("Options:")
		for _, r := range fs.rows {
			flags := strings.Join(r.names, ", ")
			if r.arg != "" {
				flags += " " + r.arg
			}
			printOption(flags, r.help)
		}
		printStderr("\n")
	}

	if len(doc.exits) > 0 {
		printHeading("Exit status:")
		for _, e := range doc.exits {
			printStderr("  %s\n", e)
		}
		printStderr("\n")
	}

	printHeading("Examples:")
	printExamples(doc.examples)
}

func printStderr(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func printHeading(text string) {
	printStderr("%s\n", boldUnderStyle.Render(text))
}

func printOption(flags, description string) {
	styledFlags := boldStyle.Render(flags)
	padding := optionColumn - len(flags)
	if padding < 0 {
		padding = 0
	}
	printStderr("  %s%*s %s\n", styledFlags, padding, "", description)
}

// printExamples lists commands with their notes aligned in a column; a
// command too wide for the column pushes its note to the next line.
func printExamples(examples []example) {
	for _, e := range examples {
		switch {
		case e.note == "":
			printStderr("  %s\n", e.cmd)
		case len(e.cmd) < exampleColumn-1:
			printStderr("  %-*s %s\n", exampleColumn-1, e.cmd, e.note)
		default:
			printStderr("  %s\n  %*s %s\n", e.cmd, exampleColumn-1, "", e.note)
		}
	}
}

// wrap folds text at spaces to fit width.
func wrap(text string, width int) string {
	var b strings.Builder
	col := 0
	for _, word := range strings.Fields(text) {
		if col > 0 && col+1+len(word) > width {
			b.WriteByte('\n')
			col = 0
		} else if col > 0 {
			b.WriteByte(' ')
			col++
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String()
}

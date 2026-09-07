// Shared verb flag parsing: quiet flag sets that record their declarations
// for the verb's help page, and re-present failures in the project's
// double-hyphen idiom rather than the library's single-hyphen usage dump.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

// verbFlags is a verb's flag set. Each declaration records its aliases,
// value placeholder, and help in order, so the help page is generated from
// the same declarations that parse the command line and cannot drift.
type verbFlags struct {
	*flag.FlagSet
	rows []flagRow
}

// flagRow is one line of a help page's Options section.
type flagRow struct {
	names []string // as spelled at the command line: -f, --follow
	arg   string   // value placeholder, like <count>; empty for booleans
	help  string
}

// newVerbFlags returns a flag set that stays silent on error and help,
// leaving all presentation to parseVerbFlags.
func newVerbFlags(name string) *verbFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &verbFlags{FlagSet: fs}
}

// dash spells a flag name as the help does: one hyphen for a single letter,
// two otherwise.
func dash(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

func (v *verbFlags) record(arg, help string, names []string) {
	spelled := make([]string, len(names))
	for i, n := range names {
		spelled[i] = dash(n)
	}
	v.rows = append(v.rows, flagRow{spelled, arg, help})
}

// BoolVar declares a boolean flag under each of names, keeping *p as the
// default.
func (v *verbFlags) BoolVar(p *bool, help string, names ...string) {
	for _, n := range names {
		v.FlagSet.BoolVar(p, n, *p, help)
	}
	v.record("", help, names)
}

// Bool declares a boolean flag under each of names, default false.
func (v *verbFlags) Bool(help string, names ...string) *bool {
	p := new(bool)
	v.BoolVar(p, help, names...)
	return p
}

// StringVar declares a string flag under each of names, keeping *p as the
// default; arg names the value in help.
func (v *verbFlags) StringVar(p *string, arg, help string, names ...string) {
	for _, n := range names {
		v.FlagSet.StringVar(p, n, *p, help)
	}
	v.record(arg, help, names)
}

// String declares a string flag under each of names, default empty.
func (v *verbFlags) String(arg, help string, names ...string) *string {
	p := new(string)
	v.StringVar(p, arg, help, names...)
	return p
}

// IntVar declares an integer flag under each of names, keeping *p as the
// default.
func (v *verbFlags) IntVar(p *int, arg, help string, names ...string) {
	for _, n := range names {
		v.FlagSet.IntVar(p, n, *p, help)
	}
	v.record(arg, help, names)
}

// Int declares an integer flag under each of names with the given default.
func (v *verbFlags) Int(def int, arg, help string, names ...string) *int {
	p := new(int)
	*p = def
	v.IntVar(p, arg, help, names...)
	return p
}

// Duration declares a duration flag under each of names, default zero.
func (v *verbFlags) Duration(arg, help string, names ...string) *time.Duration {
	p := new(time.Duration)
	for _, n := range names {
		v.FlagSet.DurationVar(p, n, 0, help)
	}
	v.record(arg, help, names)
	return p
}

// flagToken matches a flag name inside package flag's error messages,
// anchored to its phrasings ("not defined: -x", "for flag -x", "for -x") so
// flag-like values in the message are left alone. Only multi-letter names
// match; single-letter flags keep their single hyphen, as the help presents
// them.
var flagToken = regexp.MustCompile(`(flag |for |: )-([\w-]{2,})`)

// parseVerbFlags parses args, exiting on failure with a double-hyphen
// rendering of the error, or with the verb's help page for -h/--help.
func parseVerbFlags(fs *verbFlags, args []string) {
	err := fs.Parse(args)
	if err == nil {
		return
	}
	if err == flag.ErrHelp {
		verbUsage(fs)
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "makedog: %s: %s (try makedog %s --help)\n",
		fs.Name(), flagToken.ReplaceAllString(err.Error(), "$1--$2"), fs.Name())
	os.Exit(2)
}

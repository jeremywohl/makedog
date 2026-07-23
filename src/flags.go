// Shared verb flag parsing: quiet flag sets whose failures are re-presented
// in the project's double-hyphen idiom, rather than the library's
// single-hyphen usage dump.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
)

// newVerbFlags returns a flag set that stays silent on error and help,
// leaving all presentation to parseVerbFlags.
func newVerbFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// flagToken matches a flag name inside package flag's error messages,
// anchored to its phrasings ("not defined: -x", "for flag -x", "for -x") so
// flag-like values in the message are left alone. Only multi-letter names
// match; single-letter flags keep their single hyphen, as the help presents
// them.
var flagToken = regexp.MustCompile(`(flag |for |: )-([\w-]{2,})`)

// parseVerbFlags parses args, exiting on failure with a double-hyphen
// rendering of the error, or with the full help for an undefined -h/--help.
func parseVerbFlags(fs *flag.FlagSet, args []string) {
	err := fs.Parse(args)
	if err == nil {
		return
	}
	if err == flag.ErrHelp {
		usage()
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "makedog: %s: %s (try --help)\n",
		fs.Name(), flagToken.ReplaceAllString(err.Error(), "$1--$2"))
	os.Exit(2)
}

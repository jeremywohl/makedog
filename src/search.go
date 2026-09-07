// The search verb: grep across recorded runs. Scopes by lineage (or every
// binary in the project), run range, and age; matches record payloads with
// ANSI stripped, plain and compressed runs alike. Hits print as
// [binary:]run:timestamp  text, so any hit is addressable by the other read
// verbs. Grep's exit convention: 0 on any hit, 1 on none.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// searchScope narrows which runs a search visits.
type searchScope struct {
	runs   runRange
	oldest time.Time // zero means unbounded
}

// runRange is an inclusive run number interval; zero means unbounded.
type runRange struct {
	lo, hi int
}

// parseRunRange reads "135" or "130..135".
func parseRunRange(s string) (runRange, error) {
	lo, hi := s, s
	if a, b, ok := strings.Cut(s, ".."); ok {
		lo, hi = a, b
	}
	l, err1 := strconv.Atoi(lo)
	h, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || l < 1 || h < l {
		return runRange{}, fmt.Errorf("expected a run number or range like 130..135")
	}
	return runRange{l, h}, nil
}

func (r runRange) contains(n int) bool {
	return r.lo == 0 || (n >= r.lo && n <= r.hi)
}

// searchMain implements `makedog search <pattern>` (alias: grep).
func searchMain(args []string) {
	pattern := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pattern, args = args[0], args[1:]
	}

	fs := newVerbFlags("search")
	runsFlag := fs.String("<a..b>", "Limit to a run number or range, like 130..135", "runs")
	since := fs.String("<age>", "Only runs active within this age, like 2d or 6h", "since")
	allBinaries := fs.Bool("Search every binary recorded for the project", "all-binaries")
	jsonOut := fs.Bool("Matching records as JSONL, with run and binary set", "json", "jsonl")
	plain := fs.Bool("Strip the binary's ANSI escapes from hits", "plain")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, args)

	if pattern == "" {
		fatal("search: what pattern?")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		fatal("bad pattern: %v", err)
	}

	scope := searchScope{}
	if *runsFlag != "" {
		if scope.runs, err = parseRunRange(*runsFlag); err != nil {
			fatal("bad --runs: %v", err)
		}
	}
	if *since != "" {
		age, err := parseAge(*since)
		if err != nil {
			fatal("bad --since: %v", err)
		}
		scope.oldest = time.Now().Add(-age)
	}

	var stores []*binaryStore
	if *allBinaries {
		if stores, err = projectLineages(*dir); err != nil {
			fatal("%v", err)
		}
	} else {
		s, err := openLineage(*dir, *binary)
		if err != nil {
			fatal("%v", err)
		}
		stores = []*binaryStore{s}
	}

	matched := false
	for _, s := range stores {
		infos, err := s.runInfos()
		if err != nil {
			continue
		}
		for _, info := range infos {
			if !scope.runs.contains(info.number) {
				continue
			}
			if !scope.oldest.IsZero() && info.mtime.Before(scope.oldest) {
				continue
			}
			if searchRun(s, info.number, re, *jsonOut, *plain, *allBinaries) {
				matched = true
			}
		}
	}
	if !matched {
		os.Exit(1)
	}
}

// hit sigils identify non-output records among hits, echoing the renderer.
var hitSigils = map[string]string{recCommand: "--> ", recEvent: "* ", recError: "! "}

// searchRun scans one run's records, printing hits; reports whether any matched.
func searchRun(s *binaryStore, number int, re *regexp.Regexp, jsonOut, plain, showBinary bool) bool {
	f, _, err := s.openRun(number)
	if err != nil {
		return false
	}
	defer f.Close()

	matched := false
	tail := newRecordReader(f)
	for {
		line, err := tail.next()
		if err != nil {
			return matched
		}
		if line.rec.S == "" || !re.MatchString(ansi.Strip(line.rec.S)) {
			continue
		}
		matched = true

		if jsonOut {
			rec := line.rec
			rec.Run, rec.Binary = number, s.meta.Binary
			if b, err := json.Marshal(rec); err == nil {
				fmt.Println(string(b))
			}
			continue
		}

		prefix := ""
		if showBinary {
			prefix = s.meta.Binary + ":"
		}
		text := line.rec.S
		if plain {
			text = ansi.Strip(text)
		}
		fmt.Printf("%s%d:%s  %s%s\n",
			prefix, number, line.rec.TS.Format("2006-01-02 15:04:05"), hitSigils[line.rec.T], text)
	}
}

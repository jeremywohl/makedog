// The crash verb: the runs that tripped the watch loop's spin detector, back
// to back, so a crash loop reads as one page rather than three replays. The
// pause itself is announced after the last run's log is sealed and so is
// never recorded; instead the detector's rule is replayed over the run
// trailers, with the constants it shares. When recent history holds no spin,
// the last few runs print instead and the exit status says so.
package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"time"
)

// crashMain implements `makedog crash` (alias: spin). Exit 0 when a spin
// was found, 1 when it fell back to the last runs.
func crashMain(args []string) {
	opts := showOptions{}
	fs := newVerbFlags("crash")
	fs.IntVar(&opts.lastN, "<count>", "Only the last N records of each run", "n")
	fs.BoolVar(&opts.json, "Raw JSONL records, no headers", "json", "jsonl")
	fs.BoolVar(&opts.plain, "Strip ANSI styling", "plain")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, args)

	store, err := openLineage(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	numbers, err := store.runNumbers()
	if err != nil || len(numbers) == 0 {
		fatal("no runs recorded for %s", store.meta.Binary)
	}

	all := make([]runSummary, len(numbers))
	for i, n := range numbers {
		all[i] = summarizeRun(store, n)
	}
	picked, spun := spinRuns(all)

	err = page(func(w io.Writer) {
		if !opts.json {
			var note string
			if spun {
				note = fmt.Sprintf("spin: %d runs exited within %s of each other; makedog paused",
					len(picked), spinWindow)
			} else {
				note = fmt.Sprintf("no spin in recent runs; showing the last %d", len(picked))
			}
			fmt.Fprintf(w, "%s\n", restyle(eventStyle, note, opts))
		}
		for _, s := range picked {
			if err := renderCrashRun(w, store, s, opts); err != nil {
				fmt.Fprintf(os.Stderr, "makedog: run %d: %v\n", s.Run, err)
			}
		}
	})
	if err != nil {
		fatal("%v", err)
	}
	if !spun {
		os.Exit(1)
	}
}

// renderCrashRun prints one run under a header that leads with its exit —
// the column a crash loop is read by — then its records per opts. In JSON
// mode the meta and exit records already delimit runs, so no header.
func renderCrashRun(w io.Writer, store *binaryStore, s runSummary, opts showOptions) error {
	f, _, err := store.openRun(s.Run)
	if err != nil {
		return err
	}
	defer f.Close()

	if !opts.json {
		header := fmt.Sprintf("=== run %d  exit %s", s.Run, s.Exit)
		if s.WallNs > 0 {
			header += "  " + formatDuration(s.WallNs)
		}
		if !s.Start.IsZero() {
			header += "  " + s.Start.Format("2006-01-02 15:04:05")
		}
		if s.Git != "" {
			header += "  " + s.Git
		}
		fmt.Fprintf(w, "\n%s\n", restyle(commandStyle, header, opts))
	}
	for _, l := range visibleBacklog(readBacklog(newRecordReader(f)), opts) {
		renderLine(w, l, opts)
	}
	return nil
}

// spinRuns picks the runs that tripped the spin detector, oldest first, by
// replaying its rule over the summaries: self-exits shorter than spinSettled,
// counted back from the newest sealed run, of which at least spinMinExits
// ended within spinWindow of the last. A live newest run is skipped, so the
// verb still answers after the restart that follows a pause; a stopped or
// unclean run ends the streak, as those never reach the detector. Without a
// spin, the last spinMinExits runs return with false.
func spinRuns(all []runSummary) ([]runSummary, bool) {
	end := len(all)
	if end > 0 && all[end-1].Exit == "live" {
		end--
	}

	var streak []runSummary // newest first
	for i := end - 1; i >= 0 && len(streak) < spinMemory; i-- {
		if !all[i].selfExited() {
			break
		}
		streak = append(streak, all[i])
	}
	if len(streak) >= spinMinExits {
		threshold := streak[0].exitTime().Add(-spinWindow)
		n := 0
		for n < len(streak) && streak[n].exitTime().After(threshold) {
			n++
		}
		if n >= spinMinExits {
			picked := streak[:n]
			slices.Reverse(picked)
			return picked, true
		}
	}

	lo := max(len(all)-spinMinExits, 0)
	return all[lo:], false
}

// selfExited reports whether the run ended on its own, quickly enough to
// count toward a spin: sealed, with no stop reason, in under spinSettled.
func (s runSummary) selfExited() bool {
	switch s.Exit {
	case "live", "unclean", "?":
		return false
	}
	return s.Reason == "" && !s.Start.IsZero() && time.Duration(s.WallNs) < spinSettled
}

func (s runSummary) exitTime() time.Time {
	return s.Start.Add(time.Duration(s.WallNs))
}

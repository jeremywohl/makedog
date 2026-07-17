// Read-side verbs: replay a recorded run (optionally following it live),
// wait on the next run, tail a lineage across restarts, and list run history.
// Rendering mirrors the terminal sink, so a replayed log reads like the
// original session. Snapshot output pages on a terminal and streams raw when
// piped; only the explicitly blocking forms (--follow, --until, next, tail)
// keep the process alive, so tool callers never hang by default. Blocking
// forms exit 0 on success or an --until match, 1 when the run ends before a
// match, and 2 on --timeout.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// runRef identifies a recorded run: an absolute number, or a position counted
// back from the most recent ("latest", "latest~2").
type runRef struct {
	latest bool
	back   int
	number int
}

// parseRunRef reports whether s names a run, and which.
func parseRunRef(s string) (runRef, bool) {
	if s == "latest" {
		return runRef{latest: true}, true
	}
	if rest, ok := strings.CutPrefix(s, "latest~"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil || n < 1 {
			return runRef{}, false
		}
		return runRef{latest: true, back: n}, true
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return runRef{number: n}, true
	}
	return runRef{}, false
}

func isRunRef(s string) bool {
	_, ok := parseRunRef(s)
	return ok
}

// showOptions carries the read verbs' rendering and lifetime choices.
type showOptions struct {
	follow   bool
	lastN    int
	json     bool
	plain    bool
	since    time.Time      // render only records from this instant on; zero means all
	until    *regexp.Regexp // stop following on a matching payload; implies follow
	deadline time.Time      // stop following at this instant; zero means never
}

// matched reports whether a record's payload satisfies --until.
func (o *showOptions) matched(l logLine) bool {
	return o.until != nil && l.rec.S != "" && o.until.MatchString(ansi.Strip(l.rec.S))
}

// expired reports whether --timeout has elapsed.
func (o *showOptions) expired() bool {
	return !o.deadline.IsZero() && time.Now().After(o.deadline)
}

// readFlags declares the flags every read verb shares; verbs add their own to
// fs before parse.
type readFlags struct {
	fs      *flag.FlagSet
	until   *string
	timeout *time.Duration
	binary  *string
	dir     *string
}

func newReadFlags(name string, opts *showOptions) *readFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.BoolVar(&opts.json, "json", false, "emit raw JSONL records")
	fs.BoolVar(&opts.plain, "plain", false, "strip ANSI styling")
	return &readFlags{
		fs:      fs,
		until:   fs.String("until", "", "stream until a line matches this regex"),
		timeout: fs.Duration("timeout", 0, "give up after this duration (exit 2)"),
		binary:  fs.String("binary", "", "which binary's runs"),
		dir:     fs.String("C", "", "project directory (default: current)"),
	}
}

// parse finalizes the shared flags into opts and resolves the target lineage.
func (r *readFlags) parse(args []string, opts *showOptions) *binaryStore {
	r.fs.Parse(args)

	if *r.until != "" {
		re, err := regexp.Compile(*r.until)
		if err != nil {
			fatal("bad --until pattern: %v", err)
		}
		opts.until = re
		opts.follow = true
	}
	if *r.timeout > 0 {
		opts.deadline = time.Now().Add(*r.timeout)
	}

	store, err := openLineage(*r.dir, *r.binary)
	if err != nil {
		fatal("%v", err)
	}
	return store
}

// showMain implements `makedog <ref>` and `makedog show <ref>`.
func showMain(refArg string, args []string) {
	opts := showOptions{}
	rf := newReadFlags("show", &opts)
	rf.fs.BoolVar(&opts.follow, "f", false, "follow output as it arrives")
	rf.fs.BoolVar(&opts.follow, "follow", false, "follow output as it arrives")
	rf.fs.IntVar(&opts.lastN, "n", 0, "only the last N records")
	since := rf.fs.String("since", "", "only records within this age, like 30s or 5m")

	ref, ok := parseRunRef(refArg)
	if !ok {
		fatal("bad run reference '%s' (a number, or latest[~N])", refArg)
	}

	store := rf.parse(args, &opts)
	if *since != "" {
		age, err := parseAge(*since)
		if err != nil {
			fatal("bad --since: %v", err)
		}
		opts.since = time.Now().Add(-age)
	}
	number, err := store.resolveRef(ref)
	if err != nil {
		fatal("%v", err)
	}

	status, err := showRun(store, number, opts)
	if err != nil {
		fatal("%v", err)
	}
	if status != 0 {
		os.Exit(status)
	}
}

// nextMain implements `makedog next`: wait for a run newer than the current
// latest, stream it from the start, and end by seal, match, or timeout.
func nextMain(args []string) {
	opts := showOptions{follow: true}
	rf := newReadFlags("next", &opts)
	store := rf.parse(args, &opts)

	baseline := 0
	if numbers, err := store.runNumbers(); err == nil && len(numbers) > 0 {
		baseline = numbers[len(numbers)-1]
	}

	fmt.Fprintf(os.Stderr, "makedog: waiting for the next run of %s\n", store.meta.Binary)
	number, ok := waitForRunAfter(store, baseline, &opts)
	if !ok {
		os.Exit(2)
	}

	status, err := showRun(store, number, opts)
	if err != nil {
		fatal("%v", err)
	}
	if status != 0 {
		os.Exit(status)
	}
}

// tailMain implements `makedog tail`: follow the lineage across restarts,
// rolling from each run into its successor. Endless by design, short of an
// --until match, a --timeout, or an interrupt.
func tailMain(args []string) {
	opts := showOptions{follow: true}
	rf := newReadFlags("tail", &opts)
	lastN := rf.fs.Int("n", 10, "initial backlog from the current run")
	store := rf.parse(args, &opts)

	current := 0
	if numbers, err := store.runNumbers(); err == nil && len(numbers) > 0 {
		current = numbers[len(numbers)-1]
	}
	if current == 0 {
		fmt.Fprintf(os.Stderr, "makedog: waiting for a run of %s\n", store.meta.Binary)
		n, ok := waitForRunAfter(store, 0, &opts)
		if !ok {
			os.Exit(2)
		}
		current = n
	}

	firstAttach := true
	for {
		runOpts := opts
		if firstAttach {
			runOpts.lastN = *lastN // successors stream whole from their start
		}
		firstAttach = false

		outcome, err := streamRun(store, current, runOpts)
		if err != nil {
			fatal("%v", err)
		}
		switch outcome {
		case followMatched:
			os.Exit(0)
		case followTimedOut:
			os.Exit(2)
		}

		// Sealed or writer gone: roll into the next run once it begins.
		n, ok := waitForRunAfter(store, current, &opts)
		if !ok {
			os.Exit(2)
		}
		current = n
	}
}

// waitForRunAfter polls for a run numbered past baseline, honoring the
// deadline. Returns the earliest such run, in case several appeared.
func waitForRunAfter(store *binaryStore, baseline int, opts *showOptions) (int, bool) {
	for {
		if numbers, err := store.runNumbers(); err == nil {
			for _, n := range numbers {
				if n > baseline {
					return n, true
				}
			}
		}
		if opts.expired() {
			fmt.Fprintln(os.Stderr, "makedog: timeout")
			return 0, false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// resolveRef maps a runRef onto this lineage's recorded run numbers.
func (s *binaryStore) resolveRef(ref runRef) (int, error) {
	numbers, err := s.runNumbers()
	if err != nil || len(numbers) == 0 {
		return 0, fmt.Errorf("no runs recorded for %s", s.meta.Binary)
	}
	if ref.latest {
		i := len(numbers) - 1 - ref.back
		if i < 0 {
			return 0, fmt.Errorf("only %d runs recorded for %s", len(numbers), s.meta.Binary)
		}
		return numbers[i], nil
	}
	if !slices.Contains(numbers, ref.number) {
		return 0, fmt.Errorf("run %d not found for %s (have %d..%d)",
			ref.number, s.meta.Binary, numbers[0], numbers[len(numbers)-1])
	}
	return ref.number, nil
}

// logLine pairs a log's raw line with its parsed record.
type logLine struct {
	raw string
	rec record
}

// recordReader yields complete lines from a possibly still-growing file,
// holding a partial tail line until its newline arrives.
type recordReader struct {
	r       *bufio.Reader
	pending []byte
}

func newRecordReader(r io.Reader) *recordReader {
	return &recordReader{r: bufio.NewReader(r)}
}

// next returns the next complete line, or io.EOF when no full line is
// available yet.
func (t *recordReader) next() (logLine, error) {
	chunk, err := t.r.ReadBytes('\n')
	t.pending = append(t.pending, chunk...)
	if err != nil {
		return logLine{}, err
	}
	raw := strings.TrimRight(string(t.pending), "\n")
	t.pending = t.pending[:0]
	var rec record
	json.Unmarshal([]byte(raw), &rec) // a garbled line renders as an unknown record
	return logLine{raw: raw, rec: rec}, nil
}

// followOutcome says how a followed run ended.
type followOutcome int

const (
	followSealed     followOutcome = iota // the exit record arrived
	followMatched                         // an --until pattern matched
	followTimedOut                        // the --timeout deadline passed
	followWriterGone                      // no trailer, and the writer has died
)

// readBacklog consumes the records already present in the log.
func readBacklog(tail *recordReader) []logLine {
	var all []logLine
	for {
		line, err := tail.next()
		if err != nil {
			return all
		}
		all = append(all, line)
	}
}

func trimBacklog(all []logLine, n int) []logLine {
	if n > 0 && len(all) > n {
		return all[len(all)-n:]
	}
	return all
}

// visibleBacklog applies the --since window, then the -n cap.
func visibleBacklog(all []logLine, opts showOptions) []logLine {
	if !opts.since.IsZero() {
		kept := all[:0:0]
		for _, l := range all {
			if !l.rec.TS.Before(opts.since) {
				kept = append(kept, l)
			}
		}
		all = kept
	}
	return trimBacklog(all, opts.lastN)
}

// showRun replays a run log per opts, returning the process exit status:
// 0 on success or match, 1 when the run ends before an --until match, 2 on
// timeout. Snapshot mode pages and always succeeds.
func showRun(store *binaryStore, number int, opts showOptions) (int, error) {
	if !opts.follow {
		f, _, err := store.openRun(number)
		if err != nil {
			return 1, err
		}
		defer f.Close()
		backlog := visibleBacklog(readBacklog(newRecordReader(f)), opts)
		return 0, page(func(w io.Writer) {
			for _, l := range backlog {
				renderLine(w, l, opts)
			}
		})
	}

	outcome, err := streamRun(store, number, opts)
	if err != nil {
		return 1, err
	}
	switch outcome {
	case followMatched:
		return 0, nil
	case followTimedOut:
		return 2, nil
	default: // sealed, or writer gone (already reported)
		if opts.until != nil {
			return 1, nil
		}
		return 0, nil
	}
}

// streamRun renders a run's backlog and follows it to an outcome, honoring
// --until within the backlog itself so an already-passed match still counts.
func streamRun(store *binaryStore, number int, opts showOptions) (followOutcome, error) {
	f, compressed, err := store.openRun(number)
	if err != nil {
		return followSealed, err
	}
	defer f.Close()

	tail := newRecordReader(f)
	all := readBacklog(tail)

	pid := 0
	if len(all) > 0 && all[0].rec.T == recMeta {
		pid = all[0].rec.Pid
	}
	sealed := compressed || (len(all) > 0 && all[len(all)-1].rec.T == recExit)

	for _, l := range visibleBacklog(all, opts) {
		renderLine(os.Stdout, l, opts)
		if opts.matched(l) {
			return followMatched, nil
		}
	}
	if sealed {
		return followSealed, nil
	}
	return followRun(tail, pid, opts)
}

// followRun streams new records until the run's exit record, an --until
// match, or the deadline, polling the file for growth. A dead child with no
// trailer after a grace period means the writer died uncleanly; report and
// stop rather than wait forever.
func followRun(tail *recordReader, pid int, opts showOptions) (followOutcome, error) {
	const pollInterval = 250 * time.Millisecond
	const gracePolls = 8

	strikes := 0
	for {
		line, err := tail.next()
		if err == nil {
			renderLine(os.Stdout, line, opts)
			if opts.matched(line) {
				return followMatched, nil
			}
			if line.rec.T == recExit {
				return followSealed, nil
			}
			strikes = 0
			continue
		}
		if err != io.EOF {
			return followSealed, err
		}
		if opts.expired() {
			fmt.Fprintln(os.Stderr, "makedog: timeout")
			return followTimedOut, nil
		}
		if pid != 0 && syscall.Kill(pid, 0) != nil {
			if strikes++; strikes >= gracePolls {
				fmt.Fprintln(os.Stderr, "makedog: log ended without an exit record (writer gone)")
				return followWriterGone, nil
			}
		}
		time.Sleep(pollInterval)
	}
}

// renderLine writes one record in the terminal sink's dialect.
func renderLine(w io.Writer, l logLine, opts showOptions) {
	if opts.json {
		fmt.Fprintln(w, l.raw)
		return
	}
	switch l.rec.T {
	case recLine:
		s := l.rec.S
		if opts.plain {
			s = ansi.Strip(s)
		}
		fmt.Fprintf(w, "%s  %s\n", l.rec.TS.Format("[2006-01-02 15:04:05.000]"), s)
	case recCommand:
		fmt.Fprintf(w, "--> %s\n", restyle(commandStyle, l.rec.S, opts))
	case recEvent:
		fmt.Fprintf(w, "* %s\n", restyle(eventStyle, l.rec.S, opts))
	case recError:
		fmt.Fprintf(w, "! %s\n", restyle(errorStyle, l.rec.S, opts))
	case recMeta, recExit:
		// Data records; their substance already appears as command/event lines.
	}
}

func restyle(style lipgloss.Style, s string, opts showOptions) string {
	if opts.plain {
		return s
	}
	return style.Render(s)
}

// page routes output through $PAGER on a terminal, and directly otherwise.
func page(render func(io.Writer)) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		render(os.Stdout)
		return nil
	}

	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less -FRX"
	}
	words := strings.Fields(pager)
	cmd := exec.Command(words[0], words[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	in, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		render(os.Stdout)
		return nil
	}
	render(in)
	in.Close()
	return cmd.Wait()
}

// runSummary is one row of `makedog runs`.
type runSummary struct {
	Run    int       `json:"run"`
	Start  time.Time `json:"start,omitzero"`
	WallNs int64     `json:"wall_ns,omitempty"`
	Exit   string    `json:"exit"` // exit code, "sig NAME", "live", or "unclean"
	Reason string    `json:"reason,omitempty"`
	Git    string    `json:"git,omitempty"`
}

// runsMain implements `makedog runs`.
func runsMain(args []string) {
	fs := flag.NewFlagSet("runs", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSONL summaries")
	binary := fs.String("binary", "", "which binary's runs")
	dir := fs.String("C", "", "project directory (default: current)")
	fs.Parse(args)

	store, err := openLineage(*dir, *binary)
	if err != nil {
		fatal("%v", err)
	}
	numbers, err := store.runNumbers()
	if err != nil || len(numbers) == 0 {
		fatal("no runs recorded for %s", store.meta.Binary)
	}

	summaries := make([]runSummary, 0, len(numbers))
	for _, n := range numbers {
		summaries = append(summaries, summarizeRun(store, n))
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		for _, s := range summaries {
			enc.Encode(s)
		}
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tSTART\tDURATION\tEXIT\tREASON\tGIT")
	for _, s := range summaries {
		start, duration := "?", ""
		if !s.Start.IsZero() {
			start = s.Start.Format("2006-01-02 15:04:05")
		}
		if s.WallNs > 0 {
			duration = formatDuration(s.WallNs)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", s.Run, start, duration, s.Exit, s.Reason, s.Git)
	}
	tw.Flush()
}

// summarizeRun builds a run's summary from its header and trailer records —
// without scanning the middle for plain logs; compressed ones stream whole.
func summarizeRun(store *binaryStore, number int) runSummary {
	s := runSummary{Run: number, Exit: "?"}
	first, last, err := firstAndLastLines(store, number)
	if err != nil {
		return s
	}

	var meta, tail record
	json.Unmarshal([]byte(first), &meta)
	json.Unmarshal([]byte(last), &tail)

	if meta.T == recMeta {
		s.Start = meta.TS
		if meta.GitBranch != "" {
			commit := meta.GitCommit
			if len(commit) > 7 {
				commit = commit[:7]
			}
			s.Git = meta.GitBranch + "/" + commit
		}
	}

	switch {
	case tail.T == recExit:
		s.WallNs = tail.WallNs
		s.Reason = tail.Reason
		switch {
		case tail.Signal != "":
			s.Exit = "sig " + tail.Signal
		case tail.ExitCode != nil:
			s.Exit = strconv.Itoa(*tail.ExitCode)
		}
	case meta.Pid != 0 && syscall.Kill(meta.Pid, 0) == nil:
		s.Exit = "live"
		if !s.Start.IsZero() {
			s.WallNs = time.Since(s.Start).Nanoseconds()
		}
	default:
		s.Exit = "unclean"
	}
	return s
}

// firstAndLastLines reads a log's opening line and trailing complete line —
// by seeking for plain files, by streaming for compressed ones. A partial
// trailing line (a record mid-write) is ignored.
func firstAndLastLines(store *binaryStore, number int) (string, string, error) {
	r, compressed, err := store.openRun(number)
	if err != nil {
		return "", "", err
	}
	defer r.Close()

	if compressed {
		br := bufio.NewReader(r)
		var first, last string
		for {
			line, err := br.ReadString('\n')
			if line = strings.TrimRight(line, "\n"); line != "" && err == nil {
				if first == "" {
					first = line
				}
				last = line
			}
			if err != nil {
				return first, last, nil
			}
		}
	}

	f := r.(*os.File)

	first, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && first == "" {
		return "", "", err
	}
	first = strings.TrimRight(first, "\n")

	info, err := f.Stat()
	if err != nil {
		return first, "", err
	}
	const chunk = 64 * 1024
	off := max(info.Size()-chunk, 0)
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return first, "", err
	}

	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return first, first, nil
	}
	buf = buf[:end]
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		return first, string(buf[i+1:]), nil
	}
	return first, string(buf), nil
}

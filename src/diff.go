// The diff verb: compare two runs' output. Logs diff poorly raw — every
// timestamp and pid differs — so comparison works on a normalized projection:
// child output lines only, ANSI stripped, lifecycle chrome dropped. A Myers
// edit script renders as unified hunks under headers carrying each run's exit
// status. Exit codes follow diff(1): 0 identical, 1 different, 2 trouble.
package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	delStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	insStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("green"))
)

// diffMain implements `makedog diff [refA [refB]]`, defaulting to
// latest~1 vs latest — the "what changed since the last run" question.
func diffMain(args []string) {
	trouble := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "makedog: "+format+"\n", a...)
		os.Exit(2)
	}

	refA, rest := splitRef(args)
	refB, rest := splitRef(rest)
	switch {
	case refA == "":
		refA, refB = "latest~1", "latest"
	case refB == "":
		refB = "latest"
	}

	fs := newVerbFlags("diff")
	plain := fs.Bool("No color on change lines", "plain")
	binary, dir := lineageFlags(fs)
	parseVerbFlags(fs, rest)
	if fs.NArg() > 0 {
		trouble("diff: bad run reference '%s' (a number, or latest[~N])", fs.Arg(0))
	}

	store, err := openLineage(*dir, *binary)
	if err != nil {
		trouble("%v", err)
	}
	resolve := func(ref string) int {
		r, _ := parseRunRef(ref)
		n, err := store.resolveRef(r)
		if err != nil {
			trouble("%v", err)
		}
		return n
	}
	na, nb := resolve(refA), resolve(refB)

	linesA, err := childLines(store, na)
	if err != nil {
		trouble("%v", err)
	}
	linesB, err := childLines(store, nb)
	if err != nil {
		trouble("%v", err)
	}

	edits := myersDiff(linesA, linesB)
	body := unifiedHunks(edits, 3)
	if len(body) == 0 {
		return // identical: silence, exit 0, per diff(1)
	}

	fmt.Printf("--- %s\n", runHeader(store, na))
	fmt.Printf("+++ %s\n", runHeader(store, nb))
	for _, line := range body {
		if !*plain {
			switch line[0] {
			case '-':
				line = delStyle.Render(line)
			case '+':
				line = insStyle.Render(line)
			}
		}
		fmt.Println(line)
	}
	os.Exit(1)
}

// childLines projects a run onto its comparable form: output lines only,
// ANSI stripped.
func childLines(store *binaryStore, number int) ([]string, error) {
	f, _, err := store.openRun(number)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	tail := newRecordReader(f)
	for {
		l, err := tail.next()
		if err != nil {
			return lines, nil
		}
		if l.rec.T == recLine {
			lines = append(lines, ansi.Strip(l.rec.S))
		}
	}
}

// runHeader labels one side of the diff with the run's status and date.
func runHeader(store *binaryStore, number int) string {
	card, err := buildRunCard(store, number)
	if err != nil {
		return fmt.Sprintf("run %d", number)
	}
	status := card.Status
	switch {
	case card.Signal != "":
		status = "killed by " + card.Signal
	case card.ExitCode != nil:
		status = fmt.Sprintf("exit %d", *card.ExitCode)
	}
	when := ""
	if !card.Start.IsZero() {
		when = ", " + card.Start.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("run %d (%s%s)", number, status, when)
}

// editKind classifies one step of an edit script.
type editKind int

const (
	opEq editKind = iota
	opDel
	opIns
)

// edit is one line of an edit script.
type edit struct {
	kind editKind
	text string
}

// myersDiff computes a line-level edit script from a to b, via the greedy
// O((N+M)·D) Myers algorithm — linear-ish when the runs mostly agree.
func myersDiff(a, b []string) []edit {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}

	// Forward pass: v[k] is the furthest x on diagonal k after d steps;
	// trace snapshots v before each depth for the backtrack.
	offset := max
	v := make([]int, 2*max+1)
	var trace [][]int
	depth := -1
walk:
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[offset+k] = x
			if x >= n && y >= m {
				depth = d
				break walk
			}
		}
	}

	// Backtrack from (n, m) through the snapshots, emitting in reverse.
	var reversed []edit
	x, y := n, m
	for d := depth; d >= 0 && (x > 0 || y > 0); d-- {
		vd := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vd[offset+k-1] < vd[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vd[offset+prevK]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			reversed = append(reversed, edit{opEq, a[x-1]})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == prevX {
				reversed = append(reversed, edit{opIns, b[y-1]})
			} else {
				reversed = append(reversed, edit{opDel, a[x-1]})
			}
		}
		x, y = prevX, prevY
	}

	edits := make([]edit, len(reversed))
	for i, e := range reversed {
		edits[len(reversed)-1-i] = e
	}
	return edits
}

// unifiedHunks renders an edit script as unified-diff hunk lines with the
// given context; empty when the script holds no changes.
func unifiedHunks(edits []edit, context int) []string {
	// Line numbers (0-based) on each side before every edit.
	aPos := make([]int, len(edits)+1)
	bPos := make([]int, len(edits)+1)
	changed := false
	for i, e := range edits {
		aPos[i+1], bPos[i+1] = aPos[i], bPos[i]
		if e.kind != opIns {
			aPos[i+1]++
		}
		if e.kind != opDel {
			bPos[i+1]++
		}
		changed = changed || e.kind != opEq
	}
	if !changed {
		return nil
	}

	// Group changes into spans of edit indexes, expanded by context and
	// merged when their contexts touch.
	type span struct{ lo, hi int }
	var spans []span
	for i, e := range edits {
		if e.kind == opEq {
			continue
		}
		lo, hi := max(0, i-context), min(len(edits), i+context+1)
		if len(spans) > 0 && lo <= spans[len(spans)-1].hi {
			spans[len(spans)-1].hi = hi
		} else {
			spans = append(spans, span{lo, hi})
		}
	}

	var out []string
	for _, sp := range spans {
		aLen, bLen := aPos[sp.hi]-aPos[sp.lo], bPos[sp.hi]-bPos[sp.lo]
		out = append(out, fmt.Sprintf("@@ -%s +%s @@",
			hunkRange(aPos[sp.lo], aLen), hunkRange(bPos[sp.lo], bLen)))
		for _, e := range edits[sp.lo:sp.hi] {
			prefix := " "
			switch e.kind {
			case opDel:
				prefix = "-"
			case opIns:
				prefix = "+"
			}
			out = append(out, prefix+e.text)
		}
	}
	return out
}

// hunkRange formats one side's "start,len" per unified convention: 1-based
// starts, except a zero-length side names the line before.
func hunkRange(pos, length int) string {
	start := pos + 1
	if length == 0 {
		start = pos
	}
	if length == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, length)
}

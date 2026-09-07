// Tests for the crash verb: spin detection replayed over run trailers, the
// live-run skip, the fallback when nothing spun, and JSON output.
package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// crashRun writes run number with one output line, starting at start and
// exiting code after wall; reason marks a stop makedog initiated.
func crashRun(t *testing.T, lineage string, number int, start time.Time, wall time.Duration, code int, reason, line string) {
	t.Helper()
	end := start.Add(wall)
	writeRun(t, lineage, number, []record{
		{T: recMeta, TS: start, Run: number, Binary: "child", Pid: 1},
		{T: recLine, TS: start.Add(wall / 2), S: line},
		{T: recExit, TS: end, ExitCode: &code, Reason: reason, WallNs: wall.Nanoseconds()},
	})
}

// spinFixture lays out a healthy run stopped for a rebuild, then a crash
// loop of spinMinExits fast self-exits.
func spinFixture(t *testing.T) (string, string, string, time.Time) {
	t.Helper()
	proj, state, lineage := fixtureStore(t)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	crashRun(t, lineage, 1, base, time.Minute, 0, "binary modified", "listening on :8080")
	next := base.Add(61 * time.Second)
	for i := 0; i < spinMinExits; i++ {
		start := next.Add(time.Duration(i) * 400 * time.Millisecond)
		crashRun(t, lineage, 2+i, start, 300*time.Millisecond, 1, "", "panic: boom")
	}
	return proj, state, lineage, next.Add(10 * time.Second)
}

func TestCrash(t *testing.T) {
	t.Run("spin runs print back to back, exit 0", func(t *testing.T) {
		proj, state, _, _ := spinFixture(t)
		out, code := readVerb(t, proj, state, 5*time.Second, "crash", "--plain")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		if !strings.Contains(out, "spin: 3 runs") {
			t.Errorf("missing spin note:\n%s", out)
		}
		for _, want := range []string{"=== run 2  exit 1", "=== run 3  exit 1", "=== run 4  exit 1"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing header %q:\n%s", want, out)
			}
		}
		if strings.Count(out, "panic: boom") != 3 {
			t.Errorf("want 3 panic lines:\n%s", out)
		}
		if strings.Contains(out, "run 1") || strings.Contains(out, "listening") {
			t.Errorf("healthy run leaked in:\n%s", out)
		}
	})

	t.Run("a live newest run is skipped", func(t *testing.T) {
		proj, state, lineage, later := spinFixture(t)
		writeRun(t, lineage, 5, []record{
			{T: recMeta, TS: later, Run: 5, Binary: "child", Pid: os.Getpid()},
			{T: recLine, TS: later, S: "back up"},
		})
		out, code := readVerb(t, proj, state, 5*time.Second, "crash", "--plain")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		if !strings.Contains(out, "=== run 4  exit 1") || strings.Contains(out, "back up") {
			t.Errorf("want runs 2..4 without the live run:\n%s", out)
		}
	})

	t.Run("no spin falls back to the last runs, exit 1", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		base := time.Now().Add(-time.Hour)
		for n := 1; n <= 4; n++ {
			crashRun(t, lineage, n, base.Add(time.Duration(n)*time.Minute), 30*time.Second, 1, "", "slow death")
		}
		out, code := readVerb(t, proj, state, 5*time.Second, "crash", "--plain")
		if code != 1 {
			t.Fatalf("exit = %d; want 1; output:\n%s", code, out)
		}
		if !strings.Contains(out, "no spin in recent runs; showing the last 3") {
			t.Errorf("missing fallback note:\n%s", out)
		}
		if strings.Contains(out, "=== run 1 ") || !strings.Contains(out, "=== run 2 ") || !strings.Contains(out, "=== run 4 ") {
			t.Errorf("want runs 2..4:\n%s", out)
		}
	})

	t.Run("json emits raw records only", func(t *testing.T) {
		proj, state, _, _ := spinFixture(t)
		out, code := readVerb(t, proj, state, 5*time.Second, "crash", "--json")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		if strings.Contains(out, "spin:") || strings.Contains(out, "===") {
			t.Errorf("headers in json output:\n%s", out)
		}
		if got := strings.Count(out, `"t":"meta"`); got != 3 {
			t.Errorf("want 3 meta records, got %d:\n%s", got, out)
		}
	})
}

func TestSpinRuns(t *testing.T) {
	at := func(offset time.Duration, wall time.Duration, exit, reason string) runSummary {
		return runSummary{Start: time.Unix(1_700_000_000, 0).Add(offset), WallNs: wall.Nanoseconds(), Exit: exit, Reason: reason}
	}
	fast := func(offset time.Duration) runSummary { return at(offset, 200*time.Millisecond, "1", "") }

	t.Run("exits spread past the window do not spin", func(t *testing.T) {
		all := []runSummary{fast(0), fast(3 * time.Second), fast(6 * time.Second)}
		if picked, spun := spinRuns(all); spun || len(picked) != 3 {
			t.Errorf("spun = %v, picked = %d", spun, len(picked))
		}
	})

	t.Run("a stop reason ends the streak", func(t *testing.T) {
		all := []runSummary{fast(0), at(time.Second, 200*time.Millisecond, "0", "manual restart"), fast(2 * time.Second), fast(3 * time.Second)}
		if _, spun := spinRuns(all); spun {
			t.Error("two self-exits after a manual stop should not spin")
		}
	})

	t.Run("only the exits inside the window are picked", func(t *testing.T) {
		all := []runSummary{fast(0), fast(20 * time.Second), fast(21 * time.Second), fast(22 * time.Second)}
		picked, spun := spinRuns(all)
		if !spun || len(picked) != 3 || picked[0].Start != all[1].Start {
			t.Errorf("spun = %v, picked = %+v", spun, picked)
		}
	})
}

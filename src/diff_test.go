// Test suite for the diff verb: the Myers edit script's correctness, hunk
// grouping, and the verb's normalization and exit convention end to end.
package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// reconstruct checks an edit script's soundness: its eq+del lines must spell
// a, its eq+ins lines must spell b.
func reconstruct(t *testing.T, a, b []string, edits []edit) {
	t.Helper()
	var gotA, gotB []string
	for _, e := range edits {
		if e.kind != opIns {
			gotA = append(gotA, e.text)
		}
		if e.kind != opDel {
			gotB = append(gotB, e.text)
		}
	}
	if strings.Join(gotA, "\n") != strings.Join(a, "\n") {
		t.Errorf("script does not spell a: %v", edits)
	}
	if strings.Join(gotB, "\n") != strings.Join(b, "\n") {
		t.Errorf("script does not spell b: %v", edits)
	}
}

func TestMyersDiff(t *testing.T) {
	cases := []struct {
		name    string
		a, b    []string
		changes int // del + ins count
	}{
		{"identical", []string{"x", "y", "z"}, []string{"x", "y", "z"}, 0},
		{"both empty", nil, nil, 0},
		{"all inserted", nil, []string{"x", "y"}, 2},
		{"all deleted", []string{"x", "y"}, nil, 2},
		{"replace middle", []string{"a", "b", "c"}, []string{"a", "B", "c"}, 2},
		{"insert within", []string{"a", "c"}, []string{"a", "b", "c"}, 1},
		{"delete within", []string{"a", "b", "c"}, []string{"a", "c"}, 1},
		{"disjoint", []string{"a", "b"}, []string{"x", "y"}, 4},
		{"classic abcabba", strings.Split("abcabba", ""), strings.Split("cbabac", ""), 5},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			edits := myersDiff(tt.a, tt.b)
			reconstruct(t, tt.a, tt.b, edits)
			changes := 0
			for _, e := range edits {
				if e.kind != opEq {
					changes++
				}
			}
			if changes != tt.changes {
				t.Errorf("changes = %d; want %d (%v)", changes, tt.changes, edits)
			}
		})
	}
}

func TestUnifiedHunks(t *testing.T) {
	t.Run("no changes renders nothing", func(t *testing.T) {
		if h := unifiedHunks([]edit{{opEq, "x"}, {opEq, "y"}}, 3); h != nil {
			t.Errorf("hunks = %v", h)
		}
	})

	t.Run("one change with context", func(t *testing.T) {
		a := []string{"1", "2", "3", "4", "5", "6", "7"}
		b := []string{"1", "2", "3", "X", "5", "6", "7"}
		got := unifiedHunks(myersDiff(a, b), 3)
		want := []string{"@@ -1,7 +1,7 @@", " 1", " 2", " 3", "-4", "+X", " 5", " 6", " 7"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("hunks = %v; want %v", got, want)
		}
	})

	t.Run("distant changes split into hunks", func(t *testing.T) {
		var a, b []string
		for i := 0; i < 30; i++ {
			line := string(rune('a' + i%26))
			a = append(a, line)
			b = append(b, line)
		}
		b[2], b[27] = "X", "Y"
		got := unifiedHunks(myersDiff(a, b), 3)
		headers := 0
		for _, l := range got {
			if strings.HasPrefix(l, "@@") {
				headers++
			}
		}
		if headers != 2 {
			t.Errorf("hunk count = %d; want 2:\n%s", headers, strings.Join(got, "\n"))
		}
	})
}

func TestDiffVerb(t *testing.T) {
	fixture := func(t *testing.T, secondLines []string) (string, string) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()),
			lineRec("listening on :8080"), lineRec("migrations: 0 pending"), exitRec()})
		recs := []record{metaRec(2, os.Getpid())}
		for _, l := range secondLines {
			recs = append(recs, lineRec(l))
		}
		writeRun(t, lineage, 2, append(recs, exitRec()))
		return proj, state
	}

	t.Run("changed output diffs with exit 1", func(t *testing.T) {
		proj, state := fixture(t, []string{"listening on :8080", "migrations: 3 pending"})
		out, code := readVerb(t, proj, state, 5*time.Second, "diff", "--plain")
		if code != 1 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		for _, want := range []string{"--- run 1 (exit 0", "+++ run 2 (exit 0",
			"-migrations: 0 pending", "+migrations: 3 pending", " listening on :8080"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("identical output is silent with exit 0", func(t *testing.T) {
		proj, state := fixture(t, []string{"listening on :8080", "migrations: 0 pending"})
		out, code := readVerb(t, proj, state, 5*time.Second, "diff")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("exit = %d, output %q; want silent success", code, out)
		}
	})

	t.Run("explicit refs and trouble exit", func(t *testing.T) {
		proj, state := fixture(t, []string{"other"})
		_, code := readVerb(t, proj, state, 5*time.Second, "diff", "1", "2")
		if code != 1 {
			t.Errorf("explicit refs: exit = %d; want 1", code)
		}
		_, code = readVerb(t, proj, state, 5*time.Second, "diff", "1", "9")
		if code != 2 {
			t.Errorf("missing run: exit = %d; want 2", code)
		}
	})
}

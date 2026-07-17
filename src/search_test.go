// Test suite for the search verb: hit addressing, scope filters, mixed
// plain/compressed forms, and grep's exit convention.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchFixture builds a lineage with three sealed runs, the first
// compressed: run 1 "listening on :8080", run 2 "panic: boom", run 3 quiet.
func searchFixture(t *testing.T) (string, string, string) {
	t.Helper()
	proj, state, lineage := fixtureStore(t)
	s := &binaryStore{dir: lineage}

	writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), lineRec("listening on :8080"), exitRec()})
	writeRun(t, lineage, 2, []record{metaRec(2, os.Getpid()), lineRec("panic: boom"), exitRec()})
	writeRun(t, lineage, 3, []record{metaRec(3, os.Getpid()), lineRec("all quiet"), exitRec()})
	if err := compressRun(s.runPath(1)); err != nil {
		t.Fatal(err)
	}
	return proj, state, lineage
}

func TestSearch(t *testing.T) {
	t.Run("hits print run and timestamp, exit 0", func(t *testing.T) {
		proj, state, _ := searchFixture(t)
		out, code := readVerb(t, proj, state, 5*time.Second, "search", "listening|panic")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		if !strings.Contains(out, "1:") || !strings.Contains(out, "listening on :8080") {
			t.Errorf("missing compressed-run hit:\n%s", out)
		}
		if !strings.Contains(out, "2:") || !strings.Contains(out, "panic: boom") {
			t.Errorf("missing plain-run hit:\n%s", out)
		}
	})

	t.Run("no match exits 1", func(t *testing.T) {
		proj, state, _ := searchFixture(t)
		_, code := readVerb(t, proj, state, 5*time.Second, "search", "unicorn")
		if code != 1 {
			t.Errorf("exit = %d; want 1", code)
		}
	})

	t.Run("runs range scopes the sweep", func(t *testing.T) {
		proj, state, _ := searchFixture(t)
		out, code := readVerb(t, proj, state, 5*time.Second, "search", "listening|panic", "--runs", "2..3")
		if code != 0 || strings.Contains(out, "listening") || !strings.Contains(out, "panic") {
			t.Errorf("exit = %d, output:\n%s", code, out)
		}
	})

	t.Run("since excludes idle runs", func(t *testing.T) {
		proj, state, lineage := searchFixture(t)
		old := time.Now().Add(-48 * time.Hour)
		s := &binaryStore{dir: lineage}
		if err := os.Chtimes(s.runPath(2), old, old); err != nil {
			t.Fatal(err)
		}
		_, code := readVerb(t, proj, state, 5*time.Second, "search", "panic", "--since", "24h")
		if code != 1 {
			t.Errorf("exit = %d; want 1 (idle run excluded)", code)
		}
	})

	t.Run("json hits carry run and binary", func(t *testing.T) {
		proj, state, _ := searchFixture(t)
		out, code := readVerb(t, proj, state, 5*time.Second, "search", "panic", "--json")
		if code != 0 || !strings.Contains(out, `"run":2`) || !strings.Contains(out, `"binary":"child"`) {
			t.Errorf("exit = %d, output:\n%s", code, out)
		}
	})

	t.Run("all-binaries prefixes each hit's lineage", func(t *testing.T) {
		proj, state, lineage := searchFixture(t)
		other := filepath.Join(filepath.Dir(lineage), slug("bin/other"))
		if err := os.MkdirAll(filepath.Join(other, "runs"), 0o755); err != nil {
			t.Fatal(err)
		}
		meta := []byte("binary = \"bin/other\"\ncwd = \"x\"\n")
		if err := os.WriteFile(filepath.Join(other, "meta.toml"), meta, 0o644); err != nil {
			t.Fatal(err)
		}
		writeRun(t, other, 1, []record{metaRec(1, os.Getpid()), lineRec("panic: elsewhere"), exitRec()})

		out, code := readVerb(t, proj, state, 5*time.Second, "search", "panic", "--all-binaries")
		if code != 0 || !strings.Contains(out, "child:2:") || !strings.Contains(out, "bin/other:1:") {
			t.Errorf("exit = %d, output:\n%s", code, out)
		}
	})
}

func TestParseRunRange(t *testing.T) {
	tests := []struct {
		in   string
		want runRange
		ok   bool
	}{
		{"135", runRange{135, 135}, true},
		{"130..135", runRange{130, 135}, true},
		{"5..3", runRange{}, false},
		{"0..3", runRange{}, false},
		{"..", runRange{}, false},
		{"abc", runRange{}, false},
	}
	for _, tt := range tests {
		got, err := parseRunRange(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseRunRange(%q) = %+v, %v; want %+v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

// Test suite for the read verbs' blocking forms: --until/--timeout exit
// codes, next, and tail. Runs the real binary against handcrafted store
// fixtures, so timing and exit statuses are exercised as callers see them.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureStore lays out a store lineage for a project directory and returns
// (projDir, stateDir, lineageDir).
func fixtureStore(t *testing.T) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	projDir, stateDir := filepath.Join(base, "proj"), filepath.Join(base, "state")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, err := realDir(projDir)
	if err != nil {
		t.Fatal(err)
	}
	lineage := filepath.Join(stateDir, slug(cwd), slug("child"))
	if err := os.MkdirAll(filepath.Join(lineage, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte("binary = \"child\"\ncwd = \"" + cwd + "\"\n")
	if err := os.WriteFile(filepath.Join(lineage, "meta.toml"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	return projDir, stateDir, lineage
}

// writeRun writes a run log from records.
func writeRun(t *testing.T, lineage string, number int, recs []record) string {
	t.Helper()
	var data []byte
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, b...), '\n')
	}
	path := (&binaryStore{dir: lineage}).runPath(number)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func metaRec(number, pid int) record {
	return record{T: recMeta, TS: time.Now(), Run: number, Binary: "child", Pid: pid}
}

func lineRec(s string) record {
	return record{T: recLine, TS: time.Now(), S: s}
}

func exitRec() record {
	code := 0
	return record{T: recExit, TS: time.Now(), ExitCode: &code, Reason: "quit requested"}
}

// readVerb runs the makedog binary with a read-verb command line against the
// fixture, returning combined output and exit code.
func readVerb(t *testing.T, projDir, stateDir string, timeout time.Duration, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(makedogBinary(t), args...)
	cmd.Dir = projDir
	cmd.Env = append(os.Environ(), "MAKEDOG_STATE_DIR="+stateDir)

	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(timeout):
			cmd.Process.Kill()
		}
	}()
	out, _ := cmd.CombinedOutput()
	close(done)

	if cmd.ProcessState == nil {
		t.Fatalf("makedog %v never ran", args)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

func TestUntilExitCodes(t *testing.T) {
	deadPid := func() int {
		c := exec.Command("true")
		if err := c.Run(); err != nil {
			t.Fatal(err)
		}
		return c.Process.Pid
	}

	t.Run("match in sealed run exits 0", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, deadPid()), lineRec("listening on :8080"), exitRec()})

		out, code := readVerb(t, proj, state, 5*time.Second, "latest", "--until", "listening on", "--plain")
		if code != 0 {
			t.Errorf("exit = %d; want 0; output:\n%s", code, out)
		}
	})

	t.Run("sealed run without match exits 1", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, deadPid()), lineRec("crashed early"), exitRec()})

		out, code := readVerb(t, proj, state, 5*time.Second, "latest", "--until", "listening on")
		if code != 1 {
			t.Errorf("exit = %d; want 1; output:\n%s", code, out)
		}
	})

	t.Run("live unsealed run times out with exit 2", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		// Our own pid keeps the run "live" so only the deadline can end it.
		writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), lineRec("still starting")})

		out, code := readVerb(t, proj, state, 5*time.Second, "latest", "--until", "listening on", "--timeout", "700ms")
		if code != 2 {
			t.Errorf("exit = %d; want 2; output:\n%s", code, out)
		}
	})

	t.Run("dead writer without trailer ends follow", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, deadPid()), lineRec("tick")})

		out, code := readVerb(t, proj, state, 15*time.Second, "latest", "-f")
		if code != 0 {
			t.Errorf("exit = %d; want 0; output:\n%s", code, out)
		}
	})
}

func TestNext(t *testing.T) {
	t.Run("streams the run that appears and matches", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), exitRec()}) // baseline

		go func() {
			time.Sleep(500 * time.Millisecond)
			writeRun(t, lineage, 2, []record{metaRec(2, os.Getpid()), lineRec("ready to serve"), exitRec()})
		}()

		out, code := readVerb(t, proj, state, 10*time.Second, "next", "--until", "ready", "--timeout", "5s")
		if code != 0 {
			t.Errorf("exit = %d; want 0; output:\n%s", code, out)
		}
	})

	t.Run("times out with exit 2 when no run appears", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), exitRec()})

		out, code := readVerb(t, proj, state, 5*time.Second, "next", "--timeout", "600ms")
		if code != 2 {
			t.Errorf("exit = %d; want 2; output:\n%s", code, out)
		}
	})
}

// TestShowSince verifies --since slices records within a run by timestamp.
func TestShowSince(t *testing.T) {
	proj, state, lineage := fixtureStore(t)
	stale := record{T: recLine, TS: time.Now().Add(-2 * time.Hour), S: "old news"}
	writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), stale, lineRec("fresh line"), exitRec()})

	out, code := readVerb(t, proj, state, 5*time.Second, "latest", "--since", "1h", "--plain")
	if code != 0 {
		t.Fatalf("exit = %d; output:\n%s", code, out)
	}
	if strings.Contains(out, "old news") || !strings.Contains(out, "fresh line") {
		t.Errorf("--since window wrong:\n%s", out)
	}
}

// TestTailRollsAcrossRuns starts tail on a sealed run without a match, then
// adds a matching successor: tail must roll into it and exit 0.
func TestTailRollsAcrossRuns(t *testing.T) {
	proj, state, lineage := fixtureStore(t)
	writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), lineRec("first run"), exitRec()})

	go func() {
		time.Sleep(500 * time.Millisecond)
		writeRun(t, lineage, 2, []record{metaRec(2, os.Getpid()), lineRec("second run says hello"), exitRec()})
	}()

	out, code := readVerb(t, proj, state, 10*time.Second, "tail", "--until", "second run", "--timeout", "5s")
	if code != 0 {
		t.Errorf("exit = %d; want 0; output:\n%s", code, out)
	}
}

func TestRunsLong(t *testing.T) {
	proj, state, _ := searchFixture(t) // runs 1 (compressed), 2, 3

	t.Run("json emits one full card per run", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "runs", "--long", "--json")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 3 {
			t.Fatalf("got %d cards; want 3:\n%s", len(lines), out)
		}
		for i, line := range lines {
			var card runCard
			if err := json.Unmarshal([]byte(line), &card); err != nil {
				t.Fatalf("parsing card %d: %v\n%s", i, err, line)
			}
			if card.Run != i+1 || card.Status != "sealed" || card.LogSize == 0 {
				t.Errorf("card %d = %+v", i, card)
			}
			if (i == 0) != card.Compressed {
				t.Errorf("card %d compressed = %v", i, card.Compressed)
			}
		}
	})

	t.Run("table carries the wide columns", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "runs", "--long")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		for _, want := range []string{"CPU", "MEMORY", "SIZE", "quit requested"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})
}

// Test suite for the path and info plumbing verbs, over plain and compressed
// runs through the real binary.
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPath(t *testing.T) {
	proj, state, lineage := searchFixture(t) // runs 1 (compressed), 2, 3

	t.Run("prints the run file, whichever form", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "path", "2")
		if code != 0 || strings.TrimSpace(out) != (&binaryStore{dir: lineage}).runPath(2) {
			t.Errorf("exit %d, output %q", code, out)
		}
		out, code = readVerb(t, proj, state, 5*time.Second, "path", "1")
		if code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "000001.jsonl.zst") {
			t.Errorf("compressed form: exit %d, output %q", code, out)
		}
	})

	t.Run("no ref prints the lineage directory", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "path")
		if code != 0 || strings.TrimSpace(out) != lineage {
			t.Errorf("exit %d, output %q; want %q", code, out, lineage)
		}
	})

	t.Run("bad ref fails", func(t *testing.T) {
		_, code := readVerb(t, proj, state, 5*time.Second, "path", "9")
		if code != 1 {
			t.Errorf("exit = %d; want 1", code)
		}
	})
}

func TestInfo(t *testing.T) {
	proj, state, _ := searchFixture(t)

	t.Run("json card carries trailer and file facts", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "info", "1", "--json")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		var card runCard
		if err := json.Unmarshal([]byte(out), &card); err != nil {
			t.Fatalf("parsing card: %v\n%s", err, out)
		}
		if card.Run != 1 || card.Binary != "child" || card.Status != "sealed" ||
			card.ExitCode == nil || *card.ExitCode != 0 || card.Reason != "quit requested" {
			t.Errorf("card = %+v", card)
		}
		if !card.Compressed || card.LogSize == 0 || !strings.HasSuffix(card.LogPath, ".zst") {
			t.Errorf("file facts wrong: %+v", card)
		}
	})

	t.Run("defaults to latest, renders the human card", func(t *testing.T) {
		out, code := readVerb(t, proj, state, 5*time.Second, "info")
		if code != 0 {
			t.Fatalf("exit = %d; output:\n%s", code, out)
		}
		for _, want := range []string{"run      3  child", "status   sealed (exit 0)", "reason   quit requested", "log      "} {
			if !strings.Contains(out, want) {
				t.Errorf("card missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("live run reports live status", func(t *testing.T) {
		proj, state, lineage := fixtureStore(t)
		writeRun(t, lineage, 1, []record{metaRec(1, os.Getpid()), lineRec("starting")})

		out, code := readVerb(t, proj, state, 5*time.Second, "info", "--json")
		var card runCard
		if code != 0 || json.Unmarshal([]byte(out), &card) != nil || card.Status != "live" {
			t.Errorf("exit %d, card %+v", code, card)
		}
	})
}

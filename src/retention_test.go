// Test suite for store retention: policy parsing, and a maintenance pass
// compressing idle runs and pruning by count, age, and size — with the read
// side still serving the compressed survivors.
package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseAge(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"24h", 24 * time.Hour, true},
		{"90m", 90 * time.Minute, true},
		{"30d", 30 * 24 * time.Hour, true},
		{"0", 0, true},
		{"-2h", 0, false},
		{"soon", 0, false},
	}
	for _, tt := range tests {
		got, err := parseAge(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseAge(%q) = %v, %v; want %v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"512MB", 512 << 20, true},
		{"1GB", 1 << 30, true},
		{"64KB", 64 << 10, true},
		{"2048", 2048, true},
		{"0", 0, true},
		{"-1MB", 0, false},
		{"lots", 0, false},
	}
	for _, tt := range tests {
		got, err := parseSize(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseSize(%q) = %v, %v; want %v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

// agedLineage builds a lineage of sealed runs whose files are backdated.
func agedLineage(t *testing.T, runs int, age time.Duration) (string, string, *binaryStore) {
	t.Helper()
	proj, state, lineage := fixtureStore(t)
	s := &binaryStore{dir: lineage}
	old := time.Now().Add(-age)
	for n := 1; n <= runs; n++ {
		path := writeRun(t, lineage, n, []record{metaRec(n, os.Getpid()), lineRec("tick"), exitRec()})
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return proj, state, s
}

func TestMaintainCompressesAndProtects(t *testing.T) {
	proj, state, s := agedLineage(t, 5, 48*time.Hour)

	compressed, pruned, err := s.maintain(retentionPolicy{compressAfter: 24 * time.Hour, keepRuns: 500})
	if err != nil {
		t.Fatal(err)
	}
	if compressed != 3 || pruned != 0 {
		t.Errorf("maintain = %d compressed, %d pruned; want 3, 0", compressed, pruned)
	}

	// The protected window (newest two) stays plain; the rest are zst-only.
	for n := 1; n <= 3; n++ {
		if _, err := os.Stat(s.runPath(n)); !os.IsNotExist(err) {
			t.Errorf("run %d still plain after compression", n)
		}
		if _, err := os.Stat(s.runPath(n) + ".zst"); err != nil {
			t.Errorf("run %d has no compressed form: %v", n, err)
		}
	}
	for n := 4; n <= 5; n++ {
		if _, err := os.Stat(s.runPath(n)); err != nil {
			t.Errorf("protected run %d was touched: %v", n, err)
		}
	}

	// The read side serves compressed runs: replay and list through the binary.
	out, code := readVerb(t, proj, state, 5*time.Second, "1", "--plain")
	if code != 0 || !strings.Contains(out, "tick") {
		t.Errorf("replay of compressed run: exit %d, output:\n%s", code, out)
	}
	out, code = readVerb(t, proj, state, 5*time.Second, "runs")
	if code != 0 || strings.Count(out, "quit requested") != 5 {
		t.Errorf("runs listing over mixed forms: exit %d, output:\n%s", code, out)
	}
}

func TestMaintainPrunes(t *testing.T) {
	t.Run("by count", func(t *testing.T) {
		_, _, s := agedLineage(t, 6, time.Hour)
		_, pruned, err := s.maintain(retentionPolicy{keepRuns: 3})
		if err != nil || pruned != 3 {
			t.Fatalf("pruned = %d, %v; want 3", pruned, err)
		}
		if numbers, _ := s.runNumbers(); len(numbers) != 3 || numbers[0] != 4 {
			t.Errorf("survivors = %v; want [4 5 6]", numbers)
		}
	})

	t.Run("by age", func(t *testing.T) {
		_, _, s := agedLineage(t, 4, 72*time.Hour)
		_, pruned, err := s.maintain(retentionPolicy{maxAge: 24 * time.Hour})
		if err != nil || pruned != 2 {
			t.Fatalf("pruned = %d, %v; want 2 (newest two protected)", pruned, err)
		}
	})

	t.Run("by total size", func(t *testing.T) {
		_, _, s := agedLineage(t, 5, time.Hour)
		infos, err := s.runInfos()
		if err != nil || len(infos) != 5 {
			t.Fatal(err)
		}
		// Cap to roughly two files: the three oldest must go.
		cap := infos[0].size * 5 / 2
		_, pruned, err := s.maintain(retentionPolicy{maxTotal: cap})
		if err != nil || pruned != 3 {
			t.Fatalf("pruned = %d, %v; want 3", pruned, err)
		}
	})

	t.Run("never below the protected window", func(t *testing.T) {
		_, _, s := agedLineage(t, 3, 720*time.Hour)
		_, pruned, err := s.maintain(retentionPolicy{keepRuns: 1, maxAge: time.Minute, maxTotal: 1})
		if err != nil || pruned != 1 {
			t.Fatalf("pruned = %d, %v; want 1", pruned, err)
		}
		if numbers, _ := s.runNumbers(); len(numbers) != 2 {
			t.Errorf("survivors = %v; want the protected two", numbers)
		}
	})
}

func TestRetentionFromConfig(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }

	t.Run("nil section takes defaults", func(t *testing.T) {
		if p := retentionFromConfig(nil); p != defaultRetention {
			t.Errorf("policy = %+v; want defaults", p)
		}
	})

	t.Run("explicit zeros disable", func(t *testing.T) {
		p := retentionFromConfig(&LogsConfig{CompressAfter: str("0"), KeepRuns: num(0)})
		if p.compressAfter != 0 || p.keepRuns != 0 {
			t.Errorf("policy = %+v; want compression and keep_runs disabled", p)
		}
	})

	t.Run("bad values warn and keep defaults", func(t *testing.T) {
		p := retentionFromConfig(&LogsConfig{MaxAge: str("whenever"), MaxTotal: str("plenty")})
		if p != defaultRetention {
			t.Errorf("policy = %+v; want defaults", p)
		}
	})
}

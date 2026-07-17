// Test suite for the run store: identity slugs, run references, and the
// durability of the flock-guarded run counter under contention.
package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/Users/x/dev/proj", "-Users-x-dev-proj"},
		{"bin/server", "bin-server"},
		{"test-binary_1.2", "test-binary_1.2"},
		{"weird name/α", "weird-name--"},
	}
	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestBinaryKey(t *testing.T) {
	cwd := "/home/dev/proj"
	tests := []struct{ in, want string }{
		{"bin/server", "bin/server"},
		{"./bin/server", "bin/server"},
		{"/home/dev/proj/bin/server", "bin/server"},
		{"/usr/local/bin/other", "/usr/local/bin/other"},
		{"../sibling/bin/x", "/home/dev/sibling/bin/x"},
	}
	for _, tt := range tests {
		if got := binaryKey(cwd, tt.in); got != tt.want {
			t.Errorf("binaryKey(%q, %q) = %q; want %q", cwd, tt.in, got, tt.want)
		}
	}
}

func TestParseRunRef(t *testing.T) {
	tests := []struct {
		in   string
		want runRef
		ok   bool
	}{
		{"135", runRef{number: 135}, true},
		{"1", runRef{number: 1}, true},
		{"latest", runRef{latest: true}, true},
		{"latest~2", runRef{latest: true, back: 2}, true},
		{"latest~0", runRef{}, false},
		{"latest~x", runRef{}, false},
		{"0", runRef{}, false},
		{"-3", runRef{}, false},
		{"watch", runRef{}, false},
		{"bin/server", runRef{}, false},
	}
	for _, tt := range tests {
		got, ok := parseRunRef(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseRunRef(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestBeginRunConcurrent exercises the counter's flock under contention:
// every minted run number and log file must be distinct.
func TestBeginRunConcurrent(t *testing.T) {
	s := &binaryStore{dir: t.TempDir()}
	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	const workers = 20
	numbers := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, f, err := s.beginRun()
			if err != nil {
				t.Errorf("beginRun: %v", err)
				return
			}
			f.Close()
			numbers <- n
		}()
	}
	wg.Wait()
	close(numbers)

	seen := make(map[int]bool)
	for n := range numbers {
		if seen[n] {
			t.Errorf("run number %d minted twice", n)
		}
		seen[n] = true
	}
	for n := 1; n <= workers; n++ {
		if !seen[n] {
			t.Errorf("run number %d never minted", n)
		}
	}

	got, err := s.runNumbers()
	if err != nil || len(got) != workers {
		t.Errorf("runNumbers() = %v, %v; want %d entries", got, err, workers)
	}
}

// TestResolveRef checks run reference resolution against a recorded lineage.
func TestResolveRef(t *testing.T) {
	s := &binaryStore{dir: t.TempDir()}
	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 5, 9} {
		if err := os.WriteFile(s.runPath(n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		ref  runRef
		want int
		ok   bool
	}{
		{runRef{latest: true}, 9, true},
		{runRef{latest: true, back: 2}, 3, true},
		{runRef{latest: true, back: 3}, 0, false},
		{runRef{number: 5}, 5, true},
		{runRef{number: 4}, 0, false},
	}
	for _, tt := range tests {
		got, err := s.resolveRef(tt.ref)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("resolveRef(%+v) = %d, %v; want %d, ok=%v", tt.ref, got, err, tt.want, tt.ok)
		}
	}
}

// TestFirstAndLastLines checks header/trailer extraction, including a partial
// trailing line as left by a record mid-write.
func TestFirstAndLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	content := `{"t":"meta","run":7}` + "\n" +
		`{"t":"line","s":"hi"}` + "\n" +
		`{"t":"exit","reason":"quit"}` + "\n" +
		`{"t":"line","s":"partial` // no newline: mid-write
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	first, last, err := firstAndLastLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != `{"t":"meta","run":7}` {
		t.Errorf("first = %q", first)
	}
	if last != `{"t":"exit","reason":"quit"}` {
		t.Errorf("last = %q", last)
	}
}

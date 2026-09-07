package main

import (
	"os/exec"
	"strings"
	"testing"
)

// verbs is every verb dispatch routes to a flag set, as the flag set names it.
var verbs = []string{
	"watch", "show", "runs", "current", "next", "tail", "search", "info",
	"path", "diff", "crash", "status", "restart", "stop", "start", "quit", "signal",
	"loglevel",
}

func TestVerbDocsCoverEveryVerb(t *testing.T) {
	for _, v := range verbs {
		doc, ok := verbDocs[v]
		if !ok {
			t.Errorf("%s: no verbDoc", v)
			continue
		}
		if doc.summary == "" || len(doc.usage) == 0 || len(doc.examples) == 0 {
			t.Errorf("%s: doc needs a summary, usage, and examples", v)
		}
		for _, e := range doc.examples {
			if !strings.Contains(e.cmd, "makedog") {
				t.Errorf("%s: example %q does not invoke makedog", v, e.cmd)
			}
		}
	}
	for name := range verbDocs {
		found := false
		for _, v := range verbs {
			found = found || v == name
		}
		if !found {
			t.Errorf("verbDoc %q names no verb", name)
		}
	}
}

func TestVerbFlagsRecordAliases(t *testing.T) {
	type parsed struct {
		follow, json bool
		n            int
		binary       string
	}
	declare := func() (*verbFlags, *parsed) {
		var got parsed
		fs := newVerbFlags("x")
		fs.BoolVar(&got.follow, "follow it", "f", "follow")
		fs.IntVar(&got.n, "<count>", "how many", "n")
		fs.BoolVar(&got.json, "as json", "json", "jsonl")
		fs.StringVar(&got.binary, "<path>", "which", "binary")
		return fs, &got
	}

	for _, args := range [][]string{
		{"--follow", "-n", "3", "--jsonl", "--binary", "b"},
		{"-f", "-n", "3", "-json", "-binary", "b"},
	} {
		fs, got := declare()
		if err := fs.Parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if want := (parsed{true, true, 3, "b"}); *got != want {
			t.Errorf("%v: got %+v, want %+v", args, *got, want)
		}
	}

	fs, _ := declare()
	want := []flagRow{
		{[]string{"-f", "--follow"}, "", "follow it"},
		{[]string{"-n"}, "<count>", "how many"},
		{[]string{"--json", "--jsonl"}, "", "as json"},
		{[]string{"--binary"}, "<path>", "which"},
	}
	if len(fs.rows) != len(want) {
		t.Fatalf("rows: got %d, want %d", len(fs.rows), len(want))
	}
	for i, r := range fs.rows {
		if strings.Join(r.names, ",") != strings.Join(want[i].names, ",") || r.arg != want[i].arg || r.help != want[i].help {
			t.Errorf("row %d: got %+v, want %+v", i, r, want[i])
		}
	}
}

func TestWrap(t *testing.T) {
	got := wrap("aa bb cc dd", 5)
	if got != "aa bb\ncc dd" {
		t.Errorf("wrap: %q", got)
	}
}

// TestVerbHelpPages runs every verb's --help through the binary: each exits
// 0, shows its own options, and matches its `help <verb>` form.
func TestVerbHelpPages(t *testing.T) {
	bin := makedogBinary(t)
	for _, v := range verbs {
		args := []string{v, "--help"}
		if v == "show" {
			args = []string{"latest", "--help"}
		}
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Errorf("%s --help: %v\n%s", v, err, out)
			continue
		}
		page := string(out)
		if !strings.HasPrefix(page, "makedog "+v+":") {
			t.Errorf("%s --help: page starts %q", v, firstLine(page))
		}
		for _, want := range []string{"Usage:", "Examples:"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s --help: no %s section", v, want)
			}
		}
		if v != "watch" && !strings.Contains(page, "  -C <dir>") {
			t.Errorf("%s --help: -C missing from options", v)
		}
		if strings.Contains(page, "Read runs:") {
			t.Errorf("%s --help: printed the overview instead of its own page", v)
		}

		via, err := exec.Command(bin, "help", v).CombinedOutput()
		if err != nil {
			t.Errorf("help %s: %v\n%s", v, err, via)
		} else if string(via) != page {
			t.Errorf("help %s differs from %s --help", v, v)
		}
	}

	out, err := exec.Command(bin, "help", "grep").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "makedog search:") {
		t.Errorf("help grep: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin, "help", "bogus").CombinedOutput(); err == nil {
		t.Errorf("help bogus: succeeded\n%s", out)
	}
	if out, err := exec.Command(bin, "tail", "--bogus").CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "--bogus (try makedog tail --help)") {
		t.Errorf("tail --bogus: %v\n%s", err, out)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Tests for [loglevel] config parsing, level resolution, and listing.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadLoglevelConfig writes body to a temp config and loads it, returning the
// config and anything warned to stderr.
func loadLoglevelConfig(t *testing.T, body string) (*Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var config *Config
	stderr := captureStderr(t, func() { config = loadConfig(path) })
	return config, stderr
}

func TestLoglevelParse(t *testing.T) {
	config, stderr := loadLoglevelConfig(t, `
[loglevel]
env = "DEBUG"
debug.env = "app:*,http:*"
up.signal = "SIGUSR1"
mute.command = "curl -sX PUT localhost:8080/log/level -d off"
`)
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	lc := config.Loglevel
	if lc == nil {
		t.Fatal("loglevel config not parsed")
	}
	if lc.Env != "DEBUG" {
		t.Errorf("Env = %q; want DEBUG", lc.Env)
	}
	if got := lc.Levels["up"].Signal; got != "USR1" {
		t.Errorf("up.signal = %q; want USR1 (normalized)", got)
	}
	if want := []string{"debug", "up", "mute"}; strings.Join(lc.order, " ") != strings.Join(want, " ") {
		t.Errorf("order = %v; want %v", lc.order, want)
	}
}

func TestLoglevelParseErrors(t *testing.T) {
	cases := map[string]string{
		"two mechanisms":         "[loglevel]\ndebug.env = \"x\"\ndebug.signal = \"USR1\"\nenv = \"DEBUG\"\n",
		"unknown signal":         "[loglevel]\nup.signal = \"BOGUS\"\n",
		"env value without var":  "[loglevel]\ndebug.env = \"x\"\n",
		"command missing %s":     "[loglevel]\ncommand = \"curl localhost\"\n",
		"two transports":         "[loglevel]\nenv = \"DEBUG\"\ncommand = \"curl %s\"\n",
		"non-string env":         "[loglevel]\nenv = 3\n",
		"bare level not a table": "[loglevel]\ndebug = \"x\"\n",
		"command amid named":     "[loglevel]\ncommand = \"curl %s\"\nup.signal = \"USR1\"\n",
		"env unused amid named":  "[loglevel]\nenv = \"DEBUG\"\nup.signal = \"USR1\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			// The rest of the config must survive a bad [loglevel].
			config, stderr := loadLoglevelConfig(t, "[[signals]]\nsignal = \"HUP\"\n"+body)
			if !strings.Contains(stderr, "ignoring [loglevel]") {
				t.Errorf("no loglevel warning; stderr %q", stderr)
			}
			if config.Loglevel != nil {
				t.Error("bad loglevel config was kept")
			}
			if len(config.Signals) != 1 {
				t.Error("bad [loglevel] discarded unrelated config")
			}
		})
	}
}

func TestLoglevelResolve(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
env = "DEBUG"
debug.env = "app:*"
up.signal = "USR1"
mute.command = "curl -d off"
`)
	lc := config.Loglevel

	if p, err := lc.resolve("debug"); err != nil || p.env != "DEBUG=app:*" {
		t.Errorf("resolve(debug) = %+v, %v; want env DEBUG=app:*", p, err)
	}
	if p, _ := lc.resolve("up"); p.signal != "USR1" {
		t.Errorf("resolve(up) = %+v; want signal USR1", p)
	}
	if p, _ := lc.resolve("mute"); p.command != "curl -d off" {
		t.Errorf("resolve(mute) = %+v; want the command verbatim", p)
	}
	// Named levels close the vocabulary: no passthrough for absent words.
	if _, err := lc.resolve("trace"); err == nil || !strings.Contains(err.Error(), "debug, up, mute") {
		t.Errorf("resolve(trace) err = %v; want unknown level naming the defined ones", err)
	}
}

func TestLoglevelResolveEnvPassthrough(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
env = "DEBUG"
`)
	p, err := config.Loglevel.resolve("trace")
	if err != nil || p.env != "DEBUG=trace" {
		t.Errorf("resolve(trace) = %+v, %v; want env DEBUG=trace", p, err)
	}
}

func TestLoglevelResolveCommandPassthrough(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
command = "curl -d %s localhost"
`)
	lc := config.Loglevel

	p, err := lc.resolve("warn")
	if err != nil || p.command != "curl -d warn localhost" {
		t.Errorf("resolve(warn) = %+v, %v; want substituted command", p, err)
	}
	if _, err := lc.resolve("warn; rm -rf /"); err == nil {
		t.Error("shell-hostile level substituted into command")
	}
}

func TestLoglevelResolveUnknown(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
up.signal = "USR1"
down.signal = "USR2"
`)
	_, err := config.Loglevel.resolve("bogus")
	if err == nil || !strings.Contains(err.Error(), "up, down") {
		t.Errorf("resolve(bogus) err = %v; want the defined levels named", err)
	}
}

func TestLoglevelListing(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
env = "LOG_LEVEL"
debug.env = "verbose"
up.signal = "USR1"
`)
	rows := config.Loglevel.listing()
	if len(rows) != 2 {
		t.Fatalf("listing = %+v; want the two named levels, no passthrough row", rows)
	}
	if rows[0].Name != "debug" || rows[0].Via != "env" || rows[0].Detail != "LOG_LEVEL=verbose" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].Name != "up" || rows[1].Via != "signal" || rows[1].Detail != "USR1" {
		t.Errorf("row 1 = %+v", rows[1])
	}
}

func TestLoglevelListingPassthrough(t *testing.T) {
	config, _ := loadLoglevelConfig(t, `
[loglevel]
env = "LOG_LEVEL"
`)
	rows := config.Loglevel.listing()
	if len(rows) != 1 || rows[0].Name != "*" || rows[0].Via != "env" || rows[0].Detail != "LOG_LEVEL=<level>" {
		t.Errorf("listing = %+v; want just the passthrough row", rows)
	}
}

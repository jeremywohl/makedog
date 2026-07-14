// Tests for configuration file loading and parse-failure fallback.
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()

	fn()

	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("valid file", func(t *testing.T) {
		path := writeFile("good.toml", `
[[signals]]
signal = "SIGHUP"
name = "reload"
`)
		var config *Config
		stderr := captureStderr(t, func() { config = loadConfig(path) })
		if stderr != "" {
			t.Errorf("unexpected stderr: %q", stderr)
		}
		if len(config.Signals) != 1 || config.Signals[0].Signal != "SIGHUP" || config.Signals[0].Name != "reload" {
			t.Errorf("unexpected config: %+v", config)
		}
	})

	t.Run("missing file is silent", func(t *testing.T) {
		var config *Config
		stderr := captureStderr(t, func() { config = loadConfig(filepath.Join(dir, "absent.toml")) })
		if stderr != "" {
			t.Errorf("unexpected stderr: %q", stderr)
		}
		if len(config.Signals) != 0 {
			t.Errorf("expected empty config, got: %+v", config)
		}
	})

	t.Run("malformed file warns and falls back", func(t *testing.T) {
		path := writeFile("bad.toml", `signals = [ broken`)
		var config *Config
		stderr := captureStderr(t, func() { config = loadConfig(path) })
		if !strings.Contains(stderr, "makedog: ignoring config") || !strings.Contains(stderr, path) {
			t.Errorf("expected warning naming %s, got: %q", path, stderr)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Errorf("expected exactly one warning line, got: %q", stderr)
		}
		if len(config.Signals) != 0 {
			t.Errorf("expected default config, got: %+v", config)
		}
	})
}

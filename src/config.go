// Configuration file parsing for makedog.
package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// SignalConfig defines a signal that can be sent to the monitored process.
type SignalConfig struct {
	Signal string `toml:"signal"`
	Name   string `toml:"name"` // optional display name
}

// LogsConfig tunes run log retention. Pointers distinguish an absent field
// (take the default) from an explicit zero (disable the rule).
type LogsConfig struct {
	CompressAfter *string `toml:"compress_after"` // e.g. "24h", "7d"
	KeepRuns      *int    `toml:"keep_runs"`
	MaxAge        *string `toml:"max_age"`   // e.g. "90d"
	MaxTotal      *string `toml:"max_total"` // e.g. "512MB"
}

// Config holds the makedog configuration.
type Config struct {
	Signals []SignalConfig `toml:"signals"`
	Logs    *LogsConfig    `toml:"logs"`

	// The [loglevel] table mixes reserved keys with arbitrary level names, so
	// it lands raw here and parseLoglevel gives it shape.
	LoglevelTable map[string]toml.Primitive `toml:"loglevel"`
	Loglevel      *LoglevelConfig           `toml:"-"`
}

// loadConfig attempts to load config from the specified path, or .makedog.toml from
// the current directory if configPath is empty.
// Returns an empty config if the file doesn't exist; a parse failure warns and
// falls back to defaults rather than aborting the run.
func loadConfig(configPath string) *Config {
	config := &Config{}

	// Determine which file to load
	path := configPath
	if path == "" {
		path = ".makedog.toml"
	}

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return config
	}

	// Parse the TOML file
	md, err := toml.DecodeFile(path, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "makedog: ignoring config %s: %v\n", path, err)
		return &Config{}
	}

	// A bad [loglevel] disables that feature alone, not the whole config.
	if lc, err := parseLoglevel(md, config.LoglevelTable); err != nil {
		fmt.Fprintf(os.Stderr, "makedog: ignoring [loglevel] config: %v\n", err)
	} else {
		config.Loglevel = lc
	}
	config.LoglevelTable = nil

	return config
}

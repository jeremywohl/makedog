// Configuration file parsing for makedog.
package main

import (
	"os"

	"github.com/BurntSushi/toml"
)

// SignalConfig defines a signal that can be sent to the monitored process.
type SignalConfig struct {
	Signal string `toml:"signal"`
	Name   string `toml:"name"` // optional display name
}

// Config holds the makedog configuration.
type Config struct {
	Signals []SignalConfig `toml:"signals"`
}

// loadConfig attempts to load config from the specified path, or .makedog.toml from
// the current directory if configPath is empty.
// Returns an empty config if the file doesn't exist or cannot be parsed.
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
	if _, err := toml.DecodeFile(path, config); err != nil {
		// If we can't parse it, just return empty config and use defaults
		return &Config{}
	}

	return config
}

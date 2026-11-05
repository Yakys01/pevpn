// Package config defines upvpn's on-disk configuration.
//
// Only operational parameters live here: where the Brave profiles are, which
// providers to load and how the selector behaves. Provider internals (scheme,
// port, how credentials are read) stay in their own package.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Provider selects an upstream provider and optionally overrides where its
// extension storage lives.
type Provider struct {
	Name        string `json:"name"`
	ProfileDir  string `json:"profileDir,omitempty"`
	ExtensionID string `json:"extensionId,omitempty"`
}

// Selector tunes how targets are picked per connection.
type Selector struct {
	Strategy           string `json:"strategy,omitempty"`
	MaxFails           int    `json:"maxFails,omitempty"`
	FailTimeoutSeconds int    `json:"failTimeoutSeconds,omitempty"`
}

// Config is the whole file.
type Config struct {
	Bind      string     `json:"bind,omitempty"`
	Port      int        `json:"port,omitempty"`
	Providers []Provider `json:"providers,omitempty"`
	Selector  Selector   `json:"selector,omitempty"`
}

// Default returns the config used when no file exists.
func Default() Config {
	return Config{
		Bind: "127.0.0.1",
		Port: 8888,
		Providers: []Provider{
			{Name: "urbanvpn"},
			{Name: "vpnbrowser"},
		},
		Selector: Selector{
			Strategy:           "round-robin",
			MaxFails:           1,
			FailTimeoutSeconds: 30,
		},
	}
}

// Path returns the default config path (XDG aware).
func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "upvpn", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "upvpn", "config.json")
}

// Load reads the config at path, falling back to Default for missing fields
// and for a missing file.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.fill()
	return cfg, nil
}

func (c *Config) fill() {
	def := Default()
	if c.Bind == "" {
		c.Bind = def.Bind
	}
	if c.Port == 0 {
		c.Port = def.Port
	}
	if len(c.Providers) == 0 {
		c.Providers = def.Providers
	}
	if c.Selector.Strategy == "" {
		c.Selector.Strategy = def.Selector.Strategy
	}
	if c.Selector.MaxFails <= 0 {
		c.Selector.MaxFails = def.Selector.MaxFails
	}
	if c.Selector.FailTimeoutSeconds <= 0 {
		c.Selector.FailTimeoutSeconds = def.Selector.FailTimeoutSeconds
	}
}

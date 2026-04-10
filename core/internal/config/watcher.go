package config

import (
	"log"
	"os"
	"sync"
	"time"
)

// ConfigWatcher polls config files for changes and hot-reloads.
// Teams pulling new .curb.yml changes get immediate enforcement.
type ConfigWatcher struct {
	paths    []string
	current  *Config
	mu       sync.RWMutex
	onChange func(*Config)
	stop     chan struct{}
}

// NewConfigWatcher creates a watcher that polls the given config paths.
func NewConfigWatcher(paths []string, onChange func(*Config)) *ConfigWatcher {
	cfg, err := LoadConfig(paths...)
	if err != nil {
		log.Printf("[Curb] Initial config load warning: %v", err)
		cfg = &Config{Version: 1, MCP: MCPConfig{Servers: map[string]MCPServer{}}}
	}

	return &ConfigWatcher{
		paths:    paths,
		current:  cfg,
		onChange: onChange,
		stop:     make(chan struct{}),
	}
}

// Current returns the latest loaded config (thread-safe).
func (w *ConfigWatcher) Current() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current
}

// Start begins polling for config file changes every 2 seconds.
func (w *ConfigWatcher) Start() {
	modTimes := make(map[string]time.Time)
	for _, p := range w.paths {
		if info, err := os.Stat(p); err == nil {
			modTimes[p] = info.ModTime()
		}
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				changed := false
				for _, p := range w.paths {
					info, err := os.Stat(p)
					if err != nil {
						continue
					}
					prev, exists := modTimes[p]
					if !exists || !info.ModTime().Equal(prev) {
						modTimes[p] = info.ModTime()
						changed = true
					}
				}

				if changed {
					cfg, err := LoadConfig(w.paths...)
					if err != nil {
						log.Printf("[Curb] Config reload error: %v", err)
						continue
					}

					w.mu.Lock()
					w.current = cfg
					w.mu.Unlock()

					log.Println("[Curb] Config reloaded — new rules are active")
					if w.onChange != nil {
						w.onChange(cfg)
					}
				}
			}
		}
	}()
}

// SetCurrent immediately updates the watcher's internal config state.
// Use this after saving config to disk so that subsequent RPC reads
// return the freshly saved config without waiting for the poll cycle.
func (w *ConfigWatcher) SetCurrent(cfg *Config) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.current = cfg
}

// Stop the watcher loop.
func (w *ConfigWatcher) Stop() {
	close(w.stop)
}

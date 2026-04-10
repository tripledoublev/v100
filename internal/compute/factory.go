package compute

import (
	"fmt"
	"time"
)

// Config is the deserialized [compute] section from research.toml.
type Config struct {
	Provider    string `toml:"provider"`
	GPU         string `toml:"gpu"`
	Image       string `toml:"image"`
	Timeout     string `toml:"timeout"`      // duration string e.g. "30m"
	ModalSecret string `toml:"modal_secret"` // named Modal secret for env injection
}

// Build creates a Provider from cfg.
// An empty or "local" provider returns a LocalProvider (backward-compatible default).
func Build(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "", "local":
		return NewLocalProvider(), nil
	case "modal":
		var timeout time.Duration
		if cfg.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(cfg.Timeout)
			if err != nil {
				return nil, fmt.Errorf("compute: invalid timeout %q: %w", cfg.Timeout, err)
			}
		}
		return NewModalProvider(ModalConfig{
			GPU:         cfg.GPU,
			Image:       cfg.Image,
			Timeout:     timeout,
			ModalSecret: cfg.ModalSecret,
		}), nil
	default:
		return nil, fmt.Errorf("compute: unknown provider %q (supported: local, modal)", cfg.Provider)
	}
}

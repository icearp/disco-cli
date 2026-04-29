package cmd

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// configDir returns the XDG config directory for disco config files.
// Linux: $XDG_CONFIG_HOME/disco (~/.config/disco)
// macOS: ~/Library/Application Support/disco
// Windows: %LOCALAPPDATA%\disco
func configDir() string {
	return filepath.Join(xdg.ConfigHome, "disco")
}

// dataDir returns the XDG data directory for disco state (default DB path).
// Linux: $XDG_DATA_HOME/disco (~/.local/share/disco)
// macOS: ~/Library/Application Support/disco
// Windows: %LOCALAPPDATA%\disco
func dataDir() string {
	return filepath.Join(xdg.DataHome, "disco")
}

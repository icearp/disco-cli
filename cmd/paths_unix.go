//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
)

// discoDir returns the platform-specific directory for disco config and data.
// On Linux and macOS: $HOME/.config/disco
func discoDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "disco"
	}
	return filepath.Join(home, ".config", "disco")
}

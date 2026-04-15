//go:build windows

package cmd

import (
	"os"
	"path/filepath"
)

// discoDir returns the platform-specific directory for disco config and data.
// On Windows: %LOCALAPPDATA%\disco (falls back to %USERPROFILE%\AppData\Local\disco)
func discoDir() string {
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "disco")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "disco"
	}
	return filepath.Join(home, "AppData", "Local", "disco")
}

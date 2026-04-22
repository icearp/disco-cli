package cmd

// Provider packages register themselves via init() by calling providers.Register.
// Each blank import triggers the package's init(), which registers its Scanner
// and makes it available as a "disco scan <name>" subcommand.

import (
	_ "codeberg.org/icearp/disco/internal/providers/aws"
	_ "codeberg.org/icearp/disco/internal/providers/azure"
	_ "codeberg.org/icearp/disco/internal/providers/gcp"
)

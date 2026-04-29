package cmd

import (
	// Provider packages self-register their Scanner via init() so the
	// blank imports here are what wires `disco scan <name>` subcommands.
	_ "codeberg.org/icearp/disco/internal/providers/aws"
	_ "codeberg.org/icearp/disco/internal/providers/azure"
	_ "codeberg.org/icearp/disco/internal/providers/gcp"
)

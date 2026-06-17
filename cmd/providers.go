package cmd

import (
	// Providers self-wire through the internal/providers/all aggregator: it
	// blank-imports each provider's `enable` subpackage, which registers the
	// provider's Scanner via init(). cmd names no provider directly, so the set
	// of compiled providers is controlled entirely under
	// internal/providers/<provider> (default = all; `-tags 'slim aws'` = aws
	// only — see internal/providers/all).
	_ "codeberg.org/icearp/disco/internal/providers/all"
)

package cmd

import (
	// Providers self-wire via internal/providers/all: it blank-imports each
	// provider's `enable` subpackage, which registers the provider's Scanner
	// via init(). cmd names no provider directly — compiled providers are
	// controlled entirely under internal/providers/<provider> (default = all;
	// `-tags 'slim aws'` = aws only; see internal/providers/all).
	_ "github.com/icearp/disco-cli/internal/providers/all"
)

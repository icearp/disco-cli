// Package all is the single wiring point that registers every provider's
// supported-region list into the github.com/icearp/disco-cli/regions registry for
// standalone consumers (e.g. the SaaS control plane) that do not import the
// providers themselves.
//
// One build-tagged file per provider (aws.go etc.) blank-imports that provider's
// SDK-free <p>regions leaf package, gated on `//go:build !slim || <provider>`:
// a default build registers every provider, while `-tags 'slim aws'` registers
// only the named ones — matching internal/providers/all so the two stay in sync.
// Each gate references only `slim` plus its own provider tag, never siblings.
//
// The disco binary already triggers registration transitively (each provider
// package imports its own <p>regions leaf), so it never needs this package;
// it exists for callers that link regions without the providers.
//
// This file carries the package clause with no imports so the package stays
// importable when every provider is tagged out (`-tags slim` alone).
package all

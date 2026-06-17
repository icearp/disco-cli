// Package all is the single wiring point that pulls cloud providers into the
// binary. cmd blank-imports this package and never names a provider directly,
// so provider selection stays out of cmd.
//
// One build-tagged file per provider (all_aws.go etc.) blank-imports that
// provider's package, gated on `//go:build !slim || <provider>`: a default
// build compiles every provider, while `-tags 'slim aws'` compiles only the
// named ones — excluded providers' SDKs are never linked. Each gate references
// only `slim` plus its own provider tag, never siblings, so providers stay
// decoupled. Adding a provider is one tagged file here.
//
// This file carries the package clause with no imports so the package stays
// importable when every provider is tagged out (`-tags slim` alone) — cmd's
// blank import of it must still resolve.
package all

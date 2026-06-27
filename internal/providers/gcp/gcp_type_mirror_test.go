package gcp

import "testing"

// TestGCPTypeMirrorsDiscovery is the regression ratchet for the disco-type
// naming rule: every aliased GCP type mirrors its Discovery resource name, so
// AlgorithmicKey (kebab -> Pascal, "<svc>.googleapis.com/<Resource>")
// reconstructs the alias's upstream key exactly. A rename or abbreviation
// relative to Discovery (e.g. keyring for KeyRing) fails here.
func TestGCPTypeMirrorsDiscovery(t *testing.T) {
	p := coverageProvider{}
	for disco, up := range p.Aliases() {
		if got := p.AlgorithmicKey(disco); got != up {
			t.Errorf("AlgorithmicKey(%q) = %q; want %q", disco, got, up)
		}
	}
}

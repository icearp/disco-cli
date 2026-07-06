package azure

import (
	"slices"
	"testing"
)

// TestAzureTypeMirrorsARM is the regression ratchet for the disco-type naming
// rule: every disco type mirrors its upstream ARM resource-type key.
// AlgorithmicKey (strip kebab dashes, turn ':' sub-resource separators into
// the ARM '/' hierarchy) must reconstruct one of the ARM keys the type is
// registered under. A new Azure type whose string drifts from upstream — an
// added hyphen the ARM key lacks, a '/' instead of ':', wrong singular/plural
// — fails here. Where one disco type intentionally collapses several ARM
// types (DNS record-sets), reconstruction can't match a single key, so those
// are skipped.
func TestAzureTypeMirrorsARM(t *testing.T) {
	armKeysByType := map[string][]string{}
	for arm, disco := range azureAPITypeMap {
		armKeysByType[disco] = append(armKeysByType[disco], arm)
	}
	collapsed := map[string]bool{
		TypeDNSRecordSet:        true,
		TypeDNSPrivateRecordSet: true,
	}
	p := coverageProvider{}
	for disco, arms := range armKeysByType {
		if collapsed[disco] {
			continue
		}
		if got := p.AlgorithmicKey(disco); !slices.Contains(arms, got) {
			t.Errorf("AlgorithmicKey(%q) = %q; want one of %v", disco, got, arms)
		}
	}
}

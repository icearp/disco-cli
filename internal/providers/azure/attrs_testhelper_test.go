package azure

import (
	"encoding/json"
	"testing"
)

// marshalAttrs returns the JSON encoding of v as the attrsJSON value scanners
// would persist to the store. Tests use this with real SDK structs (e.g.
// armcompute.Disk, armnetwork.VirtualNetwork) so that schema drift between
// SDK upgrades surfaces as a Go compile error rather than a silent
// resolver edge-loss when hand-rolled JSON literals fall out of sync.
func marshalAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalAttrs: %v", err)
	}
	return string(b)
}

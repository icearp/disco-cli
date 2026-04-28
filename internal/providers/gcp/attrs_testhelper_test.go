package gcp

import (
	"encoding/json"
	"testing"
)

// marshalAttrs returns the JSON encoding of v as the attrsJSON value scanners
// would persist to the store. Tests use this with real SDK structs (e.g.
// compute.Instance, compute.Firewall) so that JSON-tag drift between
// google.golang.org/api/* SDK upgrades surfaces as a Go compile error rather
// than a silent resolver edge-loss when hand-rolled JSON literals fall out of
// sync with the discovery document.
func marshalAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalAttrs: %v", err)
	}
	return string(b)
}

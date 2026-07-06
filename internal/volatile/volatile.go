// Package volatile drops provider-declared volatile fields from resource
// AttributesJSON at the store boundary, BEFORE the version-comparison in
// Store.UpsertResources.
//
// Some cloud APIs return fields that change on every read independent of any
// real resource change — e.g. CloudWatch Logs returns a fresh, deprecated
// UploadSequenceToken on every DescribeLogStreams call. Storing such a field
// version-splits an otherwise-unchanged resource on every scan, falsely
// reporting it as "changed". Unlike redaction (which replaces a value with
// "[REDACTED]"), volatile rules REMOVE the key entirely, so stored attributes
// honestly reflect only the fields disco tracks — no stale value left behind.
//
// Provider packages register per-type rules in their init() blocks (see
// internal/providers/aws/aws_volatile.go). Store.UpsertResources calls Apply
// once per row, right after redact.Apply and before the jsonEqual comparison.
package volatile

import (
	"encoding/json"
	"strings"
	"sync"
)

// TypeRules names every volatile JSON path to drop for one resource type.
// Paths are dot-separated literal keys (e.g. "A.B.C"); the leaf key is
// deleted from its parent object. No wildcards — today's drops are flat
// keys; extend if a nested-array volatile field appears.
type TypeRules struct {
	Type  string
	Paths []string
}

var (
	regMu    sync.RWMutex
	registry = map[string][][]string{}
)

// Register installs r in the registry. Subsequent Register calls for the same
// Type append paths, mirroring redact.Register.
func Register(r TypeRules) {
	if r.Type == "" || len(r.Paths) == 0 {
		return
	}
	compiled := make([][]string, 0, len(r.Paths))
	for _, p := range r.Paths {
		if p == "" {
			continue
		}
		compiled = append(compiled, strings.Split(p, "."))
	}
	if len(compiled) == 0 {
		return
	}
	regMu.Lock()
	registry[r.Type] = append(registry[r.Type], compiled...)
	regMu.Unlock()
}

// HasRules reports whether any volatile path is registered for resourceType.
func HasRules(resourceType string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := registry[resourceType]
	return ok
}

// Apply removes every registered volatile path from attributesJSON, returning
// input unchanged when no rules exist or JSON parse/marshal fails — callers
// never want a silently dropped row.
func Apply(resourceType, attributesJSON string) string {
	regMu.RLock()
	paths := registry[resourceType]
	regMu.RUnlock()
	if attributesJSON == "" || len(paths) == 0 {
		return attributesJSON
	}
	var v any
	if err := json.Unmarshal([]byte(attributesJSON), &v); err != nil {
		return attributesJSON
	}
	for _, segs := range paths {
		dropPath(v, segs)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return attributesJSON
	}
	return string(out)
}

// dropPath navigates v down segs[0:len-1] (each must be an object) and deletes
// the leaf key segs[len-1]. A missing or non-object segment is a no-op.
func dropPath(v any, segs []string) {
	m, ok := v.(map[string]any)
	if !ok || len(segs) == 0 {
		return
	}
	if len(segs) == 1 {
		delete(m, segs[0])
		return
	}
	dropPath(m[segs[0]], segs[1:])
}

// resetForTest wipes the registry. Test-only.
func resetForTest() {
	regMu.Lock()
	registry = map[string][][]string{}
	regMu.Unlock()
}

package store

import (
	"reflect"
	"strings"
	"testing"
)

// TestResource_NoVersioningFields locks the type-separation rule: the
// `Resource` struct stays the base row type. Versioning-only state belongs
// on ResourceVersion (resources_versioning.go) via embedding so future
// additions to Resource cascade for free.
//
// A future PR that adds a `db:"verified_at"` (or similar versioning-only
// tag) to Resource regresses the split. This test fails the build.
func TestResource_NoVersioningFields(t *testing.T) {
	forbidden := map[string]struct{}{
		"verified_at":         {},
		"verified_by":         {},
		"root_id":             {},
		"previous_version_id": {},
		"superseded_by":       {},
		"version_row_id":      {},
	}
	rt := reflect.TypeOf(Resource{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := f.Tag.Get("db")
		// db tags may carry options (e.g. "name,omitempty"); split on
		// comma so we compare the column name only.
		col := tag
		if idx := strings.IndexByte(col, ','); idx >= 0 {
			col = col[:idx]
		}
		if _, bad := forbidden[col]; bad {
			t.Errorf("Resource.%s (db:%q) is a versioning-only field — move to ResourceVersion in resources_versioning.go", f.Name, col)
		}
	}
}

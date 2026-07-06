package azure

import "testing"

// TestAzTagsJSON pins the faithful-serialization contract: stored tags must
// mirror the Azure API response — null values preserved, empty non-nil map
// serialized as "{}", nil only for a genuinely nil map. mustJSON sorts keys,
// so multi-key output is deterministic.
func TestAzTagsJSON(t *testing.T) {
	sp := func(s string) *string { return &s }
	cases := []struct {
		name string
		tags map[string]*string
		want *string // nil means "expect a nil pointer"
	}{
		{"nil map → nil", nil, nil},
		{"empty non-nil map → {}", map[string]*string{}, sp("{}")},
		{"null value preserved", map[string]*string{"k": nil}, sp(`{"k":null}`)},
		{"string value", map[string]*string{"env": sp("prod")}, sp(`{"env":"prod"}`)},
		{"multi-key sorted", map[string]*string{"team": sp("core"), "env": sp("prod")}, sp(`{"env":"prod","team":"core"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := azTagsJSON(tc.tags)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("azTagsJSON(%v) = %q; want nil", tc.tags, *got)
			case tc.want != nil && got == nil:
				t.Errorf("azTagsJSON(%v) = nil; want %q", tc.tags, *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Errorf("azTagsJSON(%v) = %q; want %q", tc.tags, *got, *tc.want)
			}
		})
	}
}

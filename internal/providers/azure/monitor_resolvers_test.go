package azure

import (
	"testing"
)

// TestEventHubNamespaceFromAuthRule verifies the auth-rule → namespace ID
// trim handles canonical case, mixed case (caller already lowercases), and
// the "no auth rule segment" no-op.
func TestEventHubNamespaceFromAuthRule(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantOK  bool
		comment string
	}{
		{
			in:      "/subscriptions/abc/resourcegroups/rg/providers/microsoft.eventhub/namespaces/ns/authorizationrules/r",
			want:    "/subscriptions/abc/resourcegroups/rg/providers/microsoft.eventhub/namespaces/ns",
			wantOK:  true,
			comment: "canonical lowercase",
		},
		{
			in:      "/subscriptions/abc/.../namespaces/ns",
			want:    "",
			wantOK:  false,
			comment: "no auth-rule segment → no namespace extractable",
		},
		{
			in:      "",
			want:    "",
			wantOK:  false,
			comment: "empty input",
		},
	}
	for _, tt := range tests {
		got, ok := eventHubNamespaceFromAuthRule(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", tt.comment, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestStrLower confirms nil-safe behaviour.
func TestStrLower(t *testing.T) {
	s := "MIXED"
	if got := strLower(&s); got != "mixed" {
		t.Errorf("strLower(MIXED): %q want mixed", got)
	}
	if got := strLower(nil); got != "" {
		t.Errorf("strLower(nil): %q want empty", got)
	}
	empty := ""
	if got := strLower(&empty); got != "" {
		t.Errorf("strLower(empty-ptr): %q want empty", got)
	}
}

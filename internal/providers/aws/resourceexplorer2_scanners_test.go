package aws

import "testing"

func TestRE2ViewName(t *testing.T) {
	cases := []struct {
		arn  string
		want string
	}{
		// Default view: name equals the region.
		{"arn:aws:resource-explorer-2:us-west-2:439071338928:view/us-west-2/4a672490-2a60-4dad-99a4-b8e194c0ca97", "us-west-2"},
		// Customer view: chosen name.
		{"arn:aws:resource-explorer-2:us-east-1:439071338928:view/my-view/11111111-2222-3333-4444-555555555555", "my-view"},
		// Unexpected shape falls back to the full ARN.
		{"arn:aws:resource-explorer-2:us-east-1:439071338928:index/abc", "arn:aws:resource-explorer-2:us-east-1:439071338928:index/abc"},
	}
	for _, c := range cases {
		if got := re2ViewName(c.arn); got != c.want {
			t.Errorf("re2ViewName(%q) = %q, want %q", c.arn, got, c.want)
		}
	}
}

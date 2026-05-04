package main

import "testing"

// TestClassifyBareIDPrecedence locks in the prefix-ordering rules in
// classifyBareID. Several AWS bare-ID prefixes share an initial substring
// (`igw-` ⊃ `i-`, `tgw-attach-` ⊃ `tgw-`, `fsmt-` ⊃ `fs-`); the switch must
// match the longer/more-specific prefix first.
func TestClassifyBareIDPrecedence(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"i-0123456789abcdef0", "aws:ec2:instance"},
		{"igw-0123456789abcdef0", "aws:ec2:internet-gateway"},
		{"tgw-0a1b2c3d4e5f6a7b8", "aws:ec2:transit-gateway"},
		{"tgw-attach-0123456789abc", "aws:ec2:transit-gateway-attachment"},
		{"tgw-rtb-0123456789abc", "aws:ec2:transit-gateway-route-table"},
		{"tgw-mc-0123456789abc", "aws:ec2:transit-gateway-multicast-domain"},
		{"fs-01234567", "aws:efs:file-system"},
		{"fsmt-01234567", "aws:efs:mount-target"},
		{"fsap-01234567", "aws:efs:access-point"},
		{"vpce-01234567", "aws:ec2:vpc-endpoint"},
		{"unknown-prefix-x", ""},
	}
	for _, c := range cases {
		got := classifyBareID(c.id)
		if got != c.want {
			t.Errorf("classifyBareID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

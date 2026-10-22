package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveIpamPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")
	polARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-policy/ipam-pol-1"
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamPolicy, polARN, region, `{"IpamId":"ipam-aaa"}`)

	if err := resolveIpamPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveIpamPolicyRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(polID)
	assertRelationship(t, rels, polID, ipamID, store.RelAttachedTo)
}

func TestResolveIpamPolicyRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	polARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-policy/ipam-pol-1"
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamPolicy, polARN, region, "{}")
	if err := resolveIpamPolicyRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(polID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveIpamVerificationTokenRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")
	tokARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-external-resource-verification-token/ipam-ervt-1"
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamExternalResourceVerificationToken, tokARN, region, `{"IpamId":"ipam-aaa"}`)

	if err := resolveIpamVerificationTokenRelationships(acct, st); err != nil {
		t.Fatalf("resolveIpamVerificationTokenRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tokID)
	assertRelationship(t, rels, tokID, ipamID, store.RelAttachedTo)
}

// A row whose attributes do not parse is reported, and the rows listed before
// and after it are still wired. Names a, b, c fix the listing order
// (ListResources orders by type, then name).
func TestResolveIpamChildRelationships_BadAttrsReportedOthersWired(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		kind    string
		attrs   string
		resolve func(*account, *store.Store) error
	}{
		{
			"policy", TypeEC2IpamPolicy, "ipam-policy",
			mustJSON(ec2types.IpamPolicy{IpamId: sdkaws.String("ipam-aaa")}), resolveIpamPolicyRelationships,
		},
		{
			"verification-token", TypeEC2IpamExternalResourceVerificationToken, "ipam-external-resource-verification-token",
			mustJSON(ec2types.IpamExternalResourceVerificationToken{IpamId: sdkaws.String("ipam-aaa")}), resolveIpamVerificationTokenRelationships,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ec2ARN(testRegion, acct.ID, "ipam", "ipam-aaa"), testRegion, "{}")
			childARN := func(n string) string { return ec2ARN(testRegion, acct.ID, tc.kind, n) }
			before := upsertTestResourceNamed(t, st, tc.typ, childARN("a"), testRegion, tc.attrs, "a")
			upsertTestResourceNamed(t, st, tc.typ, childARN("b"), testRegion, `{"IpamId":7}`, "b")
			after := upsertTestResourceNamed(t, st, tc.typ, childARN("c"), testRegion, tc.attrs, "c")

			var typeErr *json.UnmarshalTypeError
			if err := tc.resolve(acct, st); !errors.As(err, &typeErr) {
				t.Errorf("err = %v, want a JSON type error", err)
			}
			for _, id := range []string{before, after} {
				rels, err := st.RelationshipsFrom(id)
				if err != nil {
					t.Fatalf("RelationshipsFrom: %v", err)
				}
				assertRelationship(t, rels, id, ipamID, store.RelAttachedTo)
			}
		})
	}
}

func TestResolveIpamVerificationTokenRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	tokARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-external-resource-verification-token/ipam-ervt-1"
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamExternalResourceVerificationToken, tokARN, region, "{}")
	if err := resolveIpamVerificationTokenRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(tokID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

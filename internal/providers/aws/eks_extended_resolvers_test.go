package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestEKSClusterNameFromChildARN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:eks:us-east-1:123:addon/prod/vpc-cni", "prod"},
		{"arn:aws:eks:us-east-1:123:nodegroup/prod/ng1/abc-uuid", "prod"},
		{"arn:aws:eks:us-east-1:123:podidentityassociation/prod/a-1234", "prod"},
		{"arn:aws:eks:us-east-1:123:cluster/prod", "prod"},
		{"arn:aws:eks:us-east-1:123:fargateprofile/staging/fp1", "staging"},
		{"", ""},
		{"not-an-arn", ""},
	}
	for _, c := range cases {
		if got := eksClusterNameFromChildARN(c.in); got != c.want {
			t.Errorf("eksClusterNameFromChildARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveEKSChildrenToCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/prod", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, cARN, testRegion, "{}")

	aeARN := fmt.Sprintf("arn:aws:eks:%s:%s:access-entry/prod/arn:aws:iam::%s:role/eks-admin", testRegion, acct.ID, acct.ID)
	aeID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSAccessEntry, aeARN, testRegion, "{}")

	addonARN := fmt.Sprintf("arn:aws:eks:%s:%s:addon/prod/vpc-cni", testRegion, acct.ID)
	addonID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSAddon, addonARN, testRegion, "{}")

	fpARN := fmt.Sprintf("arn:aws:eks:%s:%s:fargateprofile/prod/fp1", testRegion, acct.ID)
	fpID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSFargateProfile, fpARN, testRegion, "{}")

	ngARN := fmt.Sprintf("arn:aws:eks:%s:%s:nodegroup/prod/ng1/abc-uuid", testRegion, acct.ID)
	ngID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSNodegroup, ngARN, testRegion, "{}")

	piARN := fmt.Sprintf("arn:aws:eks:%s:%s:podidentityassociation/prod/a-xxx", testRegion, acct.ID)
	piID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSPodIdentityAssociation, piARN, testRegion, "{}")

	if err := resolveEKSChildrenToCluster(acct, st); err != nil {
		t.Fatalf("resolveEKSChildrenToCluster: %v", err)
	}
	for _, child := range []string{aeID, addonID, fpID, ngID, piID} {
		rels, _ := st.RelationshipsFrom(child)
		assertRelationship(t, rels, child, cID, store.RelAttachedTo)
	}
}

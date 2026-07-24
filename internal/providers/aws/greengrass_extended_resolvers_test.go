package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestGreengrassVersionParent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:greengrass:us-east-1:123:/greengrass/definition/cores/c1/versions/v1", "arn:aws:greengrass:us-east-1:123:/greengrass/definition/cores/c1"},
		{"arn:aws:greengrass:us-east-1:123:/greengrass/groups/g1/versions/v1", "arn:aws:greengrass:us-east-1:123:/greengrass/groups/g1"},
		{"arn:aws:greengrass:us-east-1:123:/greengrass/groups/g1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := greengrassVersionParent(c.in); got != c.want {
			t.Errorf("greengrassVersionParent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveGreengrassVersionsToParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cdARN := fmt.Sprintf("arn:aws:greengrass:%s:%s:/greengrass/definition/cores/c1", testRegion, acct.ID)
	cdID := upsertTestResource(t, st, "aws", acct.ID, TypeGreengrassCoreDefinition, cdARN, testRegion, "{}")
	cdvARN := cdARN + "/versions/v1"
	cdvID := upsertTestResource(t, st, "aws", acct.ID, TypeGreengrassCoreDefinitionVersion, cdvARN, testRegion, "{}")
	gARN := fmt.Sprintf("arn:aws:greengrass:%s:%s:/greengrass/groups/g1", testRegion, acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeGreengrassGroup, gARN, testRegion, "{}")
	gvARN := gARN + "/versions/v1"
	gvID := upsertTestResource(t, st, "aws", acct.ID, TypeGreengrassGroupVersion, gvARN, testRegion, "{}")
	if err := resolveGreengrassVersionsToParent(acct, st); err != nil {
		t.Fatalf("resolveGreengrassVersionsToParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cdvID)
	assertRelationship(t, rels, cdvID, cdID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(gvID)
	assertRelationship(t, rels, gvID, gID, store.RelAttachedTo)
}

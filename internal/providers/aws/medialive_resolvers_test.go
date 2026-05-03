package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveMediaLiveChannelRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/medialive", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	inputARN := fmt.Sprintf("arn:aws:medialive:%s:%s:input/in-1", testRegion, acct.ID)
	inputID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInput, inputARN, testRegion, `{"Id":"in-1"}`)
	chARN := fmt.Sprintf("arn:aws:medialive:%s:%s:channel:ch-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Id":"ch-1","RoleArn":%q,"InputAttachments":[{"InputId":"in-1"}]}`, roleARN)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveChannel, chARN, testRegion, attrs)
	if err := resolveMediaLiveChannelRefs(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveChannelRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(chID)
	assertRelationship(t, rels, chID, roleID, store.RelAssumes)
	assertRelationship(t, rels, chID, inputID, store.RelUses)
}

func TestResolveMediaLiveInputSecurityGroups(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	isgARN := fmt.Sprintf("arn:aws:medialive:%s:%s:inputsg/isg-1", testRegion, acct.ID)
	isgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInputSecurityGroup, isgARN, testRegion, `{"Id":"isg-1"}`)
	inputARN := fmt.Sprintf("arn:aws:medialive:%s:%s:input/in-2", testRegion, acct.ID)
	inputID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInput, inputARN, testRegion, `{"Id":"in-2","SecurityGroups":["isg-1"]}`)
	if err := resolveMediaLiveInputSecurityGroups(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveInputSecurityGroups: %v", err)
	}
	rels, _ := st.RelationshipsFrom(inputID)
	assertRelationship(t, rels, inputID, isgID, store.RelUses)
}

func TestResolveMediaLiveChannelPlacementGroupCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:medialive:%s:%s:cluster/cl-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveCluster, clARN, testRegion, `{"Id":"cl-1"}`)
	pgARN := fmt.Sprintf("arn:aws:medialive:%s:%s:cpg/pg-1", testRegion, acct.ID)
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveChannelPlacementGroup, pgARN, testRegion, `{"Id":"pg-1","ClusterId":"cl-1"}`)
	if err := resolveMediaLiveChannelPlacementGroupCluster(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveChannelPlacementGroupCluster: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pgID)
	assertRelationship(t, rels, pgID, clID, store.RelAttachedTo)
}

func TestResolveMediaLiveMultiplexProgramParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	mxARN := fmt.Sprintf("arn:aws:medialive:%s:%s:multiplex:mx-1", testRegion, acct.ID)
	mxID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveMultiplex, mxARN, testRegion, `{"Id":"mx-1"}`)
	mpARN := fmt.Sprintf("arn:aws:medialive:%s:%s:multiplexprogram/mx-1/prog-A", testRegion, acct.ID)
	mpID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveMultiplexProgram, mpARN, testRegion, "{}")
	if err := resolveMediaLiveMultiplexProgramParent(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveMultiplexProgramParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mpID)
	assertRelationship(t, rels, mpID, mxID, store.RelAttachedTo)
}

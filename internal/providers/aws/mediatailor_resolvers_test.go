package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveMediaTailorChannelPolicyToChannel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	chARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:channel/c1", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorChannel, chARN, testRegion, "{}")
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorChannelPolicy, chARN+"/policy", testRegion, "{}")
	if err := resolveMediaTailorChannelPolicyToChannel(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorChannelPolicyToChannel: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, chID, store.RelAttachedTo)
}

func TestResolveMediaTailorSourcesToSourceLocation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	slARN := mtSourceLocationARN(testRegion, acct.ID, "sl1")
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorSourceLocation, slARN, testRegion, "{}")
	lsARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:liveSource/sl1/ls1", testRegion, acct.ID)
	lsID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorLiveSource, lsARN, testRegion, `{"SourceLocationName":"sl1"}`)
	vsARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:vodSource/sl1/v1", testRegion, acct.ID)
	vsID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorVodSource, vsARN, testRegion, `{"SourceLocationName":"sl1"}`)
	if err := resolveMediaTailorSourcesToSourceLocation(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorSourcesToSourceLocation: %v", err)
	}
	for _, c := range []string{lsID, vsID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, slID, store.RelAttachedTo)
	}
}

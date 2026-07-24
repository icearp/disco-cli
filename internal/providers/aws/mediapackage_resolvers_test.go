package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveMPV1OriginEndpointToChannel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	chARN := mediapackageARN(testRegion, acct.ID, "channels", "ch1")
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageChannel, chARN, testRegion, "{}")
	epARN := mediapackageARN(testRegion, acct.ID, "origin_endpoints", "ep1")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageOriginEndpoint, epARN, testRegion, `{"ChannelId":"ch1"}`)
	if err := resolveMPV1OriginEndpointToChannel(acct, st); err != nil {
		t.Fatalf("resolveMPV1OriginEndpointToChannel: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, chID, store.RelAttachedTo)
}

func TestResolveMPV1AssetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pgARN := mediapackageARN(testRegion, acct.ID, "packaging-groups", "pg1")
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackagePackagingGroup, pgARN, testRegion, "{}")
	bktARN := "arn:aws:s3:::vod-source"
	bktID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bktARN, "us-east-1", "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mp-vod", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	aARN := mediapackageARN(testRegion, acct.ID, "assets", "a1")
	attrs := fmt.Sprintf(`{"PackagingGroupId":"pg1","SourceArn":"%s/path/to/file.smil","SourceRoleArn":"%s"}`, bktARN, roleARN)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageAsset, aARN, testRegion, attrs)
	if err := resolveMPV1AssetRefs(acct, st); err != nil {
		t.Fatalf("resolveMPV1AssetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, pgID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, bktID, store.RelUses)
	assertRelationship(t, rels, aID, roleID, store.RelUses)
}

func TestResolveMPV2ChildrenToChannelGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cgARN := fmt.Sprintf("arn:aws:mediapackagev2:%s:%s:channelGroup/cg1", testRegion, acct.ID)
	cgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2ChannelGroup, cgARN, testRegion, "{}")
	chARN := cgARN + "/channel/c1"
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2Channel, chARN, testRegion, "{}")
	epARN := chARN + "/originEndpoint/ep1"
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2OriginEndpoint, epARN, testRegion, "{}")
	cpARN := chARN + "/policy"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2ChannelPolicy, cpARN, testRegion, "{}")
	epPolARN := epARN + "/policy"
	epPolID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2OriginEndpointPolicy, epPolARN, testRegion, "{}")
	if err := resolveMPV2ChildrenToChannelGroup(acct, st); err != nil {
		t.Fatalf("resolveMPV2ChildrenToChannelGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(chID)
	assertRelationship(t, rels, chID, cgID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, chID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, chID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(epPolID)
	assertRelationship(t, rels, epPolID, epID, store.RelAttachedTo)
}

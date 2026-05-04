package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveIVSChannelRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rcARN := fmt.Sprintf("arn:aws:ivs:%s:%s:recording-configuration/rc1", testRegion, acct.ID)
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSRecordingConfiguration, rcARN, testRegion, "{}")
	prARN := fmt.Sprintf("arn:aws:ivs:%s:%s:playback-restriction-policy/pr1", testRegion, acct.ID)
	prID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSPlaybackRestrictionPolicy, prARN, testRegion, "{}")
	chARN := fmt.Sprintf("arn:aws:ivs:%s:%s:channel/c1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RecordingConfigurationArn":%q,"PlaybackRestrictionPolicyArn":%q}`, rcARN, prARN)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSChannel, chARN, testRegion, attrs)
	if err := resolveIVSChannelRefs(acct, st); err != nil {
		t.Fatalf("resolveIVSChannelRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(chID)
	assertRelationship(t, rels, chID, rcID, store.RelUses)
	assertRelationship(t, rels, chID, prID, store.RelUses)
}

func TestResolveIVSStreamKeyChannel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	chARN := fmt.Sprintf("arn:aws:ivs:%s:%s:channel/c1", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSChannel, chARN, testRegion, "{}")
	skARN := fmt.Sprintf("arn:aws:ivs:%s:%s:stream-key/sk1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ChannelArn":%q}`, chARN)
	skID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSStreamKey, skARN, testRegion, attrs)
	if err := resolveIVSStreamKeyChannel(acct, st); err != nil {
		t.Fatalf("resolveIVSStreamKeyChannel: %v", err)
	}
	rels, _ := st.RelationshipsFrom(skID)
	assertRelationship(t, rels, skID, chID, store.RelAttachedTo)
}

func TestResolveIVSIngestConfigStage(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stARN := fmt.Sprintf("arn:aws:ivs:%s:%s:stage/s1", testRegion, acct.ID)
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSStage, stARN, testRegion, "{}")
	icARN := fmt.Sprintf("arn:aws:ivs:%s:%s:ingest-configuration/ic1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"StageArn":%q}`, stARN)
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSIngestConfiguration, icARN, testRegion, attrs)
	if err := resolveIVSIngestConfigStage(acct, st); err != nil {
		t.Fatalf("resolveIVSIngestConfigStage: %v", err)
	}
	rels, _ := st.RelationshipsFrom(icID)
	assertRelationship(t, rels, icID, stID, store.RelAttachedTo)
}

func TestResolveIVSRecordingConfigS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bName := "ivs-rec-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bName, testRegion, "{}")
	rcARN := fmt.Sprintf("arn:aws:ivs:%s:%s:recording-configuration/rc-1", testRegion, acct.ID)
	rcAttrs := fmt.Sprintf(`{"DestinationConfiguration":{"S3":{"BucketName":%q}}}`, bName)
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSRecordingConfiguration, rcARN, testRegion, rcAttrs)
	if err := resolveIVSRecordingConfigS3(acct, st); err != nil {
		t.Fatalf("resolveIVSRecordingConfigS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rcID)
	assertRelationship(t, rels, rcID, bID, store.RelUses)
}

func TestResolveIVSStorageConfigS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bName := "ivs-store-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bName, testRegion, "{}")
	scARN := fmt.Sprintf("arn:aws:ivs:%s:%s:storage-configuration/sc-1", testRegion, acct.ID)
	scAttrs := fmt.Sprintf(`{"S3":{"BucketName":%q}}`, bName)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSStorageConfiguration, scARN, testRegion, scAttrs)
	if err := resolveIVSStorageConfigS3(acct, st); err != nil {
		t.Fatalf("resolveIVSStorageConfigS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(scID)
	assertRelationship(t, rels, scID, bID, store.RelUses)
}

func TestResolveIVSStageStorageConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	scARN := fmt.Sprintf("arn:aws:ivs:%s:%s:storage-configuration/sc-1", testRegion, acct.ID)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSStorageConfiguration, scARN, testRegion, "{}")
	stARN := fmt.Sprintf("arn:aws:ivs:%s:%s:stage/s1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"AutoParticipantRecordingConfiguration":{"StorageConfigurationArn":%q}}`, scARN)
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeIVSStage, stARN, testRegion, attrs)
	if err := resolveIVSStageStorageConfig(acct, st); err != nil {
		t.Fatalf("resolveIVSStageStorageConfig: %v", err)
	}
	rels, _ := st.RelationshipsFrom(stID)
	assertRelationship(t, rels, stID, scID, store.RelUses)
}

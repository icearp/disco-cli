package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

func TestResolveCloudFrontFLEConfigProfile(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	profID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFieldLevelEncryptionProfile, "P123", "", "{}")

	// A config referencing the profile from both the content-type and query-arg paths.
	summary := cftypes.FieldLevelEncryptionSummary{
		Id: sdkaws.String("C1"),
		ContentTypeProfileConfig: &cftypes.ContentTypeProfileConfig{
			ContentTypeProfiles: &cftypes.ContentTypeProfiles{
				Items: []cftypes.ContentTypeProfile{{ProfileId: sdkaws.String("P123")}},
			},
		},
		QueryArgProfileConfig: &cftypes.QueryArgProfileConfig{
			QueryArgProfiles: &cftypes.QueryArgProfiles{
				Items: []cftypes.QueryArgProfile{{ProfileId: sdkaws.String("P123")}},
			},
		},
	}
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFieldLevelEncryptionConfig, "C1", "", mustJSON(summary))

	if err := resolveCloudFrontFLEConfigProfile(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontFLEConfigProfile: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfgID)
	assertRelationship(t, rels, cfgID, profID, store.RelUses)
	// Deduped across the two reference paths → exactly one edge.
	if len(rels) != 1 {
		t.Errorf("got %d edges, want 1 (deduped)", len(rels))
	}
}

func TestResolveCloudFrontFLEProfilePublicKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontPublicKey, "K123", "", "{}")

	profile := cftypes.FieldLevelEncryptionProfileSummary{
		Id: sdkaws.String("P1"),
		EncryptionEntities: &cftypes.EncryptionEntities{
			Items: []cftypes.EncryptionEntity{
				{PublicKeyId: sdkaws.String("K123")},
				{PublicKeyId: sdkaws.String("K123")},  // dup → one edge
				{PublicKeyId: sdkaws.String("never")}, // unscanned → no edge
			},
		},
	}
	profID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFieldLevelEncryptionProfile, "P1", "", mustJSON(profile))

	if err := resolveCloudFrontFLEProfilePublicKey(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontFLEProfilePublicKey: %v", err)
	}
	rels, _ := st.RelationshipsFrom(profID)
	assertRelationship(t, rels, profID, keyID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("got %d edges, want 1 (deduped, unscanned skipped)", len(rels))
	}
}

// A config referencing an unscanned profile, and one with no profile refs, emit
// no edge.
func TestResolveCloudFrontFLEConfigProfile_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	missing := cftypes.FieldLevelEncryptionSummary{
		Id: sdkaws.String("C1"),
		ContentTypeProfileConfig: &cftypes.ContentTypeProfileConfig{
			ContentTypeProfiles: &cftypes.ContentTypeProfiles{
				Items: []cftypes.ContentTypeProfile{{ProfileId: sdkaws.String("never")}},
			},
		},
	}
	c1 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFieldLevelEncryptionConfig, "C1", "", mustJSON(missing))
	c2 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFieldLevelEncryptionConfig, "C2", "",
		mustJSON(cftypes.FieldLevelEncryptionSummary{Id: sdkaws.String("C2")}))

	if err := resolveCloudFrontFLEConfigProfile(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontFLEConfigProfile: %v", err)
	}
	for _, id := range []string{c1, c2} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}

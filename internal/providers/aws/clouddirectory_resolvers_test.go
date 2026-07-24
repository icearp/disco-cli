package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveCloudDirectoryAppliedSchema(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dirARN := fmt.Sprintf("arn:aws:clouddirectory:%s:%s:directory/dir1", testRegion, acct.ID)
	dirID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudDirectoryDirectory, dirARN, testRegion, "{}")

	schemaARN := dirARN + "/schema/MySchema/1.0"
	attrs := fmt.Sprintf(`{"SchemaArn":%q,"DirectoryArn":%q}`, schemaARN, dirARN)
	schemaID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudDirectoryAppliedSchema, schemaARN, testRegion, attrs)

	if err := resolveCloudDirectoryAppliedSchema(acct, st); err != nil {
		t.Fatalf("resolveCloudDirectoryAppliedSchema: %v", err)
	}
	rels, _ := st.RelationshipsFrom(schemaID)
	assertRelationship(t, rels, schemaID, dirID, store.RelAttachedTo)
}

// An applied schema whose DirectoryArn points at an unscanned directory, or has
// none at all, emits no edge.
func TestResolveCloudDirectoryAppliedSchema_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	missingDir := fmt.Sprintf("arn:aws:clouddirectory:%s:%s:directory/never", testRegion, acct.ID)
	schemaARN := missingDir + "/schema/MySchema/1.0"
	attrs := fmt.Sprintf(`{"SchemaArn":%q,"DirectoryArn":%q}`, schemaARN, missingDir)
	s1 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudDirectoryAppliedSchema, schemaARN, testRegion, attrs)

	noDir := fmt.Sprintf("arn:aws:clouddirectory:%s:%s:schema/published/Orphan/1.0", testRegion, acct.ID)
	s2 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudDirectoryAppliedSchema, noDir, testRegion, fmt.Sprintf(`{"SchemaArn":%q}`, noDir))

	if err := resolveCloudDirectoryAppliedSchema(acct, st); err != nil {
		t.Fatalf("resolveCloudDirectoryAppliedSchema: %v", err)
	}
	for _, id := range []string{s1, s2} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}

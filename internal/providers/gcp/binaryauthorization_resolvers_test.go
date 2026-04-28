package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveBinaryAuthorizationRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "binauth-delegate@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthAttestor,
		"projects/my-project/attestors/prod-build", "",
		`{"userOwnedGrafeasNote": {"delegationServiceAccountEmail": "`+saEmail+`"}}`)

	if err := resolveBinaryAuthorizationRelationships(p, st); err != nil {
		t.Fatalf("resolveBinaryAuthorizationRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →SA uses", rels)
	}
}

package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCloudBuildRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "build-sa@my-project.iam.gserviceaccount.com"
	saNative := "projects/my-project/serviceAccounts/" + saEmail
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, saNative, "", "{}")

	// Full resource-name form.
	trFull := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/abc", "",
		`{"serviceAccount": "`+saNative+`"}`)
	// Email-only form.
	trEmail := upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/def", "",
		`{"serviceAccount": "`+saEmail+`"}`)
	// Cross-project SA — must be skipped.
	upsertTestResource(t, st, "gcp", p.ID, TypeCloudBuildTrigger,
		"projects/my-project/locations/global/triggers/orphan", "",
		`{"serviceAccount": "other@other-project.iam.gserviceaccount.com"}`)

	if err := resolveCloudBuildRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudBuildRelationships: %v", err)
	}
	for _, fromID := range []string{trFull, trEmail} {
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
			t.Errorf("from %s: got %+v, want →SA uses", fromID, rels)
		}
	}
}

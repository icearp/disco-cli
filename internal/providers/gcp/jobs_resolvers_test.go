package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveJobsRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "job-sa@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	runJobID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunJob,
		"projects/my-project/locations/us-central1/jobs/r1", "us-central1",
		`{"template": {"template": {"serviceAccount": "`+saEmail+`"}}}`)

	batchJobID := upsertTestResource(t, st, "gcp", p.ID, TypeBatchJob,
		"projects/my-project/locations/us-central1/jobs/b1", "us-central1",
		`{"allocationPolicy": {"serviceAccount": {"email": "`+saEmail+`"}}}`)

	if err := resolveJobsRelationships(p, st); err != nil {
		t.Fatalf("resolveJobsRelationships: %v", err)
	}
	for label, fromID := range map[string]string{"run": runJobID, "batch": batchJobID} {
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: got %+v, want →SA uses", label, rels)
		}
	}
}

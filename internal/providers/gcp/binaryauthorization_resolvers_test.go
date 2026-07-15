package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

func upsertNamedTestResource(t *testing.T, st *store.Store, provider, accountID, rtype, nativeID, region, name, attrsJSON string) string {
	t.Helper()
	r := &store.Resource{
		Provider:       provider,
		AccountID:      accountID,
		Type:           rtype,
		NativeID:       nativeID,
		Region:         &region,
		Name:           &name,
		AttributesJSON: attrsJSON,
		DiscoveredBy:   testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsertNamedTestResource %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID(provider, accountID, nativeID)
}

func TestResolveBinaryAuthorizationPolicyRelationships_AttestorsAndCluster(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	defaultAttID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthAttestor,
		"projects/my-project/attestors/default-attestor", "", "{}")
	clusterAttID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthAttestor,
		"projects/my-project/attestors/prod-attestor", "", "{}")
	clusterID := upsertNamedTestResource(t, st, "gcp", p.ID, TypeGKECluster,
		"https://container.googleapis.com/v1/projects/my-project/locations/us-central1-a/clusters/prod",
		"us-central1-a", "prod", "{}")

	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthPolicy,
		"projects/my-project/policy", "",
		`{"defaultAdmissionRule": {"requireAttestationsBy": ["projects/my-project/attestors/default-attestor"]},`+
			`"clusterAdmissionRules": {"us-central1-a.prod": {"requireAttestationsBy": ["projects/my-project/attestors/prod-attestor"]}}}`)

	if err := resolveBinaryAuthorizationPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBinaryAuthorizationPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{defaultAttID: false, clusterAttID: false, clusterID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		if rel.Kind != store.RelUses {
			t.Errorf("got kind %s, want %s", rel.Kind, store.RelUses)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolveBinaryAuthorizationPolicyRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthPolicy,
		"projects/my-project/policy", "",
		`{"defaultAdmissionRule": {"requireAttestationsBy": ["projects/my-project/attestors/not-scanned"]},`+
			`"clusterAdmissionRules": {"us-central1-a.not-scanned": {}}}`)

	if err := resolveBinaryAuthorizationPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBinaryAuthorizationPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveBinaryAuthorizationPolicyRelationships_NoAdmissionRulesNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeBinAuthPolicy,
		"projects/my-project/policy", "", "{}")

	if err := resolveBinaryAuthorizationPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBinaryAuthorizationPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when admission rules are unset, got %+v", rels)
	}
}

func TestResolveBinaryAuthorizationPolicyRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveBinaryAuthorizationPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBinaryAuthorizationPolicyRelationships on empty project: %v", err)
	}
}

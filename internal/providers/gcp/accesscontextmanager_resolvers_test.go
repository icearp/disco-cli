package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveAccessContextManagerRelationships(t *testing.T) {
	st := newTestStore(t)

	orgID := "organizations/456"
	policyName := "accessPolicies/123"
	levelName := policyName + "/accessLevels/level1"

	levelID := upsertTestResource(t, st, "gcp", orgID, TypeAccessLevel, levelName, "", "{}")

	perimeterID := upsertTestResource(t, st, "gcp", orgID, TypeServicePerimeter, policyName+"/servicePerimeters/perim1", "",
		`{"status": {"accessLevels": ["`+levelName+`"], "resources": ["projects/987654321"]}}`)

	projID := upsertTestResource(t, st, "gcp", "my-project", TypeProject, "my-project", "", `{"name": "projects/987654321"}`)

	folderID := upsertTestResource(t, st, "gcp", "folders/222", TypeFolder, "folders/222", "", "{}")

	accessPolicyID := upsertTestResource(t, st, "gcp", orgID, TypeAccessPolicy, policyName, "",
		`{"scopes": ["folders/222", "projects/987654321"]}`)

	bindingID := upsertTestResource(t, st, "gcp", orgID, TypeGcpUserAccessBinding, orgID+"/gcpUserAccessBindings/b1", "",
		`{"accessLevels": ["`+levelName+`"]}`)

	orgsDescID := upsertTestResource(t, st, "gcp", orgID, TypeAuthorizedOrgsDesc, policyName+"/authorizedOrgsDescs/aod1", "",
		`{"orgs": ["organizations/999"]}`)

	if err := resolveAccessContextManagerRelationships(st); err != nil {
		t.Fatalf("resolveAccessContextManagerRelationships: %v", err)
	}

	perimRels, _ := st.RelationshipsFrom(perimeterID)
	got := map[string]string{}
	for _, r := range perimRels {
		got[r.ToID] = r.Kind
	}
	if got[levelID] != store.RelUses || got[projID] != store.RelAttachedTo {
		t.Errorf("perimeter edges: got %+v, want →accessLevel uses + →project attached-to", got)
	}

	polRels, _ := st.RelationshipsFrom(accessPolicyID)
	got = map[string]string{}
	for _, r := range polRels {
		got[r.ToID] = r.Kind
	}
	if got[folderID] != store.RelAttachedTo || got[projID] != store.RelAttachedTo {
		t.Errorf("accessPolicy edges: got %+v, want →folder + →project (attached-to)", got)
	}

	bindRels, _ := st.RelationshipsFrom(bindingID)
	if len(bindRels) != 1 || bindRels[0].ToID != levelID || bindRels[0].Kind != store.RelUses {
		t.Errorf("binding edge: got %+v, want →accessLevel uses", bindRels)
	}

	orgsDescRels, err := st.RelationshipsFrom(orgsDescID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(orgsDescRels) != 1 || orgsDescRels[0].Kind != store.RelUses {
		t.Fatalf("authorizedOrgsDesc edge: got %+v, want 1 →organization uses", orgsDescRels)
	}
	placeholderID := store.ResourceID("gcp", "organizations/999", "organizations/999")
	if orgsDescRels[0].ToID != placeholderID {
		t.Errorf("authorizedOrgsDesc edge target: got %s, want placeholder %s", orgsDescRels[0].ToID, placeholderID)
	}
	placeholder, err := st.GetResource(placeholderID)
	if err != nil {
		t.Fatalf("GetResource(placeholder): %v", err)
	}
	if placeholder.AttributesJSON != "{}" {
		t.Errorf("placeholder org: want empty-attribute placeholder, got %q", placeholder.AttributesJSON)
	}
}

func TestResolveAccessContextManagerRelationships_AccessLevelSelfReference(t *testing.T) {
	st := newTestStore(t)

	orgID := "organizations/456"
	policyName := "accessPolicies/123"
	baseLevelName := policyName + "/accessLevels/base"
	combinedLevelName := policyName + "/accessLevels/combined"

	baseLevelID := upsertTestResource(t, st, "gcp", orgID, TypeAccessLevel, baseLevelName, "", "{}")
	combinedLevelID := upsertTestResource(t, st, "gcp", orgID, TypeAccessLevel, combinedLevelName, "",
		`{"basic": {"conditions": [{"requiredAccessLevels": ["`+baseLevelName+`"]}]}}`)

	if err := resolveAccessContextManagerRelationships(st); err != nil {
		t.Fatalf("resolveAccessContextManagerRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(combinedLevelID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != baseLevelID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →base accessLevel uses", rels)
	}
}

func TestResolveAccessContextManagerRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)

	orgID := "organizations/456"
	policyName := "accessPolicies/123"

	perimeterID := upsertTestResource(t, st, "gcp", orgID, TypeServicePerimeter, policyName+"/servicePerimeters/perim1", "",
		`{"status": {"accessLevels": ["`+policyName+`/accessLevels/not-scanned"], "resources": ["projects/000000000"]}}`)
	accessPolicyID := upsertTestResource(t, st, "gcp", orgID, TypeAccessPolicy, policyName, "",
		`{"scopes": ["folders/not-scanned", "projects/000000000"]}`)
	bindingID := upsertTestResource(t, st, "gcp", orgID, TypeGcpUserAccessBinding, orgID+"/gcpUserAccessBindings/b1", "",
		`{"accessLevels": ["`+policyName+`/accessLevels/not-scanned"]}`)

	if err := resolveAccessContextManagerRelationships(st); err != nil {
		t.Fatalf("resolveAccessContextManagerRelationships: %v", err)
	}

	for label, id := range map[string]string{"perimeter": perimeterID, "accessPolicy": accessPolicyID, "binding": bindingID} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", label, err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: want no edges for unscanned targets, got %+v", label, rels)
		}
	}
}

func TestResolveAccessContextManagerRelationships_NoFieldsNoPanic(t *testing.T) {
	st := newTestStore(t)

	orgID := "organizations/456"
	perimeterID := upsertTestResource(t, st, "gcp", orgID, TypeServicePerimeter, "accessPolicies/123/servicePerimeters/perim1", "", "{}")
	accessPolicyID := upsertTestResource(t, st, "gcp", orgID, TypeAccessPolicy, "accessPolicies/123", "", "{}")
	bindingID := upsertTestResource(t, st, "gcp", orgID, TypeGcpUserAccessBinding, orgID+"/gcpUserAccessBindings/b1", "", "{}")
	orgsDescID := upsertTestResource(t, st, "gcp", orgID, TypeAuthorizedOrgsDesc, "accessPolicies/123/authorizedOrgsDescs/aod1", "", "{}")

	if err := resolveAccessContextManagerRelationships(st); err != nil {
		t.Fatalf("resolveAccessContextManagerRelationships: %v", err)
	}

	for label, id := range map[string]string{
		"perimeter": perimeterID, "accessPolicy": accessPolicyID, "binding": bindingID, "orgsDesc": orgsDescID,
	} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", label, err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: want no edges when fields are unset, got %+v", label, rels)
		}
	}
}

func TestResolveAccessContextManagerRelationships_EmptyStoreNoResources(t *testing.T) {
	st := newTestStore(t)
	if err := resolveAccessContextManagerRelationships(st); err != nil {
		t.Fatalf("resolveAccessContextManagerRelationships on empty store: %v", err)
	}
}

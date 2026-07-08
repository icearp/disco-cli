package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	compute "google.golang.org/api/compute/v1"
)

func TestResolvePublicDelegatedPrefixRelationships_ParentIsAdvertisedPrefix(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	papSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/publicAdvertisedPrefixes/pap-1"
	papID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePublicAdvertisedPrefix, papSelfLink, "", "{}")

	pdpSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/publicDelegatedPrefixes/pdp-1"
	pdpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePublicDelegatedPrefix, pdpSelfLink, "us-central1",
		marshalAttrs(t, &compute.PublicDelegatedPrefix{
			Name:         "pdp-1",
			SelfLink:     pdpSelfLink,
			ParentPrefix: papSelfLink,
		}))

	if err := resolvePublicDelegatedPrefixRelationships(p, st); err != nil {
		t.Fatalf("resolvePublicDelegatedPrefixRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pdpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != papID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →publicAdvertisedPrefix uses", rels)
	}
}

func TestResolvePublicDelegatedPrefixRelationships_ParentIsGlobalDelegatedPrefix(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	parentSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/publicDelegatedPrefixes/parent-pdp"
	parentID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeGlobalPublicDelegatedPrefix, parentSelfLink, "", "{}")

	childSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/publicDelegatedPrefixes/child-pdp"
	childID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePublicDelegatedPrefix, childSelfLink, "us-central1",
		marshalAttrs(t, &compute.PublicDelegatedPrefix{
			Name:         "child-pdp",
			SelfLink:     childSelfLink,
			ParentPrefix: parentSelfLink,
		}))

	if err := resolvePublicDelegatedPrefixRelationships(p, st); err != nil {
		t.Fatalf("resolvePublicDelegatedPrefixRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(childID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != parentID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →global publicDelegatedPrefix uses", rels)
	}
}

func TestResolvePublicDelegatedPrefixRelationships_UnscannedParentSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pdpSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/publicDelegatedPrefixes/pdp-1"
	pdpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePublicDelegatedPrefix, pdpSelfLink, "us-central1",
		marshalAttrs(t, &compute.PublicDelegatedPrefix{
			Name:         "pdp-1",
			SelfLink:     pdpSelfLink,
			ParentPrefix: "https://www.googleapis.com/compute/v1/projects/my-project/global/publicAdvertisedPrefixes/not-scanned",
		}))

	if err := resolvePublicDelegatedPrefixRelationships(p, st); err != nil {
		t.Fatalf("resolvePublicDelegatedPrefixRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pdpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned parent prefix, got %+v", rels)
	}
}

func TestResolvePublicDelegatedPrefixRelationships_NoParentPrefixNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pdpSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/publicDelegatedPrefixes/pdp-1"
	pdpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePublicDelegatedPrefix, pdpSelfLink, "us-central1",
		marshalAttrs(t, &compute.PublicDelegatedPrefix{Name: "pdp-1", SelfLink: pdpSelfLink}))

	if err := resolvePublicDelegatedPrefixRelationships(p, st); err != nil {
		t.Fatalf("resolvePublicDelegatedPrefixRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pdpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when parentPrefix is unset, got %+v", rels)
	}
}

func TestResolvePublicDelegatedPrefixRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolvePublicDelegatedPrefixRelationships(p, st); err != nil {
		t.Fatalf("resolvePublicDelegatedPrefixRelationships on empty project: %v", err)
	}
}

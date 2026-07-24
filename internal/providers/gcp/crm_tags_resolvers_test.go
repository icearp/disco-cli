package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveCRMTagBindingRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tvID := upsertTestResource(t, st, "gcp", p.ID, TypeTagValue, "tagValues/1", "", "{}")
	tbID := upsertTestResource(t, st, "gcp", p.ID, TypeTagBinding,
		"tagBindings/%2F%2Fcloudresourcemanager.googleapis.com%2Fprojects%2F123/tagValues/1", "",
		`{"tagValue": "tagValues/1"}`)

	if err := resolveCRMTagBindingRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMTagBindingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tbID)
	if len(rels) != 1 || rels[0].ToID != tvID || rels[0].Kind != store.RelUses {
		t.Errorf("tag binding: got %+v, want →tagValue uses", rels)
	}
}

func TestResolveCRMTagBindingRelationships_UnscannedValueSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tbID := upsertTestResource(t, st, "gcp", p.ID, TypeTagBinding,
		"tagBindings/%2F%2Fcloudresourcemanager.googleapis.com%2Fprojects%2F123/tagValues/9", "",
		`{"tagValue": "tagValues/9"}`)

	if err := resolveCRMTagBindingRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMTagBindingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tbID)
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned tag value, got %+v", rels)
	}
}

func TestResolveCRMEffectiveTagRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tvID := upsertTestResource(t, st, "gcp", p.ID, TypeTagValue, "tagValues/1", "", "{}")
	etID := upsertTestResource(t, st, "gcp", p.ID, TypeEffectiveTag,
		"//cloudresourcemanager.googleapis.com/projects/123/effectiveTags/1", "",
		`{"tagValue": "tagValues/1", "namespacedTagValue": "my-org/env/prod"}`)

	if err := resolveCRMEffectiveTagRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMEffectiveTagRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(etID)
	if len(rels) != 1 || rels[0].ToID != tvID || rels[0].Kind != store.RelUses {
		t.Errorf("effective tag: got %+v, want →tagValue uses", rels)
	}
}

func TestResolveCRMEffectiveTagRelationships_UnscannedValueSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	etID := upsertTestResource(t, st, "gcp", p.ID, TypeEffectiveTag,
		"//cloudresourcemanager.googleapis.com/projects/123/effectiveTags/9", "",
		`{"tagValue": "tagValues/9"}`)

	if err := resolveCRMEffectiveTagRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMEffectiveTagRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(etID)
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned tag value, got %+v", rels)
	}
}

func TestResolveCRMTagsResolvers_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveCRMTagBindingRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMTagBindingRelationships on empty project: %v", err)
	}
	if err := resolveCRMEffectiveTagRelationships(p, st); err != nil {
		t.Fatalf("resolveCRMEffectiveTagRelationships on empty project: %v", err)
	}
}

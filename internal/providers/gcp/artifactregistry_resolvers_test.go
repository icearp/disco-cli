package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	artifactregistry "google.golang.org/api/artifactregistry/v1"
)

func TestResolveArtifactRegistryRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us", "{}")

	repoID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRepository,
		"projects/my-project/locations/us/repositories/docker", "us",
		`{"kmsKeyName": "`+keyName+`"}`)
	upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRepository,
		"projects/my-project/locations/us/repositories/plain", "us", `{}`)

	if err := resolveArtifactRegistryRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRegistryRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(repoID)
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →key uses", rels)
	}
}

func TestResolveArtifactRuleRelationships_PackageScoped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pkgID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactPackage,
		"projects/my-project/locations/us/repositories/docker/packages/pkg-1", "us", "{}")

	ruleID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRule,
		"projects/my-project/locations/us/repositories/docker/rules/rule-1", "us",
		marshalAttrs(t, &artifactregistry.GoogleDevtoolsArtifactregistryV1Rule{
			Name:      "projects/my-project/locations/us/repositories/docker/rules/rule-1",
			PackageId: "pkg-1",
		}))

	if err := resolveArtifactRuleRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRuleRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != pkgID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →package uses", rels)
	}
}

func TestResolveArtifactRuleRelationships_RepositoryScopedNoEdge(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	ruleID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRule,
		"projects/my-project/locations/us/repositories/docker/rules/rule-1", "us",
		marshalAttrs(t, &artifactregistry.GoogleDevtoolsArtifactregistryV1Rule{
			Name: "projects/my-project/locations/us/repositories/docker/rules/rule-1",
		}))

	if err := resolveArtifactRuleRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRuleRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for a repository-wide rule (empty packageId), got %+v", rels)
	}
}

func TestResolveArtifactRuleRelationships_UnscannedPackageSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	ruleID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRule,
		"projects/my-project/locations/us/repositories/docker/rules/rule-1", "us",
		marshalAttrs(t, &artifactregistry.GoogleDevtoolsArtifactregistryV1Rule{
			Name:      "projects/my-project/locations/us/repositories/docker/rules/rule-1",
			PackageId: "not-scanned",
		}))

	if err := resolveArtifactRuleRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRuleRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned package, got %+v", rels)
	}
}

func TestResolveArtifactRuleRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveArtifactRuleRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRuleRelationships on empty project: %v", err)
	}
}

func TestResolveArtifactAttachmentRelationships_TargetsPackage(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pkgID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactPackage,
		"projects/my-project/locations/us/repositories/docker/packages/pkg-1", "us", "{}")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactAttachment,
		"projects/my-project/locations/us/repositories/docker/attachments/att-1", "us",
		marshalAttrs(t, &artifactregistry.Attachment{
			Name:   "projects/my-project/locations/us/repositories/docker/attachments/att-1",
			Target: "projects/my-project/locations/us/repositories/docker/packages/pkg-1",
		}))

	if err := resolveArtifactAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactAttachmentRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != pkgID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →package uses", rels)
	}
}

func TestResolveArtifactAttachmentRelationships_TargetsVersionSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactAttachment,
		"projects/my-project/locations/us/repositories/docker/attachments/att-1", "us",
		marshalAttrs(t, &artifactregistry.Attachment{
			Name:   "projects/my-project/locations/us/repositories/docker/attachments/att-1",
			Target: "projects/my-project/locations/us/repositories/docker/packages/pkg-1/versions/v1",
		}))

	if err := resolveArtifactAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactAttachmentRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for a Version target (unscanned type), got %+v", rels)
	}
}

func TestResolveArtifactAttachmentRelationships_TargetsOwnRepositorySkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactAttachment,
		"projects/my-project/locations/us/repositories/docker/attachments/att-1", "us",
		marshalAttrs(t, &artifactregistry.Attachment{
			Name:   "projects/my-project/locations/us/repositories/docker/attachments/att-1",
			Target: "projects/my-project/locations/us/repositories/docker",
		}))

	if err := resolveArtifactAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactAttachmentRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when target is the attachment's own owning repository (redundant with hierarchy parent), got %+v", rels)
	}
}

func TestResolveArtifactAttachmentRelationships_UnscannedPackageSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactAttachment,
		"projects/my-project/locations/us/repositories/docker/attachments/att-1", "us",
		marshalAttrs(t, &artifactregistry.Attachment{
			Name:   "projects/my-project/locations/us/repositories/docker/attachments/att-1",
			Target: "projects/my-project/locations/us/repositories/docker/packages/not-scanned",
		}))

	if err := resolveArtifactAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactAttachmentRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned package, got %+v", rels)
	}
}

func TestResolveArtifactAttachmentRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveArtifactAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactAttachmentRelationships on empty project: %v", err)
	}
}

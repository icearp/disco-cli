package gcp

import (
	"testing"

	cloudkms "google.golang.org/api/cloudkms/v1"
)

func TestResolveCryptoKeyRelationships_PrimaryVersionAndEkmBackend(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	cvID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKeyVersion, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1", "us",
		marshalAttrs(t, &cloudkms.CryptoKeyVersion{Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1"}))
	ekmID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSEkmConnection, "projects/proj-1/locations/us/ekmConnections/ekm-1", "us",
		marshalAttrs(t, &cloudkms.EkmConnection{Name: "projects/proj-1/locations/us/ekmConnections/ekm-1"}))

	ckID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{
			Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1",
			Primary: &cloudkms.CryptoKeyVersion{
				Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1",
			},
			CryptoKeyBackend: "projects/proj-1/locations/us/ekmConnections/ekm-1",
		}))

	if err := resolveCryptoKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ckID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[cvID] != "uses" || got[ekmID] != "uses" {
		t.Errorf("want cryptoKey->primaryVersion+ekmConnection edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveCryptoKeyRelationships_SingleTenantHsmBackend(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	hsmID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSSingleTenantHsmInstance, "projects/proj-1/locations/us/singleTenantHsmInstances/hsm-1", "us",
		marshalAttrs(t, &cloudkms.SingleTenantHsmInstance{Name: "projects/proj-1/locations/us/singleTenantHsmInstances/hsm-1"}))

	ckID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{
			Name:             "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1",
			CryptoKeyBackend: "projects/proj-1/locations/us/singleTenantHsmInstances/hsm-1",
		}))

	if err := resolveCryptoKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ckID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != hsmID || rels[0].Kind != "uses" {
		t.Errorf("want cryptoKey->singleTenantHsmInstance edge, got %+v", rels)
	}
}

func TestResolveCryptoKeyRelationships_NilPrimaryAndUnscannedBackendSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ckID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{
			Name:             "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1",
			CryptoKeyBackend: "projects/proj-1/locations/us/ekmConnections/not-scanned",
		}))

	if err := resolveCryptoKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ckID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges (nil primary, unscanned backend), got %+v", rels)
	}
}

func TestResolveCryptoKeyRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveCryptoKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyRelationships on empty project: %v", err)
	}
}

func TestResolveKeyHandleRelationships_ToCryptoKey(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ckID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/autokey/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{Name: "projects/proj-1/locations/us/keyRings/autokey/cryptoKeys/ck-1"}))

	khID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSKeyHandle, "projects/proj-1/locations/us/keyHandles/kh-1", "us",
		marshalAttrs(t, &cloudkms.KeyHandle{
			Name:   "projects/proj-1/locations/us/keyHandles/kh-1",
			KmsKey: "projects/proj-1/locations/us/keyRings/autokey/cryptoKeys/ck-1",
		}))

	if err := resolveKeyHandleRelationships(p, st); err != nil {
		t.Fatalf("resolveKeyHandleRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(khID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != ckID || rels[0].Kind != "uses" {
		t.Errorf("want keyHandle->cryptoKey edge, got %+v", rels)
	}
}

func TestResolveCryptoKeyVersionRelationships_ToImportJob(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ijID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSImportJob, "projects/proj-1/locations/us/keyRings/kr-1/importJobs/ij-1", "us",
		marshalAttrs(t, &cloudkms.ImportJob{Name: "projects/proj-1/locations/us/keyRings/kr-1/importJobs/ij-1"}))

	cvID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKeyVersion, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1", "us",
		marshalAttrs(t, &cloudkms.CryptoKeyVersion{
			Name:      "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1",
			ImportJob: "projects/proj-1/locations/us/keyRings/kr-1/importJobs/ij-1",
		}))

	if err := resolveCryptoKeyVersionRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyVersionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(cvID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != ijID || rels[0].Kind != "uses" {
		t.Errorf("want cryptoKeyVersion->importJob edge, got %+v", rels)
	}
}

func TestResolveCryptoKeyVersionRelationships_NoImportJobSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	cvID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKeyVersion, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1", "us",
		marshalAttrs(t, &cloudkms.CryptoKeyVersion{
			Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1/cryptoKeyVersions/1",
		}))

	if err := resolveCryptoKeyVersionRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyVersionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(cvID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when key material wasn't imported, got %+v", rels)
	}
}

func TestResolveCryptoKeyVersionRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveCryptoKeyVersionRelationships(p, st); err != nil {
		t.Fatalf("resolveCryptoKeyVersionRelationships on empty project: %v", err)
	}
}

func TestResolveKeyHandleRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	khID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSKeyHandle, "projects/proj-1/locations/us/keyHandles/kh-1", "us",
		marshalAttrs(t, &cloudkms.KeyHandle{
			Name:   "projects/proj-1/locations/us/keyHandles/kh-1",
			KmsKey: "projects/proj-1/locations/us/keyRings/autokey/cryptoKeys/not-scanned",
		}))

	if err := resolveKeyHandleRelationships(p, st); err != nil {
		t.Fatalf("resolveKeyHandleRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(khID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned crypto key, got %+v", rels)
	}
}

func TestResolveKeyHandleRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveKeyHandleRelationships(p, st); err != nil {
		t.Fatalf("resolveKeyHandleRelationships on empty project: %v", err)
	}
}

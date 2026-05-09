package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBigQueryRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us", "{}")

	dsCMEK := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:secure", "us",
		`{"defaultEncryptionConfiguration": {"kmsKeyName": "`+keyName+`"}}`)
	dsPlain := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:plain", "us", `{}`)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsCMEK)
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("CMEK dataset: got %+v, want →key uses", rels)
	}
	relsPlain, _ := st.RelationshipsFrom(dsPlain)
	if len(relsPlain) != 0 {
		t.Errorf("plain dataset: expected no edges, got %+v", relsPlain)
	}
}

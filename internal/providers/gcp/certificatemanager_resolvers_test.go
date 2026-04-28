package gcp

import (
	"testing"
)

// TestResolveCertificateManagerRelationships covers both edge classes.
func TestResolveCertificateManagerRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	cert1 := "projects/my-project/locations/global/certificates/c1"
	cert2 := "projects/my-project/locations/global/certificates/c2"
	mapName := "projects/my-project/locations/global/certificateMaps/m1"
	entryName := mapName + "/certificateMapEntries/e1"
	tpName := "https://www.googleapis.com/compute/v1/projects/my-project/global/targetHttpsProxies/tp-1"

	c1ID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerCertificate, cert1, "", "{}")
	c2ID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerCertificate, cert2, "", "{}")
	mapID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerMap, mapName, "", "{}")
	entryID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerMapEntry, entryName, "",
		`{"certificates": ["`+cert1+`", "`+cert2+`"]}`)
	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetHTTPSProxy, tpName, "",
		`{"certificateMap": "`+mapName+`"}`)

	if err := resolveCertificateManagerRelationships(p, st); err != nil {
		t.Fatalf("resolveCertificateManagerRelationships: %v", err)
	}

	// entry → c1, c2
	rels, _ := st.RelationshipsFrom(entryID)
	if len(rels) != 2 {
		t.Errorf("entry: expected 2 edges, got %d", len(rels))
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[c1ID] || !got[c2ID] {
		t.Errorf("entry edges missing — c1ID=%s c2ID=%s rels=%+v", c1ID, c2ID, rels)
	}

	// tp → map
	relsTP, _ := st.RelationshipsFrom(tpID)
	if len(relsTP) != 1 || relsTP[0].ToID != mapID {
		t.Errorf("tp edge: expected → mapID=%s, got %+v", mapID, relsTP)
	}
}

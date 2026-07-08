package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

func TestResolveCertificateRelationships_DNSAuthAndIssuanceConfig(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	dnsAuthName := "projects/my-project/locations/global/dnsAuthorizations/da1"
	dnsAuthID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerDNSAuth, dnsAuthName, "", "{}")

	icName := "projects/my-project/locations/global/certificateIssuanceConfigs/ic1"
	icID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerIssuanceConfig, icName, "", "{}")

	certID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerCertificate,
		"projects/my-project/locations/global/certificates/c1", "",
		`{"managed": {"dnsAuthorizations": ["`+dnsAuthName+`"], "issuanceConfig": "`+icName+`"}}`)

	if err := resolveCertificateRelationships(p, st); err != nil {
		t.Fatalf("resolveCertificateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[dnsAuthID] != store.RelUses || got[icID] != store.RelUses {
		t.Errorf("got %+v, want →dnsAuth + →issuanceConfig", got)
	}
}

func TestResolveCertificateRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	certID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerCertificate,
		"projects/my-project/locations/global/certificates/c1", "",
		`{"managed": {"dnsAuthorizations": ["projects/my-project/locations/global/dnsAuthorizations/not-scanned"], "issuanceConfig": "projects/my-project/locations/global/certificateIssuanceConfigs/not-scanned"}}`)

	if err := resolveCertificateRelationships(p, st); err != nil {
		t.Fatalf("resolveCertificateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveCertificateRelationships_SelfManagedNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	certID := upsertTestResource(t, st, "gcp", p.ID, TypeCertManagerCertificate,
		"projects/my-project/locations/global/certificates/c1", "", `{"selfManaged": {}}`)

	if err := resolveCertificateRelationships(p, st); err != nil {
		t.Fatalf("resolveCertificateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for a self-managed cert (no Managed field), got %+v", rels)
	}
}

func TestResolveCertificateRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveCertificateRelationships(p, st); err != nil {
		t.Fatalf("resolveCertificateRelationships on empty project: %v", err)
	}
}

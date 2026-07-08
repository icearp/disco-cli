package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBigtableAppProfileRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	c1ID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster,
		"projects/my-project/instances/i1/clusters/c1", "us-central1", "{}")
	c2ID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster,
		"projects/my-project/instances/i1/clusters/c2", "us-central1", "{}")

	singleID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableAppProfile,
		"projects/my-project/instances/i1/appProfiles/ap-single", "",
		`{"singleClusterRouting": {"clusterId": "c1"}}`)
	multiID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableAppProfile,
		"projects/my-project/instances/i1/appProfiles/ap-multi", "",
		`{"multiClusterRoutingUseAny": {"clusterIds": ["c1", "c2"]}}`)

	if err := resolveBigtableAppProfileRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableAppProfileRelationships: %v", err)
	}

	singleRels, _ := st.RelationshipsFrom(singleID)
	if len(singleRels) != 1 || singleRels[0].ToID != c1ID || singleRels[0].Kind != store.RelUses {
		t.Errorf("single-cluster routing: got %+v, want →c1 uses", singleRels)
	}

	multiRels, _ := st.RelationshipsFrom(multiID)
	got := map[string]bool{}
	for _, r := range multiRels {
		got[r.ToID] = true
	}
	if !got[c1ID] || !got[c2ID] || len(multiRels) != 2 {
		t.Errorf("multi-cluster routing: got %+v, want →c1 + →c2", multiRels)
	}
}

// TestResolveBigtableAppProfileRelationships_ClusterIDScopedByInstance proves
// bare cluster IDs are only unique within their owning instance: two
// instances in the same project each have a cluster named "c1", and an
// AppProfile belonging to i1 must resolve to i1's c1, never i2's (i2 sorts
// after i1 by NativeID — the case a last-write-wins bare-name map would get
// wrong).
func TestResolveBigtableAppProfileRelationships_ClusterIDScopedByInstance(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	i1c1ID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster,
		"projects/my-project/instances/i1/clusters/c1", "us-central1", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster,
		"projects/my-project/instances/i2/clusters/c1", "us-east1", "{}")

	apID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableAppProfile,
		"projects/my-project/instances/i1/appProfiles/ap-1", "",
		`{"singleClusterRouting": {"clusterId": "c1"}}`)

	if err := resolveBigtableAppProfileRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableAppProfileRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(apID)
	if len(rels) != 1 || rels[0].ToID != i1c1ID {
		t.Errorf("cross-instance cluster ID collision: got %+v, want →i1's c1 only", rels)
	}
}

func TestResolveBigtableAppProfileRelationships_UnmatchedClusterSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	apID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableAppProfile,
		"projects/my-project/instances/i1/appProfiles/ap-1", "",
		`{"singleClusterRouting": {"clusterId": "not-scanned"}}`)

	if err := resolveBigtableAppProfileRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableAppProfileRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(apID)
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned cluster, got %+v", rels)
	}
}

func TestResolveBigtableBackupRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tableID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableTable,
		"projects/my-project/instances/i1/tables/t1", "", "{}")
	origBackupID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableBackup,
		"projects/my-project/instances/i1/clusters/c1/backups/orig", "",
		`{"sourceTable": "projects/my-project/instances/i1/tables/t1"}`)
	copyBackupID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableBackup,
		"projects/my-project/instances/i1/clusters/c2/backups/copy", "",
		`{"sourceTable": "projects/my-project/instances/i1/tables/t1", "sourceBackup": "projects/my-project/instances/i1/clusters/c1/backups/orig"}`)

	if err := resolveBigtableBackupRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableBackupRelationships: %v", err)
	}

	origRels, _ := st.RelationshipsFrom(origBackupID)
	if len(origRels) != 1 || origRels[0].ToID != tableID || origRels[0].Kind != store.RelUses {
		t.Errorf("orig backup: got %+v, want →table uses", origRels)
	}

	copyRels, _ := st.RelationshipsFrom(copyBackupID)
	got := map[string]bool{}
	for _, r := range copyRels {
		got[r.ToID] = true
	}
	if !got[tableID] || !got[origBackupID] || len(copyRels) != 2 {
		t.Errorf("copy backup: got %+v, want →table + →origBackup", copyRels)
	}
}

func TestResolveBigtableTableRelationships_RestoredFromBackup(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	backupID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableBackup,
		"projects/my-project/instances/i1/clusters/c1/backups/b1", "", "{}")
	tableID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableTable,
		"projects/my-project/instances/i1/tables/restored", "",
		`{"restoreInfo": {"sourceType": "BACKUP", "backupInfo": {"backup": "projects/my-project/instances/i1/clusters/c1/backups/b1"}}}`)

	if err := resolveBigtableTableRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableTableRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tableID)
	if len(rels) != 1 || rels[0].ToID != backupID || rels[0].Kind != store.RelUses {
		t.Errorf("restored table: got %+v, want →backup uses", rels)
	}
}

func TestResolveBigtableTableRelationships_NoRestoreInfoSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tableID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableTable,
		"projects/my-project/instances/i1/tables/t1", "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableBackup,
		"projects/my-project/instances/i1/clusters/c1/backups/b1", "", "{}")

	if err := resolveBigtableTableRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableTableRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tableID)
	if len(rels) != 0 {
		t.Errorf("want no edge for table with no restoreInfo, got %+v", rels)
	}
}

func TestResolveBigtableHotTabletRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tableID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableTable,
		"projects/my-project/instances/i1/tables/t1", "", "{}")
	htID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableHotTablet,
		"projects/my-project/instances/i1/clusters/c1/hotTablets/ht1", "",
		`{"tableName": "projects/my-project/instances/i1/tables/t1"}`)

	if err := resolveBigtableHotTabletRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableHotTabletRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(htID)
	if len(rels) != 1 || rels[0].ToID != tableID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("hot tablet: got %+v, want →table attached-to", rels)
	}
}

func TestResolveBigtableHotTabletRelationships_NoAttrsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableTable,
		"projects/my-project/instances/i1/tables/t1", "", "{}")
	htID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableHotTablet,
		"projects/my-project/instances/i1/clusters/c1/hotTablets/ht1", "", "{}")

	if err := resolveBigtableHotTabletRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableHotTabletRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(htID)
	if len(rels) != 0 {
		t.Errorf("want no edge for empty attrs, got %+v", rels)
	}
}

func TestResolveBigtableResolvers_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveBigtableAppProfileRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableAppProfileRelationships on empty project: %v", err)
	}
	if err := resolveBigtableBackupRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableBackupRelationships on empty project: %v", err)
	}
	if err := resolveBigtableTableRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableTableRelationships on empty project: %v", err)
	}
	if err := resolveBigtableHotTabletRelationships(p, st); err != nil {
		t.Fatalf("resolveBigtableHotTabletRelationships on empty project: %v", err)
	}
}

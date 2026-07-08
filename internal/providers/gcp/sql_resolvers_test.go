package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
	sqladmin "google.golang.org/api/sqladmin/v1"

	cloudkms "google.golang.org/api/cloudkms/v1"
)

func TestResolveSQLInstanceRelationships_HappyPath(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink, Name: "net-1"}))
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1"}))
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "sa-1@proj-1.iam.gserviceaccount.com"}))
	primaryID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLInstance, "projects/proj-1/instances/primary-1", "us-central1",
		marshalAttrs(t, &sqladmin.DatabaseInstance{Name: "primary-1"}))

	replicaInst := &sqladmin.DatabaseInstance{
		Name:                       "replica-1",
		MasterInstanceName:         "primary-1",
		ServiceAccountEmailAddress: "sa-1@proj-1.iam.gserviceaccount.com",
		DiskEncryptionConfiguration: &sqladmin.DiskEncryptionConfiguration{
			KmsKeyName: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1",
		},
		Settings: &sqladmin.Settings{
			IpConfiguration: &sqladmin.IpConfiguration{
				// Cloud SQL's own doc example format: a relative resource
				// link, NOT the fully-qualified compute self-link format
				// TypeComputeNetwork's NativeID uses above — the mismatch is
				// exactly what networkNameIndex's bare-name lookup handles.
				PrivateNetwork: "/projects/proj-1/global/networks/net-1",
			},
		},
	}
	replicaID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLInstance, "projects/proj-1/instances/replica-1", "us-central1",
		marshalAttrs(t, replicaInst))

	if err := resolveSQLInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(replicaID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(replicaID): %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != "attached-to" || got[keyID] != "uses" || got[saID] != "uses" || got[primaryID] != "attached-to" {
		t.Errorf("want replica->network/cryptoKey/serviceAccount/masterInstance edges, got %+v", rels)
	}
	if len(rels) != 4 {
		t.Errorf("want exactly 4 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveSQLInstanceRelationships_NilFieldsAndUnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLInstance, "projects/proj-1/instances/inst-1", "us-central1",
		marshalAttrs(t, &sqladmin.DatabaseInstance{
			Name:               "inst-1",
			MasterInstanceName: "not-scanned",
			Settings: &sqladmin.Settings{
				IpConfiguration: &sqladmin.IpConfiguration{
					PrivateNetwork: "projects/proj-1/global/networks/not-scanned",
				},
			},
		}))

	if err := resolveSQLInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges (nil optional fields, unscanned targets), got %+v", rels)
	}
}

func TestResolveSQLInstanceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSQLInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLInstanceRelationships on empty project: %v", err)
	}
}

func TestResolveSQLBackupRunRelationships_ToCryptoKey(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1", "us",
		marshalAttrs(t, &cloudkms.CryptoKey{Name: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1"}))

	brID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLBackupRun, "projects/proj-1/instances/inst-1/backupRuns/1", "us-central1",
		marshalAttrs(t, &sqladmin.BackupRun{
			Id: 1,
			DiskEncryptionConfiguration: &sqladmin.DiskEncryptionConfiguration{
				KmsKeyName: "projects/proj-1/locations/us/keyRings/kr-1/cryptoKeys/ck-1",
			},
		}))

	if err := resolveSQLBackupRunRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLBackupRunRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(brID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != "uses" {
		t.Errorf("want backupRun->cryptoKey edge, got %+v", rels)
	}
}

func TestResolveSQLBackupRunRelationships_NilConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	brID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLBackupRun, "projects/proj-1/instances/inst-1/backupRuns/1", "us-central1",
		marshalAttrs(t, &sqladmin.BackupRun{Id: 1}))

	if err := resolveSQLBackupRunRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLBackupRunRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(brID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for nil disk-encryption config, got %+v", rels)
	}
}

func TestResolveSQLBackupRunRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSQLBackupRunRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLBackupRunRelationships on empty project: %v", err)
	}
}

func TestResolveSQLUserRelationships_IamEmailToServiceAccount(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "sa-1@proj-1.iam.gserviceaccount.com"}))

	userID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLUser, "projects/proj-1/instances/inst-1/users/sa-1@proj-1.iam.gserviceaccount.com", "us-central1",
		marshalAttrs(t, &sqladmin.User{
			Name:     "sa-1@proj-1.iam.gserviceaccount.com",
			IamEmail: "sa-1@proj-1.iam.gserviceaccount.com",
		}))

	if err := resolveSQLUserRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLUserRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(userID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != "uses" {
		t.Errorf("want user->serviceAccount edge, got %+v", rels)
	}
}

func TestResolveSQLUserRelationships_EmptyIamEmailSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	userID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLUser, "projects/proj-1/instances/inst-1/users/dbuser", "us-central1",
		marshalAttrs(t, &sqladmin.User{Name: "dbuser"}))

	if err := resolveSQLUserRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLUserRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(userID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for a normal (non-IAM) database user, got %+v", rels)
	}
}

func TestResolveSQLUserRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSQLUserRelationships(p, st); err != nil {
		t.Fatalf("resolveSQLUserRelationships on empty project: %v", err)
	}
}

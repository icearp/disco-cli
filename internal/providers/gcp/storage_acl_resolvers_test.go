package gcp

import (
	"testing"

	storage "google.golang.org/api/storage/v1"
)

func TestResolveStorageHmacKeyRelationships_ToServiceAccount(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "sa-1@proj-1.iam.gserviceaccount.com"}))

	hkID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageHmacKey, "GOOG1234567890", "",
		marshalAttrs(t, &storage.HmacKeyMetadata{
			Id:                  "GOOG1234567890",
			ServiceAccountEmail: "sa-1@proj-1.iam.gserviceaccount.com",
		}))

	if err := resolveStorageHmacKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageHmacKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(hkID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != "uses" {
		t.Errorf("want hmacKey->serviceAccount edge, got %+v", rels)
	}
}

func TestResolveStorageHmacKeyRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	hkID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageHmacKey, "GOOG1234567890", "",
		marshalAttrs(t, &storage.HmacKeyMetadata{
			Id:                  "GOOG1234567890",
			ServiceAccountEmail: "not-scanned@proj-1.iam.gserviceaccount.com",
		}))

	if err := resolveStorageHmacKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageHmacKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(hkID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned service account, got %+v", rels)
	}
}

func TestResolveStorageHmacKeyRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveStorageHmacKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageHmacKeyRelationships on empty project: %v", err)
	}
}

func TestResolveStorageNotificationRelationships_ToPubSubTopic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	topicID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic, "projects/proj-1/topics/topic-1", "",
		marshalAttrs(t, map[string]string{"name": "projects/proj-1/topics/topic-1"}))

	notifID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageNotification, "b/bucket-1/notificationConfigs/1", "",
		marshalAttrs(t, &storage.Notification{
			Id:    "1",
			Topic: "//pubsub.googleapis.com/projects/proj-1/topics/topic-1",
		}))

	if err := resolveStorageNotificationRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageNotificationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(notifID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != topicID || rels[0].Kind != "routes-to" {
		t.Errorf("want notification->pubsubTopic edge, got %+v", rels)
	}
}

func TestResolveStorageNotificationRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	notifID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageNotification, "b/bucket-1/notificationConfigs/1", "",
		marshalAttrs(t, &storage.Notification{
			Id:    "1",
			Topic: "//pubsub.googleapis.com/projects/proj-1/topics/not-scanned",
		}))

	if err := resolveStorageNotificationRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageNotificationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(notifID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned topic, got %+v", rels)
	}
}

func TestResolveStorageNotificationRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveStorageNotificationRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageNotificationRelationships on empty project: %v", err)
	}
}

func TestResolveStorageAccessControlRelationships_ServiceAccountEntity(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "sa-1@proj-1.iam.gserviceaccount.com"}))

	bacID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucketAccessControl, "bucket-1/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, &storage.BucketAccessControl{
			Entity: "user-sa-1@proj-1.iam.gserviceaccount.com",
			Email:  "sa-1@proj-1.iam.gserviceaccount.com",
			Role:   "OWNER",
		}))
	doacID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageDefaultObjectAccessControl, "bucket-1/default/sa-1@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, &storage.ObjectAccessControl{
			Entity: "user-sa-1@proj-1.iam.gserviceaccount.com",
			Email:  "sa-1@proj-1.iam.gserviceaccount.com",
			Role:   "READER",
		}))

	if err := resolveStorageAccessControlRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageAccessControlRelationships: %v", err)
	}

	for name, id := range map[string]string{"bucketAccessControl": bacID, "defaultObjectAccessControl": doacID} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", name, err)
		}
		if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != "uses" {
			t.Errorf("%s: want ->serviceAccount edge, got %+v", name, rels)
		}
	}
}

func TestResolveStorageAccessControlRelationships_NonServiceAccountEntitySkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	bacID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucketAccessControl, "bucket-1/allUsers", "",
		marshalAttrs(t, &storage.BucketAccessControl{
			Entity: "allUsers",
			Role:   "READER",
		}))

	if err := resolveStorageAccessControlRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageAccessControlRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bacID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for a non-service-account entity (empty email), got %+v", rels)
	}
}

func TestResolveStorageAccessControlRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveStorageAccessControlRelationships(p, st); err != nil {
		t.Fatalf("resolveStorageAccessControlRelationships on empty project: %v", err)
	}
}

package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveLoggingSinkRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket,
		"https://www.googleapis.com/storage/v1/b/log-bucket", "us", "{}")
	dsID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset,
		"my-project:logs", "us", "{}")
	topicID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic,
		"projects/my-project/topics/log-topic", "", "{}")

	storageSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-storage", "",
		`{"destination": "storage.googleapis.com/log-bucket"}`)
	bqSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-bq", "",
		`{"destination": "bigquery.googleapis.com/projects/my-project/datasets/logs"}`)
	pubSubSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-pubsub", "",
		`{"destination": "pubsub.googleapis.com/projects/my-project/topics/log-topic"}`)
	// Unknown destination — must not error.
	upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/orphan", "",
		`{"destination": "logging.googleapis.com/projects/my-project/locations/us/buckets/_Default"}`)

	if err := resolveLoggingSinkRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingSinkRelationships: %v", err)
	}

	check := func(label, fromID, want string) {
		t.Helper()
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != want || rels[0].Kind != store.RelRoutesTo {
			t.Errorf("%s: got %+v, want →%s routes-to", label, rels, want)
		}
	}
	check("storage", storageSink, bucketID)
	check("bigquery", bqSink, dsID)
	check("pubsub", pubSubSink, topicID)
}

func TestResolveLoggingBucketRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingBucket,
		"projects/my-project/locations/global/buckets/cmek-bucket", "global",
		`{"cmekSettings": {"kmsKeyName": "`+keyName+`"}}`)
	plainBucketID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingBucket,
		"projects/my-project/locations/global/buckets/_Default", "global", "{}")

	if err := resolveLoggingBucketRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingBucketRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(bucketID)
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("cmek bucket: got %+v, want →key uses", rels)
	}
	plainRels, _ := st.RelationshipsFrom(plainBucketID)
	if len(plainRels) != 0 {
		t.Errorf("plain bucket: want no edge, got %+v", plainRels)
	}
}

func TestResolveLoggingLinkRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	dsID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:logs_ds", "us", "{}")
	linkID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingLink,
		"projects/my-project/locations/global/buckets/_Default/links/logs_ds", "",
		`{"bigqueryDataset": {"datasetId": "bigquery.googleapis.com/projects/my-project/datasets/logs_ds"}}`)

	if err := resolveLoggingLinkRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLinkRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 1 || rels[0].ToID != dsID || rels[0].Kind != store.RelUses {
		t.Errorf("link: got %+v, want →dataset uses", rels)
	}
}

func TestResolveLoggingLinkRelationships_NoDatasetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	linkID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingLink,
		"projects/my-project/locations/global/buckets/_Default/links/l1", "", "{}")

	if err := resolveLoggingLinkRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLinkRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 0 {
		t.Errorf("want no edge for empty attrs, got %+v", rels)
	}
}

// TestResolveLoggingLogScopeRelationships covers both resourceNames shapes:
// a bare "projects/{id}" entry (same-project) and a full log-view resource
// name entry.
func TestResolveLoggingLogScopeRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	hostProjID := upsertTestResource(t, st, "gcp", p.ID, TypeProject, p.ID, "", "{}")
	viewID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingView,
		"projects/my-project/locations/global/buckets/_Default/views/v1", "global", "{}")

	scopeID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingLogScope,
		"projects/my-project/locations/global/logScopes/ls-1", "global",
		`{"resourceNames": ["projects/my-project", "projects/my-project/locations/global/buckets/_Default/views/v1"]}`)

	if err := resolveLoggingLogScopeRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLogScopeRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(scopeID)
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[hostProjID] || !got[viewID] || len(rels) != 2 {
		t.Errorf("log scope: got %+v, want →project + →view", rels)
	}
}

// TestResolveLoggingLogScopeRelationships_ForeignProjectPlaceholder covers a
// resourceNames entry naming a project outside scan scope — must insert a
// placeholder self-node rather than erroring on a dangling FK.
func TestResolveLoggingLogScopeRelationships_ForeignProjectPlaceholder(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	scopeID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingLogScope,
		"projects/my-project/locations/global/logScopes/ls-1", "global",
		`{"resourceNames": ["projects/other-project"]}`)

	if err := resolveLoggingLogScopeRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLogScopeRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(scopeID)
	wantID := store.ResourceID("gcp", "other-project", "other-project")
	if len(rels) != 1 || rels[0].ToID != wantID || rels[0].Kind != store.RelUses {
		t.Errorf("foreign project ref: got %+v, want →%s uses", rels, wantID)
	}
}

func TestResolveLoggingLogScopeRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveLoggingLogScopeRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLogScopeRelationships on empty project: %v", err)
	}
}

func TestResolveLoggingMetricRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingBucket,
		"projects/my-project/locations/global/buckets/_Default", "global", "{}")
	bucketMetricID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingMetric,
		"projects/my-project/metrics/m-bucket", "",
		`{"bucketName": "projects/my-project/locations/global/buckets/_Default"}`)
	projectMetricID := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingMetric,
		"projects/my-project/metrics/m-project", "", "{}")

	if err := resolveLoggingMetricRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingMetricRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(bucketMetricID)
	if len(rels) != 1 || rels[0].ToID != bucketID || rels[0].Kind != store.RelUses {
		t.Errorf("bucket-scoped metric: got %+v, want →bucket uses", rels)
	}
	projRels, _ := st.RelationshipsFrom(projectMetricID)
	if len(projRels) != 0 {
		t.Errorf("project-scoped metric: want no edge, got %+v", projRels)
	}
}

func TestResolveLoggingObservabilityResolvers_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveLoggingBucketRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingBucketRelationships on empty project: %v", err)
	}
	if err := resolveLoggingLinkRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingLinkRelationships on empty project: %v", err)
	}
	if err := resolveLoggingMetricRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingMetricRelationships on empty project: %v", err)
	}
	if err := resolveMonitoringServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringServiceRelationships on empty project: %v", err)
	}
}

func TestResolveMonitoringServiceRelationships_CloudRunAndGKEVariants(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	runID := upsertNamedTestResource(t, st, "gcp", p.ID, TypeCloudRunSvc,
		"projects/my-project/locations/us-central1/services/svc-1", "us-central1", "svc-1", "{}")
	clusterID := upsertNamedTestResource(t, st, "gcp", p.ID, TypeGKECluster,
		"https://container.googleapis.com/v1/projects/my-project/locations/us-central1-a/clusters/prod",
		"us-central1-a", "prod", "{}")

	runSvcID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/run-svc-1", "",
		`{"cloudRun": {"location": "us-central1", "serviceName": "svc-1"}}`)
	istioSvcID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/istio-svc-1", "",
		`{"clusterIstio": {"clusterName": "prod", "location": "us-central1-a", "serviceName": "s", "serviceNamespace": "ns"}}`)
	gkeNsID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/gke-ns-svc-1", "",
		`{"gkeNamespace": {"clusterName": "prod", "location": "us-central1-a", "namespaceName": "ns"}}`)
	gkeSvcID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/gke-svc-1", "",
		`{"gkeService": {"clusterName": "prod", "location": "us-central1-a", "serviceName": "s", "namespaceName": "ns"}}`)
	gkeWorkloadID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/gke-workload-svc-1", "",
		`{"gkeWorkload": {"clusterName": "prod", "location": "us-central1-a", "topLevelControllerName": "w", "topLevelControllerType": "Deployment"}}`)

	if err := resolveMonitoringServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringServiceRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(runSvcID)
	if len(rels) != 1 || rels[0].ToID != runID || rels[0].Kind != store.RelUses {
		t.Errorf("cloudRun: got %+v, want →run.service uses", rels)
	}
	for label, id := range map[string]string{
		"clusterIstio": istioSvcID, "gkeNamespace": gkeNsID, "gkeService": gkeSvcID, "gkeWorkload": gkeWorkloadID,
	} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 1 || rels[0].ToID != clusterID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: got %+v, want →GKE cluster uses", label, rels)
		}
	}
}

func TestResolveMonitoringServiceRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	svcID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/run-svc-1", "",
		`{"cloudRun": {"location": "us-central1", "serviceName": "not-scanned"}, `+
			`"gkeWorkload": {"clusterName": "not-scanned", "location": "us-central1-a", "topLevelControllerName": "w", "topLevelControllerType": "Deployment"}}`)

	if err := resolveMonitoringServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveMonitoringServiceRelationships_NoIdentifierNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	svcID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService,
		"projects/my-project/services/custom-svc-1", "", `{"custom": {}}`)

	if err := resolveMonitoringServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for a non-resolvable identifier oneof, got %+v", rels)
	}
}

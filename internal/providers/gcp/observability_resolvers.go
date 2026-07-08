package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveLoggingSinkRelationships,
		EdgeDecl{TypeLoggingSink, TypeStorageBucket, store.RelRoutesTo},
		EdgeDecl{TypeLoggingSink, TypeBQDataset, store.RelRoutesTo},
		EdgeDecl{TypeLoggingSink, TypePubSubTopic, store.RelRoutesTo},
	)
	registerResolver(resolveLoggingBucketRelationships,
		EdgeDecl{TypeLoggingBucket, TypeKMSCryptoKey, store.RelUses},
	)
	registerResolver(resolveLoggingLinkRelationships,
		EdgeDecl{TypeLoggingLink, TypeBQDataset, store.RelUses},
	)
	registerResolver(resolveLoggingLogScopeRelationships,
		EdgeDecl{TypeLoggingLogScope, TypeProject, store.RelUses},
		EdgeDecl{TypeLoggingLogScope, TypeLoggingView, store.RelUses},
	)
	registerResolver(resolveLoggingMetricRelationships,
		EdgeDecl{TypeLoggingMetric, TypeLoggingBucket, store.RelUses},
	)
}

// bqDatasetIDFromResourcePath converts a BigQuery resource path of the form
// "projects/{p}/datasets/{ds}" into the canonical "{p}:{ds}" NativeID
// BigQuery datasets are scanned under, or reports ok=false if path doesn't
// match that shape. Shared by resolveLoggingSinkRelationships's
// bigquery.googleapis.com/ destination and resolveLoggingLinkRelationships's
// BigqueryDataset.DatasetId (same path shape, different wrapping prefix).
func bqDatasetIDFromResourcePath(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[0] == "projects" && parts[2] == "datasets" {
		return parts[1] + ":" + parts[3], true
	}
	return "", false
}

// resolveLoggingSinkRelationships derives sink -[routes-to]-> destination
// edges. Logging sink destinations come in three canonical shapes:
//
//   - storage.googleapis.com/{bucket}                                → gcp:storage:bucket
//   - bigquery.googleapis.com/projects/{p}/datasets/{ds}             → gcp:bigquery:dataset
//   - pubsub.googleapis.com/projects/{p}/topics/{topic}              → gcp:pubsub:topic
//
// Logbucket destinations (`logging.googleapis.com/projects/{p}/locations/{l}/buckets/{b}`)
// deferred — log-bucket scanner not yet landed.
func resolveLoggingSinkRelationships(p *project, st *store.Store) error {
	sinks, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeLoggingSink},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(sinks) == 0 {
		return nil
	}

	// Per-type destination indexes.
	bucketIDByNative := map[string]string{}
	bs, _ := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeStorageBucket},
		Limit: util.AllResources,
	})
	for _, b := range bs {
		// Storage bucket NativeID is the SelfLink (e.g.
		// "https://www.googleapis.com/storage/v1/b/my-bucket"); index by
		// bucket name to match sink destinations.
		if i := strings.LastIndex(b.NativeID, "/"); i >= 0 {
			bucketIDByNative[b.NativeID[i+1:]] = b.ID
		}
	}

	dsIDByNative := map[string]string{}
	dss, _ := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBQDataset},
		Limit: util.AllResources,
	})
	for _, d := range dss {
		// BigQuery dataset NativeID is canonical "{project}:{dataset}"; sinks
		// reference datasets as "projects/{p}/datasets/{ds}" — convert to
		// canonical form.
		dsIDByNative[d.NativeID] = d.ID
	}

	topicIDByNative := map[string]string{}
	ts, _ := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypePubSubTopic},
		Limit: util.AllResources,
	})
	for _, t := range ts {
		// Topic NativeID is full resource name "projects/{p}/topics/{topic}".
		topicIDByNative[t.NativeID] = t.ID
	}

	for _, s := range sinks {
		var a struct {
			Destination string `json:"destination"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		var toID string
		switch {
		case strings.HasPrefix(a.Destination, "storage.googleapis.com/"):
			bucket := strings.TrimPrefix(a.Destination, "storage.googleapis.com/")
			toID = bucketIDByNative[bucket]
		case strings.HasPrefix(a.Destination, "pubsub.googleapis.com/"):
			toID = topicIDByNative[strings.TrimPrefix(a.Destination, "pubsub.googleapis.com/")]
		case strings.HasPrefix(a.Destination, "bigquery.googleapis.com/"):
			path := strings.TrimPrefix(a.Destination, "bigquery.googleapis.com/")
			if native, ok := bqDatasetIDFromResourcePath(path); ok {
				toID = dsIDByNative[native]
			}
		}
		if toID == "" {
			continue
		}
		if err := st.UpsertRelationship(s.ID, toID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sink→destination: %w", err)
		}
	}
	return nil
}

// resolveLoggingBucketRelationships wires LogBucket -> the CryptoKey
// encrypting it (`cmekSettings.kmsKeyName`, full resource name).
func resolveLoggingBucketRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeLoggingBucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	if len(keyIDByNative) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			CmekSettings *struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"cmekSettings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CmekSettings == nil || attrs.CmekSettings.KmsKeyName == "" {
			continue
		}
		keyID, ok := keyIDByNative[stripCryptoKeyVersion(attrs.CmekSettings.KmsKeyName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert logBucket→cryptoKey: %w", err)
		}
	}
	return nil
}

// resolveLoggingLinkRelationships wires Link -> the BigQuery dataset it
// created (`bigqueryDataset.datasetId`, in the form
// "bigquery.googleapis.com/projects/{p}/datasets/{ds}" — same conversion as
// resolveLoggingSinkRelationships's bigquery destination).
func resolveLoggingLinkRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeLoggingLink},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedDatasets, err := scannedIDSet(p, st, TypeBQDataset)
	if err != nil {
		return err
	}
	if len(scannedDatasets) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			BigqueryDataset *struct {
				DatasetID string `json:"datasetId"`
			} `json:"bigqueryDataset"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.BigqueryDataset == nil {
			continue
		}
		path := strings.TrimPrefix(attrs.BigqueryDataset.DatasetID, "bigquery.googleapis.com/")
		native, ok := bqDatasetIDFromResourcePath(path)
		if !ok {
			continue
		}
		if err := upsertIfScanned(st, scannedDatasets, r.ID, "gcp", p.ID, TypeBQDataset, native, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveLoggingLogScopeRelationships wires LogScope -> each parent resource
// named in `resourceNames[]`: bare "projects/{id}" entries resolve to the
// referenced Project (cross-project references get an empty-attribute
// placeholder inserted at their self-node natural key, mirroring
// resolveMonitoringDashboardRelationships); "projects/.../buckets/.../views/..."
// entries resolve to the named LogView by exact NativeID match (a log scope
// names either projects or views, never a mix, per the SDK doc, but each
// entry is classified independently so either shape is handled).
func resolveLoggingLogScopeRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeLoggingLogScope},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanID := rows[0].DiscoveredBy
	scannedViews, err := scannedIDSet(p, st, TypeLoggingView)
	if err != nil {
		return err
	}

	type pendingProjectEdge struct {
		fromID    string
		projectID string
	}
	var pendingProjects []pendingProjectEdge
	foreignProjects := map[string]struct{}{}

	for _, r := range rows {
		var attrs struct {
			ResourceNames []string `json:"resourceNames"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seenProjects := map[string]bool{}
		for _, name := range attrs.ResourceNames {
			if strings.Contains(name, "/views/") {
				if err := upsertIfScanned(st, scannedViews, r.ID, "gcp", p.ID, TypeLoggingView, name, store.RelUses); err != nil {
					return err
				}
				continue
			}
			projectID := strings.TrimPrefix(name, "projects/")
			if projectID == "" || projectID == name || seenProjects[projectID] {
				continue
			}
			seenProjects[projectID] = true
			pendingProjects = append(pendingProjects, pendingProjectEdge{fromID: r.ID, projectID: projectID})
			if projectID != p.ID {
				foreignProjects[projectID] = struct{}{}
			}
		}
	}
	if len(pendingProjects) == 0 {
		return nil
	}

	if len(foreignProjects) > 0 {
		placeholders := make([]*store.Resource, 0, len(foreignProjects))
		for proj := range foreignProjects {
			name := proj
			placeholders = append(placeholders, &store.Resource{
				Provider:       "gcp",
				AccountID:      proj,
				Type:           TypeProject,
				NativeID:       proj,
				Name:           &name,
				AttributesJSON: "{}",
				DiscoveredBy:   scanID,
			})
		}
		if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
			return fmt.Errorf("insert referenced-project placeholders: %w", err)
		}
	}

	for _, e := range pendingProjects {
		toID := store.ResourceID("gcp", e.projectID, TypeProject, e.projectID)
		if err := st.UpsertRelationship(e.fromID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert logScope→project: %w", err)
		}
	}
	return nil
}

// resolveLoggingMetricRelationships wires LogMetric -> the LogBucket it's
// scoped to (`bucketName`, full resource name; empty for project-scoped
// metrics, which are not Bucket-owned).
func resolveLoggingMetricRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeLoggingMetric},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedBuckets, err := scannedIDSet(p, st, TypeLoggingBucket)
	if err != nil {
		return err
	}
	if len(scannedBuckets) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			BucketName string `json:"bucketName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.BucketName == "" {
			continue
		}
		if err := upsertIfScanned(st, scannedBuckets, r.ID, "gcp", p.ID, TypeLoggingBucket, attrs.BucketName, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

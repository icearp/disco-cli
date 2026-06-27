package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveLoggingSinkRelationships) }

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
		// "https://www.googleapis.com/storage/v1/b/my-bucket"). Index by
		// bucket name for a fast match against the sink destination.
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
		// BigQuery dataset NativeID is the canonical "{project}:{dataset}".
		// Sinks reference datasets in path form
		// "projects/{p}/datasets/{ds}" — convert to canonical form.
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
			// Convert "projects/{p}/datasets/{ds}" → "{p}:{ds}".
			parts := strings.Split(path, "/")
			if len(parts) == 4 && parts[0] == "projects" && parts[2] == "datasets" {
				toID = dsIDByNative[parts[1]+":"+parts[3]]
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

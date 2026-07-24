package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Resolver Wave R22 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the first resolvers for `dataflow_scanners.go`'s Job
// and Snapshot (no dataflow_resolvers.go existed before this wave).
//
// Job.environment carries `serviceAccountEmail` (plain email) and
// `serviceKmsKeyName` (a full CryptoKey resource name, verified via
// `go doc dataflow.Environment`), plus `workerPools[]` — each pool has its
// own `network`/`subnetwork` (bare Compute names, `subnetwork` documented as
// `regions/{region}/subnetworks/{name}`, matched by trailing segment like
// every other bare-name Compute reference in this package). Multiple worker
// pools can in principle reference distinct networks, so all pools are
// walked and deduped through the same bare-name index rather than assuming
// only the first pool matters. `environment.tempStoragePrefix` (a GCS
// staging path in one of two undocumented-boundary shapes,
// `storage.googleapis.com/{bucket}/...` or `{bucket}.storage.googleapis.com/...`)
// is deliberately NOT wired — parsing either shape reliably would need a
// guess-format parser with no live-account sample to verify against, and the
// field is explicitly documented as commonly overridden/unset by pipeline
// options outside this typed struct. Revisit if a live account surfaces
// real, non-empty values.
//
// Snapshot.sourceJobId is a bare job ID; combined with the Snapshot row's
// own Region (Dataflow jobs and their snapshots are both region-scoped, see
// scanDataflowWithClient), the composite Job NativeID is reconstructed
// exactly — same pattern as Dataproc's Job->Cluster resolver (R12/R19).
// Snapshot.pubsubMetadata[].topicName is a full Pub/Sub topic resource name,
// matched by exact NativeID (Topic's own NativeID is scanned in that same
// format, see pubsub_scanners.go).
func init() {
	registerResolver(resolveDataflowJobRelationships,
		EdgeDecl{TypeDataflowJob, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeDataflowJob, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeDataflowJob, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeDataflowJob, TypeComputeSubnet, store.RelAttachedTo},
	)
	registerResolver(resolveDataflowSnapshotRelationships,
		EdgeDecl{TypeDataflowSnapshot, TypeDataflowJob, store.RelUses},
		EdgeDecl{TypeDataflowSnapshot, TypePubSubTopic, store.RelUses},
	)
}

func resolveDataflowJobRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataflowJob},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	netByName, err := bareNameIndex(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	subnetByName, err := bareNameIndex(p, st, TypeComputeSubnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Environment *struct {
				ServiceAccountEmail string `json:"serviceAccountEmail"`
				ServiceKmsKeyName   string `json:"serviceKmsKeyName"`
				WorkerPools         []*struct {
					Network    string `json:"network"`
					Subnetwork string `json:"subnetwork"`
				} `json:"workerPools"`
			} `json:"environment"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		env := attrs.Environment
		if env == nil {
			continue
		}
		if env.ServiceAccountEmail != "" {
			if toID, ok := saByEmail[env.ServiceAccountEmail]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert job→serviceAccount: %w", err)
				}
			}
		}
		if env.ServiceKmsKeyName != "" {
			if toID, ok := keyIDByNative[stripCryptoKeyVersion(env.ServiceKmsKeyName)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert job→cryptoKey: %w", err)
				}
			}
		}
		for _, wp := range env.WorkerPools {
			if wp == nil {
				continue
			}
			if wp.Network != "" {
				if toID, ok := netByName[lastSegment(wp.Network)]; ok {
					if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert job→network: %w", err)
					}
				}
			}
			if wp.Subnetwork != "" {
				if toID, ok := subnetByName[lastSegment(wp.Subnetwork)]; ok {
					if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert job→subnetwork: %w", err)
					}
				}
			}
		}
	}
	return nil
}

func resolveDataflowSnapshotRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataflowSnapshot},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedJobs, err := scannedIDSet(p, st, TypeDataflowJob)
	if err != nil {
		return err
	}
	scannedTopics, err := scannedIDSet(p, st, TypePubSubTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceJobID    string `json:"sourceJobId"`
			PubsubMetadata []*struct {
				TopicName string `json:"topicName"`
			} `json:"pubsubMetadata"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SourceJobID != "" && r.Region != nil && len(scannedJobs) > 0 {
			jobNativeID := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", p.ID, *r.Region, attrs.SourceJobID)
			if err := upsertIfScanned(st, scannedJobs, r.ID, "gcp", p.ID, TypeDataflowJob, jobNativeID, store.RelUses); err != nil {
				return fmt.Errorf("upsert snapshot→job: %w", err)
			}
		}
		if len(scannedTopics) > 0 {
			for _, m := range attrs.PubsubMetadata {
				if m == nil || m.TopicName == "" {
					continue
				}
				if err := upsertIfScanned(st, scannedTopics, r.ID, "gcp", p.ID, TypePubSubTopic, m.TopicName, store.RelUses); err != nil {
					return fmt.Errorf("upsert snapshot→topic: %w", err)
				}
			}
		}
	}
	return nil
}

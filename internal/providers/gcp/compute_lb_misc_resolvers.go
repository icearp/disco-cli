package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Resolver Wave R4 (part 2) of the resolver-implementation backlog
// (ROADMAP.md "Resolver buildout"): the network-LB (TargetPool/TargetInstance)
// surface and BackendBucket's cross-provider link to the GCS bucket it fronts
// — neither shares the HTTP(S)/gRPC/SSL/TCP proxy chain in
// loadbalancing_resolvers.go.
func init() {
	registerResolver(resolveBackendBucketRelationships,
		EdgeDecl{TypeComputeBackendBucket, TypeStorageBucket, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendBucket, TypeStorageBucket, store.RelUses},
	)
	registerResolver(resolveTargetPoolRelationships,
		EdgeDecl{TypeComputeTargetPool, TypeComputeInstance, store.RelAttachedTo},
		EdgeDecl{TypeComputeTargetPool, TypeComputeHTTPHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeTargetPool, TypeComputeTargetPool, store.RelAttachedTo},
	)
	registerResolver(resolveTargetInstanceRelationships,
		EdgeDecl{TypeComputeTargetInstance, TypeComputeInstance, store.RelAttachedTo},
		EdgeDecl{TypeComputeTargetInstance, TypeComputeNetwork, store.RelAttachedTo},
	)
}

// resolveBackendBucketRelationships links a BackendBucket to the GCS bucket
// it fronts. bucketName is documented as the bare Cloud Storage bucket name
// (unlike every self-link field elsewhere in the LB chain), so lookup is by
// name, not ResourceID.
func resolveBackendBucketRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeBackendBucket, TypeComputeRegionBackendBucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	buckets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeStorageBucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	bucketIDByName := make(map[string]string, len(buckets))
	for _, b := range buckets {
		if b.Name != nil {
			bucketIDByName[*b.Name] = b.ID
		}
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
		bucketID, ok := bucketIDByName[attrs.BucketName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backend-bucket→storage-bucket: %w", err)
		}
	}
	return nil
}

func resolveTargetPoolRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeTargetPool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeInstance, TypeComputeHTTPHealthCheck, TypeComputeTargetPool)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Instances    []string `json:"instances"`
			HealthChecks []string `json:"healthChecks"`
			BackupPool   string   `json:"backupPool"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, inst := range attrs.Instances {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInstance, inst, store.RelAttachedTo); err != nil {
				return err
			}
		}
		for _, hc := range attrs.HealthChecks {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeHTTPHealthCheck, hc, store.RelUses); err != nil {
				return err
			}
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeTargetPool, attrs.BackupPool, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

func resolveTargetInstanceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeTargetInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeInstance, TypeComputeNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Instance string `json:"instance"`
			Network  string `json:"network"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInstance, attrs.Instance, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

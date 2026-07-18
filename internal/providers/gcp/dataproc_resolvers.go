package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R12 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Dataproc Cluster and Job. Cluster's
// gceClusterConfig.{networkUri,subnetworkUri} accept a full URL, partial
// URI, or short name per the SDK's own doc comment — resolved by bare name
// (bareNameIndex) rather than exact match, since all three input shapes
// collapse to the same trailing segment. serviceAccount is a plain email
// (buildSAEmailIndex). encryptionConfig.{gcePdKmsKeyName,kmsKey} and
// configBucket are same-cloudkms/storage-family resource names — the
// former matches CryptoKey's NativeID exactly (loadKMSCryptoKeyIndex), the
// latter is documented as a bare bucket name (not a gs:// URI or self-link),
// so it needs bareNameIndex too. Job's placement.clusterName is a bare
// cluster name; combined with the Job row's own Region (dataproc is
// region-scoped, see scanDataprocIn), the composite Cluster NativeID can be
// reconstructed exactly — no bare-name index needed there.
func init() {
	registerResolver(resolveDataprocClusterRelationships,
		EdgeDecl{TypeDataprocCluster, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeDataprocCluster, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeDataprocCluster, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeDataprocCluster, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeDataprocCluster, TypeStorageBucket, store.RelUses},
	)
	registerResolver(resolveDataprocJobRelationships,
		EdgeDecl{TypeDataprocJob, TypeDataprocCluster, store.RelUses},
	)
}

// dataprocClusterConfigAttrs mirrors dataproc.ClusterConfig's JSON shape —
// shared verbatim by Cluster's own top-level `config` and, since
// WorkflowTemplate.Placement.ManagedCluster.Config is typed *ClusterConfig*
// too (Wave R19), WorkflowTemplate's managed-cluster config.
type dataprocClusterConfigAttrs struct {
	GceClusterConfig *struct {
		NetworkURI     string `json:"networkUri"`
		SubnetworkURI  string `json:"subnetworkUri"`
		ServiceAccount string `json:"serviceAccount"`
	} `json:"gceClusterConfig"`
	EncryptionConfig *struct {
		GcePdKmsKeyName string `json:"gcePdKmsKeyName"`
		KmsKey          string `json:"kmsKey"`
	} `json:"encryptionConfig"`
	ConfigBucket string `json:"configBucket"`
}

type dataprocClusterAttrs struct {
	Config dataprocClusterConfigAttrs `json:"config"`
}

// dataprocRelIndex bundles the target-type lookup maps shared by every
// Dataproc resolver in this file — built once per resolver call, not once
// per row.
type dataprocRelIndex struct {
	netByName, subnetByName, bucketByName map[string]string
	saByEmail, keyIDByNative              map[string]string
}

func loadDataprocRelIndex(p *project, st *store.Store) (dataprocRelIndex, error) {
	var idx dataprocRelIndex
	var err error
	if idx.netByName, err = bareNameIndex(p, st, TypeComputeNetwork); err != nil {
		return idx, err
	}
	if idx.subnetByName, err = bareNameIndex(p, st, TypeComputeSubnet); err != nil {
		return idx, err
	}
	if idx.bucketByName, err = bareNameIndex(p, st, TypeStorageBucket); err != nil {
		return idx, err
	}
	if idx.saByEmail, err = buildSAEmailIndex(p, st); err != nil {
		return idx, err
	}
	if idx.keyIDByNative, err = loadKMSCryptoKeyIndex(p, st); err != nil {
		return idx, err
	}
	return idx, nil
}

// wireDataprocClusterConfig wires the network/subnetwork/serviceAccount/
// cryptoKey/bucket edges shared by Cluster's own config and (Wave R19)
// WorkflowTemplate's managed-cluster config — both dataproc.ClusterConfig.
func wireDataprocClusterConfig(st *store.Store, fromID string, cfg *dataprocClusterConfigAttrs, idx dataprocRelIndex) error {
	if cfg == nil {
		return nil
	}
	if gc := cfg.GceClusterConfig; gc != nil {
		if toID, ok := idx.netByName[lastSegment(gc.NetworkURI)]; ok && gc.NetworkURI != "" {
			if err := st.UpsertRelationship(fromID, toID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert →network: %w", err)
			}
		}
		if toID, ok := idx.subnetByName[lastSegment(gc.SubnetworkURI)]; ok && gc.SubnetworkURI != "" {
			if err := st.UpsertRelationship(fromID, toID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert →subnetwork: %w", err)
			}
		}
		if toID, ok := idx.saByEmail[gc.ServiceAccount]; ok && gc.ServiceAccount != "" {
			if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert →serviceAccount: %w", err)
			}
		}
	}
	if ec := cfg.EncryptionConfig; ec != nil {
		for _, kmsKeyName := range []string{ec.GcePdKmsKeyName, ec.KmsKey} {
			if kmsKeyName == "" {
				continue
			}
			if toID, ok := idx.keyIDByNative[stripCryptoKeyVersion(kmsKeyName)]; ok {
				if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert →cryptoKey: %w", err)
				}
			}
		}
	}
	if cfg.ConfigBucket != "" {
		if toID, ok := idx.bucketByName[cfg.ConfigBucket]; ok {
			if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert →bucket: %w", err)
			}
		}
	}
	return nil
}

func resolveDataprocClusterRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadDataprocRelIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs dataprocClusterAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := wireDataprocClusterConfig(st, r.ID, &attrs.Config, idx); err != nil {
			return fmt.Errorf("cluster %s: %w", r.NativeID, err)
		}
	}
	return nil
}

// resolveDataprocJobRelationships wires Job -> the Cluster it's placed on
// (`placement.clusterName`, a bare cluster name; the composite Cluster
// NativeID is reconstructed using the Job row's own Region since Dataproc
// clusters and jobs are both region-scoped — see scanDataprocIn).
func resolveDataprocJobRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocJob},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeDataprocCluster)
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Placement struct {
				ClusterName string `json:"clusterName"`
			} `json:"placement"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Placement.ClusterName == "" || r.Region == nil {
			continue
		}
		nativeID := fmt.Sprintf("projects/%s/regions/%s/clusters/%s", p.ID, *r.Region, attrs.Placement.ClusterName)
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeDataprocCluster, nativeID, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// Resolver Wave R19 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Batch, Session, SessionTemplate, WorkflowTemplate.
// Batch/Session/SessionTemplate share environmentConfig.executionConfig's
// flat {networkUri,subnetworkUri,serviceAccount,kmsKey,stagingBucket} shape
// (a different, flatter struct than Cluster's nested gceClusterConfig +
// encryptionConfig — verified via `go doc dataproc.ExecutionConfig`).
// WorkflowTemplate.placement.managedCluster.config is typed
// *dataproc.ClusterConfig* — byte-identical to Cluster's own top-level
// config, reused via dataprocClusterConfigAttrs/wireDataprocClusterConfig
// above. WorkflowTemplate also carries its own top-level
// encryptionConfig.kmsKey (CMEK for workflow job arguments — a distinct
// field from the managed cluster's own encryption). ClusterSelector (the
// other half of WorkflowTemplatePlacement's oneof) is a label matcher, not
// a resource reference — no edge to emit. Session additionally references
// its SessionTemplate, which the SDK doc says may arrive as either a bare
// resource name or a full URL — both share the same trailing
// "projects/.../sessionTemplates/{id}" path, normalized via
// dataprocResourceNameSuffix before matching against the scanned
// SessionTemplate's own NativeID (= its bare resource name).
func init() {
	registerResolver(resolveDataprocBatchRelationships, dataprocFamilyEdges(TypeDataprocBatch)...)
	registerResolver(resolveDataprocSessionRelationships, append(dataprocFamilyEdges(TypeDataprocSession),
		EdgeDecl{TypeDataprocSession, TypeDataprocSessionTemplate, store.RelUses})...)
	registerResolver(resolveDataprocSessionTemplateRelationships, dataprocFamilyEdges(TypeDataprocSessionTemplate)...)
	registerResolver(resolveDataprocWorkflowTemplateRelationships, dataprocFamilyEdges(TypeDataprocWorkflowTemplate)...)
}

// dataprocFamilyEdges is the network/subnetwork/serviceAccount/cryptoKey/
// bucket edge quintet shared by every Wave R19 resolver's source type.
func dataprocFamilyEdges(from string) []EdgeDecl {
	return []EdgeDecl{
		{from, TypeComputeNetwork, store.RelAttachedTo},
		{from, TypeComputeSubnet, store.RelAttachedTo},
		{from, TypeIAMServiceAccount, store.RelUses},
		{from, TypeKMSCryptoKey, store.RelUses},
		{from, TypeStorageBucket, store.RelUses},
	}
}

// dataprocExecutionConfigAttrs mirrors dataproc.ExecutionConfig's flat JSON
// shape, shared by Batch/Session/SessionTemplate's environmentConfig.
type dataprocExecutionConfigAttrs struct {
	NetworkURI     string `json:"networkUri"`
	SubnetworkURI  string `json:"subnetworkUri"`
	ServiceAccount string `json:"serviceAccount"`
	KmsKey         string `json:"kmsKey"`
	StagingBucket  string `json:"stagingBucket"`
}

// wireDataprocExecutionConfig wires the network/subnetwork/serviceAccount/
// cryptoKey/bucket edges shared by Batch/Session/SessionTemplate.
func wireDataprocExecutionConfig(st *store.Store, fromID string, ec *dataprocExecutionConfigAttrs, idx dataprocRelIndex) error {
	if ec == nil {
		return nil
	}
	if toID, ok := idx.netByName[lastSegment(ec.NetworkURI)]; ok && ec.NetworkURI != "" {
		if err := st.UpsertRelationship(fromID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert →network: %w", err)
		}
	}
	if toID, ok := idx.subnetByName[lastSegment(ec.SubnetworkURI)]; ok && ec.SubnetworkURI != "" {
		if err := st.UpsertRelationship(fromID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert →subnetwork: %w", err)
		}
	}
	if toID, ok := idx.saByEmail[ec.ServiceAccount]; ok && ec.ServiceAccount != "" {
		if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert →serviceAccount: %w", err)
		}
	}
	if ec.KmsKey != "" {
		if toID, ok := idx.keyIDByNative[stripCryptoKeyVersion(ec.KmsKey)]; ok {
			if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert →cryptoKey: %w", err)
			}
		}
	}
	if ec.StagingBucket != "" {
		if toID, ok := idx.bucketByName[ec.StagingBucket]; ok {
			if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert →bucket: %w", err)
			}
		}
	}
	return nil
}

// dataprocResourceNameSuffix strips any URL scheme/host prefix, returning
// the resource name starting at "projects/" — SessionTemplate refs may
// arrive as either shape per the SDK doc.
func dataprocResourceNameSuffix(ref string) string {
	if idx := strings.Index(ref, "projects/"); idx >= 0 {
		return ref[idx:]
	}
	return ref
}

func resolveDataprocBatchRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocBatch},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadDataprocRelIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EnvironmentConfig *struct {
				ExecutionConfig *dataprocExecutionConfigAttrs `json:"executionConfig"`
			} `json:"environmentConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EnvironmentConfig == nil {
			continue
		}
		if err := wireDataprocExecutionConfig(st, r.ID, attrs.EnvironmentConfig.ExecutionConfig, idx); err != nil {
			return fmt.Errorf("batch %s: %w", r.NativeID, err)
		}
	}
	return nil
}

func resolveDataprocSessionTemplateRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocSessionTemplate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadDataprocRelIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EnvironmentConfig *struct {
				ExecutionConfig *dataprocExecutionConfigAttrs `json:"executionConfig"`
			} `json:"environmentConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EnvironmentConfig == nil {
			continue
		}
		if err := wireDataprocExecutionConfig(st, r.ID, attrs.EnvironmentConfig.ExecutionConfig, idx); err != nil {
			return fmt.Errorf("session template %s: %w", r.NativeID, err)
		}
	}
	return nil
}

// resolveDataprocSessionRelationships wires Session's environmentConfig
// (same shape as Batch/SessionTemplate) plus Session -> SessionTemplate
// (the template it was launched from, if any).
func resolveDataprocSessionRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocSession},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadDataprocRelIndex(p, st)
	if err != nil {
		return err
	}
	scannedTemplates, err := scannedIDSet(p, st, TypeDataprocSessionTemplate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EnvironmentConfig *struct {
				ExecutionConfig *dataprocExecutionConfigAttrs `json:"executionConfig"`
			} `json:"environmentConfig"`
			SessionTemplate string `json:"sessionTemplate"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EnvironmentConfig != nil {
			if err := wireDataprocExecutionConfig(st, r.ID, attrs.EnvironmentConfig.ExecutionConfig, idx); err != nil {
				return fmt.Errorf("session %s: %w", r.NativeID, err)
			}
		}
		if attrs.SessionTemplate != "" && len(scannedTemplates) > 0 {
			nativeID := dataprocResourceNameSuffix(attrs.SessionTemplate)
			if err := upsertIfScanned(st, scannedTemplates, r.ID, "gcp", p.ID, TypeDataprocSessionTemplate, nativeID, store.RelUses); err != nil {
				return fmt.Errorf("session %s: %w", r.NativeID, err)
			}
		}
	}
	return nil
}

// resolveDataprocWorkflowTemplateRelationships wires the managed-cluster
// config edges (same shape as Cluster's own config) plus the workflow
// template's own top-level encryptionConfig.kmsKey (a distinct CMEK field
// from the managed cluster's own encryption).
func resolveDataprocWorkflowTemplateRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDataprocWorkflowTemplate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadDataprocRelIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Placement *struct {
				ManagedCluster *struct {
					Config *dataprocClusterConfigAttrs `json:"config"`
				} `json:"managedCluster"`
			} `json:"placement"`
			EncryptionConfig *struct {
				KmsKey string `json:"kmsKey"`
			} `json:"encryptionConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Placement != nil && attrs.Placement.ManagedCluster != nil {
			if err := wireDataprocClusterConfig(st, r.ID, attrs.Placement.ManagedCluster.Config, idx); err != nil {
				return fmt.Errorf("workflow template %s: %w", r.NativeID, err)
			}
		}
		if attrs.EncryptionConfig != nil && attrs.EncryptionConfig.KmsKey != "" {
			if toID, ok := idx.keyIDByNative[stripCryptoKeyVersion(attrs.EncryptionConfig.KmsKey)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert workflow template→cryptoKey: %w", err)
				}
			}
		}
	}
	return nil
}

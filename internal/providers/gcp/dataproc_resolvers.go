package gcp

import (
	"encoding/json"
	"fmt"

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

type dataprocClusterAttrs struct {
	Config struct {
		GceClusterConfig *struct {
			NetworkUri     string `json:"networkUri"`
			SubnetworkUri  string `json:"subnetworkUri"`
			ServiceAccount string `json:"serviceAccount"`
		} `json:"gceClusterConfig"`
		EncryptionConfig *struct {
			GcePdKmsKeyName string `json:"gcePdKmsKeyName"`
			KmsKey          string `json:"kmsKey"`
		} `json:"encryptionConfig"`
		ConfigBucket string `json:"configBucket"`
	} `json:"config"`
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

	netByName, err := bareNameIndex(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	subnetByName, err := bareNameIndex(p, st, TypeComputeSubnet)
	if err != nil {
		return err
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	bucketByName, err := bareNameIndex(p, st, TypeStorageBucket)
	if err != nil {
		return err
	}

	for _, r := range rows {
		var attrs dataprocClusterAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if gc := attrs.Config.GceClusterConfig; gc != nil {
			if toID, ok := netByName[lastSegment(gc.NetworkUri)]; ok && gc.NetworkUri != "" {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→network: %w", err)
				}
			}
			if toID, ok := subnetByName[lastSegment(gc.SubnetworkUri)]; ok && gc.SubnetworkUri != "" {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→subnetwork: %w", err)
				}
			}
			if toID, ok := saByEmail[gc.ServiceAccount]; ok && gc.ServiceAccount != "" {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→serviceAccount: %w", err)
				}
			}
		}
		if ec := attrs.Config.EncryptionConfig; ec != nil {
			for _, kmsKeyName := range []string{ec.GcePdKmsKeyName, ec.KmsKey} {
				if kmsKeyName == "" {
					continue
				}
				if toID, ok := keyIDByNative[stripCryptoKeyVersion(kmsKeyName)]; ok {
					if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert cluster→cryptoKey: %w", err)
					}
				}
			}
		}
		if attrs.Config.ConfigBucket != "" {
			if toID, ok := bucketByName[attrs.Config.ConfigBucket]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→bucket: %w", err)
				}
			}
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

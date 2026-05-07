package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveNeptuneGraphSnapshotRefs,
		EdgeDecl{TypeNeptuneGraphGraphSnapshot, TypeNeptuneGraphGraph, store.RelAttachedTo},
		EdgeDecl{TypeNeptuneGraphGraphSnapshot, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveNeptuneGraphPrivateEndpointRefs,
		EdgeDecl{TypeNeptuneGraphPrivateGraphEndpoint, TypeNeptuneGraphGraph, store.RelAttachedTo},
		EdgeDecl{TypeNeptuneGraphPrivateGraphEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
}

func neptuneGraphARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:neptune-graph:%s:%s:graph/%s", region, acct, id)
}

// resolveNeptuneGraphSnapshotRefs wires each graph-snapshot to its source
// graph (SourceGraphID) and KMS key (KmsKeyIdentifier).
func resolveNeptuneGraphSnapshotRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeNeptuneGraphGraphSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	gSet, err := scannedIDSet(acct, st, TypeNeptuneGraphGraph)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceGraphID    *string `json:"SourceGraphId"`
			KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if gid := sv(attrs.SourceGraphID); gid != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeNeptuneGraphGraph, neptuneGraphARN(region, acct.ID, gid))
			if gSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ng-snapshot→graph: %w", err)
				}
			}
		}
		if k := sv(attrs.KmsKeyIdentifier); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ng-snapshot→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveNeptuneGraphPrivateEndpointRefs wires each private-graph-endpoint to
// its parent graph (NativeID `{graphARN}/private-graph-endpoint/{vpcId}`) and
// the VPC it lives in.
func resolveNeptuneGraphPrivateEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeNeptuneGraphPrivateGraphEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	gSet, err := scannedIDSet(acct, st, TypeNeptuneGraphGraph)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	const seg = "/private-graph-endpoint/"
	for _, r := range rows {
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		graphARN := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeNeptuneGraphGraph, graphARN)
		if gSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ng-priv-ep→graph: %w", err)
			}
		}
		var attrs struct {
			VpcID *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if vid := sv(attrs.VpcID); vid != "" {
			vpcARN := ec2ARN(sv(r.Region), acct.ID, "vpc", vid)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ng-priv-ep→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

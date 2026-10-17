package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSnowballClusterRelationships,
		EdgeDecl{TypeSnowballCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSnowballCluster, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeSnowballCluster, TypeSnowballAddress, store.RelUses},
		EdgeDecl{TypeSnowballCluster, TypeSNSTopic, store.RelUses},
	)
}

// snowballClusterRefs is the part of a stored ClusterMetadata the resolver reads.
type snowballClusterRefs struct {
	AddressID           *string `json:"AddressId"`
	ForwardingAddressID *string `json:"ForwardingAddressId"`
	KmsKeyARN           *string `json:"KmsKeyARN"`
	RoleARN             *string `json:"RoleARN"`
	Notification        *struct {
		SnsTopicARN             *string `json:"SnsTopicARN"`
		DevicePickupSnsTopicARN *string `json:"DevicePickupSnsTopicARN"`
	} `json:"Notification"`
}

// resolveSnowballClusterRelationships wires each cluster to its KMS key, IAM
// role, shipping and forwarding addresses and notification SNS topics. Only
// targets scanned in the cluster's account get an edge. A row that fails to
// decode is reported at the end, so that it costs the other rows none of
// their edges.
func resolveSnowballClusterRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSnowballCluster}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	scanned := map[string]bool{}
	for _, typ := range []string{TypeIAMRole, TypeSnowballAddress, TypeSNSTopic} {
		ids, err := scannedIDSet(acct, st, typ)
		if err != nil {
			return err
		}
		for id := range ids {
			scanned[id] = true
		}
	}
	var decodeErrs []error
	for _, r := range rows {
		var refs snowballClusterRefs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &refs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeSnowballCluster, r.NativeID, err))
			continue
		}
		var targets []string
		if keyID, ok := kms.resolveKMSKeyID(sv(refs.KmsKeyARN), sv(r.Region), acct.ID); ok {
			targets = append(targets, keyID)
		}
		nativeTargets := []string{sv(refs.RoleARN)}
		for _, addr := range []string{sv(refs.AddressID), sv(refs.ForwardingAddressID)} {
			if addr != "" {
				nativeTargets = append(nativeTargets, snowballAddressNativeID(acct.ID, addr))
			}
		}
		if n := refs.Notification; n != nil {
			nativeTargets = append(nativeTargets, sv(n.SnsTopicARN), sv(n.DevicePickupSnsTopicARN))
		}
		for _, native := range nativeTargets {
			if native == "" {
				continue
			}
			if id := store.ResourceID("aws", acct.ID, native); scanned[id] {
				targets = append(targets, id)
			}
		}
		for _, tgt := range targets {
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert snowball cluster→%s: %w", tgt, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

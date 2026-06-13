package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveEKSChildrenToCluster,
		EdgeDecl{TypeEKSAccessEntry, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSAddon, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSCapability, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSFargateProfile, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSIdentityProviderConfig, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSNodegroup, TypeEKSCluster, store.RelAttachedTo},
		EdgeDecl{TypeEKSPodIdentityAssociation, TypeEKSCluster, store.RelAttachedTo},
	)
}

// eksClusterNameFromChildARN parses cluster name out of any per-cluster child
// ARN. All EKS child ARNs (scanner-synth + SDK-returned) share the shape
// `arn:aws:eks:{r}:{a}:{kind}/{cluster}/...`.
func eksClusterNameFromChildARN(arn string) string {
	// Skip past the 5 leading ARN colons (`arn:aws:eks:r:a:`) — the resource
	// segment may itself contain colons (e.g. an embedded principal ARN).
	colons := 0
	resStart := -1
	for i := 0; i < len(arn); i++ {
		if arn[i] == ':' {
			colons++
			if colons == 5 {
				resStart = i + 1
				break
			}
		}
	}
	if resStart < 0 || resStart >= len(arn) {
		return ""
	}
	rest := arn[resStart:]
	j := strings.IndexByte(rest, '/')
	if j < 0 {
		return ""
	}
	rest = rest[j+1:]
	if k := strings.IndexByte(rest, '/'); k >= 0 {
		return rest[:k]
	}
	return rest
}

// resolveEKSChildrenToCluster wires every per-cluster child type
// (access-entry, addon, capability, fargate-profile, identity-provider-config,
// nodegroup, pod-identity-association) to its cluster via NativeID parent
// extract. Cluster ARN rebuilt as `arn:aws:eks:{r}:{a}:cluster/{name}`.
func resolveEKSChildrenToCluster(acct *account, st *store.Store) error {
	clusterSet, err := scannedIDSet(acct, st, TypeEKSCluster)
	if err != nil {
		return err
	}
	if len(clusterSet) == 0 {
		return nil
	}
	childTypes := []string{
		TypeEKSAccessEntry,
		TypeEKSAddon,
		TypeEKSCapability,
		TypeEKSFargateProfile,
		TypeEKSIdentityProviderConfig,
		TypeEKSNodegroup,
		TypeEKSPodIdentityAssociation,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			name := eksClusterNameFromChildARN(r.NativeID)
			if name == "" {
				continue
			}
			cARN := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", sv(r.Region), acct.ID, name)
			tgtID := store.ResourceID("aws", acct.ID, TypeEKSCluster, cARN)
			if !clusterSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eks %s→cluster: %w", ctype, err)
			}
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveKMSAliases,
		EdgeDecl{TypeKMSAlias, TypeKMSKey, store.RelAttachedTo},
		EdgeDecl{TypeKMSKey, TypeKMSAlias, store.RelContains},
	)
	registerResolver(
		resolveKMSGrants,
		EdgeDecl{TypeKMSGrant, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeKMSGrant, TypeIAMUser, store.RelUses},
	)
	registerResolver(
		resolveKMSGrantEncryptionContext,
		EdgeDecl{TypeKMSGrant, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeKMSGrant, TypeEC2Volume, store.RelUses},
	)
	registerResolver(
		resolveKMSCustomKeyStoreBackends,
		EdgeDecl{TypeKMSCustomKeyStore, TypeCloudHSMCluster, store.RelUses},
		EdgeDecl{TypeKMSCustomKeyStore, TypeEC2VPCEndpointService, store.RelUses},
	)
	registerResolver(
		resolveKMSKeyCustomKeyStores,
		EdgeDecl{TypeKMSKey, TypeKMSCustomKeyStore, store.RelAttachedTo},
	)
}

// resolveKMSGrants links each KMS grant to its grantee + retiring principal
// (IAM role or user). Service principals like "ec2.amazonaws.com" and
// ephemeral assumed-role/federated session ARNs are skipped — no target
// resource exists. Cross-account principals are FK-safe-skipped.
func resolveKMSGrants(acct *account, st *store.Store) error {
	grants, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSGrant},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	principals, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeIAMRole, TypeIAMUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(principals))
	for _, p := range principals {
		known[p.ID] = true
	}
	for _, gr := range grants {
		var a struct {
			GranteePrincipal  *string `json:"GranteePrincipal"`
			RetiringPrincipal *string `json:"RetiringPrincipal"`
		}
		if err := json.Unmarshal([]byte(gr.AttributesJSON), &a); err != nil {
			continue
		}
		for _, p := range []*string{a.GranteePrincipal, a.RetiringPrincipal} {
			arn := sv(p)
			if arn == "" || !strings.HasPrefix(arn, "arn:aws:iam::") {
				continue
			}
			if !strings.Contains(arn, ":role/") && !strings.Contains(arn, ":user/") {
				continue
			}
			pid := store.ResourceID("aws", acct.ID, arn)
			if !known[pid] {
				continue
			}
			if err := st.UpsertRelationship(gr.ID, pid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert kms-grant→principal: %w", err)
			}
		}
	}
	return nil
}

// kmsEncCtxARNDispatch maps an EncryptionContext key (the well-known
// "aws:<svc>:<Field>Arn" shape AWS services use to scope KMS grants) to the
// disco type its ARN value points at. Lambda's `aws:lambda:FunctionArn` is
// the canonical case (env-var encryption); add new keys as other services
// adopt the pattern (precedent: aws-resolver-audit vs AWS-managed-key grants).
var kmsEncCtxARNDispatch = map[string]string{
	"aws:lambda:FunctionArn": TypeLambdaFunction,
}

// resolveKMSGrantEncryptionContext walks each grant's
// `Constraints.EncryptionContextEquals` map. AWS services scope grants to a
// specific consumer resource by injecting the consumer's ARN under a
// well-known key (e.g. Lambda sets `aws:lambda:FunctionArn`). Each match
// emits a `uses` edge from the grant to the named resource. FK-safe — skip
// when the target isn't in the local store (cross-account, deleted, or
// service not scanned).
func resolveKMSGrantEncryptionContext(acct *account, st *store.Store) error {
	grants, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSGrant},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	// Pre-load id sets for every target type the dispatch map names plus
	// EC2 volume (bare-id encryption-context key, not full ARN).
	idSets := map[string]map[string]bool{}
	for _, dtype := range kmsEncCtxARNDispatch {
		if _, ok := idSets[dtype]; ok {
			continue
		}
		set, err := scannedIDSet(acct, st, dtype)
		if err != nil {
			return fmt.Errorf("load id-set %s: %w", dtype, err)
		}
		idSets[dtype] = set
	}
	volSet, err := scannedIDSet(acct, st, TypeEC2Volume)
	if err != nil {
		return fmt.Errorf("load id-set %s: %w", TypeEC2Volume, err)
	}
	for _, gr := range grants {
		var a struct {
			Constraints struct {
				EncryptionContextEquals map[string]string `json:"EncryptionContextEquals"`
				EncryptionContextSubset map[string]string `json:"EncryptionContextSubset"`
			} `json:"Constraints"`
		}
		if err := json.Unmarshal([]byte(gr.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(gr.Region)
		for _, ctx := range []map[string]string{a.Constraints.EncryptionContextEquals, a.Constraints.EncryptionContextSubset} {
			for k, v := range ctx {
				if v == "" {
					continue
				}
				if k == "aws:ebs:id" {
					volID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "volume", v))
					if !volSet[volID] {
						continue
					}
					if err := st.UpsertRelationship(gr.ID, volID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert kms-grant→%s: %w", TypeEC2Volume, err)
					}
					continue
				}
				dtype, ok := kmsEncCtxARNDispatch[k]
				if !ok {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, v)
				if !idSets[dtype][tgtID] {
					continue
				}
				if err := st.UpsertRelationship(gr.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert kms-grant→%s: %w", dtype, err)
				}
			}
		}
	}
	return nil
}

// resolveKMSAliases links each alias to the key it points at. TargetKeyID on an
// AliasListEntry is either a bare KeyId or a full ARN; the bare form is resolved
// by rebuilding the key ARN from the alias's region.
func resolveKMSAliases(acct *account, st *store.Store) error {
	aliases, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeKMSAlias},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeKMSKey},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	knownKey := make(map[string]bool, len(keys))
	for _, k := range keys {
		knownKey[k.ID] = true
	}
	for _, a := range aliases {
		var attrs struct {
			TargetKeyID *string `json:"TargetKeyID"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.TargetKeyID == nil {
			continue
		}
		target := *attrs.TargetKeyID
		if !strings.HasPrefix(target, "arn:") {
			target = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", sv(a.Region), acct.ID, target)
		}
		keyID := store.ResourceID("aws", acct.ID, target)
		if !knownKey[keyID] {
			continue
		}
		if err := st.UpsertRelationship(a.ID, keyID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert kms-alias→key: %w", err)
		}
		// Inverse: the key owns its aliases (a key can have multiple aliases).
		if err := st.UpsertRelationship(keyID, a.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert kms-key→alias: %w", err)
		}
	}
	return nil
}

// resolveKMSCustomKeyStoreBackends links each custom key store to what holds
// its key material: a CloudHSM key store to its cluster, and an external key
// store on VPC_ENDPOINT_SERVICE connectivity to the endpoint service its proxy
// sits behind. Targets in another account, or not scanned, get no edge.
func resolveKMSCustomKeyStoreBackends(acct *account, st *store.Store) error {
	keyStores, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSCustomKeyStore},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(keyStores) == 0 {
		return nil
	}
	clusters, err := scannedIDSet(acct, st, TypeCloudHSMCluster)
	if err != nil {
		return err
	}
	endpointServices, err := scannedIDSet(acct, st, TypeEC2VPCEndpointService)
	if err != nil {
		return err
	}
	for _, ks := range keyStores {
		var attrs struct {
			CloudHsmClusterID     *string `json:"CloudHsmClusterId"`
			XksProxyConfiguration *struct {
				VpcEndpointServiceName  *string `json:"VpcEndpointServiceName"`
				VpcEndpointServiceOwner *string `json:"VpcEndpointServiceOwner"`
			} `json:"XksProxyConfiguration"`
		}
		if err := json.Unmarshal([]byte(ks.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode %s %s: %w", TypeKMSCustomKeyStore, ks.NativeID, err)
		}
		var targets []string
		if cid := sv(attrs.CloudHsmClusterID); cid != "" {
			clusterARN := cloudhsmClusterARN(sv(ks.Region), acct.ID, cid)
			if id := store.ResourceID("aws", acct.ID, clusterARN); clusters[id] {
				targets = append(targets, id)
			}
		}
		if xks := attrs.XksProxyConfiguration; xks != nil {
			if svcARN, ok := vpcEndpointServiceARNFromName(sv(xks.VpcEndpointServiceName), sv(xks.VpcEndpointServiceOwner)); ok {
				if id := store.ResourceID("aws", acct.ID, svcARN); endpointServices[id] {
					targets = append(targets, id)
				}
			}
		}
		for _, tgt := range targets {
			if err := st.UpsertRelationship(ks.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert kms-custom-key-store→backend: %w", err)
			}
		}
	}
	return nil
}

// vpcEndpointServiceARNFromName rebuilds the NativeID scanVPCEndpointServices
// gives an endpoint service from its service name,
// "com.amazonaws.vpce.<region>.<service-id>" (the model's pattern also allows
// an "eu" first label), and the owning account. ok is false for any other
// shape or an empty owner.
func vpcEndpointServiceARNFromName(name, owner string) (string, bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 5 || parts[1] != "amazonaws" || parts[2] != "vpce" || !strings.HasPrefix(parts[4], "vpce-svc-") || owner == "" {
		return "", false
	}
	return ec2ARN(parts[3], owner, "vpc-endpoint-service", parts[4]), true
}

// resolveKMSKeyCustomKeyStores links each key created in a custom key store to
// that store. KeyMetadata.CustomKeyStoreId is the bare store id; a store lives
// in its keys' region.
func resolveKMSKeyCustomKeyStores(acct *account, st *store.Store) error {
	keyStores, err := scannedIDSet(acct, st, TypeKMSCustomKeyStore)
	if err != nil {
		return err
	}
	if len(keyStores) == 0 {
		return nil
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, k := range keys {
		var attrs struct {
			Metadata struct {
				CustomKeyStoreID *string `json:"CustomKeyStoreId"`
			} `json:"Metadata"`
		}
		if err := json.Unmarshal([]byte(k.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode %s %s: %w", TypeKMSKey, k.NativeID, err)
		}
		storeID := sv(attrs.Metadata.CustomKeyStoreID)
		if storeID == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, kmsCustomKeyStoreNativeID(sv(k.Region), acct.ID, storeID))
		if !keyStores[tgt] {
			continue
		}
		if err := st.UpsertRelationship(k.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert kms-key→custom-key-store: %w", err)
		}
	}
	return nil
}

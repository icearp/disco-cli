package aws

import (
	"encoding/json"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// kmsResolveIndex carries everything a resolver needs to turn a KMS key
// reference (ARN, alias name, alias ARN, or bare key ID) into the resource
// ID of a scanned aws:kms:key. Construct once per resolver via
// loadKMSResolveIndex; reuse for every edge.
type kmsResolveIndex struct {
	// keyIDs holds the resource IDs of every scanned aws:kms:key in the
	// account. Membership is the FK-safety gate.
	keyIDs map[string]struct{}
	// aliasToKeyARN maps both "alias/foo" and the alias ARN form
	// ("arn:{p}:kms:{region}:{acct}:alias/foo") to the target key's ARN.
	aliasToKeyARN map[string]string
	// keyARNByID resolves a bare key UUID ref to its canonical key ARN.
	keyARNByID map[string]string
	// partition is the account's, for the key ARN a bare key id expands to.
	partition string
}

// loadKMSResolveIndex builds the alias + key indexes for one account.
// Resolvers that emit KMS edges should call this once and reuse the result.
func loadKMSResolveIndex(acct *account, st *store.Store) (*kmsResolveIndex, error) {
	idx := &kmsResolveIndex{
		keyIDs:        map[string]struct{}{},
		aliasToKeyARN: map[string]string{},
		keyARNByID:    map[string]string{},
		partition:     acct.partition(),
	}

	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		idx.keyIDs[k.ID] = struct{}{}
		idx.keyARNByID[k.NativeID] = k.NativeID
	}

	aliases, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKMSAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	for _, a := range aliases {
		var attrs struct {
			AliasName   *string `json:"AliasName"`
			AliasArn    *string `json:"AliasArn"`
			TargetKeyID *string `json:"TargetKeyID"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AliasName == nil || attrs.TargetKeyID == nil {
			continue
		}
		region := ""
		if a.Region != nil {
			region = *a.Region
		}
		// TargetKeyID is the bare key UUID; build the canonical key ARN.
		keyARN := kmsKeyARN(acct.partition(), region, acct.ID, *attrs.TargetKeyID)
		idx.aliasToKeyARN[*attrs.AliasName] = keyARN
		if attrs.AliasArn != nil {
			idx.aliasToKeyARN[*attrs.AliasArn] = keyARN
		}
	}
	return idx, nil
}

// resolveKMSKeyID maps a KMS key reference to the resource ID of a scanned
// aws:kms:key. Returns ok=false when the target was not scanned (cross-account
// key, or a stale reference) so callers can skip emit and avoid FK errors.
func (idx *kmsResolveIndex) resolveKMSKeyID(ref, region, acctID string) (string, bool) {
	if ref == "" {
		return "", false
	}
	// Alias name shape: "alias/foo" — look up via index first.
	if strings.HasPrefix(ref, "alias/") {
		if keyARN, ok := idx.aliasToKeyARN[ref]; ok {
			id := store.ResourceID("aws", acctID, keyARN)
			_, present := idx.keyIDs[id]
			return id, present
		}
		return "", false
	}
	// Alias ARN shape: arn:{p}:kms:{r}:{a}:alias/foo
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/") {
		if keyARN, ok := idx.aliasToKeyARN[ref]; ok {
			id := store.ResourceID("aws", acctID, keyARN)
			_, present := idx.keyIDs[id]
			return id, present
		}
		return "", false
	}
	target := kmsKeyRefARN(idx.partition, region, acctID, ref)
	id := store.ResourceID("aws", acctID, target)
	_, present := idx.keyIDs[id]
	return id, present
}

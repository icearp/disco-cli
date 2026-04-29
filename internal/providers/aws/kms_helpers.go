package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
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
	// ("arn:aws:kms:{region}:{acct}:alias/foo") to the target key's ARN.
	aliasToKeyARN map[string]string
	// keyARNByID lets callers find the canonical key ARN given a partially
	//-built ID — used when the input ref is the bare key UUID.
	keyARNByID map[string]string
}

// loadKMSResolveIndex builds the alias + key indexes for one account.
// Resolvers that emit KMS edges should call this once and reuse the result.
func loadKMSResolveIndex(acct *account, st *store.Store) (*kmsResolveIndex, error) {
	idx := &kmsResolveIndex{
		keyIDs:        map[string]struct{}{},
		aliasToKeyARN: map[string]string{},
		keyARNByID:    map[string]string{},
	}

	keys, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKMSKey},
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKMSAlias},
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
		keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, acct.ID, *attrs.TargetKeyID)
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
			id := store.ResourceID("aws", acctID, TypeKMSKey, keyARN)
			_, present := idx.keyIDs[id]
			return id, present
		}
		// Fallback: synthesize the alias ARN and try the key ARN form so
		// kmsKeyTargetARN's old behavior still works for tests.
		return "", false
	}
	// Alias ARN shape: arn:aws:kms:{r}:{a}:alias/foo
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/") {
		if keyARN, ok := idx.aliasToKeyARN[ref]; ok {
			id := store.ResourceID("aws", acctID, TypeKMSKey, keyARN)
			_, present := idx.keyIDs[id]
			return id, present
		}
		return "", false
	}
	// Already a key ARN, or bare key UUID — normalize via kmsKeyTargetARN.
	target := kmsKeyTargetARN(ref, region, acctID)
	id := store.ResourceID("aws", acctID, TypeKMSKey, target)
	_, present := idx.keyIDs[id]
	return id, present
}

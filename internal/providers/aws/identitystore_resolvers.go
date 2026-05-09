package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveIdentityStoreGroupMembershipRefs,
		EdgeDecl{TypeIdentityStoreGroupMembership, TypeIdentityStoreGroup, store.RelAttachedTo},
		EdgeDecl{TypeIdentityStoreGroupMembership, TypeIdentityStoreUser, store.RelAttachedTo},
	)
}

// idStoreMembershipParse extracts (ownerAccountID, identityStoreID) from a
// membership NativeID: `arn:aws:identitystore::{owner}:membership/{store}/{mid}`.
func idStoreMembershipParse(arn string) (owner, store string) {
	const prefix = "arn:aws:identitystore::"
	if !strings.HasPrefix(arn, prefix) {
		return "", ""
	}
	tail := arn[len(prefix):]
	colon := strings.IndexByte(tail, ':')
	if colon < 0 {
		return "", ""
	}
	owner = tail[:colon]
	rest := tail[colon+1:]
	if !strings.HasPrefix(rest, "membership/") {
		return "", ""
	}
	rest = rest[len("membership/"):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", ""
	}
	store = rest[:slash]
	return owner, store
}

// resolveIdentityStoreGroupMembershipRefs wires each membership to its
// parent group (GroupId) and its user member (MemberId.Value). FK-safe.
func resolveIdentityStoreGroupMembershipRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIdentityStoreGroupMembership}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeIdentityStoreGroup)
	if err != nil {
		return err
	}
	userSet, err := scannedIDSet(acct, st, TypeIdentityStoreUser)
	if err != nil {
		return err
	}
	for _, r := range rows {
		owner, idStore := idStoreMembershipParse(r.NativeID)
		if owner == "" || idStore == "" {
			continue
		}
		var attrs struct {
			GroupID  *string `json:"GroupId"`
			MemberID *struct {
				Value *string `json:"Value"`
			} `json:"MemberId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if g := sv(attrs.GroupID); g != "" {
			gARN := identityStoreGroupNativeID(owner, idStore, g)
			tgt := store.ResourceID("aws", acct.ID, TypeIdentityStoreGroup, gARN)
			if groupSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert idstore membership→group: %w", err)
				}
			}
		}
		if attrs.MemberID != nil {
			if u := sv(attrs.MemberID.Value); u != "" {
				uARN := identityStoreUserNativeID(owner, idStore, u)
				tgt := store.ResourceID("aws", acct.ID, TypeIdentityStoreUser, uARN)
				if userSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert idstore membership→user: %w", err)
					}
				}
			}
		}
	}
	return nil
}

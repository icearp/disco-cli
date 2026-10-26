package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveIdentityStoreGroupMembershipRefs,
		EdgeDecl{TypeIdentityStoreGroupMembership, TypeIdentityStoreGroup, store.RelAttachedTo},
		EdgeDecl{TypeIdentityStoreGroupMembership, TypeIdentityStoreUser, store.RelAttachedTo},
	)
}

// idStoreMembershipParse extracts (ownerAccountID, identityStoreID) from a
// membership NativeID, arn:{p}:identitystore::{owner}:membership/{store}/{mid},
// in any partition.
func idStoreMembershipParse(id string) (owner, storeID string) {
	a, err := arn.Parse(id)
	if err != nil || a.Service != "identitystore" || a.Region != "" {
		return "", ""
	}
	rest, ok := strings.CutPrefix(a.Resource, "membership/")
	if !ok {
		return "", ""
	}
	storeID, _, ok = strings.Cut(rest, "/")
	if !ok {
		return "", ""
	}
	return a.AccountID, storeID
}

// resolveIdentityStoreGroupMembershipRefs wires each membership to its
// parent group (GroupId) and its user member (MemberId.Value). FK-safe.
func resolveIdentityStoreGroupMembershipRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIdentityStoreGroupMembership}, Limit: util.AllResources,
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
			gARN := identityStoreGroupNativeIDIn(acct.partition(), owner, idStore, g)
			tgt := store.ResourceID("aws", acct.ID, gARN)
			if groupSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert idstore membership→group: %w", err)
				}
			}
		}
		if attrs.MemberID != nil {
			if u := sv(attrs.MemberID.Value); u != "" {
				uARN := identityStoreUserNativeIDIn(acct.partition(), owner, idStore, u)
				tgt := store.ResourceID("aws", acct.ID, uARN)
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

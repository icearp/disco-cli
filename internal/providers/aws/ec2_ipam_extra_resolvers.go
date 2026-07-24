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
		resolveIpamPolicyRelationships,
		EdgeDecl{TypeEC2IpamPolicy, TypeEC2IPAM, store.RelAttachedTo},
	)
	registerResolver(
		resolveIpamVerificationTokenRelationships,
		EdgeDecl{TypeEC2IpamExternalResourceVerificationToken, TypeEC2IPAM, store.RelAttachedTo},
	)
}

// ipamByShortID maps each scanned IPAM's short id (ipam-xxx, parsed from the
// ":ipam/" segment of its ARN NativeID) to its resource id, so children with
// only a bare IpamId can resolve regardless of the ARN's region.
func ipamByShortID(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IPAM}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, after, ok := strings.Cut(r.NativeID, ":ipam/"); ok && after != "" {
			idx[after] = r.ID
		}
	}
	return idx, nil
}

func resolveIpamPolicyRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IpamPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := ipamByShortID(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			IpamID *string `json:"IpamId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := idx[sv(attrs.IpamID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-policy→ipam: %w", err)
			}
		}
	}
	return nil
}

func resolveIpamVerificationTokenRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IpamExternalResourceVerificationToken}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := ipamByShortID(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			IpamID *string `json:"IpamId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := idx[sv(attrs.IpamID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-verification-token→ipam: %w", err)
			}
		}
	}
	return nil
}

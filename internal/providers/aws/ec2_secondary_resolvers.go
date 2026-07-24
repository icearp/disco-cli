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
		resolveSecondaryInterfaceRelationships,
		EdgeDecl{TypeEC2SecondaryInterface, TypeEC2SecondaryNetwork, store.RelAttachedTo},
		EdgeDecl{TypeEC2SecondaryInterface, TypeEC2SecondarySubnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveSecondarySubnetRelationships,
		EdgeDecl{TypeEC2SecondarySubnet, TypeEC2SecondaryNetwork, store.RelAttachedTo},
	)
}

// secondaryByID indexes rows of one type by the short id parsed from the given
// ARN segment (e.g. ":secondary-network/"), so children carrying only the bare
// id resolve to the ARN-keyed parent.
func secondaryByID(acct *account, st *store.Store, rtype, segment string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, after, ok := strings.Cut(r.NativeID, segment); ok && after != "" {
			idx[after] = r.ID
		}
	}
	return idx, nil
}

func resolveSecondaryInterfaceRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2SecondaryInterface}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netByID, err := secondaryByID(acct, st, TypeEC2SecondaryNetwork, ":secondary-network/")
	if err != nil {
		return err
	}
	subnetByID, err := secondaryByID(acct, st, TypeEC2SecondarySubnet, ":secondary-subnet/")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SecondaryNetworkID *string `json:"SecondaryNetworkId"`
			SecondarySubnetID  *string `json:"SecondarySubnetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := netByID[sv(attrs.SecondaryNetworkID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert secondary-interface→secondary-network: %w", err)
			}
		}
		if tgtID, ok := subnetByID[sv(attrs.SecondarySubnetID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert secondary-interface→secondary-subnet: %w", err)
			}
		}
	}
	return nil
}

func resolveSecondarySubnetRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2SecondarySubnet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netByID, err := secondaryByID(acct, st, TypeEC2SecondaryNetwork, ":secondary-network/")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SecondaryNetworkID *string `json:"SecondaryNetworkId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := netByID[sv(attrs.SecondaryNetworkID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert secondary-subnet→secondary-network: %w", err)
			}
		}
	}
	return nil
}

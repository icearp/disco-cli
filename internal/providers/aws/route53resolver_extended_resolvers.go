package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveR53RResolverEndpointVPC,
		EdgeDecl{TypeRoute53ResolverResolverEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveR53RDNSSECConfigVPC,
		EdgeDecl{TypeRoute53ResolverResolverDNSSECConfig, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveR53RQueryLogAssocRefs,
		EdgeDecl{TypeRoute53ResolverResolverQueryLoggingConfigAssociation, TypeRoute53ResolverResolverQueryLoggingConfig, store.RelAttachedTo},
		EdgeDecl{TypeRoute53ResolverResolverQueryLoggingConfigAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
}

func resolveR53RResolverEndpointVPC(acct *account, st *store.Store) error {
	return r53rResolveVPCField(acct, st, TypeRoute53ResolverResolverEndpoint, "HostVPCId")
}

func resolveR53RDNSSECConfigVPC(acct *account, st *store.Store) error {
	return r53rResolveVPCField(acct, st, TypeRoute53ResolverResolverDNSSECConfig, "ResourceId")
}

func r53rResolveVPCField(acct *account, st *store.Store, sourceType, fieldName string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		v, ok := raw[fieldName].(string)
		if !ok || v == "" {
			continue
		}
		region := sv(r.Region)
		vARN := ec2ARN(region, acct.ID, "vpc", v)
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vARN)
		if !vpcSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert r53r %s→vpc: %w", sourceType, err)
		}
	}
	return nil
}

// resolveR53RQueryLogAssocRefs wires query-log-config-association to the
// underlying query-log-config (by ID lookup) and to the VPC carrying the
// associated resource.
func resolveR53RQueryLogAssocRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53ResolverResolverQueryLoggingConfigAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	cfgIdx, err := r53rQueryLogConfigIDIndex(acct, st)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ResolverQueryLogConfigID *string `json:"ResolverQueryLogConfigId"`
			ResourceID               *string `json:"ResourceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if cid := sv(attrs.ResolverQueryLogConfigID); cid != "" {
			if tgtID, ok := cfgIdx[cid]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert r53r ql-assoc→config: %w", err)
				}
			}
		}
		if vid := sv(attrs.ResourceID); vid != "" {
			region := sv(r.Region)
			vARN := ec2ARN(region, acct.ID, "vpc", vid)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert r53r ql-assoc→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

func r53rQueryLogConfigIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRoute53ResolverResolverQueryLoggingConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

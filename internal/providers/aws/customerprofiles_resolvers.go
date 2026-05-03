package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCustomerProfilesChildrenToDomain,
		EdgeDecl{TypeCPCalculatedAttributeDefinition, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPEventStream, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPEventTrigger, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPIntegration, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPObjectType, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPRecommender, TypeCPDomain, store.RelAttachedTo},
		EdgeDecl{TypeCPSegmentDefinition, TypeCPDomain, store.RelAttachedTo},
	)
}

// cpDomainARNFromChild extracts `arn:aws:profile:r:a:domains/{name}` from any
// child NativeID of shape `…:domains/{name}/<kind>/<id>`.
func cpDomainARNFromChild(arn string) string {
	const prefix = "domains/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

func resolveCustomerProfilesChildrenToDomain(acct *account, st *store.Store) error {
	domSet, err := scannedIDSet(acct, st, TypeCPDomain)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeCPCalculatedAttributeDefinition,
		TypeCPEventStream,
		TypeCPEventTrigger,
		TypeCPIntegration,
		TypeCPObjectType,
		TypeCPRecommender,
		TypeCPSegmentDefinition,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := cpDomainARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeCPDomain, parent)
			if !domSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert customer-profiles %s→domain: %w", ctype, err)
			}
		}
	}
	return nil
}

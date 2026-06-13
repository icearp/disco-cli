package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCasesChildrenToDomain,
		EdgeDecl{TypeCasesCaseRule, TypeCasesDomain, store.RelAttachedTo},
		EdgeDecl{TypeCasesField, TypeCasesDomain, store.RelAttachedTo},
		EdgeDecl{TypeCasesLayout, TypeCasesDomain, store.RelAttachedTo},
		EdgeDecl{TypeCasesTemplate, TypeCasesDomain, store.RelAttachedTo},
	)
}

// casesDomainARNFromChild extracts `arn:aws:cases:r:a:domain/{domainId}` from
// any per-domain child NativeID of shape
// `arn:aws:cases:r:a:domain/{domainId}/<kind>/{childId}`.
func casesDomainARNFromChild(arn string) string {
	const seg = ":domain/"
	i := strings.Index(arn, seg)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(seg):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + seg + tail[:end]
}

func resolveCasesChildrenToDomain(acct *account, st *store.Store) error {
	dSet, err := scannedIDSet(acct, st, TypeCasesDomain)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeCasesCaseRule, TypeCasesField, TypeCasesLayout, TypeCasesTemplate} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := casesDomainARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeCasesDomain, parent)
			if !dSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert cases %s→domain: %w", t, err)
			}
		}
	}
	return nil
}

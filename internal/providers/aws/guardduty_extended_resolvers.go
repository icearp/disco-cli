package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveGuardDutyChildrenToDetector,
		EdgeDecl{TypeGuardDutyFilter, TypeGuardDutyDetector, store.RelAttachedTo},
		EdgeDecl{TypeGuardDutyPublishingDestination, TypeGuardDutyDetector, store.RelAttachedTo},
		EdgeDecl{TypeGuardDutyThreatEntitySet, TypeGuardDutyDetector, store.RelAttachedTo},
		EdgeDecl{TypeGuardDutyThreatIntelSet, TypeGuardDutyDetector, store.RelAttachedTo},
		EdgeDecl{TypeGuardDutyTrustedEntitySet, TypeGuardDutyDetector, store.RelAttachedTo},
	)
}

// guarddutyDetectorARNFromChild extracts `arn:aws:guardduty:r:a:detector/{id}`
// from any per-detector child NativeID of shape `…:detector/{id}/<kind>/<id>`.
func guarddutyDetectorARNFromChild(arn string) string {
	const prefix = ":detector/"
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

// resolveGuardDutyChildrenToDetector wires every per-detector child kind to
// its parent detector via NativeID `:detector/{id}/...` parent extraction.
// Hierarchy closure (parent→child) is already written by the scanner; this
// emits the reverse `attached-to` edge so children are not source-orphans.
func resolveGuardDutyChildrenToDetector(acct *account, st *store.Store) error {
	detSet, err := scannedIDSet(acct, st, TypeGuardDutyDetector)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeGuardDutyFilter,
		TypeGuardDutyPublishingDestination,
		TypeGuardDutyThreatEntitySet,
		TypeGuardDutyThreatIntelSet,
		TypeGuardDutyTrustedEntitySet,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := guarddutyDetectorARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeGuardDutyDetector, parent)
			if !detSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert guardduty %s→detector: %w", ctype, err)
			}
		}
	}
	return nil
}

package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveEntityResolutionPolicyStatementToParent,
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionMatchingWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionIdMappingWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionIdNamespace, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionSchemaMapping, store.RelAttachedTo},
	)
}

// resolveEntityResolutionPolicyStatementToParent wires each policy-statement
// to its parent via NativeID `{parentARN}/policy` strip; parent may be a
// matching-workflow, id-mapping-workflow, id-namespace, or schema-mapping —
// dispatch by ARN substring.
func resolveEntityResolutionPolicyStatementToParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEntityResolutionPolicyStatement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	mwSet, err := scannedIDSet(acct, st, TypeEntityResolutionMatchingWorkflow)
	if err != nil {
		return err
	}
	imwSet, err := scannedIDSet(acct, st, TypeEntityResolutionIdMappingWorkflow)
	if err != nil {
		return err
	}
	insSet, err := scannedIDSet(acct, st, TypeEntityResolutionIdNamespace)
	if err != nil {
		return err
	}
	smSet, err := scannedIDSet(acct, st, TypeEntityResolutionSchemaMapping)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		var tgtType string
		var present bool
		switch {
		case strings.Contains(parent, "/matchingworkflow/"), strings.Contains(parent, ":matchingworkflow/"):
			tgtType = TypeEntityResolutionMatchingWorkflow
			present = mwSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/idmappingworkflow/"), strings.Contains(parent, ":idmappingworkflow/"):
			tgtType = TypeEntityResolutionIdMappingWorkflow
			present = imwSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/idnamespace/"), strings.Contains(parent, ":idnamespace/"):
			tgtType = TypeEntityResolutionIdNamespace
			present = insSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/schemamapping/"), strings.Contains(parent, ":schemamapping/"):
			tgtType = TypeEntityResolutionSchemaMapping
			present = smSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		default:
			continue
		}
		if !present {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, parent)
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert er policy-statement→%s: %w", tgtType, err)
		}
	}
	return nil
}

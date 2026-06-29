package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveFraudDetectorRuleRelationships,
		EdgeDecl{TypeFraudDetectorRule, TypeFraudDetectorDetector, store.RelAttachedTo},
	)
}

// resolveFraudDetectorRuleRelationships wires each rule to its detector — the
// detector ARN is rebuilt from the rule's DetectorId.
func resolveFraudDetectorRuleRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFraudDetectorRule}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	detSet, err := scannedIDSet(acct, st, TypeFraudDetectorDetector)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DetectorID *string `json:"DetectorId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		did := sv(attrs.DetectorID)
		if did == "" {
			continue
		}
		detARN := fmt.Sprintf("arn:aws:frauddetector:%s:%s:detector/%s", sv(r.Region), acct.ID, did)
		tgt := store.ResourceID("aws", acct.ID, TypeFraudDetectorDetector, detARN)
		if detSet[tgt] {
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert frauddetector rule→detector: %w", err)
			}
		}
	}
	return nil
}

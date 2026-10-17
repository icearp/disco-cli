package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveComputeOptimizerPreferenceTargets,
		EdgeDecl{TypeComputeOptimizerRecommendationPreference, TypeEC2Instance, store.RelUses},
		EdgeDecl{TypeComputeOptimizerRecommendationPreference, TypeAutoScalingGroup, store.RelUses},
	)
}

// resolveComputeOptimizerPreferenceTargets wires a resource-level preference
// (Scope.Name ResourceArn) to the EC2 instance or Auto Scaling group whose
// ARN is its scope value, when that resource was scanned in the preference's
// account. An organization or account scope's value (ALL_ACCOUNTS, an
// account id) matches no scanned resource, so it gets no edge. A row that
// fails to decode is reported at the end, so that it costs the other rows
// none of their edges.
func resolveComputeOptimizerPreferenceTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeComputeOptimizerRecommendationPreference}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	instances, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	groups, err := scannedIDSet(acct, st, TypeAutoScalingGroup)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			Scope struct {
				Value string `json:"Value"`
			} `json:"Scope"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeComputeOptimizerRecommendationPreference, r.NativeID, err))
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, attrs.Scope.Value)
		if !instances[tgtID] && !groups[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert compute-optimizer preference→resource: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

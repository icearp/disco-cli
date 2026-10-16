package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopRUMHierarchy,
		EdgeDecl{TypeRUMAppMonitor, TypeRUMMetricsDestination, store.RelContains},
		EdgeDecl{TypeRUMMetricsDestination, TypeRUMMetricDefinition, store.RelContains},
	)
	registerResolver(
		resolveRUMMetricsDestinationRoles,
		EdgeDecl{TypeRUMMetricsDestination, TypeIAMRole, store.RelUses},
	)
}

// noopRUMHierarchy only declares the app monitor -> destination -> metric
// definition contains edges, which the scanner records itself via childFanOut.
func noopRUMHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveRUMMetricsDestinationRoles wires each metrics destination to the IAM
// role RUM writes to it with (IamRoleArn, set for Evidently destinations).
func resolveRUMMetricsDestinationRoles(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRUMMetricsDestination}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			IamRoleArn *string `json:"IamRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode rum metrics destination %s: %w", r.NativeID, err)
		}
		if err := upsertIfScanned(acct, st, roleSet, r.ID, sv(attrs.IamRoleArn), store.RelUses, "rum metrics destination→role"); err != nil {
			return err
		}
	}
	return nil
}

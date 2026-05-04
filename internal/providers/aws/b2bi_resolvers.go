package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveB2BIProfileLogGroup,
		EdgeDecl{TypeB2BIProfile, TypeLogsLogGroup, store.RelUses},
	)
}

// resolveB2BIProfileLogGroup wires each B2BI profile to the CloudWatch log
// group it streams logs to (LogGroupName). The SDK exposes only the bare
// log-group name; rebuild the ARN per region+acct via logGroupNativeIDFromName.
func resolveB2BIProfileLogGroup(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeB2BIProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LogGroupName *string `json:"LogGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		name := sv(attrs.LogGroupName)
		if name == "" {
			continue
		}
		lgARN := logGroupNativeIDFromName(acct.ID, sv(r.Region), name)
		tgt := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lgARN)
		if !lgSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert b2bi profile→log-group: %w", err)
		}
	}
	return nil
}

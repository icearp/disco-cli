package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveBACPaymentConnectorParent,
		EdgeDecl{TypeBedrockAgentCorePaymentConnector, TypeBedrockAgentCorePaymentManager, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACPaymentManagerRole,
		EdgeDecl{TypeBedrockAgentCorePaymentManager, TypeIAMRole, store.RelAssumes},
	)
}

// resolveBACPaymentConnectorParent wires each payment-connector to its parent
// payment-manager. Scanner synthesizes NativeID
// `arn:aws:bedrock-agentcore:r:a:payment-connector/{managerID}/{connectorID}`;
// rebuild the manager ARN as `...:payment-manager/{managerID}`.
func resolveBACPaymentConnectorParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePaymentConnector},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	mgrIdx, err := bacPaymentManagerIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = ":payment-connector/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		mid := tail[:end]
		tgtID, ok := mgrIdx[mid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac payment-connector→manager: %w", err)
		}
	}
	return nil
}

// bacPaymentManagerIDIndex maps PaymentManagerId → resource ID.
func bacPaymentManagerIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePaymentManager},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			PaymentManagerID *string `json:"PaymentManagerId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.PaymentManagerID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveBACPaymentManagerRole wires each payment-manager to the IAM role it
// assumes via the RoleArn SDK field, FK-safe against the scanned role set.
func resolveBACPaymentManagerRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePaymentManager},
		Limit: util.AllResources,
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
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.RoleArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac payment-manager→role: %w", err)
		}
	}
	return nil
}

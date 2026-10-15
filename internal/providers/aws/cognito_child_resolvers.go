package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopCognitoChildHierarchy,
		EdgeDecl{TypeCognitoUserPool, TypeCognitoUserPoolReplica, store.RelContains},
		EdgeDecl{TypeCognitoAppClient, TypeCognitoUserPoolClientSecret, store.RelContains},
	)
	registerResolver(
		resolveCognitoReplicaPool,
		EdgeDecl{TypeCognitoUserPoolReplica, TypeCognitoUserPool, store.RelAttachedTo},
	)
}

// noopCognitoChildHierarchy only declares the contains edges the scanner
// records through childFanOut (scanCognitoUserPoolReplicas,
// scanCognitoUserPoolClientSecrets), so coverage tooling sees them as wired.
func noopCognitoChildHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveCognitoReplicaPool links each secondary replica to the user pool its
// UserPoolArn names in the replica's region, when that region was scanned.
func resolveCognitoReplicaPool(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoUserPoolReplica},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	poolSet, err := scannedIDSet(acct, st, TypeCognitoUserPool)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			UserPoolArn *string `json:"UserPoolArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode cognito user-pool-replica %s: %w", r.NativeID, err)
		}
		arn := sv(attrs.UserPoolArn)
		if arn == "" {
			continue
		}
		poolID := store.ResourceID("aws", acct.ID, arn)
		if !poolSet[poolID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, poolID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cognito user-pool-replica→user-pool: %w", err)
		}
	}
	return nil
}

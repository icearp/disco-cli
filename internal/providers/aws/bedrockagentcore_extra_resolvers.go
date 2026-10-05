package aws

import (
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBACGatewayRuleParent,
		EdgeDecl{TypeBedrockAgentCoreGatewayRule, TypeBedrockAgentCoreGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACGatewayRateLimitParent,
		EdgeDecl{TypeBedrockAgentCoreGatewayRateLimit, TypeBedrockAgentCoreGateway, store.RelAttachedTo},
	)
}

func resolveBACGatewayRuleParent(acct *account, st *store.Store) error {
	return resolveBACParentFromNativeID(acct, st, TypeBedrockAgentCoreGatewayRule, TypeBedrockAgentCoreGateway, "/rule/")
}

func resolveBACGatewayRateLimitParent(acct *account, st *store.Store) error {
	return resolveBACParentFromNativeID(acct, st, TypeBedrockAgentCoreGatewayRateLimit, TypeBedrockAgentCoreGateway, "/rate-limit/")
}

// resolveBACParentFromNativeID links each childType row to the parentType row
// whose NativeID precedes seg in the child's `{parentARN}{seg}{id}` NativeID.
// The edge is emitted only when that parent was scanned.
func resolveBACParentFromNativeID(acct *account, st *store.Store, childType, parentType, seg string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parents, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parentARN, _, ok := strings.Cut(r.NativeID, seg)
		if !ok {
			continue
		}
		parentID := store.ResourceID("aws", acct.ID, parentARN)
		if !parents[parentID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, parentID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}

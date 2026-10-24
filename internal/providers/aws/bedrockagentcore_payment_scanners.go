package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/store"
)

// AgentCore payments scanners. List ops return summaries only — secret-bearing
// fields (provider API keys, OAuth secrets) live on per-resource Get bodies,
// intentionally NOT fetched.

// scanBACPaymentManagers stores the region's payment managers with their
// tags and returns the stored ones that carry a PaymentManagerId, the parents
// of the connector phase. A first page refused with a region-gap code
// (isBACRegionGap) means the op is not deployed in this region.
func scanBACPaymentManagers(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (managers []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagentcore:ListPaymentManagers", "payment managers", "PaymentManagerId",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.PaymentManagerSummary, *string, error) {
			out, err := client.ListPaymentManagers(ctx, &bac.ListPaymentManagersInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PaymentManagers, out.NextToken, nil
		}),
		func(m bactypes.PaymentManagerSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCorePaymentManager, "payment managers",
				m.PaymentManagerArn, cmp.Or(sv(m.Name), sv(m.PaymentManagerId)), sv(m.PaymentManagerId), m)
			return bacWithStatus(r, why, key, string(m.Status), m.CreatedAt)
		},
		func(m bactypes.PaymentManagerSummary) string { return sv(m.PaymentManagerId) },
		tags, p)
}

// scanBACPaymentConnectors lists the connectors of each manager the manager
// phase stored. PaymentConnectorSummary carries no ARN, so NativeID is
// synthesized as
// `arn:aws:bedrock-agentcore:r:a:payment-connector/{managerID}/{connectorID}` —
// the resolver recovers the parent manager from it. The op's model lists no
// not-found code, so a manager deleted after it was listed fails the phase
// with whatever code the service answers with (decision #14, a known race).
func scanBACPaymentConnectors(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, managers []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListPaymentConnectors", nil, childParentIDs(managers),
		bacParentPages(nil, func(ctx context.Context, manager string, token *string) ([]bactypes.PaymentConnectorSummary, *string, error) {
			out, err := client.ListPaymentConnectors(ctx, &bac.ListPaymentConnectorsInput{PaymentManagerId: &manager, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PaymentConnectors, out.NextToken, nil
		}),
		func(manager string, c bactypes.PaymentConnectorSummary) (*store.Resource, string, string) {
			cid := sv(c.PaymentConnectorId)
			if cid == "" {
				return nil, "payment connectors have an empty PaymentConnectorId", cmp.Or(sv(c.Name), "(a connector with no name) of manager "+manager)
			}
			r := bacRowAt(acct, region, scanID, TypeBedrockAgentCorePaymentConnector, bacARN(region, acct.ID, "payment-connector", manager+"/"+cid), cmp.Or(sv(c.Name), cid), c)
			status := string(c.Status)
			r.Status = &status
			return r, "", ""
		})
}

// scanBACPaymentCredentialProviders stores the region's payment credential
// providers with their tags. A first page refused with a region-gap code
// (isBACRegionGap) means the op is not deployed in this region.
func scanBACPaymentCredentialProviders(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagentcore:ListPaymentCredentialProviders"
	var unauthorized bacUnauthorized
	stop := storeTokenListTags(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.PaymentCredentialProviderItem, *string, error) {
			out, err := client.ListPaymentCredentialProviders(ctx, &bac.ListPaymentCredentialProvidersInput{NextToken: token, MaxResults: sdkaws.Int32(bacSmallPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CredentialProviders, out.NextToken, nil
		})),
		func(c bactypes.PaymentCredentialProviderItem) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCorePaymentCredentialProvider, "payment credential providers",
				c.CredentialProviderArn, cmp.Or(sv(c.Name), sv(c.CredentialProviderArn)), sv(c.Name), c)
			if r != nil {
				r.CreatedAt = tp(c.CreatedTime)
			}
			return r, why, key
		}, tags, p)
	unauthorized.warn(st, op, acct, region, stop)
	return stop
}

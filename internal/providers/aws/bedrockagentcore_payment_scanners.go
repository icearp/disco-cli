package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
)

// AgentCore payments scanners. List ops return summaries only — secret-bearing
// fields (provider API keys, OAuth secrets) live on per-resource Get bodies,
// intentionally NOT fetched.

// scanBACPaymentManagers lists payment managers and returns their IDs for the
// connector fan-out.
func scanBACPaymentManagers(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListPaymentManagersPaginator(client, &bac.ListPaymentManagersInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			t, i, e := bacListSkip(st, batch, "bedrockagentcore:ListPaymentManagers", "payment-managers", acct.ID, region, perr)
			return ids, t, i, e
		}
		for _, m := range out.PaymentManagers {
			arn := sv(m.PaymentManagerArn)
			if arn == "" {
				continue
			}
			if id := sv(m.PaymentManagerId); id != "" {
				ids = append(ids, id)
			}
			label := sv(m.Name)
			if label == "" {
				label = sv(m.PaymentManagerId)
			}
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCorePaymentManager, NativeID: arn,
				Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(m),
				CreatedAt: tp(m.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	t, i, e := upsertBatch(st, batch, "bedrockagentcore payment-managers")
	return ids, t, i, e
}

// scanBACPaymentConnectors fans out per payment-manager ID. PaymentConnectorSummary
// carries no ARN, so NativeID is synthesized as
// `arn:aws:bedrock-agentcore:r:a:payment-connector/{managerID}/{connectorID}` —
// the resolver recovers the parent manager from it.
func scanBACPaymentConnectors(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, managerIDs []string) (int, int, error) {
	if len(managerIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, mid := range managerIDs {
		id := mid
		pager := bac.NewListPaymentConnectorsPaginator(client, &bac.ListPaymentConnectorsInput{PaymentManagerId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListPaymentConnectors %s: %w", mid, perr)
			}
			for _, c := range out.PaymentConnectors {
				cid := sv(c.PaymentConnectorId)
				if cid == "" {
					continue
				}
				label := sv(c.Name)
				if label == "" {
					label = cid
				}
				status := string(c.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCorePaymentConnector, NativeID: bacARN(region, acct.ID, "payment-connector", mid+"/"+cid),
					Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore payment-connectors")
}

func scanBACPaymentCredentialProviders(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListPaymentCredentialProvidersPaginator(client, &bac.ListPaymentCredentialProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListPaymentCredentialProviders", "payment-credential-providers", acct.ID, region, perr)
		}
		for _, p := range out.CredentialProviders {
			arn := sv(p.CredentialProviderArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCorePaymentCredentialProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p),
				CreatedAt: tp(p.CreatedTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore payment-credential-providers")
}

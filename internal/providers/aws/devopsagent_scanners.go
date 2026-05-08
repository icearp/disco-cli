package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
)

func init() {
	registerService(serviceEntry{
		name: "aws:dev-ops-agent",
		fn:   scanDevOpsAgent,
		emits: []coverage.TypeDecl{
			{Service: "dev-ops-agent", DiscoType: TypeDevOpsAgentAgentSpace, Leaf: true},
			{Service: "dev-ops-agent", DiscoType: TypeDevOpsAgentAssociation, Leaf: true},
			{Service: "dev-ops-agent", DiscoType: TypeDevOpsAgentService, Leaf: true},
		},
	})
}

type devOpsAgentAPI interface {
	ListAgentSpaces(context.Context, *devopsagent.ListAgentSpacesInput, ...func(*devopsagent.Options)) (*devopsagent.ListAgentSpacesOutput, error)
	ListAssociations(context.Context, *devopsagent.ListAssociationsInput, ...func(*devopsagent.Options)) (*devopsagent.ListAssociationsOutput, error)
	ListServices(context.Context, *devopsagent.ListServicesInput, ...func(*devopsagent.Options)) (*devopsagent.ListServicesOutput, error)
}

// scanDevOpsAgent discovers DevOpsAgent agent spaces, services, and per-space
// associations. PrivateConnection skip-logged: SDK exposes only Create/Delete/
// Describe with no list endpoint. List APIs return raw IDs — synthesize ARNs.
func scanDevOpsAgent(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := devopsagent.NewFromConfig(acct.cfg, func(o *devopsagent.Options) { o.Region = region })

	spaceIDs, t, i, ferr := scanDOAAgentSpaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanDOAServices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, sid := range spaceIDs {
		t, i, ferr = scanDOAAssociations(ctx, client, acct, region, st, scanID, sid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanDOAAgentSpaces(ctx context.Context, client devOpsAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := devopsagent.NewListAgentSpacesPaginator(client, &devopsagent.ListAgentSpacesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "devopsagent:ListAgentSpaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("devopsagent:ListAgentSpaces: %w", err)
		}
		for _, a := range out.AgentSpaces {
			id := sv(a.AgentSpaceId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:devopsagent:%s:%s:agent-space/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDevOpsAgentAgentSpace, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "devopsagent agent-spaces")
	return ids, t, i, err
}

func scanDOAServices(ctx context.Context, client devOpsAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := devopsagent.NewListServicesPaginator(client, &devopsagent.ListServicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "devopsagent:ListServices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("devopsagent:ListServices: %w", err)
		}
		for _, s := range out.Services {
			id := sv(s.ServiceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:devopsagent:%s:%s:service/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDevOpsAgentService, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "devopsagent services")
}

func scanDOAAssociations(ctx context.Context, client devOpsAgentAPI, acct *account, region string, st *store.Store, scanID string, agentSpaceID string) (int, int, error) {
	asid := agentSpaceID
	pager := devopsagent.NewListAssociationsPaginator(client, &devopsagent.ListAssociationsInput{AgentSpaceId: &asid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "devopsagent:ListAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("devopsagent:ListAssociations: %w", err)
		}
		for _, a := range out.Associations {
			aid := sv(a.AssociationId)
			if aid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:devopsagent:%s:%s:agent-space/%s/association/%s", region, acct.ID, asid, aid)
			label := aid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDevOpsAgentAssociation, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "devopsagent associations")
}

package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAidevopsAgentSpace, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsService, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsAssociations, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsPrivateConnection, Service: "aidevops", Upstream: "AWS::aidevops::private-connection"})
	registerService(serviceEntry{
		name: "aws:aidevops",
		fn:   scanAidevops,
	})
}

// aidevopsAPI is the narrow surface the aidevops scanners use;
// *devopsagent.Client (the DevOps Agent / aidevops service) satisfies it.
type aidevopsAPI interface {
	ListAgentSpaces(context.Context, *devopsagent.ListAgentSpacesInput, ...func(*devopsagent.Options)) (*devopsagent.ListAgentSpacesOutput, error)
	ListServices(context.Context, *devopsagent.ListServicesInput, ...func(*devopsagent.Options)) (*devopsagent.ListServicesOutput, error)
	ListAssociations(context.Context, *devopsagent.ListAssociationsInput, ...func(*devopsagent.Options)) (*devopsagent.ListAssociationsOutput, error)
	ListPrivateConnections(context.Context, *devopsagent.ListPrivateConnectionsInput, ...func(*devopsagent.Options)) (*devopsagent.ListPrivateConnectionsOutput, error)
}

// aidevops resources carry no AWS-issued ARN; NativeIDs are synthesized in the
// canonical arn:aws:aidevops:{region}:{account}:{kind}/{id} shape.
func aidevopsARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:aidevops:%s:%s:%s/%s", region, acctID, kind, id)
}

func scanAidevops(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := devopsagent.NewFromConfig(acct.cfg, func(o *devopsagent.Options) { o.Region = region })
	return scanAidevopsAll(ctx, client, acct, region, st, scanID)
}

func scanAidevopsAll(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	spaceIDs, t, i, err := scanAidevopsAgentSpaces(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, err
	}
	t, i, err = scanAidevopsServices(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, err
	}
	t, i, err = scanAidevopsAssociations(ctx, client, acct, region, st, scanID, spaceIDs)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, err
	}
	t, i, err = scanAidevopsPrivateConnections(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, err
}

func scanAidevopsAgentSpaces(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (spaceIDs []string, total, inserted int, err error) {
	p := devopsagent.NewListAgentSpacesPaginator(client, &devopsagent.ListAgentSpacesInput{})
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return spaceIDs, total, inserted, skipIfAccessDenied(st, "aidevops:ListAgentSpaces", acct.ID, region, perr)
			}
			return spaceIDs, total, inserted, fmt.Errorf("aidevops:ListAgentSpaces: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(page.AgentSpaces))
		for _, a := range page.AgentSpaces {
			id := sv(a.AgentSpaceId)
			if id == "" {
				continue
			}
			spaceIDs = append(spaceIDs, id)
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAidevopsAgentSpace,
				NativeID:       aidevopsARN(region, acct.ID, "agent-space", id),
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.CreatedAt),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "aidevops agent-spaces")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return spaceIDs, total, inserted, uerr
		}
	}
	return spaceIDs, total, inserted, nil
}

func scanAidevopsServices(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := devopsagent.NewListServicesPaginator(client, &devopsagent.ListServicesInput{})
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "aidevops:ListServices", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("aidevops:ListServices: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(page.Services))
		for _, s := range page.Services {
			id := sv(s.ServiceId)
			if id == "" {
				continue
			}
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAidevopsService,
				NativeID:       aidevopsARN(region, acct.ID, "service", id),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "aidevops services")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return total, inserted, uerr
		}
	}
	return total, inserted, nil
}

// scanAidevopsAssociations fans out ListAssociations per agent space — an
// association binds an agent space to a registered service.
func scanAidevopsAssociations(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaceIDs []string) (total, inserted int, err error) {
	for _, spaceID := range spaceIDs {
		p := devopsagent.NewListAssociationsPaginator(client, &devopsagent.ListAssociationsInput{AgentSpaceId: &spaceID})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "aidevops:ListAssociations", acct.ID, region, perr)
					break
				}
				return total, inserted, fmt.Errorf("aidevops:ListAssociations %s: %w", spaceID, perr)
			}
			batch := make([]*store.Resource, 0, len(page.Associations))
			for _, a := range page.Associations {
				id := sv(a.AssociationId)
				if id == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAidevopsAssociations,
					NativeID:       aidevopsARN(region, acct.ID, "association", id),
					Name:           &id,
					Region:         &region,
					CreatedAt:      tp(a.CreatedAt),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			t, i, uerr := upsertBatch(st, batch, "aidevops associations")
			total, inserted = total+t, inserted+i
			if uerr != nil {
				return total, inserted, uerr
			}
		}
	}
	return total, inserted, nil
}

// scanAidevopsPrivateConnections: ListPrivateConnections is a single-call list
// (no paginator / NextToken in the SDK).
func scanAidevopsPrivateConnections(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, perr := client.ListPrivateConnections(ctx, &devopsagent.ListPrivateConnectionsInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			return 0, 0, skipIfAccessDenied(st, "aidevops:ListPrivateConnections", acct.ID, region, perr)
		}
		return 0, 0, fmt.Errorf("aidevops:ListPrivateConnections: %w", perr)
	}
	batch := make([]*store.Resource, 0, len(out.PrivateConnections))
	for _, c := range out.PrivateConnections {
		name := sv(c.Name)
		if name == "" {
			continue
		}
		n := name
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAidevopsPrivateConnection,
			NativeID:       aidevopsARN(region, acct.ID, "private-connection", name),
			Name:           &n,
			Region:         &region,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:   scanID,
		})
	}
	return upsertBatch(st, batch, "aidevops private-connections")
}

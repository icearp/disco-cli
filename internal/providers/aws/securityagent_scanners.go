package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/securityagent"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityAgentAgentSpace, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentApplication, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentPentest, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentTargetDomain, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentIntegration, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentPrivateConnection, Service: "security-agent", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityAgentSecurityRequirementPack, Service: "security-agent", Leaf: true})
	registerService(serviceEntry{
		name: "aws:security-agent",
		fn:   scanSecurityAgent,
	})
}

type securityAgentAPI interface {
	ListAgentSpaces(context.Context, *securityagent.ListAgentSpacesInput, ...func(*securityagent.Options)) (*securityagent.ListAgentSpacesOutput, error)
	ListApplications(context.Context, *securityagent.ListApplicationsInput, ...func(*securityagent.Options)) (*securityagent.ListApplicationsOutput, error)
	ListPentests(context.Context, *securityagent.ListPentestsInput, ...func(*securityagent.Options)) (*securityagent.ListPentestsOutput, error)
	ListTargetDomains(context.Context, *securityagent.ListTargetDomainsInput, ...func(*securityagent.Options)) (*securityagent.ListTargetDomainsOutput, error)
	ListIntegrations(context.Context, *securityagent.ListIntegrationsInput, ...func(*securityagent.Options)) (*securityagent.ListIntegrationsOutput, error)
	ListPrivateConnections(context.Context, *securityagent.ListPrivateConnectionsInput, ...func(*securityagent.Options)) (*securityagent.ListPrivateConnectionsOutput, error)
	ListSecurityRequirementPacks(context.Context, *securityagent.ListSecurityRequirementPacksInput, ...func(*securityagent.Options)) (*securityagent.ListSecurityRequirementPacksOutput, error)
}

// scanSecurityAgent discovers Security Agent agent spaces, applications,
// per-space pentests, and target domains. List APIs return only IDs; ARNs
// synthesized per (account, region, kind, id).
func scanSecurityAgent(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := securityagent.NewFromConfig(acct.cfg, func(o *securityagent.Options) { o.Region = region })

	spaceIDs, t, i, ferr := scanSAAgentSpaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSAApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, sid := range spaceIDs {
		t, i, ferr = scanSAPentests(ctx, client, acct, region, st, scanID, sid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanSATargetDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSAIntegrations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSAPrivateConnections(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSASecurityRequirementPacks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSAIntegrations(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityagent.NewListIntegrationsPaginator(client, &securityagent.ListIntegrationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListIntegrations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListIntegrations: %w", err)
		}
		for _, in := range out.IntegrationSummaries {
			id := sv(in.IntegrationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:integration/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentIntegration, NativeID: arn,
				Name: in.DisplayName, Region: &region,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent integrations")
}

func scanSAPrivateConnections(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityagent.NewListPrivateConnectionsPaginator(client, &securityagent.ListPrivateConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListPrivateConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListPrivateConnections: %w", err)
		}
		for _, c := range out.PrivateConnections {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:private-connection/%s", region, acct.ID, name)
			nm := name
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentPrivateConnection, NativeID: arn,
				Name: &nm, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent private-connections")
}

func scanSASecurityRequirementPacks(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityagent.NewListSecurityRequirementPacksPaginator(client, &securityagent.ListSecurityRequirementPacksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListSecurityRequirementPacks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListSecurityRequirementPacks: %w", err)
		}
		for _, p := range out.SecurityRequirementPackSummaries {
			id := sv(p.PackId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:security-requirement-pack/%s", region, acct.ID, id)
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentSecurityRequirementPack, NativeID: arn,
				Name: p.Name, Region: &region, Status: &status,
				ManagedByProvider: p.ManagementType == "AWS_MANAGED",
				AttributesJSON:    mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent security-requirement-packs")
}

func scanSAAgentSpaces(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := securityagent.NewListAgentSpacesPaginator(client, &securityagent.ListAgentSpacesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "securityagent:ListAgentSpaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("securityagent:ListAgentSpaces: %w", err)
		}
		for _, s := range out.AgentSpaceSummaries {
			id := sv(s.AgentSpaceId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:agent-space/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentAgentSpace, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "securityagent agent-spaces")
	return ids, t, i, err
}

func scanSAApplications(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityagent.NewListApplicationsPaginator(client, &securityagent.ListApplicationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListApplications: %w", err)
		}
		for _, a := range out.ApplicationSummaries {
			id := sv(a.ApplicationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:application/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentApplication, NativeID: arn,
				Name: a.ApplicationName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent applications")
}

func scanSAPentests(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, agentSpaceID string) (int, int, error) {
	asid := agentSpaceID
	pager := securityagent.NewListPentestsPaginator(client, &securityagent.ListPentestsInput{AgentSpaceId: &asid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListPentests", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListPentests: %w", err)
		}
		for _, p := range out.PentestSummaries {
			pid := sv(p.PentestId)
			if pid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:agent-space/%s/pentest/%s", region, acct.ID, asid, pid)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentPentest, NativeID: arn,
				Name: p.Title, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent pentests")
}

func scanSATargetDomains(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityagent.NewListTargetDomainsPaginator(client, &securityagent.ListTargetDomainsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securityagent:ListTargetDomains", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securityagent:ListTargetDomains: %w", err)
		}
		for _, d := range out.TargetDomainSummaries {
			id := sv(d.TargetDomainId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityagent:%s:%s:target-domain/%s", region, acct.ID, id)
			status := string(d.VerificationStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentTargetDomain, NativeID: arn,
				Name: d.DomainName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityagent target-domains")
}

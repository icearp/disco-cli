package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/customerprofiles"
)

func init() {
	registerService(serviceEntry{
		name: "aws:customer-profiles",
		fn:   scanCustomerProfiles,
		emits: []coverage.TypeDecl{
			{Service: "customer-profiles", DiscoType: TypeCPDomain},
			{Service: "customer-profiles", DiscoType: TypeCPCalculatedAttributeDefinition},
			{Service: "customer-profiles", DiscoType: TypeCPEventStream},
			{Service: "customer-profiles", DiscoType: TypeCPEventTrigger},
			{Service: "customer-profiles", DiscoType: TypeCPIntegration},
			{Service: "customer-profiles", DiscoType: TypeCPObjectType},
			{Service: "customer-profiles", DiscoType: TypeCPRecommender},
			{Service: "customer-profiles", DiscoType: TypeCPSegmentDefinition},
		},
	})
}

type cpAPI interface {
	ListDomains(context.Context, *customerprofiles.ListDomainsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListDomainsOutput, error)
	GetDomain(context.Context, *customerprofiles.GetDomainInput, ...func(*customerprofiles.Options)) (*customerprofiles.GetDomainOutput, error)
	ListRecommenders(context.Context, *customerprofiles.ListRecommendersInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListRecommendersOutput, error)
	ListCalculatedAttributeDefinitions(context.Context, *customerprofiles.ListCalculatedAttributeDefinitionsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListCalculatedAttributeDefinitionsOutput, error)
	ListEventStreams(context.Context, *customerprofiles.ListEventStreamsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListEventStreamsOutput, error)
	ListEventTriggers(context.Context, *customerprofiles.ListEventTriggersInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListEventTriggersOutput, error)
	ListIntegrations(context.Context, *customerprofiles.ListIntegrationsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListIntegrationsOutput, error)
	ListProfileObjectTypes(context.Context, *customerprofiles.ListProfileObjectTypesInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListProfileObjectTypesOutput, error)
	ListSegmentDefinitions(context.Context, *customerprofiles.ListSegmentDefinitionsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListSegmentDefinitionsOutput, error)
}

func cpARN(region, acct string, segs ...string) string {
	s := fmt.Sprintf("arn:aws:profile:%s:%s:domains", region, acct)
	for _, seg := range segs {
		s += "/" + seg
	}
	return s
}

func scanCustomerProfiles(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := customerprofiles.NewFromConfig(acct.cfg, func(o *customerprofiles.Options) { o.Region = region })

	// Phase 1: domains (collect names).
	names, t, i, ferr := scanCPDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Phase 2: per-domain children.
	for _, name := range names {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanCPCalcAttrDefs(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPEventStreams(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPEventTriggers(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPIntegrations(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPObjectTypes(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPSegmentDefinitions(ctx, client, acct, region, st, scanID, name) },
			func() (int, int, error) { return scanCPRecommenders(ctx, client, acct, region, st, scanID, name) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanCPDomains(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	// No SDK paginator; manual NextToken loop.
	input := &customerprofiles.ListDomainsInput{}
	var batch []*store.Resource
	var names []string
	for {
		out, err := client.ListDomains(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "profile:ListDomains", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("profile:ListDomains: %w", err)
		}
		for _, d := range out.Items {
			name := sv(d.DomainName)
			if name == "" {
				continue
			}
			arn := cpARN(region, acct.ID, name)
			label := name
			names = append(names, name)
			attrsJSON := mustJSON(d)
			if gout, gerr := client.GetDomain(ctx, &customerprofiles.GetDomainInput{DomainName: d.DomainName}); gerr == nil {
				attrsJSON = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPDomain, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "customer-profiles domains")
	return names, t, i, err
}

func scanCPCalcAttrDefs(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	input := &customerprofiles.ListCalculatedAttributeDefinitionsInput{DomainName: &dn}
	var batch []*store.Resource
	for {
		out, err := client.ListCalculatedAttributeDefinitions(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "profile:ListCalculatedAttributeDefinitions", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListCalculatedAttributeDefinitions: %w", err)
		}
		for _, c := range out.Items {
			name := sv(c.CalculatedAttributeName)
			if name == "" {
				continue
			}
			arn := cpARN(region, acct.ID, domainName, "calculated-attributes", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPCalculatedAttributeDefinition, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "customer-profiles calculated-attribute-definitions")
}

func scanCPEventStreams(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	pager := customerprofiles.NewListEventStreamsPaginator(client, &customerprofiles.ListEventStreamsInput{DomainName: &dn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "profile:ListEventStreams", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListEventStreams: %w", perr)
		}
		for _, e := range out.Items {
			arn := sv(e.EventStreamArn)
			if arn == "" {
				continue
			}
			label := sv(e.EventStreamName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPEventStream, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "customer-profiles event-streams")
}

func scanCPEventTriggers(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	pager := customerprofiles.NewListEventTriggersPaginator(client, &customerprofiles.ListEventTriggersInput{DomainName: &dn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "profile:ListEventTriggers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListEventTriggers: %w", perr)
		}
		for _, e := range out.Items {
			name := sv(e.EventTriggerName)
			if name == "" {
				continue
			}
			arn := cpARN(region, acct.ID, domainName, "event-triggers", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPEventTrigger, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "customer-profiles event-triggers")
}

func scanCPIntegrations(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	input := &customerprofiles.ListIntegrationsInput{DomainName: &dn}
	var batch []*store.Resource
	for {
		out, err := client.ListIntegrations(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "profile:ListIntegrations", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListIntegrations: %w", err)
		}
		for _, integration := range out.Items {
			uri := sv(integration.Uri)
			if uri == "" {
				continue
			}
			arn := cpARN(region, acct.ID, domainName, "integrations", uri)
			label := uri
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPIntegration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(integration), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "customer-profiles integrations")
}

func scanCPObjectTypes(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	input := &customerprofiles.ListProfileObjectTypesInput{DomainName: &dn}
	var batch []*store.Resource
	for {
		out, err := client.ListProfileObjectTypes(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "profile:ListProfileObjectTypes", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListProfileObjectTypes: %w", err)
		}
		for _, ot := range out.Items {
			name := sv(ot.ObjectTypeName)
			if name == "" {
				continue
			}
			arn := cpARN(region, acct.ID, domainName, "object-types", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPObjectType, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ot), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "customer-profiles object-types")
}

func scanCPSegmentDefinitions(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	pager := customerprofiles.NewListSegmentDefinitionsPaginator(client, &customerprofiles.ListSegmentDefinitionsInput{DomainName: &dn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "profile:ListSegmentDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListSegmentDefinitions: %w", perr)
		}
		for _, s := range out.Items {
			arn := sv(s.SegmentDefinitionArn)
			if arn == "" {
				continue
			}
			label := sv(s.SegmentDefinitionName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPSegmentDefinition, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "customer-profiles segment-definitions")
}

func scanCPRecommenders(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID, domainName string) (int, int, error) {
	dn := domainName
	pager := customerprofiles.NewListRecommendersPaginator(client, &customerprofiles.ListRecommendersInput{DomainName: &dn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "profile:ListRecommenders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("profile:ListRecommenders: %w", perr)
		}
		for _, r := range out.Recommenders {
			name := sv(r.RecommenderName)
			if name == "" {
				continue
			}
			arn := cpARN(region, acct.ID, domainName, "recommenders", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCPRecommender, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "customer-profiles recommenders")
}

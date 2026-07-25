package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connectcases"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCasesDomain, Service: "cases", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCasesCaseRule, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesField, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesLayout, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesTemplate, Service: "cases"})
	registerService(serviceEntry{
		name: "aws:cases",
		fn:   scanCases,
	})
}

type casesAPI interface {
	ListDomains(context.Context, *connectcases.ListDomainsInput, ...func(*connectcases.Options)) (*connectcases.ListDomainsOutput, error)
	ListCaseRules(context.Context, *connectcases.ListCaseRulesInput, ...func(*connectcases.Options)) (*connectcases.ListCaseRulesOutput, error)
	ListFields(context.Context, *connectcases.ListFieldsInput, ...func(*connectcases.Options)) (*connectcases.ListFieldsOutput, error)
	ListLayouts(context.Context, *connectcases.ListLayoutsInput, ...func(*connectcases.Options)) (*connectcases.ListLayoutsOutput, error)
	ListTemplates(context.Context, *connectcases.ListTemplatesInput, ...func(*connectcases.Options)) (*connectcases.ListTemplatesOutput, error)
}

// scanCases discovers Connect Cases domains and per-domain case rules,
// fields, layouts, and templates.
func scanCases(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connectcases.NewFromConfig(acct.cfg, func(o *connectcases.Options) { o.Region = region })

	domainIDs, t, i, ferr := scanCasesDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, d := range domainIDs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanCasesCaseRules(ctx, client, acct, region, st, scanID, d) },
			func() (int, int, error) { return scanCasesFields(ctx, client, acct, region, st, scanID, d) },
			func() (int, int, error) { return scanCasesLayouts(ctx, client, acct, region, st, scanID, d) },
			func() (int, int, error) { return scanCasesTemplates(ctx, client, acct, region, st, scanID, d) },
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

func scanCasesDomains(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := connectcases.NewListDomainsPaginator(client, &connectcases.ListDomainsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "connectcases:ListDomains", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("connectcases:ListDomains: %w", err)
		}
		for _, d := range out.Domains {
			arn := sv(d.DomainArn)
			if arn == "" {
				continue
			}
			if id := sv(d.DomainId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesDomain, NativeID: arn,
				Name: d.Name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cases domains")
	return ids, t, i, err
}

func scanCasesCaseRules(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListCaseRulesPaginator(client, &connectcases.ListCaseRulesInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListCaseRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListCaseRules: %w", err)
		}
		for _, r := range out.CaseRules {
			arn := sv(r.CaseRuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesCaseRule, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases case-rules")
}

func scanCasesFields(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListFieldsPaginator(client, &connectcases.ListFieldsInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListFields", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListFields: %w", err)
		}
		for _, f := range out.Fields {
			arn := sv(f.FieldArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesField, NativeID: arn,
				Name: f.Name, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases fields")
}

func scanCasesLayouts(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListLayoutsPaginator(client, &connectcases.ListLayoutsInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListLayouts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListLayouts: %w", err)
		}
		for _, l := range out.Layouts {
			arn := sv(l.LayoutArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesLayout, NativeID: arn,
				Name: l.Name, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases layouts")
}

func scanCasesTemplates(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListTemplatesPaginator(client, &connectcases.ListTemplatesInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListTemplates: %w", err)
		}
		for _, tmpl := range out.Templates {
			arn := sv(tmpl.TemplateArn)
			if arn == "" {
				continue
			}
			status := string(tmpl.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesTemplate, NativeID: arn,
				Name: tmpl.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(tmpl), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases templates")
}

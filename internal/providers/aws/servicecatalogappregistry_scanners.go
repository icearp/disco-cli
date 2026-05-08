package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalogappregistry"
)

func init() {
	registerService(serviceEntry{
		name: "aws:service-catalog-app-registry",
		fn:   scanServiceCatalogAppRegistry,
		emits: []coverage.TypeDecl{
			{Service: "service-catalog-app-registry", DiscoType: TypeSCARApplication, Leaf: true},
			{Service: "service-catalog-app-registry", DiscoType: TypeSCARAttributeGroup, Leaf: true},
			{Service: "service-catalog-app-registry", DiscoType: TypeSCARAttributeGroupAssociation},
			{Service: "service-catalog-app-registry", DiscoType: TypeSCARResourceAssociation},
		},
	})
}

type scarAPI interface {
	ListApplications(context.Context, *servicecatalogappregistry.ListApplicationsInput, ...func(*servicecatalogappregistry.Options)) (*servicecatalogappregistry.ListApplicationsOutput, error)
	ListAttributeGroups(context.Context, *servicecatalogappregistry.ListAttributeGroupsInput, ...func(*servicecatalogappregistry.Options)) (*servicecatalogappregistry.ListAttributeGroupsOutput, error)
	ListAttributeGroupsForApplication(context.Context, *servicecatalogappregistry.ListAttributeGroupsForApplicationInput, ...func(*servicecatalogappregistry.Options)) (*servicecatalogappregistry.ListAttributeGroupsForApplicationOutput, error)
	ListAssociatedResources(context.Context, *servicecatalogappregistry.ListAssociatedResourcesInput, ...func(*servicecatalogappregistry.Options)) (*servicecatalogappregistry.ListAssociatedResourcesOutput, error)
}

// scanServiceCatalogAppRegistry discovers AppRegistry applications,
// attribute groups, plus per-application attribute-group and resource
// associations. Applications and attribute groups carry native ARNs;
// associations synthesize an ARN as parent application ARN + path.
func scanServiceCatalogAppRegistry(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := servicecatalogappregistry.NewFromConfig(acct.cfg, func(o *servicecatalogappregistry.Options) { o.Region = region })

	apps, t, i, ferr := scanSCARApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSCARAttributeGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, app := range apps {
		t, i, ferr = scanSCARAGAssociations(ctx, client, acct, region, st, scanID, app)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanSCARResourceAssociations(ctx, client, acct, region, st, scanID, app)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type scarApp struct{ id, arn string }

func scanSCARApplications(ctx context.Context, client scarAPI, acct *account, region string, st *store.Store, scanID string) ([]scarApp, int, int, error) {
	pager := servicecatalogappregistry.NewListApplicationsPaginator(client, &servicecatalogappregistry.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []scarApp
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "servicecatalogappregistry:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("servicecatalogappregistry:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			id := sv(a.Id)
			if arn == "" || id == "" {
				continue
			}
			apps = append(apps, scarApp{id, arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSCARApplication, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appregistry applications")
	return apps, t, i, err
}

func scanSCARAttributeGroups(ctx context.Context, client scarAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := servicecatalogappregistry.NewListAttributeGroupsPaginator(client, &servicecatalogappregistry.ListAttributeGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "servicecatalogappregistry:ListAttributeGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("servicecatalogappregistry:ListAttributeGroups: %w", err)
		}
		for _, ag := range out.AttributeGroups {
			arn := sv(ag.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSCARAttributeGroup, NativeID: arn,
				Name: ag.Name, Region: &region,
				AttributesJSON: mustJSON(ag), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appregistry attribute-groups")
}

func scanSCARAGAssociations(ctx context.Context, client scarAPI, acct *account, region string, st *store.Store, scanID string, app scarApp) (int, int, error) {
	id := app.id
	pager := servicecatalogappregistry.NewListAttributeGroupsForApplicationPaginator(client, &servicecatalogappregistry.ListAttributeGroupsForApplicationInput{Application: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "servicecatalogappregistry:ListAttributeGroupsForApplication", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("servicecatalogappregistry:ListAttributeGroupsForApplication: %w", err)
		}
		for _, ag := range out.AttributeGroupsDetails {
			agID := sv(ag.Id)
			if agID == "" {
				continue
			}
			arn := app.arn + "/attribute-group-association/" + agID
			label := agID
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSCARAttributeGroupAssociation, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(ag), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appregistry attribute-group-associations")
}

func scanSCARResourceAssociations(ctx context.Context, client scarAPI, acct *account, region string, st *store.Store, scanID string, app scarApp) (int, int, error) {
	id := app.id
	pager := servicecatalogappregistry.NewListAssociatedResourcesPaginator(client, &servicecatalogappregistry.ListAssociatedResourcesInput{Application: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "servicecatalogappregistry:ListAssociatedResources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("servicecatalogappregistry:ListAssociatedResources: %w", err)
		}
		for _, r := range out.Resources {
			rArn := sv(r.Arn)
			if rArn == "" {
				continue
			}
			arn := app.arn + "/resource-association/" + rArn
			label := sv(r.Name)
			if label == "" {
				label = rArn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSCARResourceAssociation, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appregistry resource-associations")
}

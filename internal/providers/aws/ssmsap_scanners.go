package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ssmsap"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMSAPApplication, Service: "systems-manager-sap", Upstream: "AWS::SystemsManagerSAP::Application", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSystemsManagerSAPComponent, Service: "systems-manager-sap", Upstream: "AWS::ssm-sap::component"})
	registerType(restype.Descriptor{Type: TypeSystemsManagerSAPDatabase, Service: "systems-manager-sap", Upstream: "AWS::ssm-sap::database"})
	registerService(serviceEntry{
		name: "aws:systems-manager-sap",
		fn:   scanSSMSAP,
	})
}

type ssmSAPAPI interface {
	ListApplications(context.Context, *ssmsap.ListApplicationsInput, ...func(*ssmsap.Options)) (*ssmsap.ListApplicationsOutput, error)
	ListComponents(context.Context, *ssmsap.ListComponentsInput, ...func(*ssmsap.Options)) (*ssmsap.ListComponentsOutput, error)
	ListDatabases(context.Context, *ssmsap.ListDatabasesInput, ...func(*ssmsap.Options)) (*ssmsap.ListDatabasesOutput, error)
}

// scanSSMSAP discovers Systems Manager for SAP applications and, per
// application, the registered components and SAP HANA databases.
func scanSSMSAP(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmsap.NewFromConfig(acct.cfg, func(o *ssmsap.Options) { o.Region = region })

	appIDs, t, i, ferr := scanSSMSAPApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, id := range appIDs {
		t, i, ferr = scanSSMSAPComponents(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanSSMSAPDatabases(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSSMSAPApplications(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := ssmsap.NewListApplicationsPaginator(client, &ssmsap.ListApplicationsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "ssm-sap:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("ssm-sap:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			if id := sv(a.Id); id != "" {
				ids = append(ids, id)
			}
			status := string(a.DiscoveryStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMSAPApplication, NativeID: arn,
				Name: a.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "ssm-sap applications")
	return ids, t, i, err
}

func scanSSMSAPComponents(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID, appID string) (int, int, error) {
	id := appID
	pager := ssmsap.NewListComponentsPaginator(client, &ssmsap.ListComponentsInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-sap:ListComponents", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-sap:ListComponents: %w", err)
		}
		for _, c := range out.Components {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.ComponentId)
			status := string(c.ComponentType)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSystemsManagerSAPComponent, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm-sap components")
}

func scanSSMSAPDatabases(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID, appID string) (int, int, error) {
	id := appID
	pager := ssmsap.NewListDatabasesPaginator(client, &ssmsap.ListDatabasesInput{ApplicationId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-sap:ListDatabases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-sap:ListDatabases: %w", err)
		}
		for _, d := range out.Databases {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.DatabaseId)
			status := string(d.DatabaseType)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSystemsManagerSAPDatabase, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssm-sap databases")
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/workspacesthinclient"
)

func init() {
	registerService(serviceEntry{
		name: "aws:thinclient",
		fn:   scanThinClient,
		emits: []coverage.TypeDecl{
			{Service: "thinclient", DiscoType: TypeThinClientDevice},
			{Service: "thinclient", DiscoType: TypeThinClientEnvironment, Leaf: true},
			{Service: "thinclient", DiscoType: TypeThinClientSoftwareSet, Leaf: true},
		},
	})
}

type thinClientAPI interface {
	ListDevices(context.Context, *workspacesthinclient.ListDevicesInput, ...func(*workspacesthinclient.Options)) (*workspacesthinclient.ListDevicesOutput, error)
	ListEnvironments(context.Context, *workspacesthinclient.ListEnvironmentsInput, ...func(*workspacesthinclient.Options)) (*workspacesthinclient.ListEnvironmentsOutput, error)
	ListSoftwareSets(context.Context, *workspacesthinclient.ListSoftwareSetsInput, ...func(*workspacesthinclient.Options)) (*workspacesthinclient.ListSoftwareSetsOutput, error)
}

func scanThinClient(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspacesthinclient.NewFromConfig(acct.cfg, func(o *workspacesthinclient.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanThinClientDevices(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanThinClientEnvironments(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanThinClientSoftwareSets(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanThinClientDevices(ctx context.Context, client thinClientAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesthinclient.NewListDevicesPaginator(client, &workspacesthinclient.ListDevicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "thinclient:ListDevices", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("thinclient:ListDevices: %w", perr)
		}
		for _, d := range out.Devices {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeThinClientDevice, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "thinclient devices")
}

func scanThinClientEnvironments(ctx context.Context, client thinClientAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesthinclient.NewListEnvironmentsPaginator(client, &workspacesthinclient.ListEnvironmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "thinclient:ListEnvironments", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("thinclient:ListEnvironments: %w", perr)
		}
		for _, e := range out.Environments {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeThinClientEnvironment, NativeID: arn,
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "thinclient environments")
}

func scanThinClientSoftwareSets(ctx context.Context, client thinClientAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesthinclient.NewListSoftwareSetsPaginator(client, &workspacesthinclient.ListSoftwareSetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "thinclient:ListSoftwareSets", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("thinclient:ListSoftwareSets: %w", perr)
		}
		for _, s := range out.SoftwareSets {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Version)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeThinClientSoftwareSet, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "thinclient software-sets")
}

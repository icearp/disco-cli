package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/tnb"
)

func init() {
	registerService(serviceEntry{
		name: "aws:tnb",
		fn:   scanTnb,
		emits: []coverage.TypeDecl{
			{Service: "tnb", DiscoType: TypeTnbFunctionInstance},
			{Service: "tnb", DiscoType: TypeTnbFunctionPackage, Leaf: true},
			{Service: "tnb", DiscoType: TypeTnbNetworkInstance},
			{Service: "tnb", DiscoType: TypeTnbNetworkPackage, Leaf: true},
			{Service: "tnb", DiscoType: TypeTnbNetworkOperation},
		},
	})
}

type tnbAPI interface {
	ListSolFunctionInstances(context.Context, *tnb.ListSolFunctionInstancesInput, ...func(*tnb.Options)) (*tnb.ListSolFunctionInstancesOutput, error)
	ListSolFunctionPackages(context.Context, *tnb.ListSolFunctionPackagesInput, ...func(*tnb.Options)) (*tnb.ListSolFunctionPackagesOutput, error)
	ListSolNetworkInstances(context.Context, *tnb.ListSolNetworkInstancesInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkInstancesOutput, error)
	ListSolNetworkPackages(context.Context, *tnb.ListSolNetworkPackagesInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkPackagesOutput, error)
	ListSolNetworkOperations(context.Context, *tnb.ListSolNetworkOperationsInput, ...func(*tnb.Options)) (*tnb.ListSolNetworkOperationsOutput, error)
}

func scanTnb(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := tnb.NewFromConfig(acct.cfg, func(o *tnb.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanTnbFunctionPackages(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTnbFunctionInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTnbNetworkPackages(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTnbNetworkInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTnbNetworkOperations(ctx, client, acct, region, st, scanID) },
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

func scanTnbFunctionInstances(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := tnb.NewListSolFunctionInstancesPaginator(client, &tnb.ListSolFunctionInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "tnb:ListSolFunctionInstances", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("tnb:ListSolFunctionInstances: %w", perr)
		}
		for _, f := range out.FunctionInstances {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			status := string(f.InstantiationState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTnbFunctionInstance, NativeID: arn,
				Name: f.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "tnb function-instances")
}

func scanTnbFunctionPackages(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := tnb.NewListSolFunctionPackagesPaginator(client, &tnb.ListSolFunctionPackagesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "tnb:ListSolFunctionPackages", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("tnb:ListSolFunctionPackages: %w", perr)
		}
		for _, p := range out.FunctionPackages {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			status := string(p.OnboardingState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTnbFunctionPackage, NativeID: arn,
				Name: p.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "tnb function-packages")
}

func scanTnbNetworkInstances(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := tnb.NewListSolNetworkInstancesPaginator(client, &tnb.ListSolNetworkInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "tnb:ListSolNetworkInstances", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("tnb:ListSolNetworkInstances: %w", perr)
		}
		for _, n := range out.NetworkInstances {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			status := string(n.NsState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTnbNetworkInstance, NativeID: arn,
				Name: n.NsInstanceName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "tnb network-instances")
}

func scanTnbNetworkPackages(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := tnb.NewListSolNetworkPackagesPaginator(client, &tnb.ListSolNetworkPackagesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "tnb:ListSolNetworkPackages", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("tnb:ListSolNetworkPackages: %w", perr)
		}
		for _, p := range out.NetworkPackages {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			status := string(p.NsdOnboardingState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTnbNetworkPackage, NativeID: arn,
				Name: p.NsdName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "tnb network-packages")
}

func scanTnbNetworkOperations(ctx context.Context, client tnbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := tnb.NewListSolNetworkOperationsPaginator(client, &tnb.ListSolNetworkOperationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "tnb:ListSolNetworkOperations", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("tnb:ListSolNetworkOperations: %w", perr)
		}
		for _, o := range out.NetworkOperations {
			arn := sv(o.Arn)
			if arn == "" {
				continue
			}
			status := string(o.OperationState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTnbNetworkOperation, NativeID: arn,
				Name: o.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "tnb network-operations")
}

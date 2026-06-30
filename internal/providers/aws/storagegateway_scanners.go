package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/storagegateway"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:storagegateway",
		fn:   scanStorageGateway,
		emits: []coverage.TypeDecl{
			{Service: "storagegateway", DiscoType: TypeStorageGatewayGateway, Leaf: true},
			// volume / share / tape / fs-association / device wire an outbound
			// attached-to edge to their gateway (see storagegateway_resolvers.go).
			{Service: "storagegateway", DiscoType: TypeStorageGatewayVolume},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayShare},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayTape},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayTapePool, Leaf: true},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayFsAssociation},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayCacheReport, Leaf: true},
			{Service: "storagegateway", DiscoType: TypeStorageGatewayDevice},
		},
	})
}

// storageGatewayAPI is the narrow surface the scanner uses. Every List op is
// account-wide and paginator-native; DescribeVTLDevices is per-gateway (the
// gateway ARN is required) and has no paginator.
type storageGatewayAPI interface {
	ListGateways(context.Context, *storagegateway.ListGatewaysInput, ...func(*storagegateway.Options)) (*storagegateway.ListGatewaysOutput, error)
	ListVolumes(context.Context, *storagegateway.ListVolumesInput, ...func(*storagegateway.Options)) (*storagegateway.ListVolumesOutput, error)
	ListFileShares(context.Context, *storagegateway.ListFileSharesInput, ...func(*storagegateway.Options)) (*storagegateway.ListFileSharesOutput, error)
	ListTapes(context.Context, *storagegateway.ListTapesInput, ...func(*storagegateway.Options)) (*storagegateway.ListTapesOutput, error)
	ListTapePools(context.Context, *storagegateway.ListTapePoolsInput, ...func(*storagegateway.Options)) (*storagegateway.ListTapePoolsOutput, error)
	ListFileSystemAssociations(context.Context, *storagegateway.ListFileSystemAssociationsInput, ...func(*storagegateway.Options)) (*storagegateway.ListFileSystemAssociationsOutput, error)
	ListCacheReports(context.Context, *storagegateway.ListCacheReportsInput, ...func(*storagegateway.Options)) (*storagegateway.ListCacheReportsOutput, error)
	DescribeVTLDevices(context.Context, *storagegateway.DescribeVTLDevicesInput, ...func(*storagegateway.Options)) (*storagegateway.DescribeVTLDevicesOutput, error)
}

func scanStorageGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := storagegateway.NewFromConfig(acct.cfg, func(o *storagegateway.Options) { o.Region = region })
	return scanStorageGatewayWith(ctx, client, acct, region, st, scanID)
}

func scanStorageGatewayWith(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	gatewayARNs, t, i, ferr := scanSGWGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSGWVolumes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWFileShares(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWTapes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWTapePools(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWFileSystemAssociations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWCacheReports(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSGWVTLDevices(ctx, client, gatewayARNs, acct, region, st, scanID) },
	} {
		pt, pi, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += pt
		inserted += pi
	}
	return total, inserted, nil
}

func scanSGWGateways(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := storagegateway.NewListGatewaysPaginator(client, &storagegateway.ListGatewaysInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "storagegateway:ListGateways", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("storagegateway:ListGateways: %w", perr)
		}
		for _, g := range out.Gateways {
			arn := sv(g.GatewayARN)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			name := sv(g.GatewayName)
			state := sv(g.GatewayOperationalState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayGateway, NativeID: arn,
				Name: &name, Region: &region, Status: &state,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "storagegateway gateways")
	return arns, t, i, err
}

func scanSGWVolumes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListVolumesPaginator(client, &storagegateway.ListVolumesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListVolumes", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListVolumes: %w", perr)
		}
		for _, v := range out.VolumeInfos {
			arn := sv(v.VolumeARN)
			if arn == "" {
				continue
			}
			label := sv(v.VolumeId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayVolume, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway volumes")
}

func scanSGWFileShares(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListFileSharesPaginator(client, &storagegateway.ListFileSharesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListFileShares", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListFileShares: %w", perr)
		}
		for _, fs := range out.FileShareInfoList {
			arn := sv(fs.FileShareARN)
			if arn == "" {
				continue
			}
			label := sv(fs.FileShareId)
			status := sv(fs.FileShareStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayShare, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(fs), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway file-shares")
}

func scanSGWTapes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListTapesPaginator(client, &storagegateway.ListTapesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListTapes", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListTapes: %w", perr)
		}
		for _, tp := range out.TapeInfos {
			arn := sv(tp.TapeARN)
			if arn == "" {
				continue
			}
			label := sv(tp.TapeBarcode)
			status := sv(tp.TapeStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayTape, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(tp), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway tapes")
}

func scanSGWTapePools(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListTapePoolsPaginator(client, &storagegateway.ListTapePoolsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListTapePools", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListTapePools: %w", perr)
		}
		for _, p := range out.PoolInfos {
			arn := sv(p.PoolARN)
			if arn == "" {
				continue
			}
			label := sv(p.PoolName)
			status := string(p.PoolStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayTapePool, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway tape-pools")
}

func scanSGWFileSystemAssociations(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListFileSystemAssociationsPaginator(client, &storagegateway.ListFileSystemAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListFileSystemAssociations", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListFileSystemAssociations: %w", perr)
		}
		for _, fa := range out.FileSystemAssociationSummaryList {
			arn := sv(fa.FileSystemAssociationARN)
			if arn == "" {
				continue
			}
			label := sv(fa.FileSystemAssociationId)
			status := sv(fa.FileSystemAssociationStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayFsAssociation, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(fa), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway fs-associations")
}

func scanSGWCacheReports(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListCacheReportsPaginator(client, &storagegateway.ListCacheReportsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListCacheReports", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListCacheReports: %w", perr)
		}
		for _, cr := range out.CacheReportList {
			arn := sv(cr.CacheReportARN)
			if arn == "" {
				continue
			}
			label := sv(cr.ReportName)
			status := string(cr.CacheReportStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayCacheReport, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(cr), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway cache-reports")
}

// scanSGWVTLDevices fans out DescribeVTLDevices per gateway — only VTL (tape)
// gateways host devices, so non-VTL gateways reject the call; tolerate the
// per-gateway error and keep scanning siblings. The VTLDeviceARN embeds the
// gateway ARN, so the resolver recovers the parent from the NativeID.
func scanSGWVTLDevices(ctx context.Context, client storageGatewayAPI, gatewayARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(gatewayARNs) == 0 {
		return 0, 0, nil
	}
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, gwARN := range gatewayARNs {
		g.Go(func() error {
			marker := ""
			for {
				in := &storagegateway.DescribeVTLDevicesInput{GatewayARN: &gwARN}
				if marker != "" {
					in.Marker = &marker
				}
				out, derr := client.DescribeVTLDevices(gctx, in)
				if derr != nil {
					// Non-VTL gateways (file/volume) reject DescribeVTLDevices;
					// tolerate per-gateway like an access denial.
					if isAccessDenied(derr) || isAPIErrorCode(derr, "InvalidGatewayRequestException") {
						return nil
					}
					return fmt.Errorf("storagegateway:DescribeVTLDevices %s: %w", gwARN, derr)
				}
				mu.Lock()
				for _, d := range out.VTLDevices {
					arn := sv(d.VTLDeviceARN)
					if arn == "" {
						continue
					}
					label := sv(d.VTLDeviceType)
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeStorageGatewayDevice, NativeID: arn,
						Name: &label, Region: &region,
						AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
					})
				}
				mu.Unlock()
				if out.Marker == nil || *out.Marker == "" {
					break
				}
				marker = *out.Marker
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	return upsertBatch(st, batch, "storagegateway vtl-devices")
}

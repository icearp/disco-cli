package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
)

func init() {
	registerService(serviceEntry{
		name: "aws:backupgateway",
		fn:   scanBackupGateway,
		emits: []coverage.TypeDecl{
			{Service: "backupgateway", DiscoType: TypeBackupGatewayHypervisor},
			// gateway + virtual-machine reference their hypervisor.
			{Service: "backupgateway", DiscoType: TypeBackupGatewayGateway},
			{Service: "backupgateway", DiscoType: TypeBackupGatewayVirtualMachine},
		},
	})
}

// backupGatewayAPI is the narrow surface scanBackupGateway uses.
type backupGatewayAPI interface {
	ListHypervisors(context.Context, *backupgateway.ListHypervisorsInput, ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error)
	ListGateways(context.Context, *backupgateway.ListGatewaysInput, ...func(*backupgateway.Options)) (*backupgateway.ListGatewaysOutput, error)
	ListVirtualMachines(context.Context, *backupgateway.ListVirtualMachinesInput, ...func(*backupgateway.Options)) (*backupgateway.ListVirtualMachinesOutput, error)
}

func scanBackupGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := backupgateway.NewFromConfig(acct.cfg, func(o *backupgateway.Options) { o.Region = region })
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBackupGatewayHypervisors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBackupGatewayGateways(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanBackupGatewayVirtualMachines(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBackupGatewayGateways(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := backupgateway.NewListGatewaysPaginator(client, &backupgateway.ListGatewaysInput{})
	var batch []*store.Resource
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "backupgateway:ListGateways", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("backupgateway:ListGateways: %w", perr)
		}
		for _, gw := range page.Gateways {
			arn := sv(gw.GatewayArn)
			if arn == "" {
				continue
			}
			label := sv(gw.GatewayDisplayName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupGatewayGateway, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(gw), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "backupgateway gateways")
}

func scanBackupGatewayVirtualMachines(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := backupgateway.NewListVirtualMachinesPaginator(client, &backupgateway.ListVirtualMachinesInput{})
	var batch []*store.Resource
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "backupgateway:ListVirtualMachines", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("backupgateway:ListVirtualMachines: %w", perr)
		}
		for _, vm := range page.VirtualMachines {
			arn := sv(vm.ResourceArn)
			if arn == "" {
				continue
			}
			label := sv(vm.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupGatewayVirtualMachine, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(vm), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "backupgateway virtual-machines")
}

func scanBackupGatewayHypervisors(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := backupgateway.NewListHypervisorsPaginator(client, &backupgateway.ListHypervisorsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "backupgateway:ListHypervisors", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("backupgateway:ListHypervisors: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Hypervisors))
		for _, h := range page.Hypervisors {
			arn := sv(h.HypervisorArn)
			if arn == "" {
				continue
			}
			name := sv(h.Name)
			status := string(h.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBackupGatewayHypervisor,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(h),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert backupgateway hypervisors: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

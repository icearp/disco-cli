package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBackupGatewayHypervisor, Service: "backupgateway"})
	registerType(restype.Descriptor{Type: TypeBackupGatewayGateway, Service: "backupgateway"})
	registerType(restype.Descriptor{Type: TypeBackupGatewayVirtualMachine, Service: "backupgateway"})
	registerType(restype.Descriptor{Type: TypeBackupGatewayBandwidthRateLimitSchedule, Service: "backupgateway"})
	registerType(restype.Descriptor{Type: TypeBackupGatewayHypervisorPropertyMapping, Service: "backupgateway"})
	registerService(serviceEntry{
		name: "aws:backupgateway",
		fn:   scanBackupGateway,
	})
}

const (
	backupGatewayBandwidthScheduleOp = "backupgateway:GetBandwidthRateLimitSchedule"
	backupGatewayPropertyMappingsOp  = "backupgateway:GetHypervisorPropertyMappings"
)

// backupGatewayAPI is the narrow surface scanBackupGateway uses.
type backupGatewayAPI interface {
	ListHypervisors(context.Context, *backupgateway.ListHypervisorsInput, ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error)
	ListGateways(context.Context, *backupgateway.ListGatewaysInput, ...func(*backupgateway.Options)) (*backupgateway.ListGatewaysOutput, error)
	ListVirtualMachines(context.Context, *backupgateway.ListVirtualMachinesInput, ...func(*backupgateway.Options)) (*backupgateway.ListVirtualMachinesOutput, error)
	GetBandwidthRateLimitSchedule(context.Context, *backupgateway.GetBandwidthRateLimitScheduleInput, ...func(*backupgateway.Options)) (*backupgateway.GetBandwidthRateLimitScheduleOutput, error)
	GetHypervisorPropertyMappings(context.Context, *backupgateway.GetHypervisorPropertyMappingsInput, ...func(*backupgateway.Options)) (*backupgateway.GetHypervisorPropertyMappingsOutput, error)
}

func scanBackupGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := backupgateway.NewFromConfig(acct.cfg, func(o *backupgateway.Options) { o.Region = region })
	return scanBackupGatewayAll(ctx, client, acct, region, st, scanID)
}

// scanBackupGatewayAll runs the resource phases, which stop at their first
// hard error, then the per-parent configuration phases over the parents stored
// before that error, so a failure on either side costs the other no rows. The
// resource side's transient error is demoted to a warning: one transient
// member would make the dispatcher read the whole join as transient, hiding a
// configuration phase's hard error and discarding every phase's counts.
func scanBackupGatewayAll(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, parents, err := scanBackupGatewayResources(ctx, client, acct, region, st, scanID)
	t1, i1, serr := scanBackupGatewayBandwidthSchedules(ctx, client, acct, region, st, scanID, parents.gateways)
	t2, i2, merr := scanBackupGatewayPropertyMappings(ctx, client, acct, region, st, scanID, parents.hypervisors)
	return total + t1 + t2, inserted + i1 + i2, errors.Join(demoteTransient(st, "backupgateway", acct, region, err), serr, merr)
}

// backupGatewayParents holds the hypervisors and gateways stored this scan.
type backupGatewayParents struct {
	hypervisors, gateways []childParent
}

// scanBackupGatewayResources runs the hypervisor, gateway and virtual-machine
// phases in order, stopping at the first hard error, and returns the parents
// stored before it.
func scanBackupGatewayResources(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents backupGatewayParents, err error) {
	var t, i int
	parents.hypervisors, t, i, err = scanBackupGatewayHypervisors(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, parents, err
	}
	parents.gateways, t, i, err = scanBackupGatewayGateways(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, parents, err
	}
	t, i, err = scanBackupGatewayVirtualMachines(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, parents, err
}

// isBackupGatewayParentGone reports a parent deleted since it was listed.
func isBackupGatewayParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// backupGatewayConfigFanOut reads one configuration per parent through get
// and links each row under its parent. Per parent: ResourceNotFound (deleted
// since listing) is skipped silently, a denial warns once, a transient error
// after the SDK's retries and a ValidationException are each skipped with one
// warning for all such parents, and anything else fails the phase. How these
// ops answer a parent that is not ONLINE has not been observed; failing the
// phase would fail it on every scan, and skipping silently would hide a real
// input error.
func backupGatewayConfigFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op, why string, parents []childParent,
	get func(context.Context, childParent) (*store.Resource, error),
) (int, int, error) {
	return childFanOutSkipNotReady(ctx, st, acct, region, op, parents, isBackupGatewayParentGone, byParentARN,
		"parent(s)", why, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			r, err := get(ctx, p)
			if err != nil || r == nil {
				return nil, err
			}
			return []*store.Resource{r}, nil
		})
}

// scanBackupGatewayBandwidthSchedules stores the bandwidth rate limit schedule
// of each gateway that has one, one row per gateway with NativeID
// {gatewayARN}/bandwidth-rate-limit-schedule. gateways holds only gateways
// stored this scan. A gateway with no intervals has no schedule and stores
// nothing.
func scanBackupGatewayBandwidthSchedules(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent) (int, int, error) {
	return backupGatewayConfigFanOut(ctx, st, acct, region, backupGatewayBandwidthScheduleOp,
		"Backup Gateway may refuse a gateway that is offline (unverified)", gateways,
		func(ctx context.Context, p childParent) (*store.Resource, error) {
			out, err := client.GetBandwidthRateLimitSchedule(ctx, &backupgateway.GetBandwidthRateLimitScheduleInput{GatewayArn: &p.arn})
			if err != nil || len(out.BandwidthRateLimitIntervals) == 0 {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupGatewayBandwidthRateLimitSchedule, NativeID: p.arn + "/bandwidth-rate-limit-schedule",
				Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, nil
		})
}

// scanBackupGatewayPropertyMappings stores the property mappings of each
// hypervisor that has them, one row per hypervisor with NativeID
// {hypervisorARN}/property-mappings. hypervisors holds only hypervisors stored
// this scan. A hypervisor with neither an IAM role nor a tag mapping has none
// and stores nothing.
func scanBackupGatewayPropertyMappings(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, hypervisors []childParent) (int, int, error) {
	return backupGatewayConfigFanOut(ctx, st, acct, region, backupGatewayPropertyMappingsOp,
		"Backup Gateway may refuse a hypervisor that is not ONLINE (unverified)", hypervisors,
		func(ctx context.Context, p childParent) (*store.Resource, error) {
			out, err := client.GetHypervisorPropertyMappings(ctx, &backupgateway.GetHypervisorPropertyMappingsInput{HypervisorArn: &p.arn})
			if err != nil || (sv(out.IamRoleArn) == "" && len(out.VmwareToAwsTagMappings) == 0) {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupGatewayHypervisorPropertyMapping, NativeID: backupGatewayPropertyMappingNativeID(p.arn),
				Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, nil
		})
}

func backupGatewayPropertyMappingNativeID(hypervisorARN string) string {
	return hypervisorARN + "/property-mappings"
}

// scanBackupGatewayGateways stores every gateway and returns them as parents,
// none unless the phase stored them.
func scanBackupGatewayGateways(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	p := backupgateway.NewListGatewaysPaginator(client, &backupgateway.ListGatewaysInput{})
	var batch []*store.Resource
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "backupgateway:ListGateways", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("backupgateway:ListGateways: %w", perr)
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
	total, inserted, err := upsertBatch(st, batch, "backupgateway gateways")
	if err != nil {
		return nil, total, inserted, err
	}
	parents := make([]childParent, len(batch))
	for i, r := range batch {
		parents[i] = childParent{id: r.NativeID, arn: r.NativeID}
	}
	return parents, total, inserted, nil
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

// scanBackupGatewayHypervisors stores every hypervisor and returns those stored
// as parents, including the pages stored before an error.
func scanBackupGatewayHypervisors(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (parents []childParent, total, inserted int, err error) {
	p := backupgateway.NewListHypervisorsPaginator(client, &backupgateway.ListHypervisorsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return parents, total, inserted, skipIfAccessDenied(st, "backupgateway:ListHypervisors", acct.ID, region, err)
			}
			return parents, total, inserted, fmt.Errorf("backupgateway:ListHypervisors: %w", err)
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
				return parents, total, inserted, fmt.Errorf("upsert backupgateway hypervisors: %w", err)
			}
			total += len(batch)
			inserted += n
			for _, r := range batch {
				parents = append(parents, childParent{id: r.NativeID, arn: r.NativeID})
			}
		}
	}
	return parents, total, inserted, nil
}

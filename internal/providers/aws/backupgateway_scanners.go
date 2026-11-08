package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	bgwtypes "github.com/aws/aws-sdk-go-v2/service/backupgateway/types"
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
	backupGatewayListHypervisorsOp     = "backupgateway:ListHypervisors"
	backupGatewayListGatewaysOp        = "backupgateway:ListGateways"
	backupGatewayListVirtualMachinesOp = "backupgateway:ListVirtualMachines"
	backupGatewayBandwidthScheduleOp   = "backupgateway:GetBandwidthRateLimitSchedule"
	backupGatewayPropertyMappingsOp    = "backupgateway:GetHypervisorPropertyMappings"
	backupGatewayListTagsForResourceOp = "backupgateway:ListTagsForResource"
)

// backupGatewayGone is the not-found code ListTagsForResource models, and the
// one GetBandwidthRateLimitSchedule and GetHypervisorPropertyMappings model
// for a parent deleted since it was listed.
var backupGatewayGone = []string{"ResourceNotFoundException"}

// backupGatewayAPI is the narrow surface scanBackupGateway uses.
type backupGatewayAPI interface {
	ListHypervisors(context.Context, *backupgateway.ListHypervisorsInput, ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error)
	ListGateways(context.Context, *backupgateway.ListGatewaysInput, ...func(*backupgateway.Options)) (*backupgateway.ListGatewaysOutput, error)
	ListVirtualMachines(context.Context, *backupgateway.ListVirtualMachinesInput, ...func(*backupgateway.Options)) (*backupgateway.ListVirtualMachinesOutput, error)
	GetBandwidthRateLimitSchedule(context.Context, *backupgateway.GetBandwidthRateLimitScheduleInput, ...func(*backupgateway.Options)) (*backupgateway.GetBandwidthRateLimitScheduleOutput, error)
	GetHypervisorPropertyMappings(context.Context, *backupgateway.GetHypervisorPropertyMappingsInput, ...func(*backupgateway.Options)) (*backupgateway.GetHypervisorPropertyMappingsOutput, error)
	ListTagsForResource(context.Context, *backupgateway.ListTagsForResourceInput, ...func(*backupgateway.Options)) (*backupgateway.ListTagsForResourceOutput, error)
}

func scanBackupGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := backupgateway.NewFromConfig(acct.cfg, func(o *backupgateway.Options) { o.Region = region })
	return scanBackupGatewayAll(ctx, client, acct, region, st, scanID)
}

// scanBackupGatewayAll runs every listing as its own phase, one at a time:
// the hypervisors, gateways and virtual machines, then the per-parent
// configuration phases over the gateways and hypervisors stored this scan. A
// phase's error does not stop the later ones; a failed write and ctx's end
// do, and so does an NXDOMAIN of the first listing with nothing stored (the
// region does not serve the service) (runHostPhases). Hypervisors, gateways
// and virtual machines have their tags read per row (backupGatewayTags),
// whose denials are one warning for the scan.
func scanBackupGatewayAll(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := backupGatewayTags(client).sharingDenials()
	var hypervisors, gateways []childParent
	total, err := runHostPhases(ctx, st, acct, region, "backupgateway", 0, []hostPhase{
		{op: backupGatewayListHypervisorsOp, run: func(p *phaseTotals) (failed bool) {
			hypervisors, failed = scanBackupGatewayHypervisors(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: backupGatewayListGatewaysOp, run: func(p *phaseTotals) (failed bool) {
			gateways, failed = scanBackupGatewayGateways(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: backupGatewayListVirtualMachinesOp, run: func(p *phaseTotals) bool {
			return scanBackupGatewayVirtualMachines(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, backupGatewayBandwidthScheduleOp, func() (int, error) {
			return scanBackupGatewayBandwidthSchedules(ctx, client, acct, region, st, scanID, gateways)
		}),
		countedPhase(0, backupGatewayPropertyMappingsOp, func() (int, error) {
			return scanBackupGatewayPropertyMappings(ctx, client, acct, region, st, scanID, hypervisors)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// backupGatewayTags reads a row's tags with ListTagsForResource, sent the
// row's NativeID: the ARN its listing returned. The Service Reference lists
// gateway, hypervisor and virtualmachine, the three types read here, under
// ListTagsForResource and TagResource, and the op's model ARN pattern accepts
// each of their ARNs. The op does not page its tags.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
func backupGatewayTags(client backupGatewayAPI) rowTagReader {
	return rowTagReader{
		op:       backupGatewayListTagsForResourceOp,
		notFound: backupGatewayGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &backupgateway.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t bgwtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// isBackupGatewayParentGone reports a parent deleted since it was listed.
func isBackupGatewayParentGone(err error) bool {
	return isAPIErrorCode(err, backupGatewayGone...)
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
) (int, error) {
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
func scanBackupGatewayBandwidthSchedules(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent) (int, error) {
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
func scanBackupGatewayPropertyMappings(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, hypervisors []childParent) (int, error) {
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

// scanBackupGatewayHypervisors stores the hypervisors with their tags, a page
// at a time, and returns those stored as the parents of the property-mapping
// phase, each keyed by its ARN. A hypervisor's Name and Status are stored as
// listed, empty included. It reports whether a write failed, after which the
// scan must stop.
func scanBackupGatewayHypervisors(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, backupGatewayListHypervisorsOp, "hypervisors", "HypervisorArn",
		func(ctx context.Context, token *string) ([]bgwtypes.Hypervisor, *string, error) {
			out, err := client.ListHypervisors(ctx, &backupgateway.ListHypervisorsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Hypervisors, out.NextToken, nil
		},
		func(h bgwtypes.Hypervisor) (*store.Resource, string, string) {
			name, status := sv(h.Name), string(h.State)
			return arnRow("hypervisors", h.HypervisorArn, h.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupGatewayHypervisor, NativeID: sv(h.HypervisorArn),
				Name: &name, Region: &region, Status: &status, AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		},
		func(h bgwtypes.Hypervisor) string { return sv(h.HypervisorArn) },
		tags, p)
}

// scanBackupGatewayGateways stores the gateways with their tags, a page at a
// time, and returns those stored as the parents of the bandwidth-schedule
// phase, each keyed by its ARN. It reports whether a write failed, after
// which the scan must stop.
func scanBackupGatewayGateways(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, backupGatewayListGatewaysOp, "gateways", "GatewayArn",
		func(ctx context.Context, token *string) ([]bgwtypes.Gateway, *string, error) {
			out, err := client.ListGateways(ctx, &backupgateway.ListGatewaysInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Gateways, out.NextToken, nil
		},
		func(gw bgwtypes.Gateway) (*store.Resource, string, string) {
			return arnRow("gateways", gw.GatewayArn, gw.GatewayDisplayName,
				backupGatewayNamedRow(acct, region, scanID, TypeBackupGatewayGateway, sv(gw.GatewayArn), sv(gw.GatewayDisplayName), gw))
		},
		func(gw bgwtypes.Gateway) string { return sv(gw.GatewayArn) },
		tags, p)
}

// scanBackupGatewayVirtualMachines stores the virtual machines of every
// hypervisor with their tags, a page at a time. It reports whether a write
// failed, after which the scan must stop.
func scanBackupGatewayVirtualMachines(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, backupGatewayListVirtualMachinesOp,
		func(ctx context.Context, token *string) ([]bgwtypes.VirtualMachine, *string, error) {
			out, err := client.ListVirtualMachines(ctx, &backupgateway.ListVirtualMachinesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualMachines, out.NextToken, nil
		},
		func(vm bgwtypes.VirtualMachine) (*store.Resource, string, string) {
			return arnRow("virtual machines", vm.ResourceArn, vm.Name,
				backupGatewayNamedRow(acct, region, scanID, TypeBackupGatewayVirtualMachine, sv(vm.ResourceArn), sv(vm.Name), vm))
		},
		tags, p)
}

// backupGatewayNamedRow is the row of a gateway or virtual machine stored
// under arn and named name, or arn when name is empty.
func backupGatewayNamedRow(acct *account, region, scanID, typ, arn, name string, element any) *store.Resource {
	label := cmp.Or(name, arn)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

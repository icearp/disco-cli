package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/snowdevicemanagement"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSnowDeviceManagementManagedDevice, Service: "snow-device-management", Upstream: "AWS::snow-device-management::managed-device", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSnowDeviceManagementTask, Service: "snow-device-management", Upstream: "AWS::snow-device-management::task", Leaf: true})
	registerService(serviceEntry{
		name: "aws:snow-device-management",
		fn:   scanSnowDeviceManagement,
	})
}

type snowDeviceManagementAPI interface {
	ListDevices(context.Context, *snowdevicemanagement.ListDevicesInput, ...func(*snowdevicemanagement.Options)) (*snowdevicemanagement.ListDevicesOutput, error)
	ListTasks(context.Context, *snowdevicemanagement.ListTasksInput, ...func(*snowdevicemanagement.Options)) (*snowdevicemanagement.ListTasksOutput, error)
}

// scanSnowDeviceManagement discovers Snow Device Management managed devices
// and remote-management tasks.
func scanSnowDeviceManagement(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := snowdevicemanagement.NewFromConfig(acct.cfg, func(o *snowdevicemanagement.Options) { o.Region = region })

	t, i, ferr := scanSnowDevices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSnowTasks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSnowDevices(ctx context.Context, client snowDeviceManagementAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := snowdevicemanagement.NewListDevicesPaginator(client, &snowdevicemanagement.ListDevicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// "No devices found in this region" is an empty-state 403, not a real
			// deny — the service works, just with zero devices.
			if isAPIErrorWithMessage(err, "AccessDeniedException", "No devices found") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "snow-device-management:ListDevices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("snow-device-management:ListDevices: %w", err)
		}
		for _, d := range out.Devices {
			arn := sv(d.ManagedDeviceArn)
			if arn == "" {
				continue
			}
			label := sv(d.ManagedDeviceId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSnowDeviceManagementManagedDevice, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "snow-device-management managed-devices")
}

func scanSnowTasks(ctx context.Context, client snowDeviceManagementAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := snowdevicemanagement.NewListTasksPaginator(client, &snowdevicemanagement.ListTasksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// "No devices found in this region" is an empty-state 403, not a real
			// deny — the service works, just with zero devices/tasks.
			if isAPIErrorWithMessage(err, "AccessDeniedException", "No devices found") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "snow-device-management:ListTasks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("snow-device-management:ListTasks: %w", err)
		}
		for _, t := range out.Tasks {
			arn := sv(t.TaskArn)
			if arn == "" {
				continue
			}
			label := sv(t.TaskId)
			status := string(t.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSnowDeviceManagementTask, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "snow-device-management tasks")
}

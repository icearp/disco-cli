package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeviceFarmProject, Service: "devicefarm", Redact: []redact.Rule{{Path: "EnvironmentVariables[*].Value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeDeviceFarmDevicePool, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmNetworkProfile, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmInstanceProfile, Service: "devicefarm", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDeviceFarmDeviceInstance, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmVPCEConfiguration, Service: "devicefarm", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDeviceFarmTestGridProject, Service: "devicefarm", Upstream: "AWS::devicefarm::testgrid-project"})
	registerService(serviceEntry{
		name:   "aws:devicefarm",
		fn:     scanDeviceFarm,
		global: true, // Device Farm has a single regional endpoint: us-west-2.
	})
}

type deviceFarmAPI interface {
	ListProjects(context.Context, *devicefarm.ListProjectsInput, ...func(*devicefarm.Options)) (*devicefarm.ListProjectsOutput, error)
	ListDevicePools(context.Context, *devicefarm.ListDevicePoolsInput, ...func(*devicefarm.Options)) (*devicefarm.ListDevicePoolsOutput, error)
	ListNetworkProfiles(context.Context, *devicefarm.ListNetworkProfilesInput, ...func(*devicefarm.Options)) (*devicefarm.ListNetworkProfilesOutput, error)
	ListInstanceProfiles(context.Context, *devicefarm.ListInstanceProfilesInput, ...func(*devicefarm.Options)) (*devicefarm.ListInstanceProfilesOutput, error)
	ListDeviceInstances(context.Context, *devicefarm.ListDeviceInstancesInput, ...func(*devicefarm.Options)) (*devicefarm.ListDeviceInstancesOutput, error)
	ListVPCEConfigurations(context.Context, *devicefarm.ListVPCEConfigurationsInput, ...func(*devicefarm.Options)) (*devicefarm.ListVPCEConfigurationsOutput, error)
	ListTestGridProjects(context.Context, *devicefarm.ListTestGridProjectsInput, ...func(*devicefarm.Options)) (*devicefarm.ListTestGridProjectsOutput, error)
}

// scanDeviceFarm discovers Device Farm projects (with device pools and network
// profiles), account-level instance profiles, device instances, VPCE configs,
// and Selenium test-grid projects. Not scanned: device catalog; test runs and
// their job/suite/test/sample/artifact/session children; uploaded app packages
// (catalog / ephemeral / content).
func scanDeviceFarm(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-west-2"
	client := devicefarm.NewFromConfig(acct.cfg, func(o *devicefarm.Options) { o.Region = region })

	projectARNs, t, i, ferr := scanDeviceFarmProjects(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, parn := range projectARNs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) {
				return scanDeviceFarmDevicePools(ctx, client, acct, region, st, scanID, parn)
			},
			func() (int, int, error) {
				return scanDeviceFarmNetworkProfiles(ctx, client, acct, region, st, scanID, parn)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDeviceFarmInstanceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDeviceFarmDeviceInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanDeviceFarmVPCEConfigurations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanDeviceFarmTestGridProjects(ctx, client, acct, region, st, scanID) },
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

func scanDeviceFarmProjects(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	pager := devicefarm.NewListProjectsPaginator(client, &devicefarm.ListProjectsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "devicefarm:ListProjects", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("devicefarm:ListProjects: %w", perr)
		}
		for _, p := range out.Projects {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmProject, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "devicefarm projects")
	return arns, t, i, err
}

func scanDeviceFarmDevicePools(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID, projectARN string) (int, int, error) {
	var batch []*store.Resource
	pager := devicefarm.NewListDevicePoolsPaginator(client, &devicefarm.ListDevicePoolsInput{Arn: &projectARN})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListDevicePools", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListDevicePools: %w", perr)
		}
		for _, p := range out.DevicePools {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmDevicePool, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "devicefarm device-pools")
}

func scanDeviceFarmNetworkProfiles(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID, projectARN string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListNetworkProfiles(ctx, &devicefarm.ListNetworkProfilesInput{Arn: &projectARN, NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListNetworkProfiles", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListNetworkProfiles: %w", perr)
		}
		for _, p := range out.NetworkProfiles {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmNetworkProfile, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "devicefarm network-profiles")
}

func scanDeviceFarmInstanceProfiles(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListInstanceProfiles(ctx, &devicefarm.ListInstanceProfilesInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListInstanceProfiles", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListInstanceProfiles: %w", perr)
		}
		for _, p := range out.InstanceProfiles {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmInstanceProfile, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "devicefarm instance-profiles")
}

func scanDeviceFarmDeviceInstances(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListDeviceInstances(ctx, &devicefarm.ListDeviceInstancesInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListDeviceInstances", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListDeviceInstances: %w", perr)
		}
		for _, d := range out.DeviceInstances {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmDeviceInstance, NativeID: arn,
				Name: &arn, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "devicefarm device-instances")
}

func scanDeviceFarmVPCEConfigurations(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListVPCEConfigurations(ctx, &devicefarm.ListVPCEConfigurationsInput{NextToken: token})
		if perr != nil {
			// VPCE configs are allowlist opt-in; non-allowlisted accounts get
			// ServiceAccountException — expected state, not an error. Silent
			// skip keeps the rest of devicefarm scanning.
			if isAPIErrorWithMessage(perr, "ServiceAccountException", "not allowlisted") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListVPCEConfigurations", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListVPCEConfigurations: %w", perr)
		}
		for _, c := range out.VpceConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			name := sv(c.VpceConfigurationName)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmVPCEConfiguration, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "devicefarm vpce-configurations")
}

func scanDeviceFarmTestGridProjects(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := devicefarm.NewListTestGridProjectsPaginator(client, &devicefarm.ListTestGridProjectsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "devicefarm:ListTestGridProjects", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("devicefarm:ListTestGridProjects: %w", perr)
		}
		for _, p := range out.TestGridProjects {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			name := sv(p.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeviceFarmTestGridProject, NativeID: arn,
				Name: &name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "devicefarm testgrid-projects")
}

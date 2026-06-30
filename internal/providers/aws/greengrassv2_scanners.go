package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/greengrassv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:greengrass-v2",
		fn:   scanGreengrassV2,
		emits: []coverage.TypeDecl{
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2ComponentVersion, Leaf: true},
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2Deployment},
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2Component, Leaf: true},
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2CoreDevice, Leaf: true},
		},
	})
}

type greengrassV2API interface {
	ListComponents(context.Context, *greengrassv2.ListComponentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentsOutput, error)
	ListComponentVersions(context.Context, *greengrassv2.ListComponentVersionsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentVersionsOutput, error)
	ListCoreDevices(context.Context, *greengrassv2.ListCoreDevicesInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListCoreDevicesOutput, error)
	ListDeployments(context.Context, *greengrassv2.ListDeploymentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListDeploymentsOutput, error)
}

// greengrassV2Regions enumerates the AWS Regions where IoT Greengrass V2
// control-plane operations are available. Superset of v1: adds
// ap-southeast-5, ca-central-1, eu-south-2. Same 429-on-unsupported-region
// behaviour as v1; allowlist short-circuits the SDK retry burn.
// Source: https://docs.aws.amazon.com/general/latest/gr/greengrassv2.html
var greengrassV2Regions = map[string]bool{
	"us-east-1": true, "us-east-2": true, "us-west-2": true,
	"ap-south-1":     true,
	"ap-northeast-1": true, "ap-northeast-2": true,
	"ap-southeast-1": true, "ap-southeast-2": true, "ap-southeast-5": true,
	"ca-central-1": true,
	"eu-central-1": true,
	"eu-west-1":    true, "eu-west-2": true,
	"eu-south-2":    true,
	"cn-north-1":    true,
	"us-gov-east-1": true, "us-gov-west-1": true,
}

// scanGreengrassV2 discovers Greengrass v2 components (catalog artifacts),
// their component versions, core devices, and deployments.
func scanGreengrassV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if !greengrassV2Regions[region] {
		return 0, 0, nil
	}
	client := greengrassv2.NewFromConfig(acct.cfg, func(o *greengrassv2.Options) { o.Region = region })

	// ListComponents drives both the component rows and the per-component
	// version fan-out — list once, reuse the ARN set.
	compArns, t, i, ferr := scanGGV2Components(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanGGV2ComponentVersions(ctx, client, compArns, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanGGV2CoreDevices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanGGV2Deployments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanGGV2Components upserts a component row per component and returns the
// component ARN set for the version fan-out.
func scanGGV2Components(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var compArns []string
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListComponents(ctx, &greengrassv2.ListComponentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "greengrass-v2:ListComponents", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("greengrass-v2:ListComponents: %w", err)
		}
		for _, c := range out.Components {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			compArns = append(compArns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGreengrassV2Component, NativeID: arn,
				Name: c.ComponentName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "greengrass-v2 components")
	return compArns, t, i, err
}

func scanGGV2ComponentVersions(ctx context.Context, client greengrassV2API, compArns []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, c := range compArns {
		compArn := c
		var vToken *string
		for {
			vout, err := client.ListComponentVersions(ctx, &greengrassv2.ListComponentVersionsInput{
				Arn:       &compArn,
				NextToken: vToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "greengrass-v2:ListComponentVersions", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("greengrass-v2:ListComponentVersions c=%s: %w", compArn, err)
			}
			for _, v := range vout.ComponentVersions {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				label := fmt.Sprintf("%s:%s", sv(v.ComponentName), sv(v.ComponentVersion))
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeGreengrassV2ComponentVersion, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
			if vout.NextToken == nil || *vout.NextToken == "" {
				break
			}
			vToken = vout.NextToken
		}
	}
	return upsertBatch(st, batch, "greengrass-v2 component-versions")
}

// scanGGV2CoreDevices discovers Greengrass v2 core devices. ListCoreDevices
// summaries carry the IoT thing name and status but no ARN, so synthesize the
// canonical coreDevices ARN from the thing name.
func scanGGV2CoreDevices(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCoreDevices(ctx, &greengrassv2.ListCoreDevicesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "greengrass-v2:ListCoreDevices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("greengrass-v2:ListCoreDevices: %w", err)
		}
		for _, d := range out.CoreDevices {
			thing := sv(d.CoreDeviceThingName)
			if thing == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:greengrass:%s:%s:coreDevices:%s", region, acct.ID, thing)
			name := thing
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGreengrassV2CoreDevice, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "greengrass-v2 core-devices")
}

func scanGGV2Deployments(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDeployments(ctx, &greengrassv2.ListDeploymentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "greengrass-v2:ListDeployments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("greengrass-v2:ListDeployments: %w", err)
		}
		for _, d := range out.Deployments {
			id := sv(d.DeploymentId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:greengrass:%s:%s:deployments:%s", region, acct.ID, id)
			status := string(d.DeploymentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGreengrassV2Deployment, NativeID: arn,
				Name: d.DeploymentName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "greengrass-v2 deployments")
}

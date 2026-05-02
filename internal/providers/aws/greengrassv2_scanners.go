package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/greengrassv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:greengrass-v2",
		fn:   scanGreengrassV2,
		emits: []coverage.TypeDecl{
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2ComponentVersion},
			{Service: "greengrass-v2", DiscoType: TypeGreengrassV2Deployment},
		},
	})
}

type greengrassV2API interface {
	ListComponents(context.Context, *greengrassv2.ListComponentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentsOutput, error)
	ListComponentVersions(context.Context, *greengrassv2.ListComponentVersionsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListComponentVersionsOutput, error)
	ListDeployments(context.Context, *greengrassv2.ListDeploymentsInput, ...func(*greengrassv2.Options)) (*greengrassv2.ListDeploymentsOutput, error)
}

// scanGreengrassV2 discovers Greengrass v2 component versions (per
// component) and deployments.
func scanGreengrassV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := greengrassv2.NewFromConfig(acct.cfg, func(o *greengrassv2.Options) { o.Region = region })

	t, i, ferr := scanGGV2ComponentVersions(ctx, client, acct, region, st, scanID)
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

func scanGGV2ComponentVersions(ctx context.Context, client greengrassV2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var compArns []string
	var nextToken *string
	for {
		out, err := client.ListComponents(ctx, &greengrassv2.ListComponentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "greengrass-v2:ListComponents", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("greengrass-v2:ListComponents: %w", err)
		}
		for _, c := range out.Components {
			if a := sv(c.Arn); a != "" {
				compArns = append(compArns, a)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

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

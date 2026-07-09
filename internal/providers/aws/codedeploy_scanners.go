package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeDeployApplication, Service: "codedeploy", Upstream: "AWS::CodeDeploy::Application", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCodeDeployDeploymentGroup, Service: "codedeploy", Upstream: "AWS::CodeDeploy::DeploymentGroup"})
	registerType(restype.Descriptor{Type: TypeCodeDeployDeploymentConfig, Service: "codedeploy", Upstream: "AWS::CodeDeploy::DeploymentConfig", Leaf: true})
	registerService(serviceEntry{
		name: "aws:codedeploy",
		fn:   scanCodeDeploy,
	})
}

type codeDeployAPI interface {
	ListApplications(context.Context, *codedeploy.ListApplicationsInput, ...func(*codedeploy.Options)) (*codedeploy.ListApplicationsOutput, error)
	BatchGetApplications(context.Context, *codedeploy.BatchGetApplicationsInput, ...func(*codedeploy.Options)) (*codedeploy.BatchGetApplicationsOutput, error)
	ListDeploymentGroups(context.Context, *codedeploy.ListDeploymentGroupsInput, ...func(*codedeploy.Options)) (*codedeploy.ListDeploymentGroupsOutput, error)
	BatchGetDeploymentGroups(context.Context, *codedeploy.BatchGetDeploymentGroupsInput, ...func(*codedeploy.Options)) (*codedeploy.BatchGetDeploymentGroupsOutput, error)
	ListDeploymentConfigs(context.Context, *codedeploy.ListDeploymentConfigsInput, ...func(*codedeploy.Options)) (*codedeploy.ListDeploymentConfigsOutput, error)
	GetDeploymentConfig(context.Context, *codedeploy.GetDeploymentConfigInput, ...func(*codedeploy.Options)) (*codedeploy.GetDeploymentConfigOutput, error)
}

// scanCodeDeploy discovers applications, deployment groups (per app), and
// deployment configs (built-in CodeDeployDefault.* flagged ManagedByProvider).
func scanCodeDeploy(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codedeploy.NewFromConfig(acct.cfg, func(o *codedeploy.Options) { o.Region = region })

	appNames, t, i, ferr := scanCDApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCDDeploymentGroups(ctx, client, appNames, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCDDeploymentConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCDApplications(ctx context.Context, client codeDeployAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var names []string
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &codedeploy.ListApplicationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "codedeploy:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("codedeploy:ListApplications: %w", err)
		}
		names = append(names, out.Applications...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var batch []*store.Resource
	for i := 0; i < len(names); i += 100 {
		end := i + 100
		if end > len(names) {
			end = len(names)
		}
		chunk := names[i:end]
		out, err := client.BatchGetApplications(ctx, &codedeploy.BatchGetApplicationsInput{ApplicationNames: chunk})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "codedeploy:BatchGetApplications", acct.ID, region, err)
				continue
			}
			return nil, 0, 0, fmt.Errorf("codedeploy:BatchGetApplications: %w", err)
		}
		for _, a := range out.ApplicationsInfo {
			name := sv(a.ApplicationName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:codedeploy:%s:%s:application:%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeDeployApplication, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "codedeploy applications")
	return names, t, i, err
}

func scanCDDeploymentGroups(ctx context.Context, client codeDeployAPI, appNames []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, app := range appNames {
		appName := app
		var groupNames []string
		var nextToken *string
		for {
			out, err := client.ListDeploymentGroups(ctx, &codedeploy.ListDeploymentGroupsInput{
				ApplicationName: &appName,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "codedeploy:ListDeploymentGroups", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("codedeploy:ListDeploymentGroups app=%s: %w", appName, err)
			}
			groupNames = append(groupNames, out.DeploymentGroups...)
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
		for i := 0; i < len(groupNames); i += 100 {
			end := i + 100
			if end > len(groupNames) {
				end = len(groupNames)
			}
			chunk := groupNames[i:end]
			out, err := client.BatchGetDeploymentGroups(ctx, &codedeploy.BatchGetDeploymentGroupsInput{
				ApplicationName:      &appName,
				DeploymentGroupNames: chunk,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "codedeploy:BatchGetDeploymentGroups", acct.ID, region, err)
					continue
				}
				return 0, 0, fmt.Errorf("codedeploy:BatchGetDeploymentGroups app=%s: %w", appName, err)
			}
			for _, g := range out.DeploymentGroupsInfo {
				groupName := sv(g.DeploymentGroupName)
				if groupName == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:codedeploy:%s:%s:deploymentgroup:%s/%s", region, acct.ID, appName, groupName)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCodeDeployDeploymentGroup, NativeID: arn,
					Name: &groupName, Region: &region,
					AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "codedeploy deployment-groups")
}

func scanCDDeploymentConfigs(ctx context.Context, client codeDeployAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var names []string
	var nextToken *string
	for {
		out, err := client.ListDeploymentConfigs(ctx, &codedeploy.ListDeploymentConfigsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codedeploy:ListDeploymentConfigs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codedeploy:ListDeploymentConfigs: %w", err)
		}
		names = append(names, out.DeploymentConfigsList...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var batch []*store.Resource
	for _, name := range names {
		nm := name
		out, err := client.GetDeploymentConfig(ctx, &codedeploy.GetDeploymentConfigInput{DeploymentConfigName: &nm})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("codedeploy:GetDeploymentConfig %s: %w", nm, err)
		}
		if out.DeploymentConfigInfo == nil {
			continue
		}
		arn := fmt.Sprintf("arn:aws:codedeploy:%s:%s:deploymentconfig:%s", region, acct.ID, nm)
		managed := strings.HasPrefix(nm, "CodeDeployDefault.")
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCodeDeployDeploymentConfig, NativeID: arn,
			Name: &nm, Region: &region,
			AttributesJSON:    mustJSON(out.DeploymentConfigInfo),
			DiscoveredBy:      scanID,
			ManagedByProvider: managed,
		})
	}
	return upsertBatch(st, batch, "codedeploy deployment-configs")
}

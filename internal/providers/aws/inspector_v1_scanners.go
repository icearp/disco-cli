package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/inspector"
)

func init() {
	registerService(serviceEntry{
		name: "aws:inspector",
		fn:   scanInspectorV1,
		emits: []coverage.TypeDecl{
			{Service: "inspector", DiscoType: TypeInspectorAssessmentTarget},
			{Service: "inspector", DiscoType: TypeInspectorAssessmentTemplate},
			{Service: "inspector", DiscoType: TypeInspectorResourceGroup},
		},
	})
}

type inspectorV1API interface {
	ListAssessmentTargets(context.Context, *inspector.ListAssessmentTargetsInput, ...func(*inspector.Options)) (*inspector.ListAssessmentTargetsOutput, error)
	DescribeAssessmentTargets(context.Context, *inspector.DescribeAssessmentTargetsInput, ...func(*inspector.Options)) (*inspector.DescribeAssessmentTargetsOutput, error)
	ListAssessmentTemplates(context.Context, *inspector.ListAssessmentTemplatesInput, ...func(*inspector.Options)) (*inspector.ListAssessmentTemplatesOutput, error)
	DescribeAssessmentTemplates(context.Context, *inspector.DescribeAssessmentTemplatesInput, ...func(*inspector.Options)) (*inspector.DescribeAssessmentTemplatesOutput, error)
	DescribeResourceGroups(context.Context, *inspector.DescribeResourceGroupsInput, ...func(*inspector.Options)) (*inspector.DescribeResourceGroupsOutput, error)
}

// scanInspectorV1 discovers classic Inspector v1 resources: assessment targets,
// templates, and the resource groups attached to targets. Inspector v1 has been
// largely superseded by Inspector v2 (aws:inspector2:*) but legacy data persists
// in tenants that have not migrated. List returns ARNs only — Describe in
// batches of up to 10 fills in detail.
func scanInspectorV1(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := inspector.NewFromConfig(acct.cfg, func(o *inspector.Options) { o.Region = region })
	t1, i1, rgArns, err := scanInspectorTargets(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1, i1, err
	}
	t2, i2, err := scanInspectorTemplates(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1 + t2, i1 + i2, err
	}
	t3, i3, err := scanInspectorResourceGroups(ctx, client, acct, region, st, scanID, rgArns)
	if err != nil {
		return t1 + t2 + t3, i1 + i2 + i3, err
	}
	return t1 + t2 + t3, i1 + i2 + i3, nil
}

// scanInspectorTargets lists assessment-target ARNs, describes them in batches
// of 10, and returns the distinct ResourceGroupArn refs for downstream
// resolution.
func scanInspectorTargets(ctx context.Context, client inspectorV1API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, rgArns []string, err error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListAssessmentTargets(ctx, &inspector.ListAssessmentTargetsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "inspector:ListAssessmentTargets", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("inspector:ListAssessmentTargets: %w", err)
		}
		arns = append(arns, out.AssessmentTargetArns...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(arns) == 0 {
		return 0, 0, nil, nil
	}

	rgSeen := map[string]struct{}{}
	var batch []*store.Resource
	for i := 0; i < len(arns); i += 10 {
		end := i + 10
		if end > len(arns) {
			end = len(arns)
		}
		out, derr := client.DescribeAssessmentTargets(ctx, &inspector.DescribeAssessmentTargetsInput{
			AssessmentTargetArns: arns[i:end],
		})
		if derr != nil {
			if isAccessDenied(derr) {
				return 0, 0, nil, skipIfAccessDenied(st, "inspector:DescribeAssessmentTargets", acct.ID, region, derr)
			}
			return 0, 0, nil, fmt.Errorf("inspector:DescribeAssessmentTargets: %w", derr)
		}
		for _, t := range out.AssessmentTargets {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspectorAssessmentTarget, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
			if rg := sv(t.ResourceGroupArn); rg != "" {
				rgSeen[rg] = struct{}{}
			}
		}
	}
	rgArns = make([]string, 0, len(rgSeen))
	for k := range rgSeen {
		rgArns = append(rgArns, k)
	}
	t, i, err := upsertBatch(st, batch, "inspector assessment-targets")
	return t, i, rgArns, err
}

// scanInspectorTemplates lists template ARNs and describes them in batches of 10.
func scanInspectorTemplates(ctx context.Context, client inspectorV1API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListAssessmentTemplates(ctx, &inspector.ListAssessmentTemplatesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "inspector:ListAssessmentTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("inspector:ListAssessmentTemplates: %w", err)
		}
		arns = append(arns, out.AssessmentTemplateArns...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	var batch []*store.Resource
	for i := 0; i < len(arns); i += 10 {
		end := i + 10
		if end > len(arns) {
			end = len(arns)
		}
		out, derr := client.DescribeAssessmentTemplates(ctx, &inspector.DescribeAssessmentTemplatesInput{
			AssessmentTemplateArns: arns[i:end],
		})
		if derr != nil {
			if isAccessDenied(derr) {
				return 0, 0, skipIfAccessDenied(st, "inspector:DescribeAssessmentTemplates", acct.ID, region, derr)
			}
			return 0, 0, fmt.Errorf("inspector:DescribeAssessmentTemplates: %w", derr)
		}
		for _, t := range out.AssessmentTemplates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspectorAssessmentTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "inspector assessment-templates")
}

// scanInspectorResourceGroups describes resource groups discovered via target
// ResourceGroupArn refs. ResourceGroup has no top-level List op; Describe
// requires explicit ARNs in batches of 10.
func scanInspectorResourceGroups(ctx context.Context, client inspectorV1API, acct *account, region string, st *store.Store, scanID string, rgArns []string) (total, inserted int, err error) {
	if len(rgArns) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for i := 0; i < len(rgArns); i += 10 {
		end := i + 10
		if end > len(rgArns) {
			end = len(rgArns)
		}
		out, derr := client.DescribeResourceGroups(ctx, &inspector.DescribeResourceGroupsInput{
			ResourceGroupArns: rgArns[i:end],
		})
		if derr != nil {
			if isAccessDenied(derr) {
				return 0, 0, skipIfAccessDenied(st, "inspector:DescribeResourceGroups", acct.ID, region, derr)
			}
			return 0, 0, fmt.Errorf("inspector:DescribeResourceGroups: %w", derr)
		}
		for _, g := range out.ResourceGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspectorResourceGroup, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "inspector resource-groups")
}

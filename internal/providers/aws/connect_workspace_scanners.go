package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectTaskTemplate, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectEvaluationForm, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectView, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectViewVersion, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectWorkspace, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectPrompt, Service: "connect"})
}

// connectWorkspaceAPI is the narrow surface used by the Workspace family.
type connectWorkspaceAPI interface {
	ListTaskTemplates(context.Context, *connect.ListTaskTemplatesInput, ...func(*connect.Options)) (*connect.ListTaskTemplatesOutput, error)
	ListEvaluationForms(context.Context, *connect.ListEvaluationFormsInput, ...func(*connect.Options)) (*connect.ListEvaluationFormsOutput, error)
	DescribeEvaluationForm(context.Context, *connect.DescribeEvaluationFormInput, ...func(*connect.Options)) (*connect.DescribeEvaluationFormOutput, error)
	ListViews(context.Context, *connect.ListViewsInput, ...func(*connect.Options)) (*connect.ListViewsOutput, error)
	DescribeView(context.Context, *connect.DescribeViewInput, ...func(*connect.Options)) (*connect.DescribeViewOutput, error)
	ListViewVersions(context.Context, *connect.ListViewVersionsInput, ...func(*connect.Options)) (*connect.ListViewVersionsOutput, error)
	ListWorkspaces(context.Context, *connect.ListWorkspacesInput, ...func(*connect.Options)) (*connect.ListWorkspacesOutput, error)
	DescribeWorkspace(context.Context, *connect.DescribeWorkspaceInput, ...func(*connect.Options)) (*connect.DescribeWorkspaceOutput, error)
	ListPrompts(context.Context, *connect.ListPromptsInput, ...func(*connect.Options)) (*connect.ListPromptsOutput, error)
	DescribePrompt(context.Context, *connect.DescribePromptInput, ...func(*connect.Options)) (*connect.DescribePromptOutput, error)
}

// scanConnectWorkspace runs Workspace-family phases per instance.
func scanConnectWorkspace(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanConnectTaskTemplates(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectEvaluationForms(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanConnectViews(ctx, client, instances, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanConnectViewVersions(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectWorkspaces(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanConnectPrompts(ctx, client, instances, acct, region, st, scanID) },
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

// scanConnectTaskTemplates is list-only (no Describe op); stores
// list-summary metadata as attrs.
func scanConnectTaskTemplates(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.ListTaskTemplates(ctx, &connect.ListTaskTemplatesInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListTaskTemplates", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListTaskTemplates %s: %w", instID, perr)
			}
			for _, m := range out.TaskTemplates {
				arn := sv(m.Arn)
				if arn == "" {
					continue
				}
				name := sv(m.Name)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectTaskTemplate,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(m.CreatedTime),
					AttributesJSON: mustJSON(m),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertConnectBatch(st, batch, "connect task templates")
}

func scanConnectEvaluationForms(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListEvaluationFormsPaginator(client, &connect.ListEvaluationFormsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListEvaluationForms", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListEvaluationForms %s: %w", instID, perr)
			}
			for _, f := range out.EvaluationFormSummaryList {
				if f.EvaluationFormId != nil {
					items = append(items, connectInstanceItem{instID, *f.EvaluationFormId})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeEvaluationForm(gctx, &connect.DescribeEvaluationFormInput{InstanceId: &k.instanceID, EvaluationFormId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeEvaluationForm %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.EvaluationForm == nil {
			return nil, nil
		}
		arn := sv(out.EvaluationForm.EvaluationFormArn)
		if arn == "" {
			return nil, nil
		}
		title := sv(out.EvaluationForm.Title)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectEvaluationForm,
			NativeID:       arn,
			Name:           &title,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect evaluation forms")
}

func scanConnectViews(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	items, err := listViewsByInstance(ctx, client, instances, acct, region, st)
	if err != nil {
		return 0, 0, err
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeView(gctx, &connect.DescribeViewInput{InstanceId: &k.instanceID, ViewId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeView %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.View == nil {
			return nil, nil
		}
		arn := sv(out.View.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.View.Name)
		status := string(out.View.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectView,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect views")
}

func listViewsByInstance(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store) ([]connectInstanceItem, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.ListViews(ctx, &connect.ListViewsInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListViews", acct.ID, region, perr)
					break
				}
				return nil, fmt.Errorf("connect:ListViews %s: %w", instID, perr)
			}
			for _, v := range out.ViewsSummaryList {
				if v.Id != nil {
					items = append(items, connectInstanceItem{instID, *v.Id})
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return items, nil
}

// scanConnectViewVersions — list-only per view (no Describe). Two-stage:
// list views per instance, then list versions per view.
func scanConnectViewVersions(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	views, err := listViewsByInstance(ctx, client, instances, acct, region, st)
	if err != nil {
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, v := range views {
		instID := v.instanceID
		viewID := v.id
		var token *string
		for {
			out, perr := client.ListViewVersions(ctx, &connect.ListViewVersionsInput{InstanceId: &instID, ViewId: &viewID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListViewVersions %s/%s: %w", instID, viewID, perr)
			}
			for _, vv := range out.ViewVersionSummaryList {
				arn := sv(vv.Arn)
				if arn == "" {
					continue
				}
				name := sv(vv.Name)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectViewVersion,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(vv),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertConnectBatch(st, batch, "connect view versions")
}

func scanConnectWorkspaces(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListWorkspacesPaginator(client, &connect.ListWorkspacesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListWorkspaces", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListWorkspaces %s: %w", instID, perr)
			}
			for _, w := range out.WorkspaceSummaryList {
				if w.Id != nil {
					items = append(items, connectInstanceItem{instID, *w.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeWorkspace(gctx, &connect.DescribeWorkspaceInput{InstanceId: &k.instanceID, WorkspaceId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeWorkspace %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.Workspace == nil {
			return nil, nil
		}
		arn := sv(out.Workspace.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.Workspace.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectWorkspace,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect workspaces")
}

func scanConnectPrompts(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListPromptsPaginator(client, &connect.ListPromptsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListPrompts", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListPrompts %s: %w", instID, perr)
			}
			for _, p := range out.PromptSummaryList {
				if p.Id != nil {
					items = append(items, connectInstanceItem{instID, *p.Id})
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribePrompt(gctx, &connect.DescribePromptInput{InstanceId: &k.instanceID, PromptId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribePrompt %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.Prompt == nil {
			return nil, nil
		}
		arn := sv(out.Prompt.PromptARN)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.Prompt.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectPrompt,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect prompts")
}

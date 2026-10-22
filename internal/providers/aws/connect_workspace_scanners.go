package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

// scanConnectWorkspace runs Workspace-family phases per instance. Views are
// listed once: the view-version phase walks the same listing, so a listing
// error is returned once, by the view phase.
func scanConnectWorkspace(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	views := connectListPerInstance(ctx, st, acct, region, "connect:ListViews", instances,
		func(instID string) connectPager[*connect.ListViewsOutput] {
			return connect.NewListViewsPaginator(client, &connect.ListViewsInput{InstanceId: &instID},
				func(o *connect.ListViewsPaginatorOptions) { o.Limit = 100 })
		},
		func(o *connect.ListViewsOutput) []cttypes.ViewSummary { return o.ViewsSummaryList },
		func(s cttypes.ViewSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectRunPhases(st, acct, region,
		connectPhase{"connect task templates", func() (int, int, error) {
			return scanConnectTaskTemplates(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect evaluation forms", func() (int, int, error) {
			return scanConnectEvaluationForms(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect views", func() (int, int, error) {
			return scanConnectViews(ctx, client, views, acct, region, st, scanID)
		}},
		connectPhase{"connect view versions", func() (int, int, error) {
			return scanConnectViewVersions(ctx, client, views.items, acct, region, st, scanID)
		}},
		connectPhase{"connect workspaces", func() (int, int, error) {
			return scanConnectWorkspaces(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect prompts", func() (int, int, error) {
			return scanConnectPrompts(ctx, client, instances, acct, region, st, scanID)
		}},
	)
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
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListEvaluationForms", instances,
		func(instID string) connectPager[*connect.ListEvaluationFormsOutput] {
			return connect.NewListEvaluationFormsPaginator(client, &connect.ListEvaluationFormsInput{InstanceId: &instID},
				func(o *connect.ListEvaluationFormsPaginatorOptions) { o.Limit = 100 })
		},
		func(o *connect.ListEvaluationFormsOutput) []cttypes.EvaluationFormSummary {
			return o.EvaluationFormSummaryList
		},
		func(s cttypes.EvaluationFormSummary) (string, string) {
			return sv(s.EvaluationFormId), sv(s.EvaluationFormArn)
		})
	return connectDescribe[cttypes.EvaluationFormSummary]{
		rtype: TypeConnectEvaluationForm, op: "connect:DescribeEvaluationForm", label: "connect evaluation forms",
		describe: func(ctx context.Context, it connectListed[cttypes.EvaluationFormSummary], r *store.Resource) error {
			out, err := client.DescribeEvaluationForm(ctx, &connect.DescribeEvaluationFormInput{InstanceId: &it.instanceID, EvaluationFormId: &it.id})
			if err != nil {
				return err
			}
			f := out.EvaluationForm
			if f == nil {
				return errConnectNoBody
			}
			title := sv(f.Title)
			r.Name = &title
			r.TagsJSON = mapTagsJSON(f.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.EvaluationFormSummary) {
			title := sv(s.Title)
			r.Name = &title
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectViews(ctx context.Context, client connectWorkspaceAPI, listed *connectListing[cttypes.ViewSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return connectDescribe[cttypes.ViewSummary]{
		rtype: TypeConnectView, op: "connect:DescribeView", label: "connect views",
		describe: func(ctx context.Context, it connectListed[cttypes.ViewSummary], r *store.Resource) error {
			out, err := client.DescribeView(ctx, &connect.DescribeViewInput{InstanceId: &it.instanceID, ViewId: &it.id})
			if err != nil {
				return err
			}
			v := out.View
			if v == nil {
				return errConnectNoBody
			}
			name, status := sv(v.Name), string(v.Status)
			r.Name, r.Status = &name, &status
			r.TagsJSON = mapTagsJSON(v.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.ViewSummary) {
			name, status := sv(s.Name), string(s.Status)
			r.Name, r.Status = &name, &status
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectViewVersions — list-only per view (no Describe): lists the
// versions of each view scanConnectWorkspace listed.
func scanConnectViewVersions(ctx context.Context, client connectWorkspaceAPI, views []connectListed[cttypes.ViewSummary], acct *account, region string, st *store.Store, scanID string) (int, int, error) {
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
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListWorkspaces", instances,
		func(instID string) connectPager[*connect.ListWorkspacesOutput] {
			return connect.NewListWorkspacesPaginator(client, &connect.ListWorkspacesInput{InstanceId: &instID},
				func(o *connect.ListWorkspacesPaginatorOptions) { o.Limit = 100 })
		},
		func(o *connect.ListWorkspacesOutput) []cttypes.WorkspaceSummary { return o.WorkspaceSummaryList },
		func(s cttypes.WorkspaceSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.WorkspaceSummary]{
		rtype: TypeConnectWorkspace, op: "connect:DescribeWorkspace", label: "connect workspaces",
		describe: func(ctx context.Context, it connectListed[cttypes.WorkspaceSummary], r *store.Resource) error {
			out, err := client.DescribeWorkspace(ctx, &connect.DescribeWorkspaceInput{InstanceId: &it.instanceID, WorkspaceId: &it.id})
			if err != nil {
				return err
			}
			w := out.Workspace
			if w == nil {
				return errConnectNoBody
			}
			name := sv(w.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(w.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.WorkspaceSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

func scanConnectPrompts(ctx context.Context, client connectWorkspaceAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListPrompts", instances,
		func(instID string) connectPager[*connect.ListPromptsOutput] {
			return connect.NewListPromptsPaginator(client, &connect.ListPromptsInput{InstanceId: &instID},
				func(o *connect.ListPromptsPaginatorOptions) { o.Limit = 1000 })
		},
		func(o *connect.ListPromptsOutput) []cttypes.PromptSummary { return o.PromptSummaryList },
		func(s cttypes.PromptSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	return connectDescribe[cttypes.PromptSummary]{
		rtype: TypeConnectPrompt, op: "connect:DescribePrompt", label: "connect prompts",
		describe: func(ctx context.Context, it connectListed[cttypes.PromptSummary], r *store.Resource) error {
			out, err := client.DescribePrompt(ctx, &connect.DescribePromptInput{InstanceId: &it.instanceID, PromptId: &it.id})
			if err != nil {
				return err
			}
			p := out.Prompt
			if p == nil {
				return errConnectNoBody
			}
			name := sv(p.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(p.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.PromptSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

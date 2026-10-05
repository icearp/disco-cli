package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// AWS Migration Hub Orchestrator — migration workflows, the templates they
// instantiate from, the step groups and steps of both, and the on-premises
// plugins registered with the service. Step groups are contained by their
// workflow or template, steps by their step group; no edges to other types.
func init() {
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorWorkflow, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorTemplate, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorPlugin, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorWorkflowStepGroup, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorWorkflowStep, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorTemplateStepGroup, Service: "migrationhub-orchestrator"})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorTemplateStep, Service: "migrationhub-orchestrator"})
	registerService(serviceEntry{
		name: "aws:migrationhub-orchestrator",
		fn:   scanMigrationHubOrchestrator,
	})
}

type migrationHubOrchestratorAPI interface {
	ListWorkflows(context.Context, *migrationhuborchestrator.ListWorkflowsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowsOutput, error)
	ListTemplates(context.Context, *migrationhuborchestrator.ListTemplatesInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplatesOutput, error)
	ListPlugins(context.Context, *migrationhuborchestrator.ListPluginsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListPluginsOutput, error)
	ListWorkflowStepGroups(context.Context, *migrationhuborchestrator.ListWorkflowStepGroupsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowStepGroupsOutput, error)
	ListWorkflowSteps(context.Context, *migrationhuborchestrator.ListWorkflowStepsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowStepsOutput, error)
	ListTemplateStepGroups(context.Context, *migrationhuborchestrator.ListTemplateStepGroupsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplateStepGroupsOutput, error)
	ListTemplateSteps(context.Context, *migrationhuborchestrator.ListTemplateStepsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplateStepsOutput, error)
}

func scanMigrationHubOrchestrator(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := migrationhuborchestrator.NewFromConfig(acct.cfg, func(o *migrationhuborchestrator.Options) { o.Region = region })
	return scanMHOWithClient(ctx, client, acct, region, st, scanID)
}

// The workflow-steps phase runs last: ListWorkflowSteps tolerates no
// vanished-parent error, so a workflow deleted mid-scan must not cost the
// template children.
func scanMHOWithClient(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	w, wi, workflows, werr := scanMHOWorkflows(ctx, client, acct, region, st, scanID)
	if werr != nil {
		return w, wi, werr
	}
	t, ti, templates, terr := scanMHOTemplates(ctx, client, acct, region, st, scanID)
	total, inserted = w+t, wi+ti
	if terr != nil {
		return total, inserted, terr
	}

	n, i, err := scanMHOPlugins(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += n
	inserted += i

	n, i, workflowGroups, err := scanMHOStepGroups(ctx, st, acct, region, "migrationhub-orchestrator:ListWorkflowStepGroups", workflows,
		func(ctx context.Context, p childParent) ([]*store.Resource, []childParent, error) {
			return listMHOWorkflowStepGroups(ctx, client, acct, region, scanID, p)
		})
	if err != nil {
		return total, inserted, err
	}
	total += n
	inserted += i

	n, i, templateGroups, err := scanMHOStepGroups(ctx, st, acct, region, "migrationhub-orchestrator:ListTemplateStepGroups", templates,
		func(ctx context.Context, p childParent) ([]*store.Resource, []childParent, error) {
			return listMHOTemplateStepGroups(ctx, client, acct, region, scanID, p)
		})
	if err != nil {
		return total, inserted, err
	}
	total += n
	inserted += i

	n, i, err = childFanOut(ctx, st, acct, region, "migrationhub-orchestrator:ListTemplateSteps", templateGroups.parents, isMHONotFound,
		func(ctx context.Context, g childParent) ([]*store.Resource, error) {
			return listMHOTemplateSteps(ctx, client, acct, region, scanID, templateGroups.rootID[g.arn], g)
		})
	if err != nil {
		return total, inserted, err
	}
	total += n
	inserted += i

	n, i, err = childFanOut(ctx, st, acct, region, "migrationhub-orchestrator:ListWorkflowSteps", workflowGroups.parents, mhoNeverSkip,
		func(ctx context.Context, g childParent) ([]*store.Resource, error) {
			return listMHOWorkflowSteps(ctx, client, acct, region, scanID, workflowGroups.rootID[g.arn], g)
		})
	return total + n, inserted + i, err
}

// mhoListErr classifies the two non-fatal shapes the top-level Migration Hub
// Orchestrator list phases share: the Nov-2025 closed-to-new-customers gate
// (not-entitled — the whole service is inert for this account, can't be
// enabled) and the per-region "Unauthorized access denied" outside the
// account's MHO home region (region gap). Returns (handled, out): out is the
// not-entitled sentinel for the former, nil for the latter; (false, nil)
// leaves err for the caller to treat as real.
func mhoListErr(err error) (handled bool, out error) {
	switch {
	case isAccessDeniedWithMessage(err, "no longer open to new customers"):
		return true, markServiceNotEntitled(err)
	case isAPIErrorWithMessage(err, "ValidationException", "Unauthorized access denied"):
		return true, nil
	}
	return false, nil
}

// isMHONotFound matches a workflow, template or step group deleted between
// its list call and its child list call.
func isMHONotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// mhoNeverSkip is the skip predicate for ListWorkflowSteps, which models no
// ResourceNotFoundException. An error code an operation does not model is
// deliberately not tolerated, so a vanished workflow or step group fails the
// phase instead of being guessed at.
func mhoNeverSkip(error) bool { return false }

// scanMHOWorkflows also returns the stored workflows for the step-group phase.
// Workflow summaries carry no ARN, so the synthesized ARN is the type's only
// NativeID source.
func scanMHOWorkflows(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	p := migrationhuborchestrator.NewListWorkflowsPaginator(client, &migrationhuborchestrator.ListWorkflowsInput{}, func(o *migrationhuborchestrator.ListWorkflowsPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if handled, out := mhoListErr(err); handled {
				return 0, 0, nil, out
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub-orchestrator:ListWorkflows", acct.ID, region, err)
				break
			}
			return 0, 0, nil, fmt.Errorf("migrationhub-orchestrator:ListWorkflows: %w", err)
		}
		for _, w := range page.MigrationWorkflowSummary {
			id := sv(w.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:migrationhub-orchestrator:%s:%s:workflow/%s", region, acct.ID, id)
			parents = append(parents, childParent{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMigrationHubOrchestratorWorkflow, NativeID: arn, Name: w.Name, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "migrationhub-orchestrator workflows")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

// scanMHOTemplates also returns the stored templates for the template
// step-group phase. A template needs both its returned Arn (the NativeID) and
// its Id (what every child op takes); a summary missing either is skipped.
func scanMHOTemplates(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	p := migrationhuborchestrator.NewListTemplatesPaginator(client, &migrationhuborchestrator.ListTemplatesInput{}, func(o *migrationhuborchestrator.ListTemplatesPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if handled, out := mhoListErr(err); handled {
				return 0, 0, nil, out
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub-orchestrator:ListTemplates", acct.ID, region, err)
				break
			}
			return 0, 0, nil, fmt.Errorf("migrationhub-orchestrator:ListTemplates: %w", err)
		}
		for _, t := range page.TemplateSummary {
			arn, id := sv(t.Arn), sv(t.Id)
			if arn == "" || id == "" {
				continue
			}
			parents = append(parents, childParent{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMigrationHubOrchestratorTemplate, NativeID: arn, Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "migrationhub-orchestrator templates")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
}

// Plugin summaries carry no ARN and the Service Reference defines no plugin
// resource, so the NativeID follows the synthesized workflow ARN shape.
func scanMHOPlugins(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := migrationhuborchestrator.NewListPluginsPaginator(client, &migrationhuborchestrator.ListPluginsInput{}, func(o *migrationhuborchestrator.ListPluginsPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if handled, out := mhoListErr(err); handled {
				return 0, 0, out
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub-orchestrator:ListPlugins", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("migrationhub-orchestrator:ListPlugins: %w", err)
		}
		for _, pl := range page.Plugins {
			id := sv(pl.PluginId)
			if id == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeMigrationHubOrchestratorPlugin,
				NativeID: fmt.Sprintf("arn:aws:migrationhub-orchestrator:%s:%s:plugin/%s", region, acct.ID, id),
				Name:     pl.Hostname, Region: &region,
				AttributesJSON: mustJSON(pl), DiscoveredBy: scanID,
			}
			if status := string(pl.Status); status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "migrationhub-orchestrator plugins")
}

// mhoStepGroups are the step groups a step-group phase stored, as step-phase
// parents (id = step group id, arn = its NativeID). Step ops also need the
// workflow or template id: rootID maps a step group's NativeID to it, since a
// step group id is only unique within its workflow or template. Read-only
// once the phase has returned.
type mhoStepGroups struct {
	parents []childParent
	rootID  map[string]string
}

// scanMHOStepGroups runs a step-group phase through childFanOut and collects
// the step groups of every parent whose list call succeeded: a parent skipped
// or denied (even mid-pagination) stores no rows, so its groups are not
// handed on either.
func scanMHOStepGroups(ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	list func(context.Context, childParent) ([]*store.Resource, []childParent, error),
) (int, int, mhoStepGroups, error) {
	var mu sync.Mutex
	groups := mhoStepGroups{rootID: map[string]string{}}
	total, inserted, err := childFanOut(ctx, st, acct, region, op, parents, isMHONotFound,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, found, err := list(ctx, p)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			for _, g := range found {
				groups.parents = append(groups.parents, g)
				groups.rootID[g.arn] = p.id
			}
			return rows, nil
		})
	if err != nil {
		return 0, 0, mhoStepGroups{}, err
	}
	return total, inserted, groups, nil
}

func mhoChildRow(acct *account, region, scanID, rtype, nativeID string, name *string, status string, attrs any) *store.Resource {
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: name, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
	if status != "" {
		r.Status = &status
	}
	return r
}

func listMHOWorkflowStepGroups(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region, scanID string, w childParent) ([]*store.Resource, []childParent, error) {
	p := migrationhuborchestrator.NewListWorkflowStepGroupsPaginator(client,
		&migrationhuborchestrator.ListWorkflowStepGroupsInput{WorkflowId: &w.id},
		func(o *migrationhuborchestrator.ListWorkflowStepGroupsPaginatorOptions) { o.Limit = 100 })
	var rows []*store.Resource
	var groups []childParent
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, g := range page.WorkflowStepGroupsSummary {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			nid := w.arn + "/step-group/" + id
			rows = append(rows, mhoChildRow(acct, region, scanID, TypeMigrationHubOrchestratorWorkflowStepGroup, nid, g.Name, string(g.Status), g))
			groups = append(groups, childParent{id: id, arn: nid})
		}
	}
	return rows, groups, nil
}

func listMHOTemplateStepGroups(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region, scanID string, t childParent) ([]*store.Resource, []childParent, error) {
	p := migrationhuborchestrator.NewListTemplateStepGroupsPaginator(client,
		&migrationhuborchestrator.ListTemplateStepGroupsInput{TemplateId: &t.id},
		func(o *migrationhuborchestrator.ListTemplateStepGroupsPaginatorOptions) { o.Limit = 100 })
	var rows []*store.Resource
	var groups []childParent
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, g := range page.TemplateStepGroupSummary {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			nid := t.arn + "/step-group/" + id
			rows = append(rows, mhoChildRow(acct, region, scanID, TypeMigrationHubOrchestratorTemplateStepGroup, nid, g.Name, "", g))
			groups = append(groups, childParent{id: id, arn: nid})
		}
	}
	return rows, groups, nil
}

func listMHOWorkflowSteps(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region, scanID, workflowID string, g childParent) ([]*store.Resource, error) {
	p := migrationhuborchestrator.NewListWorkflowStepsPaginator(client,
		&migrationhuborchestrator.ListWorkflowStepsInput{WorkflowId: &workflowID, StepGroupId: &g.id},
		func(o *migrationhuborchestrator.ListWorkflowStepsPaginatorOptions) { o.Limit = 100 })
	var rows []*store.Resource
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.WorkflowStepsSummary {
			id := sv(s.StepId)
			if id == "" {
				continue
			}
			rows = append(rows, mhoChildRow(acct, region, scanID, TypeMigrationHubOrchestratorWorkflowStep, g.arn+"/step/"+id, s.Name, string(s.Status), s))
		}
	}
	return rows, nil
}

func listMHOTemplateSteps(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region, scanID, templateID string, g childParent) ([]*store.Resource, error) {
	p := migrationhuborchestrator.NewListTemplateStepsPaginator(client,
		&migrationhuborchestrator.ListTemplateStepsInput{TemplateId: &templateID, StepGroupId: &g.id},
		func(o *migrationhuborchestrator.ListTemplateStepsPaginatorOptions) { o.Limit = 100 })
	var rows []*store.Resource
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.TemplateStepSummaryList {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			rows = append(rows, mhoChildRow(acct, region, scanID, TypeMigrationHubOrchestratorTemplateStep, g.arn+"/step/"+id, s.Name, "", s))
		}
	}
	return rows, nil
}

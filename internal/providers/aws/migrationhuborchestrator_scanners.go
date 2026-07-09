package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator"
)

// AWS Migration Hub Orchestrator — migration workflows and the templates they
// instantiate from. Both leaf: no outbound edges to other scanned AWS types.
func init() {
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorWorkflow, Service: "migrationhub-orchestrator", Upstream: "AWS::migrationhub-orchestrator::workflow", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMigrationHubOrchestratorTemplate, Service: "migrationhub-orchestrator", Upstream: "AWS::migrationhub-orchestrator::template", Leaf: true})
	registerService(serviceEntry{
		name: "aws:migrationhub-orchestrator",
		fn:   scanMigrationHubOrchestrator,
	})
}

type migrationHubOrchestratorAPI interface {
	ListWorkflows(context.Context, *migrationhuborchestrator.ListWorkflowsInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowsOutput, error)
	ListTemplates(context.Context, *migrationhuborchestrator.ListTemplatesInput, ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplatesOutput, error)
}

func scanMigrationHubOrchestrator(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := migrationhuborchestrator.NewFromConfig(acct.cfg, func(o *migrationhuborchestrator.Options) { o.Region = region })
	w, wi, werr := scanMHOWorkflows(ctx, client, acct, region, st, scanID)
	if werr != nil {
		return w, wi, werr
	}
	t, ti, terr := scanMHOTemplates(ctx, client, acct, region, st, scanID)
	return w + t, wi + ti, terr
}

// mhoListErr classifies the two non-fatal shapes both Migration Hub Orchestrator
// list phases share: the Nov-2025 closed-to-new-customers gate (not-entitled —
// the whole service is inert for this account, can't be enabled) and the
// per-region "Unauthorized access denied" outside the account's MHO home
// region (region gap). Returns (handled, out): out is the not-entitled
// sentinel for the former, nil for the latter; (false, nil) leaves err for
// the caller to treat as real.
func mhoListErr(err error) (handled bool, out error) {
	switch {
	case isAccessDeniedWithMessage(err, "no longer open to new customers"):
		return true, markServiceNotEntitled(err)
	case isAPIErrorWithMessage(err, "ValidationException", "Unauthorized access denied"):
		return true, nil
	}
	return false, nil
}

func scanMHOWorkflows(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := migrationhuborchestrator.NewListWorkflowsPaginator(client, &migrationhuborchestrator.ListWorkflowsInput{}, func(o *migrationhuborchestrator.ListWorkflowsPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if handled, out := mhoListErr(err); handled {
				return 0, 0, out
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub-orchestrator:ListWorkflows", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("migrationhub-orchestrator:ListWorkflows: %w", err)
		}
		for _, w := range page.MigrationWorkflowSummary {
			id := sv(w.Id)
			if id == "" {
				continue
			}
			// Workflow summaries carry no ARN; synthesize the canonical NativeID.
			arn := fmt.Sprintf("arn:aws:migrationhub-orchestrator:%s:%s:workflow/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMigrationHubOrchestratorWorkflow, NativeID: arn, Name: w.Name, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "migrationhub-orchestrator workflows")
}

func scanMHOTemplates(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := migrationhuborchestrator.NewListTemplatesPaginator(client, &migrationhuborchestrator.ListTemplatesInput{}, func(o *migrationhuborchestrator.ListTemplatesPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if handled, out := mhoListErr(err); handled {
				return 0, 0, out
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "migrationhub-orchestrator:ListTemplates", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("migrationhub-orchestrator:ListTemplates: %w", err)
		}
		for _, t := range page.TemplateSummary {
			arn := sv(t.Arn)
			if arn == "" {
				if id := sv(t.Id); id != "" {
					arn = fmt.Sprintf("arn:aws:migrationhub-orchestrator:%s:%s:template/%s", region, acct.ID, id)
				} else {
					continue
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMigrationHubOrchestratorTemplate, NativeID: arn, Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "migrationhub-orchestrator templates")
}

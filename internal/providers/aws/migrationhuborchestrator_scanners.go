package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator"
)

// AWS Migration Hub Orchestrator — migration workflows and the templates they
// instantiate from. Both leaf: no outbound edges to other scanned AWS types.
func init() {
	registerService(serviceEntry{
		name: "aws:migrationhub-orchestrator",
		fn:   scanMigrationHubOrchestrator,
		emits: []coverage.TypeDecl{
			{Service: "migrationhub-orchestrator", DiscoType: TypeMigrationHubOrchestratorWorkflow, Leaf: true},
			{Service: "migrationhub-orchestrator", DiscoType: TypeMigrationHubOrchestratorTemplate, Leaf: true},
		},
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

func scanMHOWorkflows(ctx context.Context, client migrationHubOrchestratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := migrationhuborchestrator.NewListWorkflowsPaginator(client, &migrationhuborchestrator.ListWorkflowsInput{}, func(o *migrationhuborchestrator.ListWorkflowsPaginatorOptions) {
		o.Limit = 100
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			// Migration Hub closed to new customers (Nov 2025); the whole service
			// is inert for this account and can't be enabled. The sentinel halts
			// the sibling templates phase too.
			if isAccessDeniedWithMessage(err, "no longer open to new customers") {
				return 0, 0, markServiceNotEntitled(err)
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

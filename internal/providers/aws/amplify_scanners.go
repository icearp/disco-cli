package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/amplify"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAmplifyApp, Service: "amplify"})
	registerType(restype.Descriptor{Type: TypeAmplifyBranch, Service: "amplify"})
	registerType(restype.Descriptor{Type: TypeAmplifyDomain, Service: "amplify", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAmplifyWebhooks, Service: "amplify", Upstream: "AWS::amplify::webhooks", Leaf: true, Redact: []redact.Rule{{Path: "WebhookUrl", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "aws:amplify",
		fn:   scanAmplify,
	})
}

type amplifyAPI interface {
	ListApps(context.Context, *amplify.ListAppsInput, ...func(*amplify.Options)) (*amplify.ListAppsOutput, error)
	ListBranches(context.Context, *amplify.ListBranchesInput, ...func(*amplify.Options)) (*amplify.ListBranchesOutput, error)
	ListDomainAssociations(context.Context, *amplify.ListDomainAssociationsInput, ...func(*amplify.Options)) (*amplify.ListDomainAssociationsOutput, error)
	ListWebhooks(context.Context, *amplify.ListWebhooksInput, ...func(*amplify.Options)) (*amplify.ListWebhooksOutput, error)
}

func scanAmplify(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := amplify.NewFromConfig(acct.cfg, func(o *amplify.Options) { o.Region = region })
	return scanAmplifyAll(ctx, client, acct, region, st, scanID)
}

// scanAmplifyAll runs phase 1 (ListApps) then per-app fan-out for branches,
// domain associations, and webhooks. Branch, Domain, and Webhook rows are
// hierarchy-closure-wired to their parent app.
func scanAmplifyAll(ctx context.Context, client amplifyAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	type appRef struct{ id, arn string }
	var apps []appRef

	pAppsPage := amplify.NewListAppsPaginator(client, &amplify.ListAppsInput{})
	for pAppsPage.HasMorePages() {
		page, err := pAppsPage.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "amplify:ListApps", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("amplify:ListApps: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Apps))
		for _, a := range page.Apps {
			arn := sv(a.AppArn)
			if arn == "" {
				continue
			}
			id := sv(a.AppId)
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAmplifyApp,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.CreateTime),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
			apps = append(apps, appRef{id: id, arn: arn})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert amplify apps: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	if len(apps) == 0 {
		return total, inserted, nil
	}

	// Phase 2: per-app branches + domain associations, concurrent.
	var (
		mu         sync.Mutex
		childBatch []*store.Resource
		pairs      [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, app := range apps {
		g.Go(func() error {
			parentID := store.ResourceID("aws", acct.ID, app.arn)
			pBranches := amplify.NewListBranchesPaginator(client, &amplify.ListBranchesInput{AppId: &app.id})
			for pBranches.HasMorePages() {
				bp, berr := pBranches.NextPage(gctx)
				if berr != nil {
					if isAccessDenied(berr) {
						_ = skipIfAccessDenied(st, "amplify:ListBranches", acct.ID, region, berr)
						break
					}
					return fmt.Errorf("amplify:ListBranches %s: %w", app.id, berr)
				}
				for _, br := range bp.Branches {
					arn := sv(br.BranchArn)
					if arn == "" {
						continue
					}
					name := sv(br.BranchName)
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeAmplifyBranch,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						CreatedAt:      tp(br.CreateTime),
						AttributesJSON: mustJSON(br),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					childBatch = append(childBatch, r)
					pairs = append(pairs, [2]string{r.ID, parentID})
					mu.Unlock()
				}
			}
			pDomains := amplify.NewListDomainAssociationsPaginator(client, &amplify.ListDomainAssociationsInput{AppId: &app.id})
			for pDomains.HasMorePages() {
				dp, derr := pDomains.NextPage(gctx)
				if derr != nil {
					if isAccessDenied(derr) {
						_ = skipIfAccessDenied(st, "amplify:ListDomainAssociations", acct.ID, region, derr)
						break
					}
					return fmt.Errorf("amplify:ListDomainAssociations %s: %w", app.id, derr)
				}
				for _, d := range dp.DomainAssociations {
					arn := sv(d.DomainAssociationArn)
					if arn == "" {
						continue
					}
					name := sv(d.DomainName)
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeAmplifyDomain,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						AttributesJSON: mustJSON(d),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					childBatch = append(childBatch, r)
					pairs = append(pairs, [2]string{r.ID, parentID})
					mu.Unlock()
				}
			}
			whRows, werr := listAmplifyWebhooksForApp(gctx, client, acct, region, scanID, app.id, st)
			if werr != nil {
				return werr
			}
			mu.Lock()
			for _, r := range whRows {
				childBatch = append(childBatch, r)
				pairs = append(pairs, [2]string{r.ID, parentID})
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return total, inserted, err
	}
	if len(childBatch) > 0 {
		n, err := st.UpsertResources(childBatch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert amplify children: %w", err)
		}
		total += len(childBatch)
		inserted += n
		// pairs[i][0] was empty when appended (UpsertResources populates r.ID);
		// rebuild from childBatch[i].ID — pairs[i][1] (parentID) is already set.
		idPairs := make([][2]string, len(childBatch))
		for i, r := range childBatch {
			idPairs[i] = [2]string{r.ID, pairs[i][1]}
		}
		if err := st.RecordHierarchyBatch(idPairs); err != nil {
			return total, inserted, fmt.Errorf("closure amplify children: %w", err)
		}
	}
	return total, inserted, nil
}

// listAmplifyWebhooksForApp returns one app's webhook rows via manual NextToken
// pagination (ListWebhooks has no SDK paginator). AccessDenied soft-skips,
// returning rows collected so far; caller closure-wires each webhook to its
// parent app.
func listAmplifyWebhooksForApp(ctx context.Context, client amplifyAPI, acct *account, region, scanID, appID string, st *store.Store) ([]*store.Resource, error) {
	var rows []*store.Resource
	var token *string
	for {
		wp, werr := client.ListWebhooks(ctx, &amplify.ListWebhooksInput{AppId: &appID, NextToken: token})
		if werr != nil {
			if isAccessDenied(werr) {
				_ = skipIfAccessDenied(st, "amplify:ListWebhooks", acct.ID, region, werr)
				return rows, nil
			}
			return nil, fmt.Errorf("amplify:ListWebhooks %s: %w", appID, werr)
		}
		for _, w := range wp.Webhooks {
			arn := sv(w.WebhookArn)
			if arn == "" {
				continue
			}
			name := sv(w.WebhookId)
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAmplifyWebhooks,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(w.CreateTime),
				AttributesJSON: mustJSON(w),
				DiscoveredBy:   scanID,
			})
		}
		if wp.NextToken == nil {
			return rows, nil
		}
		token = wp.NextToken
	}
}

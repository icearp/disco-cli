package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
	"github.com/icearp/disco-cli/store"
)

// dzFanout classifies list errors for one op across a per-parent fan-out so a
// denied or vanished parent does not stop its siblings.
type dzFanout struct {
	st     *store.Store
	acct   *account
	region string
	op     string
	// notFoundModeled is set only for ops whose SDK error set models
	// ResourceNotFoundException; tolerating an unmodeled code would hide a
	// real failure.
	notFoundModeled bool
	warned          bool
}

// skip returns nil when the caller should move on to the next parent, and the
// wrapped error otherwise. AccessDenied warns once per op, not once per parent.
func (f *dzFanout) skip(parent string, err error) error {
	switch {
	case f.notFoundModeled && isAPIErrorCode(err, "ResourceNotFoundException"):
		return nil
	case isAccessDenied(err):
		if !f.warned {
			f.warned = true
			_ = skipIfAccessDenied(f.st, f.op, f.acct.ID, f.region, err)
		}
		return nil
	default:
		return fmt.Errorf("%s %s: %w", f.op, parent, err)
	}
}

// dzCollect drains one parent's pages. A skippable error ends this parent and
// keeps the items already read.
func dzCollect[T any](ctx context.Context, f *dzFanout, parent string, hasMore func() bool, next func(context.Context) ([]T, error)) ([]T, error) {
	var items []T
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			return items, f.skip(parent, err)
		}
		items = append(items, page...)
	}
	return items, nil
}

func dzResource(acct *account, region, scanID, typ, nativeID, name string, attrs any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}

func dzStatus(r *store.Resource, status string) {
	if status != "" {
		r.Status = &status
	}
}

func scanDataZoneAccountPools(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListAccountPools"}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListAccountPoolsPaginator(client, &datazone.ListAccountPoolsInput{DomainIdentifier: &d.id})
		items, err := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.AccountPoolSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		if err != nil {
			return 0, 0, err
		}
		for _, p := range items {
			id := sv(p.Id)
			if id == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, dzResource(acct, region, scanID, TypeDataZoneAccountPool, dzARN(region, acct.ID, d.id, "account-pool", id), label, p))
		}
	}
	return upsertBatch(st, batch, "datazone account-pools")
}

// scanDataZoneEnvironmentBlueprints lists both custom and AWS-managed
// blueprints: the Managed filter selects one population per call, and only the
// managed pass is provider-owned. The managed pass runs first so a blueprint
// both passes return is stored once, as managed.
func scanDataZoneEnvironmentBlueprints(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironmentBlueprints", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		seen := map[string]bool{}
		for _, managed := range []bool{true, false} {
			pager := datazone.NewListEnvironmentBlueprintsPaginator(client, &datazone.ListEnvironmentBlueprintsInput{DomainIdentifier: &d.id, Managed: &managed})
			items, err := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.EnvironmentBlueprintSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			if err != nil {
				return 0, 0, err
			}
			for _, b := range items {
				id := sv(b.Id)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				label := sv(b.Name)
				if label == "" {
					label = id
				}
				r := dzResource(acct, region, scanID, TypeDataZoneEnvironmentBlueprint, dzARN(region, acct.ID, d.id, "environment-blueprint", id), label, b)
				r.CreatedAt = tp(b.CreatedAt)
				r.ManagedByProvider = managed
				batch = append(batch, r)
			}
		}
	}
	return upsertBatch(st, batch, "datazone environment-blueprints")
}

func scanDataZoneNotebooks(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListNotebooks"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, projectID := range d.projectIDs {
			pager := datazone.NewListNotebooksPaginator(client, &datazone.ListNotebooksInput{DomainIdentifier: &d.id, OwningProjectIdentifier: &projectID})
			items, err := dzCollect(ctx, f, d.id+"/"+projectID, pager.HasMorePages, func(c context.Context) ([]dztypes.NotebookSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			if err != nil {
				return 0, 0, err
			}
			for _, n := range items {
				id := sv(n.Id)
				if id == "" {
					continue
				}
				label := sv(n.Name)
				if label == "" {
					label = id
				}
				r := dzResource(acct, region, scanID, TypeDataZoneNotebook, dzARN(region, acct.ID, d.id, "notebook", id), label, n)
				r.CreatedAt = tp(n.CreatedAt)
				dzStatus(r, string(n.Status))
				batch = append(batch, r)
			}
		}
	}
	return upsertBatch(st, batch, "datazone notebooks")
}

// scanDataZoneRules lists rules per domain unit, the only RuleTargetType.
// IncludeCascaded is set false explicitly (the API leaves its default
// undocumented) so a rule should come back only at the unit it targets; the
// per-domain seen set still keeps one row per rule if the server cascades
// anyway.
func scanDataZoneRules(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListRules", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		seen := map[string]bool{}
		for _, unitID := range d.unitIDs {
			pager := datazone.NewListRulesPaginator(client, &datazone.ListRulesInput{
				DomainIdentifier: &d.id, TargetIdentifier: &unitID, TargetType: dztypes.RuleTargetTypeDomainUnit,
				IncludeCascaded: sdkaws.Bool(false),
			})
			items, err := dzCollect(ctx, f, d.id+"/"+unitID, pager.HasMorePages, func(c context.Context) ([]dztypes.RuleSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			if err != nil {
				return 0, 0, err
			}
			for _, r := range items {
				id := sv(r.Identifier)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				label := sv(r.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, dzResource(acct, region, scanID, TypeDataZoneRule, dzARN(region, acct.ID, d.id, "rule", id), label, r))
			}
		}
	}
	return upsertBatch(st, batch, "datazone rules")
}

// scanDataZoneSubscriptions leaves Status unset, so DataZone returns only
// APPROVED subscriptions; REVOKED and CANCELLED are terminal history, not live
// access.
func scanDataZoneSubscriptions(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListSubscriptions", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListSubscriptionsPaginator(client, &datazone.ListSubscriptionsInput{DomainIdentifier: &d.id})
		items, err := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.SubscriptionSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		if err != nil {
			return 0, 0, err
		}
		for _, s := range items {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			r := dzResource(acct, region, scanID, TypeDataZoneSubscription, dzARN(region, acct.ID, d.id, "subscription", id), id, s)
			r.CreatedAt = tp(s.CreatedAt)
			dzStatus(r, string(s.Status))
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "datazone subscriptions")
}

func scanDataZoneSubscriptionGrants(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListSubscriptionGrants", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListSubscriptionGrantsPaginator(client, &datazone.ListSubscriptionGrantsInput{DomainIdentifier: &d.id})
		items, err := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.SubscriptionGrantSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		if err != nil {
			return 0, 0, err
		}
		for _, g := range items {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			r := dzResource(acct, region, scanID, TypeDataZoneSubscriptionGrant, dzARN(region, acct.ID, d.id, "subscription-grant", id), id, g)
			r.CreatedAt = tp(g.CreatedAt)
			dzStatus(r, string(g.Status))
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "datazone subscription-grants")
}

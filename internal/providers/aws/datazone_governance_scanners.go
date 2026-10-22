package aws

import (
	"context"
	"errors"
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
	what            string // what the op lists, e.g. "projects"
	notFoundModeled bool
	warned          bool
	// errs holds the parents' failures that skip did not tolerate; finish
	// returns them after storing what the other parents listed.
	errs []error
	// unkeyed counts listed items with no id, which cannot be stored.
	unkeyed skipTally
}

// skip returns nil when the caller should move on to the next parent, and the
// wrapped error otherwise. AccessDenied warns once per op, not once per parent.
func (f *dzFanout) skip(parent string, err error) error {
	switch {
	case f.notFoundModeled && isAPIErrorCode(err, "ResourceNotFoundException"):
		return nil
	case isAccessDenied(err):
		f.warnDenied(err)
		return nil
	default:
		return fmt.Errorf("%s: %w", parent, err)
	}
}

// warnDenied warns about a denied call once per op, not once per parent.
func (f *dzFanout) warnDenied(err error) {
	if !f.warned {
		f.warned = true
		_ = skipIfAccessDenied(f.st, f.op, f.acct.ID, f.region, err)
	}
}

// record files one parent's failed call: a code skip tolerates is dropped
// (a denial warns once), any other error is kept for finish, and the fan-out
// moves on to the next parent.
func (f *dzFanout) record(parent string, err error) {
	if e := f.skip(parent, err); e != nil {
		f.errs = append(f.errs, e)
	}
}

// noID counts a listed item with no id under the parent that listed it.
func (f *dzFanout) noID(parent string) {
	f.unkeyed.skipped("under parent "+parent, nil)
}

// finish stores what the fan-out listed, warns once about items with no id,
// and returns the recorded parent failures beside any write error. A failed
// write is the phase's whole outcome, so it warns about nothing else.
func (f *dzFanout) finish(batch []*store.Resource) (int, int, error) {
	total, inserted, err := upsertListed(f.st, batch, f.op, errors.Join(f.errs...))
	if !errors.Is(err, store.ErrStoreWrite) {
		warnNotStored(f.st, f.op, f.acct, f.region, &f.unkeyed, f.what+" have an empty id")
	}
	return total, inserted, err
}

// dzCollect drains one parent's pages, keeping the items read before a failed
// page and recording its error (see record).
func dzCollect[T any](ctx context.Context, f *dzFanout, parent string, hasMore func() bool, next func(context.Context) ([]T, error)) []T {
	var items []T
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			f.record(parent, err)
			return items
		}
		items = append(items, page...)
	}
	return items
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
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListAccountPools", what: "account pools"}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListAccountPoolsPaginator(client, &datazone.ListAccountPoolsInput{DomainIdentifier: &d.id})
		items := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.AccountPoolSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		for _, p := range items {
			id := sv(p.Id)
			if id == "" {
				f.noID(d.id)
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, dzResource(acct, region, scanID, TypeDataZoneAccountPool, dzARN(region, acct.ID, d.id, "account-pool", id), label, p))
		}
	}
	return f.finish(batch)
}

// scanDataZoneEnvironmentBlueprints lists both custom and AWS-managed
// blueprints: the Managed filter selects one population per call, and only the
// managed pass is provider-owned. The managed pass runs first so a blueprint
// both passes return is stored once, as managed.
func scanDataZoneEnvironmentBlueprints(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironmentBlueprints", what: "environment blueprints", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		seen := map[string]bool{}
		for _, managed := range []bool{true, false} {
			pager := datazone.NewListEnvironmentBlueprintsPaginator(client, &datazone.ListEnvironmentBlueprintsInput{DomainIdentifier: &d.id, Managed: &managed})
			items := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.EnvironmentBlueprintSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			for _, b := range items {
				id := sv(b.Id)
				if id == "" {
					f.noID(d.id)
					continue
				}
				if seen[id] {
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
	return f.finish(batch)
}

func scanDataZoneNotebooks(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListNotebooks", what: "notebooks"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, projectID := range d.projectIDs {
			pager := datazone.NewListNotebooksPaginator(client, &datazone.ListNotebooksInput{DomainIdentifier: &d.id, OwningProjectIdentifier: &projectID})
			items := dzCollect(ctx, f, d.id+"/"+projectID, pager.HasMorePages, func(c context.Context) ([]dztypes.NotebookSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			for _, n := range items {
				id := sv(n.Id)
				if id == "" {
					f.noID(d.id)
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
	return f.finish(batch)
}

// scanDataZoneRules lists rules per domain unit, the only RuleTargetType.
// IncludeCascaded is set false explicitly (the API leaves its default
// undocumented) so a rule should come back only at the unit it targets; the
// per-domain seen set still keeps one row per rule if the server cascades
// anyway.
func scanDataZoneRules(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListRules", what: "rules", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		seen := map[string]bool{}
		for _, unitID := range d.unitIDs {
			pager := datazone.NewListRulesPaginator(client, &datazone.ListRulesInput{
				DomainIdentifier: &d.id, TargetIdentifier: &unitID, TargetType: dztypes.RuleTargetTypeDomainUnit,
				IncludeCascaded: sdkaws.Bool(false),
			})
			items := dzCollect(ctx, f, d.id+"/"+unitID, pager.HasMorePages, func(c context.Context) ([]dztypes.RuleSummary, error) {
				out, err := pager.NextPage(c)
				if err != nil {
					return nil, err
				}
				return out.Items, nil
			})
			for _, r := range items {
				id := sv(r.Identifier)
				if id == "" {
					f.noID(d.id)
					continue
				}
				if seen[id] {
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
	return f.finish(batch)
}

// scanDataZoneSubscriptions leaves Status unset, so DataZone returns only
// APPROVED subscriptions; REVOKED and CANCELLED are terminal history, not live
// access.
func scanDataZoneSubscriptions(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListSubscriptions", what: "subscriptions", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListSubscriptionsPaginator(client, &datazone.ListSubscriptionsInput{DomainIdentifier: &d.id})
		items := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.SubscriptionSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		for _, s := range items {
			id := sv(s.Id)
			if id == "" {
				f.noID(d.id)
				continue
			}
			r := dzResource(acct, region, scanID, TypeDataZoneSubscription, dzARN(region, acct.ID, d.id, "subscription", id), id, s)
			r.CreatedAt = tp(s.CreatedAt)
			dzStatus(r, string(s.Status))
			batch = append(batch, r)
		}
	}
	return f.finish(batch)
}

func scanDataZoneSubscriptionGrants(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListSubscriptionGrants", what: "subscription grants", notFoundModeled: true}
	var batch []*store.Resource
	for _, d := range domains {
		pager := datazone.NewListSubscriptionGrantsPaginator(client, &datazone.ListSubscriptionGrantsInput{DomainIdentifier: &d.id})
		items := dzCollect(ctx, f, d.id, pager.HasMorePages, func(c context.Context) ([]dztypes.SubscriptionGrantSummary, error) {
			out, err := pager.NextPage(c)
			if err != nil {
				return nil, err
			}
			return out.Items, nil
		})
		for _, g := range items {
			id := sv(g.Id)
			if id == "" {
				f.noID(d.id)
				continue
			}
			r := dzResource(acct, region, scanID, TypeDataZoneSubscriptionGrant, dzARN(region, acct.ID, d.id, "subscription-grant", id), id, g)
			r.CreatedAt = tp(g.CreatedAt)
			dzStatus(r, string(g.Status))
			batch = append(batch, r)
		}
	}
	return f.finish(batch)
}

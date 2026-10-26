package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataZoneDomain, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneDomainUnit, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneProject, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneProjectProfile, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneProjectMembership, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneGroupProfile, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneUserProfile, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironment, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentProfile, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentActions, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentBlueprintConfiguration, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneDataSource, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneConnection, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneSubscriptionTarget, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneAccountPool, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentBlueprint, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneNotebook, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneRule, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneSubscription, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneSubscriptionGrant, Service: "datazone"})
	registerService(serviceEntry{
		name: "aws:datazone",
		fn:   scanDataZone,
	})
}

// dataZoneAPI — narrow surface of DataZone ops. DataZone is hierarchical:
// most resources require a DomainIdentifier and child collections fan-out
// per (domain, project) or (domain, environment).
type dataZoneAPI interface {
	ListDomains(context.Context, *datazone.ListDomainsInput, ...func(*datazone.Options)) (*datazone.ListDomainsOutput, error)
	GetDomain(context.Context, *datazone.GetDomainInput, ...func(*datazone.Options)) (*datazone.GetDomainOutput, error)
	ListDomainUnitsForParent(context.Context, *datazone.ListDomainUnitsForParentInput, ...func(*datazone.Options)) (*datazone.ListDomainUnitsForParentOutput, error)
	ListProjects(context.Context, *datazone.ListProjectsInput, ...func(*datazone.Options)) (*datazone.ListProjectsOutput, error)
	ListProjectProfiles(context.Context, *datazone.ListProjectProfilesInput, ...func(*datazone.Options)) (*datazone.ListProjectProfilesOutput, error)
	ListProjectMemberships(context.Context, *datazone.ListProjectMembershipsInput, ...func(*datazone.Options)) (*datazone.ListProjectMembershipsOutput, error)
	SearchGroupProfiles(context.Context, *datazone.SearchGroupProfilesInput, ...func(*datazone.Options)) (*datazone.SearchGroupProfilesOutput, error)
	SearchUserProfiles(context.Context, *datazone.SearchUserProfilesInput, ...func(*datazone.Options)) (*datazone.SearchUserProfilesOutput, error)
	ListEnvironments(context.Context, *datazone.ListEnvironmentsInput, ...func(*datazone.Options)) (*datazone.ListEnvironmentsOutput, error)
	ListEnvironmentProfiles(context.Context, *datazone.ListEnvironmentProfilesInput, ...func(*datazone.Options)) (*datazone.ListEnvironmentProfilesOutput, error)
	ListEnvironmentActions(context.Context, *datazone.ListEnvironmentActionsInput, ...func(*datazone.Options)) (*datazone.ListEnvironmentActionsOutput, error)
	ListEnvironmentBlueprintConfigurations(context.Context, *datazone.ListEnvironmentBlueprintConfigurationsInput, ...func(*datazone.Options)) (*datazone.ListEnvironmentBlueprintConfigurationsOutput, error)
	ListDataSources(context.Context, *datazone.ListDataSourcesInput, ...func(*datazone.Options)) (*datazone.ListDataSourcesOutput, error)
	ListConnections(context.Context, *datazone.ListConnectionsInput, ...func(*datazone.Options)) (*datazone.ListConnectionsOutput, error)
	ListSubscriptionTargets(context.Context, *datazone.ListSubscriptionTargetsInput, ...func(*datazone.Options)) (*datazone.ListSubscriptionTargetsOutput, error)
	ListAccountPools(context.Context, *datazone.ListAccountPoolsInput, ...func(*datazone.Options)) (*datazone.ListAccountPoolsOutput, error)
	ListEnvironmentBlueprints(context.Context, *datazone.ListEnvironmentBlueprintsInput, ...func(*datazone.Options)) (*datazone.ListEnvironmentBlueprintsOutput, error)
	ListNotebooks(context.Context, *datazone.ListNotebooksInput, ...func(*datazone.Options)) (*datazone.ListNotebooksOutput, error)
	ListRules(context.Context, *datazone.ListRulesInput, ...func(*datazone.Options)) (*datazone.ListRulesOutput, error)
	ListSubscriptions(context.Context, *datazone.ListSubscriptionsInput, ...func(*datazone.Options)) (*datazone.ListSubscriptionsOutput, error)
	ListSubscriptionGrants(context.Context, *datazone.ListSubscriptionGrantsInput, ...func(*datazone.Options)) (*datazone.ListSubscriptionGrantsOutput, error)
}

// dzDomain holds per-domain identifiers needed for downstream fan-outs.
type dzDomain struct {
	id         string
	rootUnitID string
	unitIDs    []string // root included; filled by the domain-units walk
	projectIDs []string
	envIDs     []string
}

func scanDataZone(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := datazone.NewFromConfig(acct.cfg, func(o *datazone.Options) { o.Region = region })

	// domains holds only the domains stored, so a failed ListDomains or
	// GetDomain call, filed in errs under its own op, still lets the child
	// phases walk them; a failed write stops here.
	var errs phaseErrs
	domains, total, inserted, werr := scanDataZoneDomains(ctx, client, acct, region, st, scanID, &errs)
	if werr != nil || len(domains) == 0 {
		return total, inserted, errors.Join(errs.join(st, acct, region), werr)
	}

	// Every phase runs whatever an earlier one returned, except a failed store
	// write; a phase that fails keeps the rows it read, so a dependent phase
	// walks only the parents that were stored.
	for _, phase := range []struct {
		op   string
		scan func() (int, int, error)
	}{
		{"datazone:ListDomainUnitsForParent", func() (int, int, error) {
			return scanDataZoneDomainUnits(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListProjectProfiles", func() (int, int, error) {
			return scanDataZoneProjectProfiles(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListProjects", func() (int, int, error) {
			return scanDataZoneProjects(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListProjectMemberships", func() (int, int, error) {
			return scanDataZoneProjectMemberships(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:SearchGroupProfiles", func() (int, int, error) {
			return scanDataZoneGroupProfiles(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:SearchUserProfiles", func() (int, int, error) {
			return scanDataZoneUserProfiles(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListEnvironmentProfiles", func() (int, int, error) {
			return scanDataZoneEnvironmentProfiles(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListEnvironmentBlueprintConfigurations", func() (int, int, error) {
			return scanDataZoneEnvironmentBlueprintConfigurations(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListEnvironments", func() (int, int, error) {
			return scanDataZoneEnvironments(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListEnvironmentActions", func() (int, int, error) {
			return scanDataZoneEnvironmentActions(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListSubscriptionTargets", func() (int, int, error) {
			return scanDataZoneSubscriptionTargets(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListDataSources", func() (int, int, error) {
			return scanDataZoneDataSources(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListConnections", func() (int, int, error) {
			return scanDataZoneConnections(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListEnvironmentBlueprints", func() (int, int, error) {
			return scanDataZoneEnvironmentBlueprints(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListRules", func() (int, int, error) {
			return scanDataZoneRules(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListSubscriptions", func() (int, int, error) {
			return scanDataZoneSubscriptions(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListSubscriptionGrants", func() (int, int, error) {
			return scanDataZoneSubscriptionGrants(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListAccountPools", func() (int, int, error) {
			return scanDataZoneAccountPools(ctx, client, acct, region, st, scanID, domains)
		}},
		{"datazone:ListNotebooks", func() (int, int, error) {
			return scanDataZoneNotebooks(ctx, client, acct, region, st, scanID, domains)
		}},
	} {
		t, i, ferr := phase.scan()
		total += t
		inserted += i
		errs.add(phase.op, ferr)
		if errors.Is(ferr, store.ErrStoreWrite) {
			break
		}
	}
	return total, inserted, errs.join(st, acct, region)
}

func dzARN(partition, region, acct, domainID, kind, id string) string {
	return arnFor(partition, "datazone", region, acct, "domain/"+domainID+"/"+kind+"/"+id)
}

const (
	dzListDomainsOp = "datazone:ListDomains"
	dzGetDomainOp   = "datazone:GetDomain"
)

// dzDomainReads tallies the listed domains not stored from their GetDomain
// body, one tally per cause, so that each cause warns once.
type dzDomainReads struct {
	denied  skipTally
	unkeyed skipTally
}

// scanDataZoneDomains stores each listed domain from its GetDomain body, the
// only source of its tags and root domain unit (see dzDomainRow for a failed
// read), and returns the stored domains for the child phases. A ListDomains
// page that fails keeps the domains read before it, if any, warning when it
// was denied and filing any other error in errs under ListDomains; every
// GetDomain error that is neither a denial nor a not-found is filed under
// GetDomain. It returns only a failed write.
func scanDataZoneDomains(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, errs *phaseErrs) ([]*dzDomain, int, int, error) {
	var (
		domains    []*dzDomain
		batch      []*store.Resource
		reads      dzDomainReads
		listDenied error
		nextToken  *string
	)
	// A manual loop, not the SDK paginator: the paginator follows a repeated
	// NextToken forever, or with StopOnDuplicateToken stores a partial list
	// as complete; lastPage returns errRepeatedNextToken instead.
	for {
		out, perr := client.ListDomains(ctx, &datazone.ListDomainsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				listDenied = perr
			} else {
				errs.add(dzListDomainsOp, fmt.Errorf("%s: %w", dzListDomainsOp, perr))
			}
			break
		}
		for _, d := range out.Items {
			row, dd, rerr := dzDomainRow(ctx, client, acct, region, scanID, d, &reads)
			errs.add(dzGetDomainOp, rerr)
			if row != nil {
				batch = append(batch, row)
				domains = append(domains, dd)
			}
		}
		last, terr := lastPage(nextToken, out.NextToken)
		errs.add(dzListDomainsOp, terr)
		if last {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, uerr := upsertBatch(st, batch, "datazone domains")
	if uerr != nil {
		return nil, total, inserted, uerr
	}
	if listDenied != nil {
		_ = skipIfAccessDenied(st, dzListDomainsOp, acct.ID, region, fmt.Errorf(
			"domains on this and later pages not stored: %w", listDenied))
	}
	const kept = "%d domain(s) stored as their ListDomains element, without tags or root domain unit, so their domain units and rules were not listed; first %s: %w"
	if reads.denied.count > 0 {
		_ = skipIfAccessDenied(st, dzGetDomainOp, acct.ID, region, fmt.Errorf(kept, reads.denied.count, reads.denied.first, reads.denied.err))
	}
	warnNotStored(st, dzListDomainsOp, acct, region, &reads.unkeyed, "domains have an empty ARN or id")
	return domains, total, inserted, nil
}

// dzDomainRow builds a listed domain's row and child-phase handle from its
// GetDomain body. A domain deleted since it was listed
// (ResourceNotFoundException) is not stored. A denied GetDomain keeps the
// ListDomains element, which has no tags and no root domain unit, and tallies
// it in reads; any other error, a transient one included, keeps it too and is
// returned, because the domain's units and rules go unlisted. A domain with no
// ARN or id is tallied as unkeyed.
func dzDomainRow(ctx context.Context, client dataZoneAPI, acct *account, region, scanID string, d dztypes.DomainSummary, reads *dzDomainReads) (*store.Resource, *dzDomain, error) {
	arn, id := sv(d.Arn), sv(d.Id)
	if arn == "" || id == "" {
		reads.unkeyed.skipped(cmp.Or(arn, id, sv(d.Name)), nil)
		return nil, nil, nil
	}
	dd := &dzDomain{id: id}
	attrsJSON := mustJSON(d)
	var (
		tags map[string]string
		err  error
	)
	gout, derr := client.GetDomain(ctx, &datazone.GetDomainInput{Identifier: &id})
	switch {
	case derr == nil:
		// Full body carries RootDomainUnitId (downstream walk) and
		// resolver fields (KmsKeyIdentifier, *Role).
		dd.rootUnitID = sv(gout.RootDomainUnitId)
		attrsJSON, tags = mustJSON(gout), gout.Tags
	case isAPIErrorCode(derr, "ResourceNotFoundException"):
		return nil, nil, nil
	case isAccessDenied(derr):
		reads.denied.skipped(id, derr)
	default:
		err = fmt.Errorf("%s %s: %w", dzGetDomainOp, id, derr)
	}
	label := cmp.Or(sv(d.Name), id)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeDataZoneDomain, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: attrsJSON,
		TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, dd, err
}

// scanDataZoneDomainUnits walks the domain unit tree starting at the
// domain's root unit. BFS via ListDomainUnitsForParent.
func scanDataZoneDomainUnits(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListDomainUnitsForParent", what: "domain units"}
	var batch []*store.Resource
	for _, d := range domains {
		if d.rootUnitID == "" {
			continue
		}
		queue := []string{d.rootUnitID}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			d.unitIDs = append(d.unitIDs, parent)
			pager := datazone.NewListDomainUnitsForParentPaginator(client, &datazone.ListDomainUnitsForParentInput{
				DomainIdentifier:           &d.id,
				ParentDomainUnitIdentifier: &parent,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, parent), perr)
					break
				}
				for _, u := range out.Items {
					id := sv(u.Id)
					if id == "" {
						f.noID(d.id)
						continue
					}
					queue = append(queue, id)
					label := sv(u.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneDomainUnit, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "domain-unit", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneProjects(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListProjects", what: "projects"}
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListProjectsPaginator(client, &datazone.ListProjectsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				f.record(d.id, perr)
				break
			}
			for _, p := range out.Items {
				id := sv(p.Id)
				if id == "" {
					f.noID(d.id)
					continue
				}
				d.projectIDs = append(d.projectIDs, id)
				label := sv(p.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneProject, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "project", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneProjectProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListProjectProfiles", what: "project profiles"}
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListProjectProfilesPaginator(client, &datazone.ListProjectProfilesInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				f.record(d.id, perr)
				break
			}
			for _, p := range out.Items {
				id := sv(p.Id)
				if id == "" {
					f.noID(d.id)
					continue
				}
				label := sv(p.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneProjectProfile, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "project-profile", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneProjectMemberships(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListProjectMemberships", what: "project memberships"}
	var batch []*store.Resource
	memberIdx := 0
	for _, d := range domains {
		for _, projectID := range d.projectIDs {
			did := d.id
			pid := projectID
			pager := datazone.NewListProjectMembershipsPaginator(client, &datazone.ListProjectMembershipsInput{
				DomainIdentifier: &did, ProjectIdentifier: &pid,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, projectID), perr)
					break
				}
				for _, m := range out.Members {
					memberIdx++
					id := fmt.Sprintf("%s/membership/%d", projectID, memberIdx)
					label := string(m.Designation)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneProjectMembership, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "project-membership", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneEnvironments(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironments", what: "environments"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, projectID := range d.projectIDs {
			did := d.id
			pid := projectID
			pager := datazone.NewListEnvironmentsPaginator(client, &datazone.ListEnvironmentsInput{
				DomainIdentifier: &did, ProjectIdentifier: &pid,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, projectID), perr)
					break
				}
				for _, e := range out.Items {
					id := sv(e.Id)
					if id == "" {
						f.noID(d.id)
						continue
					}
					d.envIDs = append(d.envIDs, id)
					label := sv(e.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneEnvironment, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "environment", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneEnvironmentProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironmentProfiles", what: "environment profiles"}
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListEnvironmentProfilesPaginator(client, &datazone.ListEnvironmentProfilesInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				f.record(d.id, perr)
				break
			}
			for _, e := range out.Items {
				id := sv(e.Id)
				if id == "" {
					f.noID(d.id)
					continue
				}
				label := sv(e.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneEnvironmentProfile, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "environment-profile", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneEnvironmentActions(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironmentActions", what: "environment actions"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, envID := range d.envIDs {
			did := d.id
			eid := envID
			pager := datazone.NewListEnvironmentActionsPaginator(client, &datazone.ListEnvironmentActionsInput{
				DomainIdentifier: &did, EnvironmentIdentifier: &eid,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, envID), perr)
					break
				}
				for _, a := range out.Items {
					id := sv(a.Id)
					if id == "" {
						f.noID(d.id)
						continue
					}
					label := sv(a.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneEnvironmentActions, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "environment-action", envID+"/"+id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneEnvironmentBlueprintConfigurations(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListEnvironmentBlueprintConfigurations", what: "environment blueprint configurations"}
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListEnvironmentBlueprintConfigurationsPaginator(client, &datazone.ListEnvironmentBlueprintConfigurationsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				f.record(d.id, perr)
				break
			}
			for _, c := range out.Items {
				bpID := sv(c.EnvironmentBlueprintId)
				if bpID == "" {
					f.noID(d.id)
					continue
				}
				name := bpID
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneEnvironmentBlueprintConfiguration, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "environment-blueprint-configuration", bpID),
					Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneSubscriptionTargets(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListSubscriptionTargets", what: "subscription targets"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, envID := range d.envIDs {
			did := d.id
			eid := envID
			pager := datazone.NewListSubscriptionTargetsPaginator(client, &datazone.ListSubscriptionTargetsInput{
				DomainIdentifier: &did, EnvironmentIdentifier: &eid,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, envID), perr)
					break
				}
				for _, s := range out.Items {
					id := sv(s.Id)
					if id == "" {
						f.noID(d.id)
						continue
					}
					label := sv(s.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneSubscriptionTarget, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "subscription-target", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneDataSources(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListDataSources", what: "data sources"}
	var batch []*store.Resource
	for _, d := range domains {
		for _, projectID := range d.projectIDs {
			did := d.id
			pid := projectID
			pager := datazone.NewListDataSourcesPaginator(client, &datazone.ListDataSourcesInput{
				DomainIdentifier: &did, ProjectIdentifier: &pid,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					f.record(fmt.Sprintf("%s/%s", d.id, projectID), perr)
					break
				}
				for _, ds := range out.Items {
					id := sv(ds.DataSourceId)
					if id == "" {
						f.noID(d.id)
						continue
					}
					label := sv(ds.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneDataSource, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "data-source", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(ds), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return f.finish(batch)
}

func scanDataZoneConnections(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	f := &dzFanout{st: st, acct: acct, region: region, op: "datazone:ListConnections", what: "connections"}
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListConnectionsPaginator(client, &datazone.ListConnectionsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				f.record(d.id, perr)
				break
			}
			for _, c := range out.Items {
				id := sv(c.ConnectionId)
				if id == "" {
					f.noID(d.id)
					continue
				}
				label := sv(c.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneConnection, NativeID: dzARN(acct.partition(), region, acct.ID, d.id, "connection", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return f.finish(batch)
}

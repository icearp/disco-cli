package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/datazone"
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
	registerType(restype.Descriptor{Type: TypeDataZoneUserProfile, Service: "datazone", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironment, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentProfile, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentActions, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneEnvironmentBlueprintConfiguration, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneDataSource, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneConnection, Service: "datazone"})
	registerType(restype.Descriptor{Type: TypeDataZoneSubscriptionTarget, Service: "datazone"})
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
}

// dzDomain holds per-domain identifiers needed for downstream fan-outs.
type dzDomain struct {
	id         string
	rootUnitID string
	projectIDs []string
	envIDs     []string
}

func scanDataZone(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := datazone.NewFromConfig(acct.cfg, func(o *datazone.Options) { o.Region = region })

	domains, t, i, ferr := scanDataZoneDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if len(domains) == 0 {
		return total, inserted, nil
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanDataZoneDomainUnits(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneProjectProfiles(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) { return scanDataZoneProjects(ctx, client, acct, region, st, scanID, domains) },
		func() (int, int, error) {
			return scanDataZoneProjectMemberships(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneGroupProfiles(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneUserProfiles(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneEnvironmentProfiles(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneEnvironmentBlueprints(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneEnvironments(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneEnvironmentActions(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneSubscriptionTargets(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneDataSources(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanDataZoneConnections(ctx, client, acct, region, st, scanID, domains)
		},
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

func dzARN(region, acct, domainID, kind, id string) string {
	return fmt.Sprintf("arn:aws:datazone:%s:%s:domain/%s/%s/%s", region, acct, domainID, kind, id)
}

func scanDataZoneDomains(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string) ([]*dzDomain, int, int, error) {
	pager := datazone.NewListDomainsPaginator(client, &datazone.ListDomainsInput{})
	var domains []*dzDomain
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "datazone:ListDomains", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("datazone:ListDomains: %w", perr)
		}
		for _, d := range out.Items {
			arn := sv(d.Arn)
			id := sv(d.Id)
			if arn == "" || id == "" {
				continue
			}
			dd := &dzDomain{id: id}
			attrsJSON := mustJSON(d)
			// Full body carries RootDomainUnitId (downstream walk) and
			// resolver fields (KmsKeyIdentifier, *Role).
			gout, derr := client.GetDomain(ctx, &datazone.GetDomainInput{Identifier: &id})
			if derr == nil {
				dd.rootUnitID = sv(gout.RootDomainUnitId)
				attrsJSON = mustJSON(gout)
			} else if !isAccessDenied(derr) {
				return nil, 0, 0, fmt.Errorf("datazone:GetDomain %s: %w", id, derr)
			}
			domains = append(domains, dd)
			label := sv(d.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataZoneDomain, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "datazone domains")
	return domains, t, i, err
}

// scanDataZoneDomainUnits walks the domain unit tree starting at the
// domain's root unit. BFS via ListDomainUnitsForParent.
func scanDataZoneDomainUnits(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		if d.rootUnitID == "" {
			continue
		}
		queue := []string{d.rootUnitID}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			pager := datazone.NewListDomainUnitsForParentPaginator(client, &datazone.ListDomainUnitsForParentInput{
				DomainIdentifier:           &d.id,
				ParentDomainUnitIdentifier: &parent,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListDomainUnitsForParent %s/%s: %w", d.id, parent, perr)
				}
				for _, u := range out.Items {
					id := sv(u.Id)
					if id == "" {
						continue
					}
					queue = append(queue, id)
					label := sv(u.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneDomainUnit, NativeID: dzARN(region, acct.ID, d.id, "domain-unit", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone domain-units")
}

func scanDataZoneProjects(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListProjectsPaginator(client, &datazone.ListProjectsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("datazone:ListProjects %s: %w", d.id, perr)
			}
			for _, p := range out.Items {
				id := sv(p.Id)
				if id == "" {
					continue
				}
				d.projectIDs = append(d.projectIDs, id)
				label := sv(p.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneProject, NativeID: dzARN(region, acct.ID, d.id, "project", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "datazone projects")
}

func scanDataZoneProjectProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListProjectProfilesPaginator(client, &datazone.ListProjectProfilesInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("datazone:ListProjectProfiles %s: %w", d.id, perr)
			}
			for _, p := range out.Items {
				id := sv(p.Id)
				if id == "" {
					continue
				}
				label := sv(p.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneProjectProfile, NativeID: dzARN(region, acct.ID, d.id, "project-profile", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "datazone project-profiles")
}

func scanDataZoneProjectMemberships(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
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
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListProjectMemberships %s/%s: %w", d.id, projectID, perr)
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
						Type: TypeDataZoneProjectMembership, NativeID: dzARN(region, acct.ID, d.id, "project-membership", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone project-memberships")
}

func scanDataZoneEnvironments(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
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
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListEnvironments %s/%s: %w", d.id, projectID, perr)
				}
				for _, e := range out.Items {
					id := sv(e.Id)
					if id == "" {
						continue
					}
					d.envIDs = append(d.envIDs, id)
					label := sv(e.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneEnvironment, NativeID: dzARN(region, acct.ID, d.id, "environment", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone environments")
}

func scanDataZoneEnvironmentProfiles(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListEnvironmentProfilesPaginator(client, &datazone.ListEnvironmentProfilesInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("datazone:ListEnvironmentProfiles %s: %w", d.id, perr)
			}
			for _, e := range out.Items {
				id := sv(e.Id)
				if id == "" {
					continue
				}
				label := sv(e.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneEnvironmentProfile, NativeID: dzARN(region, acct.ID, d.id, "environment-profile", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "datazone environment-profiles")
}

func scanDataZoneEnvironmentActions(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
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
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListEnvironmentActions %s/%s: %w", d.id, envID, perr)
				}
				for _, a := range out.Items {
					id := sv(a.Id)
					if id == "" {
						continue
					}
					label := sv(a.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneEnvironmentActions, NativeID: dzARN(region, acct.ID, d.id, "environment-action", envID+"/"+id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone environment-actions")
}

func scanDataZoneEnvironmentBlueprints(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListEnvironmentBlueprintConfigurationsPaginator(client, &datazone.ListEnvironmentBlueprintConfigurationsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("datazone:ListEnvironmentBlueprintConfigurations %s: %w", d.id, perr)
			}
			for _, c := range out.Items {
				bpID := sv(c.EnvironmentBlueprintId)
				if bpID == "" {
					continue
				}
				name := bpID
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneEnvironmentBlueprintConfiguration, NativeID: dzARN(region, acct.ID, d.id, "environment-blueprint-configuration", bpID),
					Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "datazone environment-blueprint-configurations")
}

func scanDataZoneSubscriptionTargets(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
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
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListSubscriptionTargets %s/%s: %w", d.id, envID, perr)
				}
				for _, s := range out.Items {
					id := sv(s.Id)
					if id == "" {
						continue
					}
					label := sv(s.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneSubscriptionTarget, NativeID: dzARN(region, acct.ID, d.id, "subscription-target", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone subscription-targets")
}

func scanDataZoneDataSources(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
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
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("datazone:ListDataSources %s/%s: %w", d.id, projectID, perr)
				}
				for _, ds := range out.Items {
					id := sv(ds.DataSourceId)
					if id == "" {
						continue
					}
					label := sv(ds.Name)
					if label == "" {
						label = id
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataZoneDataSource, NativeID: dzARN(region, acct.ID, d.id, "data-source", id),
						Name: &label, Region: &region, AttributesJSON: mustJSON(ds), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "datazone data-sources")
}

func scanDataZoneConnections(ctx context.Context, client dataZoneAPI, acct *account, region string, st *store.Store, scanID string, domains []*dzDomain) (int, int, error) {
	var batch []*store.Resource
	for _, d := range domains {
		did := d.id
		pager := datazone.NewListConnectionsPaginator(client, &datazone.ListConnectionsInput{DomainIdentifier: &did})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("datazone:ListConnections %s: %w", d.id, perr)
			}
			for _, c := range out.Items {
				id := sv(c.ConnectionId)
				if id == "" {
					continue
				}
				label := sv(c.Name)
				if label == "" {
					label = id
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDataZoneConnection, NativeID: dzARN(region, acct.ID, d.id, "connection", id),
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "datazone connections")
}

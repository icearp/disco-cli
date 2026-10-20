package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ssmincidents"
	ssmitypes "github.com/aws/aws-sdk-go-v2/service/ssmincidents/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// ssmIncidentsService is the registered service name. registerService below
// repeats it as a literal because regionsgen reads the name from the literal.
const ssmIncidentsService = "aws:ssm-incidents"

func init() {
	registerType(restype.Descriptor{Type: TypeSSMIncidentsReplicationSet, Service: "ssm-incidents"})
	registerType(restype.Descriptor{Type: TypeSSMIncidentsResponsePlan, Service: "ssm-incidents"})
	registerType(restype.Descriptor{Type: TypeSSMIncidentsResourcePolicy, Service: "ssm-incidents"})
	registerService(serviceEntry{
		name: "aws:ssm-incidents",
		fn:   scanSSMIncidents,
	})
}

type ssmIncidentsAPI interface {
	ListReplicationSets(context.Context, *ssmincidents.ListReplicationSetsInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListReplicationSetsOutput, error)
	GetReplicationSet(context.Context, *ssmincidents.GetReplicationSetInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetReplicationSetOutput, error)
	ListResponsePlans(context.Context, *ssmincidents.ListResponsePlansInput, ...func(*ssmincidents.Options)) (*ssmincidents.ListResponsePlansOutput, error)
	GetResponsePlan(context.Context, *ssmincidents.GetResponsePlanInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetResponsePlanOutput, error)
	GetResourcePolicies(context.Context, *ssmincidents.GetResourcePoliciesInput, ...func(*ssmincidents.Options)) (*ssmincidents.GetResourcePoliciesOutput, error)
}

// scanSSMIncidents discovers Incident Manager replication sets, response plans
// and the resource policies attached to each response plan. Replication set is
// a per-account singleton: ListReplicationSets returns the ARN list,
// GetReplicationSet enriches.
//
// Every type stored here has a region-less identity: the replication-set and
// response-plan ARN formats carry no region, and a resource policy is keyed
// under its plan's ARN. Incident Manager replicates its data to every region in
// the replication set, so each of those regions lists the same rows. Only the
// region ssmIncidentsStoreRegion elects stores them; any other region returns
// after reading the replication set, which is not a failure.
func scanSSMIncidents(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmincidents.NewFromConfig(acct.cfg, func(o *ssmincidents.Options) { o.Region = region })

	stores, blind, t, i, ferr := scanSSMIReplicationSets(ctx, client, acct, region, st, scanID)
	if ferr != nil || !stores {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, plans, ferr := scanSSMIResponsePlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSSMIResourcePolicies(ctx, client, acct, region, st, scanID, plans)
	total += t
	inserted += i
	// Only a region that stored rows warns: a region whose own reads were all
	// denied also elects itself blind, but it has stored nothing to warn about.
	if blind && total > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: ssmIncidentsService, Scope: acct.ID + "/" + region,
			Message: "no replication set RegionMap could be read here, so Incident Manager rows were stored from " + region +
				", the first region ssm-incidents is scanned in; a region that could read the replication set may also store them. " +
				"Grant ssm-incidents:ListReplicationSets and ssm-incidents:GetReplicationSet to elect the storing region from the replication set.",
		})
	}
	return total, inserted, ferr
}

// ssmIReplicationSet pairs a listed replication-set ARN with its
// GetReplicationSet body.
type ssmIReplicationSet struct {
	arn string
	set *ssmitypes.ReplicationSet
}

// scanSSMIReplicationSets reads the replication sets in every region, since
// each region needs their RegionMap to elect the storing region, and stores
// them only in the elected one. stores reports whether region is elected;
// blind, that it was elected without any RegionMap because the replication
// sets could not be read.
func scanSSMIReplicationSets(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (stores, blind bool, total, inserted int, err error) {
	sets, listDenied, err := listSSMIReplicationSets(ctx, client, acct, region, st)
	if err != nil {
		return false, false, 0, 0, err
	}
	// Resource policies ride this gate too, on the assumption that
	// GetResourcePolicies returns a plan's policies for every
	// RamResourceShareRegion from any region. That is unverified: if a region
	// returns only its own share region's policies, the other regions' policies
	// are not listed.
	if ssmIncidentsStoreRegion(sets, acct.dispatchedRegions(ssmIncidentsService)) != region {
		return false, false, 0, 0, nil
	}
	readable := slices.ContainsFunc(sets, func(s ssmIReplicationSet) bool { return s.set != nil })
	blind = !readable && (listDenied || len(sets) > 0)
	batch := make([]*store.Resource, 0, len(sets))
	for _, s := range sets {
		label := s.arn
		row := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSSMIncidentsReplicationSet, NativeID: s.arn,
			Name: &label, Region: &region,
			AttributesJSON: "{}", DiscoveredBy: scanID,
		}
		// A set whose GetReplicationSet was denied is still a listed row; it
		// keeps empty attributes rather than being dropped.
		if s.set != nil {
			row.Status = nonEmptyPtr(string(s.set.Status))
			row.AttributesJSON = mustJSON(s.set)
		}
		batch = append(batch, row)
	}
	total, inserted, err = upsertBatch(st, batch, "ssm-incidents replication-sets")
	return true, blind, total, inserted, err
}

// listSSMIReplicationSets lists the replication sets and reads each body. A set
// whose body is denied is kept with a nil set; a set deleted since listing is
// left out. listDenied reports that the list
// itself was denied.
func listSSMIReplicationSets(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store) (sets []ssmIReplicationSet, listDenied bool, err error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListReplicationSets(ctx, &ssmincidents.ListReplicationSetsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, true, skipIfAccessDenied(st, "ssm-incidents:ListReplicationSets", acct.ID, region, err)
			}
			return nil, false, fmt.Errorf("ssm-incidents:ListReplicationSets: %w", err)
		}
		arns = append(arns, out.ReplicationSetArns...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	deniedWarned := false
	for _, arn := range arns {
		a := arn
		out, err := client.GetReplicationSet(ctx, &ssmincidents.GetReplicationSetInput{Arn: &a})
		if err != nil {
			if isAccessDenied(err) {
				if !deniedWarned {
					_ = skipIfAccessDenied(st, "ssm-incidents:GetReplicationSet", acct.ID, region, err)
					deniedWarned = true
				}
				sets = append(sets, ssmIReplicationSet{arn: a})
				continue
			}
			if isSSMINotFound(err) {
				continue
			}
			return nil, false, fmt.Errorf("ssm-incidents:GetReplicationSet %s: %w", a, err)
		}
		sets = append(sets, ssmIReplicationSet{arn: a, set: out.ReplicationSet})
	}
	return sets, false, nil
}

// ssmIncidentsStoreRegion elects the one region that stores this account's
// Incident Manager rows. GetReplicationSet does not say which region created
// the set (RegionMap entries name no creating region), so the choice is the
// lexically first scanned region that is an ACTIVE member of a replication set;
// failing that, the lexically first scanned member in any state; failing that,
// the lexically first scanned region. scanned must hold only regions the
// service runs in (account.dispatchedRegions), or the elected region stores
// nothing. The result depends only on the sets and the scanned list, never on
// the order in which regions run, so regions agree only when every region reads
// the same replication sets. Under per-region denial they may not: two regions
// can each elect themselves and store the rows twice in one scan.
func ssmIncidentsStoreRegion(sets []ssmIReplicationSet, scanned []string) string {
	status := map[string]ssmitypes.RegionStatus{}
	for _, s := range sets {
		if s.set == nil {
			continue
		}
		for r, info := range s.set.RegionMap {
			if status[r] != ssmitypes.RegionStatusActive {
				status[r] = info.Status
			}
		}
	}
	active := slices.DeleteFunc(slices.Clone(scanned), func(r string) bool { return status[r] != ssmitypes.RegionStatusActive })
	members := slices.DeleteFunc(slices.Clone(scanned), func(r string) bool {
		_, ok := status[r]
		return !ok
	})
	for _, pool := range [][]string{active, members} {
		if len(pool) > 0 {
			return slices.Min(pool)
		}
	}
	return slices.Min(scanned)
}

// scanSSMIResponsePlans stores the response plans and returns them as the
// parents of the resource-policy phase.
func scanSSMIResponsePlans(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var (
		batch []*store.Resource
		plans []childParent
	)
	var nextToken *string
	for {
		out, err := client.ListResponsePlans(ctx, &ssmincidents.ListResponsePlansInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "ssm-incidents:ListResponsePlans", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("ssm-incidents:ListResponsePlans: %w", err)
		}
		for _, p := range out.ResponsePlanSummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			// Enrich with GetResponsePlan body — Engagements (ssm-contacts ARNs)
			// and other refs are not on the list-summary shape. A denied read
			// falls back to the summary; a plan deleted since listing
			// (ResourceNotFoundException, modelled for the op) is skipped.
			attrs := mustJSON(p)
			parn := arn
			gout, gerr := client.GetResponsePlan(ctx, &ssmincidents.GetResponsePlanInput{Arn: &parn})
			switch {
			case gerr == nil:
				attrs = mustJSON(gout)
			case isAccessDenied(gerr):
				_ = skipIfAccessDenied(st, "ssm-incidents:GetResponsePlan", acct.ID, region, gerr)
			case isSSMINotFound(gerr):
				continue
			default:
				return 0, 0, nil, fmt.Errorf("ssm-incidents:GetResponsePlan %s: %w", arn, gerr)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMIncidentsResponsePlan, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
			plans = append(plans, childParent{id: arn, arn: arn})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "ssm-incidents response-plans")
	if err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, plans, nil
}

// ssmIResourcePolicyNativeID keys a policy under its response plan: the API
// returns no ARN for a policy and the service reference defines none, and the
// SDK documents no scope in which a PolicyId is unique.
func ssmIResourcePolicyNativeID(planARN, policyID string) string {
	return planARN + "/resource-policy/" + policyID
}

// isSSMINotFound reports a response plan or replication set deleted since it
// was listed.
func isSSMINotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanSSMIResourcePolicies lists the resource policies that share each response
// plan stored this scan through RAM, and stores each under its plan.
func scanSSMIResourcePolicies(ctx context.Context, client ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string, plans []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ssm-incidents:GetResourcePolicies", plans, isSSMINotFound,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := ssmincidents.NewGetResourcePoliciesPaginator(client, &ssmincidents.GetResourcePoliciesInput{ResourceArn: &p.arn})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, pol := range out.ResourcePolicies {
					id := sv(pol.PolicyId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSMIncidentsResourcePolicy, NativeID: ssmIResourcePolicyNativeID(p.arn, id),
						Name: pol.PolicyId, Region: &region,
						AttributesJSON: mustJSON(pol), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

package aws

import (
	"cmp"
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
// after reading the replication set, which is not a failure. A region that
// lists replication sets but can read none of them elects differently (see
// scanSSMIReplicationSets).
//
// Resource policies ride the same election, on the assumption that
// GetResourcePolicies returns a plan's policies for every
// RamResourceShareRegion from any region. That is unverified: if a region
// returns only its own share region's policies, the other regions' policies
// are not listed.
func scanSSMIncidents(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	clientIn := func(r string) ssmIncidentsAPI {
		return ssmincidents.NewFromConfig(acct.cfg, func(o *ssmincidents.Options) { o.Region = r })
	}
	client := clientIn(region)

	stores, listDenied, t, i, ferr := scanSSMIReplicationSets(ctx, client, clientIn, acct, region, st, scanID)
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
	// A region denied ListReplicationSets elects without any RegionMap. It
	// warns only if it stored rows: a region whose every call was denied also
	// elects itself this way, but has stored nothing to warn about.
	if listDenied && total > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: ssmIncidentsService, Scope: acct.ID + "/" + region,
			Message: "ssm-incidents:ListReplicationSets was denied in " + region + ", so no replication set RegionMap could be read here " +
				"and Incident Manager rows were stored from " + region + ", the lexically first region ssm-incidents is scanned in; " +
				"a region that can read the replication set, or a region that lists but cannot read it, may also store them in this scan. " +
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
// them only in the elected one. stores reports whether region is elected.
// listDenied is meaningful only when stores is true: it reports that
// ListReplicationSets was denied, so region was elected without any RegionMap. clientIn returns a client pinned to the given region,
// for the election's probes. The cases the election still gets wrong are listed
// once, on ssmIncidentsStoreRegion.
//
// A region that lists replication sets but is denied GetReplicationSet on
// every one of them is blind: it knows Incident Manager is in use but not
// where. It elects by ssmIncidentsBlindElection instead, and warns.
func scanSSMIReplicationSets(ctx context.Context, client ssmIncidentsAPI, clientIn func(region string) ssmIncidentsAPI, acct *account, region string, st *store.Store, scanID string) (stores, listDenied bool, total, inserted int, err error) {
	sets, listDenied, err := listSSMIReplicationSets(ctx, client, acct, region, st)
	if err != nil {
		return false, false, 0, 0, err
	}
	scanned := acct.dispatchedRegions(ssmIncidentsService)
	if len(sets) > 0 && !slices.ContainsFunc(sets, func(s ssmIReplicationSet) bool { return s.set != nil }) {
		outcome, other, err := ssmIncidentsBlindElection(ctx, clientIn, sets, scanned, region)
		if err != nil {
			return false, false, 0, 0, err
		}
		reportSSMIBlind(st, acct, region, len(sets), outcome, other)
		if outcome != ssmIBlindSelf {
			return false, false, 0, 0, nil
		}
	} else {
		elected, err := ssmIncidentsStoreRegion(ctx, clientIn, sets, scanned, region)
		if err != nil || elected != region {
			return false, false, 0, 0, err
		}
	}
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
	return true, listDenied, total, inserted, err
}

// reportSSMIBlind warns once for a blind region (see scanSSMIReplicationSets),
// saying which way ssmIncidentsBlindElection went; other is the region that
// decided it, for ssmIBlindReader and ssmIBlindAhead.
func reportSSMIBlind(st *store.Store, acct *account, region string, listed int, outcome ssmIBlindOutcome, other string) {
	cause := fmt.Sprintf("ssm-incidents:GetReplicationSet was denied in %s for all %d replication set(s) ssm-incidents:ListReplicationSets listed", region, listed)
	fix := fmt.Sprintf("Grant ssm-incidents:GetReplicationSet for calls in %s so the storing region is elected from the replication set.", region)
	var msg string
	switch outcome {
	case ssmIBlindReader:
		msg = fmt.Sprintf("Incident Manager rows were not stored from %s: %s, so this region cannot tell which region should store them. "+
			"%s can read the replication set and can elect another region to store them. %s", region, cause, other, fix)
	case ssmIBlindAhead:
		msg = fmt.Sprintf("Incident Manager rows were not stored from %s: %s, so this region cannot tell which region should store them. "+
			"%s, ahead of it in lexical order, lists the replication sets but is also denied GetReplicationSet; "+
			"it, or a region that can read the replication set, can elect the region that stores them. %s", region, cause, other, fix)
	case ssmIBlindSelf:
		msg = fmt.Sprintf("Incident Manager rows were stored from %s without a RegionMap: %s, and no other scanned region could read it. "+
			"%s is the lexically first region that lists the replication sets but is denied GetReplicationSet, so it stores the "+
			"replication-set rows, with empty attributes, and the response plans and resource policies it can list. %s", region, cause, region, fix)
	}
	st.ReportWarning(store.ScanWarning{Provider: "aws", Service: ssmIncidentsService, Scope: acct.ID + "/" + region, Message: msg})
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
// Incident Manager rows, for a region that is not blind (see
// scanSSMIReplicationSets). GetReplicationSet does not say which region created
// the set (RegionMap entries name no creating region), so the candidates are
// the scanned regions in ssmIncidentsCandidates order. scanned must hold only
// regions the service runs in (account.dispatchedRegions), region among them.
//
// A region that read a set probes each candidate ahead of itself
// (probeSSMIReplicationSet) and elects the first that can read the set, or
// itself, so each candidate is probed at most once and region never probes
// itself. A region that read no set (ListReplicationSets denied, or none
// listed) has nothing to probe with and elects the first candidate, the
// lexically first scanned region.
//
// The answer depends only on the sets, the scanned list and each candidate's
// own permissions, never on the order in which regions run, so every reading
// region elects the same candidate, and every blind region stays out when a
// reading region exists (ssmIncidentsBlindElection). Known gaps, where the rows are stored
// twice, or partly, or not at all in one scan:
//   - Probes of one candidate disagree between regions (a permission change
//     during the scan): two regions can each elect a different candidate and
//     store the rows twice, or elect one that then stays out, and store nothing.
//   - A probe reads one ARN. A candidate that can read some sets but not that
//     one probes as blind, or as unable to store, though it reads a RegionMap.
//   - A candidate allowed GetReplicationSet but denied ListReplicationSets, or
//     listing none (whether AWS answers that way is unverified), probes as
//     readable, to a reading and to a blind region alike, so both stay out of
//     its way. It then elects the lexically first scanned region without
//     probing, so it stores only if it is that region itself, and then only the
//     plans and policies it can list, with no replication-set row: it listed
//     none.
//   - A region denied ListReplicationSets, listing no set (a region outside
//     the replication set, say), or keeping no set (every GetReplicationSet
//     not found) elects the lexically first scanned region without probing; if
//     that is itself and it can list response plans, it stores them beside the
//     region the others elect, twice in one scan.
//   - A probe error other than a denial or a not-found, a throttle that
//     outlasts the retries included, fails the probing region's scan and is
//     recorded as a scan error; if that region was to store the rows, they are
//     not stored this scan.
//   - A blind region stays out because a probe found a reader, or a blind
//     region ahead of it; if that region's scan, or the scan of the region it
//     elects, then fails, nothing is stored this scan, and the failure is
//     recorded as a scan error.
func ssmIncidentsStoreRegion(ctx context.Context, clientIn func(region string) ssmIncidentsAPI, sets []ssmIReplicationSet, scanned []string, region string) (string, error) {
	candidates, self, err := ssmIncidentsCandidatesFor(sets, scanned, region)
	if err != nil {
		return "", err
	}
	var readable []string
	for _, s := range sets {
		if s.set != nil {
			readable = append(readable, s.arn)
		}
	}
	if len(readable) == 0 {
		return candidates[0], nil
	}
	// Every region probes with the same ARN, whatever order ListReplicationSets
	// returned, so a resource-scoped policy gives every region the same answer.
	probeARN := slices.Min(readable)
	for _, c := range candidates[:self] {
		p, err := probeSSMIReplicationSet(ctx, clientIn, c, region, probeARN)
		if err != nil {
			return "", err
		}
		if p == ssmIProbeReadable {
			return c, nil
		}
	}
	return region, nil
}

// ssmIBlindOutcome is how ssmIncidentsBlindElection decided.
type ssmIBlindOutcome int

const (
	ssmIBlindReader ssmIBlindOutcome = iota // another region can read the set; region stays out
	ssmIBlindAhead                          // a blind region is ahead of region; region stays out
	ssmIBlindSelf                           // no region can read the set and none ahead is blind; region stores
)

// ssmIncidentsBlindElection elects for a blind region (see
// scanSSMIReplicationSets), in ssmIncidentsCandidates order, which for a blind
// region is lexical order. The rule it implements: if any scanned region can
// read the set, the readers elect and every blind region stays out; otherwise
// the first blind candidate stores. It probes with one of region's listed ARNs:
//   - first the candidates ahead of region, stopping at the first that is
//     readable (ssmIBlindReader) or blind (ssmIBlindAhead), and staying out;
//   - if none ahead is either, the candidates after region, stopping at the
//     first readable one (ssmIBlindReader) and staying out;
//   - if none after is readable, region stores (ssmIBlindSelf).
//
// This elects the same region as probing every candidate. A readable candidate
// ahead means some region reads the set, so readers elect and region stays
// out. A blind candidate ahead means region is not the first blind candidate:
// if no region reads the set, that earlier one stores, and if one does, readers
// elect; either way region stays out. With neither ahead, region is the first
// blind candidate, so it stores exactly when no candidate after it can read the
// set. Other answers (a candidate that cannot store) leave the walk going, so
// region probes each other candidate at most once. scanned must hold region.
func ssmIncidentsBlindElection(ctx context.Context, clientIn func(region string) ssmIncidentsAPI, sets []ssmIReplicationSet, scanned []string, region string) (outcome ssmIBlindOutcome, other string, err error) {
	candidates, self, err := ssmIncidentsCandidatesFor(sets, scanned, region)
	if err != nil {
		return 0, "", err
	}
	arns := make([]string, 0, len(sets))
	for _, s := range sets {
		arns = append(arns, s.arn)
	}
	// The same ARN from every region, as in ssmIncidentsStoreRegion.
	probeARN := slices.Min(arns)
	for _, c := range candidates[:self] {
		p, err := probeSSMIReplicationSet(ctx, clientIn, c, region, probeARN)
		switch {
		case err != nil:
			return 0, "", err
		case p == ssmIProbeReadable:
			return ssmIBlindReader, c, nil
		case p == ssmIProbeBlind:
			return ssmIBlindAhead, c, nil
		}
	}
	for _, c := range candidates[self+1:] {
		p, err := probeSSMIReplicationSet(ctx, clientIn, c, region, probeARN)
		if err != nil {
			return 0, "", err
		}
		if p == ssmIProbeReadable {
			return ssmIBlindReader, c, nil
		}
	}
	return ssmIBlindSelf, "", nil
}

// ssmIncidentsCandidatesFor is ssmIncidentsCandidates plus region's index in
// it. region missing from scanned is an error: every region the service is
// dispatched to is in scanned, so it would mean the dispatcher and
// account.dispatchedRegions disagree, and no election could include region.
func ssmIncidentsCandidatesFor(sets []ssmIReplicationSet, scanned []string, region string) ([]string, int, error) {
	candidates := ssmIncidentsCandidates(sets, scanned)
	self := slices.Index(candidates, region)
	if self < 0 {
		return nil, 0, fmt.Errorf("ssm-incidents: %s is not among the regions the service is dispatched to (%v), so it cannot take part in the storing-region election", region, scanned)
	}
	return candidates, self, nil
}

// ssmIProbe is what probeSSMIReplicationSet learnt about a candidate region.
type ssmIProbe int

const (
	ssmIProbeReadable    ssmIProbe = iota // the candidate can read the set
	ssmIProbeBlind                        // the candidate lists sets but is denied GetReplicationSet
	ssmIProbeCannotStore                  // the candidate neither reads the set nor lists any
)

// probeSSMIReplicationSet reads arn with GetReplicationSet in candidate's
// region, for an election run in from. A denied read alone does not make the
// candidate blind: blind means it lists sets too, and a region denied every
// call (an SCP region deny, say) lists none and can store nothing, so a denial
// is followed by one ListReplicationSets page there. A page with a NextToken
// counts as listing sets even if it holds no ARN, as the region's own
// paginated listing would go on to find them. A ResourceNotFoundException
// from GetReplicationSet (modelled for the op) also means the candidate cannot
// store the set: it was deleted during the scan, or the candidate does not hold
// it, for example as a member that is not ACTIVE. Any other error is returned:
// it says nothing about whether the candidate can store the rows, and electing
// past it or into it would both rest on a guess.
func probeSSMIReplicationSet(ctx context.Context, clientIn func(region string) ssmIncidentsAPI, candidate, from, arn string) (ssmIProbe, error) {
	client := clientIn(candidate)
	_, err := client.GetReplicationSet(ctx, &ssmincidents.GetReplicationSetInput{Arn: &arn})
	switch {
	case err == nil:
		return ssmIProbeReadable, nil
	case isSSMINotFound(err):
		return ssmIProbeCannotStore, nil
	case !isAccessDenied(err):
		return 0, fmt.Errorf("ssm-incidents:GetReplicationSet probe of %s from %s: %w", candidate, from, err)
	}
	out, err := client.ListReplicationSets(ctx, &ssmincidents.ListReplicationSetsInput{})
	switch {
	case err == nil && (len(out.ReplicationSetArns) > 0 || sv(out.NextToken) != ""):
		return ssmIProbeBlind, nil
	case err == nil || isAccessDenied(err):
		return ssmIProbeCannotStore, nil
	default:
		return 0, fmt.Errorf("ssm-incidents:ListReplicationSets probe of %s from %s: %w", candidate, from, err)
	}
}

// ssmIncidentsCandidates orders scanned for election: ACTIVE members of any
// replication set first, then members in any other state, then regions in no
// set, each group in lexical order.
func ssmIncidentsCandidates(sets []ssmIReplicationSet, scanned []string) []string {
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
	group := func(r string) int {
		st, member := status[r]
		switch {
		case st == ssmitypes.RegionStatusActive:
			return 0
		case member:
			return 1
		default:
			return 2
		}
	}
	candidates := slices.Clone(scanned)
	slices.SortFunc(candidates, func(a, b string) int { return cmp.Or(cmp.Compare(group(a), group(b)), cmp.Compare(a, b)) })
	return candidates
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

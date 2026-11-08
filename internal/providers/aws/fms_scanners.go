package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/fms"
	fmstypes "github.com/aws/aws-sdk-go-v2/service/fms/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isFMSNotEnabled disambiguates the "no default admin" not-onboarded state
// from a real IAM denial.
func isFMSNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "No default admin could be found")
}

// isFMSAdminOnlyDenial matches the AccessDeniedException a non-FMS-admin
// account gets on a management-only op (ListPolicies, ListResourceSets).
// Distinct from isFMSNotEnabled — admin exists elsewhere in the org. Member
// accounts hit this every scan; silent-skip.
func isFMSAdminOnlyDenial(err error) bool {
	return isAccessDeniedWithMessage(err, "only available to AWS Firewall Manager Administrators")
}

// fmsNotFound is the not-found code GetPolicy, ListResourceSetResources and
// ListTagsForResource model: the policy, set or list was deleted after it
// was listed.
var fmsNotFound = []string{"ResourceNotFoundException"}

// isFMSResourceNotFound matches ResourceNotFoundException, which
// GetNotificationChannel models for a region with no channel set.
func isFMSResourceNotFound(err error) bool {
	return isAPIErrorCode(err, fmsNotFound...)
}

// fmsPageSize is the model's MaxResults cap (PaginationMaxResults, 1-100).
const fmsPageSize int32 = 100

const fmsTagsOp = "fms:ListTagsForResource"

var errFMSPolicyMissing = errors.New("GetPolicy answered without a policy body; rescan, and report it if it recurs")

// fmsResourceSetARN builds a resource set's ARN; ResourceSetSummary carries
// none. The resolver rebuilds it from a policy's ResourceSetIds with the
// policy row's region: resource sets are regional, and whether a policy
// can name a set of another region is unverified (such a set gets no edge).
func fmsResourceSetARN(partition, region, accountID, id string) string {
	return arnFor(partition, "fms", region, accountID, "resource-set/"+id)
}

// fmsBuiltPolicyARN builds a policy's ARN in the Service Reference's format
// (policy/{Id}), for a summary listed with a PolicyId but no PolicyArn.
func fmsBuiltPolicyARN(partition, region, accountID, id string) string {
	return arnFor(partition, "fms", region, accountID, "policy/"+id)
}

// fmsNotificationChannelNativeID names the notification channel row. The API
// returns no ARN, so the id is synthesized in the fms ARN shape. It carries
// the region so two regions' channels never share one row.
func fmsNotificationChannelNativeID(partition, region, accountID string) string {
	return arnFor(partition, "fms", region, accountID, "notification-channel")
}

func init() {
	registerType(restype.Descriptor{Type: TypeFMSNotificationChannel, Service: "fms", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeFMSPolicy, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSResourceSet, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSAppsList, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSProtocolsList, Service: "fms"})
	registerService(serviceEntry{
		name: "aws:fms",
		fn:   scanFMS,
	})
}

type fmsAPI interface {
	GetNotificationChannel(context.Context, *fms.GetNotificationChannelInput, ...func(*fms.Options)) (*fms.GetNotificationChannelOutput, error)
	GetPolicy(context.Context, *fms.GetPolicyInput, ...func(*fms.Options)) (*fms.GetPolicyOutput, error)
	ListPolicies(context.Context, *fms.ListPoliciesInput, ...func(*fms.Options)) (*fms.ListPoliciesOutput, error)
	ListResourceSets(context.Context, *fms.ListResourceSetsInput, ...func(*fms.Options)) (*fms.ListResourceSetsOutput, error)
	ListResourceSetResources(context.Context, *fms.ListResourceSetResourcesInput, ...func(*fms.Options)) (*fms.ListResourceSetResourcesOutput, error)
	ListAppsLists(context.Context, *fms.ListAppsListsInput, ...func(*fms.Options)) (*fms.ListAppsListsOutput, error)
	ListProtocolsLists(context.Context, *fms.ListProtocolsListsInput, ...func(*fms.Options)) (*fms.ListProtocolsListsOutput, error)
	ListTagsForResource(context.Context, *fms.ListTagsForResourceInput, ...func(*fms.Options)) (*fms.ListTagsForResourceOutput, error)
}

// fmsTags reads a policy's, resource set's, applications list's or protocols
// list's tags with ListTagsForResource, sent the row's NativeID: the listed
// PolicyArn or ListArn, or the ARN built in the Service Reference's format
// for a resource set (its summary carries none) or a policy listed without
// PolicyArn. The Service Reference's ListTagsForResource names only
// policies, but its TagResource names all four resource types and each
// one's Put input takes TagList, so all four are read; how the op answers a
// resource-set ARN is unverified live. ResourceNotFoundException, the op's
// modeled not-found, drops a resource deleted after it was listed. No list
// call or Get body carries these tags. The notification channel is not a
// taggable resource.
func fmsTags(client fmsAPI) rowTagReader {
	return rowTagReader{
		op:       fmsTagsOp,
		notFound: fmsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &fms.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.TagList, func(t fmstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// fmsGate is a listing's answer that Firewall Manager was never onboarded
// for the account (isFMSNotEnabled), under the op that met it, as the
// disabled sentinel; err is nil when no listing met it.
type fmsGate struct {
	op  string
	err error
}

// fmsGatedPages is page with the not-onboarded answer recorded in gate and
// ending the listing without an error, keeping the pages read before it,
// and with the admin-only denial a member account gets on a first page read
// as an empty listing, without a warning (isFMSAdminOnlyDenial). After a
// page was read that denial is returned, and warned about as a denial
// (firstPageGapPages).
func fmsGatedPages[Item any](gate *fmsGate, op string, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	page = firstPageGapPages(isFMSAdminOnlyDenial, page)
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		// The not-onboarded answer is an AccessDeniedException, so it is
		// matched before the listing reads err as a denial.
		if isFMSNotEnabled(err) {
			*gate = fmsGate{op, markServiceDisabled(err)}
			return nil, nil, nil
		}
		return items, next, err
	}
}

// scanFMS discovers one region's Firewall Manager policies, resource sets,
// applications and protocols lists, and notification channel. It runs per
// region because the SDK endpoint table serves fms per region and the API
// returns region-bearing policy and list ARNs; that each region holds its own
// objects, the notification channel included, is the premise of that choice,
// not a verified fact. Every row carries its region either way. An
// administrator whose admin scope is limited to some regions may be denied in
// the others with a message that is unverified; if it matches neither the
// not-enabled nor the admin-only matcher, each such region warns once per op.
//
// Every listing runs as its own phase, one at a time: a phase's error does
// not stop the later ones; a failed write and ctx's end do, and so does an
// NXDOMAIN of the first listing with nothing stored (runHostPhases).
// Policies, resource sets, applications lists and protocols lists have their
// tags read per row (fmsTags), whose denials are one warning for the scan.
// An account FMS never onboarded reports it on ListPolicies, the first call,
// before anything is stored in this region; no later phase runs, and the
// scan returns the disabled sentinel for this region. A not-onboarded answer
// after rows were stored or a call failed becomes a warning instead, and the
// later phases run: the dispatcher reads the sentinel through any join and
// would report the service disabled, discarding those rows' counts and
// hiding the earlier failure.
func scanFMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := fms.NewFromConfig(acct.cfg, func(o *fms.Options) { o.Region = region })
	tags := fmsTags(client).sharingDenials()
	var gate, disabled fmsGate
	phase := func(op string, run func(p *phaseTotals) (storeWriteFailed bool)) hostPhase {
		return hostPhase{op: op, run: func(p *phaseTotals) bool {
			if disabled.err != nil {
				return false
			}
			if run(p) {
				return true
			}
			switch {
			case gate.err == nil:
			case p.total == 0 && len(p.errs) == 0:
				disabled = gate
			default:
				st.ReportWarning(store.ScanWarning{
					Provider: "aws", Service: gate.op, Scope: acct.ID + "/" + region,
					Message: "not reported as disabled because FMS rows were stored or a call failed before this answer: " + gate.err.Error(),
				})
			}
			gate = fmsGate{}
			return false
		}}
	}
	total, err := runHostPhases(ctx, st, acct, region, "fms", 0, []hostPhase{
		phase("fms:ListPolicies", func(p *phaseTotals) bool {
			return scanFMSPolicies(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("fms:ListResourceSets", func(p *phaseTotals) bool {
			return scanFMSResourceSets(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("fms:GetNotificationChannel", func(p *phaseTotals) bool {
			t, err := scanFMSNotificationChannel(ctx, client, acct, region, st, scanID)
			return p.addPhase("fms:GetNotificationChannel", t, err)
		}),
		phase("fms:ListAppsLists", func(p *phaseTotals) bool {
			return scanFMSAppsLists(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("fms:ListProtocolsLists", func(p *phaseTotals) bool {
			return scanFMSProtocolsLists(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
	})
	// Disabled, nothing was stored and no later phase ran.
	if disabled.err != nil {
		return 0, disabled.err
	}
	tags.warnDenials(st, acct, region)
	return total, err
}

// fmsPolicyRow is a policy's row under arn, its listed summary as
// attributes until GetPolicy's body replaces them.
func fmsPolicyRow(acct *account, region string, p fmstypes.PolicySummary, arn, scanID string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeFMSPolicy, NativeID: arn,
		Name: p.PolicyName, Region: &region, Status: nonEmptyPtr(string(p.PolicyStatus)),
		AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
	}
}

// scanFMSPolicies stores the policies, each page written as soon as its
// rows' GetPolicy bodies and tags are read. A row's attributes are its
// GetPolicy body: the listed summary lacks ResourceSetIds, which the
// policy->resource-set edge reads. A policy GetPolicy reports not found was
// deleted since it was listed and is not stored; any other failed GetPolicy
// stores the summary as the row (readRowDetails). A summary without
// PolicyArn is stored under the ARN built from its PolicyId; one without
// PolicyId is stored from the listing, warning once, as GetPolicy takes the
// id; one with neither cannot be named and is not stored, warning once. It
// reports whether a write failed, after which the scan must stop.
func scanFMSPolicies(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string, gate *fmsGate, tags rowTagReader, p *phaseTotals) bool {
	// ids maps each row of the page in flight to its PolicyId; a row listed
	// without one has no entry. The row builder fills it and the reads only
	// look entries up; it is emptied before the next page is fetched, which
	// follows that page's write, so one page is held.
	ids := map[*store.Resource]string{}
	var noID skipTally
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: "fms:GetPolicy", what: "policies", notFound: fmsNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				id, ok := ids[r]
				if !ok {
					return nil, nil
				}
				out, err := client.GetPolicy(ctx, &fms.GetPolicyInput{PolicyId: &id})
				if err != nil {
					return nil, err
				}
				if out.Policy == nil {
					return nil, errFMSPolicyMissing
				}
				return out.Policy, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if _, ok := ids[r]; !ok {
					noID.skipped(r.NativeID, nil)
				}
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "fms:ListPolicies",
		fmsGatedPages(gate, "fms:ListPolicies", func(ctx context.Context, token *string) ([]fmstypes.PolicySummary, *string, error) {
			clear(ids)
			out, err := client.ListPolicies(ctx, &fms.ListPoliciesInput{MaxResults: sdkaws.Int32(fmsPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.PolicyList, out.NextToken, nil
		}),
		func(s fmstypes.PolicySummary) (*store.Resource, string, string) {
			id := sv(s.PolicyId)
			arn := sv(s.PolicyArn)
			if arn == "" && id != "" {
				arn = fmsBuiltPolicyARN(acct.partition(), region, acct.ID, id)
			}
			if arn == "" {
				return nil, "policies have neither a PolicyArn nor a PolicyId", cmp.Or(sv(s.PolicyName), noIdentifier)
			}
			r := fmsPolicyRow(acct, region, s, arn, scanID)
			if id != "" {
				ids[r] = id
			}
			return r, "", ""
		},
		steps, tags, p)
	if !failed && noID.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "fms:ListPolicies", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d listed policies have no PolicyId, so GetPolicy could not be called; stored from the listing only, first %s", noID.count, noID.first),
		})
	}
	return failed
}

// fmsResourceSetAttrs is a resource-set row's attributes: the listed summary
// and every resource associated to the set, sorted so an unchanged set does
// not version-split. Resources is a pointer so a set whose member read
// failed stores no Resources key, while a set with no members stores []:
// an empty list is always the whole list, never a stand-in for an unread one.
type fmsResourceSetAttrs struct {
	ResourceSet fmstypes.ResourceSetSummary `json:"ResourceSet"`
	Resources   *[]fmstypes.Resource        `json:"Resources,omitempty"`
}

// scanFMSResourceSets stores the resource sets, each page written as soon as
// its rows' members and tags are read. A set ListResourceSetResources
// reports not found was deleted since it was listed and is not stored; any
// other failed member read stores the set without Resources
// (readRowDetails). A listed set with no Id cannot be named, so it is not
// stored and is warned about once. It reports whether a write failed, after
// which the scan must stop.
func scanFMSResourceSets(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string, gate *fmsGate, tags rowTagReader, p *phaseTotals) bool {
	// listed maps each row of the page in flight to its summary. The row
	// builder fills it and the reads only look entries up; it is emptied
	// before the next page is fetched, which follows that page's write.
	listed := map[*store.Resource]fmstypes.ResourceSetSummary{}
	steps := tokenPageSteps{details: rowDetailReader{
		op: "fms:ListResourceSetResources", what: "resource set(s)", notFound: fmsNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			s := listed[r]
			members, err := listFMSResourceSetMembers(ctx, client, sv(s.Id))
			if err != nil {
				return nil, err
			}
			return fmsResourceSetAttrs{ResourceSet: s, Resources: &members}, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "fms:ListResourceSets",
		fmsGatedPages(gate, "fms:ListResourceSets", func(ctx context.Context, token *string) ([]fmstypes.ResourceSetSummary, *string, error) {
			clear(listed)
			out, err := client.ListResourceSets(ctx, &fms.ListResourceSetsInput{MaxResults: sdkaws.Int32(fmsPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResourceSets, out.NextToken, nil
		}),
		func(s fmstypes.ResourceSetSummary) (*store.Resource, string, string) {
			id := sv(s.Id)
			if id == "" {
				return nil, "resource sets have no Id", cmp.Or(sv(s.Name), noIdentifier)
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSResourceSet, NativeID: fmsResourceSetARN(acct.partition(), region, acct.ID, id),
				Name: s.Name, Region: &region, Status: nonEmptyPtr(string(s.ResourceSetStatus)),
				AttributesJSON: mustJSON(fmsResourceSetAttrs{ResourceSet: s}), DiscoveredBy: scanID,
			}
			listed[r] = s
			return r, "", ""
		},
		steps, tags, p)
}

// listFMSResourceSetMembers returns every resource associated to the set,
// sorted, and never nil. ListResourceSetResources has no paginator; a
// repeated token fails the read rather than return a partial list.
func listFMSResourceSetMembers(ctx context.Context, client fmsAPI, id string) ([]fmstypes.Resource, error) {
	members := []fmstypes.Resource{}
	in := &fms.ListResourceSetResourcesInput{Identifier: &id, MaxResults: sdkaws.Int32(fmsPageSize)}
	for {
		out, err := client.ListResourceSetResources(ctx, in)
		if err != nil {
			return nil, err
		}
		members = append(members, out.Items...)
		next := sv(out.NextToken)
		if next == "" {
			break
		}
		if next == sv(in.NextToken) {
			return nil, fmt.Errorf("NextToken %q: %w", next, errRepeatedNextToken)
		}
		in.NextToken = out.NextToken
	}
	slices.SortFunc(members, func(a, b fmstypes.Resource) int {
		return cmp.Or(cmp.Compare(sv(a.URI), sv(b.URI)), cmp.Compare(sv(a.AccountId), sv(b.AccountId)))
	})
	return members, nil
}

// scanFMSNotificationChannel stores the region's notification channel, a
// singleton named by fmsNotificationChannelNativeID. ResourceNotFoundException,
// which the op models, is read as no channel set; a not-enabled or admin-only
// answer is the same expected state ListPolicies already reports, and any
// other denial warns.
func scanFMSNotificationChannel(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	out, err := client.GetNotificationChannel(ctx, &fms.GetNotificationChannelInput{})
	switch {
	case err == nil:
	case isFMSNotEnabled(err), isFMSAdminOnlyDenial(err), isFMSResourceNotFound(err):
		return 0, nil
	case isAccessDenied(err):
		return 0, skipIfAccessDenied(st, "fms:GetNotificationChannel", acct.ID, region, err)
	default:
		return 0, fmt.Errorf("fms:GetNotificationChannel: %w", err)
	}
	topic := sv(out.SnsTopicArn)
	if topic == "" {
		return 0, nil
	}
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeFMSNotificationChannel, NativeID: fmsNotificationChannelNativeID(acct.partition(), region, acct.ID),
		Name: &topic, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "fms notification-channel")
}

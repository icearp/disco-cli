package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

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

// isFMSResourceNotFound matches the modeled not-found of GetPolicy and
// ListResourceSetResources: the policy or set was deleted after it was listed.
func isFMSResourceNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// fmsHomeRegion is the one region scanFMS calls. Resource-set ARNs carry it,
// so the resolver rebuilds them from a policy's ResourceSetIds with it.
const fmsHomeRegion = "us-east-1"

// fmsPageSize is the model's MaxResults cap (PaginationMaxResults, 1-100).
const fmsPageSize int32 = 100

var errFMSPolicyMissing = errors.New("GetPolicy answered without a policy body; rescan, and report it if it recurs")

func fmsResourceSetARN(accountID, id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:resource-set/%s", fmsHomeRegion, accountID, id)
}

func init() {
	registerType(restype.Descriptor{Type: TypeFMSNotificationChannel, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSPolicy, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSResourceSet, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSAppsList, Service: "fms"})
	registerType(restype.Descriptor{Type: TypeFMSProtocolsList, Service: "fms"})
	registerService(serviceEntry{
		name:   "aws:fms",
		global: true,
		fn:     scanFMS,
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
}

// scanFMS discovers Firewall Manager policies, resource sets, and the
// per-org notification channel, calling only fmsHomeRegion. Every phase runs
// whatever the others returned, and the counts are summed. Each phase's error
// passes through demoteTransient, so one transient failure cannot make the
// dispatcher read the whole join as transient and hide another phase's hard
// error. An account FMS never onboarded reports it on ListPolicies, the first
// call, before anything is stored, and the scan returns the disabled sentinel.
// A disabled answer after an earlier phase stored rows or failed hard becomes
// a warning instead: the dispatcher reads the sentinel through any join and
// would report the service disabled, discarding those rows' counts and hiding
// the hard error.
func scanFMS(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := fmsHomeRegion
	client := fms.NewFromConfig(acct.cfg, func(o *fms.Options) { o.Region = region })
	var errs []error
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{"fms:ListPolicies", func() (int, int, error) { return scanFMSPolicies(ctx, client, acct, region, st, scanID) }},
		{"fms:ListResourceSets", func() (int, int, error) { return scanFMSResourceSets(ctx, client, acct, region, st, scanID) }},
		{"fms:GetNotificationChannel", func() (int, int, error) { return scanFMSNotificationChannel(ctx, client, acct, region, st, scanID) }},
		{"fms:ListAppsLists", func() (int, int, error) { return scanFMSAppsLists(ctx, client, acct, region, st, scanID) }},
		{"fms:ListProtocolsLists", func() (int, int, error) { return scanFMSProtocolsLists(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		switch {
		case errors.Is(perr, errServiceDisabled) && total == 0 && len(errs) == 0:
			return total, inserted, perr
		case errors.Is(perr, errServiceDisabled):
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: phase.op, Scope: acct.ID + "/" + region,
				Message: "not reported as disabled because earlier FMS phases stored rows or failed: " + perr.Error(),
			})
		case perr != nil:
			if herr := demoteTransient(st, phase.op, acct, region, perr); herr != nil {
				errs = append(errs, herr)
			}
		}
	}
	return total, inserted, errors.Join(errs...)
}

// warnFMSListingOnly warns once for the keys of a fan-out whose read failed
// transiently and which were stored from the listing alone.
func warnFMSListingOnly(st *store.Store, op string, acct *account, region string, t *transientTally) {
	if t.count == 0 {
		return
	}
	_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf("%d stored from the listing only, first %s: %w", t.count, t.first, t.err))
}

// warnFMSNotStored warns once for the listed items of a phase that carry no
// identifier to store them under; why says which one is missing.
func warnFMSNotStored(st *store.Store, op string, acct *account, region string, t *skipTally, why string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d listed %s, so they were not stored; first %s", t.count, why, t.first),
	})
}

// fmsPolicyRow is a policy's row under its ARN; attrs is its GetPolicy body,
// or its listed summary when GetPolicy could not be read.
func fmsPolicyRow(acct *account, p fmstypes.PolicySummary, arn, attrs, scanID string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeFMSPolicy, NativeID: arn,
		Name: p.PolicyName, Region: regionGlobal, Status: nonEmptyPtr(string(p.PolicyStatus)),
		AttributesJSON: attrs, DiscoveredBy: scanID,
	}
}

// scanFMSPolicies lists the policies, then stores each one's GetPolicy body:
// the listed summary lacks ResourceSetIds, which the policy->resource-set
// edge reads. A policy deleted between the two calls is dropped. A policy
// whose GetPolicy is denied or fails transiently, or whose summary has no
// PolicyId to call it with, keeps its row with the summary as attributes,
// warning once per cause. A summary without PolicyArn takes the ARN from
// GetPolicy; one that ends up with no ARN cannot be named and is not stored,
// warning once.
func scanFMSPolicies(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListPoliciesPaginator(client, &fms.ListPoliciesInput{}, func(o *fms.ListPoliciesPaginatorOptions) { o.Limit = fmsPageSize })
	summaries := map[string]fmstypes.PolicySummary{}
	var (
		ids     []string
		noID    []fmstypes.PolicySummary
		unnamed skipTally
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListPolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListPolicies: %w", err)
		}
		for _, p := range out.PolicyList {
			id := sv(p.PolicyId)
			switch {
			case id != "":
				summaries[id] = p
				ids = append(ids, id)
			case sv(p.PolicyArn) != "":
				noID = append(noID, p)
			default:
				unnamed.skipped(fmt.Sprintf("%q", sv(p.PolicyName)), nil)
			}
		}
	}
	var (
		denyOnce  sync.Once
		transient transientTally
	)
	rows, err := describeFanOut(ctx, st, acct, region, "fms:GetPolicy", ids, isFMSResourceNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			p := summaries[id]
			arn, attrs := sv(p.PolicyArn), mustJSON(p)
			out, err := client.GetPolicy(ctx, &fms.GetPolicyInput{PolicyId: &id})
			switch {
			case err == nil && out.Policy == nil:
				return nil, errFMSPolicyMissing
			case err == nil:
				attrs = mustJSON(out.Policy)
				arn = cmp.Or(arn, sv(out.PolicyArn))
			case isAccessDenied(err):
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, "fms:GetPolicy", acct.ID, region, err) })
			case transient.drop(id, err):
			default:
				return nil, err
			}
			if arn == "" {
				unnamed.skipped(id, nil)
				return nil, nil
			}
			return fmsPolicyRow(acct, p, arn, attrs, scanID), nil
		})
	if err != nil {
		return 0, 0, err
	}
	for _, p := range noID {
		rows = append(rows, fmsPolicyRow(acct, p, sv(p.PolicyArn), mustJSON(p), scanID))
	}
	total, inserted, err := upsertBatch(st, rows, "fms policies")
	if err != nil {
		return total, inserted, err
	}
	warnFMSListingOnly(st, "fms:GetPolicy", acct, region, &transient)
	warnFMSNotStored(st, "fms:ListPolicies", acct, region, &unnamed, "policies have no PolicyArn in the listing or from GetPolicy")
	if len(noID) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "fms:ListPolicies", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d listed policies have no PolicyId, so GetPolicy could not be called; stored from the listing only, first %s", len(noID), sv(noID[0].PolicyArn)),
		})
	}
	return total, inserted, nil
}

// fmsResourceSetAttrs is a resource-set row's attributes: the listed summary
// and every resource associated to the set, sorted so an unchanged set does
// not version-split. Resources is a pointer so a denied or transiently failed
// member read stores no Resources key, while a set with no members stores []:
// an empty list is always the whole list, never a stand-in for an unread one.
type fmsResourceSetAttrs struct {
	ResourceSet fmstypes.ResourceSetSummary `json:"ResourceSet"`
	Resources   *[]fmstypes.Resource        `json:"Resources,omitempty"`
}

// scanFMSResourceSets lists the resource sets, then each set's members. A set
// deleted between the two calls is dropped; a listed set with no Id cannot be
// named, so it is not stored and is warned about once.
func scanFMSResourceSets(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	summaries := map[string]fmstypes.ResourceSetSummary{}
	var (
		ids     []string
		unnamed skipTally
	)
	in := &fms.ListResourceSetsInput{MaxResults: sdkaws.Int32(fmsPageSize)}
	for {
		out, err := client.ListResourceSets(ctx, in)
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListResourceSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListResourceSets: %w", err)
		}
		for _, r := range out.ResourceSets {
			id := sv(r.Id)
			if id == "" {
				unnamed.skipped(fmt.Sprintf("%q", sv(r.Name)), nil)
				continue
			}
			summaries[id] = r
			ids = append(ids, id)
		}
		next := sv(out.NextToken)
		if next == "" {
			break
		}
		if next == sv(in.NextToken) {
			return 0, 0, fmt.Errorf("fms:ListResourceSets: NextToken %q: %w", next, errRepeatedNextToken)
		}
		in.NextToken = out.NextToken
	}
	var (
		denyOnce  sync.Once
		transient transientTally
	)
	rows, err := describeFanOut(ctx, st, acct, region, "fms:ListResourceSetResources", ids, isFMSResourceNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			var members *[]fmstypes.Resource
			list, err := listFMSResourceSetMembers(ctx, client, id)
			switch {
			case err == nil:
				members = &list
			case isAccessDenied(err):
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, "fms:ListResourceSetResources", acct.ID, region, err) })
			case transient.drop(id, err):
			default:
				return nil, err
			}
			r := summaries[id]
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSResourceSet, NativeID: fmsResourceSetARN(acct.ID, id),
				Name: r.Name, Region: regionGlobal, Status: nonEmptyPtr(string(r.ResourceSetStatus)),
				AttributesJSON: mustJSON(fmsResourceSetAttrs{ResourceSet: r, Resources: members}), DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	total, inserted, err := upsertBatch(st, rows, "fms resource-sets")
	if err != nil {
		return total, inserted, err
	}
	warnFMSListingOnly(st, "fms:ListResourceSetResources", acct, region, &transient)
	warnFMSNotStored(st, "fms:ListResourceSets", acct, region, &unnamed, "resource sets have no Id")
	return total, inserted, nil
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

// scanFMSNotificationChannel captures the per-account notification channel
// (singleton). Synth ARN keyed on the SNS topic ARN.
func scanFMSNotificationChannel(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetNotificationChannel(ctx, &fms.GetNotificationChannelInput{})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("fms:GetNotificationChannel: %w", err)
	}
	topic := sv(out.SnsTopicArn)
	if topic == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:fms::%s:notification-channel", acct.ID)
	label := topic
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeFMSNotificationChannel, NativeID: arn,
		Name: &label, Region: regionGlobal,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "fms notification-channel")
}

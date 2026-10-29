package aws

import (
	"context"
	"slices"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
	rhv2types "github.com/aws/aws-sdk-go-v2/service/resiliencehubv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// The v2 API (policies, services, systems and their children) is a separate
// SDK module from the app-centric v1 API, but shares its endpoint, ARN
// namespace and IAM prefix, so its types live under the same service.
// A v2 policy (arn:…:policy/…) is a different entity from a v1 resiliency
// policy (arn:…:resiliency-policy/…).
func init() {
	registerType(restype.Descriptor{Type: TypeResilienceHubPolicy, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubService, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubSystem, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubTest, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubUserJourney, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubServiceFunction, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubInputSource, Service: "resilience-hub"})
}

type resilienceHubV2API interface {
	ListPolicies(context.Context, *resiliencehubv2.ListPoliciesInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListPoliciesOutput, error)
	ListServices(context.Context, *resiliencehubv2.ListServicesInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListServicesOutput, error)
	ListSystems(context.Context, *resiliencehubv2.ListSystemsInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListSystemsOutput, error)
	ListTests(context.Context, *resiliencehubv2.ListTestsInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListTestsOutput, error)
	ListUserJourneys(context.Context, *resiliencehubv2.ListUserJourneysInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListUserJourneysOutput, error)
	ListServiceFunctions(context.Context, *resiliencehubv2.ListServiceFunctionsInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListServiceFunctionsOutput, error)
	ListInputSources(context.Context, *resiliencehubv2.ListInputSourcesInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListInputSourcesOutput, error)
	ListTagsForResource(context.Context, *resiliencehubv2.ListTagsForResourceInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListTagsForResourceOutput, error)
}

const (
	rhListPoliciesOp     = "resiliencehub:ListPolicies"
	rhListServicesOp     = "resiliencehub:ListServices"
	rhListSystemsOp      = "resiliencehub:ListSystems"
	rhListTestsOp        = "resiliencehub:ListTests"
	rhListUserJourneysOp = "resiliencehub:ListUserJourneys"
)

// rhTagsV2 reads a v2 row's tags with the v2 client's ListTagsForResource,
// which the Service Reference lists for every Resilience Hub type; it is read
// for policies, services and systems, whose listed elements carry no tags. It
// is sent the row's NativeID, the ARN its listing returned; the op takes no page
// token and returns every tag at once. Only rows homed in the scanned
// account and region are stored (rhHomedHere), so no read is about another
// account's resource.
func rhTagsV2(client resilienceHubV2API) rowTagReader {
	return rowTagReader{
		op:       rhTagsOp,
		notFound: rhNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &resiliencehubv2.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// rhHomedHere reports whether a v2 ARN names a resource of accountID homed
// in region. The v2 list ops can return resources other accounts own:
// ListPolicies documents an optional AccountId owning-account filter, and
// services and systems span an organization's accounts. The filters are not
// passed: whether a caller outside an organization is refused for using them
// is unverified, and a refusal would empty every scan. This check alone keeps
// the scanned account's resources. Services
// operate across Regions, so a region's list may also return objects homed
// elsewhere; storing those from every region would flap their Region and
// re-list their children per region. The owning account's scan of the home
// region picks each one up exactly once.
func rhHomedHere(resourceARN, accountID, region string) bool {
	a, err := arn.Parse(resourceARN)
	return err == nil && a.AccountID == accountID && a.Region == region
}

// rhHomedPages is page with each page's elements homed elsewhere left out
// (rhHomedHere), without a warning: they are not this scan's to store. An
// element without an ARN is kept, so that its row builder warns about it.
func rhHomedPages[Item any](acct *account, region string, arnOf func(Item) *string, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		items = slices.DeleteFunc(items, func(it Item) bool {
			a := sv(arnOf(it))
			return a != "" && !rhHomedHere(a, acct.ID, region)
		})
		return items, next, err
	}
}

func rhResource(acct *account, region, scanID, typ, nativeID, name, status string, createdAt *time.Time, elem any) *store.Resource {
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID, Region: &region, CreatedAt: tp(createdAt),
		AttributesJSON: mustJSON(elem), DiscoveredBy: scanID,
	}
	if name != "" {
		r.Name = &name
	}
	if status != "" {
		r.Status = &status
	}
	return r
}

// scanRHPolicies stores the v2 policies homed here, each page written as
// soon as its rows' tags are read. It reports absent when its first call
// shows the v2 API is not deployed in region (isOpNotDeployed). The v2
// client shares v1's endpoint host, so a region where only v1 is deployed
// answers with an unrouted-op shape; its gateway message arrives as a
// NotFoundException. ListPolicies models no not-found error, so any 404 on
// its first call means the API is absent. That is a sub-feature gap inside a
// present service, so it is skipped silently rather than failing the whole
// resilience-hub scan. Once a page has succeeded the API is deployed, and a
// later failure of any shape is a real error. It reports whether a write
// failed, after which the scan must stop.
func scanRHPolicies(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (absent, storeWriteFailed bool) {
	// firstPageGapPages asks only about the first page's error, so absent
	// records the first call's answer.
	notDeployed := func(err error) bool {
		absent = isOpNotDeployed(err)
		return absent
	}
	storeWriteFailed = storeTokenPagesTags(ctx, st, acct, region, rhListPoliciesOp,
		firstPageGapPages(notDeployed, rhHomedPages(acct, region, func(pol rhv2types.PolicySummary) *string { return pol.PolicyArn },
			func(ctx context.Context, token *string) ([]rhv2types.PolicySummary, *string, error) {
				out, err := client.ListPolicies(ctx, &resiliencehubv2.ListPoliciesInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.PolicySummaries, out.NextToken, nil
			})),
		func(pol rhv2types.PolicySummary) (*store.Resource, string, string) {
			return arnRow("policies", pol.PolicyArn, pol.Name,
				rhResource(acct, region, scanID, TypeResilienceHubPolicy, sv(pol.PolicyArn), sv(pol.Name), "", pol.CreatedAt, pol))
		},
		tags, p)
	return absent, storeWriteFailed
}

// scanRHServices stores the services homed here, each page written as soon
// as its rows' tags are read, and returns the stored ones keyed by their ARN:
// the parents ListTests, ListServiceFunctions and ListInputSources require.
// It reports whether a write failed, after which the scan must stop.
func scanRHServices(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, rhListServicesOp, "services", "ServiceArn",
		rhHomedPages(acct, region, func(s rhv2types.ServiceSummary) *string { return s.ServiceArn },
			func(ctx context.Context, token *string) ([]rhv2types.ServiceSummary, *string, error) {
				out, err := client.ListServices(ctx, &resiliencehubv2.ListServicesInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.ServiceSummaries, out.NextToken, nil
			}),
		func(s rhv2types.ServiceSummary) (*store.Resource, string, string) {
			return arnRow("services", s.ServiceArn, s.Name,
				rhResource(acct, region, scanID, TypeResilienceHubService, sv(s.ServiceArn), sv(s.Name), string(s.AssessmentStatus), s.CreatedAt, s))
		},
		func(s rhv2types.ServiceSummary) string { return sv(s.ServiceArn) },
		tags, p)
}

// scanRHSystems stores the systems homed here, each page written as soon as
// its rows' tags are read, and returns the stored ones keyed by their ARN:
// the parents ListUserJourneys requires. SystemArn is optional in the
// summary (SystemId is the required field); a system without one has neither
// an ARN identity nor a way to list its user journeys, so it is not stored
// and is warned about. It reports whether a write failed, after which the
// scan must stop.
func scanRHSystems(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, rhListSystemsOp, "systems", "SystemArn",
		rhHomedPages(acct, region, func(s rhv2types.SystemSummary) *string { return s.SystemArn },
			func(ctx context.Context, token *string) ([]rhv2types.SystemSummary, *string, error) {
				out, err := client.ListSystems(ctx, &resiliencehubv2.ListSystemsInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.SystemSummaries, out.NextToken, nil
			}),
		func(s rhv2types.SystemSummary) (*store.Resource, string, string) {
			return arnRow("systems", s.SystemArn, s.SystemId,
				rhResource(acct, region, scanID, TypeResilienceHubSystem, sv(s.SystemArn), sv(s.Name), "", s.CreatedAt, s))
		},
		func(s rhv2types.SystemSummary) string { return sv(s.SystemArn) },
		tags, p)
}

// A test has no ARN; its NativeID is {serviceARN}/test/{testId}. The summary
// has no name.
func scanRHTests(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, serviceARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, rhListTestsOp, rhParents(serviceARNs), isRHParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			serviceARN := p.arn
			var rows []*store.Resource
			pager := resiliencehubv2.NewListTestsPaginator(client, &resiliencehubv2.ListTestsInput{ServiceArn: &serviceARN})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, t := range out.Tests {
					id := sv(t.TestId)
					if id == "" {
						continue
					}
					rows = append(rows, rhResource(acct, region, scanID, TypeResilienceHubTest, serviceARN+"/test/"+id, "", "", t.CreationTime, t))
				}
			}
			return rows, nil
		})
}

// A user journey has no ARN; its NativeID is
// {systemARN}/user-journey/{userJourneyId}.
func scanRHUserJourneys(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, systemARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, rhListUserJourneysOp, rhParents(systemARNs), isRHParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			systemARN := p.arn
			var rows []*store.Resource
			pager := resiliencehubv2.NewListUserJourneysPaginator(client, &resiliencehubv2.ListUserJourneysInput{SystemArn: &systemARN})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, u := range out.UserJourneySummaries {
					id := sv(u.UserJourneyId)
					if id == "" {
						continue
					}
					rows = append(rows, rhResource(acct, region, scanID, TypeResilienceHubUserJourney, systemARN+"/user-journey/"+id, sv(u.Name), "", u.CreatedAt, u))
				}
			}
			return rows, nil
		})
}

const rhListServiceFunctionsOp = "resiliencehub:ListServiceFunctions"

// A service function has no ARN; its NativeID is
// {serviceARN}/service-function/{serviceFunctionId}. Functions Resilience Hub
// generated (Source AI_GENERATED) are stored like user-defined ones, not as
// provider-managed: they hang off the customer's service and the model lets
// the customer delete any function (DeleteServiceFunction).
func scanRHServiceFunctions(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, serviceARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, rhListServiceFunctionsOp, rhParents(serviceARNs), isRHParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			serviceARN := p.arn
			var rows []*store.Resource
			pager := resiliencehubv2.NewListServiceFunctionsPaginator(client, &resiliencehubv2.ListServiceFunctionsInput{ServiceArn: &serviceARN},
				func(o *resiliencehubv2.ListServiceFunctionsPaginatorOptions) { o.Limit = rhPageLimit })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, f := range out.ServiceFunctions {
					id := sv(f.ServiceFunctionId)
					if id == "" {
						continue
					}
					rows = append(rows, rhResource(acct, region, scanID, TypeResilienceHubServiceFunction,
						serviceARN+"/service-function/"+id, sv(f.Name), "", f.CreatedAt, f))
				}
			}
			return rows, nil
		})
}

const rhListInputSourcesOp = "resiliencehub:ListInputSources"

// A v2 input source has no ARN; its NativeID is
// {serviceARN}/input-source/{inputSourceId}. The id's model pattern (\S)
// allows "/", so it goes last. The summary has no name.
func scanRHInputSources(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, serviceARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, rhListInputSourcesOp, rhParents(serviceARNs), isRHParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			serviceARN := p.arn
			var rows []*store.Resource
			pager := resiliencehubv2.NewListInputSourcesPaginator(client, &resiliencehubv2.ListInputSourcesInput{ServiceArn: &serviceARN},
				func(o *resiliencehubv2.ListInputSourcesPaginatorOptions) { o.Limit = rhPageLimit })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, src := range out.InputSourceSummaries {
					id := sv(src.InputSourceId)
					if id == "" {
						continue
					}
					rows = append(rows, rhResource(acct, region, scanID, TypeResilienceHubInputSource,
						serviceARN+"/input-source/"+id, "", "", src.CreatedAt, src))
				}
			}
			return rows, nil
		})
}

// The child list ops key on the parent ARN, which is also its NativeID.
func rhParents(arns []string) []childParent {
	parents := make([]childParent, len(arns))
	for i, a := range arns {
		parents[i] = childParent{id: a, arn: a}
	}
	return parents
}

// isRHParentGone matches a parent deleted between its list and the child
// call; the parent is skipped.
func isRHParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

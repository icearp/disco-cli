package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
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
}

type resilienceHubV2API interface {
	ListPolicies(context.Context, *resiliencehubv2.ListPoliciesInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListPoliciesOutput, error)
	ListServices(context.Context, *resiliencehubv2.ListServicesInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListServicesOutput, error)
	ListSystems(context.Context, *resiliencehubv2.ListSystemsInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListSystemsOutput, error)
	ListTests(context.Context, *resiliencehubv2.ListTestsInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListTestsOutput, error)
	ListUserJourneys(context.Context, *resiliencehubv2.ListUserJourneysInput, ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListUserJourneysOutput, error)
}

// scanResilienceHubV2 lists policies, services and systems, then fans out
// tests per service and user journeys per system. ListPolicies doubles as the
// deployment probe: when it reports the v2 API absent from the region, the
// remaining phases are skipped.
func scanResilienceHubV2(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	available, t, i, err := scanRHPolicies(ctx, client, acct, region, st, scanID)
	if err != nil || !available {
		return t, i, err
	}
	total += t
	inserted += i

	serviceARNs, t, i, err := scanRHServices(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	systemARNs, t, i, err := scanRHSystems(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	t, i, err = scanRHTests(ctx, client, acct, region, st, scanID, serviceARNs)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i

	t, i, err = scanRHUserJourneys(ctx, client, acct, region, st, scanID, systemARNs)
	if err != nil {
		return total, inserted, err
	}
	total += t
	inserted += i
	return total, inserted, nil
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

// isRHV2NotDeployed reports the shapes an unrouted v2 call takes in a region
// where only the v1 API is deployed: the v2 client shares v1's endpoint host,
// which answers an unknown operation with UnknownOperationException, a
// NotFoundException "Unable to determine service/operation name", or an HTTP
// 404. ListPolicies models no not-found error, so any 404 on its first call
// means the API is absent. That is a sub-feature gap inside a present service,
// so it is skipped silently rather than failing the whole resilience-hub scan.
// Only the first call is classified this way: once a page has succeeded the
// API is deployed, and a later failure of any shape is a real error.
func isRHV2NotDeployed(err error) bool {
	return isAPIErrorCode(err, "UnknownOperationException") || isServiceNotAvailableInRegion(err) || isHTTP404(err)
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

// scanRHPolicies reports available=false when its first call shows the v2
// API is not deployed in region; see isRHV2NotDeployed.
func scanRHPolicies(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) (available bool, total, inserted int, err error) {
	pager := resiliencehubv2.NewListPoliciesPaginator(client, &resiliencehubv2.ListPoliciesInput{})
	var batch []*store.Resource
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if firstPage && isRHV2NotDeployed(err) {
				return false, 0, 0, nil
			}
			if isAccessDenied(err) {
				return true, 0, 0, skipIfAccessDenied(st, "resiliencehub:ListPolicies", acct.ID, region, err)
			}
			return true, 0, 0, fmt.Errorf("resiliencehub:ListPolicies: %w", err)
		}
		for _, p := range out.PolicySummaries {
			policyARN := sv(p.PolicyArn)
			if !rhHomedHere(policyARN, acct.ID, region) {
				continue
			}
			batch = append(batch, rhResource(acct, region, scanID, TypeResilienceHubPolicy, policyARN, sv(p.Name), "", p.CreatedAt, p))
		}
	}
	total, inserted, err = upsertBatch(st, batch, "resilience-hub policies")
	return true, total, inserted, err
}

// scanRHServices returns the stored services' ARNs: the parents ListTests
// requires.
func scanRHServices(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := resiliencehubv2.NewListServicesPaginator(client, &resiliencehubv2.ListServicesInput{})
	var batch []*store.Resource
	var serviceARNs []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "resiliencehub:ListServices", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("resiliencehub:ListServices: %w", err)
		}
		for _, s := range out.ServiceSummaries {
			serviceARN := sv(s.ServiceArn)
			if !rhHomedHere(serviceARN, acct.ID, region) {
				continue
			}
			serviceARNs = append(serviceARNs, serviceARN)
			batch = append(batch, rhResource(acct, region, scanID, TypeResilienceHubService, serviceARN, sv(s.Name), string(s.AssessmentStatus), s.CreatedAt, s))
		}
	}
	t, i, err := upsertBatch(st, batch, "resilience-hub services")
	return serviceARNs, t, i, err
}

// scanRHSystems returns the stored systems' ARNs: the parents
// ListUserJourneys requires. SystemArn is optional in the summary (SystemId
// is the required field); a system without one is skipped, as it has neither
// an ARN identity nor a way to list its user journeys.
func scanRHSystems(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := resiliencehubv2.NewListSystemsPaginator(client, &resiliencehubv2.ListSystemsInput{})
	var batch []*store.Resource
	var systemARNs []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "resiliencehub:ListSystems", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("resiliencehub:ListSystems: %w", err)
		}
		for _, s := range out.SystemSummaries {
			systemARN := sv(s.SystemArn)
			if !rhHomedHere(systemARN, acct.ID, region) {
				continue
			}
			systemARNs = append(systemARNs, systemARN)
			batch = append(batch, rhResource(acct, region, scanID, TypeResilienceHubSystem, systemARN, sv(s.Name), "", s.CreatedAt, s))
		}
	}
	t, i, err := upsertBatch(st, batch, "resilience-hub systems")
	return systemARNs, t, i, err
}

// A test has no ARN; its NativeID is {serviceARN}/test/{testId}. The summary
// has no name.
func scanRHTests(ctx context.Context, client resilienceHubV2API, acct *account, region string, st *store.Store, scanID string, serviceARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "resiliencehub:ListTests", rhParents(serviceARNs), isRHParentGone,
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
	return childFanOut(ctx, st, acct, region, "resiliencehub:ListUserJourneys", rhParents(systemARNs), isRHParentGone,
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

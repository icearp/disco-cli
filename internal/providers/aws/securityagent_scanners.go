package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/securityagent"
	satypes "github.com/aws/aws-sdk-go-v2/service/securityagent/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityAgentAgentSpace, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentApplication, Service: "security-agent"})
	// A custom header can carry a bearer token the pentest sends to its target.
	registerType(restype.Descriptor{Type: TypeSecurityAgentPentest, Service: "security-agent", Redact: []redact.Rule{
		{Path: "NetworkTrafficConfig.CustomHeaders[*].Value", Mode: redact.RedactScalar},
		saActorAuthRedact,
	}})
	registerType(restype.Descriptor{Type: TypeSecurityAgentTargetDomain, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentIntegration, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentPrivateConnection, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentSecurityRequirementPack, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentCodeReview, Service: "security-agent", Redact: []redact.Rule{saActorAuthRedact}})
	registerType(restype.Descriptor{Type: TypeSecurityAgentThreatModel, Service: "security-agent", Redact: []redact.Rule{saActorAuthRedact}})
	registerType(restype.Descriptor{Type: TypeSecurityAgentSecurityRequirement, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentMembership, Service: "security-agent"})
	registerType(restype.Descriptor{Type: TypeSecurityAgentIntegratedResource, Service: "security-agent"})
	registerService(serviceEntry{
		name: "aws:security-agent",
		fn:   scanSecurityAgent,
	})
}

// saActorAuthRedact keeps an actor's Authentication.Value only when it is a
// reference or a boolean. For the SECRETS_MANAGER, AWS_LAMBDA and
// AWS_IAM_ROLE provider types it is documented as an ARN, but for
// AWS_INTERNAL its content is undocumented, so any other value is treated
// as a credential.
var saActorAuthRedact = redact.Rule{Path: "Assets.Actors[*].Authentication.Value", Mode: redact.RedactUnlessSafe, KeyIsSensitive: true}

type securityAgentAPI interface {
	ListAgentSpaces(context.Context, *securityagent.ListAgentSpacesInput, ...func(*securityagent.Options)) (*securityagent.ListAgentSpacesOutput, error)
	ListApplications(context.Context, *securityagent.ListApplicationsInput, ...func(*securityagent.Options)) (*securityagent.ListApplicationsOutput, error)
	ListPentests(context.Context, *securityagent.ListPentestsInput, ...func(*securityagent.Options)) (*securityagent.ListPentestsOutput, error)
	BatchGetPentests(context.Context, *securityagent.BatchGetPentestsInput, ...func(*securityagent.Options)) (*securityagent.BatchGetPentestsOutput, error)
	ListCodeReviews(context.Context, *securityagent.ListCodeReviewsInput, ...func(*securityagent.Options)) (*securityagent.ListCodeReviewsOutput, error)
	BatchGetCodeReviews(context.Context, *securityagent.BatchGetCodeReviewsInput, ...func(*securityagent.Options)) (*securityagent.BatchGetCodeReviewsOutput, error)
	ListThreatModels(context.Context, *securityagent.ListThreatModelsInput, ...func(*securityagent.Options)) (*securityagent.ListThreatModelsOutput, error)
	BatchGetThreatModels(context.Context, *securityagent.BatchGetThreatModelsInput, ...func(*securityagent.Options)) (*securityagent.BatchGetThreatModelsOutput, error)
	ListMemberships(context.Context, *securityagent.ListMembershipsInput, ...func(*securityagent.Options)) (*securityagent.ListMembershipsOutput, error)
	ListIntegratedResources(context.Context, *securityagent.ListIntegratedResourcesInput, ...func(*securityagent.Options)) (*securityagent.ListIntegratedResourcesOutput, error)
	ListTargetDomains(context.Context, *securityagent.ListTargetDomainsInput, ...func(*securityagent.Options)) (*securityagent.ListTargetDomainsOutput, error)
	ListIntegrations(context.Context, *securityagent.ListIntegrationsInput, ...func(*securityagent.Options)) (*securityagent.ListIntegrationsOutput, error)
	ListPrivateConnections(context.Context, *securityagent.ListPrivateConnectionsInput, ...func(*securityagent.Options)) (*securityagent.ListPrivateConnectionsOutput, error)
	ListSecurityRequirementPacks(context.Context, *securityagent.ListSecurityRequirementPacksInput, ...func(*securityagent.Options)) (*securityagent.ListSecurityRequirementPacksOutput, error)
	ListSecurityRequirements(context.Context, *securityagent.ListSecurityRequirementsInput, ...func(*securityagent.Options)) (*securityagent.ListSecurityRequirementsOutput, error)
	BatchGetSecurityRequirements(context.Context, *securityagent.BatchGetSecurityRequirementsInput, ...func(*securityagent.Options)) (*securityagent.BatchGetSecurityRequirementsOutput, error)
	ListTagsForResource(context.Context, *securityagent.ListTagsForResourceInput, ...func(*securityagent.Options)) (*securityagent.ListTagsForResourceOutput, error)
}

const (
	saTagsOp                   = "securityagent:ListTagsForResource"
	saListAgentSpacesOp        = "securityagent:ListAgentSpaces"
	saListApplicationsOp       = "securityagent:ListApplications"
	saListTargetDomainsOp      = "securityagent:ListTargetDomains"
	saListIntegrationsOp       = "securityagent:ListIntegrations"
	saListPrivateConnectionsOp = "securityagent:ListPrivateConnections"
	saListPacksOp              = "securityagent:ListSecurityRequirementPacks"
)

// scanSecurityAgent discovers Security Agent agent spaces, applications,
// target domains, integrations, private connections and requirement packs, the
// requirements of customer-managed packs, and under each agent space its
// pentests, code reviews, threat models, memberships and integrated resources.
// The APIs return no ARNs; each NativeID is synthesized per (account, region,
// kind, id).
func scanSecurityAgent(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return scanSecurityAgentAll(ctx, securityagent.NewFromConfig(acct.cfg, func(o *securityagent.Options) { o.Region = region }), acct, region, st, scanID)
}

// scanSecurityAgentAll runs every listing as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end do,
// and so does an NXDOMAIN of the first listing with nothing stored (the region
// does not serve the service) (runHostPhases). The per-parent phases get the parents stored this scan.
// The agent space, application, target domain, integration and pack rows have
// their tags read per row (saTags), whose denials are one warning for the
// scan; private connections are listed with theirs.
func scanSecurityAgentAll(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := saTags(client).sharingDenials()
	var spaces, apps, packs []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "securityagent", 0, []hostPhase{
		{op: saListAgentSpacesOp, run: func(p *phaseTotals) (failed bool) {
			spaces, failed = scanSAAgentSpaces(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: saListApplicationsOp, run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanSAApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "securityagent:ListPentests", func() (int, int, error) {
			return scanSAPentests(ctx, client, acct, region, st, scanID, spaces)
		}),
		{op: saListTargetDomainsOp, run: func(p *phaseTotals) bool {
			return scanSATargetDomains(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: saListIntegrationsOp, run: func(p *phaseTotals) bool {
			return scanSAIntegrations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, saListPrivateConnectionsOp, func() (int, int, error) {
			return scanSAPrivateConnections(ctx, client, acct, region, st, scanID)
		}),
		{op: saListPacksOp, run: func(p *phaseTotals) (failed bool) {
			packs, failed = scanSASecurityRequirementPacks(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "securityagent:ListCodeReviews", func() (int, int, error) {
			return scanSACodeReviews(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "securityagent:ListThreatModels", func() (int, int, error) {
			return scanSAThreatModels(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "securityagent:ListMemberships", func() (int, int, error) {
			return scanSAMemberships(ctx, client, acct, region, st, scanID, spaces, childParentIDs(apps))
		}),
		countedPhase(0, "securityagent:ListIntegratedResources", func() (int, int, error) {
			return scanSAIntegratedResources(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "securityagent:ListSecurityRequirements", func() (int, int, error) {
			return scanSASecurityRequirements(ctx, client, acct, region, st, scanID, packs)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// saTags reads a row's tags with ListTagsForResource, sent the row's NativeID:
// the listings return no ARN, so it is the ARN built from the listed id in the
// Service Reference's format (agent-space/${AgentId},
// application/${ApplicationId}, …), on the assumption, unverified live, that
// the ARN's id is the id the listing returns. The Service Reference lists
// agent spaces, applications, integrations, private connections, requirement
// packs and target domains under ListTagsForResource, and each one's Create op
// takes Tags; private connections are listed with their tags, so they are not
// read here. An AWS-managed pack is AWS-authored, and it is not known whether
// ListTagsForResource accepts the ARN built for it under this account
// (unverified live), so no read is made and the row is stored without tags. The op does not page and models no error, so a
// resource deleted after it was listed is not dropped: whatever the service
// answers for it keeps the row untagged, and that error is returned.
func saTags(client securityAgentAPI) rowTagReader {
	return rowTagReader{
		op: saTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return nil, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &securityagent.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

func saAgentSpaceARN(partition, region, accountID, id string) string {
	return arnFor(partition, "securityagent", region, accountID, "agent-space/"+id)
}

// scanSAAgentSpaces stores the agent spaces with their tags, a page at a
// time, and returns the stored ones, the parents of the per-space phases, and
// whether a write failed. The listing models no page size range, so none is
// sent.
func scanSAAgentSpaces(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, saListAgentSpacesOp, "agent spaces", "AgentSpaceId",
		func(ctx context.Context, token *string) ([]satypes.AgentSpaceSummary, *string, error) {
			out, err := client.ListAgentSpaces(ctx, &securityagent.ListAgentSpacesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentSpaceSummaries, out.NextToken, nil
		},
		func(s satypes.AgentSpaceSummary) (*store.Resource, string, string) {
			id := sv(s.AgentSpaceId)
			if id == "" {
				return nil, "agent spaces have no id", cmp.Or(sv(s.Name), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentAgentSpace, NativeID: saAgentSpaceARN(acct.partition(), region, acct.ID, id),
				Name: s.Name, Region: &region, CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		},
		func(s satypes.AgentSpaceSummary) string { return sv(s.AgentSpaceId) },
		tags, p)
}

// scanSAApplications stores the applications with their tags, a page at a
// time, and returns the stored ones, whose ids the membership phase pairs
// with each agent space, and whether a write failed.
func scanSAApplications(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, saListApplicationsOp, "applications", "ApplicationId",
		func(ctx context.Context, token *string) ([]satypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &securityagent.ListApplicationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationSummaries, out.NextToken, nil
		},
		func(a satypes.ApplicationSummary) (*store.Resource, string, string) {
			id := sv(a.ApplicationId)
			if id == "" {
				return nil, "applications have no id", cmp.Or(sv(a.ApplicationName), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentApplication, NativeID: arnFor(acct.partition(), "securityagent", region, acct.ID, "application/"+id),
				Name: a.ApplicationName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		},
		func(a satypes.ApplicationSummary) string { return sv(a.ApplicationId) },
		tags, p)
}

// scanSATargetDomains stores the target domains with their tags, a page at a
// time, and reports whether a write failed.
func scanSATargetDomains(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, saListTargetDomainsOp,
		func(ctx context.Context, token *string) ([]satypes.TargetDomainSummary, *string, error) {
			out, err := client.ListTargetDomains(ctx, &securityagent.ListTargetDomainsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TargetDomainSummaries, out.NextToken, nil
		},
		func(d satypes.TargetDomainSummary) (*store.Resource, string, string) {
			id := sv(d.TargetDomainId)
			if id == "" {
				return nil, "target domains have no id", cmp.Or(sv(d.DomainName), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentTargetDomain, NativeID: arnFor(acct.partition(), "securityagent", region, acct.ID, "target-domain/"+id),
				Name: d.DomainName, Region: &region, Status: nonEmptyPtr(string(d.VerificationStatus)),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scanSAIntegrations stores the integrations with their tags, a page at a
// time, and reports whether a write failed.
func scanSAIntegrations(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, saListIntegrationsOp,
		func(ctx context.Context, token *string) ([]satypes.IntegrationSummary, *string, error) {
			out, err := client.ListIntegrations(ctx, &securityagent.ListIntegrationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.IntegrationSummaries, out.NextToken, nil
		},
		func(in satypes.IntegrationSummary) (*store.Resource, string, string) {
			id := sv(in.IntegrationId)
			if id == "" {
				return nil, "integrations have no id", cmp.Or(sv(in.DisplayName), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentIntegration, NativeID: arnFor(acct.partition(), "securityagent", region, acct.ID, "integration/"+id),
				Name: in.DisplayName, Region: &region,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scanSAPrivateConnections stores the private connections with the tags their
// listed element carries, so no tag read is made.
func scanSAPrivateConnections(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, saListPrivateConnectionsOp,
		func(ctx context.Context, token *string) ([]satypes.PrivateConnectionSummary, *string, error) {
			out, err := client.ListPrivateConnections(ctx, &securityagent.ListPrivateConnectionsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.PrivateConnections, out.NextToken, nil
		},
		func(c satypes.PrivateConnectionSummary) (*store.Resource, string, string) {
			if sv(c.Name) == "" {
				return nil, "private connections have no name", noIdentifier
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentPrivateConnection, NativeID: arnFor(acct.partition(), "securityagent", region, acct.ID, "private-connection/"+*c.Name),
				Name: c.Name, Region: &region, Status: nonEmptyPtr(string(c.Status)),
				TagsJSON:       mapTagsJSON(c.Tags),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanSASecurityRequirementPacks stores every pack, a page at a time, with the
// tags of the customer-managed ones (an AWS-managed pack is stored untagged,
// see saTags), and returns the stored customer-managed ones, whose requirements the
// customer writes, and whether a write failed. The requirements of any other
// pack (AWS_MANAGED) are AWS-authored, not customer configuration, so they
// are not scanned.
func scanSASecurityRequirementPacks(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	var customer []childParent
	customerIDs := map[*store.Resource]string{}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, saListPacksOp,
		func(ctx context.Context, token *string) ([]satypes.SecurityRequirementPackSummary, *string, error) {
			out, err := client.ListSecurityRequirementPacks(ctx, &securityagent.ListSecurityRequirementPacksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SecurityRequirementPackSummaries, out.NextToken, nil
		},
		func(pk satypes.SecurityRequirementPackSummary) (*store.Resource, string, string) {
			id := sv(pk.PackId)
			if id == "" {
				return nil, "requirement packs have no id", cmp.Or(sv(pk.Name), noIdentifier)
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityAgentSecurityRequirementPack, NativeID: arnFor(acct.partition(), "securityagent", region, acct.ID, "security-requirement-pack/"+id),
				Name: pk.Name, Region: &region, Status: nonEmptyPtr(string(pk.Status)), CreatedAt: tp(pk.CreatedAt),
				ManagedByProvider: pk.ManagementType == satypes.ManagementTypeAwsManaged,
				AttributesJSON:    mustJSON(pk), DiscoveredBy: scanID,
			}
			if pk.ManagementType == satypes.ManagementTypeCustomerManaged {
				customerIDs[r] = id
			}
			return r, "", ""
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if id, ok := customerIDs[r]; ok {
					customer = append(customer, childParent{id: id, arn: r.NativeID})
				}
			}
			// Only one page's rows stay reachable, as storeParentPagesTags does.
			clear(customerIDs)
		}},
		tags, p)
	return customer, failed
}

func neverSkipSA(error) bool { return false }

func isSAParentGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// saWarnOnce records one scan warning for op, naming the first error, however
// many parents or pages hit it.
type saWarnOnce struct{ once sync.Once }

func (w *saWarnOnce) warn(st *store.Store, acct *account, region, op, what string, err error) {
	w.once.Do(func() {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: what + ": " + err.Error(),
		})
	})
}

// saBatch is one BatchGet answer: the full elements found, the ids reported
// not found, and itemErr naming any ids the call failed for individually.
type saBatch[F any] struct {
	found    []F
	notFound []string
	itemErr  error
}

// saDefinition is one child kind that a List op returns as summaries and a
// BatchGet op returns in full: an agent space's pentests, code reviews and
// threat models, and a pack's security requirements. S is the summary
// element, F the full one.
type saDefinition[S, F any] struct {
	typ, arnKind, listOp, getOp string
	// goneModeled is set when both ops model ResourceNotFoundException, so a
	// parent deleted after listing is an expected state and skipped silently.
	// Otherwise the code is unexplained and warns once per op.
	goneModeled bool
	// pages lists one parent, handing each page of summaries to page.
	pages     func(ctx context.Context, parentID string, page func([]S) error) error
	batchGet  func(ctx context.Context, parentID string, ids []string) (saBatch[F], error)
	summaryOf func(S) (id, title *string, createdAt *time.Time)
	fullOf    func(F) (id, title *string, createdAt *time.Time)
}

// scanSADefinitions stores every d under each parent, linked to it, from the
// full BatchGet element where one comes back and from the listed summary
// otherwise. BatchGet runs once per list page with that page's ids. Neither
// the model nor the API reference documents a cap on the id list, so a page
// the service lists may be more ids than its BatchGet accepts: a
// ValidationException then warns once and keeps that page's summaries, as an
// AccessDenied does. An id BatchGet reports not found was deleted after
// listing and is dropped; an id it returns that was not listed is ignored.
func scanSADefinitions[S, F any](ctx context.Context, st *store.Store, acct *account, region, scanID string, parents []childParent, d saDefinition[S, F]) (int, int, error) {
	var denyGet sync.Once
	var failedGet, partialGet, goneList, goneGet saWarnOnce
	isSkip := neverSkipSA
	if d.goneModeled {
		isSkip = isSAParentGone
	}
	row := func(id string, title *string, createdAt *time.Time, parentARN string, attrs any) *store.Resource {
		return &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: d.typ, NativeID: parentARN + "/" + d.arnKind + "/" + id,
			Name: title, Region: &region, CreatedAt: tp(createdAt),
			AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
		}
	}
	return childFanOut(ctx, st, acct, region, d.listOp, parents, isSkip, func(ctx context.Context, parent childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		err := d.pages(ctx, parent.id, func(summaries []S) error {
			var ids []string
			byID := map[string]*store.Resource{}
			for _, s := range summaries {
				id, title, createdAt := d.summaryOf(s)
				if sv(id) == "" {
					continue
				}
				ids = append(ids, *id)
				byID[*id] = row(*id, title, createdAt, parent.arn, s)
			}
			if len(ids) == 0 {
				return nil
			}
			batch, err := d.batchGet(ctx, parent.id, ids)
			switch {
			case err == nil:
			case isAccessDenied(err):
				denyGet.Do(func() { _ = skipIfAccessDenied(st, d.getOp, acct.ID, region, err) })
			case isAPIErrorCode(err, "ValidationException"):
				failedGet.warn(st, acct, region, d.getOp, "page of ids rejected; listed summaries kept", err)
			case isSAParentGone(err) && d.goneModeled:
				return fmt.Errorf("%s: %w", d.getOp, err)
			case isSAParentGone(err):
				goneGet.warn(st, acct, region, d.getOp, "unexpected not-found; listed summaries kept", err)
			default:
				return fmt.Errorf("%s: %w", d.getOp, err)
			}
			if batch.itemErr != nil {
				partialGet.warn(st, acct, region, d.getOp, "items not returned; listed summaries kept", batch.itemErr)
			}
			for _, f := range batch.found {
				id, title, createdAt := d.fullOf(f)
				if _, listed := byID[sv(id)]; listed {
					byID[*id] = row(*id, title, createdAt, parent.arn, f)
				}
			}
			for _, id := range batch.notFound {
				delete(byID, id)
			}
			for _, id := range ids {
				if r, ok := byID[id]; ok {
					rows = append(rows, r)
				}
			}
			return nil
		})
		if err != nil && !d.goneModeled && isSAParentGone(err) {
			goneList.warn(st, acct, region, d.listOp, "parent not found after listing; its children were not scanned", err)
			return nil, nil
		}
		return rows, err
	})
}

func scanSAPentests(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return scanSADefinitions(ctx, st, acct, region, scanID, spaces, saDefinition[satypes.PentestSummary, satypes.Pentest]{
		typ: TypeSecurityAgentPentest, arnKind: "pentest",
		listOp: "securityagent:ListPentests", getOp: "securityagent:BatchGetPentests",
		pages: func(ctx context.Context, spaceID string, page func([]satypes.PentestSummary) error) error {
			pager := securityagent.NewListPentestsPaginator(client, &securityagent.ListPentestsInput{AgentSpaceId: &spaceID})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				if err := page(out.PentestSummaries); err != nil {
					return err
				}
			}
			return nil
		},
		batchGet: func(ctx context.Context, spaceID string, ids []string) (saBatch[satypes.Pentest], error) {
			out, err := client.BatchGetPentests(ctx, &securityagent.BatchGetPentestsInput{AgentSpaceId: &spaceID, PentestIds: ids})
			if err != nil {
				return saBatch[satypes.Pentest]{}, err
			}
			return saBatch[satypes.Pentest]{found: out.Pentests, notFound: out.NotFound}, nil
		},
		summaryOf: func(s satypes.PentestSummary) (*string, *string, *time.Time) {
			return s.PentestId, s.Title, s.CreatedAt
		},
		fullOf: func(f satypes.Pentest) (*string, *string, *time.Time) { return f.PentestId, f.Title, f.CreatedAt },
	})
}

func scanSACodeReviews(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return scanSADefinitions(ctx, st, acct, region, scanID, spaces, saDefinition[satypes.CodeReviewSummary, satypes.CodeReview]{
		typ: TypeSecurityAgentCodeReview, arnKind: "code-review",
		listOp: "securityagent:ListCodeReviews", getOp: "securityagent:BatchGetCodeReviews",
		pages: func(ctx context.Context, spaceID string, page func([]satypes.CodeReviewSummary) error) error {
			pager := securityagent.NewListCodeReviewsPaginator(client, &securityagent.ListCodeReviewsInput{AgentSpaceId: &spaceID})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				if err := page(out.CodeReviewSummaries); err != nil {
					return err
				}
			}
			return nil
		},
		batchGet: func(ctx context.Context, spaceID string, ids []string) (saBatch[satypes.CodeReview], error) {
			out, err := client.BatchGetCodeReviews(ctx, &securityagent.BatchGetCodeReviewsInput{AgentSpaceId: &spaceID, CodeReviewIds: ids})
			if err != nil {
				return saBatch[satypes.CodeReview]{}, err
			}
			return saBatch[satypes.CodeReview]{found: out.CodeReviews, notFound: out.NotFound}, nil
		},
		summaryOf: func(s satypes.CodeReviewSummary) (*string, *string, *time.Time) {
			return s.CodeReviewId, s.Title, s.CreatedAt
		},
		fullOf: func(f satypes.CodeReview) (*string, *string, *time.Time) { return f.CodeReviewId, f.Title, f.CreatedAt },
	})
}

func scanSAThreatModels(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return scanSADefinitions(ctx, st, acct, region, scanID, spaces, saDefinition[satypes.ThreatModelSummary, satypes.ThreatModel]{
		typ: TypeSecurityAgentThreatModel, arnKind: "threat-model",
		listOp: "securityagent:ListThreatModels", getOp: "securityagent:BatchGetThreatModels",
		pages: func(ctx context.Context, spaceID string, page func([]satypes.ThreatModelSummary) error) error {
			pager := securityagent.NewListThreatModelsPaginator(client, &securityagent.ListThreatModelsInput{AgentSpaceId: &spaceID})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				if err := page(out.ThreatModelSummaries); err != nil {
					return err
				}
			}
			return nil
		},
		batchGet: func(ctx context.Context, spaceID string, ids []string) (saBatch[satypes.ThreatModel], error) {
			out, err := client.BatchGetThreatModels(ctx, &securityagent.BatchGetThreatModelsInput{AgentSpaceId: &spaceID, ThreatModelIds: ids})
			if err != nil {
				return saBatch[satypes.ThreatModel]{}, err
			}
			return saBatch[satypes.ThreatModel]{found: out.ThreatModels, notFound: out.NotFound}, nil
		},
		summaryOf: func(s satypes.ThreatModelSummary) (*string, *string, *time.Time) {
			return s.ThreatModelId, s.Title, s.CreatedAt
		},
		fullOf: func(f satypes.ThreatModel) (*string, *string, *time.Time) {
			return f.ThreatModelId, f.Title, f.CreatedAt
		},
	})
}

// scanSASecurityRequirements stores the requirements of each customer-managed
// pack the pack phase listed. Requirements are keyed by name within a pack,
// and BatchGetSecurityRequirements reports a name it cannot return as a
// per-item error rather than a not-found list.
func scanSASecurityRequirements(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, packs []childParent) (int, int, error) {
	type full = satypes.BatchGetSecurityRequirementResult
	return scanSADefinitions(ctx, st, acct, region, scanID, packs, saDefinition[satypes.SecurityRequirementSummary, full]{
		typ: TypeSecurityAgentSecurityRequirement, arnKind: "security-requirement",
		listOp: "securityagent:ListSecurityRequirements", getOp: "securityagent:BatchGetSecurityRequirements",
		goneModeled: true,
		pages: func(ctx context.Context, packID string, page func([]satypes.SecurityRequirementSummary) error) error {
			pager := securityagent.NewListSecurityRequirementsPaginator(client, &securityagent.ListSecurityRequirementsInput{PackId: &packID})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				if err := page(out.SecurityRequirementSummaries); err != nil {
					return err
				}
			}
			return nil
		},
		batchGet: func(ctx context.Context, packID string, names []string) (saBatch[full], error) {
			out, err := client.BatchGetSecurityRequirements(ctx, &securityagent.BatchGetSecurityRequirementsInput{PackId: &packID, SecurityRequirementNames: names})
			if err != nil {
				return saBatch[full]{}, err
			}
			b := saBatch[full]{found: out.SecurityRequirements}
			// The model documents no per-item Code values, so a requirement
			// deleted after listing is kept from its summary like any other
			// per-item error, with the warning.
			if len(out.Errors) > 0 {
				e := out.Errors[0]
				b.itemErr = fmt.Errorf("%d of %d requirements failed, first %q: %s: %s", len(out.Errors), len(names), sv(e.SecurityRequirementName), sv(e.Code), sv(e.Message))
			}
			return b, nil
		},
		summaryOf: func(s satypes.SecurityRequirementSummary) (*string, *string, *time.Time) {
			return s.Name, s.Name, s.CreatedAt
		},
		fullOf: func(f full) (*string, *string, *time.Time) { return f.Name, f.Name, f.CreatedAt },
	})
}

// saMembershipAttrs relabels the membership's Config and Metadata unions,
// which marshal as an unlabelled {"Value": ...}, with one key per known member.
// A member newer than the pinned SDK is kept under UnknownMember.
type saMembershipAttrs struct {
	satypes.MembershipSummary
	Config   saMembershipConfigAttrs
	Metadata saMemberMetadataAttrs
}

type saMembershipConfigAttrs struct {
	User          *satypes.UserConfig         `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember `json:",omitempty"`
}

type saMemberMetadataAttrs struct {
	User          *satypes.UserMetadata       `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember `json:",omitempty"`
}

func flattenSAMembership(m satypes.MembershipSummary) saMembershipAttrs {
	out := saMembershipAttrs{MembershipSummary: m}
	switch c := m.Config.(type) {
	case *satypes.MembershipConfigMemberUser:
		out.Config.User = &c.Value
	case *satypes.UnknownUnionMember:
		out.Config.UnknownMember = c
	}
	switch md := m.Metadata.(type) {
	case *satypes.MemberMetadataMemberUser:
		out.Metadata.User = &md.Value
	case *satypes.UnknownUnionMember:
		out.Metadata.UnknownMember = md
	}
	return out
}

// scanSAMemberships lists the memberships (user access grants) of every
// (agent space, application) pair, since ListMemberships takes both ids and
// neither summary names the other side. Each pair is its own fan-out key, so a
// failed pair drops only itself. The op models no errors: a not-found pair
// warns once and the others continue.
func scanSAMemberships(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent, appIDs []string) (int, int, error) {
	const op = "securityagent:ListMemberships"
	type pair struct{ spaceID, appID string }
	var keys []childParent
	pairs := map[string]pair{}
	for _, s := range spaces {
		for _, a := range appIDs {
			key := s.id + " (application " + a + ")"
			keys = append(keys, childParent{id: key, arn: s.arn})
			pairs[key] = pair{spaceID: s.id, appID: a}
		}
	}
	var gone saWarnOnce
	return childFanOut(ctx, st, acct, region, op, keys, neverSkipSA, func(ctx context.Context, key childParent) ([]*store.Resource, error) {
		p := pairs[key.id]
		var rows []*store.Resource
		pager := securityagent.NewListMembershipsPaginator(client, &securityagent.ListMembershipsInput{AgentSpaceId: &p.spaceID, ApplicationId: &p.appID})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if isSAParentGone(err) {
				gone.warn(st, acct, region, op, "agent space or application not found after listing; its memberships were not scanned", err)
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			for _, m := range out.MembershipSummaries {
				id := sv(m.MembershipId)
				if id == "" {
					continue
				}
				attrs := flattenSAMembership(m)
				var name *string
				if attrs.Metadata.User != nil {
					name = attrs.Metadata.User.Username
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type:     TypeSecurityAgentMembership,
					NativeID: key.arn + "/application/" + p.appID + "/membership/" + id,
					Name:     name, Region: &region, CreatedAt: tp(m.CreatedAt),
					AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// saIntegratedResourceAttrs relabels the Resource and Capabilities unions,
// which marshal as an unlabelled {"Value": ...}, with one key per known member.
// A member newer than the pinned SDK is kept under UnknownMember.
type saIntegratedResourceAttrs struct {
	satypes.IntegratedResourceSummary
	Resource     saIntegratedResourceMetadataAttrs
	Capabilities saResourceCapabilitiesAttrs
}

type saIntegratedResourceMetadataAttrs struct {
	GithubRepository    *satypes.GitHubRepositoryMetadata    `json:",omitempty"`
	GitlabRepository    *satypes.GitLabRepositoryMetadata    `json:",omitempty"`
	BitbucketRepository *satypes.BitbucketRepositoryMetadata `json:",omitempty"`
	ConfluenceDocument  *satypes.ConfluenceDocumentMetadata  `json:",omitempty"`
	UnknownMember       *satypes.UnknownUnionMember          `json:",omitempty"`
}

type saResourceCapabilitiesAttrs struct {
	Github        *satypes.GitHubResourceCapabilities     `json:",omitempty"`
	Gitlab        *satypes.GitLabResourceCapabilities     `json:",omitempty"`
	Bitbucket     *satypes.BitbucketResourceCapabilities  `json:",omitempty"`
	Confluence    *satypes.ConfluenceResourceCapabilities `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember             `json:",omitempty"`
}

// flattenSAIntegratedResource also returns the resource's provider id and
// name, which every known member carries; both are nil for an unknown member.
func flattenSAIntegratedResource(r satypes.IntegratedResourceSummary) (attrs saIntegratedResourceAttrs, providerID, name *string) {
	attrs = saIntegratedResourceAttrs{IntegratedResourceSummary: r}
	switch m := r.Resource.(type) {
	case *satypes.IntegratedResourceMetadataMemberGithubRepository:
		attrs.Resource.GithubRepository = &m.Value
		providerID, name = m.Value.ProviderResourceId, m.Value.Name
	case *satypes.IntegratedResourceMetadataMemberGitlabRepository:
		attrs.Resource.GitlabRepository = &m.Value
		providerID, name = m.Value.ProviderResourceId, m.Value.Name
	case *satypes.IntegratedResourceMetadataMemberBitbucketRepository:
		attrs.Resource.BitbucketRepository = &m.Value
		providerID, name = m.Value.ProviderResourceId, m.Value.Name
	case *satypes.IntegratedResourceMetadataMemberConfluenceDocument:
		attrs.Resource.ConfluenceDocument = &m.Value
		providerID, name = m.Value.ProviderResourceId, m.Value.Name
	case *satypes.UnknownUnionMember:
		attrs.Resource.UnknownMember = m
	}
	switch c := r.Capabilities.(type) {
	case *satypes.ProviderResourceCapabilitiesMemberGithub:
		attrs.Capabilities.Github = &c.Value
	case *satypes.ProviderResourceCapabilitiesMemberGitlab:
		attrs.Capabilities.Gitlab = &c.Value
	case *satypes.ProviderResourceCapabilitiesMemberBitbucket:
		attrs.Capabilities.Bitbucket = &c.Value
	case *satypes.ProviderResourceCapabilitiesMemberConfluence:
		attrs.Capabilities.Confluence = &c.Value
	case *satypes.UnknownUnionMember:
		attrs.Capabilities.UnknownMember = c
	}
	return attrs, providerID, name
}

// scanSAIntegratedResources stores the repositories and documents each agent
// space was given access to through an integration. ListIntegratedResources
// models ResourceNotFoundException: an agent space deleted after listing is
// skipped.
func scanSAIntegratedResources(ctx context.Context, client securityAgentAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "securityagent:ListIntegratedResources", spaces, isSAParentGone, func(ctx context.Context, space childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := securityagent.NewListIntegratedResourcesPaginator(client, &securityagent.ListIntegratedResourcesInput{AgentSpaceId: &space.id})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, r := range out.IntegratedResourceSummaries {
				attrs, providerID, name := flattenSAIntegratedResource(r)
				integrationID := sv(r.IntegrationId)
				if integrationID == "" || sv(providerID) == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type:     TypeSecurityAgentIntegratedResource,
					NativeID: space.arn + "/integrated-resource/" + integrationID + "/" + *providerID,
					Name:     name, Region: &region,
					AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

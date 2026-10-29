package aws

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53globalresolver"
	r53grtypes "github.com/aws/aws-sdk-go-v2/service/route53globalresolver/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeR53GRAccessSource, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRAccessToken, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRDNSView, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRFirewallDomainList, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRFirewallRule, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRGlobalResolver, Service: "route53globalresolver"})
	registerType(restype.Descriptor{Type: TypeR53GRHostedZoneAssociation, Service: "route53globalresolver"})
	registerService(serviceEntry{
		name:   "aws:route53globalresolver",
		global: true,
		fn:     scanRoute53GlobalResolver,
	})
}

type r53grAPI interface {
	ListGlobalResolvers(context.Context, *route53globalresolver.ListGlobalResolversInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListGlobalResolversOutput, error)
	ListAccessSources(context.Context, *route53globalresolver.ListAccessSourcesInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListAccessSourcesOutput, error)
	ListAccessTokens(context.Context, *route53globalresolver.ListAccessTokensInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListAccessTokensOutput, error)
	ListDNSViews(context.Context, *route53globalresolver.ListDNSViewsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListDNSViewsOutput, error)
	ListFirewallDomainLists(context.Context, *route53globalresolver.ListFirewallDomainListsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListFirewallDomainListsOutput, error)
	ListFirewallRules(context.Context, *route53globalresolver.ListFirewallRulesInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListFirewallRulesOutput, error)
	ListHostedZoneAssociations(context.Context, *route53globalresolver.ListHostedZoneAssociationsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListHostedZoneAssociationsOutput, error)
	ListTagsForResource(context.Context, *route53globalresolver.ListTagsForResourceInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListTagsForResourceOutput, error)
}

const (
	r53grGlobalResolversOp        = "route53globalresolver:ListGlobalResolvers"
	r53grAccessSourcesOp          = "route53globalresolver:ListAccessSources"
	r53grFirewallDomainListsOp    = "route53globalresolver:ListFirewallDomainLists"
	r53grDNSViewsOp               = "route53globalresolver:ListDNSViews"
	r53grFirewallRulesOp          = "route53globalresolver:ListFirewallRules"
	r53grAccessTokensOp           = "route53globalresolver:ListAccessTokens"
	r53grHostedZoneAssociationsOp = "route53globalresolver:ListHostedZoneAssociations"
	r53grTagsOp                   = "route53globalresolver:ListTagsForResource"
	// r53grResolverPageCap is ListGlobalResolvers' documented MaxResults
	// maximum ("Valid range is 1-100"). The other listings document no range,
	// in the model or in their doc, so they send none and take the service's
	// default page size.
	r53grResolverPageCap = 100
)

// r53grGone is the code ListTagsForResource models for a resource that does
// not exist, and the one ListDNSViews, ListFirewallRules, ListAccessTokens
// and ListHostedZoneAssociations model for a parent deleted since it was
// listed.
var r53grGone = []string{"ResourceNotFoundException"}

// scanRoute53GlobalResolver discovers Route53 Global Resolver resources.
// Global service, single regional endpoint for aws accounts
// (route53globalresolver.us-east-2.api.aws), read through homeRegion. It is
// registered global=true, so the dispatcher passes region="" — substitute
// the home to keep Resource.Region and per-op error scopes accurate.
func scanRoute53GlobalResolver(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("route53globalresolver")
	if err != nil {
		return 0, 0, err
	}
	client := route53globalresolver.NewFromConfig(acct.cfg, func(o *route53globalresolver.Options) { o.Region = region })
	return scanR53GRWithClient(ctx, client, acct, region, st, scanID)
}

// scanR53GRWithClient runs every listing as its own phase, one at a time, in
// the order below: a phase's error does not stop the later ones; a failed
// write and ctx's end do (runHostPhases). Every listing writes each page as
// soon as it is read (and its tags read), before the next. The DNS views are
// listed for the global resolvers stored this scan, and the firewall rules,
// access tokens and hosted zone associations for the DNS views stored this
// scan, those parents concurrently. The global resolvers, access sources,
// firewall domain lists, DNS views and access tokens have their tags read
// per row (r53grTags), whose denials are one warning for the whole scan.
func scanR53GRWithClient(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := r53grTags(client).sharingDenials()
	var resolvers, views []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "route53globalresolver", 0, []hostPhase{
		{op: r53grGlobalResolversOp, run: func(p *phaseTotals) (failed bool) {
			resolvers, failed = scanR53GRResolvers(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: r53grAccessSourcesOp, run: func(p *phaseTotals) bool {
			return scanR53GRAccessSources(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: r53grFirewallDomainListsOp, run: func(p *phaseTotals) bool {
			return scanR53GRFirewallDomainLists(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: r53grDNSViewsOp, run: func(p *phaseTotals) (failed bool) {
			views, failed = scanR53GRDNSViews(ctx, client, acct, region, st, scanID, resolvers, tags, p)
			return failed
		}},
		{op: r53grFirewallRulesOp, run: func(p *phaseTotals) bool {
			return scanR53GRFirewallRules(ctx, client, acct, region, st, scanID, views, p)
		}},
		{op: r53grAccessTokensOp, run: func(p *phaseTotals) bool {
			return scanR53GRAccessTokens(ctx, client, acct, region, st, scanID, views, tags, p)
		}},
		{op: r53grHostedZoneAssociationsOp, run: func(p *phaseTotals) bool {
			return scanR53GRHostedZoneAssociations(ctx, client, acct, region, st, scanID, views, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// r53grTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The op returns every tag in one
// response (it takes no page token). The Service Reference lists
// access-source, access-token, dns-view, firewall-domain-list and
// global-resolver under ListTagsForResource and TagResource, and each one's
// Create input takes Tags. Firewall rules and hosted zone associations are
// not read: the Service Reference lists neither as a resource, their Create
// and Associate inputs take no Tags, and neither listing returns an ARN.
// ResourceNotFoundException, the op's only modeled error, drops a resource
// deleted after it was listed.
func r53grTags(client r53grAPI) rowTagReader {
	return rowTagReader{
		op:       r53grTagsOp,
		notFound: r53grGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &route53globalresolver.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanR53GRResolvers stores the global resolvers with their tags, each page
// written as soon as its tags are read, and returns the stored ones, the
// parents of the DNS view phase.
func scanR53GRResolvers(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, r53grGlobalResolversOp, "global resolvers", "Id",
		func(ctx context.Context, token *string) ([]r53grtypes.GlobalResolversItem, *string, error) {
			out, err := client.ListGlobalResolvers(ctx, &route53globalresolver.ListGlobalResolversInput{MaxResults: aws.Int32(r53grResolverPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GlobalResolvers, out.NextToken, nil
		},
		func(r r53grtypes.GlobalResolversItem) (*store.Resource, string, string) {
			return arnRow("global resolvers", r.Arn, aws.String(cmp.Or(sv(r.Name), sv(r.Id))), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRGlobalResolver, NativeID: sv(r.Arn),
				Name: nonEmptyPtr(cmp.Or(sv(r.Name), sv(r.Id))), Region: regionGlobal,
				Status: nonEmptyPtr(string(r.Status)), CreatedAt: tp(r.CreatedAt),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		func(r r53grtypes.GlobalResolversItem) string { return sv(r.Id) },
		tags, p)
}

// scanR53GRAccessSources stores the access sources with their tags, each page
// written as soon as its tags are read.
func scanR53GRAccessSources(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, r53grAccessSourcesOp,
		func(ctx context.Context, token *string) ([]r53grtypes.AccessSourcesItem, *string, error) {
			out, err := client.ListAccessSources(ctx, &route53globalresolver.ListAccessSourcesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessSources, out.NextToken, nil
		},
		func(a r53grtypes.AccessSourcesItem) (*store.Resource, string, string) {
			label := cmp.Or(sv(a.Name), sv(a.Arn))
			return arnRow("access sources", a.Arn, aws.String(cmp.Or(sv(a.Name), sv(a.Id))), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRAccessSource, NativeID: sv(a.Arn),
				Name: &label, Region: regionGlobal,
				Status: nonEmptyPtr(string(a.Status)), CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanR53GRFirewallDomainLists stores the firewall domain lists with their
// tags, each page written as soon as its tags are read. The AWS-managed
// lists have a listing of their own (ListManagedFirewallDomainLists), which
// is not read.
func scanR53GRFirewallDomainLists(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, r53grFirewallDomainListsOp,
		func(ctx context.Context, token *string) ([]r53grtypes.FirewallDomainListsItem, *string, error) {
			out, err := client.ListFirewallDomainLists(ctx, &route53globalresolver.ListFirewallDomainListsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallDomainLists, out.NextToken, nil
		},
		func(f r53grtypes.FirewallDomainListsItem) (*store.Resource, string, string) {
			label := cmp.Or(sv(f.Name), sv(f.Arn))
			return arnRow("firewall domain lists", f.Arn, aws.String(cmp.Or(sv(f.Name), sv(f.Id))), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRFirewallDomainList, NativeID: sv(f.Arn),
				Name: &label, Region: regionGlobal,
				Status: nonEmptyPtr(string(f.Status)), CreatedAt: tp(f.CreatedAt),
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanR53GRDNSViews stores each global resolver's DNS views with their tags,
// each page written as soon as its tags are read; one resolver's failure
// costs the others none of theirs. It returns the stored views, sorted by
// ARN, the parents of the later phases, and reports whether a write failed,
// after which the scan must stop. A view stored without an Id is warned
// about, as stored without its children (storeNestedParentPagesTags).
func scanR53GRDNSViews(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, resolvers []childParent, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, r53grDNSViewsOp, "DNS views", "Id", r53grGone, resolvers,
		func(ctx context.Context, resolverID string, token *string) ([]r53grtypes.DNSViewSummary, *string, error) {
			out, err := client.ListDNSViews(ctx, &route53globalresolver.ListDNSViewsInput{GlobalResolverId: &resolverID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DnsViews, out.NextToken, nil
		},
		func(resolverID string, v r53grtypes.DNSViewSummary) (*store.Resource, string, string) {
			return arnRow("DNS views", v.Arn, aws.String(cmp.Or(sv(v.Name), sv(v.Id), noIdentifier)+" of global resolver "+resolverID), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRDNSView, NativeID: sv(v.Arn),
				Name: nonEmptyPtr(cmp.Or(sv(v.Name), sv(v.Id))), Region: regionGlobal,
				Status: nonEmptyPtr(string(v.Status)), CreatedAt: tp(v.CreatedAt),
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		},
		func(v r53grtypes.DNSViewSummary) string { return sv(v.Id) },
		tags, p)
}

// scanR53GRFirewallRules stores each DNS view's firewall rules, each page
// written as soon as it is read; one view's failure costs the others none of
// theirs (storeEachParentPages). A rule has no ARN, so its NativeID is built
// from its view's Id and its own Id. It reports whether a write failed.
func scanR53GRFirewallRules(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, views []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, r53grFirewallRulesOp, r53grGone, views,
		func(ctx context.Context, viewID string, token *string) ([]r53grtypes.FirewallRulesItem, *string, error) {
			out, err := client.ListFirewallRules(ctx, &route53globalresolver.ListFirewallRulesInput{DnsViewId: &viewID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallRules, out.NextToken, nil
		},
		func(viewID string, r r53grtypes.FirewallRulesItem) (*store.Resource, string, string) {
			id := sv(r.Id)
			if id == "" {
				return nil, "firewall rules have no Id", cmp.Or(sv(r.Name), noIdentifier) + " of DNS view " + viewID
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRFirewallRule, NativeID: arnFor(acct.partition(), "route53globalresolver", region, acct.ID, "dns-view/"+viewID+"/firewall-rule/"+id),
				Name: aws.String(cmp.Or(sv(r.Name), id)), Region: regionGlobal,
				Status: nonEmptyPtr(string(r.Status)), CreatedAt: tp(r.CreatedAt),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		}, p)
}

// scanR53GRAccessTokens stores each DNS view's access tokens with their
// tags, each page written as soon as its tags are read; one view's failure
// costs the others none of theirs (storeEachParentPagesTags).
func scanR53GRAccessTokens(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, views []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, r53grAccessTokensOp, r53grGone, views,
		func(ctx context.Context, viewID string, token *string) ([]r53grtypes.AccessTokenItem, *string, error) {
			out, err := client.ListAccessTokens(ctx, &route53globalresolver.ListAccessTokensInput{DnsViewId: &viewID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessTokens, out.NextToken, nil
		},
		func(viewID string, a r53grtypes.AccessTokenItem) (*store.Resource, string, string) {
			label := cmp.Or(sv(a.Name), sv(a.Arn))
			return arnRow("access tokens", a.Arn, aws.String(cmp.Or(sv(a.Name), sv(a.Id), noIdentifier)+" of DNS view "+viewID), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRAccessToken, NativeID: sv(a.Arn),
				Name: &label, Region: regionGlobal,
				Status: nonEmptyPtr(string(a.Status)), CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanR53GRHostedZoneAssociations stores the hosted zone associations of
// each DNS view, listed by the view's ARN, each page written as soon as it is
// read; one view's failure costs the others none of theirs
// (storeEachParentPages). An association has no ARN, so its NativeID is
// built under its view's. It reports whether a write failed.
func scanR53GRHostedZoneAssociations(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string, views []childParent, p *phaseTotals) bool {
	byARN := make([]childParent, len(views))
	for i, v := range views {
		byARN[i] = childParent{id: v.arn, arn: v.arn}
	}
	return storeEachParentPages(ctx, st, acct, region, r53grHostedZoneAssociationsOp, r53grGone, byARN,
		func(ctx context.Context, viewARN string, token *string) ([]r53grtypes.HostedZoneAssociationSummary, *string, error) {
			out, err := client.ListHostedZoneAssociations(ctx, &route53globalresolver.ListHostedZoneAssociationsInput{ResourceArn: &viewARN, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.HostedZoneAssociations, out.NextToken, nil
		},
		func(viewARN string, h r53grtypes.HostedZoneAssociationSummary) (*store.Resource, string, string) {
			id := sv(h.Id)
			if id == "" {
				return nil, "hosted zone associations have no Id", cmp.Or(sv(h.HostedZoneName), noIdentifier) + " of " + viewARN
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRHostedZoneAssociation, NativeID: fmt.Sprintf("%s/hosted-zone-association/%s", viewARN, id),
				Name: aws.String(cmp.Or(sv(h.HostedZoneName), sv(h.HostedZoneId))), Region: regionGlobal,
				Status: nonEmptyPtr(string(h.Status)), CreatedAt: tp(h.CreatedAt),
				AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			}, "", ""
		}, p)
}

package aws

import (
	"context"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rtbfabric"
	rtbtypes "github.com/aws/aws-sdk-go-v2/service/rtbfabric/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRTBFabricRequesterGateway, Service: "rtbfabric"})
	registerType(restype.Descriptor{Type: TypeRTBFabricResponderGateway, Service: "rtbfabric"})
	registerType(restype.Descriptor{Type: TypeRTBFabricLink, Service: "rtbfabric"})
	registerType(restype.Descriptor{Type: TypeRTBFabricLinkRoutingRule, Service: "rtbfabric"})
	registerService(serviceEntry{
		name: "aws:rtbfabric",
		fn:   scanRTBFabric,
	})
}

type rtbfabricAPI interface {
	ListRequesterGateways(context.Context, *rtbfabric.ListRequesterGatewaysInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListRequesterGatewaysOutput, error)
	ListResponderGateways(context.Context, *rtbfabric.ListResponderGatewaysInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListResponderGatewaysOutput, error)
	ListLinks(context.Context, *rtbfabric.ListLinksInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListLinksOutput, error)
	ListLinkRoutingRules(context.Context, *rtbfabric.ListLinkRoutingRulesInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListLinkRoutingRulesOutput, error)
	ListTagsForResource(context.Context, *rtbfabric.ListTagsForResourceInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListTagsForResourceOutput, error)
}

const (
	rtbTagsOp = "rtbfabric:ListTagsForResource"
	// rtbPageCap is the MaxResults sent to ListRequesterGateways,
	// ListResponderGateways and ListLinkRoutingRules: the maximum of the
	// 1-10 range the model gives each. ListLinks models no range, so it is
	// sent none.
	rtbPageCap = 10
)

// rtbGone is the not-found code ListLinks, ListLinkRoutingRules and
// ListTagsForResource model: for a listing, its parent was deleted since it
// was listed; for a tag read, the resource was.
var rtbGone = []string{"ResourceNotFoundException"}

// rtbLinkPath is the ids a stored link's routing rule listing is sent, which
// takes no ARN: the gateway the link was listed under, and the link's own.
type rtbLinkPath struct{ gateway, link string }

// rtbLinkPaths holds the path of each link listed this scan, by its ARN.
// Links are built on concurrent per-gateway reads, so it is locked.
type rtbLinkPaths struct {
	mu    sync.Mutex
	byARN map[string]rtbLinkPath
}

func (p *rtbLinkPaths) noteRTBLink(arn string, path rtbLinkPath) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byARN[arn] = path
}

func (p *rtbLinkPaths) rtbLinkOf(arn string) rtbLinkPath {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byARN[arn]
}

// scanRTBFabric discovers the RTB Fabric (real-time bidding fabric) requester
// and responder gateways of one region, the links of each gateway, and the
// routing rules of each link. Every listing runs as its own phase, one at a
// time: a phase's error does not stop the later ones; a failed write and
// ctx's end do (runHostPhases). Each child phase reads the parents stored
// this scan, those parents concurrently.
//
// No listing returns an ARN, so every NativeID is built in the Service
// Reference's ARN format: gateway/{GatewayId} for both gateway kinds,
// gateway/{GatewayId}/link/{LinkId} for a link (the gateway it was listed
// under), and that plus /routing-rule/{RuleId} for a rule. The gateways and
// rules have their tags read per row (rtbTags), whose denials are one
// warning for the whole scan; a link's listed element carries its tags.
//
// InboundExternalLink and OutboundExternalLink are not scanned: the SDK has
// only Get/Create/Delete for them, no listing.
func scanRTBFabric(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := rtbfabric.NewFromConfig(acct.cfg, func(o *rtbfabric.Options) { o.Region = region })
	tags := rtbTags(client).sharingDenials()
	paths := &rtbLinkPaths{byARN: map[string]rtbLinkPath{}}
	var requesters, responders, links []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "rtbfabric", 0, []hostPhase{
		{op: "rtbfabric:ListRequesterGateways", run: func(p *phaseTotals) (failed bool) {
			requesters, failed = scanRTBRequesterGateways(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "rtbfabric:ListResponderGateways", run: func(p *phaseTotals) (failed bool) {
			responders, failed = scanRTBResponderGateways(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "rtbfabric:ListLinks", run: func(p *phaseTotals) (failed bool) {
			gateways := append(append([]childParent{}, requesters...), responders...)
			links, failed = scanRTBLinks(ctx, client, acct, region, st, scanID, gateways, paths, p)
			return failed
		}},
		{op: "rtbfabric:ListLinkRoutingRules", run: func(p *phaseTotals) bool {
			return scanRTBLinkRoutingRules(ctx, client, acct, region, st, scanID, links, paths, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// rtbTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, its ARN. The Service Reference lists RequesterGateway,
// ResponderGateway and LinkRoutingRule (with Link and the external links)
// under ListTagsForResource, each with aws:ResourceTag, and each Create*
// input carries tags. The op does not page its tags. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
//
// No element of these listings names an owning account, and every NativeID
// is built with this account's id: the gateways come from this account's own
// listings, and a routing rule is listed under a link of one of them. So
// every row is taken as this account's own, and a denial or not-found is
// never read as another account's resource. Whether a link to another
// account's gateway changes that is unverified live; links have no tag read.
func rtbTags(client rtbfabricAPI) rowTagReader {
	return rowTagReader{
		op:       rtbTagsOp,
		notFound: rtbGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &rtbfabric.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// rtbGatewayARN is a gateway's ARN in the Service Reference format, the same
// for requester and responder gateways.
func rtbGatewayARN(partition, region, acctID, gatewayID string) string {
	return arnFor(partition, "rtbfabric", region, acctID, "gateway/"+gatewayID)
}

func rtbLinkARN(partition, region, acctID, gatewayID, linkID string) string {
	return arnFor(partition, "rtbfabric", region, acctID, "gateway/"+gatewayID+"/link/"+linkID)
}

// rtbGatewayRow is the row of a listed gateway id, stored under its ARN and
// named by the id. An empty id is not stored: every such id would share one
// NativeID. what names the listing's gateways.
func rtbGatewayRow(acct *account, region, scanID, typ, what, id string) (*store.Resource, string, string) {
	if id == "" {
		return nil, what + " have an empty id", noIdentifier
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: rtbGatewayARN(acct.partition(), region, acct.ID, id),
		Name: &id, Region: &region,
		AttributesJSON: mustJSON(map[string]string{"GatewayId": id}), DiscoveredBy: scanID,
	}, "", ""
}

// scanRTBRequesterGateways stores the requester gateways with their tags, a
// page at a time, and returns the stored ones, the parents of the link
// phase, each keyed by its id. It reports whether a write failed, after
// which the scan must stop.
func scanRTBRequesterGateways(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "rtbfabric:ListRequesterGateways", "requester gateways", "GatewayId",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListRequesterGateways(ctx, &rtbfabric.ListRequesterGatewaysInput{MaxResults: sdkaws.Int32(rtbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GatewayIds, out.NextToken, nil
		},
		func(id string) (*store.Resource, string, string) {
			return rtbGatewayRow(acct, region, scanID, TypeRTBFabricRequesterGateway, "requester gateways", id)
		},
		func(id string) string { return id },
		tags, p)
}

// scanRTBResponderGateways is scanRTBRequesterGateways for the responder
// gateways.
func scanRTBResponderGateways(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "rtbfabric:ListResponderGateways", "responder gateways", "GatewayId",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListResponderGateways(ctx, &rtbfabric.ListResponderGatewaysInput{MaxResults: sdkaws.Int32(rtbPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GatewayIds, out.NextToken, nil
		},
		func(id string) (*store.Resource, string, string) {
			return rtbGatewayRow(acct, region, scanID, TypeRTBFabricResponderGateway, "responder gateways", id)
		},
		func(id string) string { return id },
		tags, p)
}

// scanRTBLinks stores each gateway's links, each page written as soon as it
// is read; one gateway's failure costs the others none of theirs. A link is
// stored under the ARN of the gateway it was listed under, with the tags its
// listed element carries, so no tag read is made. It returns the stored
// links, the parents of the routing rule phase, each keyed by its LinkId
// with its path noted in paths. It reports whether a write failed, after
// which the scan must stop.
func scanRTBLinks(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent, paths *rtbLinkPaths, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, "rtbfabric:ListLinks", "links", "LinkId", rtbGone, gateways,
		func(ctx context.Context, gatewayID string, token *string) ([]rtbtypes.ListLinksResponseStructure, *string, error) {
			out, err := client.ListLinks(ctx, &rtbfabric.ListLinksInput{GatewayId: &gatewayID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Links, out.NextToken, nil
		},
		func(gatewayID string, l rtbtypes.ListLinksResponseStructure) (*store.Resource, string, string) {
			lid := sv(l.LinkId)
			if lid == "" {
				return nil, "links have no LinkId", gatewayID
			}
			arn := rtbLinkARN(acct.partition(), region, acct.ID, gatewayID, lid)
			paths.noteRTBLink(arn, rtbLinkPath{gateway: gatewayID, link: lid})
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRTBFabricLink, NativeID: arn,
				Name: &lid, Region: &region, Status: nonEmptyPtr(string(l.Status)),
				AttributesJSON: mustJSON(l), TagsJSON: mapTagsJSON(l.Tags), DiscoveredBy: scanID,
			}, "", ""
		},
		func(l rtbtypes.ListLinksResponseStructure) string { return sv(l.LinkId) },
		rowTagReader{}, p)
}

// scanRTBLinkRoutingRules stores each stored link's routing rules with their
// tags, each page written as soon as its tags are read; one link's failure
// costs the others none of theirs. A rule carries no ARN; it is stored under
// its link's ARN + /routing-rule/{RuleId}. It reports whether a write failed,
// after which the scan must stop.
func scanRTBLinkRoutingRules(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string, links []childParent, paths *rtbLinkPaths, tags rowTagReader, p *phaseTotals) bool {
	byARN := make([]childParent, len(links))
	for i, l := range links {
		byARN[i] = childParent{id: l.arn, arn: l.arn}
	}
	return storeEachParentPagesTags(ctx, st, acct, region, "rtbfabric:ListLinkRoutingRules", rtbGone, byARN,
		func(ctx context.Context, linkARN string, token *string) ([]rtbtypes.LinkRoutingRuleSummary, *string, error) {
			l := paths.rtbLinkOf(linkARN)
			out, err := client.ListLinkRoutingRules(ctx, &rtbfabric.ListLinkRoutingRulesInput{
				GatewayId: &l.gateway, LinkId: &l.link, MaxResults: sdkaws.Int32(rtbPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Rules, out.NextToken, nil
		},
		func(linkARN string, r rtbtypes.LinkRoutingRuleSummary) (*store.Resource, string, string) {
			rid := sv(r.RuleId)
			if rid == "" {
				return nil, "routing rules have no RuleId", linkARN
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRTBFabricLinkRoutingRule, NativeID: linkARN + "/routing-rule/" + rid,
				Name: &rid, Region: &region, Status: nonEmptyPtr(string(r.Status)),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

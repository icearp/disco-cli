package aws

import (
	"cmp"
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	ci "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity"
	citypes "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity/types"
	cp "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines"
	cptypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines/types"
	cm "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging"
	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cv "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice"
	cvtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeChimeAppInstance, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeAppInstanceBot, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeAppInstanceUser, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeChannelFlow, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaPipeline, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaInsightsPipelineConfiguration, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaPipelineKinesisVideoStreamPool, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeSipMediaApplication, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceConnector, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceProfileDomain, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceProfile, Service: "chime", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeChimeSipRule, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceConnectorGroup, Service: "chime"})
	registerService(serviceEntry{
		name: "aws:chime",
		fn:   scanChime,
	})
}

// chimeChannelFlowAttrs embeds the native channel-flow summary plus the owning
// app-instance ARN (ListChannelFlows is called per app-instance but does not
// echo it back), so the resolver can wire channel-flow → app-instance.
type chimeChannelFlowAttrs struct {
	cmtypes.ChannelFlowSummary
	AppInstanceArn *string `json:"appInstanceArn,omitempty"`
}

type chimeIdentityAPI interface {
	ListAppInstances(context.Context, *ci.ListAppInstancesInput, ...func(*ci.Options)) (*ci.ListAppInstancesOutput, error)
	ListAppInstanceBots(context.Context, *ci.ListAppInstanceBotsInput, ...func(*ci.Options)) (*ci.ListAppInstanceBotsOutput, error)
	ListAppInstanceUsers(context.Context, *ci.ListAppInstanceUsersInput, ...func(*ci.Options)) (*ci.ListAppInstanceUsersOutput, error)
	ListTagsForResource(context.Context, *ci.ListTagsForResourceInput, ...func(*ci.Options)) (*ci.ListTagsForResourceOutput, error)
}

type chimeMessagingAPI interface {
	ListChannelFlows(context.Context, *cm.ListChannelFlowsInput, ...func(*cm.Options)) (*cm.ListChannelFlowsOutput, error)
	ListTagsForResource(context.Context, *cm.ListTagsForResourceInput, ...func(*cm.Options)) (*cm.ListTagsForResourceOutput, error)
}

type chimeMediaPipelinesAPI interface {
	ListMediaPipelines(context.Context, *cp.ListMediaPipelinesInput, ...func(*cp.Options)) (*cp.ListMediaPipelinesOutput, error)
	ListMediaInsightsPipelineConfigurations(context.Context, *cp.ListMediaInsightsPipelineConfigurationsInput, ...func(*cp.Options)) (*cp.ListMediaInsightsPipelineConfigurationsOutput, error)
	ListMediaPipelineKinesisVideoStreamPools(context.Context, *cp.ListMediaPipelineKinesisVideoStreamPoolsInput, ...func(*cp.Options)) (*cp.ListMediaPipelineKinesisVideoStreamPoolsOutput, error)
	ListTagsForResource(context.Context, *cp.ListTagsForResourceInput, ...func(*cp.Options)) (*cp.ListTagsForResourceOutput, error)
}

type chimeVoiceAPI interface {
	ListSipMediaApplications(context.Context, *cv.ListSipMediaApplicationsInput, ...func(*cv.Options)) (*cv.ListSipMediaApplicationsOutput, error)
	ListVoiceConnectors(context.Context, *cv.ListVoiceConnectorsInput, ...func(*cv.Options)) (*cv.ListVoiceConnectorsOutput, error)
	ListVoiceProfileDomains(context.Context, *cv.ListVoiceProfileDomainsInput, ...func(*cv.Options)) (*cv.ListVoiceProfileDomainsOutput, error)
	GetVoiceProfileDomain(context.Context, *cv.GetVoiceProfileDomainInput, ...func(*cv.Options)) (*cv.GetVoiceProfileDomainOutput, error)
	ListVoiceProfiles(context.Context, *cv.ListVoiceProfilesInput, ...func(*cv.Options)) (*cv.ListVoiceProfilesOutput, error)
	ListSipRules(context.Context, *cv.ListSipRulesInput, ...func(*cv.Options)) (*cv.ListSipRulesOutput, error)
	ListVoiceConnectorGroups(context.Context, *cv.ListVoiceConnectorGroupsInput, ...func(*cv.Options)) (*cv.ListVoiceConnectorGroupsOutput, error)
	ListTagsForResource(context.Context, *cv.ListTagsForResourceInput, ...func(*cv.Options)) (*cv.ListTagsForResourceOutput, error)
}

// The hosts scanChime calls, one per Chime SDK package.
const (
	chimeIdentityHost = iota
	chimeMessagingHost
	chimeMediaPipelinesHost
	chimeVoiceHost
)

// chimeNoPrimaryHost is passed to runHostPhases as the primary host and names
// none, so no host's NXDOMAIN on its first phase reports the region
// unavailable. No one host's region set contains all the others' in the SDK
// endpoint tables: identity and messaging list eu-central-1 and us-east-1,
// which media pipelines and voice both list too, but media pipelines lists
// ap-south-1 and voice does not, and voice lists eu-west-1 and media
// pipelines does not. So whichever host were primary, a region served by
// another host alone would be reported unavailable, or, once that host stored
// rows, the primary's NXDOMAIN would fail the scan.
//
// The cost: a region where every host answers NXDOMAIN is reported OK with no
// rows rather than unavailable. With region scoping on, the dispatcher skips
// regions no Chime SDK endpoint table lists before this runs; with
// --scope-regions=false, or where the SSM catalog lists a region the tables do
// not, such a region reaches this scan. No rows are lost. It is accepted
// because runHostPhases cannot tell a host it never called from one that
// answered empty: with identity absent the channel-flow phase has no parents
// and calls nothing, and a rule of "nothing stored and only NXDOMAIN errors"
// would report unavailable a region where voice answered empty lists.
const chimeNoPrimaryHost = -1

// Each listing sends the maximum of its op's MaxResults range trait: 50 for
// the identity and messaging ops, 100 for the media-pipeline and voice ops,
// except ListMediaPipelines, whose doc gives "Valid Range: 1 - 99" against
// its trait's 1-100, so it is sent the smaller.
const (
	chimeAppPageCap            int32 = 50
	chimePageCap               int32 = 100
	chimeMediaPipelinesPageCap int32 = 99
)

// chimeNotFound is the not-found code the media-pipeline and voice tag ops,
// ListVoiceProfiles and GetVoiceProfileDomain model. The identity and
// messaging tag ops and their listings model none.
var chimeNotFound = []string{"NotFoundException"}

// chimeUnauthorized is UnauthorizedClientException (HTTP 401, "The client is
// not currently authorized to make the request"), which every Chime SDK op
// this scanner calls models and isAccessDenied does not match.
// ForbiddenException, the other denial code they model, it does. Every call
// takes it for a denial (isChimeDenied), listings and Gets as well as tag
// reads, so it means the same thing wherever it is answered.
var chimeUnauthorized = []string{"UnauthorizedClientException"}

// isChimeDenied reports whether err denies a Chime SDK call.
func isChimeDenied(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, chimeUnauthorized...)
}

// The tag readers send ListTagsForResource the row's NativeID, the ARN its
// list element carries; the Service Reference lists every type they read
// as a resource of the op. Each package's op serves its own types and
// answers with every tag at once (its output has no page token).

func chimeIdentityTags(client chimeIdentityAPI) rowTagReader {
	return rowTagReader{
		op:     "chime:ListTagsForResource",
		denied: chimeUnauthorized,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &ci.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t citypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

func chimeMessagingTags(client chimeMessagingAPI) rowTagReader {
	return rowTagReader{
		op:     "chime:ListTagsForResource",
		denied: chimeUnauthorized,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &cm.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t cmtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

func chimeMediaPipelinesTags(client chimeMediaPipelinesAPI) rowTagReader {
	return rowTagReader{
		op:       "chime:ListTagsForResource",
		notFound: chimeNotFound,
		denied:   chimeUnauthorized,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &cp.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t cptypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

func chimeVoiceTags(client chimeVoiceAPI) rowTagReader {
	return rowTagReader{
		op:       "chime:ListTagsForResource",
		notFound: chimeNotFound,
		denied:   chimeUnauthorized,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &cv.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t cvtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanChime discovers the Chime SDK surface across four sub-SDKs, each row
// with its tags where its type has them: identity (app-instances +
// per-instance bots/users), messaging (per-instance channel flows),
// media-pipelines, and voice (SIP apps, voice connectors, voice-profile
// domains + profiles, SIP rules, voice connector groups). Channels
// (data-plane, needs ChimeBearer user identity) and meetings (ephemeral, no
// list API) aren't scanned.
//
// Every listing runs as its own phase, a child listing after its parents'
// with the parents that phase stored: a phase's error does not stop the
// later ones; a failed write and ctx's end do (runHostPhases). The phases run
// one at a time, so readEachKey's own bound is the bound on the tag reads in
// flight.
func scanChime(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// The SDK neither lists ThrottledClientException, the Chime SDK models'
	// HTTP 429 throttle, among its throttle codes nor retries a 429 by status,
	// so without this a single throttle fails the call unretried. It gets
	// standard backoff only: adaptive mode's client-side rate limiting does not
	// learn from a code added this way.
	const throttled = "ThrottledClientException"
	idClient := ci.NewFromConfig(acct.cfg, func(o *ci.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, throttled)
	})
	msgClient := cm.NewFromConfig(acct.cfg, func(o *cm.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, throttled)
	})
	mpClient := cp.NewFromConfig(acct.cfg, func(o *cp.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, throttled)
	})
	vClient := cv.NewFromConfig(acct.cfg, func(o *cv.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, throttled)
	})

	var (
		idTags                = chimeIdentityTags(idClient)
		mpTags                = chimeMediaPipelinesTags(mpClient)
		vTags                 = chimeVoiceTags(vClient)
		appInstances, domains []childParent
		storeWriteFailed      bool
	)
	return runHostPhases(ctx, st, acct, region, "chime", chimeNoPrimaryHost, []hostPhase{
		{op: "chime:ListAppInstances", host: chimeIdentityHost, run: func(p *phaseTotals) bool {
			appInstances, storeWriteFailed = scanChimeAppInstances(ctx, idClient, acct, region, st, scanID, idTags, p)
			return storeWriteFailed
		}},
		{op: "chime:ListAppInstanceBots", host: chimeIdentityHost, run: func(p *phaseTotals) bool {
			return scanChimeAppInstanceBots(ctx, idClient, acct, region, st, scanID, appInstances, idTags, p)
		}},
		{op: "chime:ListAppInstanceUsers", host: chimeIdentityHost, run: func(p *phaseTotals) bool {
			return scanChimeAppInstanceUsers(ctx, idClient, acct, region, st, scanID, appInstances, idTags, p)
		}},
		{op: "chime:ListChannelFlows", host: chimeMessagingHost, run: func(p *phaseTotals) bool {
			return scanChimeChannelFlows(ctx, msgClient, acct, region, st, scanID, appInstances, chimeMessagingTags(msgClient), p)
		}},
		{op: "chime:ListMediaPipelines", host: chimeMediaPipelinesHost, run: func(p *phaseTotals) bool {
			return scanChimeMediaPipelines(ctx, mpClient, acct, region, st, scanID, mpTags, p)
		}},
		{op: "chime:ListMediaInsightsPipelineConfigurations", host: chimeMediaPipelinesHost, run: func(p *phaseTotals) bool {
			return scanChimeMediaInsightsPipelineConfigurations(ctx, mpClient, acct, region, st, scanID, mpTags, p)
		}},
		{op: "chime:ListMediaPipelineKinesisVideoStreamPools", host: chimeMediaPipelinesHost, run: func(p *phaseTotals) bool {
			return scanChimeKinesisVideoStreamPools(ctx, mpClient, acct, region, st, scanID, mpTags, p)
		}},
		{op: "chime:ListSipMediaApplications", host: chimeVoiceHost, run: func(p *phaseTotals) bool {
			return scanChimeSipMediaApplications(ctx, vClient, acct, region, st, scanID, vTags, p)
		}},
		{op: "chime:ListVoiceConnectors", host: chimeVoiceHost, run: func(p *phaseTotals) bool {
			return scanChimeVoiceConnectors(ctx, vClient, acct, region, st, scanID, vTags, p)
		}},
		{op: "chime:ListVoiceProfileDomains", host: chimeVoiceHost, run: func(p *phaseTotals) bool {
			domains, storeWriteFailed = scanChimeVoiceProfileDomains(ctx, vClient, acct, region, st, scanID, vTags, p)
			return storeWriteFailed
		}},
		countedPhase(chimeVoiceHost, "chime:ListVoiceProfiles", func() (int, int, error) {
			return scanChimeVoiceProfiles(ctx, vClient, acct, region, st, scanID, domains)
		}),
		countedPhase(chimeVoiceHost, "chime:ListSipRules", func() (int, int, error) {
			return scanChimeSipRules(ctx, vClient, acct, region, st, scanID)
		}),
		countedPhase(chimeVoiceHost, "chime:ListVoiceConnectorGroups", func() (int, int, error) {
			return scanChimeVoiceConnectorGroups(ctx, vClient, acct, region, st, scanID)
		}),
	})
}

// isChimeHostGap reports whether err is how a Chime SDK endpoint answers a
// call it does not serve to this account here: an operation not deployed
// (isOpNotDeployed), or a ForbiddenException saying "This feature is not
// available" or "AWS account is not enabled", or with an empty body
// (isClosedToNewCustomers). The sub-SDKs have independent regional and
// account availability, and a denial a permission change could fix carries a
// message naming the action, so these are availability, not IAM denials.
//
// isOpNotDeployed includes isHTTP404, so any 404 counts, a modeled
// NotFoundException included: ListVoiceProfileDomains and ListSipRules model
// one, and a first page answered with it reads as an empty listing, not an
// error. Neither is sent an id (ListSipRules' optional SipMediaApplicationId
// filter is not sent), so no resource the scan named can be the one not
// found.
func isChimeHostGap(err error) bool {
	return isOpNotDeployed(err) ||
		isAccessDeniedWithMessage(err, "feature is not available") ||
		isAccessDeniedWithMessage(err, "account is not enabled") ||
		isClosedToNewCustomers(err)
}

// chimePages is page with a first page answered by isChimeHostGap read as an
// empty listing: the op is not served here (firstPageGapPages).
func chimePages[Item any](page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return firstPageGapPages(isChimeHostGap, page)
}

// chimeRow is the row of a listed element whose NativeID is its ARN. An
// element without an ARN is not stored; what names the elements in the
// warning and key the element.
func chimeRow(acct *account, region, scanID, typ, what, arn, key string, name *string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(key, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// chimeTokenListTags is storeTokenListTags with a Chime denial
// (isChimeDenied) ending the listing, keeping the pages read before it, and
// warned about under op once the rows are written (listDenial); what names
// the listed items.
func chimeTokenListTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) bool {
	d := listDenial{isDenied: isChimeDenied, unread: what + " on later pages"}
	storeWriteFailed := storeTokenListTags(ctx, st, acct, region, op, deniedPages(&d, page), row, tags, p)
	if !storeWriteFailed {
		d.warn(st, op, acct, region)
	}
	return storeWriteFailed
}

// chimeTokenList is chimeTokenListTags for an untagged type (storeTokenList).
func chimeTokenList[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
) (int, int, error) {
	d := listDenial{isDenied: isChimeDenied, unread: what + " on later pages"}
	total, inserted, err := storeTokenList(ctx, st, acct, region, op, deniedPages(&d, page), row)
	if !errors.Is(err, store.ErrStoreWrite) {
		d.warn(st, op, acct, region)
	}
	return total, inserted, err
}

// scanChimeAppInstances stores the app instances and returns them as the
// parents of the bot, user and channel-flow listings, keyed by their ARN,
// which those ops take.
func scanChimeAppInstances(ctx context.Context, client chimeIdentityAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	const op = "chime:ListAppInstances"
	d := listDenial{isDenied: isChimeDenied, unread: "app instances on later pages and their children"}
	parents, storeWriteFailed := storeParentListTags(ctx, st, acct, region, op, "app instances", "AppInstanceArn",
		deniedPages(&d, chimePages(func(ctx context.Context, token *string) ([]citypes.AppInstanceSummary, *string, error) {
			out, err := client.ListAppInstances(ctx, &ci.ListAppInstancesInput{NextToken: token, MaxResults: sdkaws.Int32(chimeAppPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AppInstances, out.NextToken, nil
		})),
		func(a citypes.AppInstanceSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeAppInstance, "app instances", sv(a.AppInstanceArn), sv(a.Name), a.Name, a)
		},
		func(a citypes.AppInstanceSummary) string { return sv(a.AppInstanceArn) },
		tags, p)
	if !storeWriteFailed {
		d.warn(st, op, acct, region)
	}
	return parents, storeWriteFailed
}

func scanChimeAppInstanceBots(ctx context.Context, client chimeIdentityAPI, acct *account, region string, st *store.Store, scanID string, appInstances []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, storeWriteFailed := storeEachParentTagsDeniedBy(ctx, st, acct, region, "chime:ListAppInstanceBots", nil, appInstances, isChimeDenied,
		func(ctx context.Context, aiARN string, token *string) ([]citypes.AppInstanceBotSummary, *string, error) {
			out, err := client.ListAppInstanceBots(ctx, &ci.ListAppInstanceBotsInput{AppInstanceArn: sdkaws.String(aiARN), NextToken: token, MaxResults: sdkaws.Int32(chimeAppPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AppInstanceBots, out.NextToken, nil
		},
		func(_ string, b citypes.AppInstanceBotSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeAppInstanceBot, "app instance bots", sv(b.AppInstanceBotArn), sv(b.Name), b.Name, b)
		}, tags, p)
	return storeWriteFailed
}

func scanChimeAppInstanceUsers(ctx context.Context, client chimeIdentityAPI, acct *account, region string, st *store.Store, scanID string, appInstances []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, storeWriteFailed := storeEachParentTagsDeniedBy(ctx, st, acct, region, "chime:ListAppInstanceUsers", nil, appInstances, isChimeDenied,
		func(ctx context.Context, aiARN string, token *string) ([]citypes.AppInstanceUserSummary, *string, error) {
			out, err := client.ListAppInstanceUsers(ctx, &ci.ListAppInstanceUsersInput{AppInstanceArn: sdkaws.String(aiARN), NextToken: token, MaxResults: sdkaws.Int32(chimeAppPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AppInstanceUsers, out.NextToken, nil
		},
		func(_ string, u citypes.AppInstanceUserSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeAppInstanceUser, "app instance users", sv(u.AppInstanceUserArn), sv(u.Name), u.Name, u)
		}, tags, p)
	return storeWriteFailed
}

// scanChimeChannelFlows lists each stored app instance's channel flows. It is
// the first call to the messaging host, so each instance's first page goes
// through chimePages.
func scanChimeChannelFlows(ctx context.Context, client chimeMessagingAPI, acct *account, region string, st *store.Store, scanID string, appInstances []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, storeWriteFailed := storeEachParentTagsDeniedBy(ctx, st, acct, region, "chime:ListChannelFlows", nil, appInstances, isChimeDenied,
		func(ctx context.Context, aiARN string, token *string) ([]cmtypes.ChannelFlowSummary, *string, error) {
			return chimePages(func(ctx context.Context, token *string) ([]cmtypes.ChannelFlowSummary, *string, error) {
				out, err := client.ListChannelFlows(ctx, &cm.ListChannelFlowsInput{AppInstanceArn: sdkaws.String(aiARN), NextToken: token, MaxResults: sdkaws.Int32(chimeAppPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.ChannelFlows, out.NextToken, nil
			})(ctx, token)
		},
		func(aiARN string, cf cmtypes.ChannelFlowSummary) (*store.Resource, string, string) {
			attrs := chimeChannelFlowAttrs{ChannelFlowSummary: cf, AppInstanceArn: sdkaws.String(aiARN)}
			return chimeRow(acct, region, scanID, TypeChimeChannelFlow, "channel flows", sv(cf.ChannelFlowArn), sv(cf.Name), cf.Name, attrs)
		}, tags, p)
	return storeWriteFailed
}

func scanChimeMediaPipelines(ctx context.Context, client chimeMediaPipelinesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return chimeTokenListTags(ctx, st, acct, region, "chime:ListMediaPipelines", "media pipelines",
		chimePages(func(ctx context.Context, token *string) ([]cptypes.MediaPipelineSummary, *string, error) {
			out, err := client.ListMediaPipelines(ctx, &cp.ListMediaPipelinesInput{NextToken: token, MaxResults: sdkaws.Int32(chimeMediaPipelinesPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MediaPipelines, out.NextToken, nil
		}),
		func(m cptypes.MediaPipelineSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeMediaPipeline, "media pipelines", sv(m.MediaPipelineArn), sv(m.MediaPipelineId), nil, m)
		}, tags, p)
}

func scanChimeMediaInsightsPipelineConfigurations(ctx context.Context, client chimeMediaPipelinesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return chimeTokenListTags(ctx, st, acct, region, "chime:ListMediaInsightsPipelineConfigurations", "media insights pipeline configurations",
		chimePages(func(ctx context.Context, token *string) ([]cptypes.MediaInsightsPipelineConfigurationSummary, *string, error) {
			out, err := client.ListMediaInsightsPipelineConfigurations(ctx, &cp.ListMediaInsightsPipelineConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MediaInsightsPipelineConfigurations, out.NextToken, nil
		}),
		func(c cptypes.MediaInsightsPipelineConfigurationSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeMediaInsightsPipelineConfiguration, "media insights pipeline configurations",
				sv(c.MediaInsightsPipelineConfigurationArn), sv(c.MediaInsightsPipelineConfigurationName), c.MediaInsightsPipelineConfigurationName, c)
		}, tags, p)
}

func scanChimeKinesisVideoStreamPools(ctx context.Context, client chimeMediaPipelinesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return chimeTokenListTags(ctx, st, acct, region, "chime:ListMediaPipelineKinesisVideoStreamPools", "Kinesis video stream pools",
		chimePages(func(ctx context.Context, token *string) ([]cptypes.KinesisVideoStreamPoolSummary, *string, error) {
			out, err := client.ListMediaPipelineKinesisVideoStreamPools(ctx, &cp.ListMediaPipelineKinesisVideoStreamPoolsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.KinesisVideoStreamPools, out.NextToken, nil
		}),
		func(k cptypes.KinesisVideoStreamPoolSummary) (*store.Resource, string, string) {
			return chimeRow(acct, region, scanID, TypeChimeMediaPipelineKinesisVideoStreamPool, "Kinesis video stream pools", sv(k.PoolArn), sv(k.PoolName), k.PoolName, k)
		}, tags, p)
}

func scanChimeSipMediaApplications(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return chimeTokenListTags(ctx, st, acct, region, "chime:ListSipMediaApplications", "SIP media applications",
		chimePages(func(ctx context.Context, token *string) ([]cvtypes.SipMediaApplication, *string, error) {
			out, err := client.ListSipMediaApplications(ctx, &cv.ListSipMediaApplicationsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SipMediaApplications, out.NextToken, nil
		}),
		func(s cvtypes.SipMediaApplication) (*store.Resource, string, string) {
			r, why, key := chimeRow(acct, region, scanID, TypeChimeSipMediaApplication, "SIP media applications", sv(s.SipMediaApplicationArn), cmp.Or(sv(s.SipMediaApplicationId), sv(s.Name)), s.Name, s)
			if r != nil {
				r.CreatedAt = tp(s.CreatedTimestamp)
			}
			return r, why, key
		}, tags, p)
}

func scanChimeVoiceConnectors(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return chimeTokenListTags(ctx, st, acct, region, "chime:ListVoiceConnectors", "voice connectors",
		chimePages(func(ctx context.Context, token *string) ([]cvtypes.VoiceConnector, *string, error) {
			out, err := client.ListVoiceConnectors(ctx, &cv.ListVoiceConnectorsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VoiceConnectors, out.NextToken, nil
		}),
		func(vc cvtypes.VoiceConnector) (*store.Resource, string, string) {
			r, why, key := chimeRow(acct, region, scanID, TypeChimeVoiceConnector, "voice connectors", sv(vc.VoiceConnectorArn), cmp.Or(sv(vc.VoiceConnectorId), sv(vc.Name)), vc.Name, vc)
			if r != nil {
				r.CreatedAt = tp(vc.CreatedTimestamp)
			}
			return r, why, key
		}, tags, p)
}

// scanChimeVoiceProfileDomains stores the voice-profile domains, each with
// its GetVoiceProfileDomain body as attributes (it adds the domain's KMS
// key), and returns them as the parents of the voice-profile listing, keyed
// by their id. A domain the Get reports not found was deleted since the
// listing and is dropped; any other Get failure keeps the list element
// (readRowDetails). A domain listed without an id is stored as its list
// element, warned about as stored without its voice profiles.
func scanChimeVoiceProfileDomains(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	const op = "chime:ListVoiceProfileDomains"
	ids := map[*store.Resource]string{}
	d := &detailedRows{getOp: "chime:GetVoiceProfileDomain", what: "voice profile domain(s)", notFound: chimeNotFound, isDenied: isChimeDenied, tags: tags}
	denial := listDenial{isDenied: isChimeDenied, unread: "voice profile domains on later pages and their voice profiles"}
	total, inserted, domains, err := storeParentListWith(ctx, st, acct, region, op, "voice profile domains", "VoiceProfileDomainId",
		deniedPages(&denial, chimePages(func(ctx context.Context, token *string) ([]cvtypes.VoiceProfileDomainSummary, *string, error) {
			out, err := client.ListVoiceProfileDomains(ctx, &cv.ListVoiceProfileDomainsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VoiceProfileDomains, out.NextToken, nil
		})),
		func(v cvtypes.VoiceProfileDomainSummary) (*store.Resource, string, string) {
			r, why, key := chimeRow(acct, region, scanID, TypeChimeVoiceProfileDomain, "voice profile domains", sv(v.VoiceProfileDomainArn), cmp.Or(sv(v.VoiceProfileDomainId), sv(v.Name)), v.Name, v)
			if r != nil {
				r.CreatedAt = tp(v.CreatedTimestamp)
				ids[r] = sv(v.VoiceProfileDomainId)
			}
			return r, why, key
		},
		func(v cvtypes.VoiceProfileDomainSummary) string { return sv(v.VoiceProfileDomainId) },
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			if ids[r] == "" {
				return nil, nil
			}
			out, err := client.GetVoiceProfileDomain(ctx, &cv.GetVoiceProfileDomainInput{VoiceProfileDomainId: sdkaws.String(ids[r])})
			if err != nil || out.VoiceProfileDomain == nil {
				return nil, err
			}
			return *out.VoiceProfileDomain, nil
		}))
	storeWriteFailed := d.addPhase(p, op, total, inserted, err)
	if !storeWriteFailed {
		denial.warn(st, op, acct, region)
	}
	return domains, storeWriteFailed
}

func scanChimeVoiceProfiles(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent) (int, int, error) {
	return storeTokenEachParentDeniedBy(ctx, st, acct, region, "chime:ListVoiceProfiles", chimeNotFound, childParentIDs(domains), isChimeDenied,
		func(ctx context.Context, domainID string, token *string) ([]cvtypes.VoiceProfileSummary, *string, error) {
			out, err := client.ListVoiceProfiles(ctx, &cv.ListVoiceProfilesInput{VoiceProfileDomainId: sdkaws.String(domainID), NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VoiceProfiles, out.NextToken, nil
		},
		func(_ string, v cvtypes.VoiceProfileSummary) (*store.Resource, string, string) {
			r, why, key := chimeRow(acct, region, scanID, TypeChimeVoiceProfile, "voice profiles", sv(v.VoiceProfileArn), sv(v.VoiceProfileId), nil, v)
			if r != nil {
				r.CreatedAt = tp(v.CreatedTimestamp)
			}
			return r, why, key
		})
}

// chimeSipRuleNativeID builds a SIP rule's NativeID. AWS issues no SIP-rule ARN
// (SipRule carries only SipRuleId, and the Service Reference lists no sip-rule
// resource), so the id is synthesized in the chime ARN shape.
func chimeSipRuleNativeID(partition, region, accountID, sipRuleID string) string {
	return arnFor(partition, "chime", region, accountID, "sip-rule/"+sipRuleID)
}

// scanChimeSipRules stores the SIP rules without tags: the Service Reference
// lists no sip-rule resource, so ListTagsForResource has no ARN to take.
func scanChimeSipRules(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return chimeTokenList(ctx, st, acct, region, "chime:ListSipRules", "SIP rules",
		chimePages(func(ctx context.Context, token *string) ([]cvtypes.SipRule, *string, error) {
			out, err := client.ListSipRules(ctx, &cv.ListSipRulesInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SipRules, out.NextToken, nil
		}),
		func(r cvtypes.SipRule) (*store.Resource, string, string) {
			id := sv(r.SipRuleId)
			if id == "" {
				return nil, "SIP rules have no SipRuleId", cmp.Or(sv(r.Name), "(a SIP rule with no name)")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeSipRule, NativeID: chimeSipRuleNativeID(acct.partition(), region, acct.ID, id),
				Name: r.Name, Region: &region, CreatedAt: tp(r.CreatedTimestamp),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanChimeVoiceConnectorGroups stores the voice connector groups, keyed by
// the VoiceConnectorGroupArn the API returns, without tags: neither
// ListTagsForResource nor TagResource lists a voice-connector-group resource
// in the Service Reference, and CreateVoiceConnectorGroup takes no tags.
func scanChimeVoiceConnectorGroups(ctx context.Context, client chimeVoiceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return chimeTokenList(ctx, st, acct, region, "chime:ListVoiceConnectorGroups", "voice connector groups",
		chimePages(func(ctx context.Context, token *string) ([]cvtypes.VoiceConnectorGroup, *string, error) {
			out, err := client.ListVoiceConnectorGroups(ctx, &cv.ListVoiceConnectorGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(chimePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VoiceConnectorGroups, out.NextToken, nil
		}),
		func(g cvtypes.VoiceConnectorGroup) (*store.Resource, string, string) {
			r, why, key := chimeRow(acct, region, scanID, TypeChimeVoiceConnectorGroup, "voice connector groups", sv(g.VoiceConnectorGroupArn), cmp.Or(sv(g.VoiceConnectorGroupId), sv(g.Name)), g.Name, g)
			if r != nil {
				r.CreatedAt = tp(g.CreatedTimestamp)
			}
			return r, why, key
		})
}

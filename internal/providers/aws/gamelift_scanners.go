package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGameLiftAlias, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftBuild, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftCompute, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftContainerFleet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftContainerGroupDefinition, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftFleet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftGameServerGroup, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftGameSessionQueue, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftLocation, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftMatchmakingConfiguration, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftMatchmakingRuleSet, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftScalingPolicy, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftScript, Service: "gamelift"})
	registerType(restype.Descriptor{Type: TypeGameLiftVpcPeeringConnection, Service: "gamelift"})
	registerService(serviceEntry{
		name: "aws:gamelift",
		fn:   scanGameLift,
	})
}

type gameLiftAPI interface {
	ListAliases(context.Context, *gamelift.ListAliasesInput, ...func(*gamelift.Options)) (*gamelift.ListAliasesOutput, error)
	ListBuilds(context.Context, *gamelift.ListBuildsInput, ...func(*gamelift.Options)) (*gamelift.ListBuildsOutput, error)
	ListContainerFleets(context.Context, *gamelift.ListContainerFleetsInput, ...func(*gamelift.Options)) (*gamelift.ListContainerFleetsOutput, error)
	ListContainerGroupDefinitions(context.Context, *gamelift.ListContainerGroupDefinitionsInput, ...func(*gamelift.Options)) (*gamelift.ListContainerGroupDefinitionsOutput, error)
	ListFleets(context.Context, *gamelift.ListFleetsInput, ...func(*gamelift.Options)) (*gamelift.ListFleetsOutput, error)
	DescribeFleetAttributes(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	ListGameServerGroups(context.Context, *gamelift.ListGameServerGroupsInput, ...func(*gamelift.Options)) (*gamelift.ListGameServerGroupsOutput, error)
	DescribeGameSessionQueues(context.Context, *gamelift.DescribeGameSessionQueuesInput, ...func(*gamelift.Options)) (*gamelift.DescribeGameSessionQueuesOutput, error)
	ListLocations(context.Context, *gamelift.ListLocationsInput, ...func(*gamelift.Options)) (*gamelift.ListLocationsOutput, error)
	DescribeMatchmakingConfigurations(context.Context, *gamelift.DescribeMatchmakingConfigurationsInput, ...func(*gamelift.Options)) (*gamelift.DescribeMatchmakingConfigurationsOutput, error)
	DescribeMatchmakingRuleSets(context.Context, *gamelift.DescribeMatchmakingRuleSetsInput, ...func(*gamelift.Options)) (*gamelift.DescribeMatchmakingRuleSetsOutput, error)
	ListScripts(context.Context, *gamelift.ListScriptsInput, ...func(*gamelift.Options)) (*gamelift.ListScriptsOutput, error)
	ListCompute(context.Context, *gamelift.ListComputeInput, ...func(*gamelift.Options)) (*gamelift.ListComputeOutput, error)
	DescribeScalingPolicies(context.Context, *gamelift.DescribeScalingPoliciesInput, ...func(*gamelift.Options)) (*gamelift.DescribeScalingPoliciesOutput, error)
	DescribeVpcPeeringConnections(context.Context, *gamelift.DescribeVpcPeeringConnectionsInput, ...func(*gamelift.Options)) (*gamelift.DescribeVpcPeeringConnectionsOutput, error)
	ListTagsForResource(context.Context, *gamelift.ListTagsForResourceInput, ...func(*gamelift.Options)) (*gamelift.ListTagsForResourceOutput, error)
}

const (
	glListTagsOp    = "gamelift:ListTagsForResource"
	glListAliasesOp = "gamelift:ListAliases"

	// glDescribeFleetsBatch is how many fleet ids one DescribeFleetAttributes
	// call is sent. The op's docs give no number (a request over the limit
	// fails, its message naming the limit); 50 is the size the scanner sent
	// before, not verified live.
	glDescribeFleetsBatch = 50
)

// glNotFoundCodes is the not-found code ListTagsForResource models.
var glNotFoundCodes = []string{"NotFoundException"}

// glUnauthorizedCodes is UnauthorizedException ("the client failed
// authentication"), which most GameLift ops model and accessDeniedCodes does
// not list. Every GameLift call takes it for a denial, as accessDeniedCodes
// takes AuthFailure: a model's error list does not bound the codes a call can
// answer with (AccessDeniedException is modeled by none of them), so an op
// that does not model it is treated alike.
var glUnauthorizedCodes = []string{"UnauthorizedException"}

// isGameLiftDenied reports whether err denies a GameLift call.
func isGameLiftDenied(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, glUnauthorizedCodes...)
}

func scanGameLift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := gamelift.NewFromConfig(acct.cfg, func(o *gamelift.Options) { o.Region = region })
	return scanGameLiftIn(ctx, client, acct, region, st, scanID)
}

// scanGameLiftIn runs the phases one at a time. Each phase adds its counts
// and errors to p and reports whether a store write failed. A phase's error
// does not stop the later ones; a failed write does, since every later write
// would fail too, and so does ctx's end, reported as one error. The fleet
// children phases run last, over the fleets the fleet phase stored. The
// phases run one at a time, so readRowTags's own bound is the bound on the
// tag reads in flight.
//
// Every phase calls the same endpoint, so an alias listing that stored
// nothing and could not reach it (isEndpointUnreachable) is returned alone
// and no later phase runs: they would fail the same way, and the dispatcher
// must see the bare error to classify it (NXDOMAIN in a region without
// GameLift is region-unavailable). Past that point the endpoint answered or
// rows were stored, so every NXDOMAIN is a failure of this scan
// (lateNXDOMAIN).
func scanGameLiftIn(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p    phaseTotals
		tags = glTags(client, glNotFoundCodes)
		// fleets is the attributes of the fleets the fleet phase stored.
		fleets []gltypes.FleetAttributes
	)
	counted := func(op string, run func() (int, int, error)) func(p *phaseTotals) bool {
		return func(p *phaseTotals) bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}
	}
	phases := []func(p *phaseTotals) (storeWriteFailed bool){
		func(p *phaseTotals) bool { return scanGLAliases(ctx, client, acct, region, st, scanID, tags, p) },
		func(p *phaseTotals) bool { return scanGLBuilds(ctx, client, acct, region, st, scanID, tags, p) },
		func(p *phaseTotals) bool {
			return scanGLContainerFleets(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) bool { return scanGLContainerGroupDefs(ctx, client, acct, region, st, scanID, p) },
		func(p *phaseTotals) (stop bool) {
			fleets, stop = scanGLFleets(ctx, client, acct, region, st, scanID, tags, p)
			return stop
		},
		func(p *phaseTotals) bool {
			return scanGLGameServerGroups(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanGLGameSessionQueues(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) bool { return scanGLLocations(ctx, client, acct, region, st, scanID, tags, p) },
		func(p *phaseTotals) bool {
			return scanGLMatchmakingConfigs(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) bool {
			return scanGLMatchmakingRuleSets(ctx, client, acct, region, st, scanID, tags, p)
		},
		func(p *phaseTotals) bool { return scanGLScripts(ctx, client, acct, region, st, scanID, tags, p) },
		counted("gamelift:ListCompute", func() (int, int, error) {
			return scanGLComputes(ctx, client, acct, region, st, scanID, fleets)
		}),
		counted("gamelift:DescribeScalingPolicies", func() (int, int, error) {
			return scanGLScalingPolicies(ctx, client, acct, region, st, scanID, fleets)
		}),
		counted("gamelift:DescribeVpcPeeringConnections", func() (int, int, error) {
			return scanGLVpcPeeringConnections(ctx, client, acct, region, st, scanID, fleets)
		}),
	}
	notRun := 0
	for n, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - n
			break
		}
		total, before := p.total, len(p.errs)
		if run(&p) {
			break
		}
		if n == 0 && p.total == total {
			if err := glListingUnreachable(p.errs[before:]); err != nil {
				return 0, 0, err
			}
		}
		for i := before; i < len(p.errs); i++ {
			p.errs[i].err = lateNXDOMAIN(p.errs[i].op, p.errs[i].err)
		}
	}
	p.foldStop(ctx, "gamelift", notRun)
	return p.result(st, acct, region)
}

// glListingUnreachable returns the alias listing's error among errs, the
// alias phase's errors, when it says the endpoint could not be reached; nil
// otherwise. A tag read's error does not count: the listing reached the
// endpoint.
func glListingUnreachable(errs phaseErrs) error {
	for _, f := range errs {
		if f.op == glListAliasesOp && isEndpointUnreachable(f.err) {
			return f.err
		}
	}
	return nil
}

// glTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries; the Service Reference lists
// the action for every GameLift type whose rows it is used for, and the
// model gives ResourceARN no pattern. The op answers with every tag at once
// (no page token). notFound is the codes that drop a row (glNotFoundCodes, or nil
// to keep it). UnauthorizedException is warned about as a denial
// (glUnauthorizedCodes), although the op's model does not list it.
func glTags(client gameLiftAPI, notFound []string) rowTagReader {
	return rowTagReader{
		op:       glListTagsOp,
		notFound: notFound,
		denied:   glUnauthorizedCodes,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &gamelift.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t gltypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// glRow is the row of a listed GameLift item stored under its ARN, item
// verbatim as the attributes.
func glRow(acct *account, region, scanID, typ, arn, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// glList is one GameLift listing: its op label and the answers its op's
// model lists besides items and a hard error.
type glList struct {
	op string
	// regionGap: the op models UnsupportedRegionException. Refusing the first
	// page with it means the feature is not offered in this region, and the
	// listing is empty; after a page was read it is returned as an error.
	regionGap bool
	// denied is the first denial the listing ended on.
	denied error
}

// deniedBy reports whether err denies l's op, keeping the first such error.
func (l *glList) deniedBy(err error) bool {
	if !isGameLiftDenied(err) {
		return false
	}
	if l.denied == nil {
		l.denied = err
	}
	return true
}

// warnDenied warns once when the listing ended on a denial. Call it only
// after the rows read were written.
func (l *glList) warnDenied(st *store.Store, acct *account, region string) {
	if l.denied != nil {
		_ = skipIfAccessDenied(st, l.op, acct.ID, region, l.denied)
	}
}

// glPages is page with l's modeled answers handled: a region gap on the first
// page ends the listing empty, a denial ends it keeping the pages read
// before it (deniedBy).
func glPages[Item any](l *glList, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		switch {
		case err == nil:
			return items, next, nil
		case token == nil && l.regionGap && isAPIErrorCode(err, "UnsupportedRegionException"):
			return nil, nil, nil
		case l.deniedBy(err):
			return nil, nil, nil
		}
		return nil, nil, err
	}
}

// glStoreList stores the rows of an account-wide GameLift listing with their
// tags (storeTokenListTags): the pages read before a failed page are kept,
// a repeated NextToken ends the listing as an error, and items row cannot
// store are warned about once. It reports whether the write failed.
func glStoreList[Item any](
	ctx context.Context, st *store.Store, acct *account, region string, l glList,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	if storeTokenListTags(ctx, st, acct, region, l.op, glPages(&l, page), row, tags, p) {
		return true
	}
	l.warnDenied(st, acct, region)
	return false
}

// The listings below send no Limit where the op's model and docs give no
// maximum, leaving the page size to the service; the others send the
// maximum their model or docs give.

func scanGLAliases(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: glListAliasesOp},
		func(ctx context.Context, token *string) ([]gltypes.Alias, *string, error) {
			out, err := client.ListAliases(ctx, &gamelift.ListAliasesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Aliases, out.NextToken, nil
		},
		func(a gltypes.Alias) (*store.Resource, string, string) {
			if sv(a.AliasArn) == "" {
				return nil, "aliases have an empty AliasArn", cmp.Or(sv(a.Name), sv(a.AliasId), "(an alias with no name)")
			}
			return glRow(acct, region, scanID, TypeGameLiftAlias, sv(a.AliasArn), cmp.Or(sv(a.Name), sv(a.AliasId)), a), "", ""
		}, tags, p)
}

func scanGLBuilds(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListBuilds"},
		func(ctx context.Context, token *string) ([]gltypes.Build, *string, error) {
			out, err := client.ListBuilds(ctx, &gamelift.ListBuildsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Builds, out.NextToken, nil
		},
		func(b gltypes.Build) (*store.Resource, string, string) {
			if sv(b.BuildArn) == "" {
				return nil, "builds have an empty BuildArn", cmp.Or(sv(b.Name), sv(b.BuildId), "(a build with no name)")
			}
			return glRow(acct, region, scanID, TypeGameLiftBuild, sv(b.BuildArn), cmp.Or(sv(b.Name), sv(b.BuildId)), b), "", ""
		}, tags, p)
}

func scanGLContainerFleets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListContainerFleets", regionGap: true},
		func(ctx context.Context, token *string) ([]gltypes.ContainerFleet, *string, error) {
			out, err := client.ListContainerFleets(ctx, &gamelift.ListContainerFleetsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ContainerFleets, out.NextToken, nil
		},
		func(f gltypes.ContainerFleet) (*store.Resource, string, string) {
			if sv(f.FleetArn) == "" {
				return nil, "container fleets have an empty FleetArn", cmp.Or(sv(f.FleetId), "(a container fleet with no id)")
			}
			return glRow(acct, region, scanID, TypeGameLiftContainerFleet, sv(f.FleetArn), cmp.Or(sv(f.FleetId), sv(f.FleetArn)), f), "", ""
		}, tags, p)
}

// scanGLContainerGroupDefs stores the container group definitions with their
// tags. The element's ARN ends in the definition's version
// (".../containergroupdefinition/{name}:{version}" in the model's doc),
// while the Service Reference formats the tag op's resource without one.
// That the op accepts the versioned form is not verified live, nor what it
// answers if it does not, so a not-found answer keeps the row and is returned as an
// error rather than dropping a definition the listing just returned.
func scanGLContainerGroupDefs(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListContainerGroupDefinitions", regionGap: true},
		func(ctx context.Context, token *string) ([]gltypes.ContainerGroupDefinition, *string, error) {
			out, err := client.ListContainerGroupDefinitions(ctx, &gamelift.ListContainerGroupDefinitionsInput{NextToken: token, Limit: sdkaws.Int32(100)})
			if err != nil {
				return nil, nil, err
			}
			return out.ContainerGroupDefinitions, out.NextToken, nil
		},
		func(d gltypes.ContainerGroupDefinition) (*store.Resource, string, string) {
			if sv(d.ContainerGroupDefinitionArn) == "" {
				return nil, "container group definitions have an empty ContainerGroupDefinitionArn", cmp.Or(sv(d.Name), "(a container group definition with no name)")
			}
			arn := sv(d.ContainerGroupDefinitionArn)
			return glRow(acct, region, scanID, TypeGameLiftContainerGroupDefinition, arn, cmp.Or(sv(d.Name), arn), d), "", ""
		}, glTags(client, nil), p)
}

// scanGLFleets stores every fleet with its tags and returns the attributes
// of the fleets stored, for the fleet children phases; none after a failed
// write. ListFleets names the fleets; DescribeFleetAttributes, which omits
// a fleet it no longer finds, gives their attributes. A failed call of
// either keeps the fleets read before it.
func scanGLFleets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (fleets []gltypes.FleetAttributes, storeWriteFailed bool) {
	list := glList{op: "gamelift:ListFleets"}
	ids, listErr := readTokenPages(ctx, glPages(&list, func(ctx context.Context, token *string) ([]string, *string, error) {
		out, err := client.ListFleets(ctx, &gamelift.ListFleetsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.FleetIds, out.NextToken, nil
	}))
	if listErr != nil {
		p.errs.add(list.op, fmt.Errorf("%s: %w", list.op, listErr))
	}
	var (
		describe  = glList{op: "gamelift:DescribeFleetAttributes"}
		described []gltypes.FleetAttributes
		descErr   error
	)
	for batch := range slices.Chunk(ids, glDescribeFleetsBatch) {
		out, err := client.DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{FleetIds: batch})
		if err != nil {
			if !describe.deniedBy(err) {
				descErr = err
			}
			break
		}
		described = append(described, out.FleetAttributes...)
	}
	var skips notStoredSkips
	rows := buildListedRows(described, func(f gltypes.FleetAttributes) (*store.Resource, string, string) {
		if sv(f.FleetArn) == "" {
			return nil, "fleets have an empty FleetArn", cmp.Or(sv(f.Name), sv(f.FleetId), "(a fleet with no name)")
		}
		return glRow(acct, region, scanID, TypeGameLiftFleet, sv(f.FleetArn), cmp.Or(sv(f.Name), sv(f.FleetId)), f), "", ""
	}, nil, &skips)
	var gone glGoneRows
	if storeRowsTags(ctx, st, acct, region, describe.op, rows, descErr, func() {
		list.warnDenied(st, acct, region)
		describe.warnDenied(st, acct, region)
		skips.warn(st, describe.op, acct, region)
	}, gone.track(tags), p) {
		return nil, true
	}
	for _, f := range described {
		if arn := sv(f.FleetArn); arn != "" && !gone.has(arn) {
			fleets = append(fleets, f)
		}
	}
	return fleets, false
}

// glGoneRows records the rows whose tag read answered with one of the
// reader's notFound codes. It is coupled to readRowTags, which drops a row on
// exactly that test (isAPIErrorCode against the same tags.notFound list), so
// the two decide alike as long as readRowTags keeps that rule.
// storeRowsTags does not return the rows it stored, and the fleet children
// phases must get only the fleets stored.
type glGoneRows struct {
	mu   sync.Mutex
	arns map[string]bool
}

// track is tags with each not-found answer recorded.
func (g *glGoneRows) track(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		t, err := read(ctx, r)
		if isAPIErrorCode(err, tags.notFound...) {
			g.mu.Lock()
			if g.arns == nil {
				g.arns = map[string]bool{}
			}
			g.arns[r.NativeID] = true
			g.mu.Unlock()
		}
		return t, err
	}
	return tags
}

// has reports whether the row of arn was dropped as not found. Call it only
// after the reads ended.
func (g *glGoneRows) has(arn string) bool { return g.arns[arn] }

func scanGLGameServerGroups(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListGameServerGroups"},
		func(ctx context.Context, token *string) ([]gltypes.GameServerGroup, *string, error) {
			out, err := client.ListGameServerGroups(ctx, &gamelift.ListGameServerGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.GameServerGroups, out.NextToken, nil
		},
		func(g gltypes.GameServerGroup) (*store.Resource, string, string) {
			if sv(g.GameServerGroupArn) == "" {
				return nil, "game server groups have an empty GameServerGroupArn", cmp.Or(sv(g.GameServerGroupName), "(a game server group with no name)")
			}
			arn := sv(g.GameServerGroupArn)
			return glRow(acct, region, scanID, TypeGameLiftGameServerGroup, arn, cmp.Or(sv(g.GameServerGroupName), arn), g), "", ""
		}, tags, p)
}

func scanGLGameSessionQueues(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:DescribeGameSessionQueues"},
		func(ctx context.Context, token *string) ([]gltypes.GameSessionQueue, *string, error) {
			out, err := client.DescribeGameSessionQueues(ctx, &gamelift.DescribeGameSessionQueuesInput{NextToken: token, Limit: sdkaws.Int32(50)})
			if err != nil {
				return nil, nil, err
			}
			return out.GameSessionQueues, out.NextToken, nil
		},
		func(q gltypes.GameSessionQueue) (*store.Resource, string, string) {
			if sv(q.GameSessionQueueArn) == "" {
				return nil, "game session queues have an empty GameSessionQueueArn", cmp.Or(sv(q.Name), "(a game session queue with no name)")
			}
			arn := sv(q.GameSessionQueueArn)
			return glRow(acct, region, scanID, TypeGameLiftGameSessionQueue, arn, cmp.Or(sv(q.Name), arn), q), "", ""
		}, tags, p)
}

// scanGLLocations stores the custom locations with their tags. The listing
// also returns the AWS locations, which carry no LocationArn: the model's
// pattern for it admits only "location/custom-" ARNs. They are not stored,
// and not warned about.
func scanGLLocations(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListLocations"},
		func(ctx context.Context, token *string) ([]gltypes.LocationModel, *string, error) {
			out, err := client.ListLocations(ctx, &gamelift.ListLocationsInput{NextToken: token, Limit: sdkaws.Int32(200)})
			if err != nil {
				return nil, nil, err
			}
			custom := slices.DeleteFunc(out.Locations, func(l gltypes.LocationModel) bool { return sv(l.LocationArn) == "" })
			return custom, out.NextToken, nil
		},
		func(l gltypes.LocationModel) (*store.Resource, string, string) {
			arn := sv(l.LocationArn)
			return glRow(acct, region, scanID, TypeGameLiftLocation, arn, cmp.Or(sv(l.LocationName), arn), l), "", ""
		}, tags, p)
}

func scanGLMatchmakingConfigs(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:DescribeMatchmakingConfigurations", regionGap: true},
		func(ctx context.Context, token *string) ([]gltypes.MatchmakingConfiguration, *string, error) {
			out, err := client.DescribeMatchmakingConfigurations(ctx, &gamelift.DescribeMatchmakingConfigurationsInput{NextToken: token, Limit: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.Configurations, out.NextToken, nil
		},
		func(c gltypes.MatchmakingConfiguration) (*store.Resource, string, string) {
			if sv(c.ConfigurationArn) == "" {
				return nil, "matchmaking configurations have an empty ConfigurationArn", cmp.Or(sv(c.Name), "(a matchmaking configuration with no name)")
			}
			arn := sv(c.ConfigurationArn)
			return glRow(acct, region, scanID, TypeGameLiftMatchmakingConfiguration, arn, cmp.Or(sv(c.Name), arn), c), "", ""
		}, tags, p)
}

func scanGLMatchmakingRuleSets(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:DescribeMatchmakingRuleSets", regionGap: true},
		func(ctx context.Context, token *string) ([]gltypes.MatchmakingRuleSet, *string, error) {
			out, err := client.DescribeMatchmakingRuleSets(ctx, &gamelift.DescribeMatchmakingRuleSetsInput{NextToken: token, Limit: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuleSets, out.NextToken, nil
		},
		func(r gltypes.MatchmakingRuleSet) (*store.Resource, string, string) {
			if sv(r.RuleSetArn) == "" {
				return nil, "matchmaking rule sets have an empty RuleSetArn", cmp.Or(sv(r.RuleSetName), "(a matchmaking rule set with no name)")
			}
			arn := sv(r.RuleSetArn)
			return glRow(acct, region, scanID, TypeGameLiftMatchmakingRuleSet, arn, cmp.Or(sv(r.RuleSetName), arn), r), "", ""
		}, tags, p)
}

func scanGLScripts(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return glStoreList(ctx, st, acct, region, glList{op: "gamelift:ListScripts"},
		func(ctx context.Context, token *string) ([]gltypes.Script, *string, error) {
			out, err := client.ListScripts(ctx, &gamelift.ListScriptsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Scripts, out.NextToken, nil
		},
		func(s gltypes.Script) (*store.Resource, string, string) {
			if sv(s.ScriptArn) == "" {
				return nil, "scripts have an empty ScriptArn", cmp.Or(sv(s.Name), sv(s.ScriptId), "(a script with no name)")
			}
			return glRow(acct, region, scanID, TypeGameLiftScript, sv(s.ScriptArn), cmp.Or(sv(s.Name), sv(s.ScriptId)), s), "", ""
		}, tags, p)
}

// gameLiftFleetParents returns the fleets keep accepts as child fan-out
// parents, leaving out TERMINATED fleets, which FleetStatus documents as no
// longer existing.
func gameLiftFleetParents(fleets []gltypes.FleetAttributes, keep func(gltypes.FleetAttributes) bool) []childParent {
	var parents []childParent
	for _, f := range fleets {
		if f.Status == gltypes.FleetStatusTerminated || !keep(f) {
			continue
		}
		parents = append(parents, childParent{id: sv(f.FleetId), arn: sv(f.FleetArn)})
	}
	return parents
}

// scanGLComputes stores the computes registered to Anywhere fleets
// (RegisterCompute). The computes ListCompute returns for an EC2 fleet are its
// managed instances, which GameLift launches and terminates as the fleet
// scales, so EC2 fleets are not queried.
func scanGLComputes(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	parents := gameLiftFleetParents(fleets, func(f gltypes.FleetAttributes) bool { return f.ComputeType == gltypes.ComputeTypeAnywhere })
	// ListCompute models no not-found error, so no fleet's error is skipped.
	return glChildFanOut(ctx, st, acct, region, "gamelift:ListCompute", parents, func(error) bool { return false },
		func(ctx context.Context, fleet childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := gamelift.NewListComputePaginator(client, &gamelift.ListComputeInput{FleetId: &fleet.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.ComputeList {
					arn := sv(c.ComputeArn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGameLiftCompute, NativeID: arn,
						Name: c.ComputeName, Region: &region, Status: nonEmptyPtr(string(c.ComputeStatus)),
						CreatedAt: tp(c.CreationTime), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanGLScalingPolicies stores the scaling policies of every fleet but
// Anywhere fleets: the op documents EC2 fleets only, and a fleet reporting no
// ComputeType (the field is optional) is queried rather than assumed not EC2.
// A policy has no ARN; a fleet holds one policy per name (PutScalingPolicy),
// so the policy is keyed {fleetARN}/scaling-policy/{name}. Called without a
// Location, the op returns the policies of every fleet location, while
// PutScalingPolicy takes no location, so only the home-region entry (or one
// naming no location) is kept.
func scanGLScalingPolicies(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	parents := gameLiftFleetParents(fleets, func(f gltypes.FleetAttributes) bool { return f.ComputeType != gltypes.ComputeTypeAnywhere })
	return glChildFanOut(ctx, st, acct, region, "gamelift:DescribeScalingPolicies", parents, isGameLiftNotFound,
		func(ctx context.Context, fleet childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := gamelift.NewDescribeScalingPoliciesPaginator(client, &gamelift.DescribeScalingPoliciesInput{FleetId: &fleet.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, p := range out.ScalingPolicies {
					name := sv(p.Name)
					if name == "" || p.Status == gltypes.ScalingStatusTypeDeleted {
						continue
					}
					if loc := sv(p.Location); loc != "" && loc != region {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeGameLiftScalingPolicy, NativeID: fleet.arn + "/scaling-policy/" + name,
						Name: &name, Region: &region, Status: nonEmptyPtr(string(p.Status)),
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// glChildFanOut is childFanOut with every denial (isGameLiftDenied, which
// also takes UnauthorizedException) handled here rather than by childFanOut,
// so both codes follow one rule: a fleet so answered is skipped, and one
// warning for op covers every such fleet, sent only after a successful
// write.
func glChildFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var denied skipTally
	total, inserted, err := childFanOut(ctx, st, acct, region, op, parents, isSkip,
		func(ctx context.Context, fleet childParent) ([]*store.Resource, error) {
			rows, err := list(ctx, fleet)
			if isGameLiftDenied(err) {
				denied.skipped(fleet.id, err)
				return nil, nil
			}
			return rows, err
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		denied.warnDeniedIn(st, op, acct, region, "fleets' listings")
	}
	return total, inserted, err
}

// isGameLiftNotFound matches a fleet deleted between DescribeFleetAttributes
// and the child call.
func isGameLiftNotFound(err error) bool {
	return isAPIErrorCode(err, "NotFoundException")
}

// scanGLVpcPeeringConnections stores GameLift's record of each peering
// connection between a fleet's GameLift-managed VPC and a VPC of this account,
// keyed {fleetARN}/vpc-peering-connection/{id}. The connection is not keyed as
// the EC2 peering connection: ec2:DescribeVpcPeeringConnections stores that as
// aws:ec2:vpc-peering-connection, and sharing its NativeID would merge the two
// rows into one version chain. A record naming its fleet only by FleetId gets
// the fleet ARN built from the id, the same shape the fleet row is keyed by. A
// pending connection with no id yet is skipped. The op lists the whole account's connections at once; each is
// linked under its fleet only when the fleet phase stored that fleet.
func scanGLVpcPeeringConnections(ctx context.Context, client gameLiftAPI, acct *account, region string, st *store.Store, scanID string, fleets []gltypes.FleetAttributes) (int, int, error) {
	out, err := client.DescribeVpcPeeringConnections(ctx, &gamelift.DescribeVpcPeeringConnectionsInput{})
	if err != nil {
		if isGameLiftDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "gamelift:DescribeVpcPeeringConnections", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("gamelift:DescribeVpcPeeringConnections: %w", err)
	}
	stored := make(map[string]bool, len(fleets))
	for _, f := range fleets {
		stored[sv(f.FleetArn)] = true
	}
	var (
		batch     []*store.Resource
		fleetARNs []string // fleetARNs[i] is batch[i]'s fleet
	)
	for _, c := range out.VpcPeeringConnections {
		fleetARN, id := sv(c.FleetArn), sv(c.VpcPeeringConnectionId)
		if fleetARN == "" && sv(c.FleetId) != "" {
			fleetARN = gameliftFleetARN(acct.partition(), region, acct.ID, sv(c.FleetId))
		}
		if fleetARN == "" || id == "" {
			continue
		}
		var status *string
		if c.Status != nil {
			status = nonEmptyPtr(sv(c.Status.Code))
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeGameLiftVpcPeeringConnection, NativeID: fleetARN + "/vpc-peering-connection/" + id,
			Name: &id, Region: &region, Status: status, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
		fleetARNs = append(fleetARNs, fleetARN)
	}
	total, inserted, err := upsertBatch(st, batch, "gamelift vpc-peering-connections")
	if err != nil {
		return 0, 0, err
	}
	var pairs [][2]string
	for i, r := range batch {
		if stored[fleetARNs[i]] {
			pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, fleetARNs[i])})
		}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure gamelift vpc-peering-connections: %w", err)
	}
	return total, inserted, nil
}

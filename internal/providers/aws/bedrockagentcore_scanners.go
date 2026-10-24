package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bacdata "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreAPIKeyCredentialProvider, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreBrowser, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreBrowserCustom, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreBrowserProfile, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreCodeInterpreter, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreCodeInterpreterCustom, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreConfigurationBundle, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreDataset, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreEvaluator, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreHarness, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreHarnessEndpoint, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePolicyGeneration, Service: "bedrockagentcore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreRegistry, Service: "bedrockagentcore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreRegistryRecord, Service: "bedrockagentcore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreGateway, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreGatewayTarget, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreMemory, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreOAuth2CredentialProvider, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreOnlineEvaluationConfig, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePaymentManager, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePaymentConnector, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePaymentCredentialProvider, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePolicy, Service: "bedrockagentcore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCorePolicyEngine, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreRuntime, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreRuntimeEndpoint, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreWorkloadIdentity, Service: "bedrockagentcore"})
	registerService(serviceEntry{
		name: "aws:bedrockagentcore",
		fn:   scanBedrockAgentCore,
	})
}

type bedrockAgentCoreAPI interface {
	ListApiKeyCredentialProviders(context.Context, *bac.ListApiKeyCredentialProvidersInput, ...func(*bac.Options)) (*bac.ListApiKeyCredentialProvidersOutput, error)
	ListBrowsers(context.Context, *bac.ListBrowsersInput, ...func(*bac.Options)) (*bac.ListBrowsersOutput, error)
	ListBrowserProfiles(context.Context, *bac.ListBrowserProfilesInput, ...func(*bac.Options)) (*bac.ListBrowserProfilesOutput, error)
	ListCodeInterpreters(context.Context, *bac.ListCodeInterpretersInput, ...func(*bac.Options)) (*bac.ListCodeInterpretersOutput, error)
	ListEvaluators(context.Context, *bac.ListEvaluatorsInput, ...func(*bac.Options)) (*bac.ListEvaluatorsOutput, error)
	ListGateways(context.Context, *bac.ListGatewaysInput, ...func(*bac.Options)) (*bac.ListGatewaysOutput, error)
	ListGatewayTargets(context.Context, *bac.ListGatewayTargetsInput, ...func(*bac.Options)) (*bac.ListGatewayTargetsOutput, error)
	ListMemories(context.Context, *bac.ListMemoriesInput, ...func(*bac.Options)) (*bac.ListMemoriesOutput, error)
	ListOauth2CredentialProviders(context.Context, *bac.ListOauth2CredentialProvidersInput, ...func(*bac.Options)) (*bac.ListOauth2CredentialProvidersOutput, error)
	ListOnlineEvaluationConfigs(context.Context, *bac.ListOnlineEvaluationConfigsInput, ...func(*bac.Options)) (*bac.ListOnlineEvaluationConfigsOutput, error)
	ListPolicies(context.Context, *bac.ListPoliciesInput, ...func(*bac.Options)) (*bac.ListPoliciesOutput, error)
	ListPolicyEngines(context.Context, *bac.ListPolicyEnginesInput, ...func(*bac.Options)) (*bac.ListPolicyEnginesOutput, error)
	ListAgentRuntimes(context.Context, *bac.ListAgentRuntimesInput, ...func(*bac.Options)) (*bac.ListAgentRuntimesOutput, error)
	ListAgentRuntimeEndpoints(context.Context, *bac.ListAgentRuntimeEndpointsInput, ...func(*bac.Options)) (*bac.ListAgentRuntimeEndpointsOutput, error)
	ListWorkloadIdentities(context.Context, *bac.ListWorkloadIdentitiesInput, ...func(*bac.Options)) (*bac.ListWorkloadIdentitiesOutput, error)
	ListConfigurationBundles(context.Context, *bac.ListConfigurationBundlesInput, ...func(*bac.Options)) (*bac.ListConfigurationBundlesOutput, error)
	ListDatasets(context.Context, *bac.ListDatasetsInput, ...func(*bac.Options)) (*bac.ListDatasetsOutput, error)
	ListHarnesses(context.Context, *bac.ListHarnessesInput, ...func(*bac.Options)) (*bac.ListHarnessesOutput, error)
	ListHarnessEndpoints(context.Context, *bac.ListHarnessEndpointsInput, ...func(*bac.Options)) (*bac.ListHarnessEndpointsOutput, error)
	ListRegistries(context.Context, *bac.ListRegistriesInput, ...func(*bac.Options)) (*bac.ListRegistriesOutput, error)
	ListRegistryRecords(context.Context, *bac.ListRegistryRecordsInput, ...func(*bac.Options)) (*bac.ListRegistryRecordsOutput, error)
	ListPolicyGenerations(context.Context, *bac.ListPolicyGenerationsInput, ...func(*bac.Options)) (*bac.ListPolicyGenerationsOutput, error)
	ListPaymentManagers(context.Context, *bac.ListPaymentManagersInput, ...func(*bac.Options)) (*bac.ListPaymentManagersOutput, error)
	ListPaymentConnectors(context.Context, *bac.ListPaymentConnectorsInput, ...func(*bac.Options)) (*bac.ListPaymentConnectorsOutput, error)
	ListPaymentCredentialProviders(context.Context, *bac.ListPaymentCredentialProvidersInput, ...func(*bac.Options)) (*bac.ListPaymentCredentialProvidersOutput, error)
	ListCapacityProviders(context.Context, *bac.ListCapacityProvidersInput, ...func(*bac.Options)) (*bac.ListCapacityProvidersOutput, error)
	ListConsentPortals(context.Context, *bac.ListConsentPortalsInput, ...func(*bac.Options)) (*bac.ListConsentPortalsOutput, error)
	ListGatewayRules(context.Context, *bac.ListGatewayRulesInput, ...func(*bac.Options)) (*bac.ListGatewayRulesOutput, error)
	ListGatewayRateLimits(context.Context, *bac.ListGatewayRateLimitsInput, ...func(*bac.Options)) (*bac.ListGatewayRateLimitsOutput, error)
	ListTagsForResource(context.Context, *bac.ListTagsForResourceInput, ...func(*bac.Options)) (*bac.ListTagsForResourceOutput, error)
}

const bacListTagsOp = "bedrockagentcore:ListTagsForResource"

// Page-size caps (maxResults) the model documents for the AgentCore listings:
// 1-100 for most, 1-20 for the OAuth2 and payment credential providers and
// the workload identities, 1-1000 for the gateways and gateway targets.
const (
	bacPageCap        int32 = 100
	bacSmallPageCap   int32 = 20
	bacGatewayPageCap int32 = 1000
)

// bacPageFunc reads one page of a listing.
type bacPageFunc[Item any] func(ctx context.Context, token *string) ([]Item, *string, error)

// bacParentPageFunc reads one page of a parent's child listing.
type bacParentPageFunc[Item any] func(ctx context.Context, parent string, token *string) ([]Item, *string, error)

func scanBedrockAgentCore(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := bac.NewFromConfig(acct.cfg, func(o *bac.Options) {
		o.Region = region
		// AgentCore Control returns a *retryable* HTTP 500
		// (AuthorizerConfigurationException) in regions where it isn't
		// provisioned; the default adaptive retry would burn ~2 min of the
		// per-service budget on that permanent gap. Mark only that code
		// non-retryable — every genuine transient error on this newer service
		// keeps the full adaptive retry, so we don't miss resources to blips.
		o.Retryer = withNonRetryableCodes(o.Retryer, "AuthorizerConfigurationException")
	})
	data := bacdata.NewFromConfig(acct.cfg, func(o *bacdata.Options) {
		o.Region = region
		o.Retryer = withNonRetryableCodes(o.Retryer, "AuthorizerConfigurationException")
	})
	return scanBACAll(ctx, client, data, acct, region, st, scanID)
}

// scanBACAll runs every AgentCore phase in order. A phase's error does not
// stop the later ones; a failed write does, since every later write would
// fail too, and so does ctx's end, reported as one error. A child phase gets
// the parents its parent phase stored. The phases run one at a time, so
// readRowTags's own bound is the bound on the tag reads in flight. Every
// phase but the A/B tests calls the control-plane host, so an NXDOMAIN from
// one after control-plane rows were stored is flattened (resultLateNXDOMAIN).
// The A/B-test phase is on the data-plane host: it handles its own NXDOMAIN
// (scanBACABTests), and its counts are kept out of p, since its rows prove
// nothing about the control-plane host to resultLateNXDOMAIN; they only
// keep a control-plane NXDOMAIN from reporting the region unavailable.
func scanBACAll(ctx context.Context, client bedrockAgentCoreAPI, data bedrockAgentCoreDataAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p                                                phaseTotals
		tags                                             = bacTags(client)
		gateways, runtimes, engines, harnesses, managers []childParent
		registries                                       []childParent
		abTotal, abInserted                              int
	)
	counted := func(op string, run func() (int, int, error)) func() bool {
		return func() bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}
	}
	phases := []func() bool{
		func() (stop bool) {
			gateways, stop = scanBACGateways(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() (stop bool) {
			runtimes, stop = scanBACRuntimes(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() (stop bool) {
			engines, stop = scanBACPolicyEngines(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() (stop bool) {
			harnesses, stop = scanBACHarnesses(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool {
			var (
				t, i int
				err  error
			)
			t, i, registries, err = scanBACRegistries(ctx, client, acct, region, st, scanID)
			return p.addPhase("bedrockagentcore:ListRegistries", t, i, err)
		},
		func() (stop bool) {
			managers, stop = scanBACPaymentManagers(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		counted("bedrockagentcore:ListGatewayTargets", func() (int, int, error) {
			return scanBACGatewayTargets(ctx, client, acct, region, st, scanID, gateways)
		}),
		func() bool { return scanBACRuntimeEndpoints(ctx, client, acct, region, st, scanID, runtimes, tags, &p) },
		func() bool { return scanBACApiKeyCreds(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACOauth2Creds(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACBrowsers(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACBrowserProfiles(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACCodeInterpreters(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACEvaluators(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACMemories(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACOnlineEvalConfigs(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("bedrockagentcore:ListPolicies", func() (int, int, error) {
			return scanBACPolicies(ctx, client, acct, region, st, scanID, engines)
		}),
		counted("bedrockagentcore:ListPolicyGenerations", func() (int, int, error) {
			return scanBACPolicyGenerations(ctx, client, acct, region, st, scanID, engines)
		}),
		func() bool { return scanBACWorkloadIdentities(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACConfigurationBundles(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACDatasets(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool {
			return scanBACHarnessEndpoints(ctx, client, acct, region, st, scanID, harnesses, tags, &p)
		},
		counted("bedrockagentcore:ListRegistryRecords", func() (int, int, error) {
			return scanBACRegistryRecords(ctx, client, acct, region, st, scanID, registries)
		}),
		counted("bedrockagentcore:ListPaymentConnectors", func() (int, int, error) {
			return scanBACPaymentConnectors(ctx, client, acct, region, st, scanID, managers)
		}),
		func() bool { return scanBACPaymentCredentialProviders(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACCapacityProviders(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanBACConsentPortals(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool {
			t, i, err := scanBACABTests(ctx, data, acct, region, st, scanID)
			abTotal, abInserted = t, i
			p.errs.add("bedrockagentcore:ListABTests", err)
			return errors.Is(err, store.ErrStoreWrite)
		},
		counted("bedrockagentcore:ListGatewayRules", func() (int, int, error) {
			return scanBACGatewayRules(ctx, client, acct, region, st, scanID, gateways)
		}),
		counted("bedrockagentcore:ListGatewayRateLimits", func() (int, int, error) {
			return scanBACGatewayRateLimits(ctx, client, acct, region, st, scanID, gateways)
		}),
	}
	notRun := 0
	for i, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - i
			break
		}
		if run() {
			break
		}
	}
	bacFoldStop(ctx, &p, notRun)
	total, inserted, err := p.resultLateNXDOMAIN(st, acct, region)
	// A control-plane NXDOMAIN left after nothing of its own was stored would
	// report the region unavailable and drop the A/B rows' counts; with A/B
	// rows stored it is a failure of this scan instead. No failed write can
	// be in err here: a failed A/B write leaves abTotal 0, and the only later
	// phases are children of gateways, which nothing stored means none of.
	if abTotal > 0 {
		err = lateNXDOMAIN("bedrockagentcore", err)
	}
	return total + abTotal, inserted + abInserted, err
}

// bacFoldStop leaves ctx's end as one member of p's errors. Each phase in
// flight when ctx ended reports the stop itself, from its listing and from
// its tag reads; those reports are dropped from the joins they are in, the
// rest of each join kept, and one stop is added: the phases not run when
// notRun is set, else the first report dropped. Only joins are looked into:
// a single error that wraps the stop is the stop.
func bacFoldStop(ctx context.Context, p *phaseTotals, notRun int) {
	stop := ctx.Err()
	if stop == nil {
		return
	}
	var (
		errs  phaseErrs
		first error
	)
	for _, f := range p.errs {
		kept, dropped := bacWithoutStop(stop, f.err)
		if first == nil {
			first = dropped
		}
		errs.add(f.op, kept)
	}
	if notRun > 0 {
		first = fmt.Errorf("bedrockagentcore: %d phases not run: %w", notRun, stop)
	}
	errs.add("bedrockagentcore", first)
	p.errs = errs
}

// bacWithoutStop splits err into the part that is not stop and the first
// member that is.
func bacWithoutStop(stop, err error) (kept, dropped error) {
	members, ok := err.(interface{ Unwrap() []error })
	if !ok {
		if errors.Is(err, stop) {
			return nil, err
		}
		return err, nil
	}
	var rest []error
	for _, m := range members.Unwrap() {
		k, d := bacWithoutStop(stop, m)
		if dropped == nil {
			dropped = d
		}
		if k != nil {
			rest = append(rest, k)
		}
	}
	return errors.Join(rest...), dropped
}

// bacTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which for every type read is the resource's ARN. Its model's
// TaggableResourcesArn pattern accepts any bedrock-agentcore ARN in a
// 12-digit account,
// `^arn:(?:[^:]+)?:bedrock-agentcore:[a-z0-9-]+:[0-9]{12}:([a-z-]+/[^/]+)(?:/[a-z-]+/[^/]+)*$`.
// The consent portals, configuration bundles, datasets and harness endpoints
// are read although the Service Reference lists none of them: each one's
// Create op takes Tags, and its ARN fits that pattern. The op answers with
// every tag at once (no page token);
// ResourceNotFoundException is the only not-found code it models. An AWS
// built-in row (ManagedByProvider) is not read and keeps the TagsJSON its
// builder set: the built-in browser's and code interpreter's ARNs are in the
// "aws" account and a built-in evaluator's in none, so the pattern rejects
// them, and a rejection would fail every scan.
func bacTags(client bedrockAgentCoreAPI) rowTagReader {
	return rowTagReader{
		op:       bacListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			out, err := client.ListTagsForResource(ctx, &bac.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// bacRow is the row of a listed item, stored under the ARN its list element
// carries with item verbatim as the attributes. An item without an ARN gives
// no row: the reason names the items (what), and key, or a fixed text when
// key is empty, names the item in the warning about them.
func bacRow(acct *account, region, scanID, typ, what string, arn *string, name, key string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", cmp.Or(key, "(an item with no id)")
	}
	return bacRowAt(acct, region, scanID, typ, sv(arn), name, item), "", ""
}

// bacRowAt is the row of a listed item stored under nativeID, item verbatim
// as the attributes.
func bacRowAt(acct *account, region, scanID, typ, nativeID, name string, item any) *store.Resource {
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           typ,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(item),
		DiscoveredBy:   scanID,
	}
}

// isBACRegionGap reports the codes a newer AgentCore op answers with in a
// region where it is not deployed: some 404 with UnknownOperationException,
// others reach a front end that resolves but is not provisioned and answer
// with a 500 AuthorizerConfigurationException (marked non-retryable on the
// client, see scanBedrockAgentCore).
func isBACRegionGap(err error) bool {
	return isAPIErrorCode(err, "UnknownOperationException", "AuthorizerConfigurationException")
}

// bacPages is page with AgentCore's region gaps handled. A first page refused
// with an error isGap (nil for none) reports ends the listing as empty: the
// op is not deployed in this region. After a page was read, the gateway's
// "Unable to determine service/operation name" denial is returned as a plain
// error: left a denial, the listing's denial path would hand it to
// skipIfAccessDenied, which drops it as a region gap, and the later pages
// would be lost without a word.
func bacPages[Item any](isGap func(error) bool, page bacPageFunc[Item]) bacPageFunc[Item] {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		switch {
		case err == nil:
		case token == nil && isGap != nil && isGap(err):
			return nil, nil, nil
		case token != nil && isServiceNotAvailableInRegion(err):
			return nil, nil, fmt.Errorf("the endpoint stopped recognising the op after a page was read, so the later pages were not read; rescan: %v", err)
		}
		return items, next, err
	}
}

// bacParentPages is bacPages for each parent's child listing.
func bacParentPages[Item any](isGap func(error) bool, page bacParentPageFunc[Item]) bacParentPageFunc[Item] {
	return func(ctx context.Context, parent string, token *string) ([]Item, *string, error) {
		return bacPages(isGap, func(ctx context.Context, token *string) ([]Item, *string, error) {
			return page(ctx, parent, token)
		})(ctx, token)
	}
}

// bacUnauthorized is the UnauthorizedException a listing ended at, for the
// ops that model it (a bearer token missing or invalid for OAuth-based
// access). isAccessDenied does not match the code, so the listing's own
// denial path never sees it; it is warned about like a denial instead.
type bacUnauthorized struct{ err error }

// bacStopUnauthorized ends page's listing at an UnauthorizedException, keeping the pages
// read before it, and records it for warn.
func bacStopUnauthorized[Item any](u *bacUnauthorized, page bacPageFunc[Item]) bacPageFunc[Item] {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if !isAPIErrorCode(err, "UnauthorizedException") {
			return items, next, err
		}
		u.err = err
		if token != nil {
			u.err = fmt.Errorf("the listing stopped at a refused page after the first, so the later pages were not read: %w", err)
		}
		return nil, nil, nil
	}
}

// warn reports the recorded UnauthorizedException once the listing's write
// succeeded.
func (u *bacUnauthorized) warn(st *store.Store, op string, acct *account, region string, storeWriteFailed bool) {
	if u.err != nil && !storeWriteFailed {
		_ = skipIfAccessDenied(st, op, acct.ID, region, u.err)
	}
}

// bacParentIDs is the ids of parents, which their child listings key on.
func bacParentIDs(parents []childParent) []string {
	ids := make([]string, len(parents))
	for i, p := range parents {
		ids[i] = p.id
	}
	return ids
}

// bacStoreEachParentTags is storeTokenEachParent with each row's tags read
// through tags (readRowTags) before the write: one parent's failure does not
// stop the others, and its rows read before the failure are kept. Counts
// and errors go to p as rowTagErrs.addPhase says; it reports whether the
// write failed.
func bacStoreEachParentTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	page bacParentPageFunc[Item], row func(parent string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	ids := bacParentIDs(parents)
	reads, stopped := readEachKey(ctx, ids, func(ctx context.Context, id string) parentItems[Item] {
		return readParentPages(ctx, id, page)
	})
	out := parentOutcomes{op: op, notFound: notFound}
	for i, read := range reads {
		addParentRows(&out, ids[i], read, row)
	}
	out.stopped(stopped)
	var (
		errs rowTagErrs
		warn func()
	)
	out.rows, warn, errs.hard, errs.transient = readRowTags(ctx, st, acct, region, tags, out.rows)
	total, inserted, err := out.store(st, acct, region)
	if !errors.Is(err, store.ErrStoreWrite) {
		warn()
	}
	return errs.addPhase(p, tags.op, op, total, inserted, err)
}

func bacARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:%s/%s", region, acct, kind, id)
}

func scanBACApiKeyCreds(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagentcore:ListApiKeyCredentialProviders"
	var unauthorized bacUnauthorized
	stop := storeTokenListTags(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.ApiKeyCredentialProviderItem, *string, error) {
			out, err := client.ListApiKeyCredentialProviders(ctx, &bac.ListApiKeyCredentialProvidersInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CredentialProviders, out.NextToken, nil
		})),
		func(c bactypes.ApiKeyCredentialProviderItem) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreAPIKeyCredentialProvider, "API key credential providers",
				c.CredentialProviderArn, cmp.Or(sv(c.Name), sv(c.CredentialProviderArn)), sv(c.Name), c)
		}, tags, p)
	unauthorized.warn(st, op, acct, region, stop)
	return stop
}

func scanBACOauth2Creds(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagentcore:ListOauth2CredentialProviders"
	var unauthorized bacUnauthorized
	stop := storeTokenListTags(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.Oauth2CredentialProviderItem, *string, error) {
			out, err := client.ListOauth2CredentialProviders(ctx, &bac.ListOauth2CredentialProvidersInput{NextToken: token, MaxResults: sdkaws.Int32(bacSmallPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CredentialProviders, out.NextToken, nil
		})),
		func(c bactypes.Oauth2CredentialProviderItem) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreOAuth2CredentialProvider, "OAuth2 credential providers",
				c.CredentialProviderArn, cmp.Or(sv(c.Name), sv(c.CredentialProviderArn)), sv(c.Name), c)
		}, tags, p)
	unauthorized.warn(st, op, acct, region, stop)
	return stop
}

// scanBACBrowsers lists CUSTOM browsers (customer-created) as
// aws:bedrockagentcore:browser-custom and SYSTEM browsers (AWS built-in tools,
// e.g. aws.browser.v1) as aws:bedrockagentcore:browser with ManagedByProvider
// set — distinct upstream resource types. Each kind is its own listing, so
// one's failure does not stop the other.
func scanBACBrowsers(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	for _, kind := range []bactypes.ResourceType{bactypes.ResourceTypeCustom, bactypes.ResourceTypeSystem} {
		typ, managed := TypeBedrockAgentCoreBrowserCustom, false
		if kind == bactypes.ResourceTypeSystem {
			typ, managed = TypeBedrockAgentCoreBrowser, true
		}
		if storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListBrowsers",
			bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.BrowserSummary, *string, error) {
				out, err := client.ListBrowsers(ctx, &bac.ListBrowsersInput{Type: kind, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.BrowserSummaries, out.NextToken, nil
			}),
			func(b bactypes.BrowserSummary) (*store.Resource, string, string) {
				r, why, key := bacRow(acct, region, scanID, typ, "browsers", b.BrowserArn, sv(b.BrowserId), sv(b.BrowserId), b)
				if r != nil {
					r.ManagedByProvider = managed
					r.CreatedAt = tp(b.CreatedAt)
				}
				return r, why, key
			}, tags, p) {
			return true
		}
	}
	return false
}

func scanBACBrowserProfiles(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListBrowserProfiles",
		bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.BrowserProfileSummary, *string, error) {
			out, err := client.ListBrowserProfiles(ctx, &bac.ListBrowserProfilesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ProfileSummaries, out.NextToken, nil
		}),
		func(b bactypes.BrowserProfileSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreBrowserProfile, "browser profiles",
				b.ProfileArn, cmp.Or(sv(b.Name), sv(b.ProfileId)), sv(b.ProfileId), b)
		}, tags, p)
}

// scanBACCodeInterpreters lists CUSTOM code interpreters as
// aws:bedrockagentcore:code-interpreter-custom and SYSTEM ones (AWS built-in
// interpreter, e.g. aws.codeinterpreter.v1) as aws:bedrockagentcore:code-
// interpreter with ManagedByProvider set. Each kind is its own listing, so
// one's failure does not stop the other.
func scanBACCodeInterpreters(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	for _, kind := range []bactypes.ResourceType{bactypes.ResourceTypeCustom, bactypes.ResourceTypeSystem} {
		typ, managed := TypeBedrockAgentCoreCodeInterpreterCustom, false
		if kind == bactypes.ResourceTypeSystem {
			typ, managed = TypeBedrockAgentCoreCodeInterpreter, true
		}
		if storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListCodeInterpreters",
			bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.CodeInterpreterSummary, *string, error) {
				out, err := client.ListCodeInterpreters(ctx, &bac.ListCodeInterpretersInput{Type: kind, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.CodeInterpreterSummaries, out.NextToken, nil
			}),
			func(c bactypes.CodeInterpreterSummary) (*store.Resource, string, string) {
				r, why, key := bacRow(acct, region, scanID, typ, "code interpreters", c.CodeInterpreterArn, sv(c.CodeInterpreterId), sv(c.CodeInterpreterId), c)
				if r != nil {
					r.ManagedByProvider = managed
					r.CreatedAt = tp(c.CreatedAt)
				}
				return r, why, key
			}, tags, p) {
			return true
		}
	}
	return false
}

// scanBACEvaluators lists the region's evaluators. A first page refused with
// the gateway's "Unable to determine service/operation name to be
// authorized" is a per-region feature gap: the regional endpoint does not
// recognise the op.
func scanBACEvaluators(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListEvaluators",
		bacPages(isServiceNotAvailableInRegion, func(ctx context.Context, token *string) ([]bactypes.EvaluatorSummary, *string, error) {
			out, err := client.ListEvaluators(ctx, &bac.ListEvaluatorsInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Evaluators, out.NextToken, nil
		}),
		func(e bactypes.EvaluatorSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreEvaluator, "evaluators",
				e.EvaluatorArn, cmp.Or(sv(e.EvaluatorName), sv(e.EvaluatorId)), sv(e.EvaluatorId), e)
			if r != nil {
				// AWS-supplied evaluators carry EvaluatorType="Builtin"; customer
				// evaluators carry "Custom". Case-insensitive match guards drift.
				r.ManagedByProvider = strings.EqualFold(string(e.EvaluatorType), "Builtin")
			}
			return r, why, key
		}, tags, p)
}

// scanBACGateways stores the region's gateways with their tags and returns
// the stored ones that carry a GatewayId, the parents of the gateway target,
// rule and rate-limit phases. A gateway's list element carries no ARN; its
// NativeID is built in the Service Reference's gateway ARN format, which is
// also the GatewayArn GetGateway returns, so the tag read is sent it.
func scanBACGateways(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (gateways []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagentcore:ListGateways", "gateways", "GatewayId",
		bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.GatewaySummary, *string, error) {
			out, err := client.ListGateways(ctx, &bac.ListGatewaysInput{NextToken: token, MaxResults: sdkaws.Int32(bacGatewayPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(g bactypes.GatewaySummary) (*store.Resource, string, string) {
			id := sv(g.GatewayId)
			if id == "" {
				return nil, "gateways have an empty GatewayId", cmp.Or(sv(g.Name), "(a gateway with no name)")
			}
			return bacRowAt(acct, region, scanID, TypeBedrockAgentCoreGateway, bacARN(region, acct.ID, "gateway", id), cmp.Or(sv(g.Name), id), g), "", ""
		},
		func(g bactypes.GatewaySummary) string { return sv(g.GatewayId) },
		tags, p)
}

// scanBACGatewayTargets lists the targets of each gateway the gateway phase
// stored. A target carries no ARN, so its NativeID is built from the gateway
// and target ids. A gateway ResourceNotFoundException says was deleted since
// it was listed is skipped (storeTokenEachParent).
func scanBACGatewayTargets(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListGatewayTargets", []string{"ResourceNotFoundException"}, bacParentIDs(gateways),
		bacParentPages(nil, func(ctx context.Context, gateway string, token *string) ([]bactypes.TargetSummary, *string, error) {
			out, err := client.ListGatewayTargets(ctx, &bac.ListGatewayTargetsInput{GatewayIdentifier: &gateway, NextToken: token, MaxResults: sdkaws.Int32(bacGatewayPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(gateway string, t bactypes.TargetSummary) (*store.Resource, string, string) {
			id := sv(t.TargetId)
			if id == "" {
				return nil, "gateway targets have an empty TargetId", cmp.Or(sv(t.Name), "(a target with no name) of gateway "+gateway)
			}
			return bacRowAt(acct, region, scanID, TypeBedrockAgentCoreGatewayTarget, bacARN(region, acct.ID, "gateway-target", gateway+"/"+id), cmp.Or(sv(t.Name), id), t), "", ""
		})
}

func scanBACMemories(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListMemories",
		bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.MemorySummary, *string, error) {
			out, err := client.ListMemories(ctx, &bac.ListMemoriesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Memories, out.NextToken, nil
		}),
		func(m bactypes.MemorySummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreMemory, "memories", m.Arn, sv(m.Id), sv(m.Id), m)
		}, tags, p)
}

// scanBACOnlineEvalConfigs lists the region's online evaluation configs. A
// first page refused as scanBACEvaluators says is a per-region feature gap.
func scanBACOnlineEvalConfigs(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListOnlineEvaluationConfigs",
		bacPages(isServiceNotAvailableInRegion, func(ctx context.Context, token *string) ([]bactypes.OnlineEvaluationConfigSummary, *string, error) {
			out, err := client.ListOnlineEvaluationConfigs(ctx, &bac.ListOnlineEvaluationConfigsInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.OnlineEvaluationConfigs, out.NextToken, nil
		}),
		func(c bactypes.OnlineEvaluationConfigSummary) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreOnlineEvaluationConfig, "online evaluation configs",
				c.OnlineEvaluationConfigArn, cmp.Or(sv(c.OnlineEvaluationConfigName), sv(c.OnlineEvaluationConfigId)), sv(c.OnlineEvaluationConfigId), c)
		}, tags, p)
}

// scanBACPolicies lists the policies of each engine the policy-engine phase
// stored. An engine ResourceNotFoundException says was deleted since it was
// listed is skipped (storeTokenEachParent).
func scanBACPolicies(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, engines []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListPolicies", []string{"ResourceNotFoundException"}, bacParentIDs(engines),
		bacParentPages(nil, func(ctx context.Context, engine string, token *string) ([]bactypes.Policy, *string, error) {
			out, err := client.ListPolicies(ctx, &bac.ListPoliciesInput{PolicyEngineId: &engine, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Policies, out.NextToken, nil
		}),
		func(_ string, pol bactypes.Policy) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCorePolicy, "policies",
				pol.PolicyArn, cmp.Or(sv(pol.Name), sv(pol.PolicyId)), sv(pol.PolicyId), pol)
		})
}

// scanBACPolicyEngines stores the region's policy engines with their tags and
// returns the stored ones that carry a PolicyEngineId, the parents of the
// policy and policy-generation phases.
func scanBACPolicyEngines(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (engines []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagentcore:ListPolicyEngines", "policy engines", "PolicyEngineId",
		bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.PolicyEngine, *string, error) {
			out, err := client.ListPolicyEngines(ctx, &bac.ListPolicyEnginesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PolicyEngines, out.NextToken, nil
		}),
		func(e bactypes.PolicyEngine) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCorePolicyEngine, "policy engines",
				e.PolicyEngineArn, cmp.Or(sv(e.Name), sv(e.PolicyEngineId)), sv(e.PolicyEngineId), e)
		},
		func(e bactypes.PolicyEngine) string { return sv(e.PolicyEngineId) },
		tags, p)
}

// scanBACRuntimes stores the region's agent runtimes with their tags and
// returns the stored ones that carry an AgentRuntimeId, the parents of the
// runtime-endpoint phase.
func scanBACRuntimes(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (runtimes []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagentcore:ListAgentRuntimes", "agent runtimes", "AgentRuntimeId",
		bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.AgentRuntime, *string, error) {
			out, err := client.ListAgentRuntimes(ctx, &bac.ListAgentRuntimesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentRuntimes, out.NextToken, nil
		}),
		func(r bactypes.AgentRuntime) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreRuntime, "agent runtimes",
				r.AgentRuntimeArn, cmp.Or(sv(r.AgentRuntimeName), sv(r.AgentRuntimeId)), sv(r.AgentRuntimeId), r)
		},
		func(r bactypes.AgentRuntime) string { return sv(r.AgentRuntimeId) },
		tags, p)
}

// scanBACRuntimeEndpoints lists the endpoints of each runtime the runtime
// phase stored and stores them with their tags (bacStoreEachParentTags). The
// op's model lists no not-found code, so a runtime deleted after it was
// listed fails the phase with whatever code the service answers with
// (decision #14, a known race).
func scanBACRuntimeEndpoints(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, runtimes []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return bacStoreEachParentTags(ctx, st, acct, region, "bedrockagentcore:ListAgentRuntimeEndpoints", nil, runtimes,
		bacParentPages(nil, func(ctx context.Context, runtime string, token *string) ([]bactypes.AgentRuntimeEndpoint, *string, error) {
			out, err := client.ListAgentRuntimeEndpoints(ctx, &bac.ListAgentRuntimeEndpointsInput{AgentRuntimeId: &runtime, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuntimeEndpoints, out.NextToken, nil
		}),
		func(_ string, e bactypes.AgentRuntimeEndpoint) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreRuntimeEndpoint, "agent runtime endpoints",
				e.AgentRuntimeEndpointArn, cmp.Or(sv(e.Name), sv(e.Id)), sv(e.Id), e)
		}, tags, p)
}

func scanBACWorkloadIdentities(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagentcore:ListWorkloadIdentities"
	var unauthorized bacUnauthorized
	stop := storeTokenListTags(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(nil, func(ctx context.Context, token *string) ([]bactypes.WorkloadIdentityType, *string, error) {
			out, err := client.ListWorkloadIdentities(ctx, &bac.ListWorkloadIdentitiesInput{NextToken: token, MaxResults: sdkaws.Int32(bacSmallPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkloadIdentities, out.NextToken, nil
		})),
		func(w bactypes.WorkloadIdentityType) (*store.Resource, string, string) {
			return bacRow(acct, region, scanID, TypeBedrockAgentCoreWorkloadIdentity, "workload identities",
				w.WorkloadIdentityArn, cmp.Or(sv(w.Name), sv(w.WorkloadIdentityArn)), sv(w.Name), w)
		}, tags, p)
	unauthorized.warn(st, op, acct, region, stop)
	return stop
}

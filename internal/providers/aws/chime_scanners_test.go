package aws

import (
	"cmp"
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	ci "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity"
	citypes "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity/types"
	cp "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines"
	cptypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines/types"
	cm "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging"
	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cv "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice"
	cvtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice/types"
	"github.com/icearp/disco-cli/store"
)

var (
	chimeDenied     = apiErr("ForbiddenException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: chime:List")
	chimeUnauth     = apiErr("UnauthorizedClientException", "The client is not currently authorized to make the request")
	chimeGone       = apiErr("NotFoundException", "gone")
	chimeBadRequest = apiErr("BadRequestException", "bad")
	chimeThrottle   = apiErr("ThrottledClientException", "rate exceeded")
	chimeGap        = apiErr("ForbiddenException", "This feature is not available")
	chimeNX         = &net.DNSError{Err: "no such host", Name: "voice-chime.us-east-1.amazonaws.com", IsNotFound: true}
	chimeCreated    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

const (
	chimeTagsOp = "chime:ListTagsForResource"
	chimeKMSKey = "arn:aws:kms:us-east-1:123456789012:key/k-1"
	// chimeTotalCount is the fixture's rows: two of each of the nine
	// account-wide listings, and one child per parent for each of the four
	// child listings (two app instances, two voice profile domains).
	chimeTotalCount = 26
)

// The SDK packages, by the tag op each serves.
const (
	chimeIdentity       = "identity"
	chimeMessaging      = "messaging"
	chimeMediaPipelines = "media-pipelines"
	chimeVoice          = "voice"
)

// chimeKind is one account-wide listing of the fixture: the op, the type it
// stores, the SDK package whose tag op reads its rows ("" for an untagged
// type), the ARN resource kind, the MaxResults it must send, whether its rows
// carry a Name ("name-" + id) and a CreatedAt, and page, which builds the
// op's output from one element's ARN (nil for an element without an
// identifier), its id and the next token.
type chimeKind struct {
	op, typ, sdk, kind string
	maxResults         int32
	named, created     bool
	page               func(arn *string, id string, next *string) any
}

func chimeNameOf(id string) *string { return sdkaws.String("name-" + id) }

func chimeKinds() []chimeKind {
	return []chimeKind{
		{"ListAppInstances", TypeChimeAppInstance, chimeIdentity, "app-instance", 50, true, false, func(arn *string, id string, next *string) any {
			return &ci.ListAppInstancesOutput{AppInstances: []citypes.AppInstanceSummary{{AppInstanceArn: arn, Name: chimeNameOf(id)}}, NextToken: next}
		}},
		{"ListMediaPipelines", TypeChimeMediaPipeline, chimeMediaPipelines, "media-pipeline", 99, false, false, func(arn *string, id string, next *string) any {
			return &cp.ListMediaPipelinesOutput{MediaPipelines: []cptypes.MediaPipelineSummary{{MediaPipelineArn: arn, MediaPipelineId: sdkaws.String(id)}}, NextToken: next}
		}},
		{"ListMediaInsightsPipelineConfigurations", TypeChimeMediaInsightsPipelineConfiguration, chimeMediaPipelines, "media-insights-pipeline-configuration", 100, true, false, func(arn *string, id string, next *string) any {
			return &cp.ListMediaInsightsPipelineConfigurationsOutput{MediaInsightsPipelineConfigurations: []cptypes.MediaInsightsPipelineConfigurationSummary{{
				MediaInsightsPipelineConfigurationArn: arn, MediaInsightsPipelineConfigurationId: sdkaws.String(id), MediaInsightsPipelineConfigurationName: chimeNameOf(id),
			}}, NextToken: next}
		}},
		{"ListMediaPipelineKinesisVideoStreamPools", TypeChimeMediaPipelineKinesisVideoStreamPool, chimeMediaPipelines, "media-pipeline-kinesis-video-stream-pool", 100, true, false, func(arn *string, id string, next *string) any {
			return &cp.ListMediaPipelineKinesisVideoStreamPoolsOutput{KinesisVideoStreamPools: []cptypes.KinesisVideoStreamPoolSummary{{PoolArn: arn, PoolId: sdkaws.String(id), PoolName: chimeNameOf(id)}}, NextToken: next}
		}},
		{"ListSipMediaApplications", TypeChimeSipMediaApplication, chimeVoice, "sma", 100, true, true, func(arn *string, id string, next *string) any {
			return &cv.ListSipMediaApplicationsOutput{SipMediaApplications: []cvtypes.SipMediaApplication{{
				SipMediaApplicationArn: arn, SipMediaApplicationId: sdkaws.String(id), Name: chimeNameOf(id), CreatedTimestamp: &chimeCreated,
			}}, NextToken: next}
		}},
		{"ListVoiceConnectors", TypeChimeVoiceConnector, chimeVoice, "vc", 100, true, true, func(arn *string, id string, next *string) any {
			return &cv.ListVoiceConnectorsOutput{VoiceConnectors: []cvtypes.VoiceConnector{{
				VoiceConnectorArn: arn, VoiceConnectorId: sdkaws.String(id), Name: chimeNameOf(id), CreatedTimestamp: &chimeCreated,
			}}, NextToken: next}
		}},
		{"ListVoiceProfileDomains", TypeChimeVoiceProfileDomain, chimeVoice, "voice-profile-domain", 100, true, true, func(arn *string, id string, next *string) any {
			return &cv.ListVoiceProfileDomainsOutput{VoiceProfileDomains: []cvtypes.VoiceProfileDomainSummary{{
				VoiceProfileDomainArn: arn, VoiceProfileDomainId: sdkaws.String(id), Name: chimeNameOf(id), CreatedTimestamp: &chimeCreated,
			}}, NextToken: next}
		}},
		// A SIP rule's NativeID is synthesized from its id in the shape
		// chimeARN builds; an element "without an identifier" has no id.
		{"ListSipRules", TypeChimeSipRule, "", "sip-rule", 100, true, true, func(arn *string, id string, next *string) any {
			rule := cvtypes.SipRule{Name: chimeNameOf(id), CreatedTimestamp: &chimeCreated}
			if arn != nil {
				rule.SipRuleId = sdkaws.String(id)
			}
			return &cv.ListSipRulesOutput{SipRules: []cvtypes.SipRule{rule}, NextToken: next}
		}},
		{"ListVoiceConnectorGroups", TypeChimeVoiceConnectorGroup, "", "vcg", 100, true, true, func(arn *string, id string, next *string) any {
			return &cv.ListVoiceConnectorGroupsOutput{VoiceConnectorGroups: []cvtypes.VoiceConnectorGroup{{
				VoiceConnectorGroupArn: arn, VoiceConnectorGroupId: sdkaws.String(id), Name: chimeNameOf(id), CreatedTimestamp: &chimeCreated,
			}}, NextToken: next}
		}},
	}
}

func chimeKindOf(t *testing.T, op string) chimeKind {
	t.Helper()
	for _, k := range chimeKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return chimeKind{}
}

func chimeID(k chimeKind, suffix string) string { return k.kind + "-" + suffix }

func chimeARNIn(partition, region, account, resource string) string {
	return "arn:" + partition + ":chime:" + region + ":" + account + ":" + resource
}

func chimeARN(k chimeKind, suffix string) string {
	return chimeARNIn("aws", testRegion, testAccountID, k.kind+"/"+chimeID(k, suffix))
}

// chimeChildKind is one per-parent listing: the op, the type it stores, the
// SDK package whose tag op reads its rows ("" for an untagged type), the
// parent listing, the input field naming the parent, the MaxResults it must
// send, and page, which builds the op's output from one element's ARN. Each
// parent has one child, at childARN.
type chimeChildKind struct {
	op, typ, sdk, parentOp, parentField string
	maxResults                          int32
	childARN                            func(parentARN, parentID string) string
	page                                func(arn *string) any
}

func chimeChildKinds() []chimeChildKind {
	under := func(seg string) func(string, string) string {
		return func(parentARN, _ string) string { return parentARN + "/" + seg }
	}
	return []chimeChildKind{
		{"ListAppInstanceBots", TypeChimeAppInstanceBot, chimeIdentity, "ListAppInstances", "AppInstanceArn", 50, under("bot/b"), func(arn *string) any {
			return &ci.ListAppInstanceBotsOutput{AppInstanceBots: []citypes.AppInstanceBotSummary{{AppInstanceBotArn: arn, Name: sdkaws.String("bot")}}}
		}},
		{"ListAppInstanceUsers", TypeChimeAppInstanceUser, chimeIdentity, "ListAppInstances", "AppInstanceArn", 50, under("user/u"), func(arn *string) any {
			return &ci.ListAppInstanceUsersOutput{AppInstanceUsers: []citypes.AppInstanceUserSummary{{AppInstanceUserArn: arn, Name: sdkaws.String("user")}}}
		}},
		{"ListChannelFlows", TypeChimeChannelFlow, chimeMessaging, "ListAppInstances", "AppInstanceArn", 50, under("channel-flow/cf"), func(arn *string) any {
			return &cm.ListChannelFlowsOutput{ChannelFlows: []cmtypes.ChannelFlowSummary{{ChannelFlowArn: arn, Name: sdkaws.String("flow")}}}
		}},
		{"ListVoiceProfiles", TypeChimeVoiceProfile, "", "ListVoiceProfileDomains", "VoiceProfileDomainId", 100, func(_, domainID string) string {
			return chimeARNIn("aws", testRegion, testAccountID, "voice-profile/vp-"+domainID)
		}, func(arn *string) any {
			return &cv.ListVoiceProfilesOutput{VoiceProfiles: []cvtypes.VoiceProfileSummary{{VoiceProfileArn: arn, VoiceProfileId: sdkaws.String("vp"), CreatedTimestamp: &chimeCreated}}}
		}},
	}
}

func chimeChildKindOf(t *testing.T, op string) chimeChildKind {
	t.Helper()
	for _, k := range chimeChildKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture child kind for %s", op)
	return chimeChildKind{}
}

// chimeChildARN is the child of k's parent listed with suffix.
func chimeChildARN(t *testing.T, k chimeChildKind, suffix string) string {
	p := chimeKindOf(t, k.parentOp)
	return k.childARN(chimeARN(p, suffix), chimeID(p, suffix))
}

// chimeCalls records the stubbed calls: how often each op ran, the
// MaxResults each listing was sent, the ARNs each package's
// ListTagsForResource was sent and the domain ids GetVoiceProfileDomain was
// sent. Tag reads answer from tags or errs, keyed by ARN; Gets from getErrs,
// keyed by domain id. Reads run concurrently.
type chimeCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]int32
	tagARNs    map[string][]string
	getIDs     []string
	tags       map[string]map[string]string
	errs       map[string]error
	getErrs    map[string]error
}

// newChimeCalls tags every "tagged" resource of a tagged type, and the child
// of every "tagged" parent of a tagged child type, with its type.
func newChimeCalls(t *testing.T) *chimeCalls {
	c := &chimeCalls{
		ops: map[string]int{}, maxResults: map[string][]int32{}, tagARNs: map[string][]string{},
		tags: map[string]map[string]string{}, errs: map[string]error{}, getErrs: map[string]error{},
	}
	for _, k := range chimeKinds() {
		if k.sdk != "" {
			c.tags[chimeARN(k, "tagged")] = map[string]string{"type": k.typ}
		}
	}
	for _, k := range chimeChildKinds() {
		if k.sdk != "" {
			c.tags[chimeChildARN(t, k, "tagged")] = map[string]string{"type": k.typ}
		}
	}
	return c
}

func (c *chimeCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *chimeCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
}

// listTags answers each package's ListTagsForResource, telling them apart by
// their input type.
func (c *chimeCalls) listTags(in any) (any, error) {
	var sdk string
	switch in.(type) {
	case *ci.ListTagsForResourceInput:
		sdk = chimeIdentity
	case *cm.ListTagsForResourceInput:
		sdk = chimeMessaging
	case *cp.ListTagsForResourceInput:
		sdk = chimeMediaPipelines
	case *cv.ListTagsForResourceInput:
		sdk = chimeVoice
	}
	arn := sv(bacField[*string](in, "ResourceARN"))
	c.mu.Lock()
	c.ops["ListTagsForResource"]++
	c.tagARNs[sdk] = append(c.tagARNs[sdk], arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var keys, values []*string
	for k, v := range tags {
		keys, values = append(keys, sdkaws.String(k)), append(values, sdkaws.String(v))
	}
	switch sdk {
	case chimeIdentity:
		out := &ci.ListTagsForResourceOutput{}
		for i := range keys {
			out.Tags = append(out.Tags, citypes.Tag{Key: keys[i], Value: values[i]})
		}
		return out, nil
	case chimeMessaging:
		out := &cm.ListTagsForResourceOutput{}
		for i := range keys {
			out.Tags = append(out.Tags, cmtypes.Tag{Key: keys[i], Value: values[i]})
		}
		return out, nil
	case chimeMediaPipelines:
		out := &cp.ListTagsForResourceOutput{}
		for i := range keys {
			out.Tags = append(out.Tags, cptypes.Tag{Key: keys[i], Value: values[i]})
		}
		return out, nil
	default:
		out := &cv.ListTagsForResourceOutput{}
		for i := range keys {
			out.Tags = append(out.Tags, cvtypes.Tag{Key: keys[i], Value: values[i]})
		}
		return out, nil
	}
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *chimeCalls) twoPages(k chimeKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(sdkaws.String(chimeARN(k, suffix)), chimeID(k, suffix), next), nil
	}
}

// child answers k's op with the parent's one child, on one page.
func (c *chimeCalls) child(k chimeChildKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		parent := sv(bacField[*string](in, k.parentField))
		return k.page(sdkaws.String(k.childARN(parent, parent))), nil
	}
}

// getDomain answers GetVoiceProfileDomain with a body naming chimeKMSKey.
func (c *chimeCalls) getDomain(in any) (any, error) {
	id := sv(in.(*cv.GetVoiceProfileDomainInput).VoiceProfileDomainId)
	c.mu.Lock()
	c.ops["GetVoiceProfileDomain"]++
	c.getIDs = append(c.getIDs, id)
	err := c.getErrs[id]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &cv.GetVoiceProfileDomainOutput{VoiceProfileDomain: &cvtypes.VoiceProfileDomain{
		VoiceProfileDomainId: sdkaws.String(id), Name: chimeNameOf(id),
		ServerSideEncryptionConfiguration: &cvtypes.ServerSideEncryptionConfiguration{KmsKeyArn: sdkaws.String(chimeKMSKey)},
	}}, nil
}

// chimeFixture answers every op the scan calls with the fixture: two
// resources of every account-wide kind, on two pages, and one child of each
// parent.
func chimeFixture(c *chimeCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource":   c.listTags,
		"GetVoiceProfileDomain": c.getDomain,
	}
	for _, k := range chimeKinds() {
		h[k.op] = c.twoPages(k)
	}
	for _, k := range chimeChildKinds() {
		h[k.op] = c.child(k)
	}
	return h
}

// chimeScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func chimeScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, chimeDenied)
	return scanChime(ctx, acct, testRegion, st, testScanID)
}

func chimeRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// Every type stores both pages of its listing, and every parent its child,
// with the tags its package's ListTagsForResource returns for its ARN, nil
// for a resource without tags, and no tag read for an untagged type. Each
// package's tag op is sent exactly the ARNs of its own types' rows. Every
// listing is sent its MaxResults cap. Rows keep their Name and CreatedAt, a
// channel flow its app instance, a voice profile domain its Get body. No
// error or warning.
func TestScanChime_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newChimeCalls(t)

	total, inserted, err := chimeScan(context.Background(), st, chimeFixture(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != chimeTotalCount || inserted != chimeTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, chimeTotalCount, chimeTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}

	wantTagARNs := map[string][]string{}
	for _, k := range chimeKinds() {
		if k.maxResults != 0 && !slices.Equal(calls.maxResults[k.op], []int32{k.maxResults, k.maxResults}) {
			t.Errorf("%s MaxResults = %v; want %d on both pages", k.op, calls.maxResults[k.op], k.maxResults)
		}
		for _, suffix := range []string{"tagged", "bare"} {
			id, arn := chimeID(k, suffix), chimeARN(k, suffix)
			r, ok := chimeRowOf(t, st, k.typ, arn)
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			var wantName *string
			if k.named {
				wantName = chimeNameOf(id)
			}
			if sv(r.Name) != sv(wantName) || (r.Name == nil) != (wantName == nil) {
				t.Errorf("%s Name = %v; want %v", arn, r.Name, wantName)
			}
			if gotCreated := r.CreatedAt != nil; gotCreated != k.created || (k.created && *r.CreatedAt != "2026-03-04T05:06:07Z") {
				t.Errorf("%s CreatedAt = %v; want set %v", arn, r.CreatedAt, k.created)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("%s Region = %q", arn, sv(r.Region))
			}
			if k.sdk == "" || suffix == "bare" {
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
				}
			} else {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			}
			if k.sdk != "" {
				wantTagARNs[k.sdk] = append(wantTagARNs[k.sdk], arn)
			}
		}
	}
	for _, k := range chimeChildKinds() {
		if !slices.Equal(calls.maxResults[k.op], []int32{k.maxResults, k.maxResults}) {
			t.Errorf("%s MaxResults = %v; want %d for each parent", k.op, calls.maxResults[k.op], k.maxResults)
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := chimeChildARN(t, k, suffix)
			r, ok := chimeRowOf(t, st, k.typ, arn)
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if k.sdk == "" || suffix == "bare" {
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
				}
			} else {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			}
			if k.sdk != "" {
				wantTagARNs[k.sdk] = append(wantTagARNs[k.sdk], arn)
			}
		}
	}
	for _, sdk := range []string{chimeIdentity, chimeMessaging, chimeMediaPipelines, chimeVoice} {
		got := slices.Sorted(slices.Values(calls.tagARNs[sdk]))
		want := slices.Sorted(slices.Values(wantTagARNs[sdk]))
		if !slices.Equal(got, want) {
			t.Errorf("%s ListTagsForResource ARNs = %v; want %v", sdk, got, want)
		}
	}

	flow, _ := chimeRowOf(t, st, TypeChimeChannelFlow, chimeChildARN(t, chimeChildKindOf(t, "ListChannelFlows"), "tagged"))
	if want := `"appInstanceArn":"` + chimeARN(chimeKindOf(t, "ListAppInstances"), "tagged") + `"`; !strings.Contains(flow.AttributesJSON, want) {
		t.Errorf("channel flow attributes = %s; want %s", flow.AttributesJSON, want)
	}
	vpd := chimeKindOf(t, "ListVoiceProfileDomains")
	domain, _ := chimeRowOf(t, st, TypeChimeVoiceProfileDomain, chimeARN(vpd, "tagged"))
	if !strings.Contains(domain.AttributesJSON, chimeKMSKey) {
		t.Errorf("voice profile domain attributes = %s; want the Get body's KMS key", domain.AttributesJSON)
	}
	if got, want := slices.Sorted(slices.Values(calls.getIDs)), []string{chimeID(vpd, "bare"), chimeID(vpd, "tagged")}; !slices.Equal(got, want) {
		t.Errorf("GetVoiceProfileDomain ids = %v; want %v", got, want)
	}
	rule, _ := chimeRowOf(t, st, TypeChimeSipRule, chimeSipRuleID(chimeID(chimeKindOf(t, "ListSipRules"), "tagged")))
	if rule.NativeID == "" {
		t.Error("SIP rule missing under its synthesized NativeID")
	}
}

func chimeSipRuleID(id string) string {
	return chimeSipRuleNativeID("aws", testRegion, testAccountID, id)
}

func TestScanChime_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newChimeCalls(t)
	if _, _, err := chimeScan(context.Background(), st, chimeFixture(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := chimeScan(context.Background(), st, chimeFixture(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != chimeTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, chimeTotalCount)
	}
}

// A failed tag read, for a type of each package: a denial (ForbiddenException
// with an action, or UnauthorizedClientException) keeps the row untagged and
// warns once under chime:ListTagsForResource; NotFoundException drops the row
// silently where the package's tag op models it (media pipelines, voice) and
// is a hard error where it does not (identity, messaging); a throttle after
// retries keeps the row and is a transient error; BadRequestException keeps it
// and is a hard error.
func TestScanChime_TagReadFailures(t *testing.T) {
	for _, tc := range []struct {
		sdk, typ       string
		arn            func(t *testing.T) string
		modelsNotFound bool
	}{
		{chimeIdentity, TypeChimeAppInstanceUser, func(t *testing.T) string {
			return chimeChildARN(t, chimeChildKindOf(t, "ListAppInstanceUsers"), "tagged")
		}, false},
		{chimeMessaging, TypeChimeChannelFlow, func(t *testing.T) string {
			return chimeChildARN(t, chimeChildKindOf(t, "ListChannelFlows"), "tagged")
		}, false},
		{chimeMediaPipelines, TypeChimeMediaPipelineKinesisVideoStreamPool, func(t *testing.T) string {
			return chimeARN(chimeKindOf(t, "ListMediaPipelineKinesisVideoStreamPools"), "tagged")
		}, true},
		{chimeVoice, TypeChimeVoiceConnector, func(t *testing.T) string {
			return chimeARN(chimeKindOf(t, "ListVoiceConnectors"), "tagged")
		}, true},
	} {
		t.Run(tc.sdk, func(t *testing.T) {
			arn := tc.arn(t)
			notFound := struct {
				outcome serviceOutcome
				code    string
				total   int
				kept    bool
			}{outcomeError, "NotFoundException", chimeTotalCount, true}
			if tc.modelsNotFound {
				notFound.outcome, notFound.code, notFound.total, notFound.kept = 0, "", chimeTotalCount-1, false
			}
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"denied", chimeDenied, 0, "", chimeTotalCount, true, 1},
				{"unauthorized client", chimeUnauth, 0, "", chimeTotalCount, true, 1},
				{"not found", chimeGone, notFound.outcome, notFound.code, notFound.total, notFound.kept, 0},
				{"transient", chimeThrottle, outcomeTransient, "", chimeTotalCount, true, 0},
				{"other error", chimeBadRequest, outcomeError, "BadRequestException", chimeTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, chimeTagsOp)
					calls := newChimeCalls(t)
					calls.errs[arn] = c.err

					total, _, err := chimeScan(context.Background(), st, chimeFixture(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := chimeRowOf(t, st, tc.typ, arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", chimeTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Tag reads of two rows of one phase both denied warn once.
func TestScanChime_TagDenialsOfOnePhaseWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, chimeTagsOp)
	calls := newChimeCalls(t)
	k := chimeChildKindOf(t, "ListAppInstanceBots")
	calls.errs[chimeChildARN(t, k, "tagged")] = chimeDenied
	calls.errs[chimeChildARN(t, k, "bare")] = chimeUnauth

	if _, _, err := chimeScan(context.Background(), st, chimeFixture(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", chimeTagsOp, *warns)
	}
}

// A tag read's throttle is demoted to a warning beside another phase's hard
// error, so the scan reports the hard error.
func TestScanChime_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, chimeTagsOp)
	calls := newChimeCalls(t)
	calls.errs[chimeARN(chimeKindOf(t, "ListSipMediaApplications"), "tagged")] = chimeThrottle
	calls.errs[chimeARN(chimeKindOf(t, "ListMediaPipelines"), "bare")] = chimeBadRequest

	_, _, err := chimeScan(context.Background(), st, chimeFixture(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", chimeTagsOp, *warns)
	}
}

// A denied listing, ForbiddenException with an action or a 401
// UnauthorizedClientException, warns once under its own op, is not a scan
// error, and stores nothing of its type, nor of its children; every other
// listing still runs.
func TestScanChime_DeniedListingWarnsUnderItsOp(t *testing.T) {
	children := map[string]int{"ListAppInstances": 6, "ListVoiceProfileDomains": 2}
	for _, denial := range []error{chimeDenied, chimeUnauth} {
		for _, k := range chimeKinds() {
			t.Run(k.op+"/"+denial.Error(), func(t *testing.T) {
				st := newTestStore(t)
				all := countWarnings(st)
				warnings := bacWarnings(st, "chime:"+k.op)
				handlers := chimeFixture(newChimeCalls(t))
				handlers[k.op] = func(any) (any, error) { return nil, denial }

				total, _, err := chimeScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if want := chimeTotalCount - 2 - children[k.op]; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if len(*warnings) != 1 || *all != 1 {
					t.Errorf("chime:%s warnings = %v (all ops %d); want 1", k.op, *warnings, *all)
				}
				if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
					t.Errorf("%s rows = %d; want 0", k.typ, got)
				}
			})
		}
	}
}

// A denial after a listing's first page, a 401 UnauthorizedClientException
// included, keeps that page's rows, warns once under the op and is not a scan
// error.
func TestScanChime_LaterPageDenialKeepsPage(t *testing.T) {
	for _, op := range []string{"ListAppInstances", "ListVoiceConnectors", "ListVoiceProfileDomains", "ListSipRules"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "chime:"+op)
			handlers := chimeFixture(newChimeCalls(t))
			handlers[op] = failOnCall(2, chimeUnauth, handlers[op])
			k := chimeKindOf(t, op)

			total, _, err := chimeScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			lost := map[string]int{"ListAppInstances": 4, "ListVoiceProfileDomains": 2}[op]
			if want := chimeTotalCount - max(lost, 1); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if _, ok := chimeRowOf(t, st, k.typ, chimeARN(k, "tagged")); !ok {
				t.Error("the first page's row is missing")
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("chime:%s warnings = %v (all ops %d); want 1", op, *warnings, *all)
			}
		})
	}
}

// A child listing denied for both parents warns once under its op, the
// ForbiddenException and UnauthorizedClientException parents tallied
// together; every other listing still stores its rows.
func TestScanChime_DeniedChildListingWarnsOnce(t *testing.T) {
	for _, k := range chimeChildKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "chime:"+k.op)
			handlers := chimeFixture(newChimeCalls(t))
			parent := chimeKindOf(t, k.parentOp)
			handlers[k.op] = func(in any) (any, error) {
				// App instances are keyed by ARN, voice profile domains by id.
				if key := sv(bacField[*string](in, k.parentField)); key == chimeARN(parent, "tagged") || key == chimeID(parent, "tagged") {
					return nil, chimeDenied
				}
				return nil, chimeUnauth
			}

			total, _, err := chimeScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != chimeTotalCount-2 {
				t.Errorf("total = %d; want %d", total, chimeTotalCount-2)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("chime:%s warnings = %v (all ops %d); want 1", k.op, *warnings, *all)
			}
		})
	}
}

// A voice profile domain deleted after its listing (ListVoiceProfiles answers
// NotFoundException, which the op models) is skipped silently; its sibling's
// profiles are stored.
func TestScanChime_VoiceProfilesOfDeletedDomainSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newChimeCalls(t)
	handlers := chimeFixture(calls)
	k := chimeChildKindOf(t, "ListVoiceProfiles")
	gone := chimeID(chimeKindOf(t, "ListVoiceProfileDomains"), "tagged")
	next := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if sv(in.(*cv.ListVoiceProfilesInput).VoiceProfileDomainId) == gone {
			return nil, chimeGone
		}
		return next(in)
	}

	total, _, err := chimeScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != chimeTotalCount-1 || *warns != 0 {
		t.Errorf("total = %d, warnings = %d; want %d, 0", total, *warns, chimeTotalCount-1)
	}
	if _, ok := chimeRowOf(t, st, k.typ, chimeChildARN(t, k, "bare")); !ok {
		t.Error("the sibling domain's voice profile is missing")
	}
}

// GetVoiceProfileDomain: NotFoundException, which the op models, drops the
// domain and lists no profiles under it; a denial keeps the list element and
// warns once under chime:GetVoiceProfileDomain; any other error keeps the list
// element and is returned. A domain listed without an id is stored as its
// list element, not read, and warned about as stored without its profiles.
func TestScanChime_VoiceProfileDomainDetail(t *testing.T) {
	vpd := chimeKindOf(t, "ListVoiceProfileDomains")
	id, arn := chimeID(vpd, "tagged"), chimeARN(vpd, "tagged")
	for _, c := range []struct {
		name      string
		err       error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}{
		{"not found", chimeGone, "", chimeTotalCount - 2, false, 0},
		{"denied", chimeDenied, "", chimeTotalCount, true, 1},
		{"unauthorized client", chimeUnauth, "", chimeTotalCount, true, 1},
		{"other error", chimeBadRequest, "BadRequestException", chimeTotalCount, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "chime:GetVoiceProfileDomain")
			calls := newChimeCalls(t)
			calls.getErrs[id] = c.err

			total, _, err := chimeScan(context.Background(), st, chimeFixture(calls))
			if c.code == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := chimeRowOf(t, st, TypeChimeVoiceProfileDomain, arn)
			if ok != c.wantKept {
				t.Fatalf("domain stored = %v; want %v", ok, c.wantKept)
			}
			if ok && strings.Contains(r.AttributesJSON, chimeKMSKey) {
				t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
			}
			if len(*warnings) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("chime:GetVoiceProfileDomain warnings = %v (all ops %d); want %d", *warnings, *all, c.wantWarns)
			}
		})
	}

	t.Run("no id", func(t *testing.T) {
		st := newTestStore(t)
		all := countWarnings(st)
		warnings := bacWarnings(st, "chime:ListVoiceProfileDomains")
		calls := newChimeCalls(t)
		handlers := chimeFixture(calls)
		handlers[vpd.op] = func(any) (any, error) {
			return &cv.ListVoiceProfileDomainsOutput{VoiceProfileDomains: []cvtypes.VoiceProfileDomainSummary{{VoiceProfileDomainArn: sdkaws.String(arn), Name: chimeNameOf(id)}}}, nil
		}

		total, _, err := chimeScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if total != chimeTotalCount-3 {
			t.Errorf("total = %d; want %d", total, chimeTotalCount-3)
		}
		if _, ok := chimeRowOf(t, st, TypeChimeVoiceProfileDomain, arn); !ok {
			t.Error("domain without an id not stored")
		}
		if n := calls.count("GetVoiceProfileDomain") + calls.count("ListVoiceProfiles"); n != 0 {
			t.Errorf("Get and ListVoiceProfiles calls = %d; want 0", n)
		}
		if len(*warnings) != 1 || *all != 1 {
			t.Errorf("chime:ListVoiceProfileDomains warnings = %v (all ops %d); want 1", *warnings, *all)
		}
	})
}

// isChimeHostGap accepts the availability answers of a Chime SDK endpoint and
// refuses an IAM denial.
func TestIsChimeHostGap(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{apiErr("ForbiddenException", "This feature is not available"), true},
		{apiErr("ForbiddenException", "AWS account is not enabled"), true},
		{apiErr("ForbiddenException", ""), true},
		{apiErr("UnknownOperationException", ""), true},
		{chimeDenied, false},
		{chimeBadRequest, false},
	} {
		if got := isChimeHostGap(c.err); got != c.want {
			t.Errorf("isChimeHostGap(%v) = %v; want %v", c.err, got, c.want)
		}
	}
}

// A listing whose first page answers that the op is not served here
// (isChimeHostGap) stores nothing of its type without a warning; the same
// answer after a page keeps that page and warns as the denial it is coded as.
// Channel flows, the messaging host's first call, read each parent's first
// page alike.
func TestScanChime_HostGap(t *testing.T) {
	for _, op := range []string{"ListAppInstances", "ListMediaPipelines", "ListVoiceConnectors", "ListSipRules", "ListChannelFlows"} {
		for _, onPage2 := range []bool{false, true} {
			if op == "ListChannelFlows" && onPage2 {
				continue
			}
			name := op + "/first page"
			if onPage2 {
				name = op + "/second page"
			}
			t.Run(name, func(t *testing.T) {
				st := newTestStore(t)
				all := countWarnings(st)
				handlers := chimeFixture(newChimeCalls(t))
				next := handlers[op]
				handlers[op] = func(in any) (any, error) {
					if (bacField[*string](in, "NextToken") != nil) == onPage2 {
						return nil, chimeGap
					}
					return next(in)
				}

				total, _, err := chimeScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				// The rows the gap costs: the page's element, and an app
				// instance's three children.
				lost, wantWarns := cmp.Or(map[string]int{"ListAppInstances": 8}[op], 2), 0
				if onPage2 {
					lost, wantWarns = cmp.Or(map[string]int{"ListAppInstances": 4}[op], 1), 1
				}
				if total != chimeTotalCount-lost {
					t.Errorf("total = %d; want %d", total, chimeTotalCount-lost)
				}
				if *all != wantWarns {
					t.Errorf("warnings = %d; want %d", *all, wantWarns)
				}
			})
		}
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanChime_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := chimeFixture(newChimeCalls(t))
	k := chimeKindOf(t, "ListMediaInsightsPipelineConfigurations")
	handlers[k.op] = failOnCall(2, chimeBadRequest, handlers[k.op])

	total, _, err := chimeScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if total != chimeTotalCount-1 {
		t.Errorf("total = %d; want %d", total, chimeTotalCount-1)
	}
	if _, ok := chimeRowOf(t, st, k.typ, chimeARN(k, "tagged")); !ok {
		t.Error("configuration from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeChimeVoiceConnectorGroup)); got != 2 {
		t.Errorf("voice connector groups stored = %d; want 2", got)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanChime_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := chimeFixture(newChimeCalls(t))
	k := chimeKindOf(t, "ListVoiceConnectorGroups")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return k.page(sdkaws.String(chimeARN(k, suffix)), chimeID(k, suffix), sdkaws.String("same")), nil
	}

	total, _, err := chimeScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != chimeTotalCount {
		t.Errorf("total = %d; want %d", total, chimeTotalCount)
	}
}

// Elements without an identifier, two in one listing, are warned about once
// under the listing's op, not stored.
func TestScanChime_ElementWithoutIdentifierWarnsOnce(t *testing.T) {
	children := map[string]int{"ListAppInstances": 6, "ListVoiceProfileDomains": 2}
	for _, k := range chimeKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "chime:"+k.op)
			handlers := chimeFixture(newChimeCalls(t))
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") == nil {
					return k.page(nil, chimeID(k, "a"), sdkaws.String("t2")), nil
				}
				return k.page(nil, chimeID(k, "b"), nil), nil
			}

			total, _, err := chimeScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := chimeTotalCount - 2 - children[k.op]; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Errorf("chime:%s warnings = %v (all ops %d); want 1", k.op, *warns, *all)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanChime_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeChimeAppInstance)
	calls := newChimeCalls(t)
	calls.errs[chimeARN(chimeKindOf(t, "ListAppInstances"), "tagged")] = chimeDenied

	_, _, err := chimeScan(context.Background(), st, chimeFixture(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListAppInstanceBots") + calls.count("ListMediaPipelines"); n != 0 {
		t.Errorf("later listings called %d times after the failed write", n)
	}
}

// Rows of an own account in another partition are sent to the tag op under
// the ARNs their listings return, keep the usual own-account rules, and a SIP
// rule's NativeID takes the account's partition.
func TestScanChime_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-cn")
	region := acct.Regions[0]
	tagged := chimeARNIn("aws-cn", region, testAccountID, "vc/vc-cn")
	gone := chimeARNIn("aws-cn", region, testAccountID, "vc/vc-gone")
	st := newTestStore(t)
	calls := newChimeCalls(t)
	calls.tags[tagged] = map[string]string{"k": "v"}
	calls.errs[gone] = chimeGone
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"ListVoiceConnectors": func(any) (any, error) {
			return &cv.ListVoiceConnectorsOutput{VoiceConnectors: []cvtypes.VoiceConnector{
				{VoiceConnectorArn: &tagged, VoiceConnectorId: sdkaws.String("vc-cn")},
				{VoiceConnectorArn: &gone, VoiceConnectorId: sdkaws.String("vc-gone")},
			}}, nil
		},
		"ListSipRules": func(any) (any, error) {
			return &cv.ListSipRulesOutput{SipRules: []cvtypes.SipRule{{SipRuleId: sdkaws.String("r-cn")}}}, nil
		},
	}, chimeGap)

	if _, _, err := scanChime(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := []string{tagged, gone}; !slices.Equal(slices.Sorted(slices.Values(calls.tagARNs[chimeVoice])), want) {
		t.Errorf("voice ListTagsForResource ARNs = %v; want %v", calls.tagARNs[chimeVoice], want)
	}
	rows := rowsByNativeID(t, st, TypeChimeVoiceConnector)
	r, ok := rows[tagged]
	if !ok {
		t.Fatal("aws-cn voice connector missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	if _, ok := rows[gone]; ok {
		t.Error("aws-cn voice connector the tag read did not find was stored")
	}
	if _, ok := rowsByNativeID(t, st, TypeChimeSipRule)[chimeARNIn("aws-cn", region, testAccountID, "sip-rule/r-cn")]; !ok {
		t.Error("aws-cn SIP rule missing under its aws-cn NativeID")
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanChime_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newChimeCalls(t)
	handlers := chimeFixture(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := chimeKindOf(t, "ListMediaPipelines")
	next := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return next(in)
	}

	_, _, err := chimeScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	for _, op := range []string{"ListMediaInsightsPipelineConfigurations", "ListVoiceConnectorGroups"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// No host's NXDOMAIN on its first phase reports the region unavailable or
// fails the scan: the other hosts' rows are stored, and with every host
// absent the scan stores nothing without an error. An NXDOMAIN after a host
// answered is a failure of this scan, returned with the stored rows' counts.
func TestScanChime_NXDOMAIN(t *testing.T) {
	t.Run("one host absent", func(t *testing.T) {
		for _, ops := range [][]string{
			{"ListAppInstances"},
			{"ListMediaPipelines", "ListMediaInsightsPipelineConfigurations", "ListMediaPipelineKinesisVideoStreamPools"},
			{"ListSipMediaApplications", "ListVoiceConnectors", "ListVoiceProfileDomains", "ListSipRules", "ListVoiceConnectorGroups"},
		} {
			t.Run(ops[0], func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				handlers := chimeFixture(newChimeCalls(t))
				called := map[string]int{}
				for _, op := range ops {
					handlers[op] = func(any) (any, error) { called[op]++; return nil, chimeNX }
				}
				total, _, err := chimeScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if total == 0 || *warns != 0 {
					t.Errorf("total = %d, warnings = %d; want the other hosts' rows and no warning", total, *warns)
				}
				for i, op := range ops {
					want := 0
					if i == 0 {
						want = 1
					}
					if called[op] != want {
						t.Errorf("%s called %d times; want %d (later phases of an absent host do not run)", op, called[op], want)
					}
				}
			})
		}
	})
	t.Run("every host absent", func(t *testing.T) {
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, chimeNX)
		total, _, err := scanChime(context.Background(), acct, testRegion, st, testScanID)
		if err != nil || total != 0 {
			t.Errorf("total, err = %d, %v; want 0, nil", total, err)
		}
	})
	t.Run("later phase after the host answered", func(t *testing.T) {
		st := newTestStore(t)
		handlers := chimeFixture(newChimeCalls(t))
		handlers["ListVoiceConnectorGroups"] = func(any) (any, error) { return nil, chimeNX }
		total, _, err := chimeScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != chimeTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, chimeTotalCount-2)
		}
	})
}

func TestChimeSipRuleNativeID(t *testing.T) {
	got := chimeSipRuleNativeID("aws", "eu-west-2", "123456789012", "abc-123")
	if want := "arn:aws:chime:eu-west-2:123456789012:sip-rule/abc-123"; got != want {
		t.Errorf("chimeSipRuleNativeID = %q, want %q", got, want)
	}
}

// Each of scanChime's four clients retries Chime's ThrottledClientException,
// which the SDK retryer does not retry by itself: a single throttle costs no
// rows and no error.
func TestScanChime_ThrottledClientExceptionIsRetried(t *testing.T) {
	const aiARN = "arn:aws:chime:us-east-1:123456789012:app-instance/ai-1"
	appInstances := func(any) (any, error) {
		return &ci.ListAppInstancesOutput{AppInstances: []citypes.AppInstanceSummary{{AppInstanceArn: sdkaws.String(aiARN)}}}, nil
	}
	for _, tc := range []struct {
		op, typ string
		page    func(any) (any, error)
	}{
		{"ListAppInstances", TypeChimeAppInstance, appInstances},
		{"ListChannelFlows", TypeChimeChannelFlow, func(any) (any, error) {
			return &cm.ListChannelFlowsOutput{ChannelFlows: []cmtypes.ChannelFlowSummary{{ChannelFlowArn: sdkaws.String(aiARN + "/channel-flow/cf-1")}}}, nil
		}},
		{"ListMediaPipelines", TypeChimeMediaPipeline, func(any) (any, error) {
			return &cp.ListMediaPipelinesOutput{MediaPipelines: []cptypes.MediaPipelineSummary{{MediaPipelineArn: sdkaws.String("arn:aws:chime:us-east-1:123456789012:media-pipeline/mp-1")}}}, nil
		}},
		{"ListSipMediaApplications", TypeChimeSipMediaApplication, func(any) (any, error) {
			return &cv.ListSipMediaApplicationsOutput{SipMediaApplications: []cvtypes.SipMediaApplication{{SipMediaApplicationArn: sdkaws.String("arn:aws:chime:us-east-1:123456789012:sma/sma-1")}}}, nil
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			handlers := map[string]func(any) (any, error){
				"ListAppInstances": appInstances,
				tc.op: func(in any) (any, error) {
					calls++
					if calls == 1 {
						return nil, chimeThrottle
					}
					return tc.page(in)
				},
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = lmRetryingConfig(handlers)
			if _, _, err := scanChime(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanChime: %v", err)
			}
			if calls != 2 || len(rowsByNativeID(t, st, tc.typ)) != 1 {
				t.Errorf("%s calls = %d, %s rows = %d; want 2 calls and the retried page's row", tc.op, calls, tc.typ, len(rowsByNativeID(t, st, tc.typ)))
			}
		})
	}
}

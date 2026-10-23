package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/chatbot"
	chatbottypes "github.com/aws/aws-sdk-go-v2/service/chatbot/types"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	cbTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: chatbot:Describe")
	errCBTest    = errors.New("chatbot test failure")
)

const (
	cbActionARN1   = "arn:aws:chatbot::123456789012:custom-action/act1"
	cbActionARN2   = "arn:aws:chatbot::123456789012:custom-action/act2"
	cbSlackARN     = "arn:aws:chatbot::123456789012:chat-configuration/slack-channel/alpha"
	cbSlackARN2    = "arn:aws:chatbot::123456789012:chat-configuration/slack-channel/alpha2"
	cbSlackGoneARN = "arn:aws:chatbot::123456789012:chat-configuration/slack-channel/gone"
	cbTeamsARN     = "arn:aws:chatbot::123456789012:chat-configuration/microsoft-teams-channel/beta"
	cbTeamsARN2    = "arn:aws:chatbot::123456789012:chat-configuration/microsoft-teams-channel/beta2"
	cbChimeARN1    = "arn:aws:chatbot::123456789012:chat-configuration/chime-webhook/c1"
	cbChimeARN2    = "arn:aws:chatbot::123456789012:chat-configuration/chime-webhook/c2"
	cbRoleARN      = "arn:aws:iam::123456789012:role/chatbot"
	cbTopicARN     = "arn:aws:sns:us-east-2:123456789012:alerts"
	cbTeamsUser1   = "0f0e0d0c-0b0a-0908-0706-050403020100"
	cbTeamsUser2   = "1f1e1d1c-1b1a-1918-1716-151413121110"
	cbTeamsTeamID  = "a0a1a2a3-a4a5-a6a7-a8a9-aaabacadaeaf"
	cbTenantID     = "b0b1b2b3-b4b5-b6b7-b8b9-babbbcbdbebf"
)

// cbTags is the tags of the tagged fixture elements; the valueless tag must be
// kept with an empty value, not dropped.
var (
	cbTags     = []chatbottypes.Tag{{TagKey: ptrStr("env"), TagValue: ptrStr("prod")}, {TagKey: ptrStr("blank")}}
	cbTagsWant = map[string]string{"env": "prod", "blank": ""}
)

func cbSlackChannel(arn, name string, tags []chatbottypes.Tag) chatbottypes.SlackChannelConfiguration {
	return chatbottypes.SlackChannelConfiguration{
		ChatConfigurationArn: ptrStr(arn), ConfigurationName: ptrStr(name), IamRoleArn: ptrStr(cbRoleARN),
		SlackChannelId: ptrStr("C0123ABCD"), SlackChannelName: ptrStr("ops"), SlackTeamId: ptrStr("T01"), SlackTeamName: ptrStr("acme"),
		SnsTopicArns: []string{cbTopicARN}, GuardrailPolicyArns: []string{"arn:aws:iam::aws:policy/ReadOnlyAccess"},
		LoggingLevel: ptrStr("ERROR"), State: ptrStr("ENABLED"), Tags: tags, UserAuthorizationRequired: sdkaws.Bool(true),
	}
}

func cbTeamsChannel(arn, name string, tags []chatbottypes.Tag) chatbottypes.TeamsChannelConfiguration {
	return chatbottypes.TeamsChannelConfiguration{
		ChatConfigurationArn: ptrStr(arn), ConfigurationName: ptrStr(name), IamRoleArn: ptrStr(cbRoleARN),
		ChannelId: ptrStr("19:abc@thread.tacv2"), ChannelName: ptrStr("ops"), TeamId: ptrStr(cbTeamsTeamID), TeamName: ptrStr("acme"),
		TenantId: ptrStr(cbTenantID), SnsTopicArns: []string{cbTopicARN}, State: ptrStr("DISABLED"), StateReason: ptrStr("policy"), Tags: tags,
	}
}

func cbChime(arn, name string) chatbottypes.ChimeWebhookConfiguration {
	return chatbottypes.ChimeWebhookConfiguration{
		ChatConfigurationArn: ptrStr(arn), ConfigurationName: ptrStr(name), IamRoleArn: ptrStr(cbRoleARN),
		SnsTopicArns: []string{cbTopicARN}, WebhookDescription: ptrStr("room/hook"),
		State: ptrStr("ENABLED"), Tags: cbTags,
	}
}

func cbSlackUser(configARN, user string) chatbottypes.SlackUserIdentity {
	return chatbottypes.SlackUserIdentity{
		ChatConfigurationArn: ptrStr(configARN), IamRoleArn: ptrStr(cbRoleARN), SlackTeamId: ptrStr("T01"), SlackUserId: ptrStr(user),
	}
}

func cbTeamsUser(user string) chatbottypes.TeamsUserIdentity {
	return chatbottypes.TeamsUserIdentity{
		ChatConfigurationArn: ptrStr(cbTeamsARN), IamRoleArn: ptrStr(cbRoleARN), TeamId: ptrStr(cbTeamsTeamID), UserId: ptrStr(user),
	}
}

// cbPages answers a two-page listing: first for a request without a token,
// second for the "p2" token. MaxResults must be the model's cap and every
// filter unset, or the call fails, so the scanner's request inputs are pinned.
func cbPages(token *string, maxResults *int32, filters []*string, first, second any) (any, error) {
	if sdkaws.ToInt32(maxResults) != 100 {
		return nil, fmt.Errorf("MaxResults = %v, want 100", sdkaws.ToInt32(maxResults))
	}
	for _, f := range filters {
		if f != nil {
			return nil, fmt.Errorf("filter set to %q, want unset", *f)
		}
	}
	switch sv(token) {
	case "":
		return first, nil
	case "p2":
		return second, nil
	}
	return nil, fmt.Errorf("unexpected NextToken %q", sv(token))
}

// cbHandlers answers every chatbot op with two pages: the first holds a valid
// element and one without its key, the second a valid element.
func cbHandlers() map[string]func(any) (any, error) {
	p2 := ptrStr("p2")
	return map[string]func(any) (any, error){
		"ListCustomActions": func(in any) (any, error) {
			req := in.(*chatbot.ListCustomActionsInput)
			return cbPages(req.NextToken, req.MaxResults, nil,
				&chatbot.ListCustomActionsOutput{CustomActions: []string{cbActionARN1, ""}, NextToken: p2},
				&chatbot.ListCustomActionsOutput{CustomActions: []string{cbActionARN2}})
		},
		"DescribeSlackChannelConfigurations": func(in any) (any, error) {
			req := in.(*chatbot.DescribeSlackChannelConfigurationsInput)
			return cbPages(req.NextToken, req.MaxResults, []*string{req.ChatConfigurationArn},
				&chatbot.DescribeSlackChannelConfigurationsOutput{SlackChannelConfigurations: []chatbottypes.SlackChannelConfiguration{
					cbSlackChannel(cbSlackARN, "alpha", cbTags), {ConfigurationName: ptrStr("no-arn-slack")},
				}, NextToken: p2},
				&chatbot.DescribeSlackChannelConfigurationsOutput{SlackChannelConfigurations: []chatbottypes.SlackChannelConfiguration{
					cbSlackChannel(cbSlackARN2, "alpha2", nil),
				}})
		},
		"ListMicrosoftTeamsChannelConfigurations": func(in any) (any, error) {
			req := in.(*chatbot.ListMicrosoftTeamsChannelConfigurationsInput)
			return cbPages(req.NextToken, req.MaxResults, []*string{req.TeamId},
				&chatbot.ListMicrosoftTeamsChannelConfigurationsOutput{TeamChannelConfigurations: []chatbottypes.TeamsChannelConfiguration{
					cbTeamsChannel(cbTeamsARN, "beta", cbTags), {ConfigurationName: ptrStr("no-arn-teams")},
				}, NextToken: p2},
				&chatbot.ListMicrosoftTeamsChannelConfigurationsOutput{TeamChannelConfigurations: []chatbottypes.TeamsChannelConfiguration{
					cbTeamsChannel(cbTeamsARN2, "beta2", nil),
				}})
		},
		"DescribeChimeWebhookConfigurations": func(in any) (any, error) {
			req := in.(*chatbot.DescribeChimeWebhookConfigurationsInput)
			untagged := cbChime(cbChimeARN2, "c2")
			untagged.Tags = nil
			return cbPages(req.NextToken, req.MaxResults, []*string{req.ChatConfigurationArn},
				&chatbot.DescribeChimeWebhookConfigurationsOutput{WebhookConfigurations: []chatbottypes.ChimeWebhookConfiguration{
					cbChime(cbChimeARN1, "c1"), {ConfigurationName: ptrStr("no-arn-chime")},
				}, NextToken: p2},
				&chatbot.DescribeChimeWebhookConfigurationsOutput{WebhookConfigurations: []chatbottypes.ChimeWebhookConfiguration{untagged}})
		},
		"DescribeSlackUserIdentities": func(in any) (any, error) {
			req := in.(*chatbot.DescribeSlackUserIdentitiesInput)
			return cbPages(req.NextToken, req.MaxResults, []*string{req.ChatConfigurationArn},
				&chatbot.DescribeSlackUserIdentitiesOutput{SlackUserIdentities: []chatbottypes.SlackUserIdentity{
					cbSlackUser(cbSlackARN, "U1"), {ChatConfigurationArn: ptrStr(cbSlackARN), SlackTeamId: ptrStr("T01")},
				}, NextToken: p2},
				&chatbot.DescribeSlackUserIdentitiesOutput{SlackUserIdentities: []chatbottypes.SlackUserIdentity{cbSlackUser(cbSlackGoneARN, "U2")}})
		},
		"ListMicrosoftTeamsUserIdentities": func(in any) (any, error) {
			req := in.(*chatbot.ListMicrosoftTeamsUserIdentitiesInput)
			return cbPages(req.NextToken, req.MaxResults, []*string{req.ChatConfigurationArn},
				&chatbot.ListMicrosoftTeamsUserIdentitiesOutput{TeamsUserIdentities: []chatbottypes.TeamsUserIdentity{
					cbTeamsUser(cbTeamsUser1), {ChatConfigurationArn: ptrStr(cbTeamsARN)},
				}, NextToken: p2},
				&chatbot.ListMicrosoftTeamsUserIdentitiesOutput{TeamsUserIdentities: []chatbottypes.TeamsUserIdentity{cbTeamsUser(cbTeamsUser2)}})
		},
	}
}

// cbOp is one op of the chatbot scan with what cbHandlers makes it store: the
// valid element of each page and the key of the element it cannot store.
type cbOp struct {
	op, label, typ           string
	page1Native, page2Native string
	skippedKey               string
}

var cbOps = []cbOp{
	{"ListCustomActions", "chatbot:ListCustomActions", TypeChatbotCustomAction, cbActionARN1, cbActionARN2, "(empty)"},
	{"DescribeSlackChannelConfigurations", "chatbot:DescribeSlackChannelConfigurations", TypeChatbotSlackChannelConfiguration, cbSlackARN, cbSlackARN2, "no-arn-slack"},
	{"ListMicrosoftTeamsChannelConfigurations", "chatbot:ListMicrosoftTeamsChannelConfigurations", TypeChatbotMicrosoftTeamsChannelConfiguration, cbTeamsARN, cbTeamsARN2, "no-arn-teams"},
	{"DescribeChimeWebhookConfigurations", "chatbot:DescribeChimeWebhookConfigurations", TypeChatbotChimeWebhookConfiguration, cbChimeARN1, cbChimeARN2, "no-arn-chime"},
	{"DescribeSlackUserIdentities", "chatbot:DescribeSlackUserIdentities", TypeChatbotSlackUserIdentity, cbSlackARN + "/user-identity/T01/U1", cbSlackGoneARN + "/user-identity/T01/U2", cbSlackARN + " T01 "},
	{"ListMicrosoftTeamsUserIdentities", "chatbot:ListMicrosoftTeamsUserIdentities", TypeChatbotMicrosoftTeamsUserIdentity, cbTeamsARN + "/user-identity/" + cbTeamsUser1, cbTeamsARN + "/user-identity/" + cbTeamsUser2, cbTeamsARN + " "},
}

// cbAllRows is the number of rows cbHandlers makes the scan store: two per op.
const cbAllRows = 12

// cbScan is the outcome of a chatbot scan and the warnings it recorded.
type cbScan struct {
	total, inserted int
	err             error
	warnings        []store.ScanWarning
}

// opWarns is the messages of the warnings recorded under label.
func (r cbScan) opWarns(label string) []string {
	var msgs []string
	for _, w := range r.warnings {
		if w.Service == label {
			msgs = append(msgs, w.Message)
		}
	}
	return msgs
}

func runChatbotCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error), regions *parentCalls) cbScan {
	t.Helper()
	var r cbScan
	var mu sync.Mutex
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		r.warnings = append(r.warnings, w)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfigCtx(handlers, cbTestDenied), regions)
	r.total, r.inserted, r.err = scanChatbot(ctx, acct, "", st, testScanID)
	return r
}

func runChatbot(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) cbScan {
	t.Helper()
	return runChatbotCtx(context.Background(), t, st, ignoreCtx(handlers), &parentCalls{})
}

// failPage2 makes op answer its first page and fail every later one with err.
func failPage2(h map[string]func(any) (any, error), op string, err error) {
	first := h[op]
	calls := 0
	h[op] = func(in any) (any, error) {
		calls++
		if calls > 1 {
			return nil, err
		}
		return first(in)
	}
}

// assertCBRowCounts checks the stored rows of each op's type: want, except
// the overrides.
func assertCBRowCounts(t *testing.T, st *store.Store, want int, overrides map[string]int) {
	t.Helper()
	for _, o := range cbOps {
		w := want
		if n, ok := overrides[o.typ]; ok {
			w = n
		}
		if n := len(rowsByNativeID(t, st, o.typ)); n != w {
			t.Errorf("%s rows = %d, want %d", o.typ, n, w)
		}
	}
}

func TestScanChatbot_StoresEveryType(t *testing.T) {
	st := newTestStore(t)
	// U2's configuration is a stored row that this scan's listing did not
	// return, so U2 must not be linked under it.
	seedParentRows(t, st, TypeChatbotSlackChannelConfiguration, cbSlackGoneARN)
	var regions parentCalls
	r := runChatbotCtx(context.Background(), t, st, ignoreCtx(cbHandlers()), &regions)
	if r.err != nil {
		t.Fatalf("err = %v", r.err)
	}
	if r.total != cbAllRows || r.inserted != cbAllRows {
		t.Errorf("total, inserted = %d, %d; want %d, %d", r.total, r.inserted, cbAllRows, cbAllRows)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-2"}) {
		t.Errorf("call regions = %v, want [us-east-2]", got)
	}
	// Each op warns once, naming the element it could not store.
	for _, o := range cbOps {
		msgs := r.opWarns(o.label)
		if len(msgs) != 1 || !strings.Contains(msgs[0], "first "+o.skippedKey) {
			t.Errorf("%s warnings = %q; want one naming %q", o.label, msgs, o.skippedKey)
		}
		rows := rowsByNativeID(t, st, o.typ)
		for _, n := range []string{o.page1Native, o.page2Native} {
			if _, ok := rows[n]; !ok {
				t.Errorf("%s row %s missing", o.typ, n)
			}
		}
	}
	if len(r.warnings) != len(cbOps) {
		t.Errorf("warnings = %+v; want one per op", r.warnings)
	}

	actions := rowsByNativeID(t, st, TypeChatbotCustomAction)
	if a := actions[cbActionARN1]; sv(a.Name) != cbActionARN1 || sv(a.Region) != "global" || a.TagsJSON != nil {
		t.Errorf("custom action = name %q region %q tags %v", sv(a.Name), sv(a.Region), a.TagsJSON)
	} else {
		assertJSONEqual(t, "custom action attributes", a.AttributesJSON, `{"CustomActionArn":"`+cbActionARN1+`"}`)
	}

	configs := []struct {
		typ, native, name, status string
		element                   any
		tagged                    bool
	}{
		{TypeChatbotSlackChannelConfiguration, cbSlackARN, "alpha", "ENABLED", cbSlackChannel(cbSlackARN, "alpha", cbTags), true},
		{TypeChatbotSlackChannelConfiguration, cbSlackARN2, "alpha2", "ENABLED", cbSlackChannel(cbSlackARN2, "alpha2", nil), false},
		{TypeChatbotMicrosoftTeamsChannelConfiguration, cbTeamsARN, "beta", "DISABLED", cbTeamsChannel(cbTeamsARN, "beta", cbTags), true},
		{TypeChatbotMicrosoftTeamsChannelConfiguration, cbTeamsARN2, "beta2", "DISABLED", cbTeamsChannel(cbTeamsARN2, "beta2", nil), false},
	}
	for _, c := range configs {
		got := rowsByNativeID(t, st, c.typ)[c.native]
		if got.Type != c.typ || sv(got.Name) != c.name || sv(got.Region) != "global" || sv(got.Status) != c.status {
			t.Errorf("%s = type %q name %q region %q status %q; want %s, %s, global, %s", c.native, got.Type, sv(got.Name), sv(got.Region), sv(got.Status), c.typ, c.name, c.status)
		}
		assertJSONEqual(t, c.native+" attributes", got.AttributesJSON, mustJSON(c.element))
		if c.tagged {
			assertTagsMap(t, got.TagsJSON, cbTagsWant)
		} else if got.TagsJSON != nil {
			t.Errorf("%s tags = %s, want none", c.native, *got.TagsJSON)
		}
	}

	chime := rowsByNativeID(t, st, TypeChatbotChimeWebhookConfiguration)
	c1 := chime[cbChimeARN1]
	if sv(c1.Name) != "c1" || sv(c1.Region) != "global" || sv(c1.Status) != "ENABLED" {
		t.Errorf("chime row = name %q region %q status %q", sv(c1.Name), sv(c1.Region), sv(c1.Status))
	}
	wantChime := cbChime(cbChimeARN1, "c1")
	wantChime.WebhookDescription = ptrStr(redact.Placeholder)
	assertJSONEqual(t, "chime attributes", c1.AttributesJSON, mustJSON(wantChime))
	assertTagsMap(t, c1.TagsJSON, cbTagsWant)
	if c2 := chime[cbChimeARN2]; c2.TagsJSON != nil {
		t.Errorf("untagged chime tags = %s, want none", *c2.TagsJSON)
	}

	slack := rowsByNativeID(t, st, TypeChatbotSlackUserIdentity)
	u1Native := cbSlackARN + "/user-identity/T01/U1"
	u1 := slack[u1Native]
	if u1.Type != TypeChatbotSlackUserIdentity || sv(u1.Region) != "global" || sv(u1.Name) != "U1" {
		t.Errorf("slack identity row = type %q region %q name %q", u1.Type, sv(u1.Region), sv(u1.Name))
	}
	assertJSONEqual(t, "slack identity attributes", u1.AttributesJSON, mustJSON(cbSlackUser(cbSlackARN, "U1")))
	assertContainsNative(t, st, cbSlackARN, u1Native)
	// U2's configuration was not listed this scan: stored, but under no parent.
	u2Native := cbSlackGoneARN + "/user-identity/T01/U2"
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, u2Native), store.RelContains)
	if err != nil || len(rels) != 0 {
		t.Errorf("contains into %s = %v (%v), want none", u2Native, rels, err)
	}

	teams := rowsByNativeID(t, st, TypeChatbotMicrosoftTeamsUserIdentity)
	for _, user := range []string{cbTeamsUser1, cbTeamsUser2} {
		native := cbTeamsARN + "/user-identity/" + user
		got := teams[native]
		if got.Type != TypeChatbotMicrosoftTeamsUserIdentity || sv(got.Region) != "global" || sv(got.Name) != user {
			t.Errorf("teams identity row = type %q region %q name %q", got.Type, sv(got.Region), sv(got.Name))
		}
		assertJSONEqual(t, native+" attributes", got.AttributesJSON, mustJSON(cbTeamsUser(user)))
		assertContainsNative(t, st, cbTeamsARN, native)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanChatbot_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	h := map[string]func(any) (any, error){
		"ListCustomActions": func(any) (any, error) { return &chatbot.ListCustomActionsOutput{}, nil },
		"DescribeSlackChannelConfigurations": func(any) (any, error) {
			return &chatbot.DescribeSlackChannelConfigurationsOutput{}, nil
		},
		"ListMicrosoftTeamsChannelConfigurations": func(any) (any, error) {
			return &chatbot.ListMicrosoftTeamsChannelConfigurationsOutput{}, nil
		},
		"DescribeChimeWebhookConfigurations": func(any) (any, error) {
			return &chatbot.DescribeChimeWebhookConfigurationsOutput{}, nil
		},
		"DescribeSlackUserIdentities": func(any) (any, error) { return &chatbot.DescribeSlackUserIdentitiesOutput{}, nil },
		"ListMicrosoftTeamsUserIdentities": func(any) (any, error) {
			return &chatbot.ListMicrosoftTeamsUserIdentitiesOutput{}, nil
		},
	}
	r := runChatbot(t, st, h)
	if r.err != nil || r.total != 0 || r.inserted != 0 || len(r.warnings) != 0 {
		t.Errorf("scan = total %d inserted %d err %v warnings %+v; want nothing", r.total, r.inserted, r.err, r.warnings)
	}
	assertCBRowCounts(t, st, 0, nil)
}

// A denied op warns once and costs no other phase its rows.
func TestScanChatbot_OpDenied(t *testing.T) {
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			delete(h, o.op)
			r := runChatbot(t, st, h)
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			if got := r.opWarns(o.label); len(got) != 1 {
				t.Errorf("%s warnings = %q, want 1", o.label, got)
			}
			if r.total != cbAllRows-2 || r.inserted != cbAllRows-2 {
				t.Errorf("total, inserted = %d, %d; want %d (every other phase's rows)", r.total, r.inserted, cbAllRows-2)
			}
			assertCBRowCounts(t, st, 2, map[string]int{o.typ: 0})
		})
	}
}

// ListCustomActions models UnauthorizedException for a denial: it warns once,
// keeps the pages read before it and fails no phase.
func TestScanChatbotCustomActions_UnauthorizedIsDenial(t *testing.T) {
	unauthorized := apiErr("UnauthorizedException", "not authorized")
	for _, tc := range []struct {
		name      string
		onPage2   bool
		wantRows  int
		wantWarns int
	}{
		{"first page", false, 0, 1},
		// The first page's skipped empty ARN is warned about too.
		{"second page", true, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			if tc.onPage2 {
				failPage2(h, "ListCustomActions", unauthorized)
			} else {
				h["ListCustomActions"] = func(any) (any, error) { return nil, unauthorized }
			}
			r := runChatbot(t, st, h)
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			if got := r.opWarns("chatbot:ListCustomActions"); len(got) != tc.wantWarns {
				t.Errorf("warnings = %q, want %d", got, tc.wantWarns)
			}
			if r.total != cbAllRows-2+tc.wantRows {
				t.Errorf("total = %d, want %d", r.total, cbAllRows-2+tc.wantRows)
			}
			assertCBRowCounts(t, st, 2, map[string]int{TypeChatbotCustomAction: tc.wantRows})
		})
	}
}

// An UnauthorizedException denial is not warned about when the write of the
// page read before it failed.
func TestScanChatbotCustomActions_UnauthorizedNotWarnedAfterFailedWrite(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeChatbotCustomAction)
	h := cbHandlers()
	failPage2(h, "ListCustomActions", apiErr("UnauthorizedException", "not authorized"))
	r := runChatbot(t, st, h)
	if !errors.Is(r.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", r.err)
	}
	if got := r.opWarns("chatbot:ListCustomActions"); len(got) != 0 {
		t.Errorf("warnings = %q; want none after a failed write", got)
	}
}

// A denial on a later page warns and keeps the rows of the page before it.
func TestScanChatbot_DeniedOnSecondPageKeepsFirst(t *testing.T) {
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			failPage2(h, o.op, cbTestDenied)
			r := runChatbot(t, st, h)
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			// The denial, and the first page's element without its key.
			if got := r.opWarns(o.label); len(got) != 2 {
				t.Errorf("%s warnings = %q, want 2", o.label, got)
			}
			if r.total != cbAllRows-1 || r.inserted != cbAllRows-1 {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, cbAllRows-1)
			}
			rows := rowsByNativeID(t, st, o.typ)
			if _, ok := rows[o.page1Native]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v, want only %s", o.typ, nativeIDs(rows), o.page1Native)
			}
		})
	}
}

// Every phase runs whatever another returns: a hard error anywhere is
// returned, and the other phases' rows are stored.
func TestScanChatbot_PhaseErrorPropagates(t *testing.T) {
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			h[o.op] = func(any) (any, error) { return nil, errCBTest }
			r := runChatbot(t, st, h)
			if !errors.Is(r.err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", r.err, o.op)
			}
			if r.total != cbAllRows-2 || r.inserted != cbAllRows-2 {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, cbAllRows-2)
			}
			assertCBRowCounts(t, st, 2, map[string]int{o.typ: 0})
		})
	}
}

// A failed channel phase stores its identities unlinked: no configuration
// of it was stored to link them under.
func TestScanChatbot_FailedChannelPhaseLeavesIdentitiesUnlinked(t *testing.T) {
	for _, tc := range []struct{ op, childNative string }{
		{"DescribeSlackChannelConfigurations", cbSlackARN + "/user-identity/T01/U1"},
		{"ListMicrosoftTeamsChannelConfigurations", cbTeamsARN + "/user-identity/" + cbTeamsUser1},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			h[tc.op] = func(any) (any, error) { return nil, errCBTest }
			if r := runChatbot(t, st, h); !errors.Is(r.err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", r.err, tc.op)
			}
			rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tc.childNative), store.RelContains)
			if err != nil || len(rels) != 0 {
				t.Errorf("contains into %s = %v (%v), want none", tc.childNative, rels, err)
			}
		})
	}
}

// A hard error after a page was read is returned, and the rows of the page
// read before it are stored (and identities linked).
func TestScanChatbot_ErrorOnSecondPageKeepsFirst(t *testing.T) {
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			failPage2(h, o.op, errCBTest)
			r := runChatbot(t, st, h)
			if !errors.Is(r.err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", r.err, o.op)
			}
			if r.total != cbAllRows-1 || r.inserted != cbAllRows-1 {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, cbAllRows-1)
			}
			rows := rowsByNativeID(t, st, o.typ)
			if _, ok := rows[o.page1Native]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v, want only %s", o.typ, nativeIDs(rows), o.page1Native)
			}
		})
	}
	st := newTestStore(t)
	h := cbHandlers()
	failPage2(h, "DescribeSlackUserIdentities", errCBTest)
	if r := runChatbot(t, st, h); !errors.Is(r.err, errCBTest) {
		t.Fatalf("err = %v, want the DescribeSlackUserIdentities error", r.err)
	}
	assertContainsNative(t, st, cbSlackARN, cbSlackARN+"/user-identity/T01/U1")
}

// A listing whose service repeats the token it was sent ends with
// errRepeatedNextToken, keeping both pages' rows.
func TestScanChatbot_RepeatedTokenReturned(t *testing.T) {
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			pages := h[o.op]
			calls := 0
			h[o.op] = func(in any) (any, error) {
				calls++
				out, err := pages(in)
				if err == nil && calls == 2 {
					setNextToken(out, "p2")
				}
				return out, err
			}
			r := runChatbot(t, st, h)
			if !errors.Is(r.err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", r.err)
			}
			if r.total != cbAllRows {
				t.Errorf("total = %d, want %d", r.total, cbAllRows)
			}
			if n := len(rowsByNativeID(t, st, o.typ)); n != 2 {
				t.Errorf("%s rows = %d, want both pages' 2", o.typ, n)
			}
		})
	}
}

// setNextToken sets the NextToken of a chatbot list output.
func setNextToken(out any, token string) {
	switch o := out.(type) {
	default:
		panic(fmt.Sprintf("setNextToken: unhandled output %T", out))
	case *chatbot.ListCustomActionsOutput:
		o.NextToken = &token
	case *chatbot.DescribeSlackChannelConfigurationsOutput:
		o.NextToken = &token
	case *chatbot.ListMicrosoftTeamsChannelConfigurationsOutput:
		o.NextToken = &token
	case *chatbot.DescribeChimeWebhookConfigurationsOutput:
		o.NextToken = &token
	case *chatbot.DescribeSlackUserIdentitiesOutput:
		o.NextToken = &token
	case *chatbot.ListMicrosoftTeamsUserIdentitiesOutput:
		o.NextToken = &token
	}
}

// A transient error on a later page of a phase is returned after the rows read
// before it are stored, never only warned about.
func TestScanChatbot_TransientAloneReturned(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			failPage2(h, o.op, throttle)
			r := runChatbot(t, st, h)
			if got := classifyServiceError(r.err); got != outcomeTransient {
				t.Fatalf("classifyServiceError(%v) = %v, want outcomeTransient", r.err, got)
			}
			if r.total != cbAllRows-1 || r.inserted != cbAllRows-1 {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, cbAllRows-1)
			}
			if _, ok := rowsByNativeID(t, st, o.typ)[o.page1Native]; !ok {
				t.Errorf("%s row %s read before the throttle not stored", o.typ, o.page1Native)
			}
		})
	}
}

// A failed write stops the scan: no later phase runs, and no warning claims
// rows the write lost.
func TestScanChatbot_StoreWriteStops(t *testing.T) {
	order := make([]string, len(cbOps))
	for i, o := range cbOps {
		order[i] = o.op
	}
	for i, o := range cbOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, o.typ)
			h := cbHandlers()
			var called []string
			for op, fn := range h {
				h[op] = func(in any) (any, error) {
					if !slices.Contains(called, op) {
						called = append(called, op)
					}
					return fn(in)
				}
			}
			r := runChatbot(t, st, h)
			if !errors.Is(r.err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the store write failure", r.err)
			}
			if got := r.opWarns(o.label); len(got) != 0 {
				t.Errorf("%s warnings = %q; want none after a failed write", o.label, got)
			}
			if !slices.Equal(called, order[:i+1]) {
				t.Errorf("ops called = %v; want only %v", called, order[:i+1])
			}
		})
	}
}

// A ctx that ends mid-listing stops it: the stop is returned and the page read
// before it is stored.
func TestScanChatbot_ContextStopReturned(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := map[string]func(context.Context, any) (any, error){}
	for op, fn := range cbHandlers() {
		handlers[op] = func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out, err := fn(in)
			if op == "DescribeSlackChannelConfigurations" {
				cancel()
			}
			return out, err
		}
	}
	r := runChatbotCtx(ctx, t, st, handlers, &parentCalls{})
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", r.err)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeChatbotSlackChannelConfiguration)); !slices.Equal(got, []string{cbSlackARN}) {
		t.Errorf("slack channels = %v, want only the first page's %s", got, cbSlackARN)
	}
	if n := len(rowsByNativeID(t, st, TypeChatbotChimeWebhookConfiguration)); n != 0 {
		t.Errorf("chime rows = %d after ctx ended, want 0", n)
	}
}

// A transient error on a later page joined with the failed write of the
// pages read before it stays a store write failure, never a warning: the scan
// must not report success after losing those rows.
func TestScanChatbot_StoreWriteBesideTransientStaysHardError(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	h := cbHandlers()
	first := h["ListMicrosoftTeamsUserIdentities"]
	h["ListMicrosoftTeamsUserIdentities"] = func(in any) (any, error) {
		if in.(*chatbot.ListMicrosoftTeamsUserIdentitiesInput).NextToken == nil {
			return first(in)
		}
		// The last phase's last call: closing here fails only its own write.
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	}
	r := runChatbot(t, st, h)
	if !errors.Is(r.err, store.ErrStoreWrite) || classifyServiceError(r.err) != outcomeStoreWrite {
		t.Errorf("err = %v classified %v; want store.ErrStoreWrite, outcomeStoreWrite", r.err, classifyServiceError(r.err))
	}
	if got := r.opWarns("chatbot:ListMicrosoftTeamsUserIdentities"); len(got) != 0 {
		t.Errorf("warnings = %q; want none (the joined error is not downgraded to a transient warning)", got)
	}
}

// A phase's transient error is demoted to a warning under its op instead of
// buried in another phase's hard error.
func TestScanChatbot_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, o := range cbOps {
		hardOp := "ListCustomActions"
		if o.op == hardOp {
			hardOp = "DescribeChimeWebhookConfigurations"
		}
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers()
			h[o.op] = func(any) (any, error) { return nil, throttle }
			h[hardOp] = func(any) (any, error) { return nil, errCBTest }
			r := runChatbot(t, st, h)
			if !errors.Is(r.err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", r.err, hardOp)
			}
			if got := classifyServiceError(r.err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", r.err, got)
			}
			if got := r.opWarns(o.label); len(got) != 1 {
				t.Errorf("%s warnings = %q, want 1", o.label, got)
			}
		})
	}
}

const cbWebhookURL = "https://hooks.chime.aws/incomingwebhooks/abc?token=SECRETTOKEN123"

// The Chime webhook URL is a bearer credential. A response carrying it, in
// its own key and pasted into the description, goes through the real SDK
// deserializer, the scanner and the store; the URL must appear in no column.
func TestScanChatbotChimeWebhooks_WebhookURLNeverStored(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"WebhookConfigurations": []map[string]any{{
			"ChatConfigurationArn": cbChimeARN1,
			"ConfigurationName":    "c1",
			"IamRoleArn":           cbRoleARN,
			"SnsTopicArns":         []string{cbTopicARN},
			"WebhookDescription":   "room/hook " + cbWebhookURL,
			"WebhookUrl":           cbWebhookURL,
			"State":                "ENABLED",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	client := chatbot.NewFromConfig(sdkaws.Config{
		Region:           "us-east-2",
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
	}, func(o *chatbot.Options) { o.BaseEndpoint = sdkaws.String(srv.URL) })

	st := newTestStore(t)
	total, _, err := scanChatbotChimeWebhooks(context.Background(), client, newTestAccount(testAccountID), "us-east-2", st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d rows, err %v; want 1 row", total, err)
	}
	r, ok := rowsByNativeID(t, st, TypeChatbotChimeWebhookConfiguration)[cbChimeARN1]
	if !ok {
		t.Fatalf("chime row %s missing", cbChimeARN1)
	}
	for col, v := range map[string]string{"attributes": r.AttributesJSON, "name": sv(r.Name), "tags": sv(r.TagsJSON), "status": sv(r.Status)} {
		if strings.Contains(v, "SECRETTOKEN123") || strings.Contains(v, "hooks.chime.aws") {
			t.Errorf("%s column holds the webhook URL: %s", col, v)
		}
	}
	var attrs struct{ IamRoleArn, WebhookDescription string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.IamRoleArn != cbRoleARN || attrs.WebhookDescription != redact.Placeholder {
		t.Errorf("attrs = %+v (%v), want the role kept and the description redacted", attrs, err)
	}
}

// If a model update adds WebhookUrl to ChimeWebhookConfiguration, the
// descriptor rule keeps it out of the store.
func TestUpsertChimeWebhookConfiguration_WebhookURLRedacted(t *testing.T) {
	st := newTestStore(t)
	future := struct {
		chatbottypes.ChimeWebhookConfiguration
		WebhookURL *string `json:"WebhookUrl"`
	}{cbChime(cbChimeARN1, "c1"), ptrStr(cbWebhookURL)}
	if _, err := st.UpsertResources([]*store.Resource{{
		Provider: "aws", AccountID: testAccountID, Type: TypeChatbotChimeWebhookConfiguration, NativeID: cbChimeARN1,
		Region: regionGlobal, AttributesJSON: mustJSON(future), DiscoveredBy: testScanID,
	}}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	r := rowsByNativeID(t, st, TypeChatbotChimeWebhookConfiguration)[cbChimeARN1]
	if strings.Contains(r.AttributesJSON, "SECRETTOKEN123") {
		t.Errorf("stored attributes hold the webhook URL: %s", r.AttributesJSON)
	}
	var attrs struct {
		WebhookURL        string `json:"WebhookUrl"`
		ConfigurationName string
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.WebhookURL != redact.Placeholder || attrs.ConfigurationName != "c1" {
		t.Errorf("attrs = %+v (%v), want WebhookUrl redacted and ConfigurationName kept", attrs, err)
	}
}

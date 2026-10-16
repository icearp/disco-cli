package aws

import (
	"context"
	"encoding/json"
	"errors"
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
	cbSlackARN     = "arn:aws:chatbot::123456789012:chat-configuration/slack-channel/alpha"
	cbSlackGoneARN = "arn:aws:chatbot::123456789012:chat-configuration/slack-channel/gone"
	cbTeamsARN     = "arn:aws:chatbot::123456789012:chat-configuration/microsoft-teams-channel/beta"
	cbChimeARN1    = "arn:aws:chatbot::123456789012:chat-configuration/chime-webhook/c1"
	cbChimeARN2    = "arn:aws:chatbot::123456789012:chat-configuration/chime-webhook/c2"
	cbRoleARN      = "arn:aws:iam::123456789012:role/chatbot"
	cbTeamsUser1   = "0f0e0d0c-0b0a-0908-0706-050403020100"
	cbTeamsUser2   = "1f1e1d1c-1b1a-1918-1716-151413121110"
	cbTeamsTeamID  = "a0a1a2a3-a4a5-a6a7-a8a9-aaabacadaeaf"
)

// cbRecorder captures the MaxResults each new op was called with.
type cbRecorder struct {
	mu         sync.Mutex
	maxResults map[string][]int32
}

func (r *cbRecorder) record(op string, v *int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxResults == nil {
		r.maxResults = map[string][]int32{}
	}
	r.maxResults[op] = append(r.maxResults[op], sdkaws.ToInt32(v))
}

func cbChime(arn, name string) chatbottypes.ChimeWebhookConfiguration {
	return chatbottypes.ChimeWebhookConfiguration{
		ChatConfigurationArn: ptrStr(arn), ConfigurationName: ptrStr(name), IamRoleArn: ptrStr(cbRoleARN),
		SnsTopicArns: []string{"arn:aws:sns:us-east-2:123456789012:alerts"}, WebhookDescription: ptrStr("room/hook"),
		State: ptrStr("ENABLED"), Tags: []chatbottypes.Tag{{TagKey: ptrStr("env"), TagValue: ptrStr("prod")}},
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

// cbHandlers answers every chatbot op: one row for each older type, and two
// pages for each new op, the first holding an element without its key.
func cbHandlers(rec *cbRecorder) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListCustomActions": func(any) (any, error) {
			return &chatbot.ListCustomActionsOutput{CustomActions: []string{"arn:aws:chatbot::123456789012:custom-action/act"}}, nil
		},
		"DescribeSlackChannelConfigurations": func(any) (any, error) {
			return &chatbot.DescribeSlackChannelConfigurationsOutput{SlackChannelConfigurations: []chatbottypes.SlackChannelConfiguration{
				{ChatConfigurationArn: ptrStr(cbSlackARN), ConfigurationName: ptrStr("alpha")},
			}}, nil
		},
		"ListMicrosoftTeamsChannelConfigurations": func(any) (any, error) {
			return &chatbot.ListMicrosoftTeamsChannelConfigurationsOutput{TeamChannelConfigurations: []chatbottypes.TeamsChannelConfiguration{
				{ChatConfigurationArn: ptrStr(cbTeamsARN), ConfigurationName: ptrStr("beta")},
			}}, nil
		},
		"DescribeChimeWebhookConfigurations": func(in any) (any, error) {
			req := in.(*chatbot.DescribeChimeWebhookConfigurationsInput)
			rec.record("DescribeChimeWebhookConfigurations", req.MaxResults)
			if req.NextToken == nil {
				return &chatbot.DescribeChimeWebhookConfigurationsOutput{
					WebhookConfigurations: []chatbottypes.ChimeWebhookConfiguration{cbChime(cbChimeARN1, "c1"), {ConfigurationName: ptrStr("no-arn")}},
					NextToken:             ptrStr("p2"),
				}, nil
			}
			return &chatbot.DescribeChimeWebhookConfigurationsOutput{WebhookConfigurations: []chatbottypes.ChimeWebhookConfiguration{cbChime(cbChimeARN2, "c2")}}, nil
		},
		"DescribeSlackUserIdentities": func(in any) (any, error) {
			req := in.(*chatbot.DescribeSlackUserIdentitiesInput)
			rec.record("DescribeSlackUserIdentities", req.MaxResults)
			if req.NextToken == nil {
				return &chatbot.DescribeSlackUserIdentitiesOutput{
					SlackUserIdentities: []chatbottypes.SlackUserIdentity{cbSlackUser(cbSlackARN, "U1"), {ChatConfigurationArn: ptrStr(cbSlackARN), SlackTeamId: ptrStr("T01")}},
					NextToken:           ptrStr("p2"),
				}, nil
			}
			return &chatbot.DescribeSlackUserIdentitiesOutput{SlackUserIdentities: []chatbottypes.SlackUserIdentity{cbSlackUser(cbSlackGoneARN, "U2")}}, nil
		},
		"ListMicrosoftTeamsUserIdentities": func(in any) (any, error) {
			req := in.(*chatbot.ListMicrosoftTeamsUserIdentitiesInput)
			rec.record("ListMicrosoftTeamsUserIdentities", req.MaxResults)
			if req.NextToken == nil {
				return &chatbot.ListMicrosoftTeamsUserIdentitiesOutput{
					TeamsUserIdentities: []chatbottypes.TeamsUserIdentity{cbTeamsUser(cbTeamsUser1), {ChatConfigurationArn: ptrStr(cbTeamsARN)}},
					NextToken:           ptrStr("p2"),
				}, nil
			}
			return &chatbot.ListMicrosoftTeamsUserIdentitiesOutput{TeamsUserIdentities: []chatbottypes.TeamsUserIdentity{cbTeamsUser(cbTeamsUser2)}}, nil
		},
	}
}

func scanChatbotTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error), regions *parentCalls) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfig(handlers, cbTestDenied), regions)
	total, _, err := scanChatbot(context.Background(), acct, "", st, testScanID)
	return total, err
}

// cbNewRowCounts are the rows the new phases store from cbHandlers.
var cbNewRowCounts = map[string]int{
	TypeChatbotChimeWebhookConfiguration:  2,
	TypeChatbotSlackUserIdentity:          2,
	TypeChatbotMicrosoftTeamsUserIdentity: 2,
}

func TestScanChatbot_StoresChimeWebhooksAndUserIdentities(t *testing.T) {
	st := newTestStore(t)
	rec := &cbRecorder{}
	var regions parentCalls
	// U2's configuration is a stored row that this scan's listing did not
	// return, so U2 must not be linked under it.
	seedParentRows(t, st, TypeChatbotSlackChannelConfiguration, cbSlackGoneARN)
	// A pair naming an unstored parent is dropped with a warning; none may occur.
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	total, err := scanChatbotTopLevel(t, st, cbHandlers(rec), &regions)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// 3 older rows + 2 Chime + 2 Slack + 2 Teams
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-2"}) {
		t.Errorf("call regions = %v, want [us-east-2]", got)
	}
	for _, op := range []string{"DescribeChimeWebhookConfigurations", "DescribeSlackUserIdentities", "ListMicrosoftTeamsUserIdentities"} {
		if got := rec.maxResults[op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v, want [100 100]", op, got)
		}
	}

	chime := rowsByNativeID(t, st, TypeChatbotChimeWebhookConfiguration)
	if len(chime) != 2 {
		t.Errorf("chime rows = %d, want 2", len(chime))
	}
	c1, ok := chime[cbChimeARN1]
	if !ok {
		t.Fatalf("chime %s missing; have %v", cbChimeARN1, chime)
	}
	if c1.Type != TypeChatbotChimeWebhookConfiguration || sv(c1.Region) != "global" || sv(c1.Name) != "c1" || sv(c1.Status) != "ENABLED" {
		t.Errorf("chime row = type %q region %q name %q status %q", c1.Type, sv(c1.Region), sv(c1.Name), sv(c1.Status))
	}
	if sv(c1.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("chime tags = %s, want {\"env\":\"prod\"}", sv(c1.TagsJSON))
	}
	var chimeAttrs struct{ IamRoleArn string }
	if err := json.Unmarshal([]byte(c1.AttributesJSON), &chimeAttrs); err != nil || chimeAttrs.IamRoleArn != cbRoleARN {
		t.Errorf("chime attrs IamRoleArn = %q (%v), want %q", chimeAttrs.IamRoleArn, err, cbRoleARN)
	}
	if _, ok := chime[cbChimeARN2]; !ok {
		t.Errorf("page-2 chime %s missing", cbChimeARN2)
	}

	slack := rowsByNativeID(t, st, TypeChatbotSlackUserIdentity)
	if len(slack) != 2 {
		t.Errorf("slack identity rows = %d, want 2", len(slack))
	}
	u1Native := cbSlackARN + "/user-identity/T01/U1"
	u1, ok := slack[u1Native]
	if !ok {
		t.Fatalf("slack identity %s missing; have %v", u1Native, slack)
	}
	if u1.Type != TypeChatbotSlackUserIdentity || sv(u1.Region) != "global" || sv(u1.Name) != "U1" {
		t.Errorf("slack identity row = type %q region %q name %q", u1.Type, sv(u1.Region), sv(u1.Name))
	}
	assertContainsNative(t, st, cbSlackARN, u1Native)
	// U2's configuration was not listed this scan: stored, but under no parent.
	u2Native := cbSlackGoneARN + "/user-identity/T01/U2"
	if _, ok := slack[u2Native]; !ok {
		t.Errorf("slack identity %s missing", u2Native)
	}
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, u2Native), store.RelContains)
	if err != nil || len(rels) != 0 {
		t.Errorf("contains into %s = %v (%v), want none", u2Native, rels, err)
	}

	teams := rowsByNativeID(t, st, TypeChatbotMicrosoftTeamsUserIdentity)
	if len(teams) != 2 {
		t.Errorf("teams identity rows = %d, want 2", len(teams))
	}
	for _, user := range []string{cbTeamsUser1, cbTeamsUser2} {
		native := cbTeamsARN + "/user-identity/" + user
		r, ok := teams[native]
		if !ok {
			t.Errorf("teams identity %s missing", native)
			continue
		}
		if r.Type != TypeChatbotMicrosoftTeamsUserIdentity || sv(r.Region) != "global" || sv(r.Name) != user {
			t.Errorf("teams identity row = type %q region %q name %q", r.Type, sv(r.Region), sv(r.Name))
		}
		assertContainsNative(t, st, cbTeamsARN, native)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanChatbot_EmptyNewListings(t *testing.T) {
	st := newTestStore(t)
	h := cbHandlers(&cbRecorder{})
	h["DescribeChimeWebhookConfigurations"] = func(any) (any, error) { return &chatbot.DescribeChimeWebhookConfigurationsOutput{}, nil }
	h["DescribeSlackUserIdentities"] = func(any) (any, error) { return &chatbot.DescribeSlackUserIdentitiesOutput{}, nil }
	h["ListMicrosoftTeamsUserIdentities"] = func(any) (any, error) { return &chatbot.ListMicrosoftTeamsUserIdentitiesOutput{}, nil }
	total, err := scanChatbotTopLevel(t, st, h, &parentCalls{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (older rows only)", total)
	}
	for typ := range cbNewRowCounts {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// A denied new op warns once and costs no other phase its rows.
func TestScanChatbot_NewOpDenied(t *testing.T) {
	cases := []struct{ op, label, typ string }{
		{"DescribeChimeWebhookConfigurations", chatbotChimeWebhooksOp, TypeChatbotChimeWebhookConfiguration},
		{"DescribeSlackUserIdentities", chatbotSlackUserIdentitiesOp, TypeChatbotSlackUserIdentity},
		{"ListMicrosoftTeamsUserIdentities", chatbotTeamsUserIdentitiesOp, TypeChatbotMicrosoftTeamsUserIdentity},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers(&cbRecorder{})
			delete(h, c.op)
			warnings := countOpWarnings(st, c.label)
			total, err := scanChatbotTopLevel(t, st, h, &parentCalls{})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.label, *warnings)
			}
			if total != 7 {
				t.Errorf("total = %d, want 7 (every other phase's rows)", total)
			}
			for typ, want := range cbNewRowCounts {
				if typ == c.typ {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
		})
	}
}

// A denial on a later page warns and keeps the rows of the pages before it.
func TestScanChatbot_DeniedOnSecondPageKeepsFirst(t *testing.T) {
	cases := []struct {
		op, label, typ, keptNative string
	}{
		{"DescribeChimeWebhookConfigurations", chatbotChimeWebhooksOp, TypeChatbotChimeWebhookConfiguration, cbChimeARN1},
		{"DescribeSlackUserIdentities", chatbotSlackUserIdentitiesOp, TypeChatbotSlackUserIdentity, cbSlackARN + "/user-identity/T01/U1"},
		{"ListMicrosoftTeamsUserIdentities", chatbotTeamsUserIdentitiesOp, TypeChatbotMicrosoftTeamsUserIdentity, cbTeamsARN + "/user-identity/" + cbTeamsUser1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers(&cbRecorder{})
			first := h[c.op]
			calls := 0
			h[c.op] = func(in any) (any, error) {
				calls++
				if calls > 1 {
					return nil, cbTestDenied
				}
				return first(in)
			}
			warnings := countOpWarnings(st, c.label)
			if _, err := scanChatbotTopLevel(t, st, h, &parentCalls{}); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.label, *warnings)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.keptNative]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v, want only %s", c.typ, rows, c.keptNative)
			}
		})
	}
}

// Every phase runs whatever another returns: a hard error anywhere is
// returned, and the other phases' rows are stored.
func TestScanChatbot_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int
		wantRows  map[string]int
	}{
		// The older phases stop at the custom actions error; identities are
		// stored unlinked since no configuration was listed.
		{"ListCustomActions", 6, cbNewRowCounts},
		{"DescribeChimeWebhookConfigurations", 7, map[string]int{TypeChatbotChimeWebhookConfiguration: 0, TypeChatbotSlackUserIdentity: 2, TypeChatbotMicrosoftTeamsUserIdentity: 2}},
		{"DescribeSlackUserIdentities", 7, map[string]int{TypeChatbotChimeWebhookConfiguration: 2, TypeChatbotSlackUserIdentity: 0, TypeChatbotMicrosoftTeamsUserIdentity: 2}},
		{"ListMicrosoftTeamsUserIdentities", 7, map[string]int{TypeChatbotChimeWebhookConfiguration: 2, TypeChatbotSlackUserIdentity: 2, TypeChatbotMicrosoftTeamsUserIdentity: 0}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers(&cbRecorder{})
			h[c.op] = func(any) (any, error) { return nil, errCBTest }
			total, err := scanChatbotTopLevel(t, st, h, &parentCalls{})
			if !errors.Is(err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			for typ, want := range c.wantRows {
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
		})
	}
}

// A hard error after a page was read is returned, and the rows of the pages
// read before it are stored and linked.
func TestScanChatbot_IdentityErrorOnSecondPageKeepsFirst(t *testing.T) {
	cases := []struct {
		op, typ, parentARN, keptNative string
	}{
		{"DescribeSlackUserIdentities", TypeChatbotSlackUserIdentity, cbSlackARN, cbSlackARN + "/user-identity/T01/U1"},
		{"ListMicrosoftTeamsUserIdentities", TypeChatbotMicrosoftTeamsUserIdentity, cbTeamsARN, cbTeamsARN + "/user-identity/" + cbTeamsUser1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers(&cbRecorder{})
			first := h[c.op]
			calls := 0
			h[c.op] = func(in any) (any, error) {
				calls++
				if calls > 1 {
					return nil, errCBTest
				}
				return first(in)
			}
			total, err := scanChatbotTopLevel(t, st, h, &parentCalls{})
			if !errors.Is(err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			// 3 channel-phase rows + 2 + 2 from the untouched new phases + 1 kept
			if total != 8 {
				t.Errorf("total = %d, want 8", total)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.keptNative]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v, want only %s", c.typ, rows, c.keptNative)
			}
			assertContainsNative(t, st, c.parentARN, c.keptNative)
		})
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
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	h := cbHandlers(&cbRecorder{})
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
	_, err = scanChatbotTopLevel(t, st, h, &parentCalls{})
	if !errors.Is(err, store.ErrStoreWrite) || classifyServiceError(err) != outcomeStoreWrite {
		t.Errorf("err = %v classified %v; want store.ErrStoreWrite, outcomeStoreWrite", err, classifyServiceError(err))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v; want none (the joined error is not downgraded to a transient warning)", warnings)
	}
}

// A phase's transient error is demoted to a warning, so it cannot make the
// dispatcher read another phase's hard error as transient.
func TestScanChatbot_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"throttled older phase", "ListCustomActions", "DescribeChimeWebhookConfigurations", "chatbot"},
		{"throttled chime phase", "DescribeChimeWebhookConfigurations", "ListCustomActions", chatbotChimeWebhooksOp},
		{"throttled slack identity phase", "DescribeSlackUserIdentities", "ListCustomActions", chatbotSlackUserIdentitiesOp},
		{"throttled teams identity phase", "ListMicrosoftTeamsUserIdentities", "ListCustomActions", chatbotTeamsUserIdentitiesOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := cbHandlers(&cbRecorder{})
			h[c.transientOp] = func(any) (any, error) { return nil, throttle }
			h[c.hardOp] = func(any) (any, error) { return nil, errCBTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := scanChatbotTopLevel(t, st, h, &parentCalls{})
			if !errors.Is(err, errCBTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
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
			"SnsTopicArns":         []string{"arn:aws:sns:us-east-2:123456789012:alerts"},
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

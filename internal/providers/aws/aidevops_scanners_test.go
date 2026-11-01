package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	devopsagenttypes "github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

var (
	adoDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: aidevops:List"}
	adoNotFound  = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	adoThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	adoBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	adoCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func adoClient(handlers map[string]func(in any) (any, error)) *devopsagent.Client {
	return devopsagent.NewFromConfig(opStubConfig(handlers, adoDenied))
}

func adoSpaceARN(id string) string {
	return aidevopsAgentSpaceARN("aws", testRegion, testAccountID, id)
}

func adoAssocARN(id string) string {
	return aidevopsARN("aws", testRegion, testAccountID, "association", id)
}

// adoSeedSpaces stores the agent-space rows the children are linked under.
func adoSeedSpaces(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	spaces := make([]childParent, len(ids))
	for i, id := range ids {
		spaces[i] = childParent{id: id, arn: adoSpaceARN(id)}
		upsertTestResource(t, st, "aws", testAccountID, TypeAidevopsAgentSpace, spaces[i].arn, testRegion, "{}")
	}
	return spaces
}

func adoAsset(id string) devopsagenttypes.Asset {
	return devopsagenttypes.Asset{
		AssetId: ptrStr(id), AssetType: ptrStr("skill"), Version: sdkaws.Int32(2),
		Metadata:  document.NewLazyDocument(map[string]any{"title": "runbook " + id}),
		CreatedAt: &adoCreated, UpdatedAt: &adoCreated,
	}
}

func adoTrigger(spaceID, id string) devopsagenttypes.Trigger {
	return devopsagenttypes.Trigger{
		TriggerId: ptrStr(id), AgentSpaceId: ptrStr(spaceID), Type: ptrStr("SCHEDULE"), Status: ptrStr("ENABLED"),
		Condition: &devopsagenttypes.TriggerConditionMemberSchedule{Value: devopsagenttypes.ScheduleCondition{Expression: ptrStr("rate(1 day)")}},
		Action:    document.NewLazyDocument(map[string]any{"prompt": "check " + id}),
		CreatedAt: &adoCreated, UpdatedAt: &adoCreated,
	}
}

func adoWebhook(id string) devopsagenttypes.Webhook {
	return devopsagenttypes.Webhook{
		WebhookId: ptrStr(id), WebhookUrl: ptrStr("https://example.invalid/webhook/" + id), WebhookType: devopsagenttypes.WebhookTypeHmac,
	}
}

func TestScanAidevopsAssets_StoresEachSpacesAssets(t *testing.T) {
	st := newTestStore(t)
	spaces := adoSeedSpaces(t, st, "s-a", "s-b", "s-c")
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListAssets": func(in any) (any, error) {
			req := in.(*devopsagent.ListAssetsInput)
			calls.add(*req.AgentSpaceId)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			switch {
			case *req.AgentSpaceId == "s-c":
				return &devopsagent.ListAssetsOutput{}, nil
			case *req.AgentSpaceId == "s-b":
				return &devopsagent.ListAssetsOutput{Items: []devopsagenttypes.Asset{adoAsset("as-3"), {AssetType: ptrStr("skill")}}}, nil
			case req.NextToken == nil:
				return &devopsagent.ListAssetsOutput{Items: []devopsagenttypes.Asset{adoAsset("as-1")}, NextToken: ptrStr("p2")}, nil
			default:
				return &devopsagent.ListAssetsOutput{Items: []devopsagenttypes.Asset{adoAsset("as-2")}}, nil
			}
		},
	}
	total, _, err := scanAidevopsAssets(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, spaces)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"s-a", "s-b", "s-c"}; !slices.Equal(got, want) {
		t.Errorf("spaces queried %v, want %v", got, want)
	}
	for _, n := range maxSizes {
		if n != 100 {
			t.Errorf("MaxResults = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeAidevopsAsset)
	if len(rows) != 3 {
		t.Errorf("asset rows = %d, want 3", len(rows))
	}
	for _, c := range []struct{ space, id string }{{"s-a", "as-1"}, {"s-a", "as-2"}, {"s-b", "as-3"}} {
		native := adoSpaceARN(c.space) + "/asset/" + c.id
		if _, ok := rows[native]; !ok {
			t.Errorf("asset %s missing", native)
		}
		assertContainsNative(t, st, adoSpaceARN(c.space), native)
	}
	got := rows[adoSpaceARN("s-b")+"/asset/as-3"]
	if got.Type != TypeAidevopsAsset || sv(got.Region) != testRegion || sv(got.Name) != "as-3" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q created %q", got.Type, sv(got.Region), sv(got.Name), sv(got.CreatedAt))
	}
	var attrs struct {
		AssetType string
		Version   int
		Metadata  struct {
			Title string `json:"title"`
		}
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.AssetType != "skill" || attrs.Version != 2 || attrs.Metadata.Title != "runbook as-3" {
		t.Errorf("attrs = %+v (%v), want skill v2 with Metadata title", attrs, err)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanAidevopsTriggers_StoresEachSpacesTriggers(t *testing.T) {
	st := newTestStore(t)
	spaces := adoSeedSpaces(t, st, "s-a", "s-b", "s-gone")
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListTriggers": func(in any) (any, error) {
			req := in.(*devopsagent.ListTriggersInput)
			calls.add(*req.AgentSpaceId)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			switch {
			case *req.AgentSpaceId == "s-gone":
				return nil, adoNotFound
			case *req.AgentSpaceId == "s-b":
				return &devopsagent.ListTriggersOutput{Items: []devopsagenttypes.Trigger{adoTrigger("s-b", "tr-3"), {Status: ptrStr("ENABLED")}}}, nil
			case req.NextToken == nil:
				return &devopsagent.ListTriggersOutput{Items: []devopsagenttypes.Trigger{adoTrigger("s-a", "tr-1")}, NextToken: ptrStr("p2")}, nil
			default:
				return &devopsagent.ListTriggersOutput{Items: []devopsagenttypes.Trigger{adoTrigger("s-a", "tr-2")}}, nil
			}
		},
	}
	total, _, err := scanAidevopsTriggers(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, spaces)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"s-a", "s-b", "s-gone"}; !slices.Equal(got, want) {
		t.Errorf("spaces queried %v, want %v", got, want)
	}
	for _, n := range maxSizes {
		if n != 100 {
			t.Errorf("MaxResults = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeAidevopsTrigger)
	if len(rows) != 3 {
		t.Errorf("trigger rows = %d, want 3", len(rows))
	}
	for _, c := range []struct{ space, id string }{{"s-a", "tr-1"}, {"s-a", "tr-2"}, {"s-b", "tr-3"}} {
		native := adoSpaceARN(c.space) + "/trigger/" + c.id
		if _, ok := rows[native]; !ok {
			t.Errorf("trigger %s missing", native)
		}
		assertContainsNative(t, st, adoSpaceARN(c.space), native)
	}
	got := rows[adoSpaceARN("s-b")+"/trigger/tr-3"]
	if got.Type != TypeAidevopsTrigger || sv(got.Region) != testRegion || sv(got.Name) != "tr-3" ||
		sv(got.Status) != "ENABLED" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q status %q created %q", got.Type, sv(got.Region), sv(got.Name), sv(got.Status), sv(got.CreatedAt))
	}
	var attrs struct {
		Type      string
		Condition map[string]map[string]string
		Action    struct {
			Prompt string `json:"prompt"`
		}
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.Type != "SCHEDULE" || attrs.Action.Prompt != "check tr-3" {
		t.Errorf("attrs = %+v (%v), want SCHEDULE with Action prompt", attrs, err)
	}
	if want := map[string]map[string]string{"Schedule": {"Expression": "rate(1 day)"}}; !maps.EqualFunc(attrs.Condition, want, maps.Equal) {
		t.Errorf("attrs Condition = %v, want %v", attrs.Condition, want)
	}
	assertContainsEdgesDeclared(t, st)
}

var (
	adoPagerDuty = &devopsagenttypes.ServiceConfigurationMemberPagerduty{Value: devopsagenttypes.PagerDutyConfiguration{Services: []string{"P1"}}}
	adoGitLab    = &devopsagenttypes.ServiceConfigurationMemberGitlab{Value: devopsagenttypes.GitLabConfiguration{ProjectId: ptrStr("7")}}
	adoAWS       = &devopsagenttypes.ServiceConfigurationMemberAws{Value: devopsagenttypes.AWSConfiguration{AccountId: ptrStr(testAccountID)}}
	adoGitHub    = &devopsagenttypes.ServiceConfigurationMemberGithub{Value: devopsagenttypes.GitHubConfiguration{RepoName: ptrStr("repo")}}
)

// adoAssoc is an association in an agent space with its configuration kind.
type adoAssoc struct {
	id, space string
	config    devopsagenttypes.ServiceConfiguration
}

// adoSeedAssociations stores the association rows the webhooks are linked
// under.
func adoSeedAssociations(t *testing.T, st *store.Store, assocs ...adoAssoc) []aidevopsAssociation {
	t.Helper()
	var out []aidevopsAssociation
	for _, a := range assocs {
		upsertTestResource(t, st, "aws", testAccountID, TypeAidevopsAssociations, adoAssocARN(a.id), testRegion, "{}")
		out = append(out, aidevopsAssociation{spaceID: a.space, parent: childParent{id: a.id, arn: adoAssocARN(a.id)}, config: a.config})
	}
	return out
}

func TestScanAidevopsWebhooks_StoresEachAssociationsWebhooks(t *testing.T) {
	st := newTestStore(t)
	assocs := adoSeedAssociations(t, st,
		adoAssoc{"a-1", "s-a", adoPagerDuty}, adoAssoc{"a-2", "s-b", adoGitLab}, adoAssoc{"a-gone", "s-a", adoPagerDuty},
		adoAssoc{"a-aws", "s-a", adoAWS}, adoAssoc{"a-github", "s-b", adoGitHub}, adoAssoc{"a-none", "s-b", nil})
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){
		"ListWebhooks": func(in any) (any, error) {
			req := in.(*devopsagent.ListWebhooksInput)
			calls.add(*req.AgentSpaceId + "/" + *req.AssociationId)
			switch *req.AssociationId {
			case "a-gone":
				return nil, adoNotFound
			case "a-2":
				return &devopsagent.ListWebhooksOutput{Webhooks: []devopsagenttypes.Webhook{adoWebhook("wh-3"), {WebhookUrl: ptrStr("https://example.invalid/x")}}}, nil
			default:
				return &devopsagent.ListWebhooksOutput{Webhooks: []devopsagenttypes.Webhook{adoWebhook("wh-1"), adoWebhook("wh-2")}}, nil
			}
		},
	}
	total, _, err := scanAidevopsWebhooks(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, assocs)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"s-a/a-1", "s-a/a-gone", "s-b/a-2"}; !slices.Equal(got, want) {
		t.Errorf("space/association pairs queried %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeAidevopsWebhook)
	if len(rows) != 3 {
		t.Errorf("webhook rows = %d, want 3", len(rows))
	}
	for _, c := range []struct{ assoc, id string }{{"a-1", "wh-1"}, {"a-1", "wh-2"}, {"a-2", "wh-3"}} {
		native := adoAssocARN(c.assoc) + "/webhook/" + c.id
		if _, ok := rows[native]; !ok {
			t.Errorf("webhook %s missing", native)
		}
		assertContainsNative(t, st, adoAssocARN(c.assoc), native)
	}
	got := rows[adoAssocARN("a-2")+"/webhook/wh-3"]
	if got.Type != TypeAidevopsWebhook || sv(got.Region) != testRegion || sv(got.Name) != "wh-3" {
		t.Errorf("row = type %q region %q name %q", got.Type, sv(got.Region), sv(got.Name))
	}
	var attrs struct {
		WebhookURL  string `json:"WebhookUrl"`
		WebhookType string
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.WebhookType != "hmac" || attrs.WebhookURL != "https://example.invalid/webhook/wh-3" {
		t.Errorf("attrs = %+v (%v), want hmac with its URL", attrs, err)
	}
	assertContainsEdgesDeclared(t, st)
}

// adoNewPhases runs each new phase over two parents.
var adoNewPhases = []struct {
	op    string
	empty any
	run   func(context.Context, *devopsagent.Client, *store.Store) (int, int, error)
}{
	{"ListAssets", &devopsagent.ListAssetsOutput{}, func(ctx context.Context, c *devopsagent.Client, st *store.Store) (int, int, error) {
		return scanAidevopsAssets(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, []childParent{
			{id: "s-a", arn: adoSpaceARN("s-a")}, {id: "s-b", arn: adoSpaceARN("s-b")},
		})
	}},
	{"ListTriggers", &devopsagent.ListTriggersOutput{}, func(ctx context.Context, c *devopsagent.Client, st *store.Store) (int, int, error) {
		return scanAidevopsTriggers(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, []childParent{
			{id: "s-a", arn: adoSpaceARN("s-a")}, {id: "s-b", arn: adoSpaceARN("s-b")},
		})
	}},
	{"ListWebhooks", &devopsagent.ListWebhooksOutput{}, func(ctx context.Context, c *devopsagent.Client, st *store.Store) (int, int, error) {
		return scanAidevopsWebhooks(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, []aidevopsAssociation{
			{spaceID: "s-a", parent: childParent{id: "a-1", arn: adoAssocARN("a-1")}, config: adoPagerDuty},
			{spaceID: "s-b", parent: childParent{id: "a-2", arn: adoAssocARN("a-2")}, config: adoGitLab},
		})
	}},
	{"ListGoals", &devopsagent.ListGoalsOutput{}, func(ctx context.Context, c *devopsagent.Client, st *store.Store) (int, int, error) {
		return scanAidevopsGoals(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, []childParent{
			{id: "s-a", arn: adoSpaceARN("s-a")}, {id: "s-b", arn: adoSpaceARN("s-b")},
		})
	}},
}

func TestAidevopsHasWebhooks_OnlyWebhookCapableKinds(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  devopsagenttypes.ServiceConfiguration
		want bool
	}{
		{"eventChannel", &devopsagenttypes.ServiceConfigurationMemberEventChannel{}, true},
		{"pagerduty", adoPagerDuty, true},
		{"gitlab", adoGitLab, true},
		{"mcpserverdatadog", &devopsagenttypes.ServiceConfigurationMemberMcpserverdatadog{}, true},
		{"mcpservernewrelic", &devopsagenttypes.ServiceConfigurationMemberMcpservernewrelic{}, true},
		{"mcpserversplunk", &devopsagenttypes.ServiceConfigurationMemberMcpserversplunk{}, true},
		{"aws", adoAWS, false},
		{"github", adoGitHub, false},
		{"slack", &devopsagenttypes.ServiceConfigurationMemberSlack{}, false},
		{"mcpserver", &devopsagenttypes.ServiceConfigurationMemberMcpserver{}, false},
		{"unknown member", &devopsagenttypes.UnknownUnionMember{Tag: "future"}, false},
		{"absent", nil, false},
	} {
		if got := aidevopsHasWebhooks(c.cfg); got != c.want {
			t.Errorf("aidevopsHasWebhooks(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func adoGoal(id string, goalType devopsagenttypes.GoalType) devopsagenttypes.Goal {
	return devopsagenttypes.Goal{
		GoalId: ptrStr(id), AgentSpaceArn: ptrStr("arn:aws:aidevops:us-east-1:123456789012:agentspace/s"), Title: ptrStr("goal " + id),
		Content:  &devopsagenttypes.GoalContent{Description: ptrStr("d"), Objectives: ptrStr("keep p99 under 200ms")},
		GoalType: goalType, Status: devopsagenttypes.GoalStatusPaused, Version: sdkaws.Int32(1),
		EvaluationSchedule: &devopsagenttypes.GoalSchedule{State: devopsagenttypes.SchedulerStateDisabled, Expression: ptrStr("rate(7 days)")},
		CreatedAt:          &adoCreated, UpdatedAt: &adoCreated,
	}
}

func TestScanAidevopsGoals_StoresEachSpacesGoals(t *testing.T) {
	st := newTestStore(t)
	spaces := adoSeedSpaces(t, st, "s-a", "s-b", "s-c")
	calls := &parentCalls{}
	var (
		mu     sync.Mutex
		limits []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListGoals": func(in any) (any, error) {
			req := in.(*devopsagent.ListGoalsInput)
			calls.add(*req.AgentSpaceId)
			mu.Lock()
			limits = append(limits, *req.Limit)
			mu.Unlock()
			switch {
			case *req.AgentSpaceId == "s-c":
				return &devopsagent.ListGoalsOutput{}, nil
			case *req.AgentSpaceId == "s-b":
				return &devopsagent.ListGoalsOutput{Goals: []devopsagenttypes.Goal{
					adoGoal("g-3", devopsagenttypes.GoalTypeOncallReport), {Title: ptrStr("no id")},
				}}, nil
			case req.NextToken == nil:
				return &devopsagent.ListGoalsOutput{Goals: []devopsagenttypes.Goal{adoGoal("g-1", devopsagenttypes.GoalTypeCustomerDefined)}, NextToken: ptrStr("p2")}, nil
			default:
				return &devopsagent.ListGoalsOutput{Goals: []devopsagenttypes.Goal{adoGoal("g-2", devopsagenttypes.GoalTypeCustomerDefined)}}, nil
			}
		},
	}
	total, _, err := scanAidevopsGoals(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, spaces)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"s-a", "s-b", "s-c"}; !slices.Equal(got, want) {
		t.Errorf("spaces queried %v, want %v", got, want)
	}
	for _, n := range limits {
		if n != 100 {
			t.Errorf("Limit = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeAidevopsGoal)
	if len(rows) != 3 {
		t.Errorf("goal rows = %d, want 3", len(rows))
	}
	for _, c := range []struct{ space, id string }{{"s-a", "g-1"}, {"s-a", "g-2"}, {"s-b", "g-3"}} {
		native := adoSpaceARN(c.space) + "/goal/" + c.id
		if _, ok := rows[native]; !ok {
			t.Errorf("goal %s missing", native)
		}
		assertContainsNative(t, st, adoSpaceARN(c.space), native)
	}
	got := rows[adoSpaceARN("s-b")+"/goal/g-3"]
	if got.Type != TypeAidevopsGoal || sv(got.Region) != testRegion || sv(got.Name) != "goal g-3" ||
		sv(got.Status) != "PAUSED" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" || got.ManagedByProvider {
		t.Errorf("row = type %q region %q name %q status %q created %q managed %v",
			got.Type, sv(got.Region), sv(got.Name), sv(got.Status), sv(got.CreatedAt), got.ManagedByProvider)
	}
	var attrs struct {
		GoalType           string
		Content            struct{ Objectives string }
		EvaluationSchedule struct{ State, Expression string }
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.GoalType != "ONCALL_REPORT" ||
		attrs.Content.Objectives != "keep p99 under 200ms" || attrs.EvaluationSchedule.Expression != "rate(7 days)" {
		t.Errorf("attrs = %+v (%v), want ONCALL_REPORT with objectives and schedule", attrs, err)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestAidevopsChildPhases_Errors(t *testing.T) {
	for _, p := range adoNewPhases {
		t.Run(p.op+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return p.empty, nil }}
			total, _, err := p.run(context.Background(), adoClient(handlers), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(p.op+"/access denied warns once", func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "aidevops:"+p.op)
			total, _, err := p.run(context.Background(), adoClient(nil), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
			if *warned != 1 {
				t.Errorf("warnings = %d, want 1", *warned)
			}
		})
		t.Run(p.op+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return nil, adoBadInput }}
			_, _, err := p.run(context.Background(), adoClient(handlers), st)
			if !errors.Is(err, adoBadInput) {
				t.Errorf("err = %v, want the ValidationException", err)
			}
		})
	}
}

// ListAssets and ListGoals model no not-found error, so one is not taken for a deleted
// space and skipped.
func TestAidevopsUnmodeledNotFound_Propagates(t *testing.T) {
	for _, p := range adoNewPhases {
		if p.op != "ListAssets" && p.op != "ListGoals" {
			continue
		}
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return nil, adoNotFound }}
			_, _, err := p.run(context.Background(), adoClient(handlers), st)
			if !errors.Is(err, adoNotFound) {
				t.Errorf("err = %v, want the ResourceNotFoundException", err)
			}
		})
	}
}

// adoWrongWebhookQuery is what ListWebhooks answers for any association but
// the PagerDuty one in adoTopLevelHandlers.
var adoWrongWebhookQuery = &smithy.GenericAPIError{Code: "ValidationException", Message: "association has no webhooks"}

// adoTopLevelHandlers serves one agent space, service and private connection,
// a PagerDuty and an AWS association, and one child of each new phase, with
// no tags on any row, the ops in override answering instead. Every other op
// is denied.
func adoTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListAgentSpaces": func(any) (any, error) {
			return &devopsagent.ListAgentSpacesOutput{AgentSpaces: []devopsagenttypes.AgentSpace{{AgentSpaceId: ptrStr("s-a"), Name: ptrStr("space")}}}, nil
		},
		"ListServices": func(any) (any, error) {
			return &devopsagent.ListServicesOutput{Services: []devopsagenttypes.RegisteredService{{ServiceId: ptrStr("svc-1")}}}, nil
		},
		"ListAssociations": func(any) (any, error) {
			return &devopsagent.ListAssociationsOutput{Associations: []devopsagenttypes.Association{
				{AssociationId: ptrStr("a-1"), AgentSpaceId: ptrStr("s-a"), Configuration: adoPagerDuty},
				{AssociationId: ptrStr("a-aws"), AgentSpaceId: ptrStr("s-a"), Configuration: adoAWS},
			}}, nil
		},
		"ListPrivateConnections": func(any) (any, error) {
			return &devopsagent.ListPrivateConnectionsOutput{PrivateConnections: []devopsagenttypes.PrivateConnectionSummary{{Name: ptrStr("pc-1")}}}, nil
		},
		"ListAssets": func(any) (any, error) {
			return &devopsagent.ListAssetsOutput{Items: []devopsagenttypes.Asset{adoAsset("as-1")}}, nil
		},
		"ListTriggers": func(any) (any, error) {
			return &devopsagent.ListTriggersOutput{Items: []devopsagenttypes.Trigger{adoTrigger("s-a", "tr-1")}}, nil
		},
		"ListWebhooks": func(in any) (any, error) {
			if req := in.(*devopsagent.ListWebhooksInput); *req.AgentSpaceId != "s-a" || *req.AssociationId != "a-1" {
				return nil, adoWrongWebhookQuery
			}
			return &devopsagent.ListWebhooksOutput{Webhooks: []devopsagenttypes.Webhook{adoWebhook("wh-1")}}, nil
		},
		"ListGoals": func(any) (any, error) {
			return &devopsagent.ListGoalsOutput{Goals: []devopsagenttypes.Goal{adoGoal("g-1", devopsagenttypes.GoalTypeCustomerDefined)}}, nil
		},
		"ListTagsForResource": func(any) (any, error) { return &devopsagent.ListTagsForResourceOutput{}, nil },
	}
	maps.Copy(handlers, override)
	return handlers
}

// The new phases run over the parents the earlier phases listed, and an
// earlier phase's error neither stops them nor is lost.
func TestScanAidevopsAll_RunsNewPhasesOverListedParents(t *testing.T) {
	st := newTestStore(t)
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListServices": func(any) (any, error) { return nil, adoBadInput },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, adoBadInput) || errors.Is(err, adoWrongWebhookQuery) {
		t.Errorf("err = %v, want only the ListServices error", err)
	}
	// 1 agent space + 2 associations + 1 private connection + 1 asset + 1 trigger + 1 goal + 1 webhook.
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}
	assertContainsNative(t, st, adoSpaceARN("s-a"), adoSpaceARN("s-a")+"/asset/as-1")
	assertContainsNative(t, st, adoSpaceARN("s-a"), adoSpaceARN("s-a")+"/trigger/tr-1")
	assertContainsNative(t, st, adoSpaceARN("s-a"), adoSpaceARN("s-a")+"/goal/g-1")
	assertContainsNative(t, st, adoAssocARN("a-1"), adoAssocARN("a-1")+"/webhook/wh-1")
	assertContainsEdgesDeclared(t, st)
}

// Every new phase runs after the existing phases: their rows are stored and
// counted when every new phase fails, and each new phase's error is returned.
func TestScanAidevopsAll_NewPhaseErrorsKeepEarlierRows(t *testing.T) {
	st := newTestStore(t)
	override := map[string]func(any) (any, error){}
	var phaseErrs []error
	for _, p := range adoNewPhases {
		perr := &smithy.GenericAPIError{Code: "ValidationException", Message: p.op}
		phaseErrs = append(phaseErrs, perr)
		override[p.op] = func(any) (any, error) { return nil, perr }
	}
	total, _, err := scanAidevopsAll(context.Background(), adoClient(adoTopLevelHandlers(override)), newTestAccount(testAccountID), testRegion, st, testScanID)
	for i, perr := range phaseErrs {
		if !errors.Is(err, perr) {
			t.Errorf("err = %v, want it to carry the %s error", err, adoNewPhases[i].op)
		}
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (agent space + service + 2 associations + private connection)", total)
	}
	for typ, want := range map[string]int{
		TypeAidevopsAgentSpace: 1, TypeAidevopsService: 1, TypeAidevopsAssociations: 2, TypeAidevopsPrivateConnection: 1,
	} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
}

// Beside another phase's hard error, a later phase's transient error (assets,
// triggers and goals are on the data-plane host) is reported as a warning
// under its phase instead of inside the hard error, and the phases after it
// still run.
func TestScanAidevopsAll_TransientPhaseDoesNotMaskOthers(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "aidevops:ListAssets")
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAssets":   func(any) (any, error) { return nil, adoThrottled },
		"ListServices": func(any) (any, error) { return nil, adoBadInput },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !errors.Is(err, adoBadInput) {
		t.Errorf("err = %v, want the ListServices error", err)
	}
	if *warned != 1 {
		t.Errorf("ListAssets transient warnings = %d, want 1", *warned)
	}
	// 1 agent space + 2 associations + 1 private connection + 1 trigger + 1 goal + 1 webhook.
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
}

// In a region with no aidevops endpoint the first phase's NXDOMAIN is
// returned alone, so the dispatcher reports the region unavailable.
func TestScanAidevopsAll_NXDOMAINStopsTheScan(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){}
	for op := range adoTopLevelHandlers(nil) {
		handlers[op] = func(any) (any, error) {
			calls.add(op)
			return nil, &net.DNSError{Err: "no such host", Name: "cp.aidevops.us-east-1.api.aws", IsNotFound: true}
		}
	}
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if got, want := calls.distinct(), []string{"ListAgentSpaces"}; !slices.Equal(got, want) {
		t.Errorf("ops called %v, want %v", got, want)
	}
	if total != 0 || len(warnings) != 0 {
		t.Errorf("total, warnings = %d, %v; want 0, none", total, warnings)
	}
}

// A first-phase error other than an NXDOMAIN is returned with the other
// phases' errors, and the phases that do not need agent spaces still store
// their rows.
func TestScanAidevopsAll_AgentSpaceErrorKeepsIndependentPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAgentSpaces": func(any) (any, error) { return nil, adoBadInput },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, adoBadInput) {
		t.Errorf("err = %v, want the ListAgentSpaces error", err)
	}
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (service + private connection)", total)
	}
	for typ, want := range map[string]int{TypeAidevopsService: 1, TypeAidevopsPrivateConnection: 1, TypeAidevopsAgentSpace: 0, TypeAidevopsAssociations: 0} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
}

// A refused connection on the first listing does not stop the scan: only an
// NXDOMAIN says the region does not serve the service, so the phases that do
// not need agent spaces still run and store their rows.
func TestScanAidevopsAll_AgentSpaceConnectionErrorKeepsIndependentPhases(t *testing.T) {
	st := newTestStore(t)
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	calls := &parentCalls{}
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAgentSpaces": func(any) (any, error) { return nil, refused },
	})
	for _, op := range []string{"ListServices", "ListPrivateConnections"} {
		list := handlers[op]
		handlers[op] = func(in any) (any, error) {
			calls.add(op)
			return list(in)
		}
	}
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, refused) {
		t.Errorf("err = %v; want the refused dial", err)
	}
	if got, want := calls.distinct(), []string{"ListPrivateConnections", "ListServices"}; !slices.Equal(got, want) {
		t.Errorf("ops called %v; want %v", got, want)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (service + private connection)", total)
	}
	for typ, want := range map[string]int{TypeAidevopsService: 1, TypeAidevopsPrivateConnection: 1, TypeAidevopsAgentSpace: 0} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d; want %d", typ, n, want)
		}
	}
}

// A data-plane phase's transient error alone is returned, so the scan is
// marked partial rather than complete, with the other phases' rows counted.
func TestScanAidevopsAll_LoneTransientPhaseMarksPartial(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "aidevops:ListAssets")
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAssets": func(any) (any, error) { return nil, adoThrottled },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	if *warned != 0 {
		t.Errorf("ListAssets transient warnings = %d, want 0", *warned)
	}
	if stored := countStoredRows(t, st); total == 0 || total != stored {
		t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
	}
}

// A data-plane host that does not resolve after the control plane answered is
// a plain error, so the dispatcher does not report the region unavailable and
// drop the control-plane rows already stored.
func TestScanAidevopsAll_DataPlaneNXDOMAINKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAssets": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "dp.aidevops.us-east-1.api.aws", IsNotFound: true}
		},
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if stored := countStoredRows(t, st); total == 0 || total != stored {
		t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
	}
}

const (
	// adoTestTotal is every row of adoTagFixture: two agent spaces, two
	// services, two private connections and, for each agent space, one
	// association, asset, trigger and goal, and one webhook under its
	// association.
	adoTestTotal = 2 + 2 + 2 + 2*5
	// adoTestSpaceChildren is every row listed under one agent space.
	adoTestSpaceChildren = 5
)

var adoTestTagged = []string{TypeAidevopsAgentSpace, TypeAidevopsService, TypeAidevopsPrivateConnection}

// Each AdditionalServiceDetails member is written under its own member name,
// one newer than the pinned SDK under UnknownMember, and none as an empty
// object.
func TestAidevopsServiceAttrs_LabelsEveryDetailsMember(t *testing.T) {
	for _, c := range []struct {
		member devopsagenttypes.AdditionalServiceDetails
		key    string
	}{
		{&devopsagenttypes.AdditionalServiceDetailsMemberAzuredevops{}, "Azuredevops"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberAzureidentity{}, "Azureidentity"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberGithub{}, "Github"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberGitlab{}, "Gitlab"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpserver{}, "Mcpserver"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpserverdatadog{}, "Mcpserverdatadog"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpservergrafana{}, "Mcpservergrafana"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpservernewrelic{}, "Mcpservernewrelic"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpserversigv4{}, "Mcpserversigv4"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberMcpserversplunk{}, "Mcpserversplunk"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberPagerduty{}, "Pagerduty"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberRemoteagent{}, "Remoteagent"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberRemoteagentsigv4{}, "Remoteagentsigv4"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberServicenow{}, "Servicenow"},
		{&devopsagenttypes.AdditionalServiceDetailsMemberSlack{}, "Slack"},
		{&devopsagenttypes.UnknownUnionMember{Tag: "future"}, "UnknownMember"},
		{nil, ""},
	} {
		t.Run(c.key, func(t *testing.T) {
			var attrs struct{ AdditionalServiceDetails map[string]json.RawMessage }
			raw := mustJSON(newAidevopsServiceAttrs(devopsagenttypes.RegisteredService{AdditionalServiceDetails: c.member}))
			if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
				t.Fatalf("attrs %s: %v", raw, err)
			}
			var want []string
			if c.key != "" {
				want = []string{c.key}
			}
			if got := slices.Sorted(maps.Keys(attrs.AdditionalServiceDetails)); !slices.Equal(got, want) {
				t.Errorf("AdditionalServiceDetails keys = %v; want %v", got, want)
			}
		})
	}
}

// AccessibleResources keeps the SDK shape's null for none and [] for an empty
// list, and a document that does not render fails the row's attributes (which
// the store reports) instead of being written as {}.
func TestAidevopsServiceAttrs_AccessibleResources(t *testing.T) {
	for _, c := range []struct {
		name string
		docs []document.Interface
		want string
	}{
		{"none", nil, "null"},
		{"empty", []document.Interface{}, "[]"},
		{"listed", []document.Interface{document.NewLazyDocument(map[string]any{"repo": "org/x"})}, `[{"repo":"org/x"}]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var attrs struct{ AccessibleResources json.RawMessage }
			raw := mustJSON(newAidevopsServiceAttrs(devopsagenttypes.RegisteredService{AccessibleResources: c.docs}))
			if err := json.Unmarshal([]byte(raw), &attrs); err != nil || string(attrs.AccessibleResources) != c.want {
				t.Errorf("AccessibleResources = %s (%v); want %s", attrs.AccessibleResources, err, c.want)
			}
		})
	}
	t.Run("unrenderable", func(t *testing.T) {
		raw := mustJSON(newAidevopsServiceAttrs(devopsagenttypes.RegisteredService{
			AccessibleResources: []document.Interface{document.NewLazyDocument(complex(1, 2))},
		}))
		if !util.IsMarshalFailure(raw) {
			t.Errorf("attrs = %s; want the marshal-failure object", raw)
		}
	})
}

// adoTagFixture answers every op an aidevops scan calls, in partition and
// region: agent spaces s-tagged on page one and s-bare on page two, services
// svc-tagged and svc-bare likewise, and private connections pc-tagged and
// pc-bare in one call; each agent space has one PagerDuty association
// (a-<space>), asset, trigger and goal, and each association one webhook.
// Every "tagged" row of a tag-read type is tagged with its type. Every call
// is recorded; the tag reads answer errs by ARN first.
type adoTagFixture struct {
	partition, region string

	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

func newADOTagFixture(partition, region string) *adoTagFixture {
	f := &adoTagFixture{
		partition: partition, region: region,
		maxResults: map[string][]int32{}, parents: map[string][]string{},
		tags: map[string]map[string]string{}, errs: map[string]error{},
	}
	f.tags[f.spaceARN("s-tagged")] = map[string]string{"type": TypeAidevopsAgentSpace}
	f.tags[f.serviceARN("svc-tagged")] = map[string]string{"type": TypeAidevopsService}
	f.tags[f.pcARN("pc-tagged")] = map[string]string{"type": TypeAidevopsPrivateConnection}
	return f
}

// arn is an aidevops ARN in the fixture's partition, region and account.
func (f *adoTagFixture) arn(resource string) string {
	return "arn:" + f.partition + ":aidevops:" + f.region + ":" + testAccountID + ":" + resource
}

func (f *adoTagFixture) spaceARN(id string) string    { return f.arn("agentspace/" + id) }
func (f *adoTagFixture) serviceARN(id string) string  { return f.arn("service/" + id) }
func (f *adoTagFixture) pcARN(name string) string     { return f.arn("private-connection/" + name) }
func (f *adoTagFixture) assocARN(space string) string { return f.arn("association/a-" + space) }
func (f *adoTagFixture) assetARN(space string) string {
	return f.spaceARN(space) + "/asset/as-" + space
}

func (f *adoTagFixture) webhookARN(space string) string {
	return f.assocARN(space) + "/webhook/wh-" + space
}

// rows is the NativeID of every row of type typ the fixture lists, sorted.
func (f *adoTagFixture) rows(typ string) []string {
	var arns []string
	spaces := []string{"s-tagged", "s-bare"}
	for _, s := range spaces {
		switch typ {
		case TypeAidevopsAgentSpace:
			arns = append(arns, f.spaceARN(s))
		case TypeAidevopsAssociations:
			arns = append(arns, f.assocARN(s))
		case TypeAidevopsAsset:
			arns = append(arns, f.assetARN(s))
		case TypeAidevopsTrigger:
			arns = append(arns, f.spaceARN(s)+"/trigger/tr-"+s)
		case TypeAidevopsGoal:
			arns = append(arns, f.spaceARN(s)+"/goal/g-"+s)
		case TypeAidevopsWebhook:
			arns = append(arns, f.webhookARN(s))
		}
	}
	switch typ {
	case TypeAidevopsService:
		arns = []string{f.serviceARN("svc-tagged"), f.serviceARN("svc-bare")}
	case TypeAidevopsPrivateConnection:
		arns = []string{f.pcARN("pc-tagged"), f.pcARN("pc-bare")}
	}
	slices.Sort(arns)
	return arns
}

// record notes op's MaxResults and, on a first page, the parent it lists.
func (f *adoTagFixture) record(op string, maxResults *int32, token *string, parent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxResults[op] = append(f.maxResults[op], sdkaws.ToInt32(maxResults))
	if token == nil {
		f.parents[op] = append(f.parents[op], parent)
	}
}

func (f *adoTagFixture) calls(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.maxResults[op])
}

func (f *adoTagFixture) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

func (f *adoTagFixture) listTags(in any) (any, error) {
	arn := sv(in.(*devopsagent.ListTagsForResourceInput).ResourceArn)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagARNs = append(f.tagARNs, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	return &devopsagent.ListTagsForResourceOutput{Tags: f.tags[arn]}, nil
}

func (f *adoTagFixture) handlers() map[string]func(in any) (any, error) {
	space := func(id string) []devopsagenttypes.AgentSpace {
		return []devopsagenttypes.AgentSpace{{
			AgentSpaceId: sdkaws.String(id), Name: sdkaws.String("name-" + id), Description: sdkaws.String("desc " + id),
			Locale: sdkaws.String("en-US"), CreatedAt: &adoCreated, UpdatedAt: &adoCreated,
		}}
	}
	service := func(id string) []devopsagenttypes.RegisteredService {
		return []devopsagenttypes.RegisteredService{{
			ServiceId: sdkaws.String(id), Name: sdkaws.String("name-" + id), ServiceType: devopsagenttypes.ServiceMcpServerSigv4,
			PrivateConnectionName: sdkaws.String("pc-tagged"),
			AccessibleResources:   []document.Interface{document.NewLazyDocument(map[string]any{"repo": "org/x"})},
			AdditionalServiceDetails: &devopsagenttypes.AdditionalServiceDetailsMemberMcpserversigv4{Value: devopsagenttypes.RegisteredMCPServerSigV4Details{
				Name: sdkaws.String("mcp"), Endpoint: sdkaws.String("https://mcp.example.com"), Region: sdkaws.String("us-east-1"), Service: sdkaws.String("execute-api"),
				CustomHeaders: map[string]string{"x-api-key": "k3y-s3cret", "x-tenant": "tenant-a"},
			}},
		}}
	}
	pc := func(name string) devopsagenttypes.PrivateConnectionSummary {
		return devopsagenttypes.PrivateConnectionSummary{
			Name: sdkaws.String(name), Status: devopsagenttypes.PrivateConnectionStatusActive,
			Type: devopsagenttypes.PrivateConnectionTypeSelfManaged, HostAddress: sdkaws.String(name + ".example.internal"), VpcId: sdkaws.String("vpc-1"),
		}
	}
	return map[string]func(in any) (any, error){
		"ListTagsForResource": f.listTags,
		"ListAgentSpaces": func(in any) (any, error) {
			p := in.(*devopsagent.ListAgentSpacesInput)
			f.record("ListAgentSpaces", p.MaxResults, p.NextToken, "")
			var out devopsagent.ListAgentSpacesOutput
			out.AgentSpaces, out.NextToken = scnTwoPages(p.NextToken, space("s-tagged"), space("s-bare"))
			return &out, nil
		},
		"ListServices": func(in any) (any, error) {
			p := in.(*devopsagent.ListServicesInput)
			f.record("ListServices", p.MaxResults, p.NextToken, "")
			var out devopsagent.ListServicesOutput
			out.Services, out.NextToken = scnTwoPages(p.NextToken, service("svc-tagged"), service("svc-bare"))
			return &out, nil
		},
		"ListPrivateConnections": func(any) (any, error) {
			f.record("ListPrivateConnections", nil, nil, "")
			return &devopsagent.ListPrivateConnectionsOutput{PrivateConnections: []devopsagenttypes.PrivateConnectionSummary{pc("pc-tagged"), pc("pc-bare")}}, nil
		},
		"ListAssociations": func(in any) (any, error) {
			p := in.(*devopsagent.ListAssociationsInput)
			s := sv(p.AgentSpaceId)
			f.record("ListAssociations", p.MaxResults, p.NextToken, s)
			return &devopsagent.ListAssociationsOutput{Associations: []devopsagenttypes.Association{{
				AssociationId: sdkaws.String("a-" + s), AgentSpaceId: p.AgentSpaceId, ServiceId: sdkaws.String("svc-tagged"),
				Configuration: adoPagerDuty, CreatedAt: &adoCreated,
			}}}, nil
		},
		"ListAssets": func(in any) (any, error) {
			p := in.(*devopsagent.ListAssetsInput)
			f.record("ListAssets", p.MaxResults, p.NextToken, sv(p.AgentSpaceId))
			return &devopsagent.ListAssetsOutput{Items: []devopsagenttypes.Asset{adoAsset("as-" + sv(p.AgentSpaceId))}}, nil
		},
		"ListTriggers": func(in any) (any, error) {
			p := in.(*devopsagent.ListTriggersInput)
			f.record("ListTriggers", p.MaxResults, p.NextToken, sv(p.AgentSpaceId))
			return &devopsagent.ListTriggersOutput{Items: []devopsagenttypes.Trigger{adoTrigger(sv(p.AgentSpaceId), "tr-"+sv(p.AgentSpaceId))}}, nil
		},
		"ListGoals": func(in any) (any, error) {
			p := in.(*devopsagent.ListGoalsInput)
			f.record("ListGoals", p.Limit, p.NextToken, sv(p.AgentSpaceId))
			return &devopsagent.ListGoalsOutput{Goals: []devopsagenttypes.Goal{adoGoal("g-"+sv(p.AgentSpaceId), devopsagenttypes.GoalTypeCustomerDefined)}}, nil
		},
		"ListWebhooks": func(in any) (any, error) {
			p := in.(*devopsagent.ListWebhooksInput)
			f.record("ListWebhooks", nil, nil, sv(p.AssociationId))
			return &devopsagent.ListWebhooksOutput{Webhooks: []devopsagenttypes.Webhook{adoWebhook("wh-" + sv(p.AgentSpaceId))}}, nil
		},
	}
}

// adoScan runs the scan's real entry point against handlers in the test
// account and region; an op without a handler is denied.
func adoScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, adoDenied)
	return scanAidevops(ctx, acct, testRegion, st, testScanID)
}

// Agent spaces, services and private connections are stored under their
// ARNs, built in the Service Reference's format, each with the tags its tag
// read returns (nil for none) and its listed element as attributes. Every
// child is stored under the agent space's ARN without a tag read. The two
// paginated listings are sent MaxResults 100. No error, no warning.
func TestScanAidevops_StoresTagsOfTaggableTypes(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newADOTagFixture("aws", testRegion)

	total, inserted, err := adoScan(context.Background(), st, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != adoTestTotal || inserted != adoTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, adoTestTotal, adoTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantTagARNs []string
	for _, typ := range []string{
		TypeAidevopsAgentSpace, TypeAidevopsService, TypeAidevopsPrivateConnection,
		TypeAidevopsAssociations, TypeAidevopsAsset, TypeAidevopsTrigger, TypeAidevopsGoal, TypeAidevopsWebhook,
	} {
		rows := rowsByNativeID(t, st, typ)
		if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, f.rows(typ)) {
			t.Errorf("%s rows = %v\nwant %v", typ, got, f.rows(typ))
		}
		for _, arn := range f.rows(typ) {
			if want := f.tags[arn]; want != nil {
				assertTagsMap(t, rows[arn].TagsJSON, want)
			} else if got := rows[arn].TagsJSON; got != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *got)
			}
		}
		if slices.Contains(adoTestTagged, typ) {
			wantTagARNs = append(wantTagARNs, f.rows(typ)...)
		}
	}
	slices.Sort(wantTagARNs)
	if got := f.sortedTagARNs(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}
	assertContainsNative(t, st, f.spaceARN("s-bare"), f.assetARN("s-bare"))

	space := rowsByNativeID(t, st, TypeAidevopsAgentSpace)[f.spaceARN("s-bare")]
	var spaceAttrs devopsagenttypes.AgentSpace
	if err := json.Unmarshal([]byte(space.AttributesJSON), &spaceAttrs); err != nil || sv(spaceAttrs.AgentSpaceId) != "s-bare" || sv(spaceAttrs.Description) != "desc s-bare" || sv(spaceAttrs.Locale) != "en-US" {
		t.Errorf("agent space attrs = %s (%v); want the listed element", space.AttributesJSON, err)
	}
	if sv(space.Name) != "name-s-bare" || sv(space.CreatedAt) != "2026-09-01T08:00:00Z" || space.Status != nil || sv(space.Region) != testRegion {
		t.Errorf("agent space Name, CreatedAt, Status, Region = %q, %q, %v, %q", sv(space.Name), sv(space.CreatedAt), space.Status, sv(space.Region))
	}
	// The documents and the union member are written out by name, and the
	// custom header values are redacted.
	svc := rowsByNativeID(t, st, TypeAidevopsService)[f.serviceARN("svc-bare")]
	wantSvcAttrs := `{"AccessibleResources":[{"repo":"org/x"}],"AdditionalServiceDetails":{"Mcpserversigv4":{` +
		`"CustomHeaders":{"x-api-key":"` + redact.PlaceholderKeyName + `","x-tenant":"` + redact.PlaceholderRule + `"},` +
		`"Description":null,"Endpoint":"https://mcp.example.com","McpRoleArn":null,"Name":"mcp","Region":"us-east-1","RoleArn":null,"Service":"execute-api"}},` +
		`"KmsKeyArn":null,"Name":"name-svc-bare","PrivateConnectionName":"pc-tagged","ServiceId":"svc-bare","ServiceType":"mcpserversigv4"}`
	if svc.AttributesJSON != wantSvcAttrs {
		t.Errorf("service attrs = %s\nwant %s", svc.AttributesJSON, wantSvcAttrs)
	}
	if sv(svc.Name) != "name-svc-bare" || svc.CreatedAt != nil || svc.Status != nil || sv(svc.Region) != testRegion {
		t.Errorf("service Name, CreatedAt, Status, Region = %q, %v, %v, %q", sv(svc.Name), svc.CreatedAt, svc.Status, sv(svc.Region))
	}
	pc := rowsByNativeID(t, st, TypeAidevopsPrivateConnection)[f.pcARN("pc-bare")]
	var pcAttrs devopsagenttypes.PrivateConnectionSummary
	if err := json.Unmarshal([]byte(pc.AttributesJSON), &pcAttrs); err != nil || sv(pcAttrs.Name) != "pc-bare" || pcAttrs.Status != devopsagenttypes.PrivateConnectionStatusActive ||
		pcAttrs.Type != devopsagenttypes.PrivateConnectionTypeSelfManaged || sv(pcAttrs.HostAddress) != "pc-bare.example.internal" || sv(pcAttrs.VpcId) != "vpc-1" {
		t.Errorf("private connection attrs = %s (%v); want the listed element", pc.AttributesJSON, err)
	}
	if sv(pc.Name) != "pc-bare" || pc.CreatedAt != nil || sv(pc.Status) != "ACTIVE" || sv(pc.Region) != testRegion {
		t.Errorf("private connection Name, CreatedAt, Status, Region = %q, %v, %q, %q", sv(pc.Name), pc.CreatedAt, sv(pc.Status), sv(pc.Region))
	}

	for _, op := range []string{"ListAgentSpaces", "ListServices"} {
		if got := f.maxResults[op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v; want [100 100]", op, got)
		}
	}
	if got := slices.Sorted(slices.Values(f.parents["ListAssociations"])); !slices.Equal(got, []string{"s-bare", "s-tagged"}) {
		t.Errorf("ListAssociations parents = %v; want both agent spaces", got)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAidevops_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := adoScan(context.Background(), st, newADOTagFixture("aws", testRegion).handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := adoScan(context.Background(), st, newADOTagFixture("aws", testRegion).handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != adoTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, adoTestTotal)
	}
}

// Empty listings: nothing stored, no child listing or tag read, no error, no
// warning.
func TestScanAidevops_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newADOTagFixture("aws", testRegion)
	h := f.handlers()
	h["ListAgentSpaces"] = func(any) (any, error) { return &devopsagent.ListAgentSpacesOutput{}, nil }
	h["ListServices"] = func(any) (any, error) { return &devopsagent.ListServicesOutput{}, nil }
	h["ListPrivateConnections"] = func(any) (any, error) { return &devopsagent.ListPrivateConnectionsOutput{}, nil }

	total, inserted, err := adoScan(context.Background(), st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListAssociations", "ListAssets", "ListTriggers", "ListGoals", "ListWebhooks"} {
		if n := f.calls(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := f.sortedTagARNs(); len(got) != 0 {
		t.Errorf("tag reads = %v; want none", got)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (an agent space with its
// children, which are then not listed). A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanAidevops_TagReadFailures(t *testing.T) {
	f0 := newADOTagFixture("aws", testRegion)
	space, svc, pc := f0.spaceARN("s-tagged"), f0.serviceARN("svc-bare"), f0.pcARN("pc-tagged")
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"agent space/not found", space, TypeAidevopsAgentSpace, adoNotFound, -1, "", adoTestTotal - 1 - adoTestSpaceChildren, false, false},
		{"agent space/denied", space, TypeAidevopsAgentSpace, adoDenied, -1, "", adoTestTotal, true, true},
		{"agent space/other error", space, TypeAidevopsAgentSpace, adoBadInput, outcomeError, "ValidationException", adoTestTotal, true, false},
		{"service/not found", svc, TypeAidevopsService, adoNotFound, -1, "", adoTestTotal - 1, false, false},
		{"service/transient", svc, TypeAidevopsService, adoThrottled, outcomeTransient, "ThrottlingException", adoTestTotal, true, false},
		{"private connection/not found", pc, TypeAidevopsPrivateConnection, adoNotFound, -1, "", adoTestTotal - 1, false, false},
		{"private connection/denied", pc, TypeAidevopsPrivateConnection, adoDenied, -1, "", adoTestTotal, true, true},
		{"private connection/transient", pc, TypeAidevopsPrivateConnection, adoThrottled, outcomeTransient, "ThrottlingException", adoTestTotal, true, false},
		{"private connection/other error", pc, TypeAidevopsPrivateConnection, adoBadInput, outcomeError, "ValidationException", adoTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newADOTagFixture("aws", testRegion)
			f.errs[c.arn] = c.err

			total, _, err := adoScan(context.Background(), st, f.handlers())
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{aidevopsTagsOp}
			}
			if got := scnWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads of every tag-read type send one warning for the whole
// scan; the rows are kept.
func TestScanAidevops_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newADOTagFixture("aws", testRegion)
	for _, typ := range adoTestTagged {
		for _, arn := range f.rows(typ) {
			f.errs[arn] = adoDenied
		}
	}

	total, _, err := adoScan(context.Background(), st, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != adoTestTotal {
		t.Errorf("total = %d; want %d", total, adoTestTotal)
	}
	if got := scnWarnOps(warns()); !slices.Equal(got, []string{aidevopsTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, aidevopsTagsOp)
	}
}

// A denied tag read is still warned about when another phase fails: the scan
// returns that phase's error, and the row stays stored without tags.
func TestScanAidevops_TagDenialWarnedBesideListingError(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newADOTagFixture("aws", testRegion)
	f.errs[f.spaceARN("s-tagged")] = adoDenied
	h := f.handlers()
	h["ListServices"] = func(any) (any, error) { return nil, adoBadInput }

	_, _, err := adoScan(context.Background(), st, h)
	assertAPICode(t, err, "ValidationException")
	w := warns()
	if got := scnWarnOps(w); !slices.Equal(got, []string{aidevopsTagsOp}) {
		t.Fatalf("warnings = %v; want one under %s", w, aidevopsTagsOp)
	}
	if w[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning Scope %q; want %s/%s", w[0].Scope, testAccountID, testRegion)
	}
	r, ok := rowsByNativeID(t, st, TypeAidevopsAgentSpace)[f.spaceARN("s-tagged")]
	if !ok {
		t.Fatal("agent space with the denied tag read not stored")
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAidevops_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, aidevopsTagsOp)
	f := newADOTagFixture("aws", testRegion)
	f.errs[f.spaceARN("s-bare")] = adoThrottled
	f.errs[f.pcARN("pc-bare")] = adoBadInput

	_, _, err := adoScan(context.Background(), st, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", aidevopsTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanAidevops_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeAidevopsService)
	f := newADOTagFixture("aws", testRegion)
	f.errs[f.serviceARN("svc-tagged")] = adoDenied

	total, _, err := adoScan(context.Background(), st, f.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListAssociations", "ListPrivateConnections", "ListAssets", "ListWebhooks"} {
		if n := f.calls(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the agent spaces)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeAidevopsAgentSpace)); n != 2 {
		t.Errorf("agent space rows = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases. An agent space not stored has no
// children read.
func TestScanAidevops_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	f0 := newADOTagFixture("aws", testRegion)
	for _, c := range []struct {
		op         string
		typ        string
		kept, lost string
		wantTotal  int
		wantPCs    int
	}{
		{"ListAgentSpaces", TypeAidevopsAgentSpace, f0.spaceARN("s-tagged"), f0.spaceARN("s-bare"), adoTestTotal - 1 - adoTestSpaceChildren, 2},
		{"ListServices", TypeAidevopsService, f0.serviceARN("svc-tagged"), f0.serviceARN("svc-bare"), adoTestTotal - 1, 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			f := newADOTagFixture("aws", testRegion)
			h := f.handlers()
			list := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, adoBadInput
				}
				return list(in)
			}

			total, _, err := adoScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeAidevopsPrivateConnection)); got != c.wantPCs {
				t.Errorf("private connections (a later phase) = %d; want %d", got, c.wantPCs)
			}
		})
	}
}

// A repeated nextToken ends the listing as an error, keeping its pages.
func TestScanAidevops_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	f := newADOTagFixture("aws", testRegion)
	h := f.handlers()
	list := h["ListServices"]
	h["ListServices"] = func(in any) (any, error) {
		out, err := list(in)
		if err != nil {
			return nil, err
		}
		out.(*devopsagent.ListServicesOutput).NextToken = sdkaws.String("same")
		return out, nil
	}

	total, _, err := adoScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != adoTestTotal {
		t.Errorf("total = %d; want %d", total, adoTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeAidevopsService)); n != 2 {
		t.Errorf("services = %d; want 2", n)
	}
}

// Agent spaces and services write their first page, with its tags, before
// the second page is read.
func TestScanAidevops_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	f := newADOTagFixture("aws", testRegion)
	h := f.handlers()
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for op, arn := range map[string]string{"ListAgentSpaces": f.spaceARN("s-tagged"), "ListServices": f.serviceARN("svc-tagged")} {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[op] = b2biStoredWithTags(st, arn)
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := adoScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range []string{"ListAgentSpaces", "ListServices"} {
		if !storedFirst[op] {
			t.Errorf("%s: the first page's row was not stored with its tags when the second page was read", op)
		}
	}
}

// An element without its identifier is not stored, is not sent to the tag
// op, and is warned about once under its listing, keyed by its name, another
// field, or the fallback text.
func TestScanAidevops_ElementWithoutIdentifier(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        any
		typ           string
		wantTotal     int
	}{
		{
			"agent space without id", "ListAgentSpaces", "orphan",
			&devopsagent.ListAgentSpacesOutput{AgentSpaces: []devopsagenttypes.AgentSpace{{Name: sdkaws.String("orphan")}}},
			TypeAidevopsAgentSpace, 2 + 2,
		},
		{
			"agent space without id or name", "ListAgentSpaces", noIdentifier,
			&devopsagent.ListAgentSpacesOutput{AgentSpaces: []devopsagenttypes.AgentSpace{{Locale: sdkaws.String("en-US")}}},
			TypeAidevopsAgentSpace, 2 + 2,
		},
		{
			"service without id", "ListServices", "named-service",
			&devopsagent.ListServicesOutput{Services: []devopsagenttypes.RegisteredService{{Name: sdkaws.String("named-service"), ServiceType: devopsagenttypes.ServiceGithub}}},
			TypeAidevopsService, adoTestTotal - 2,
		},
		{
			"service without id or name", "ListServices", noIdentifier,
			&devopsagent.ListServicesOutput{Services: []devopsagenttypes.RegisteredService{{ServiceType: devopsagenttypes.ServiceGithub}}},
			TypeAidevopsService, adoTestTotal - 2,
		},
		{
			"private connection without name", "ListPrivateConnections", "host.example.internal",
			&devopsagent.ListPrivateConnectionsOutput{PrivateConnections: []devopsagenttypes.PrivateConnectionSummary{{HostAddress: sdkaws.String("host.example.internal")}}},
			TypeAidevopsPrivateConnection, adoTestTotal - 2,
		},
		{
			"private connection without name or host", "ListPrivateConnections", noIdentifier,
			&devopsagent.ListPrivateConnectionsOutput{PrivateConnections: []devopsagenttypes.PrivateConnectionSummary{{VpcId: sdkaws.String("vpc-1")}}},
			TypeAidevopsPrivateConnection, adoTestTotal - 2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newADOTagFixture("aws", testRegion)
			h := f.handlers()
			h[c.op] = func(any) (any, error) { return c.answer, nil }

			total, _, err := adoScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, n)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "aidevops:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under aidevops:%s naming %s", w, c.op, c.key)
			}
			for _, arn := range f.sortedTagARNs() {
				if strings.HasSuffix(arn, "/") {
					t.Errorf("ListTagsForResource sent %q, an ARN without an id", arn)
				}
			}
		})
	}
}

// In GovCloud and China the built ARNs carry the account's partition, and
// reach the tag op unchanged.
func TestScanAidevops_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newADOTagFixture(pf.partition, pf.region)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(f.handlers(), adoDenied)

			if _, _, err := scanAidevops(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			space := map[string]string{
				"aws-cn":     "arn:aws-cn:aidevops:cn-north-1:123456789012:agentspace/s-tagged",
				"aws-us-gov": "arn:aws-us-gov:aidevops:us-gov-west-1:123456789012:agentspace/s-tagged",
			}[pf.partition]
			for _, arn := range []string{space, f.serviceARN("svc-tagged"), f.pcARN("pc-tagged")} {
				if !slices.Contains(f.sortedTagARNs(), arn) {
					t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", f.sortedTagARNs(), arn)
				}
			}
			if n := len(f.sortedTagARNs()); n != 6 {
				t.Errorf("tag reads = %d; want 6", n)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAidevopsAgentSpace)[space].TagsJSON, map[string]string{"type": TypeAidevopsAgentSpace})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it, and the later phases are not run.
func TestScanAidevops_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	f := newADOTagFixture("aws", testRegion)
	h := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListServices"]
	h["ListServices"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := adoScan(ctx, st, h)
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
	if n := f.calls("ListAssociations"); n != 0 {
		t.Errorf("ListAssociations called %d times after ctx ended", n)
	}
}

// A tag read's NXDOMAIN after the listing reached the endpoint is a failure
// of this scan, not a region gap: the counts are kept.
func TestScanAidevops_TagReadNXDOMAINKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	f := newADOTagFixture("aws", testRegion)
	f.errs[f.serviceARN("svc-bare")] = &net.DNSError{Err: "no such host", Name: "cp.aidevops.us-east-1.api.aws", IsNotFound: true}

	total, _, err := adoScan(context.Background(), st, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	if total != adoTestTotal {
		t.Errorf("total = %d; want %d", total, adoTestTotal)
	}
}

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	devopsagenttypes "github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/aws/smithy-go"
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
	return aidevopsARN(testRegion, testAccountID, "agent-space", id)
}

func adoAssocARN(id string) string {
	return aidevopsARN(testRegion, testAccountID, "association", id)
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
// the ops in override answering instead. Every other op is denied.
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

// A later phase's transient error (assets, triggers and goals are on the
// data-plane host) is warned about on its own, so it cannot make the dispatcher classify
// the whole service transient or stop the phases after it.
func TestScanAidevopsAll_TransientPhaseDoesNotMaskOthers(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "aidevops:ListAssets")
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAssets":   func(any) (any, error) { return nil, adoThrottled },
		"ListServices": func(any) (any, error) { return nil, adoBadInput },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
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
func TestScanAidevopsAll_UnreachableEndpointStopsTheScan(t *testing.T) {
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

// A first-phase error that is not a network failure is returned with the
// other phases' errors, and the phases that do not need agent spaces still
// store their rows.
func TestScanAidevopsAll_AgentSpaceErrorKeepsIndependentPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := adoTopLevelHandlers(map[string]func(any) (any, error){
		"ListAgentSpaces": func(any) (any, error) { return nil, adoBadInput },
	})
	total, _, err := scanAidevopsAll(context.Background(), adoClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, adoBadInput) {
		t.Errorf("err = %v, want the ListAgentSpaces error", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
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

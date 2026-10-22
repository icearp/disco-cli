package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	connectListedName    = "listed"
	connectDescribedName = "described"
)

// connectTestTags is the tag map the stub Describe ops return: resources whose
// id ends in -1 are tagged, the rest are not.
func connectTestTags(id string) map[string]string {
	if strings.HasSuffix(id, "-1") {
		return map[string]string{"env": "prod"}
	}
	return nil
}

// connectTestListed is one listed resource of a connectTagCase.
type connectTestListed struct{ id, arn string }

// connectTagCase drives one Describe-backed Connect type through its scan
// function: the list op returns the resources, the Describe op returns each
// with connectTestTags(id) and the name connectDescribedName.
type connectTagCase struct {
	typ, listOp, describeOp string
	// arn is the ARN the list op returns for id.
	arn  func(id string) string
	list func(items []connectTestListed) any
	// listsIn, when set, says which list calls return the items; the rest
	// return none.
	listsIn  func(in any) bool
	describe func(in any) any
	run      func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error)
}

func connectTestChildARN(kind string) func(string) string {
	return func(id string) string { return connectTestInstanceARN + "/" + kind + "/" + id }
}

func connectTestAccountARN(kind string) func(string) string {
	return func(id string) string { return "arn:aws:connect:us-east-1:123456789012:" + kind + "/" + id }
}

var connectTestInstanceList = connectInstances("i-1")

var connectTagCases = []connectTagCase{
	{
		typ: TypeConnectInstance, describeOp: "DescribeInstance",
		arn: connectTestAccountARN("instance"),
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeInstanceInput).InstanceId)
			return &connect.DescribeInstanceOutput{Instance: &cttypes.Instance{Id: &id, InstanceAlias: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			instances := []cttypes.InstanceSummary{
				{Id: ptrStr("i-1"), Arn: ptrStr(connectTestAccountARN("instance")("i-1")), InstanceAlias: ptrStr(connectListedName)},
				{Id: ptrStr("i-2"), Arn: ptrStr(connectTestAccountARN("instance")("i-2")), InstanceAlias: ptrStr(connectListedName)},
			}
			return scanConnectInstances(ctx, c, instances, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectTrafficDistributionGroup, listOp: "ListTrafficDistributionGroups", describeOp: "DescribeTrafficDistributionGroup",
		arn: connectTestAccountARN("traffic-distribution-group"),
		list: func(items []connectTestListed) any {
			out := &connect.ListTrafficDistributionGroupsOutput{}
			for _, x := range items {
				out.TrafficDistributionGroupSummaryList = append(out.TrafficDistributionGroupSummaryList, cttypes.TrafficDistributionGroupSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeTrafficDistributionGroupInput).TrafficDistributionGroupId)
			return &connect.DescribeTrafficDistributionGroupOutput{TrafficDistributionGroup: &cttypes.TrafficDistributionGroup{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectTrafficDistributionGroups(ctx, c, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectPhoneNumber, listOp: "ListPhoneNumbersV2", describeOp: "DescribePhoneNumber",
		arn: connectTestAccountARN("phone-number"),
		list: func(items []connectTestListed) any {
			out := &connect.ListPhoneNumbersV2Output{}
			for _, x := range items {
				out.ListPhoneNumbersSummaryList = append(out.ListPhoneNumbersSummaryList, cttypes.ListPhoneNumbersSummary{PhoneNumberId: ptrStr(x.id), PhoneNumberArn: ptrStr(x.arn), PhoneNumber: ptrStr(connectListedName)})
			}
			return out
		},
		// DescribePhoneNumber must be sent the ARN; sent anything else it
		// answers no body, which fails the scan.
		describe: func(in any) any {
			arn := sv(in.(*connect.DescribePhoneNumberInput).PhoneNumberId)
			if !strings.HasPrefix(arn, "arn:") {
				return &connect.DescribePhoneNumberOutput{}
			}
			id := arn[strings.LastIndex(arn, "/")+1:]
			return &connect.DescribePhoneNumberOutput{ClaimedPhoneNumberSummary: &cttypes.ClaimedPhoneNumberSummary{PhoneNumberId: &id, PhoneNumber: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectPhoneNumbers(ctx, c, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectQueue, listOp: "ListQueues", describeOp: "DescribeQueue",
		arn: connectTestChildARN("queue"),
		list: func(items []connectTestListed) any {
			out := &connect.ListQueuesOutput{}
			for _, x := range items {
				out.QueueSummaryList = append(out.QueueSummaryList, cttypes.QueueSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeQueueInput).QueueId)
			return &connect.DescribeQueueOutput{Queue: &cttypes.Queue{QueueId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectQueues(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectRoutingProfile, listOp: "ListRoutingProfiles", describeOp: "DescribeRoutingProfile",
		arn: connectTestChildARN("routing-profile"),
		list: func(items []connectTestListed) any {
			out := &connect.ListRoutingProfilesOutput{}
			for _, x := range items {
				out.RoutingProfileSummaryList = append(out.RoutingProfileSummaryList, cttypes.RoutingProfileSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeRoutingProfileInput).RoutingProfileId)
			return &connect.DescribeRoutingProfileOutput{RoutingProfile: &cttypes.RoutingProfile{RoutingProfileId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectRoutingProfiles(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectHoursOfOperation, listOp: "ListHoursOfOperations", describeOp: "DescribeHoursOfOperation",
		arn: connectTestChildARN("operating-hours"),
		list: func(items []connectTestListed) any {
			out := &connect.ListHoursOfOperationsOutput{}
			for _, x := range items {
				out.HoursOfOperationSummaryList = append(out.HoursOfOperationSummaryList, cttypes.HoursOfOperationSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeHoursOfOperationInput).HoursOfOperationId)
			return &connect.DescribeHoursOfOperationOutput{HoursOfOperation: &cttypes.HoursOfOperation{HoursOfOperationId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectHoursOfOperations(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectAgentStatus, listOp: "ListAgentStatuses", describeOp: "DescribeAgentStatus",
		arn: connectTestChildARN("agent-state"),
		list: func(items []connectTestListed) any {
			out := &connect.ListAgentStatusesOutput{}
			for _, x := range items {
				out.AgentStatusSummaryList = append(out.AgentStatusSummaryList, cttypes.AgentStatusSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeAgentStatusInput).AgentStatusId)
			return &connect.DescribeAgentStatusOutput{AgentStatus: &cttypes.AgentStatus{AgentStatusId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectAgentStatuses(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectQuickConnect, listOp: "ListQuickConnects", describeOp: "DescribeQuickConnect",
		arn: connectTestChildARN("transfer-destination"),
		list: func(items []connectTestListed) any {
			out := &connect.ListQuickConnectsOutput{}
			for _, x := range items {
				out.QuickConnectSummaryList = append(out.QuickConnectSummaryList, cttypes.QuickConnectSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeQuickConnectInput).QuickConnectId)
			return &connect.DescribeQuickConnectOutput{QuickConnect: &cttypes.QuickConnect{QuickConnectId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectQuickConnects(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectUser, listOp: "ListUsers", describeOp: "DescribeUser",
		arn: connectTestChildARN("agent"),
		list: func(items []connectTestListed) any {
			out := &connect.ListUsersOutput{}
			for _, x := range items {
				out.UserSummaryList = append(out.UserSummaryList, cttypes.UserSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Username: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeUserInput).UserId)
			return &connect.DescribeUserOutput{User: &cttypes.User{Id: &id, Username: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectUserList(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectUserHierarchyGroup, listOp: "ListUserHierarchyGroups", describeOp: "DescribeUserHierarchyGroup",
		arn: connectTestChildARN("agent-group"),
		list: func(items []connectTestListed) any {
			out := &connect.ListUserHierarchyGroupsOutput{}
			for _, x := range items {
				out.UserHierarchyGroupSummaryList = append(out.UserHierarchyGroupSummaryList, cttypes.HierarchyGroupSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeUserHierarchyGroupInput).HierarchyGroupId)
			return &connect.DescribeUserHierarchyGroupOutput{HierarchyGroup: &cttypes.HierarchyGroup{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectUserHierarchyGroups(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectSecurityProfile, listOp: "ListSecurityProfiles", describeOp: "DescribeSecurityProfile",
		arn: connectTestChildARN("security-profile"),
		list: func(items []connectTestListed) any {
			out := &connect.ListSecurityProfilesOutput{}
			for _, x := range items {
				out.SecurityProfileSummaryList = append(out.SecurityProfileSummaryList, cttypes.SecurityProfileSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeSecurityProfileInput).SecurityProfileId)
			return &connect.DescribeSecurityProfileOutput{SecurityProfile: &cttypes.SecurityProfile{Id: &id, SecurityProfileName: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectSecurityProfiles(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectContactFlow, listOp: "ListContactFlows", describeOp: "DescribeContactFlow",
		arn: connectTestChildARN("contact-flow"),
		list: func(items []connectTestListed) any {
			out := &connect.ListContactFlowsOutput{}
			for _, x := range items {
				out.ContactFlowSummaryList = append(out.ContactFlowSummaryList, cttypes.ContactFlowSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeContactFlowInput).ContactFlowId)
			return &connect.DescribeContactFlowOutput{ContactFlow: &cttypes.ContactFlow{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectFlowsFamilyOnly(ctx, c, st, acct, TypeConnectContactFlow)
		},
	},
	{
		typ: TypeConnectContactFlowModule, listOp: "ListContactFlowModules", describeOp: "DescribeContactFlowModule",
		arn: connectTestChildARN("flow-module"),
		list: func(items []connectTestListed) any {
			out := &connect.ListContactFlowModulesOutput{}
			for _, x := range items {
				out.ContactFlowModulesSummaryList = append(out.ContactFlowModulesSummaryList, cttypes.ContactFlowModuleSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeContactFlowModuleInput).ContactFlowModuleId)
			return &connect.DescribeContactFlowModuleOutput{ContactFlowModule: &cttypes.ContactFlowModule{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectFlowsFamilyOnly(ctx, c, st, acct, TypeConnectContactFlowModule)
		},
	},
	{
		typ: TypeConnectRule, listOp: "ListRules", describeOp: "DescribeRule",
		// A rule belongs to one event source.
		listsIn: func(in any) bool {
			return in.(*connect.ListRulesInput).EventSourceName == cttypes.EventSourceNameOnContactEvaluationSubmit
		},
		arn: connectTestChildARN("rule"),
		list: func(items []connectTestListed) any {
			out := &connect.ListRulesOutput{}
			for _, x := range items {
				out.RuleSummaryList = append(out.RuleSummaryList, cttypes.RuleSummary{RuleId: ptrStr(x.id), RuleArn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeRuleInput).RuleId)
			return &connect.DescribeRuleOutput{Rule: &cttypes.Rule{RuleId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectRules(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectEvaluationForm, listOp: "ListEvaluationForms", describeOp: "DescribeEvaluationForm",
		arn: connectTestChildARN("evaluation-form"),
		list: func(items []connectTestListed) any {
			out := &connect.ListEvaluationFormsOutput{}
			for _, x := range items {
				out.EvaluationFormSummaryList = append(out.EvaluationFormSummaryList, cttypes.EvaluationFormSummary{EvaluationFormId: ptrStr(x.id), EvaluationFormArn: ptrStr(x.arn), Title: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeEvaluationFormInput).EvaluationFormId)
			return &connect.DescribeEvaluationFormOutput{EvaluationForm: &cttypes.EvaluationForm{EvaluationFormId: &id, Title: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectEvaluationForms(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectView, listOp: "ListViews", describeOp: "DescribeView",
		arn: connectTestChildARN("view"),
		list: func(items []connectTestListed) any {
			out := &connect.ListViewsOutput{}
			for _, x := range items {
				out.ViewsSummaryList = append(out.ViewsSummaryList, cttypes.ViewSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeViewInput).ViewId)
			return &connect.DescribeViewOutput{View: &cttypes.View{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectWorkspaceFamilyOnly(ctx, c, st, acct, TypeConnectView)
		},
	},
	{
		typ: TypeConnectWorkspace, listOp: "ListWorkspaces", describeOp: "DescribeWorkspace",
		arn: connectTestChildARN("workspace"),
		list: func(items []connectTestListed) any {
			out := &connect.ListWorkspacesOutput{}
			for _, x := range items {
				out.WorkspaceSummaryList = append(out.WorkspaceSummaryList, cttypes.WorkspaceSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribeWorkspaceInput).WorkspaceId)
			return &connect.DescribeWorkspaceOutput{Workspace: &cttypes.Workspace{Id: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectWorkspaces(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
	{
		typ: TypeConnectPrompt, listOp: "ListPrompts", describeOp: "DescribePrompt",
		arn: connectTestChildARN("prompt"),
		list: func(items []connectTestListed) any {
			out := &connect.ListPromptsOutput{}
			for _, x := range items {
				out.PromptSummaryList = append(out.PromptSummaryList, cttypes.PromptSummary{Id: ptrStr(x.id), Arn: ptrStr(x.arn), Name: ptrStr(connectListedName)})
			}
			return out
		},
		describe: func(in any) any {
			id := sv(in.(*connect.DescribePromptInput).PromptId)
			return &connect.DescribePromptOutput{Prompt: &cttypes.Prompt{PromptId: &id, Name: ptrStr(connectDescribedName), Tags: connectTestTags(id)}}
		},
		run: func(ctx context.Context, c *connect.Client, st *store.Store, acct *account) (int, int, error) {
			return scanConnectPrompts(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
		},
	},
}

// scanConnectFlowsFamilyOnly runs the whole flows family, so the shared
// listings are wired as in production, and reports only typ's rows.
func scanConnectFlowsFamilyOnly(ctx context.Context, c *connect.Client, st *store.Store, acct *account, typ string) (int, int, error) {
	_, _, err := scanConnectFlows(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
	n, nerr := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{typ}})
	return len(n), len(n), errors.Join(err, nerr)
}

// scanConnectWorkspaceFamilyOnly is scanConnectFlowsFamilyOnly for the
// workspace family (views share their listing with view versions).
func scanConnectWorkspaceFamilyOnly(ctx context.Context, c *connect.Client, st *store.Store, acct *account, typ string) (int, int, error) {
	_, _, err := scanConnectWorkspace(ctx, c, connectTestInstanceList, acct, testRegion, st, testScanID)
	n, nerr := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{typ}})
	return len(n), len(n), errors.Join(err, nerr)
}

// handlers answers the case's list op with x-1 and x-2 and, when describe is
// set, its Describe op; every other op is denied.
func (c connectTagCase) handlers(describe bool) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){}
	if c.list != nil {
		items := []connectTestListed{{"x-1", c.arn("x-1")}, {"x-2", c.arn("x-2")}}
		h[c.listOp] = func(in any) (any, error) {
			if c.listsIn != nil && !c.listsIn(in) {
				return c.list(nil), nil
			}
			return c.list(items), nil
		}
	}
	if describe {
		h[c.describeOp] = func(in any) (any, error) { return c.describe(in), nil }
	}
	return h
}

// listedIDs is the ids the case lists; the instance case lists i-1 and i-2.
func (c connectTagCase) listedIDs() [2]string {
	if c.list == nil {
		return [2]string{"i-1", "i-2"}
	}
	return [2]string{"x-1", "x-2"}
}

// Each Describe-backed type stores the Describe body's tags, and a resource
// described without tags stores none.
func TestConnectDescribedTypes_StoreTags(t *testing.T) {
	for _, c := range connectTagCases {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			client := connect.NewFromConfig(connectStubConfig(c.handlers(true)))
			total, _, err := c.run(context.Background(), client, st, acct)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 {
				t.Errorf("total = %d, want 2", total)
			}
			ids := c.listedIDs()
			rows := rowsByNativeID(t, st, c.typ)
			tagged, ok := rows[c.arn(ids[0])]
			if !ok {
				t.Fatalf("no row %s; rows %v", c.arn(ids[0]), rows)
			}
			assertTagsMap(t, tagged.TagsJSON, map[string]string{"env": "prod"})
			if got := sv(tagged.Name); got != connectDescribedName {
				t.Errorf("Name = %q, want the Describe body's %q", got, connectDescribedName)
			}
			untagged, ok := rows[c.arn(ids[1])]
			if !ok {
				t.Fatalf("no row %s", c.arn(ids[1]))
			}
			if untagged.TagsJSON != nil {
				t.Errorf("untagged TagsJSON = %q, want nil", *untagged.TagsJSON)
			}
		})
	}
}

// A denied Describe stores every listed resource as its list summary, without
// tags, and warns once for the op.
func TestConnectDescribedTypes_DeniedDescribeKeepsSummary(t *testing.T) {
	for _, c := range connectTagCases {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "connect:"+c.describeOp)
			acct := newTestAccount(testAccountID)
			client := connect.NewFromConfig(connectStubConfig(c.handlers(false)))
			if _, _, err := c.run(context.Background(), client, st, acct); err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, c.typ)
			for _, id := range c.listedIDs() {
				r, ok := rows[c.arn(id)]
				if !ok {
					t.Errorf("no row %s", c.arn(id))
					continue
				}
				if got := sv(r.Name); got != connectListedName {
					t.Errorf("%s Name = %q, want the summary's %q", id, got, connectListedName)
				}
				if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q, want nil", id, *r.TagsJSON)
				}
			}
			if *warned != 1 {
				t.Errorf("%s warnings = %d, want 1", c.describeOp, *warned)
			}
		})
	}
}

// queueCase is the connectTagCases entry the helper-branch tests drive.
func queueCase(t *testing.T) connectTagCase {
	t.Helper()
	return connectCase(t, TypeConnectQueue)
}

// runQueues runs the queue phase with DescribeQueue failing with fail for x-1
// (x-2 is described).
func runQueues(t *testing.T, st *store.Store, fail error) (int, error) {
	t.Helper()
	c := queueCase(t)
	h := c.handlers(true)
	h["DescribeQueue"] = func(in any) (any, error) {
		if sv(in.(*connect.DescribeQueueInput).QueueId) == "x-1" {
			return nil, fail
		}
		return c.describe(in), nil
	}
	total, _, err := scanConnectQueues(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestConnectDescribe_NotFoundSkipsTheResource(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:DescribeQueue")
	total, err := runQueues(t, st, &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	rows := rowsByNativeID(t, st, TypeConnectQueue)
	if _, ok := rows[connectTestInstanceARN+"/queue/x-1"]; ok {
		t.Error("deleted queue x-1 stored")
	}
	if _, ok := rows[connectTestInstanceARN+"/queue/x-2"]; !ok || total != 1 {
		t.Errorf("total = %d, rows %v; want only x-2", total, rows)
	}
	if *warned != 0 {
		t.Errorf("warnings = %d, want 0", *warned)
	}
}

func TestConnectDescribe_TransientKeepsSummaryAndWarns(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:DescribeQueue")
	total, err := runQueues(t, st, connectThrottled)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	r, ok := rowsByNativeID(t, st, TypeConnectQueue)[connectTestInstanceARN+"/queue/x-1"]
	if !ok || sv(r.Name) != connectListedName {
		t.Errorf("x-1 = %+v, want its summary row", r)
	}
	if total != 2 || *warned != 1 {
		t.Errorf("total = %d, warnings = %d; want 2 and 1", total, *warned)
	}
}

// Any other Describe error keeps the summary row and is returned after it is stored.
func TestConnectDescribe_OtherErrorKeepsSummaryAndReturns(t *testing.T) {
	for name, fail := range map[string]error{"api error": connectHardErr, "no body": nil} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			var total int
			var err error
			if fail == nil {
				c := queueCase(t)
				h := c.handlers(true)
				h["DescribeQueue"] = func(in any) (any, error) {
					if sv(in.(*connect.DescribeQueueInput).QueueId) == "x-1" {
						return &connect.DescribeQueueOutput{}, nil
					}
					return c.describe(in), nil
				}
				total, _, err = scanConnectQueues(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
				if !errors.Is(err, errConnectNoBody) {
					t.Errorf("err = %v, want errConnectNoBody", err)
				}
			} else {
				total, err = runQueues(t, st, fail)
				assertAPICode(t, err, "UnrecognizedFailureException")
			}
			r, ok := rowsByNativeID(t, st, TypeConnectQueue)[connectTestInstanceARN+"/queue/x-1"]
			if !ok || sv(r.Name) != connectListedName || r.TagsJSON != nil {
				t.Errorf("x-1 = %+v, want its summary row without tags", r)
			}
			if total != 2 {
				t.Errorf("total = %d, want 2", total)
			}
		})
	}
}

// A queue listed without an ARN is not stored and is warned about once.
func TestConnectListing_NoARNWarnsNotStored(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:ListQueues")
	c := queueCase(t)
	h := c.handlers(true)
	h["ListQueues"] = func(any) (any, error) {
		return &connect.ListQueuesOutput{QueueSummaryList: []cttypes.QueueSummary{
			{Id: ptrStr("x-1"), Arn: ptrStr(c.arn("x-1"))},
			{Id: ptrStr("x-2")},
		}}, nil
	}
	total, _, err := scanConnectQueues(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total = %d, err = %v; want 1, nil", total, err)
	}
	if *warned != 1 {
		t.Errorf("ListQueues warnings = %d, want 1", *warned)
	}
}

// Failed listings on the instances around i-2 keep its queues, which are
// stored before the error is returned; denials on several instances warn once.
func TestConnectListing_FailedInstanceKeepsTheOthers(t *testing.T) {
	c := queueCase(t)
	byInstance := func(fail error) map[string]func(any) (any, error) {
		h := c.handlers(true)
		h["ListQueues"] = func(in any) (any, error) {
			if sv(in.(*connect.ListQueuesInput).InstanceId) != "i-2" {
				return nil, fail
			}
			return c.list([]connectTestListed{{"x-1", c.arn("x-1")}}), nil
		}
		return h
	}
	instances := connectInstances("i-1", "i-2", "i-3")
	t.Run("hard error", func(t *testing.T) {
		st := newTestStore(t)
		total, _, err := scanConnectQueues(context.Background(), connect.NewFromConfig(connectStubConfig(byInstance(connectHardErr))), instances, newTestAccount(testAccountID), testRegion, st, testScanID)
		assertAPICode(t, err, "UnrecognizedFailureException")
		if _, ok := rowsByNativeID(t, st, TypeConnectQueue)[c.arn("x-1")]; !ok || total != 1 {
			t.Errorf("total = %d; want x-1 stored", total)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warned := countOpWarnings(st, "connect:ListQueues")
		total, _, err := scanConnectQueues(context.Background(), connect.NewFromConfig(connectStubConfig(byInstance(connectDenied))), instances, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 1 {
			t.Errorf("total = %d, err = %v; want 1, nil", total, err)
		}
		if *warned != 1 {
			t.Errorf("ListQueues warnings = %d, want 1", *warned)
		}
	})
}

// A traffic distribution group listing refused by a resource-based policy
// (no Global Resiliency instance) is expected state: no warning, no error.
func TestConnectTrafficDistributionGroups_ResourcePolicyDenySilent(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:ListTrafficDistributionGroups")
	h := map[string]func(any) (any, error){
		"ListTrafficDistributionGroups": func(any) (any, error) {
			return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "with an explicit deny in a resource-based policy"}
		},
	}
	total, _, err := scanConnectTrafficDistributionGroups(context.Background(), connect.NewFromConfig(connectStubConfig(h)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 || *warned != 0 {
		t.Errorf("total = %d, err = %v, warnings = %d; want 0, nil, 0", total, err, *warned)
	}
}

// A phase error does not stop the later phases, their counts are summed and
// the error is returned; a failed store write stops the phases at once.
func TestConnectRunPhases(t *testing.T) {
	acct := newTestAccount(testAccountID)
	t.Run("hard error", func(t *testing.T) {
		st := newTestStore(t)
		ran := false
		total, inserted, err := connectRunPhases(st, acct, testRegion,
			connectPhase{"first", func() (int, int, error) { return 1, 1, connectHardErr }},
			connectPhase{"second", func() (int, int, error) { ran = true; return 2, 2, nil }},
		)
		assertAPICode(t, err, "UnrecognizedFailureException")
		if !ran || total != 3 || inserted != 3 {
			t.Errorf("second ran = %v, total = %d, inserted = %d; want true, 3, 3", ran, total, inserted)
		}
	})
	t.Run("store write", func(t *testing.T) {
		st := newTestStore(t)
		ran := false
		_, _, err := connectRunPhases(st, acct, testRegion,
			connectPhase{"first", func() (int, int, error) { return 0, 0, fmt.Errorf("upsert: %w", store.ErrStoreWrite) }},
			connectPhase{"second", func() (int, int, error) { ran = true; return 0, 0, nil }},
		)
		if !errors.Is(err, store.ErrStoreWrite) || ran {
			t.Errorf("err = %v, second ran = %v; want ErrStoreWrite and false", err, ran)
		}
	})
}

// Notifications carry their tags on the listed element.
func TestScanConnectNotifications_Tags(t *testing.T) {
	st := newTestStore(t)
	arn := func(id string) string { return connectTestInstanceARN + "/notification/" + id }
	h := map[string]func(any) (any, error){
		"ListNotifications": func(any) (any, error) {
			return &connect.ListNotificationsOutput{NotificationSummaryList: []cttypes.Notification{
				{Id: ptrStr("n-1"), Arn: ptrStr(arn("n-1")), Tags: connectTestTags("n-1")},
				{Id: ptrStr("n-2"), Arn: ptrStr(arn("n-2"))},
			}}, nil
		},
	}
	total, _, err := scanConnectNotifications(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total = %d, err = %v; want 2, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeConnectNotification)
	assertTagsMap(t, rows[arn("n-1")].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[arn("n-2")].TagsJSON; got != nil {
		t.Errorf("n-2 TagsJSON = %q, want nil", *got)
	}
}

// A denied notification listing warns; a failed one keeps the other
// instances' notifications and returns the error; one without an ARN warns.
func TestScanConnectNotifications_ListingErrors(t *testing.T) {
	arn := connectTestInstanceARN + "/notification/n-1"
	handlers := func(fail error) map[string]func(any) (any, error) {
		return map[string]func(any) (any, error){
			"ListNotifications": func(in any) (any, error) {
				if sv(in.(*connect.ListNotificationsInput).InstanceId) != "i-2" {
					return nil, fail
				}
				return &connect.ListNotificationsOutput{NotificationSummaryList: []cttypes.Notification{
					{Id: ptrStr("n-1"), Arn: ptrStr(arn)}, {Id: ptrStr("n-noarn")},
				}}, nil
			},
		}
	}
	instances := connectInstances("i-1", "i-2")
	for name, tc := range map[string]struct {
		fail     error
		wantCode string
		warnings int
	}{
		"denied":     {connectDenied, "", 2},
		"hard error": {connectHardErr, "UnrecognizedFailureException", 1},
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "connect:ListNotifications")
			total, _, err := scanConnectNotifications(context.Background(), connect.NewFromConfig(connectStubConfig(handlers(tc.fail))), instances, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if _, ok := rowsByNativeID(t, st, TypeConnectNotification)[arn]; !ok || total != 1 {
				t.Errorf("total = %d; want n-1 stored", total)
			}
			if *warned != tc.warnings {
				t.Errorf("warnings = %d, want %d", *warned, tc.warnings)
			}
		})
	}
}

// connectCase is the connectTagCases entry for typ.
func connectCase(t *testing.T, typ string) connectTagCase {
	t.Helper()
	for _, c := range connectTagCases {
		if c.typ == typ {
			return c
		}
	}
	t.Fatalf("no %s case", typ)
	return connectTagCase{}
}

// A flow never published is a normal state: DescribeContactFlow refuses it,
// the summary row is kept, one warning covers every such flow, and the scan
// does not fail.
func TestConnectContactFlow_NotPublishedKeepsSummaryAndWarnsOnce(t *testing.T) {
	c := connectCase(t, TypeConnectContactFlow)
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:DescribeContactFlow")
	h := c.handlers(true)
	h["DescribeContactFlow"] = func(any) (any, error) {
		return nil, &smithy.GenericAPIError{Code: "ContactFlowNotPublishedException", Message: "not published"}
	}
	total, _, err := c.run(context.Background(), connect.NewFromConfig(connectStubConfig(h)), st, newTestAccount(testAccountID))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	rows := rowsByNativeID(t, st, TypeConnectContactFlow)
	for _, id := range c.listedIDs() {
		if r, ok := rows[c.arn(id)]; !ok || sv(r.Name) != connectListedName {
			t.Errorf("%s = %+v, want its summary row", id, r)
		}
	}
	if total != 2 || *warned != 1 {
		t.Errorf("total = %d, warnings = %d; want 2 and 1", total, *warned)
	}
}

// connectFlowsHandlers lists flows f-1 and modules m-1 and describes both;
// every other op is denied unless extra answers it.
func connectFlowsHandlers(extra map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListContactFlows": func(any) (any, error) {
			return &connect.ListContactFlowsOutput{ContactFlowSummaryList: []cttypes.ContactFlowSummary{
				{Id: ptrStr("f-1"), Arn: ptrStr(connectTestInstanceARN + "/contact-flow/f-1")},
			}}, nil
		},
		"ListContactFlowModules": func(any) (any, error) {
			return &connect.ListContactFlowModulesOutput{ContactFlowModulesSummaryList: []cttypes.ContactFlowModuleSummary{
				{Id: ptrStr("m-1"), Arn: ptrStr(connectTestInstanceARN + "/flow-module/m-1")},
			}}, nil
		},
		"DescribeContactFlow": func(any) (any, error) {
			return &connect.DescribeContactFlowOutput{ContactFlow: &cttypes.ContactFlow{Id: ptrStr("f-1")}}, nil
		},
		"DescribeContactFlowModule": func(any) (any, error) {
			return &connect.DescribeContactFlowModuleOutput{ContactFlowModule: &cttypes.ContactFlowModule{Id: ptrStr("m-1")}}, nil
		},
	}
	maps.Copy(h, extra)
	return h
}

// Denied version and alias listings each warn once and do not fail the scan.
func TestConnectFlows_DeniedChildListingsWarnOncePerOp(t *testing.T) {
	st := newTestStore(t)
	ops := []string{"connect:ListContactFlowVersions", "connect:ListContactFlowModuleVersions", "connect:ListContactFlowModuleAliases"}
	warned := make([]*int, len(ops))
	for i, op := range ops {
		warned[i] = countOpWarnings(st, op)
	}
	_, _, err := scanConnectFlows(context.Background(), connect.NewFromConfig(connectStubConfig(connectFlowsHandlers(nil))), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	for i, op := range ops {
		if *warned[i] != 1 {
			t.Errorf("%s warnings = %d, want 1", op, *warned[i])
		}
	}
}

// Module versions report the rows the store inserted: a rescan of the same
// versions inserts none.
func TestConnectFlowModuleVersions_InsertedIsTheStoreCount(t *testing.T) {
	st := newTestStore(t)
	h := connectFlowsHandlers(map[string]func(any) (any, error){
		"ListContactFlowModuleVersions": func(any) (any, error) {
			return &connect.ListContactFlowModuleVersionsOutput{ContactFlowModuleVersionSummaryList: []cttypes.ContactFlowModuleVersionSummary{
				{Arn: ptrStr(connectTestInstanceARN + "/flow-module/m-1:1"), Version: sdkaws.Int64(1)},
				{Arn: ptrStr(connectTestInstanceARN + "/flow-module/m-1:2"), Version: sdkaws.Int64(2)},
			}}, nil
		},
	})
	client := connect.NewFromConfig(connectStubConfig(h))
	modules := []connectListed[cttypes.ContactFlowModuleSummary]{{instanceID: "i-1", id: "m-1", arn: connectTestInstanceARN + "/flow-module/m-1"}}
	acct := newTestAccount(testAccountID)
	for run, want := range []int{2, 0} {
		total, inserted, err := scanConnectContactFlowModuleVersions(context.Background(), client, modules, acct, testRegion, st, testScanID)
		if err != nil || total != 2 || inserted != want {
			t.Errorf("run %d: total = %d, inserted = %d, err = %v; want 2, %d, nil", run, total, inserted, err, want)
		}
	}
}

// A denied DescribeContactFlowModuleAlias keeps each alias's summary row and
// warns once.
func TestConnectFlowModuleAliases_DeniedDescribeKeepsSummary(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:DescribeContactFlowModuleAlias")
	arn := connectTestInstanceARN + "/flow-module/m-1/alias/a-1"
	h := connectFlowsHandlers(map[string]func(any) (any, error){
		"ListContactFlowModuleAliases": func(any) (any, error) {
			return &connect.ListContactFlowModuleAliasesOutput{ContactFlowModuleAliasSummaryList: []cttypes.ContactFlowModuleAliasSummary{
				{AliasId: ptrStr("a-1"), Arn: ptrStr(arn), AliasName: ptrStr(connectListedName)},
				{AliasId: ptrStr("a-2"), Arn: ptrStr(arn + "2"), AliasName: ptrStr(connectListedName)},
			}}, nil
		},
	})
	_, _, err := scanConnectFlows(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if r, ok := rowsByNativeID(t, st, TypeConnectContactFlowModuleAlias)[arn]; !ok || sv(r.Name) != connectListedName {
		t.Errorf("a-1 = %+v, want its summary row", r)
	}
	if *warned != 1 {
		t.Errorf("warnings = %d, want 1", *warned)
	}
}

// A denied DescribePredefinedAttribute keeps each attribute's summary row and
// warns once.
func TestConnectPredefinedAttributes_DeniedDescribeKeepsSummary(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:DescribePredefinedAttribute")
	h := map[string]func(any) (any, error){
		"ListPredefinedAttributes": func(any) (any, error) {
			return &connect.ListPredefinedAttributesOutput{PredefinedAttributeSummaryList: []cttypes.PredefinedAttributeSummary{
				{Name: ptrStr("skill")}, {Name: ptrStr("tier")},
			}}, nil
		},
	}
	total, _, err := scanConnectPredefinedAttributes(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total = %d, err = %v; want 2, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeConnectPredefinedAttribute)[connectTestInstanceARN+"/predefined-attribute/skill"]; !ok {
		t.Error("skill not stored as its summary row")
	}
	if *warned != 1 {
		t.Errorf("warnings = %d, want 1", *warned)
	}
}

// A denied email-address search on several instances warns once and keeps
// the addresses found on the others.
func TestConnectEmailAddresses_DeniedSearchWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "connect:SearchEmailAddresses")
	arn := connectTestInstanceARN + "/email-address/e-1"
	h := map[string]func(any) (any, error){
		"SearchEmailAddresses": func(in any) (any, error) {
			if sv(in.(*connect.SearchEmailAddressesInput).InstanceId) != "i-2" {
				return nil, connectDenied
			}
			return &connect.SearchEmailAddressesOutput{EmailAddresses: []cttypes.EmailAddressMetadata{
				{EmailAddressId: ptrStr("e-1"), EmailAddressArn: ptrStr(arn), EmailAddress: ptrStr("a@example.com")},
			}}, nil
		},
	}
	total, _, err := scanConnectEmailAddresses(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectInstances("i-1", "i-2", "i-3"), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total = %d, err = %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeConnectEmailAddress)[arn]; !ok {
		t.Error("e-1 not stored")
	}
	if *warned != 1 {
		t.Errorf("warnings = %d, want 1", *warned)
	}
}

// A repeated NextToken keeps the rows of every page read and is returned.
func TestConnectTokenPager_RepeatedTokenKeepsRows(t *testing.T) {
	st := newTestStore(t)
	arn := func(id string) string { return connectTestInstanceARN + "/notification/" + id }
	h := map[string]func(any) (any, error){
		"ListNotifications": func(in any) (any, error) {
			id := "n-1"
			if in.(*connect.ListNotificationsInput).NextToken != nil {
				id = "n-2"
			}
			return &connect.ListNotificationsOutput{
				NotificationSummaryList: []cttypes.Notification{{Id: ptrStr(id), Arn: ptrStr(arn(id))}},
				NextToken:               ptrStr("same"),
			}, nil
		},
	}
	total, _, err := scanConnectNotifications(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v, want errRepeatedNextToken", err)
	}
	rows := rowsByNativeID(t, st, TypeConnectNotification)
	if _, ok := rows[arn("n-2")]; !ok || total != 2 {
		t.Errorf("total = %d, rows %v; want n-1 and n-2", total, rows)
	}
}

// A ListInstances page that fails after the first keeps the instances read:
// a denial warns, any other error is returned with them.
func TestListConnectInstances_LaterPageFailureKeepsInstances(t *testing.T) {
	for name, tc := range map[string]struct {
		fail     error
		wantCode string
		warnings int
	}{
		"denied":     {connectDenied, "", 1},
		"hard error": {connectHardErr, "UnrecognizedFailureException", 0},
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "connect:ListInstances")
			h := map[string]func(any) (any, error){
				"ListInstances": func(in any) (any, error) {
					if in.(*connect.ListInstancesInput).NextToken != nil {
						return nil, tc.fail
					}
					return &connect.ListInstancesOutput{InstanceSummaryList: connectInstances("i-1"), NextToken: ptrStr("t")}, nil
				},
			}
			got, err := listConnectInstances(context.Background(), connect.NewFromConfig(connectStubConfig(h)), newTestAccount(testAccountID), testRegion, st)
			if tc.wantCode == "" {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if len(got) != 1 || *warned != tc.warnings {
				t.Errorf("instances = %d, warnings = %d; want 1 and %d", len(got), *warned, tc.warnings)
			}
		})
	}
}

// A family that fails does not cancel the others: the users family, still
// listing when the routing family fails, stores its users.
func TestScanConnectFamilies_FailedFamilyDoesNotCancelOthers(t *testing.T) {
	users := connectCase(t, TypeConnectUser)
	h := map[string]func(context.Context, any) (any, error){
		"ListQueues": func(context.Context, any) (any, error) { return nil, connectHardErr },
		"ListUsers": func(ctx context.Context, _ any) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return users.list([]connectTestListed{{"x-1", users.arn("x-1")}}), nil
			}
		},
	}
	st := newTestStore(t)
	_, _, err := scanConnectFamilies(context.Background(), connect.NewFromConfig(opStubConfigCtx(h, connectDenied)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID)
	assertAPICode(t, err, "UnrecognizedFailureException")
	if _, ok := rowsByNativeID(t, st, TypeConnectUser)[users.arn("x-1")]; !ok {
		t.Error("users family lost its user after the routing family failed")
	}
}

// Each instance-level listing asks for its documented page cap, so an
// instance with many resources costs the fewest calls.
func TestScanConnectFamilies_ListingsAskForThePageCap(t *testing.T) {
	want := map[string]int32{
		"ListTrafficDistributionGroups": 10, "ListInstanceStorageConfigs": 10,
		"ListPhoneNumbersV2": 1000, "ListQueues": 1000, "ListRoutingProfiles": 1000, "ListHoursOfOperations": 1000,
		"ListAgentStatuses": 1000, "ListQuickConnects": 1000, "ListUsers": 1000, "ListUserHierarchyGroups": 1000,
		"ListSecurityProfiles": 1000, "ListContactFlows": 1000, "ListContactFlowModules": 1000, "ListPrompts": 1000,
		"ListAuthenticationProfiles": 1000,
		"ListRules":                  200,
		"ListPredefinedAttributes":   100, "ListEvaluationForms": 100, "ListViews": 100, "ListWorkspaces": 100,
		"ListNotifications": 100, "SearchEmailAddresses": 100, "ListIntegrationAssociations": 100, "SearchVocabularies": 100,
		"ListApprovedOrigins": 25, "ListSecurityKeys": 2,
	}
	var (
		mu  sync.Mutex
		got = map[string]int32{}
	)
	h := map[string]func(any) (any, error){}
	for op := range want {
		h[op] = func(in any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			if mr, ok := reflect.ValueOf(in).Elem().FieldByName("MaxResults").Interface().(*int32); ok && mr != nil {
				got[op] = *mr
			}
			return nil, connectDenied
		}
	}
	st := newTestStore(t)
	if _, _, err := scanConnectFamilies(context.Background(), connect.NewFromConfig(connectStubConfig(h)), connectTestInstanceList, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	for op, pageCap := range want {
		if got[op] != pageCap {
			t.Errorf("%s MaxResults = %d, want %d", op, got[op], pageCap)
		}
	}
}

// A failed ListInstances page is returned by scanConnect, after the families
// have scanned the instances read before it.
func TestScanConnect_ListInstancesErrorReturned(t *testing.T) {
	acct := newTestAccount(testAccountID)
	acct.cfg = connectStubConfig(map[string]func(any) (any, error){
		"ListInstances": func(in any) (any, error) {
			if in.(*connect.ListInstancesInput).NextToken != nil {
				return nil, connectHardErr
			}
			return &connect.ListInstancesOutput{InstanceSummaryList: connectInstances("i-1"), NextToken: ptrStr("t")}, nil
		},
	})
	_, _, err := scanConnect(context.Background(), acct, testRegion, newTestStore(t), testScanID)
	assertAPICode(t, err, "UnrecognizedFailureException")
}

// A cancelled scan starts no Describe and returns the cancel, not a failure
// per resource.
func TestConnectDescribe_CancelledStartsNoDescribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	listed := &connectListing[cttypes.QueueSummary]{op: "connect:ListQueues"}
	for _, id := range []string{"x-1", "x-2"} {
		listed.items = append(listed.items, connectListed[cttypes.QueueSummary]{instanceID: "i-1", id: id, arn: connectTestInstanceARN + "/queue/" + id})
	}
	_, _, err := connectDescribe[cttypes.QueueSummary]{
		rtype: TypeConnectQueue, op: "connect:DescribeQueue", label: "connect queues",
		describe: func(context.Context, connectListed[cttypes.QueueSummary], *store.Resource) error {
			calls.Add(1)
			return nil
		},
		summary: func(*store.Resource, cttypes.QueueSummary) {},
	}.store(ctx, newTestStore(t), newTestAccount(testAccountID), testRegion, testScanID, listed)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Errorf("err = %v, Describe calls = %d; want context.Canceled and 0", err, calls.Load())
	}
}

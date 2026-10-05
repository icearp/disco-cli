package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	agenttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"

	"github.com/icearp/disco-cli/store"
)

// bedrockChildStub serves child-list pages keyed by "<op>/<parent>" and then by
// the request's NextToken ("" is the first page); a fixture links pages by
// setting NextToken. errs (same key) is returned instead of any page. Parent
// list ops return the agents / policies fields and are otherwise empty. The
// embedded bedrockAgentAPI nil-panics on any op a test did not expect.
type bedrockChildStub struct {
	bedrockAgentAPI

	agents       []agenttypes.AgentSummary
	aliasErr     error
	policies     []bedrocktypes.AutomatedReasoningPolicySummary
	customModels []bedrocktypes.CustomModelSummary
	pages        map[string]map[string]any
	errs         map[string]error

	mu    sync.Mutex
	calls []string // "<op>/<parent>/<version>" per first-page request
}

func (s *bedrockChildStub) serve(op, parent, version string, token *string) (any, error) {
	key := op + "/" + parent
	s.mu.Lock()
	if token == nil {
		s.calls = append(s.calls, key+"/"+version)
	}
	s.mu.Unlock()
	if err := s.errs[key]; err != nil {
		return nil, err
	}
	return s.pages[key][sdkaws.ToString(token)], nil
}

func (s *bedrockChildStub) ListAgentVersions(_ context.Context, in *bedrockagent.ListAgentVersionsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentVersionsOutput, error) {
	out, err := s.serve("ListAgentVersions", sdkaws.ToString(in.AgentId), "", in.NextToken)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &bedrockagent.ListAgentVersionsOutput{}, nil
	}
	return out.(*bedrockagent.ListAgentVersionsOutput), nil
}

func (s *bedrockChildStub) ListAgentActionGroups(_ context.Context, in *bedrockagent.ListAgentActionGroupsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentActionGroupsOutput, error) {
	out, err := s.serve("ListAgentActionGroups", sdkaws.ToString(in.AgentId), sdkaws.ToString(in.AgentVersion), in.NextToken)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &bedrockagent.ListAgentActionGroupsOutput{}, nil
	}
	return out.(*bedrockagent.ListAgentActionGroupsOutput), nil
}

func (s *bedrockChildStub) ListAgentKnowledgeBases(_ context.Context, in *bedrockagent.ListAgentKnowledgeBasesInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentKnowledgeBasesOutput, error) {
	out, err := s.serve("ListAgentKnowledgeBases", sdkaws.ToString(in.AgentId), sdkaws.ToString(in.AgentVersion), in.NextToken)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &bedrockagent.ListAgentKnowledgeBasesOutput{}, nil
	}
	return out.(*bedrockagent.ListAgentKnowledgeBasesOutput), nil
}

func (s *bedrockChildStub) ListAgentCollaborators(_ context.Context, in *bedrockagent.ListAgentCollaboratorsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentCollaboratorsOutput, error) {
	out, err := s.serve("ListAgentCollaborators", sdkaws.ToString(in.AgentId), sdkaws.ToString(in.AgentVersion), in.NextToken)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &bedrockagent.ListAgentCollaboratorsOutput{}, nil
	}
	return out.(*bedrockagent.ListAgentCollaboratorsOutput), nil
}

func (s *bedrockChildStub) ListAutomatedReasoningPolicyTestCases(_ context.Context, in *bedrock.ListAutomatedReasoningPolicyTestCasesInput, _ ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPolicyTestCasesOutput, error) {
	out, err := s.serve("ListAutomatedReasoningPolicyTestCases", sdkaws.ToString(in.PolicyArn), "", in.NextToken)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &bedrock.ListAutomatedReasoningPolicyTestCasesOutput{}, nil
	}
	return out.(*bedrock.ListAutomatedReasoningPolicyTestCasesOutput), nil
}

// Parent-family ops for the whole-family tests.

func (s *bedrockChildStub) ListAgents(_ context.Context, _ *bedrockagent.ListAgentsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentsOutput, error) {
	return &bedrockagent.ListAgentsOutput{AgentSummaries: s.agents}, nil
}

func (s *bedrockChildStub) ListAgentAliases(_ context.Context, _ *bedrockagent.ListAgentAliasesInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentAliasesOutput, error) {
	if s.aliasErr != nil {
		return nil, s.aliasErr
	}
	return &bedrockagent.ListAgentAliasesOutput{}, nil
}

func (s *bedrockChildStub) ListKnowledgeBases(_ context.Context, _ *bedrockagent.ListKnowledgeBasesInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListKnowledgeBasesOutput, error) {
	return &bedrockagent.ListKnowledgeBasesOutput{}, nil
}

func (s *bedrockChildStub) ListFlows(_ context.Context, _ *bedrockagent.ListFlowsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListFlowsOutput, error) {
	return &bedrockagent.ListFlowsOutput{}, nil
}

func (s *bedrockChildStub) ListPrompts(_ context.Context, _ *bedrockagent.ListPromptsInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.ListPromptsOutput, error) {
	return &bedrockagent.ListPromptsOutput{}, nil
}

func (s *bedrockChildStub) ListAutomatedReasoningPolicies(_ context.Context, in *bedrock.ListAutomatedReasoningPoliciesInput, _ ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPoliciesOutput, error) {
	if in.PolicyArn != nil {
		return &bedrock.ListAutomatedReasoningPoliciesOutput{}, nil
	}
	return &bedrock.ListAutomatedReasoningPoliciesOutput{AutomatedReasoningPolicySummaries: s.policies}, nil
}

func (s *bedrockChildStub) ListGuardrails(_ context.Context, _ *bedrock.ListGuardrailsInput, _ ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error) {
	return &bedrock.ListGuardrailsOutput{}, nil
}

func (s *bedrockChildStub) ListPromptRouters(_ context.Context, _ *bedrock.ListPromptRoutersInput, _ ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error) {
	return &bedrock.ListPromptRoutersOutput{}, nil
}

func (s *bedrockChildStub) ListInferenceProfiles(_ context.Context, _ *bedrock.ListInferenceProfilesInput, _ ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error) {
	return &bedrock.ListInferenceProfilesOutput{}, nil
}

func (s *bedrockChildStub) ListEnforcedGuardrailsConfiguration(_ context.Context, _ *bedrock.ListEnforcedGuardrailsConfigurationInput, _ ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error) {
	return &bedrock.ListEnforcedGuardrailsConfigurationOutput{}, nil
}

func (s *bedrockChildStub) ListFoundationModels(_ context.Context, _ *bedrock.ListFoundationModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error) {
	return &bedrock.ListFoundationModelsOutput{}, nil
}

func (s *bedrockChildStub) ListCustomModels(_ context.Context, _ *bedrock.ListCustomModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListCustomModelsOutput, error) {
	return &bedrock.ListCustomModelsOutput{ModelSummaries: s.customModels}, nil
}

func (s *bedrockChildStub) ListImportedModels(_ context.Context, _ *bedrock.ListImportedModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListImportedModelsOutput, error) {
	return &bedrock.ListImportedModelsOutput{}, nil
}

func (s *bedrockChildStub) ListProvisionedModelThroughputs(_ context.Context, _ *bedrock.ListProvisionedModelThroughputsInput, _ ...func(*bedrock.Options)) (*bedrock.ListProvisionedModelThroughputsOutput, error) {
	return &bedrock.ListProvisionedModelThroughputsOutput{}, nil
}

func (s *bedrockChildStub) ListCustomModelDeployments(_ context.Context, _ *bedrock.ListCustomModelDeploymentsInput, _ ...func(*bedrock.Options)) (*bedrock.ListCustomModelDeploymentsOutput, error) {
	return &bedrock.ListCustomModelDeploymentsOutput{}, nil
}

func (s *bedrockChildStub) ListMarketplaceModelEndpoints(_ context.Context, _ *bedrock.ListMarketplaceModelEndpointsInput, _ ...func(*bedrock.Options)) (*bedrock.ListMarketplaceModelEndpointsOutput, error) {
	return &bedrock.ListMarketplaceModelEndpointsOutput{}, nil
}

func (s *bedrockChildStub) ListBlueprints(_ context.Context, _ *bda.ListBlueprintsInput, _ ...func(*bda.Options)) (*bda.ListBlueprintsOutput, error) {
	return &bda.ListBlueprintsOutput{}, nil
}

func (s *bedrockChildStub) ListDataAutomationProjects(_ context.Context, _ *bda.ListDataAutomationProjectsInput, _ ...func(*bda.Options)) (*bda.ListDataAutomationProjectsOutput, error) {
	return &bda.ListDataAutomationProjectsOutput{}, nil
}

func (s *bedrockChildStub) ListDataAutomationLibraries(_ context.Context, _ *bda.ListDataAutomationLibrariesInput, _ ...func(*bda.Options)) (*bda.ListDataAutomationLibrariesOutput, error) {
	return &bda.ListDataAutomationLibrariesOutput{}, nil
}

const bedrockTestPolicyARN = "arn:aws:bedrock:us-east-1:123456789012:automated-reasoning-policy/pol1"

// bedrockChildCase describes one child scanner: how to build a page holding
// children with the given ids, and what row each child id must produce under a
// parent. Every fixture child is built with name "<id>-name" where the element
// carries a name.
type bedrockChildCase struct {
	name       string
	op         string
	typ        string
	scan       func(context.Context, *bedrockChildStub, *account, string, *store.Store, string, []string) (int, int, error)
	parents    []string // ids passed to scan
	parentType string
	// parentARN maps a parent id to the NativeID of the parent row.
	parentARN func(id string) string
	// version is the AgentVersion the op must be called with ("" = none).
	version string
	page    func(ids []string, next *string) any
	// want returns the expected NativeID, Name, Status and verbatim id field for a child.
	want func(parentARN, id string) (nativeID, name, status, idKey string)
}

func bedrockAgentTestARN(id string) string { return bedrockAgentARN(testRegion, testAccountID, id) }

func bedrockChildCases() []bedrockChildCase {
	agentScan := func(fn func(context.Context, bedrockAgentAPI, *account, string, *store.Store, string, []string) (int, int, error)) func(context.Context, *bedrockChildStub, *account, string, *store.Store, string, []string) (int, int, error) {
		return func(ctx context.Context, s *bedrockChildStub, a *account, r string, st *store.Store, id string, p []string) (int, int, error) {
			return fn(ctx, s, a, r, st, id, p)
		}
	}
	return []bedrockChildCase{
		{
			name: "AgentVersions", op: "ListAgentVersions", typ: TypeBedrockAgentVersion,
			scan:    agentScan(scanBedrockAgentVersions),
			parents: []string{"a1", "a2"}, parentType: TypeBedrockAgent, parentARN: bedrockAgentTestARN,
			page: func(ids []string, next *string) any {
				out := &bedrockagent.ListAgentVersionsOutput{NextToken: next}
				for _, id := range ids {
					out.AgentVersionSummaries = append(out.AgentVersionSummaries, agenttypes.AgentVersionSummary{
						AgentVersion: sdkaws.String(id), AgentName: sdkaws.String(id + "-name"), AgentStatus: agenttypes.AgentStatusPrepared,
					})
				}
				return out
			},
			want: func(p, id string) (string, string, string, string) {
				return p + "/version/" + id, id, "PREPARED", "AgentVersion"
			},
		},
		{
			name: "ActionGroups", op: "ListAgentActionGroups", typ: TypeBedrockAgentActionGroup,
			scan:    agentScan(scanBedrockAgentActionGroups),
			parents: []string{"a1", "a2"}, parentType: TypeBedrockAgent, parentARN: bedrockAgentTestARN, version: "DRAFT",
			page: func(ids []string, next *string) any {
				out := &bedrockagent.ListAgentActionGroupsOutput{NextToken: next}
				for _, id := range ids {
					out.ActionGroupSummaries = append(out.ActionGroupSummaries, agenttypes.ActionGroupSummary{
						ActionGroupId: sdkaws.String(id), ActionGroupName: sdkaws.String(id + "-name"), ActionGroupState: agenttypes.ActionGroupStateEnabled,
					})
				}
				return out
			},
			want: func(p, id string) (string, string, string, string) {
				return p + "/action-group/" + id, id + "-name", "ENABLED", "ActionGroupId"
			},
		},
		{
			name: "KnowledgeBases", op: "ListAgentKnowledgeBases", typ: TypeBedrockAgentKnowledgeBase,
			scan:    agentScan(scanBedrockAgentKnowledgeBases),
			parents: []string{"a1", "a2"}, parentType: TypeBedrockAgent, parentARN: bedrockAgentTestARN, version: "DRAFT",
			page: func(ids []string, next *string) any {
				out := &bedrockagent.ListAgentKnowledgeBasesOutput{NextToken: next}
				for _, id := range ids {
					out.AgentKnowledgeBaseSummaries = append(out.AgentKnowledgeBaseSummaries, agenttypes.AgentKnowledgeBaseSummary{
						KnowledgeBaseId: sdkaws.String(id), KnowledgeBaseState: agenttypes.KnowledgeBaseStateEnabled,
					})
				}
				return out
			},
			want: func(p, id string) (string, string, string, string) {
				return p + "/knowledge-base/" + id, id, "ENABLED", "KnowledgeBaseId"
			},
		},
		{
			name: "Collaborators", op: "ListAgentCollaborators", typ: TypeBedrockAgentCollaborator,
			scan:    agentScan(scanBedrockAgentCollaborators),
			parents: []string{"a1", "a2"}, parentType: TypeBedrockAgent, parentARN: bedrockAgentTestARN, version: "DRAFT",
			page: func(ids []string, next *string) any {
				out := &bedrockagent.ListAgentCollaboratorsOutput{NextToken: next}
				for _, id := range ids {
					out.AgentCollaboratorSummaries = append(out.AgentCollaboratorSummaries, agenttypes.AgentCollaboratorSummary{
						CollaboratorId: sdkaws.String(id), CollaboratorName: sdkaws.String(id + "-name"),
					})
				}
				return out
			},
			want: func(p, id string) (string, string, string, string) {
				return p + "/collaborator/" + id, id + "-name", "", "CollaboratorId"
			},
		},
		{
			name: "ARPolicyTestCases", op: "ListAutomatedReasoningPolicyTestCases", typ: TypeBedrockAutomatedReasoningPolicyTestCase,
			scan: func(ctx context.Context, s *bedrockChildStub, a *account, r string, st *store.Store, id string, p []string) (int, int, error) {
				return scanBedrockARPolicyTestCases(ctx, s, a, r, st, id, p)
			},
			parents:    []string{bedrockTestPolicyARN, bedrockTestPolicyARN + "2"},
			parentType: TypeBedrockAutomatedReasoningPolicy,
			parentARN:  func(id string) string { return id },
			page: func(ids []string, next *string) any {
				out := &bedrock.ListAutomatedReasoningPolicyTestCasesOutput{NextToken: next}
				for _, id := range ids {
					out.TestCases = append(out.TestCases, bedrocktypes.AutomatedReasoningPolicyTestCase{
						TestCaseId: sdkaws.String(id), GuardContent: sdkaws.String("guard"),
					})
				}
				return out
			},
			want: func(p, id string) (string, string, string, string) {
				return p + "/test-case/" + id, id, "", "TestCaseId"
			},
		},
	}
}

// seedBedrockParents stores the parent rows so the contains closure can link
// children to them, as the parent phase does in production.
func seedBedrockParents(t *testing.T, st *store.Store, c bedrockChildCase) {
	t.Helper()
	for _, p := range c.parents {
		upsertTestResource(t, st, "aws", testAccountID, c.parentType, c.parentARN(p), testRegion, "{}")
	}
}

func runBedrockChild(t *testing.T, c bedrockChildCase, stub *bedrockChildStub) (int, map[string]store.Resource, []store.ScanWarning, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	seedBedrockParents(t, st, c)
	var (
		mu    sync.Mutex
		warns []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns = append(warns, w)
		mu.Unlock()
	}
	total, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, c.parents)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return total, listNetExtraRows(t, st, c.typ), warns, st
}

func assertBedrockChild(t *testing.T, st *store.Store, rows map[string]store.Resource, c bedrockChildCase, parent, id string) {
	t.Helper()
	parentARN := c.parentARN(parent)
	nativeID, name, status, idKey := c.want(parentARN, id)
	r, ok := rows[nativeID]
	if !ok {
		t.Errorf("missing row %s; have %d rows", nativeID, len(rows))
		return
	}
	if r.Type != c.typ {
		t.Errorf("%s: Type = %q; want %q", nativeID, r.Type, c.typ)
	}
	if got := sdkaws.ToString(r.Region); got != testRegion {
		t.Errorf("%s: Region = %q; want %q", nativeID, got, testRegion)
	}
	if got := sdkaws.ToString(r.Name); got != name {
		t.Errorf("%s: Name = %q; want %q", nativeID, got, name)
	}
	if status == "" && r.Status != nil {
		t.Errorf("%s: Status = %q; want unset", nativeID, *r.Status)
	}
	if got := sdkaws.ToString(r.Status); got != status {
		t.Errorf("%s: Status = %q; want %q", nativeID, got, status)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s: AttributesJSON does not decode: %v", nativeID, err)
	}
	if got := attrs[idKey]; got != id {
		t.Errorf("%s: attributes[%q] = %v; want %q (the SDK element verbatim)", nativeID, idKey, got, id)
	}
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, parentARN), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if !slices.ContainsFunc(rels, func(rel store.Relationship) bool { return rel.ToID == r.ID }) {
		t.Errorf("%s: no contains edge from parent %s", nativeID, parentARN)
	}
}

func TestBedrockChildScanners_PaginatesEveryParent(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2 := c.parents[0], c.parents[1]
			stub := &bedrockChildStub{pages: map[string]map[string]any{
				c.op + "/" + p1: {"": c.page([]string{"1"}, sdkaws.String("t2")), "t2": c.page([]string{"2", ""}, nil)},
				c.op + "/" + p2: {"": c.page([]string{"3"}, nil)},
			}}
			total, rows, _, st := runBedrockChild(t, c, stub)
			if total != 3 || len(rows) != 3 {
				t.Fatalf("total = %d, rows = %d; want 3, 3 (the id-less child is skipped)", total, len(rows))
			}
			assertBedrockChild(t, st, rows, c, p1, "1")
			assertBedrockChild(t, st, rows, c, p1, "2")
			assertBedrockChild(t, st, rows, c, p2, "3")
			want := []string{c.op + "/" + p1 + "/" + c.version, c.op + "/" + p2 + "/" + c.version}
			got := slices.Sorted(slices.Values(stub.calls))
			if !slices.Equal(got, want) {
				t.Errorf("first-page calls = %v; want %v (every parent, at version %q)", got, want, c.version)
			}
		})
	}
}

func TestBedrockChildScanners_Empty(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			total, rows, warns, _ := runBedrockChild(t, c, &bedrockChildStub{})
			if total != 0 || len(rows) != 0 || len(warns) != 0 {
				t.Errorf("total = %d, rows = %d, warns = %d; want 0, 0, 0", total, len(rows), len(warns))
			}
		})
	}
}

func TestBedrockChildScanners_NoParentsMakesNoCalls(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			stub := &bedrockChildStub{}
			c.parents = nil
			if total, _, _, _ := runBedrockChild(t, c, stub); total != 0 {
				t.Errorf("total = %d; want 0", total)
			}
			if len(stub.calls) != 0 {
				t.Errorf("calls = %v; want none", stub.calls)
			}
		})
	}
}

func TestBedrockChildScanners_ParentNotFoundSkipped(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2 := c.parents[0], c.parents[1]
			stub := &bedrockChildStub{
				errs:  map[string]error{c.op + "/" + p1: apiErr("ResourceNotFoundException", "gone")},
				pages: map[string]map[string]any{c.op + "/" + p2: {"": c.page([]string{"3"}, nil)}},
			}
			total, rows, warns, st := runBedrockChild(t, c, stub)
			if total != 1 || len(rows) != 1 || len(warns) != 0 {
				t.Fatalf("total = %d, rows = %d, warns = %d; want 1, 1, 0", total, len(rows), len(warns))
			}
			assertBedrockChild(t, st, rows, c, p2, "3")
		})
	}
}

func TestBedrockChildScanners_AccessDeniedWarnsOnceAndContinues(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2 := c.parents[0], c.parents[1]
			p3 := p2 + "x"
			c.parents = append(c.parents, p3)
			deny := apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/u is not authorized to perform: x")
			stub := &bedrockChildStub{
				errs:  map[string]error{c.op + "/" + p1: deny, c.op + "/" + p2: deny},
				pages: map[string]map[string]any{c.op + "/" + p3: {"": c.page([]string{"3"}, nil)}},
			}
			total, rows, warns, st := runBedrockChild(t, c, stub)
			if total != 1 || len(rows) != 1 {
				t.Fatalf("total = %d, rows = %d; want 1, 1 (the readable sibling)", total, len(rows))
			}
			assertBedrockChild(t, st, rows, c, p3, "3")
			prefix := "bedrock:"
			if c.typ != TypeBedrockAutomatedReasoningPolicyTestCase {
				prefix = "bedrockagent:"
			}
			if len(warns) != 1 || warns[0].Service != prefix+c.op {
				t.Errorf("warnings = %+v; want exactly one for %s%s", warns, prefix, c.op)
			}
		})
	}
}

// Denials no IAM change fixes — the AR feature gate's canned 403, the
// empty-message closed-account 403 and an SCP explicit deny — skip the parent
// without a warning, and the readable sibling is still stored.
func TestBedrockChildScanners_SilentDenialsSkipped(t *testing.T) {
	denials := map[string]error{
		"SCPExplicitDeny": apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/u is not authorized to perform: x with an explicit deny in a service control policy"),
		"FeatureGate":     apiErr("AccessDeniedException", "Your account is not authorized to invoke this API operation."),
		"ClosedAccount":   apiErr("AccessDeniedException", ""),
	}
	for _, c := range bedrockChildCases() {
		for dname, deny := range denials {
			t.Run(c.name+"/"+dname, func(t *testing.T) {
				p1, p2 := c.parents[0], c.parents[1]
				stub := &bedrockChildStub{
					errs:  map[string]error{c.op + "/" + p1: deny},
					pages: map[string]map[string]any{c.op + "/" + p2: {"": c.page([]string{"3"}, nil)}},
				}
				total, rows, warns, st := runBedrockChild(t, c, stub)
				if total != 1 || len(rows) != 1 || len(warns) != 0 {
					t.Fatalf("total = %d, rows = %d, warns = %+v; want 1, 1, none", total, len(rows), warns)
				}
				assertBedrockChild(t, st, rows, c, p2, "3")
			})
		}
	}
}

func TestBedrockChildScanners_OtherErrorPropagates(t *testing.T) {
	for _, c := range bedrockChildCases() {
		t.Run(c.name, func(t *testing.T) {
			stub := &bedrockChildStub{errs: map[string]error{c.op + "/" + c.parents[0]: apiErr("ValidationException", "bad")}}
			st := newTestStore(t)
			_, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, c.parents)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the ValidationException propagated", err)
			}
		})
	}
}

// DRAFT is the agent's working copy, already represented by the agent row.
func TestScanBedrockAgentVersions_SkipsDraft(t *testing.T) {
	c := bedrockChildCases()[0]
	stub := &bedrockChildStub{pages: map[string]map[string]any{
		c.op + "/a1": {"": c.page([]string{"DRAFT", "1"}, nil)},
	}}
	c.parents = []string{"a1"}
	total, rows, _, st := runBedrockChild(t, c, stub)
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, rows = %d; want 1, 1", total, len(rows))
	}
	assertBedrockChild(t, st, rows, c, "a1", "1")
}

// bedrockFamilyStub returns one agent, one AR policy and one custom model,
// with one child of each child type under its parent.
func bedrockFamilyStub() *bedrockChildStub {
	stub := &bedrockChildStub{
		agents:       []agenttypes.AgentSummary{{AgentId: sdkaws.String("a1"), AgentName: sdkaws.String("agent")}},
		policies:     []bedrocktypes.AutomatedReasoningPolicySummary{{PolicyArn: sdkaws.String(bedrockTestPolicyARN), Name: sdkaws.String("pol")}},
		customModels: []bedrocktypes.CustomModelSummary{{ModelArn: sdkaws.String("arn:aws:bedrock:us-east-1:123456789012:custom-model/m1")}},
		pages:        map[string]map[string]any{},
	}
	for _, c := range bedrockChildCases() {
		stub.pages[c.op+"/"+c.parents[0]] = map[string]any{"": c.page([]string{"1"}, nil)}
	}
	return stub
}

func runBedrockFamily(t *testing.T, stub *bedrockChildStub) (int, *store.Store, error) {
	t.Helper()
	st := newTestStore(t)
	total, _, err := scanBedrockClients(context.Background(), stub, stub, stub, stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, st, err
}

// Parents are listed before their children are fanned out to.
func TestScanBedrockClients_ProducesChildren(t *testing.T) {
	total, st, err := runBedrockFamily(t, bedrockFamilyStub())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range bedrockChildCases() {
		rows := listNetExtraRows(t, st, c.typ)
		if len(rows) != 1 {
			t.Errorf("%s: rows = %d; want 1", c.typ, len(rows))
			continue
		}
		assertBedrockChild(t, st, rows, c, c.parents[0], "1")
	}
	// agent + policy + custom model + one child of each of the five types.
	if total != 8 {
		t.Errorf("total = %d; want 8", total)
	}
}

// A failing child phase must not cost the parent-level phases (Models runs
// before it), and the totals already stored are still reported.
func TestScanBedrockClients_ChildErrorKeepsParentPhases(t *testing.T) {
	stub := bedrockFamilyStub()
	stub.errs = map[string]error{"ListAgentVersions/a1": apiErr("ValidationException", "bad")}
	total, st, err := runBedrockFamily(t, stub)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the ValidationException propagated", err)
	}
	if rows := listNetExtraRows(t, st, TypeBedrockCustomModel); len(rows) != 1 {
		t.Errorf("custom-model rows = %d; want 1 (Models runs before the child phases)", len(rows))
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (agent, policy, custom model)", total)
	}
}

// A mid-family agents failure reports the rows that phase already stored.
func TestScanBedrockClients_AgentsErrorKeepsPartialTotals(t *testing.T) {
	stub := bedrockFamilyStub()
	stub.aliasErr = apiErr("ValidationException", "bad")
	total, _, err := runBedrockFamily(t, stub)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the ValidationException propagated", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (policy, agent)", total)
	}
}

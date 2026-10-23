package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/icearp/disco-cli/store"
)

// stubBatch is an in-memory batchAPI: each op answers through its handler,
// and an unset handler lists nothing. Every call is counted per op;
// ListQuotaShares is called concurrently, so the count is guarded.
type stubBatch struct {
	computeEnvs func(*batch.DescribeComputeEnvironmentsInput) (*batch.DescribeComputeEnvironmentsOutput, error)
	jobQueues   func(*batch.DescribeJobQueuesInput) (*batch.DescribeJobQueuesOutput, error)
	jobDefs     func(*batch.DescribeJobDefinitionsInput) (*batch.DescribeJobDefinitionsOutput, error)
	listSched   func(*batch.ListSchedulingPoliciesInput) (*batch.ListSchedulingPoliciesOutput, error)
	descSched   func(*batch.DescribeSchedulingPoliciesInput) (*batch.DescribeSchedulingPoliciesOutput, error)
	consumables func(*batch.ListConsumableResourcesInput) (*batch.ListConsumableResourcesOutput, error)
	serviceEnvs func(*batch.DescribeServiceEnvironmentsInput) (*batch.DescribeServiceEnvironmentsOutput, error)
	quotaShares func(*batch.ListQuotaSharesInput) (*batch.ListQuotaSharesOutput, error)

	mu    sync.Mutex
	calls map[string]int
}

// batchCall counts the call to op, then answers it through h, or with an
// empty out when h is unset.
func batchCall[In, Out any](s *stubBatch, op string, h func(In) (Out, error), in In, empty Out) (Out, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[op]++
	s.mu.Unlock()
	if h == nil {
		return empty, nil
	}
	return h(in)
}

func (s *stubBatch) DescribeComputeEnvironments(_ context.Context, in *batch.DescribeComputeEnvironmentsInput, _ ...func(*batch.Options)) (*batch.DescribeComputeEnvironmentsOutput, error) {
	return batchCall(s, "DescribeComputeEnvironments", s.computeEnvs, in, &batch.DescribeComputeEnvironmentsOutput{})
}

func (s *stubBatch) DescribeJobQueues(_ context.Context, in *batch.DescribeJobQueuesInput, _ ...func(*batch.Options)) (*batch.DescribeJobQueuesOutput, error) {
	return batchCall(s, "DescribeJobQueues", s.jobQueues, in, &batch.DescribeJobQueuesOutput{})
}

func (s *stubBatch) DescribeJobDefinitions(_ context.Context, in *batch.DescribeJobDefinitionsInput, _ ...func(*batch.Options)) (*batch.DescribeJobDefinitionsOutput, error) {
	return batchCall(s, "DescribeJobDefinitions", s.jobDefs, in, &batch.DescribeJobDefinitionsOutput{})
}

func (s *stubBatch) ListSchedulingPolicies(_ context.Context, in *batch.ListSchedulingPoliciesInput, _ ...func(*batch.Options)) (*batch.ListSchedulingPoliciesOutput, error) {
	return batchCall(s, "ListSchedulingPolicies", s.listSched, in, &batch.ListSchedulingPoliciesOutput{})
}

func (s *stubBatch) DescribeSchedulingPolicies(_ context.Context, in *batch.DescribeSchedulingPoliciesInput, _ ...func(*batch.Options)) (*batch.DescribeSchedulingPoliciesOutput, error) {
	return batchCall(s, "DescribeSchedulingPolicies", s.descSched, in, &batch.DescribeSchedulingPoliciesOutput{})
}

func (s *stubBatch) ListConsumableResources(_ context.Context, in *batch.ListConsumableResourcesInput, _ ...func(*batch.Options)) (*batch.ListConsumableResourcesOutput, error) {
	return batchCall(s, "ListConsumableResources", s.consumables, in, &batch.ListConsumableResourcesOutput{})
}

func (s *stubBatch) DescribeServiceEnvironments(_ context.Context, in *batch.DescribeServiceEnvironmentsInput, _ ...func(*batch.Options)) (*batch.DescribeServiceEnvironmentsOutput, error) {
	return batchCall(s, "DescribeServiceEnvironments", s.serviceEnvs, in, &batch.DescribeServiceEnvironmentsOutput{})
}

func (s *stubBatch) ListQuotaShares(_ context.Context, in *batch.ListQuotaSharesInput, _ ...func(*batch.Options)) (*batch.ListQuotaSharesOutput, error) {
	return batchCall(s, "ListQuotaShares", s.quotaShares, in, &batch.ListQuotaSharesOutput{})
}

func (s *stubBatch) callCount(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[op]
}

var (
	batchDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: batch:X")
	batchClientErr = apiErr("ClientException", "bad request")
	batchThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

// batchPage is one page of a listing: its elements, the NextToken it
// returns ("" for none) and, instead of both, an error.
type batchPage struct {
	items []any
	next  string
	err   error
}

// batchPaged answers a paginated op from pages, keyed by the request's
// NextToken ("" for the first call). A token with no page, or a MaxResults
// other than the model cap, fails with ClientException, so the request is
// pinned. req reads the request's MaxResults and NextToken; build makes the
// output from a page's elements and next token.
func batchPaged[In, Out any](pages map[string]batchPage, req func(In) (*int32, *string), build func(items []any, next *string) Out) func(In) (Out, error) {
	return func(in In) (Out, error) {
		var zero Out
		maxResults, token := req(in)
		if sdkaws.ToInt32(maxResults) != batchPageLimit {
			return zero, apiErr("ClientException", "maxResults must be 100")
		}
		p, ok := pages[sdkaws.ToString(token)]
		if !ok {
			return zero, apiErr("ClientException", "unknown nextToken "+sdkaws.ToString(token))
		}
		if p.err != nil {
			return zero, p.err
		}
		var next *string
		if p.next != "" {
			next = &p.next
		}
		return build(p.items, next), nil
	}
}

func batchItems[T any](items []any) []T {
	out := make([]T, len(items))
	for i, it := range items {
		out[i] = it.(T)
	}
	return out
}

// batchListCase is one of the Batch types stored from one account-wide
// listing: elem builds an element (name and status left unset when empty;
// status and tags are dropped for a type whose element has none), install
// answers the type's op from pages.
type batchListCase struct {
	op, typ            string
	hasStatus, hasTags bool
	elem               func(arn, name, status string, tags map[string]string) any
	install            func(s *stubBatch, pages map[string]batchPage)
	scan               func(context.Context, batchAPI, *account, string, *store.Store, string) (int, int, error)
}

func batchListCases() []batchListCase {
	return []batchListCase{
		{
			op: "batch:DescribeComputeEnvironments", typ: TypeBatchComputeEnvironment, hasStatus: true, hasTags: true, scan: scanBatchComputeEnvironments,
			elem: func(arn, name, status string, tags map[string]string) any {
				return batchtypes.ComputeEnvironmentDetail{
					ComputeEnvironmentArn: nonEmptyPtr(arn), ComputeEnvironmentName: nonEmptyPtr(name),
					State: batchtypes.CEState(status), ServiceRole: sdkaws.String("arn:aws:iam::123456789012:role/svc"), Tags: tags,
				}
			},
			install: func(s *stubBatch, pages map[string]batchPage) {
				s.computeEnvs = batchPaged(pages,
					func(in *batch.DescribeComputeEnvironmentsInput) (*int32, *string) { return in.MaxResults, in.NextToken },
					func(items []any, next *string) *batch.DescribeComputeEnvironmentsOutput {
						return &batch.DescribeComputeEnvironmentsOutput{ComputeEnvironments: batchItems[batchtypes.ComputeEnvironmentDetail](items), NextToken: next}
					})
			},
		},
		{
			op: "batch:DescribeJobQueues", typ: TypeBatchJobQueue, hasStatus: true, hasTags: true,
			scan: func(ctx context.Context, c batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
				_, t, i, err := scanBatchJobQueues(ctx, c, acct, region, st, scanID)
				return t, i, err
			},
			elem: func(arn, name, status string, tags map[string]string) any {
				return batchtypes.JobQueueDetail{
					JobQueueArn: nonEmptyPtr(arn), JobQueueName: nonEmptyPtr(name),
					State: batchtypes.JQState(status), Priority: sdkaws.Int32(7), Tags: tags,
				}
			},
			install: func(s *stubBatch, pages map[string]batchPage) {
				s.jobQueues = batchPaged(pages,
					func(in *batch.DescribeJobQueuesInput) (*int32, *string) { return in.MaxResults, in.NextToken },
					func(items []any, next *string) *batch.DescribeJobQueuesOutput {
						return &batch.DescribeJobQueuesOutput{JobQueues: batchItems[batchtypes.JobQueueDetail](items), NextToken: next}
					})
			},
		},
		{
			op: "batch:DescribeJobDefinitions", typ: TypeBatchJobDefinition, hasStatus: true, hasTags: true, scan: scanBatchJobDefinitions,
			elem: func(arn, name, status string, tags map[string]string) any {
				return batchtypes.JobDefinition{
					JobDefinitionArn: nonEmptyPtr(arn), JobDefinitionName: nonEmptyPtr(name),
					Status: nonEmptyPtr(status), Revision: sdkaws.Int32(3), Type: sdkaws.String("container"), Tags: tags,
				}
			},
			install: func(s *stubBatch, pages map[string]batchPage) {
				paged := batchPaged(pages,
					func(in *batch.DescribeJobDefinitionsInput) (*int32, *string) { return in.MaxResults, in.NextToken },
					func(items []any, next *string) *batch.DescribeJobDefinitionsOutput {
						return &batch.DescribeJobDefinitionsOutput{JobDefinitions: batchItems[batchtypes.JobDefinition](items), NextToken: next}
					})
				s.jobDefs = func(in *batch.DescribeJobDefinitionsInput) (*batch.DescribeJobDefinitionsOutput, error) {
					if sdkaws.ToString(in.Status) != "ACTIVE" {
						return nil, apiErr("ClientException", "want status ACTIVE")
					}
					return paged(in)
				}
			},
		},
		{
			op: "batch:ListConsumableResources", typ: TypeBatchConsumableResource, scan: scanBatchConsumableResources,
			elem: func(arn, name, _ string, _ map[string]string) any {
				return batchtypes.ConsumableResourceSummary{
					ConsumableResourceArn: nonEmptyPtr(arn), ConsumableResourceName: nonEmptyPtr(name),
					ResourceType: sdkaws.String("REPLENISHABLE"), TotalQuantity: sdkaws.Int64(5),
				}
			},
			install: func(s *stubBatch, pages map[string]batchPage) {
				s.consumables = batchPaged(pages,
					func(in *batch.ListConsumableResourcesInput) (*int32, *string) { return in.MaxResults, in.NextToken },
					func(items []any, next *string) *batch.ListConsumableResourcesOutput {
						return &batch.ListConsumableResourcesOutput{ConsumableResources: batchItems[batchtypes.ConsumableResourceSummary](items), NextToken: next}
					})
			},
		},
		{
			op: "batch:DescribeServiceEnvironments", typ: TypeBatchServiceEnvironment, hasStatus: true, hasTags: true, scan: scanBatchServiceEnvironments,
			elem: func(arn, name, status string, tags map[string]string) any {
				return batchtypes.ServiceEnvironmentDetail{
					ServiceEnvironmentArn: nonEmptyPtr(arn), ServiceEnvironmentName: nonEmptyPtr(name),
					State: batchtypes.ServiceEnvironmentState(status), ServiceEnvironmentType: batchtypes.ServiceEnvironmentTypeSagemakerTraining, Tags: tags,
				}
			},
			install: func(s *stubBatch, pages map[string]batchPage) {
				s.serviceEnvs = batchPaged(pages,
					func(in *batch.DescribeServiceEnvironmentsInput) (*int32, *string) { return in.MaxResults, in.NextToken },
					func(items []any, next *string) *batch.DescribeServiceEnvironmentsOutput {
						return &batch.DescribeServiceEnvironmentsOutput{ServiceEnvironments: batchItems[batchtypes.ServiceEnvironmentDetail](items), NextToken: next}
					})
			},
		},
	}
}

func batchTestARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:batch:%s:%s:%s/%s", testRegion, testAccountID, kind, name)
}

// Each listed type stores the elements of every page under their ARN, with
// name, status, the element verbatim and its tags; an element without an
// ARN is warned about once and not stored.
func TestScanBatchListedTypes_StoresPagesWithTags(t *testing.T) {
	for _, c := range batchListCases() {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, c.op)
			tagged := c.elem(batchTestARN("x", "tagged"), "tagged", "ENABLED", map[string]string{"team": "data", "env": "prod"})
			bare := c.elem(batchTestARN("x", "bare"), "", "", nil)
			stub := &stubBatch{}
			c.install(stub, map[string]batchPage{
				"":   {items: []any{tagged, c.elem("", "no-arn", "ENABLED", nil)}, next: "p2"},
				"p2": {items: []any{bare}},
			})
			total, inserted, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d; want 1 for the element without an ARN", *warns)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if len(rows) != 2 {
				t.Fatalf("rows = %d; want 2", len(rows))
			}
			r, ok := rows[batchTestARN("x", "tagged")]
			if !ok {
				t.Fatalf("tagged element not stored under its ARN; got %v", rows)
			}
			wantStatus := ""
			if c.hasStatus {
				wantStatus = "ENABLED"
			}
			if sv(r.Name) != "tagged" || sv(r.Status) != wantStatus || (r.Status == nil) == c.hasStatus || sv(r.Region) != testRegion {
				t.Errorf("row = name %q status %v region %q; want tagged/%q/%s", sv(r.Name), r.Status, sv(r.Region), wantStatus, testRegion)
			}
			assertJSONEqual(t, "attributes", r.AttributesJSON, mustJSON(tagged))
			if c.hasTags {
				assertTagsMap(t, r.TagsJSON, map[string]string{"team": "data", "env": "prod"})
			} else if r.TagsJSON != nil {
				t.Errorf("tags = %s; want none (the element carries no tags)", *r.TagsJSON)
			}
			b := rows[batchTestARN("x", "bare")]
			if b.Name != nil || b.Status != nil || b.TagsJSON != nil {
				t.Errorf("bare row = name %v status %v tags %v; want all unset", b.Name, b.Status, b.TagsJSON)
			}
			assertJSONEqual(t, "bare attributes", b.AttributesJSON, mustJSON(bare))
		})
	}
}

// A failed page keeps the rows read before it. A denial is warned about once
// and not returned; any other error is returned after the rows are stored.
func TestScanBatchListedTypes_ListErrors(t *testing.T) {
	isClientErr := func(err error) bool { return isAPIErrorCode(err, "ClientException") }
	for _, c := range batchListCases() {
		for _, tc := range []struct {
			name      string
			first     error // the first page's error; without one it lists one element
			second    error // the second page's error
			repeat    bool  // the first page returns its own token again
			wantErr   func(error) bool
			wantRows  int
			wantWarns int
		}{
			{name: "denied", first: batchDenied, wantWarns: 1},
			{name: "denied after a page keeps its rows", second: batchDenied, wantRows: 1, wantWarns: 1},
			{name: "client error", first: batchClientErr, wantErr: isClientErr},
			{name: "client error after a page keeps its rows", second: batchClientErr, wantRows: 1, wantErr: isClientErr},
			{name: "repeated token keeps its rows", repeat: true, wantRows: 1, wantErr: func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
		} {
			t.Run(c.typ+" "+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				first := batchPage{items: []any{c.elem(batchTestARN("x", "a"), "a", "", nil)}, next: "p2", err: tc.first}
				pages := map[string]batchPage{"": first, "p2": {err: tc.second}}
				if tc.repeat {
					first.next = "p1"
					pages = map[string]batchPage{"": first, "p1": first}
				}
				stub := &stubBatch{}
				c.install(stub, pages)
				_, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
				if (tc.wantErr == nil) != (err == nil) || (tc.wantErr != nil && !tc.wantErr(err)) {
					t.Fatalf("err = %v; want error %v", err, tc.wantErr != nil)
				}
				if n := len(rowsByNativeID(t, st, c.typ)); n != tc.wantRows {
					t.Errorf("rows = %d; want %d", n, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
			})
		}
	}
}

// The job-queue phase returns the ARNs of the queues it stored, and none
// when the write failed.
func TestScanBatchJobQueues_ReturnsStoredARNs(t *testing.T) {
	c := batchListCases()[1]
	stub := &stubBatch{}
	c.install(stub, map[string]batchPage{"": {items: []any{
		c.elem(batchTestARN("job-queue", "q1"), "q1", "", nil), c.elem("", "no-arn", "", nil), c.elem(batchTestARN("job-queue", "q2"), "q2", "", nil),
	}}})
	st := newTestStore(t)
	arns, _, _, err := scanBatchJobQueues(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanBatchJobQueues: %v", err)
	}
	if want := []string{batchTestARN("job-queue", "q1"), batchTestARN("job-queue", "q2")}; !slices.Equal(arns, want) {
		t.Errorf("arns = %v; want %v", arns, want)
	}

	st = newTestStore(t)
	refuseStoreWrites(t, st, TypeBatchJobQueue)
	arns, _, _, err = scanBatchJobQueues(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || len(arns) != 0 {
		t.Errorf("failed write: arns %v, err %v; want none and ErrStoreWrite", arns, err)
	}
}

// batchSchedStub lists n scheduling policies p000.. over two pages, the
// first also carrying one without an ARN; describing an ARN returns its
// policy, p000 tagged. descErr, when set, answers the describe call for the
// chunk starting with that ARN instead. More than 100 ARNs in one describe
// call fails, so the chunk size is pinned.
func batchSchedStub(n int, listErr error, descErr map[string]error) *stubBatch {
	var first, second []any
	for i := range n {
		p := batchtypes.SchedulingPolicyListingDetail{Arn: sdkaws.String(batchTestARN("scheduling-policy", fmt.Sprintf("p%03d", i)))}
		if i < 60 {
			first = append(first, p)
		} else {
			second = append(second, p)
		}
	}
	first = append(first, batchtypes.SchedulingPolicyListingDetail{})
	pages := map[string]batchPage{"": {items: first, next: "p2"}, "p2": {items: second, err: listErr}}
	return &stubBatch{
		listSched: batchPaged(pages,
			func(in *batch.ListSchedulingPoliciesInput) (*int32, *string) { return in.MaxResults, in.NextToken },
			func(items []any, next *string) *batch.ListSchedulingPoliciesOutput {
				return &batch.ListSchedulingPoliciesOutput{SchedulingPolicies: batchItems[batchtypes.SchedulingPolicyListingDetail](items), NextToken: next}
			}),
		descSched: func(in *batch.DescribeSchedulingPoliciesInput) (*batch.DescribeSchedulingPoliciesOutput, error) {
			if len(in.Arns) == 0 || len(in.Arns) > 100 {
				return nil, apiErr("ClientException", "arns must hold 1 to 100 entries")
			}
			if err := descErr[in.Arns[0]]; err != nil {
				return nil, err
			}
			out := &batch.DescribeSchedulingPoliciesOutput{}
			for _, arn := range in.Arns {
				p := batchtypes.SchedulingPolicyDetail{Arn: sdkaws.String(arn), Name: sdkaws.String(arn[len(arn)-4:])}
				if arn == batchTestARN("scheduling-policy", "p000") {
					p.Tags = map[string]string{"team": "data"}
					p.FairsharePolicy = &batchtypes.FairsharePolicy{ShareDecaySeconds: sdkaws.Int32(3600)}
				}
				out.SchedulingPolicies = append(out.SchedulingPolicies, p)
			}
			return out, nil
		},
	}
}

func TestScanBatchSchedulingPolicies_StoresEveryChunk(t *testing.T) {
	st := newTestStore(t)
	listWarns := countOpWarnings(st, "batch:ListSchedulingPolicies")
	stub := batchSchedStub(101, nil, nil)
	stub.descSched = func(describe func(*batch.DescribeSchedulingPoliciesInput) (*batch.DescribeSchedulingPoliciesOutput, error)) func(*batch.DescribeSchedulingPoliciesInput) (*batch.DescribeSchedulingPoliciesOutput, error) {
		return func(in *batch.DescribeSchedulingPoliciesInput) (*batch.DescribeSchedulingPoliciesOutput, error) {
			out, err := describe(in)
			if in.Arns[0] == batchTestARN("scheduling-policy", "p100") {
				out.SchedulingPolicies = append(out.SchedulingPolicies, batchtypes.SchedulingPolicyDetail{Name: sdkaws.String("no-arn")})
			}
			return out, err
		}
	}(stub.descSched)
	descWarns := countOpWarnings(st, "batch:DescribeSchedulingPolicies")
	total, inserted, err := scanBatchSchedulingPolicies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 101 || inserted != 101 {
		t.Errorf("total, inserted = %d, %d; want 101, 101", total, inserted)
	}
	if n := stub.callCount("DescribeSchedulingPolicies"); n != 2 {
		t.Errorf("describe calls = %d; want 2 (100 ARNs, then 1)", n)
	}
	if *listWarns != 1 || *descWarns != 1 {
		t.Errorf("warnings = list %d, describe %d; want 1 each for the policy without an ARN", *listWarns, *descWarns)
	}
	rows := rowsByNativeID(t, st, TypeBatchSchedulingPolicy)
	arn := batchTestARN("scheduling-policy", "p000")
	r := rows[arn]
	if r.NativeID != arn || sv(r.Name) != "p000" || r.Status != nil || sv(r.Region) != testRegion {
		t.Errorf("p000 row = id %q name %q status %v region %q", r.NativeID, sv(r.Name), r.Status, sv(r.Region))
	}
	assertJSONEqual(t, "attributes", r.AttributesJSON, mustJSON(batchtypes.SchedulingPolicyDetail{
		Arn: &arn, Name: sdkaws.String("p000"), Tags: map[string]string{"team": "data"},
		FairsharePolicy: &batchtypes.FairsharePolicy{ShareDecaySeconds: sdkaws.Int32(3600)},
	}))
	assertTagsMap(t, r.TagsJSON, map[string]string{"team": "data"})
	if u := rows[batchTestARN("scheduling-policy", "p001")]; u.TagsJSON != nil {
		t.Errorf("p001 tags = %s; want none", *u.TagsJSON)
	}
}

// A failed describe call costs only its own chunk; a denied one is warned
// about once and stops the describes; the policies listed before a failed
// listing page are still described and stored.
func TestScanBatchSchedulingPolicies_Errors(t *testing.T) {
	isClientErr := func(err error) bool { return isAPIErrorCode(err, "ClientException") }
	firstChunk := batchTestARN("scheduling-policy", "p000")
	for _, tc := range []struct {
		name                         string
		listErr                      error
		descErr                      map[string]error
		wantErr                      func(error) bool
		wantRows, wantDescribes      int
		wantListWarns, wantDescWarns int
		wantDenied                   []string // what the describe denial warning names: the count not described and the first ARN
	}{
		{name: "describe error keeps the other chunk", descErr: map[string]error{firstChunk: batchClientErr}, wantErr: isClientErr, wantRows: 1, wantDescribes: 2, wantListWarns: 1},
		{
			name: "describe denied", descErr: map[string]error{firstChunk: batchDenied}, wantDescribes: 1, wantListWarns: 1, wantDescWarns: 1,
			wantDenied: []string{"not described: 101,", firstChunk},
		},
		{
			name: "second describe denied", descErr: map[string]error{batchTestARN("scheduling-policy", "p100"): batchDenied}, wantRows: 100, wantDescribes: 2, wantListWarns: 1, wantDescWarns: 1,
			wantDenied: []string{"not described: 1,", batchTestARN("scheduling-policy", "p100")},
		},
		{name: "list error after a page", listErr: batchClientErr, wantErr: isClientErr, wantRows: 60, wantDescribes: 1, wantListWarns: 1},
		{name: "list denied after a page", listErr: batchDenied, wantRows: 60, wantDescribes: 1, wantListWarns: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			listWarns := countOpWarnings(st, "batch:ListSchedulingPolicies")
			descWarns := countOpWarnings(st, "batch:DescribeSchedulingPolicies")
			var denied []string
			prev := st.OnWarn
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == "batch:DescribeSchedulingPolicies" {
					denied = append(denied, w.Message)
				}
				prev(w)
			}
			stub := batchSchedStub(101, tc.listErr, tc.descErr)
			_, _, err := scanBatchSchedulingPolicies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if (tc.wantErr == nil) != (err == nil) || (tc.wantErr != nil && !tc.wantErr(err)) {
				t.Fatalf("err = %v; want error %v", err, tc.wantErr != nil)
			}
			if n := len(rowsByNativeID(t, st, TypeBatchSchedulingPolicy)); n != tc.wantRows {
				t.Errorf("rows = %d; want %d", n, tc.wantRows)
			}
			if n := stub.callCount("DescribeSchedulingPolicies"); n != tc.wantDescribes {
				t.Errorf("describe calls = %d; want %d", n, tc.wantDescribes)
			}
			if *listWarns != tc.wantListWarns || *descWarns != tc.wantDescWarns {
				t.Errorf("warnings = list %d, describe %d; want %d, %d", *listWarns, *descWarns, tc.wantListWarns, tc.wantDescWarns)
			}
			for _, key := range tc.wantDenied {
				if len(denied) != 1 || !strings.Contains(denied[0], key) {
					t.Errorf("describe denial warnings = %q; want one naming %q", denied, key)
				}
			}
		})
	}
}

// batchQuotaStub answers ListQuotaShares per job queue from pages[queue];
// a queue with no pages fails with ClientException, as do a MaxResults
// other than the cap and an unknown token.
func batchQuotaStub(pages map[string]map[string]batchPage) func(*batch.ListQuotaSharesInput) (*batch.ListQuotaSharesOutput, error) {
	return func(in *batch.ListQuotaSharesInput) (*batch.ListQuotaSharesOutput, error) {
		qp, ok := pages[sdkaws.ToString(in.JobQueue)]
		if !ok {
			return nil, apiErr("ClientException", "unknown job queue "+sdkaws.ToString(in.JobQueue))
		}
		return batchPaged(qp,
			func(in *batch.ListQuotaSharesInput) (*int32, *string) { return in.MaxResults, in.NextToken },
			func(items []any, next *string) *batch.ListQuotaSharesOutput {
				return &batch.ListQuotaSharesOutput{QuotaShares: batchItems[batchtypes.QuotaShareDetail](items), NextToken: next}
			})(in)
	}
}

// Each queue's shares are read over every page; one queue's failure costs
// only that queue, a denial is warned about once, a share without an ARN is
// warned about once.
func TestScanBatchQuotaShares(t *testing.T) {
	q1, q2, q3 := batchTestARN("job-queue", "q1"), batchTestARN("job-queue", "q2"), batchTestARN("job-queue", "q3")
	tagged := batchtypes.QuotaShareDetail{
		QuotaShareArn: sdkaws.String(q1 + "/quota-share/s1"), QuotaShareName: sdkaws.String("s1"), JobQueueArn: &q1,
		State: batchtypes.QuotaShareStateEnabled,
	}
	bare := batchtypes.QuotaShareDetail{QuotaShareArn: sdkaws.String(q3 + "/quota-share/s3")}
	for _, tc := range []struct {
		name      string
		q2        error
		wantErr   bool
		wantWarns int
	}{
		{name: "every queue read", wantWarns: 1},
		{name: "one queue fails", q2: batchClientErr, wantErr: true, wantWarns: 1},
		{name: "one queue denied", q2: batchDenied, wantWarns: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]map[string]batchPage{
				q1: {"": {items: []any{tagged, batchtypes.QuotaShareDetail{QuotaShareName: sdkaws.String("no-arn")}}, next: "p2"}, "p2": {}},
				q3: {"": {items: []any{bare}}},
			}
			if tc.q2 != nil {
				pages[q2] = map[string]batchPage{"": {err: tc.q2}}
			} else {
				pages[q2] = map[string]batchPage{"": {}}
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "batch:ListQuotaShares")
			stub := &stubBatch{quotaShares: batchQuotaStub(pages)}
			total, inserted, err := scanBatchQuotaShares(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{q1, q2, q3})
			if tc.wantErr != (err != nil) || (tc.wantErr && !isAPIErrorCode(err, "ClientException")) {
				t.Fatalf("err = %v; want error %v", err, tc.wantErr)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeBatchQuotaShare)
			r := rows[q1+"/quota-share/s1"]
			if sv(r.Name) != "s1" || sv(r.Status) != "ENABLED" || sv(r.Region) != testRegion {
				t.Errorf("s1 row = name %q status %q region %q", sv(r.Name), sv(r.Status), sv(r.Region))
			}
			assertJSONEqual(t, "attributes", r.AttributesJSON, mustJSON(tagged))
			b := rows[q3+"/quota-share/s3"]
			if b.Name != nil || b.Status != nil || b.TagsJSON != nil {
				t.Errorf("s3 row = name %v status %v tags %v; want all unset", b.Name, b.Status, b.TagsJSON)
			}
			assertJSONEqual(t, "bare attributes", b.AttributesJSON, mustJSON(bare))
		})
	}
}

// batchAllStub lists one element of every Batch type, the quota share under
// queue q1; failOp, when set, answers that op with err instead.
func batchAllStub(failOp string, err error) *stubBatch {
	qARN := batchTestARN("job-queue", "q1")
	one := func(it any) map[string]batchPage { return map[string]batchPage{"": {items: []any{it}}} }
	s := &stubBatch{quotaShares: batchQuotaStub(map[string]map[string]batchPage{
		qARN: one(batchtypes.QuotaShareDetail{QuotaShareArn: sdkaws.String(qARN + "/quota-share/qs1")}),
	})}
	for _, c := range batchListCases() {
		c.install(s, one(c.elem(batchTestARN(c.typ, "one"), "", "", nil)))
	}
	batchListCases()[1].install(s, one(batchtypes.JobQueueDetail{JobQueueArn: &qARN}))
	sched := batchSchedStub(1, nil, nil)
	s.listSched, s.descSched = sched.listSched, sched.descSched
	fail := map[string]func(){
		"DescribeComputeEnvironments": func() {
			s.computeEnvs = func(*batch.DescribeComputeEnvironmentsInput) (*batch.DescribeComputeEnvironmentsOutput, error) {
				return nil, err
			}
		},
		"DescribeJobQueues": func() {
			s.jobQueues = func(*batch.DescribeJobQueuesInput) (*batch.DescribeJobQueuesOutput, error) { return nil, err }
		},
		"DescribeJobDefinitions": func() {
			s.jobDefs = func(*batch.DescribeJobDefinitionsInput) (*batch.DescribeJobDefinitionsOutput, error) { return nil, err }
		},
		"ListSchedulingPolicies": func() {
			s.listSched = func(*batch.ListSchedulingPoliciesInput) (*batch.ListSchedulingPoliciesOutput, error) { return nil, err }
		},
		"ListConsumableResources": func() {
			s.consumables = func(*batch.ListConsumableResourcesInput) (*batch.ListConsumableResourcesOutput, error) {
				return nil, err
			}
		},
		"DescribeServiceEnvironments": func() {
			s.serviceEnvs = func(*batch.DescribeServiceEnvironmentsInput) (*batch.DescribeServiceEnvironmentsOutput, error) {
				return nil, err
			}
		},
		"ListQuotaShares": func() {
			s.quotaShares = func(*batch.ListQuotaSharesInput) (*batch.ListQuotaSharesOutput, error) { return nil, err }
		},
	}
	if f := fail[failOp]; f != nil {
		f()
	}
	return s
}

func batchAllTypes() []string {
	return []string{
		TypeBatchComputeEnvironment, TypeBatchJobQueue, TypeBatchJobDefinition, TypeBatchSchedulingPolicy,
		TypeBatchConsumableResource, TypeBatchServiceEnvironment, TypeBatchQuotaShare,
	}
}

// Every phase runs whichever phase fails, and the failure is returned. The
// quota shares are read for the queues the job-queue phase stored, without
// listing the queues again.
func TestScanBatchAll_PhaseErrorsAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		sdkOp string
		lost  []string // the types the failure leaves with no rows
	}{
		{sdkOp: ""},
		{sdkOp: "DescribeComputeEnvironments", lost: []string{TypeBatchComputeEnvironment}},
		{sdkOp: "DescribeJobQueues", lost: []string{TypeBatchJobQueue, TypeBatchQuotaShare}},
		{sdkOp: "DescribeJobDefinitions", lost: []string{TypeBatchJobDefinition}},
		{sdkOp: "ListSchedulingPolicies", lost: []string{TypeBatchSchedulingPolicy}},
		{sdkOp: "ListConsumableResources", lost: []string{TypeBatchConsumableResource}},
		{sdkOp: "DescribeServiceEnvironments", lost: []string{TypeBatchServiceEnvironment}},
		{sdkOp: "ListQuotaShares", lost: []string{TypeBatchQuotaShare}},
	} {
		t.Run("fail "+tc.sdkOp, func(t *testing.T) {
			st := newTestStore(t)
			stub := batchAllStub(tc.sdkOp, batchClientErr)
			total, inserted, err := scanBatchAll(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if (tc.sdkOp == "") != (err == nil) || (err != nil && !isAPIErrorCode(err, "ClientException")) {
				t.Errorf("err = %v; want the failed phase's ClientException only when a phase failed", err)
			}
			want := 7 - len(tc.lost)
			if total != want || inserted != want {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
			}
			for _, typ := range batchAllTypes() {
				n := 1
				if slices.Contains(tc.lost, typ) {
					n = 0
				}
				if got := len(rowsByNativeID(t, st, typ)); got != n {
					t.Errorf("%s rows = %d; want %d", typ, got, n)
				}
			}
			if n := stub.callCount("DescribeJobQueues"); n != 1 {
				t.Errorf("DescribeJobQueues calls = %d; want 1", n)
			}
		})
	}
}

// A transient error in one phase is demoted to one warning so that another
// phase's hard error still reaches the dispatcher as a hard error; alone it
// is returned, so the scan is marked partial.
func TestScanBatchAll_TransientDemoted(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "batch:DescribeComputeEnvironments")
	stub := batchAllStub("DescribeComputeEnvironments", batchThrottled)
	stub.jobDefs = func(*batch.DescribeJobDefinitionsInput) (*batch.DescribeJobDefinitionsOutput, error) {
		return nil, batchClientErr
	}
	total, _, err := scanBatchAll(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ClientException") {
		t.Errorf("err = %v (outcome %v); want only the hard ClientException (outcomeError)", err, got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d; want 1", *warns)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (every row but the compute environment and job definition)", total)
	}

	st = newTestStore(t)
	total, _, err = scanBatchAll(context.Background(), batchAllStub("ListQuotaShares", batchThrottled), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("throttled alone: err = %v (outcome %v); want outcomeTransient", err, got)
	}
	if total != 6 {
		t.Errorf("throttled alone: total = %d; want 6", total)
	}
}

// A failed store write stops the scan: every later write would fail too.
func TestScanBatchAll_StoreWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeBatchComputeEnvironment)
	_, _, err := scanBatchAll(context.Background(), batchAllStub("", nil), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want ErrStoreWrite", err)
	}
	if n := countStoredRows(t, st); n != 0 {
		t.Errorf("stored rows = %d; want 0 (no phase after the failed write runs)", n)
	}
}

// A job-queue listing that fails on a later page still hands the queues of
// the pages before it to the quota-share phase.
func TestScanBatchAll_QuotaSharesOfQueuesBeforeFailedPage(t *testing.T) {
	st := newTestStore(t)
	qARN := batchTestARN("job-queue", "q1")
	stub := batchAllStub("", nil)
	batchListCases()[1].install(stub, map[string]batchPage{
		"":   {items: []any{batchtypes.JobQueueDetail{JobQueueArn: &qARN}}, next: "p2"},
		"p2": {err: batchClientErr},
	})
	_, _, err := scanBatchAll(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ClientException") {
		t.Errorf("err = %v; want the job-queue page's ClientException", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeBatchQuotaShare)[qARN+"/quota-share/qs1"]; !ok {
		t.Error("quota share of the queue listed before the failed page not stored")
	}
}

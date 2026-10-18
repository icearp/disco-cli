package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

var connectAnalyticsCreated = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func connectAnalyticsARN(inst, kind, id string) string {
	return "arn:aws:connect:us-east-1:123456789012:instance/" + inst + "/" + kind + "/" + id
}

// connectAnalyticsRec records what the list stubs were sent. The stubs run on
// the describe fan-out's goroutines too, so it is locked.
type connectAnalyticsRec struct {
	mu          sync.Mutex
	instances   parentCalls
	maxResults  []int32
	metricTypes []cttypes.MetricType
}

func (r *connectAnalyticsRec) list(inst string, maxResults *int32) {
	r.instances.add(inst)
	r.mu.Lock()
	defer r.mu.Unlock()
	if maxResults == nil {
		r.maxResults = append(r.maxResults, 0)
		return
	}
	r.maxResults = append(r.maxResults, *maxResults)
}

// connectAnalyticsCase drives one analytics phase. Each instance lists two
// pages: id "a" on the first, then id "b" and an element with no ARN.
type connectAnalyticsCase struct {
	name, typ, kind, listOp, describeOp string
	handlers                            func(rec *connectAnalyticsRec) map[string]func(any) (any, error)
	run                                 func(context.Context, connectAnalyticsAPI, []cttypes.InstanceSummary, *account, *store.Store) (int, int, error)
	// attr is an attribute only the describe body carries.
	attr  func(attrs map[string]any) any
	want  string
	check func(t *testing.T, r store.Resource)
}

func extractionDefinitionHandlers(rec *connectAnalyticsRec) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListExtractionDefinitions": func(in any) (any, error) {
			p := in.(*connect.ListExtractionDefinitionsInput)
			inst := sv(p.InstanceId)
			rec.list(inst, p.MaxResults)
			summary := func(id string) cttypes.ExtractionDefinitionSummary {
				return cttypes.ExtractionDefinitionSummary{ExtractionDefinitionArn: ptrStr(connectAnalyticsARN(inst, "extraction-definition", id)), ExtractionDefinitionId: ptrStr(id)}
			}
			if p.NextToken == nil {
				return &connect.ListExtractionDefinitionsOutput{NextToken: ptrStr("p2"), ExtractionDefinitionSummaryList: []cttypes.ExtractionDefinitionSummary{summary("a")}}, nil
			}
			return &connect.ListExtractionDefinitionsOutput{ExtractionDefinitionSummaryList: []cttypes.ExtractionDefinitionSummary{
				summary("b"), {ExtractionDefinitionId: ptrStr("no-arn")},
			}}, nil
		},
		"DescribeExtractionDefinition": func(in any) (any, error) {
			p := in.(*connect.DescribeExtractionDefinitionInput)
			inst, id := sv(p.InstanceId), sv(p.ExtractionDefinitionId)
			return &connect.DescribeExtractionDefinitionOutput{ExtractionDefinition: &cttypes.ExtractionDefinition{
				ExtractionDefinitionArn: ptrStr(connectAnalyticsARN(inst, "extraction-definition", id)), ExtractionDefinitionId: ptrStr(id),
				Name: ptrStr("order number " + id), CreatedTime: &connectAnalyticsCreated, Tags: map[string]string{"team": "cx"},
				ExtractionConfiguration: &cttypes.ExtractionConfiguration{PromptHint: ptrStr("find the order number")},
			}}, nil
		},
	}
}

func metricHandlers(rec *connectAnalyticsRec) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListMetrics": func(in any) (any, error) {
			p := in.(*connect.ListMetricsInput)
			inst := sv(p.InstanceId)
			rec.list(inst, p.MaxResults)
			rec.mu.Lock()
			rec.metricTypes = append(rec.metricTypes, p.Type)
			rec.mu.Unlock()
			summary := func(id string) cttypes.MetricSummary {
				return cttypes.MetricSummary{Arn: ptrStr(connectAnalyticsARN(inst, "metric", id)), Id: ptrStr(id), Status: cttypes.MetricStatusPublished}
			}
			if p.NextToken == nil {
				return &connect.ListMetricsOutput{NextToken: ptrStr("p2"), MetricSummaryList: []cttypes.MetricSummary{summary("a")}}, nil
			}
			return &connect.ListMetricsOutput{MetricSummaryList: []cttypes.MetricSummary{summary("b"), {Id: ptrStr("no-arn")}}}, nil
		},
		"DescribeMetric": func(in any) (any, error) {
			p := in.(*connect.DescribeMetricInput)
			inst, id := sv(p.InstanceId), sv(p.MetricId)
			return &connect.DescribeMetricOutput{Metric: &cttypes.MetricDefinition{
				// Qualified, as the model allows: NativeID must come from the summary.
				Arn: ptrStr(connectAnalyticsARN(inst, "metric", id) + ":$LATEST"), Id: ptrStr(id),
				Name: ptrStr("service level " + id), Status: cttypes.MetricStatusPublished, Type: cttypes.MetricTypeCustomerManaged,
				CreatedTime: &connectAnalyticsCreated, Tags: map[string]string{"team": "cx"},
				MetricCalculation: &cttypes.MetricCalculation{Calculation: ptrStr("100 * SUM(M1)")},
				CreatedUser:       &cttypes.CreatedByInfoMemberConnectUserArn{Value: connectTestInstanceARN + "/agent/u-1"},
			}}, nil
		},
	}
}

func connectAnalyticsCases() []connectAnalyticsCase {
	return []connectAnalyticsCase{
		{
			name: "extraction definitions", typ: TypeConnectExtractionDefinition, kind: "extraction-definition",
			listOp: "ListExtractionDefinitions", describeOp: "DescribeExtractionDefinition", handlers: extractionDefinitionHandlers,
			run: func(ctx context.Context, c connectAnalyticsAPI, inst []cttypes.InstanceSummary, a *account, st *store.Store) (int, int, error) {
				return scanConnectExtractionDefinitions(ctx, c, inst, a, testRegion, st, testScanID)
			},
			attr: func(attrs map[string]any) any {
				cfg, _ := attrs["ExtractionConfiguration"].(map[string]any)
				return cfg["PromptHint"]
			},
			want: "find the order number",
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Name) != "order number b" || r.Status != nil {
					t.Errorf("Name, Status = %q, %v; want order number b, nil", sv(r.Name), r.Status)
				}
			},
		},
		{
			name: "metrics", typ: TypeConnectMetric, kind: "metric",
			listOp: "ListMetrics", describeOp: "DescribeMetric", handlers: metricHandlers,
			run: func(ctx context.Context, c connectAnalyticsAPI, inst []cttypes.InstanceSummary, a *account, st *store.Store) (int, int, error) {
				return scanConnectMetrics(ctx, c, inst, a, testRegion, st, testScanID)
			},
			attr: func(attrs map[string]any) any {
				calc, _ := attrs["MetricCalculation"].(map[string]any)
				return calc["Calculation"]
			},
			want: "100 * SUM(M1)",
			check: func(t *testing.T, r store.Resource) {
				if sv(r.Name) != "service level b" || sv(r.Status) != "PUBLISHED" {
					t.Errorf("Name, Status = %q, %q; want service level b, PUBLISHED", sv(r.Name), sv(r.Status))
				}
				var attrs struct {
					CreatedUser struct{ ConnectUserArn string }
				}
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.CreatedUser.ConnectUserArn != connectTestInstanceARN+"/agent/u-1" {
					t.Errorf("CreatedUser = %+v (err %v), want keyed ConnectUserArn", attrs.CreatedUser, err)
				}
			},
		},
	}
}

func TestConnectAnalyticsPhases_DescribesEveryListedItemOfEveryInstance(t *testing.T) {
	for _, c := range connectAnalyticsCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var rec connectAnalyticsRec
			total, inserted, err := c.run(context.Background(), connectStubClient(c.handlers(&rec)), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 4 || inserted != 4 {
				t.Errorf("total, inserted = %d, %d; want 4, 4", total, inserted)
			}
			rows := rowsByNativeID(t, st, c.typ)
			want := []string{
				connectAnalyticsARN("i-1", c.kind, "a"), connectAnalyticsARN("i-1", c.kind, "b"),
				connectAnalyticsARN("i-2", c.kind, "a"), connectAnalyticsARN("i-2", c.kind, "b"),
			}
			if got := nativeIDs(rows); !slices.Equal(got, want) {
				t.Fatalf("NativeIDs = %v, want %v", got, want)
			}
			if got := rec.instances.distinct(); !slices.Equal(got, []string{"i-1", "i-2"}) {
				t.Errorf("instances listed = %v, want i-1, i-2", got)
			}
			for _, m := range rec.maxResults {
				if m != 100 {
					t.Errorf("MaxResults = %d, want the model cap 100", m)
				}
			}
			r := rows[connectAnalyticsARN("i-2", c.kind, "b")]
			if r.Type != c.typ || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-08-01T12:00:00Z" || sv(r.TagsJSON) != `{"team":"cx"}` {
				t.Errorf("row = Type %s Region %s CreatedAt %s Tags %s", r.Type, sv(r.Region), sv(r.CreatedAt), sv(r.TagsJSON))
			}
			c.check(t, r)
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attributes: %v", err)
			}
			if got := c.attr(attrs); got != c.want {
				t.Errorf("describe attribute = %v, want %q (attributes %s)", got, c.want, r.AttributesJSON)
			}
		})
	}
}

func TestScanConnectMetrics_ListsOnlyCustomerManaged(t *testing.T) {
	var rec connectAnalyticsRec
	if _, _, err := scanConnectMetrics(context.Background(), connectStubClient(metricHandlers(&rec)), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rec.metricTypes) != 2 {
		t.Fatalf("ListMetrics calls = %d, want 2", len(rec.metricTypes))
	}
	for _, typ := range rec.metricTypes {
		if typ != cttypes.MetricTypeCustomerManaged {
			t.Errorf("ListMetrics Type = %q, want CUSTOMER_MANAGED", typ)
		}
	}
}

func TestConnectAnalyticsPhases_Errors(t *testing.T) {
	ctx := context.Background()
	for _, c := range connectAnalyticsCases() {
		listLabel, describeLabel := "connect:"+c.listOp, "connect:"+c.describeOp
		scan := func(t *testing.T, st *store.Store, h map[string]func(any) (any, error), instances ...string) (int, int, error) {
			t.Helper()
			return c.run(ctx, connectStubClient(h), connectInstances(instances...), newTestAccount(testAccountID), st)
		}
		withDescribe := func(rec *connectAnalyticsRec, fail func(inst, id string) error) map[string]func(any) (any, error) {
			h := c.handlers(rec)
			ok := h[c.describeOp]
			h[c.describeOp] = func(in any) (any, error) {
				var inst, id string
				switch p := in.(type) {
				case *connect.DescribeExtractionDefinitionInput:
					inst, id = sv(p.InstanceId), sv(p.ExtractionDefinitionId)
				case *connect.DescribeMetricInput:
					inst, id = sv(p.InstanceId), sv(p.MetricId)
				}
				if err := fail(inst, id); err != nil {
					return nil, err
				}
				return ok(in)
			}
			return h
		}
		t.Run(c.name+"/no items", func(t *testing.T) {
			st := newTestStore(t)
			h := c.handlers(&connectAnalyticsRec{})
			h[c.listOp] = func(any) (any, error) {
				if c.listOp == "ListMetrics" {
					return &connect.ListMetricsOutput{}, nil
				}
				return &connect.ListExtractionDefinitionsOutput{}, nil
			}
			total, inserted, err := scan(t, st, h, "i-1")
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("got (%d, %d, %v), want (0, 0, nil)", total, inserted, err)
			}
		})
		t.Run(c.name+"/list denied warns once", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, listLabel)
			h := c.handlers(&connectAnalyticsRec{})
			delete(h, c.listOp)
			total, _, err := scan(t, st, h, "i-1", "i-2")
			if err != nil || total != 0 || *warns != 1 {
				t.Errorf("got (%d, %v, %d warnings), want (0, nil, 1)", total, err, *warns)
			}
		})
		t.Run(c.name+"/deleted instance skipped silently", func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			h := c.handlers(&connectAnalyticsRec{})
			ok := h[c.listOp]
			h[c.listOp] = func(in any) (any, error) {
				inst := ""
				switch p := in.(type) {
				case *connect.ListExtractionDefinitionsInput:
					inst = sv(p.InstanceId)
				case *connect.ListMetricsInput:
					inst = sv(p.InstanceId)
				}
				if inst == "i-1" {
					return nil, connectNotFound
				}
				return ok(in)
			}
			total, _, err := scan(t, st, h, "i-1", "i-2")
			if err != nil || total != 2 || *warns != 0 {
				t.Errorf("got (%d, %v, %d warnings), want i-2's 2 rows, nil, 0 warnings", total, err, *warns)
			}
		})
		t.Run(c.name+"/list error propagates", func(t *testing.T) {
			st := newTestStore(t)
			h := c.handlers(&connectAnalyticsRec{})
			h[c.listOp] = func(any) (any, error) { return nil, connectBadParam }
			total, _, err := scan(t, st, h, "i-1")
			assertAPICode(t, err, "InvalidParameterException")
			if total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Errorf("total = %d, want 0 and no rows", total)
			}
		})
		t.Run(c.name+"/describe denied warns once and keeps siblings", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, describeLabel)
			h := withDescribe(&connectAnalyticsRec{}, func(inst, id string) error {
				if id == "a" {
					return connectDenied
				}
				return nil
			})
			total, _, err := scan(t, st, h, "i-1", "i-2")
			if err != nil || total != 2 || *warns != 1 {
				t.Errorf("got (%d, %v, %d warnings), want (2, nil, 1)", total, err, *warns)
			}
		})
		t.Run(c.name+"/describe error propagates", func(t *testing.T) {
			st := newTestStore(t)
			h := withDescribe(&connectAnalyticsRec{}, func(inst, id string) error {
				if inst == "i-2" && id == "b" {
					return connectBadParam
				}
				return nil
			})
			total, _, err := scan(t, st, h, "i-1", "i-2")
			assertAPICode(t, err, "InvalidParameterException")
			if total != 0 {
				t.Errorf("total = %d, want 0", total)
			}
		})
		t.Run(c.name+"/repeated token stops", func(t *testing.T) {
			st := newTestStore(t)
			var mu sync.Mutex
			calls := 0
			h := c.handlers(&connectAnalyticsRec{})
			h[c.listOp] = func(any) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if calls > 5 {
					return nil, connectBadParam
				}
				if c.listOp == "ListMetrics" {
					return &connect.ListMetricsOutput{NextToken: ptrStr("same")}, nil
				}
				return &connect.ListExtractionDefinitionsOutput{NextToken: ptrStr("same")}, nil
			}
			if _, _, err := scan(t, st, h, "i-1"); err != nil {
				t.Fatalf("err = %v, want the paginator to stop on the repeated token", err)
			}
			if calls != 2 {
				t.Errorf("list calls = %d, want 2", calls)
			}
		})
		t.Run(c.name+"/rows listed before a denied page are described", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, listLabel)
			h := c.handlers(&connectAnalyticsRec{})
			ok := h[c.listOp]
			h[c.listOp] = func(in any) (any, error) {
				var token *string
				switch p := in.(type) {
				case *connect.ListExtractionDefinitionsInput:
					token = p.NextToken
				case *connect.ListMetricsInput:
					token = p.NextToken
				}
				if token != nil {
					return nil, connectDenied
				}
				return ok(in)
			}
			total, _, err := scan(t, st, h, "i-1")
			if err != nil || total != 1 || *warns != 1 {
				t.Errorf("got (%d, %v, %d warnings), want (1, nil, 1)", total, err, *warns)
			}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, []string{connectAnalyticsARN("i-1", c.kind, "a")}) {
				t.Errorf("rows = %v, want page one's row", got)
			}
		})
		t.Run(c.name+"/summary listed twice is stored once", func(t *testing.T) {
			st := newTestStore(t)
			var mu sync.Mutex
			describes := 0
			h := c.handlers(&connectAnalyticsRec{})
			list, describe := h[c.listOp], h[c.describeOp]
			h[c.listOp] = func(in any) (any, error) {
				out, err := list(in)
				// Both pages answer the first page's summary.
				switch o := out.(type) {
				case *connect.ListMetricsOutput:
					first, _ := list(&connect.ListMetricsInput{InstanceId: in.(*connect.ListMetricsInput).InstanceId})
					o.MetricSummaryList = first.(*connect.ListMetricsOutput).MetricSummaryList
				case *connect.ListExtractionDefinitionsOutput:
					first, _ := list(&connect.ListExtractionDefinitionsInput{InstanceId: in.(*connect.ListExtractionDefinitionsInput).InstanceId})
					o.ExtractionDefinitionSummaryList = first.(*connect.ListExtractionDefinitionsOutput).ExtractionDefinitionSummaryList
				}
				return out, err
			}
			h[c.describeOp] = func(in any) (any, error) {
				mu.Lock()
				describes++
				mu.Unlock()
				return describe(in)
			}
			total, inserted, err := scan(t, st, h, "i-1")
			if err != nil || total != 1 || inserted != 1 {
				t.Errorf("got (%d, %d, %v), want (1, 1, nil)", total, inserted, err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, []string{connectAnalyticsARN("i-1", c.kind, "a")}) {
				t.Errorf("rows = %v, want the one listed ARN", got)
			}
			if describes != 1 {
				t.Errorf("describe calls = %d, want 1", describes)
			}
		})
	}
}

// A describe not-found means the item vanished after the list: it is dropped
// silently and its siblings are kept.
func TestConnectAnalyticsPhases_DescribeNotFound(t *testing.T) {
	for _, c := range connectAnalyticsCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			h := c.handlers(&connectAnalyticsRec{})
			ok := h[c.describeOp]
			h[c.describeOp] = func(in any) (any, error) {
				var id string
				switch p := in.(type) {
				case *connect.DescribeExtractionDefinitionInput:
					id = sv(p.ExtractionDefinitionId)
				case *connect.DescribeMetricInput:
					id = sv(p.MetricId)
				}
				if id == "a" {
					return nil, connectNotFound
				}
				return ok(in)
			}
			total, _, err := c.run(context.Background(), connectStubClient(h), connectInstances("i-1", "i-2"), newTestAccount(testAccountID), st)
			if err != nil || total != 2 || *warns != 0 {
				t.Errorf("got (%d, %v, %d warnings), want (2, nil, 0)", total, err, *warns)
			}
		})
	}
}

// savedMetricHandlers lists one PUBLISHED metric "p" and one SAVED metric "s"
// on instance i-1 and records the MetricId each describe was sent; fail, when
// it returns an error for a MetricId, answers that describe with it.
func savedMetricHandlers(describedIDs *parentCalls, fail func(metricID string) error) map[string]func(any) (any, error) {
	h := metricHandlers(&connectAnalyticsRec{})
	h["ListMetrics"] = func(any) (any, error) {
		return &connect.ListMetricsOutput{MetricSummaryList: []cttypes.MetricSummary{
			{Arn: ptrStr(connectAnalyticsARN("i-1", "metric", "p")), Id: ptrStr("p"), Status: cttypes.MetricStatusPublished},
			{Arn: ptrStr(connectAnalyticsARN("i-1", "metric", "s")), Id: ptrStr("s"), Status: cttypes.MetricStatusSaved},
		}}, nil
	}
	h["DescribeMetric"] = func(in any) (any, error) {
		p := in.(*connect.DescribeMetricInput)
		id := sv(p.MetricId)
		describedIDs.add(id)
		if err := fail(id); err != nil {
			return nil, err
		}
		base, _, _ := strings.Cut(id, ":")
		status := cttypes.MetricStatusPublished
		if base == "s" {
			status = cttypes.MetricStatusSaved
		}
		return &connect.DescribeMetricOutput{Metric: &cttypes.MetricDefinition{
			Arn: ptrStr(connectAnalyticsARN("i-1", "metric", id)), Id: ptrStr(base), Name: ptrStr("metric " + base),
			Status: status,
		}}, nil
	}
	return h
}

func TestScanConnectMetrics_SavedMetricDescribedWithQualifier(t *testing.T) {
	ctx := context.Background()
	scan := func(st *store.Store, h map[string]func(any) (any, error)) (int, int, error) {
		return scanConnectMetrics(ctx, connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID)
	}
	t.Run("SAVED uses the qualifier, PUBLISHED does not", func(t *testing.T) {
		st := newTestStore(t)
		var ids parentCalls
		total, _, err := scan(st, savedMetricHandlers(&ids, func(string) error { return nil }))
		if err != nil || total != 2 {
			t.Fatalf("got (%d, %v), want (2, nil)", total, err)
		}
		if got := ids.distinct(); !slices.Equal(got, []string{"p", "s:$SAVED"}) {
			t.Errorf("describe MetricIds = %v, want [p s:$SAVED]", got)
		}
		rows := rowsByNativeID(t, st, TypeConnectMetric)
		want := []string{connectAnalyticsARN("i-1", "metric", "p"), connectAnalyticsARN("i-1", "metric", "s")}
		if got := nativeIDs(rows); !slices.Equal(got, want) {
			t.Fatalf("NativeIDs = %v, want the unqualified summary ARNs %v", got, want)
		}
		if s := sv(rows[want[1]].Status); s != "SAVED" {
			t.Errorf("saved metric Status = %q, want SAVED", s)
		}
	})
	t.Run("qualified describe rejected warns once and keeps siblings", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, connectDescribeMetricOp)
		var ids parentCalls
		total, _, err := scan(st, savedMetricHandlers(&ids, func(id string) error {
			if strings.HasSuffix(id, ":$SAVED") {
				return apiErr("ValidationException", "bad metric id")
			}
			return nil
		}))
		if err != nil || total != 1 || *warns != 1 {
			t.Errorf("got (%d, %v, %d warnings), want (1, nil, 1)", total, err, *warns)
		}
		if _, ok := rowsByNativeID(t, st, TypeConnectMetric)[connectAnalyticsARN("i-1", "metric", "p")]; !ok {
			t.Error("published sibling not stored")
		}
	})
	t.Run("unqualified describe ValidationException propagates", func(t *testing.T) {
		st := newTestStore(t)
		var ids parentCalls
		_, _, err := scan(st, savedMetricHandlers(&ids, func(id string) error {
			if id == "p" {
				return apiErr("ValidationException", "bad metric id")
			}
			return nil
		}))
		assertAPICode(t, err, "ValidationException")
	})
}

func TestFlattenConnectMetric_CreatedByMembers(t *testing.T) {
	cases := []struct {
		name string
		u    cttypes.CreatedByInfo
		want map[string]any
	}{
		{
			"IAM identity", &cttypes.CreatedByInfoMemberAWSIdentityArn{Value: "arn:aws:iam::123456789012:role/admin"},
			map[string]any{"AWSIdentityArn": "arn:aws:iam::123456789012:role/admin"},
		},
		{
			"Connect user", &cttypes.CreatedByInfoMemberConnectUserArn{Value: connectTestInstanceARN + "/agent/u-1"},
			map[string]any{"ConnectUserArn": connectTestInstanceARN + "/agent/u-1"},
		},
		{
			"unknown member", &cttypes.UnknownUnionMember{Tag: "FutureCreator", Value: []byte(`"x"`)},
			map[string]any{"UnknownMember": map[string]any{"Tag": "FutureCreator", "Value": "Ingi"}},
		},
		{"unset", nil, map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Each field is set alone, so a mutant that reads one for the other fails.
			for _, set := range []string{"CreatedUser", "LastModifiedUser"} {
				m := &cttypes.MetricDefinition{Name: ptrStr("sl")}
				if set == "CreatedUser" {
					m.CreatedUser = c.u
				} else {
					m.LastModifiedUser = c.u
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(mustJSON(flattenConnectMetric(m))), &attrs); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if attrs["Name"] != "sl" {
					t.Errorf("Name = %v, want the embedded MetricDefinition field", attrs["Name"])
				}
				for _, key := range []string{"CreatedUser", "LastModifiedUser"} {
					want := map[string]any{}
					if key == set {
						want = c.want
					}
					got, _ := attrs[key].(map[string]any)
					if !maps.EqualFunc(got, want, func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
						t.Errorf("%s set: %s = %v, want %v", set, key, attrs[key], want)
					}
				}
			}
		})
	}
}

func TestScanConnectTestCases_SummaryListedTwiceIsStoredOnce(t *testing.T) {
	st := newTestStore(t)
	h := map[string]func(any) (any, error){
		"ListTestCases": func(in any) (any, error) {
			summary := []cttypes.TestCaseSummary{{Arn: ptrStr(connectTestInstanceARN + "/test-case/tc-a"), Id: ptrStr("i-1:tc-a")}}
			if in.(*connect.ListTestCasesInput).NextToken == nil {
				return &connect.ListTestCasesOutput{NextToken: ptrStr("p2"), TestCaseSummaryList: summary}, nil
			}
			return &connect.ListTestCasesOutput{TestCaseSummaryList: summary}, nil
		},
		"DescribeTestCase": func(in any) (any, error) {
			return describedTestCase(sv(in.(*connect.DescribeTestCaseInput).TestCaseId))
		},
	}
	total, inserted, err := scanConnectTestCases(context.Background(), connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 || inserted != 1 {
		t.Errorf("got (%d, %d, %v), want (1, 1, nil)", total, inserted, err)
	}
}

func connectAnalyticsHandlers() map[string]func(any) (any, error) {
	h := extractionDefinitionHandlers(&connectAnalyticsRec{})
	maps.Copy(h, metricHandlers(&connectAnalyticsRec{}))
	return h
}

// A hard error in one analytics phase must not cost the other phase its rows.
func TestScanConnectAnalytics_PhasesIndependent(t *testing.T) {
	for _, failing := range connectAnalyticsCases() {
		t.Run(failing.name+" fails", func(t *testing.T) {
			st := newTestStore(t)
			h := connectAnalyticsHandlers()
			h[failing.listOp] = func(any) (any, error) { return nil, connectBadParam }
			total, inserted, err := scanConnectAnalytics(context.Background(), connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID)
			assertAPICode(t, err, "InvalidParameterException")
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want the other phase's 2, 2", total, inserted)
			}
			for _, other := range connectAnalyticsCases() {
				if other.typ == failing.typ {
					continue
				}
				if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
					t.Errorf("%s rows = %d, want 2", other.typ, n)
				}
			}
		})
	}
}

// One phase's throttle is demoted to a warning so that the other phase's hard
// error still reaches the dispatcher as a hard error.
func TestScanConnectAnalytics_TransientDoesNotMaskHardError(t *testing.T) {
	for _, throttled := range connectAnalyticsCases() {
		t.Run(throttled.name+" throttled", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "connect:"+throttled.listOp)
			h := connectAnalyticsHandlers()
			for _, c := range connectAnalyticsCases() {
				h[c.listOp] = func(any) (any, error) { return nil, connectBadParam }
			}
			h[throttled.listOp] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			_, _, err := scanConnectAnalytics(context.Background(), connectStubClient(h), connectInstances("i-1"), newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the demoted throttle", *warns)
			}
		})
	}
}

var connectAnalyticsRowIDs = map[string]string{
	TypeConnectExtractionDefinition: connectTestInstanceARN + "/extraction-definition/a",
	TypeConnectMetric:               connectTestInstanceARN + "/metric/a",
}

func connectTopLevelHandlers() map[string]func(any) (any, error) {
	h := connectFamilyHandlers()
	maps.Copy(h, connectChildOps)
	maps.Copy(h, connectAnalyticsHandlers())
	return h
}

func scanConnectTopLevel(t *testing.T, st *store.Store, h map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = connectStubConfig(h)
	return scanConnect(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanConnect_StoresAnalyticsRows(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := scanConnectTopLevel(t, st, connectTopLevelHandlers()); err != nil {
		t.Fatalf("scanConnect: %v", err)
	}
	for typ, id := range connectAnalyticsRowIDs {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
}

// An older family's hard error must not cost the analytics rows, and an
// analytics error must not cost the older families' rows.
func TestScanConnect_AnalyticsIndependentOfOlderFamilies(t *testing.T) {
	t.Run("older child phase fails", func(t *testing.T) {
		st := newTestStore(t)
		h := connectTopLevelHandlers()
		h["ListTestCases"] = func(any) (any, error) { return nil, connectBadParam }
		_, _, err := scanConnectTopLevel(t, st, h)
		assertAPICode(t, err, "InvalidParameterException")
		for typ, id := range connectAnalyticsRowIDs {
			if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
				t.Errorf("%s: no row %s", typ, id)
			}
		}
	})
	t.Run("older family fails", func(t *testing.T) {
		st := newTestStore(t)
		h := connectTopLevelHandlers()
		h["ListHoursOfOperations"] = func(any) (any, error) { return nil, connectBadParam }
		_, _, err := scanConnectTopLevel(t, st, h)
		assertAPICode(t, err, "InvalidParameterException")
		for typ, id := range connectAnalyticsRowIDs {
			if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
				t.Errorf("%s: no row %s", typ, id)
			}
		}
	})
	for _, c := range connectAnalyticsCases() {
		t.Run(c.name+" fails", func(t *testing.T) {
			st := newTestStore(t)
			h := connectTopLevelHandlers()
			h[c.listOp] = func(any) (any, error) { return nil, connectBadParam }
			_, _, err := scanConnectTopLevel(t, st, h)
			assertAPICode(t, err, "InvalidParameterException")
			for typ, id := range connectChildRowIDs {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("%s: older row %s lost", typ, id)
				}
			}
		})
	}
}

// An older family's throttle is demoted to a warning so that an analytics
// hard error still reaches the dispatcher as a hard error.
func TestScanConnect_OlderTransientDoesNotMaskAnalyticsError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "connect")
	h := connectTopLevelHandlers()
	h["ListTestCases"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListMetrics"] = func(any) (any, error) { return nil, connectBadParam }
	_, _, err := scanConnectTopLevel(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted throttle", *warns)
	}
}

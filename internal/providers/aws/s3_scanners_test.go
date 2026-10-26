package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithymw "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

var (
	s3Denied   = &smithy.GenericAPIError{Code: "AccessDenied", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: s3:List"}
	s3NoBucket = &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "The specified bucket does not exist"}
	s3Broken   = &smithy.GenericAPIError{Code: "InvalidRequest", Message: "bad"}
)

// s3StubConfig answers each S3 call from handlers keyed by operation name,
// each given the region the request was sent to and the op input; an op
// without a handler is denied. Handlers run on fan-out goroutines.
func s3StubConfig(handlers map[string]func(region string, in any) (any, error)) sdkaws.Config {
	stub := func(s *smithymw.Stack) error {
		return s.Initialize.Add(smithymw.InitializeMiddlewareFunc("discoS3Stub",
			func(ctx context.Context, in smithymw.InitializeInput, _ smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				h, ok := handlers[awsmw.GetOperationName(ctx)]
				if !ok {
					return smithymw.InitializeOutput{}, smithymw.Metadata{}, s3Denied
				}
				out, err := h(awsmw.GetRegion(ctx), in.Parameters)
				return smithymw.InitializeOutput{Result: out}, smithymw.Metadata{}, err
			}), smithymw.After)
	}
	return cfgWithStub(stub, testRegion)
}

// s3TestHomes maps each test bucket to its LocationConstraint; "" is us-east-1.
var s3TestHomes = map[string]s3types.BucketLocationConstraint{"b-east": "", "b-west": "us-west-2"}

func s3LocationHandler(_ string, in any) (any, error) {
	name := sv(in.(*s3.GetBucketLocationInput).Bucket)
	loc, ok := s3TestHomes[name]
	if !ok {
		return nil, s3NoBucket
	}
	return &s3.GetBucketLocationOutput{LocationConstraint: loc}, nil
}

func s3TestBuckets(names ...string) []s3types.Bucket {
	out := make([]s3types.Bucket, len(names))
	for i, n := range names {
		out[i] = s3types.Bucket{Name: sdkaws.String(n)}
	}
	return out
}

// s3ConfigKind adapts one ListBucket*Configurations op to the shared test
// cases: input reads the bucket and token from the op input, page builds an
// output holding a config per id.
type s3ConfigKind struct {
	op, typ, segment string
	scan             func(context.Context, *account, map[string]s3HomedBucket, *store.Store, string) (int, int, error)
	input            func(any) (bucket string, token *string)
	page             func(ids []string, truncated bool, next *string) any
}

var s3ConfigKinds = []s3ConfigKind{
	{
		op: "ListBucketAnalyticsConfigurations", typ: TypeS3BucketAnalyticsConfiguration, segment: "analytics-configuration",
		scan: scanS3AnalyticsConfigs,
		input: func(in any) (string, *string) {
			i := in.(*s3.ListBucketAnalyticsConfigurationsInput)
			return sv(i.Bucket), i.ContinuationToken
		},
		page: func(ids []string, truncated bool, next *string) any {
			out := &s3.ListBucketAnalyticsConfigurationsOutput{IsTruncated: &truncated, NextContinuationToken: next}
			for _, id := range ids {
				out.AnalyticsConfigurationList = append(out.AnalyticsConfigurationList, s3types.AnalyticsConfiguration{Id: sdkaws.String(id)})
			}
			return out
		},
	},
	{
		op: "ListBucketIntelligentTieringConfigurations", typ: TypeS3BucketIntelligentTieringConfiguration, segment: "intelligent-tiering-configuration",
		scan: scanS3IntelligentTieringConfigs,
		input: func(in any) (string, *string) {
			i := in.(*s3.ListBucketIntelligentTieringConfigurationsInput)
			return sv(i.Bucket), i.ContinuationToken
		},
		page: func(ids []string, truncated bool, next *string) any {
			out := &s3.ListBucketIntelligentTieringConfigurationsOutput{IsTruncated: &truncated, NextContinuationToken: next}
			for _, id := range ids {
				out.IntelligentTieringConfigurationList = append(out.IntelligentTieringConfigurationList,
					s3types.IntelligentTieringConfiguration{Id: sdkaws.String(id), Status: s3types.IntelligentTieringStatusEnabled})
			}
			return out
		},
	},
	{
		op: "ListBucketInventoryConfigurations", typ: TypeS3BucketInventoryConfiguration, segment: "inventory-configuration",
		scan: scanS3InventoryConfigs,
		input: func(in any) (string, *string) {
			i := in.(*s3.ListBucketInventoryConfigurationsInput)
			return sv(i.Bucket), i.ContinuationToken
		},
		page: func(ids []string, truncated bool, next *string) any {
			out := &s3.ListBucketInventoryConfigurationsOutput{IsTruncated: &truncated, NextContinuationToken: next}
			for _, id := range ids {
				out.InventoryConfigurationList = append(out.InventoryConfigurationList, s3types.InventoryConfiguration{Id: sdkaws.String(id)})
			}
			return out
		},
	},
	{
		op: "ListBucketMetricsConfigurations", typ: TypeS3BucketMetricsConfiguration, segment: "metrics-configuration",
		scan: scanS3MetricsConfigs,
		input: func(in any) (string, *string) {
			i := in.(*s3.ListBucketMetricsConfigurationsInput)
			return sv(i.Bucket), i.ContinuationToken
		},
		page: func(ids []string, truncated bool, next *string) any {
			out := &s3.ListBucketMetricsConfigurationsOutput{IsTruncated: &truncated, NextContinuationToken: next}
			for _, id := range ids {
				out.MetricsConfigurationList = append(out.MetricsConfigurationList, s3types.MetricsConfiguration{Id: sdkaws.String(id)})
			}
			return out
		},
	},
}

func s3ConfigNativeID(k s3ConfigKind, bucket, id string) string {
	return "arn:aws:s3:::" + bucket + "/" + k.segment + "/" + id
}

// s3ConfigRun is what runS3ConfigKind observed: it scans one kind over b-east
// and b-west, answering the kind's op from list (bucket, token → output or
// error; calls are serialised) and recording each request's region and token.
type s3ConfigRun struct {
	st      *store.Store
	total   int
	err     error
	regions map[string]string
	tokens  map[string][]string
	warns   *int
}

func runS3ConfigKind(t *testing.T, k s3ConfigKind, list func(bucket string, token *string) (any, error)) s3ConfigRun {
	t.Helper()
	run := s3ConfigRun{st: newTestStore(t), regions: map[string]string{}, tokens: map[string][]string{}}
	for name := range s3TestHomes {
		upsertTestResource(t, run.st, "aws", testAccountID, TypeS3Bucket, s3BucketARN("aws", name), "global", "{}")
	}
	run.warns = countOpWarnings(run.st, "s3:"+k.op)
	var mu sync.Mutex
	acct := newTestAccount(testAccountID)
	acct.cfg = s3StubConfig(map[string]func(string, any) (any, error){
		"GetBucketLocation": s3LocationHandler,
		k.op: func(region string, in any) (any, error) {
			bucket, token := k.input(in)
			mu.Lock()
			defer mu.Unlock()
			run.regions[bucket] = region
			run.tokens[bucket] = append(run.tokens[bucket], sv(token))
			return list(bucket, token)
		},
	})
	ctx := context.Background()
	homed, err := s3HomedBuckets(ctx, acct, s3.NewFromConfig(acct.cfg), s3TestBuckets("b-east", "b-west"), run.st)
	if err != nil {
		t.Fatalf("s3HomedBuckets: %v", err)
	}
	run.total, _, run.err = k.scan(ctx, acct, homed, run.st, testScanID)
	return run
}

func TestScanS3BucketConfigs_StoresEveryPageInHomeRegion(t *testing.T) {
	for _, k := range s3ConfigKinds {
		t.Run(k.op, func(t *testing.T) {
			run := runS3ConfigKind(t, k, func(bucket string, token *string) (any, error) {
				switch {
				case bucket == "b-east":
					// Not truncated, so the token must not be followed.
					return k.page([]string{"e1", ""}, false, sdkaws.String("stale")), nil
				case token == nil:
					return k.page([]string{"w1"}, true, sdkaws.String("t1")), nil
				default:
					return k.page([]string{"w2"}, false, nil), nil
				}
			})
			if run.err != nil {
				t.Fatalf("scan: %v", run.err)
			}
			rows := rowsByNativeID(t, run.st, k.typ)
			if run.total != 3 || len(rows) != 3 {
				t.Fatalf("total = %d, rows = %v; want 3 rows (empty id dropped)", run.total, rows)
			}
			for bucket, id := range map[string]string{"b-east": "e1", "b-west": "w2"} {
				r, ok := rows[s3ConfigNativeID(k, bucket, id)]
				if !ok {
					t.Fatalf("no row for %s/%s; got %v", bucket, id, rows)
				}
				if r.Type != k.typ || sv(r.Name) != id || sv(r.Region) != "global" {
					t.Errorf("row %s = type %q name %q region %q", r.NativeID, r.Type, sv(r.Name), sv(r.Region))
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs["Id"] != id {
					t.Errorf("attrs = %s (%v), want Id %q", r.AttributesJSON, err, id)
				}
				assertContainsNative(t, run.st, s3BucketARN("aws", bucket), r.NativeID)
			}
			if got := run.regions; got["b-east"] != "us-east-1" || got["b-west"] != "us-west-2" {
				t.Errorf("request regions = %v; want each bucket's home region", got)
			}
			if got := run.tokens["b-west"]; len(got) != 2 || got[1] != "t1" {
				t.Errorf("b-west tokens sent = %q; want [\"\" \"t1\"]", got)
			}
			if got := run.tokens["b-east"]; len(got) != 1 {
				t.Errorf("b-east tokens sent = %q; want one call", got)
			}
		})
	}
}

func TestScanS3BucketConfigs_Status(t *testing.T) {
	for _, k := range s3ConfigKinds {
		t.Run(k.op, func(t *testing.T) {
			run := runS3ConfigKind(t, k, func(string, *string) (any, error) { return k.page([]string{"c"}, false, nil), nil })
			if run.err != nil {
				t.Fatalf("scan: %v", run.err)
			}
			r := rowsByNativeID(t, run.st, k.typ)[s3ConfigNativeID(k, "b-east", "c")]
			want := ""
			if k.typ == TypeS3BucketIntelligentTieringConfiguration {
				want = string(s3types.IntelligentTieringStatusEnabled)
			}
			if got := r.Status; sv(got) != want || (want == "" && got != nil) {
				t.Errorf("status = %v; want %q (unset when the element has none)", got, want)
			}
		})
	}
}

func TestScanS3BucketConfigs_Empty(t *testing.T) {
	for _, k := range s3ConfigKinds {
		t.Run(k.op, func(t *testing.T) {
			run := runS3ConfigKind(t, k, func(string, *string) (any, error) { return k.page(nil, false, nil), nil })
			if run.err != nil || run.total != 0 || len(rowsByNativeID(t, run.st, k.typ)) != 0 {
				t.Errorf("total = %d, err = %v; want 0 rows and nil", run.total, run.err)
			}
		})
	}
}

// TestScanS3BucketConfigs_RepeatedTokenFails pins that a bucket whose listing
// repeats its token fails the scan with errRepeatedNextToken, keeps the
// configurations it read, and leaves its sibling bucket's configurations
// stored.
func TestScanS3BucketConfigs_RepeatedTokenFails(t *testing.T) {
	for _, k := range s3ConfigKinds {
		t.Run(k.op, func(t *testing.T) {
			calls := map[string]int{}
			run := runS3ConfigKind(t, k, func(bucket string, _ *string) (any, error) {
				if bucket != "b-west" {
					return k.page([]string{"e"}, false, nil), nil
				}
				calls[bucket]++
				// A third call means the repeated token was followed; end the
				// loop so the failure is an assertion, not a hang.
				return k.page([]string{fmt.Sprintf("w%d", calls[bucket])}, calls[bucket] < 3, sdkaws.String("same")), nil
			})
			if !errors.Is(run.err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", run.err)
			}
			if got := run.tokens["b-west"]; len(got) != 2 {
				t.Errorf("b-west calls = %q; want 2 (stop when the token repeats)", got)
			}
			assertS3ConfigsStored(t, run.st, k, s3ConfigNativeID(k, "b-west", "w1"), s3ConfigNativeID(k, "b-west", "w2"), s3ConfigNativeID(k, "b-east", "e"))
		})
	}
}

// TestScanS3BucketConfigs_TruncatedWithoutTokenFails pins that a page marked
// truncated with no continuation token fails with errTruncatedWithoutToken
// instead of reading as the last page, and that its rows are kept.
func TestScanS3BucketConfigs_TruncatedWithoutTokenFails(t *testing.T) {
	for _, k := range s3ConfigKinds {
		t.Run(k.op, func(t *testing.T) {
			run := runS3ConfigKind(t, k, func(bucket string, _ *string) (any, error) {
				if bucket != "b-west" {
					return k.page([]string{"e"}, false, nil), nil
				}
				return k.page([]string{"w1"}, true, nil), nil
			})
			if !errors.Is(run.err, errTruncatedWithoutToken) {
				t.Fatalf("err = %v, want errTruncatedWithoutToken", run.err)
			}
			if got := run.tokens["b-west"]; len(got) != 1 {
				t.Errorf("b-west calls = %q; want 1", got)
			}
			assertS3ConfigsStored(t, run.st, k, s3ConfigNativeID(k, "b-west", "w1"), s3ConfigNativeID(k, "b-east", "e"))
		})
	}
}

func assertS3ConfigsStored(t *testing.T, st *store.Store, k s3ConfigKind, want ...string) {
	t.Helper()
	rows := rowsByNativeID(t, st, k.typ)
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("%s not stored; stored %d rows", id, len(rows))
		}
	}
}

func TestScanS3BucketConfigs_PerBucketErrors(t *testing.T) {
	cases := []struct {
		name             string
		eastErr, westErr error
		wantErr          bool
		wantWarns        int
		wantEast         bool
	}{
		{name: "bucket gone skipped silently", westErr: s3NoBucket, wantEast: true},
		{name: "denied keeps siblings", westErr: s3Denied, wantWarns: 1, wantEast: true},
		{name: "denied everywhere warns once", eastErr: s3Denied, westErr: s3Denied, wantWarns: 1},
		{name: "other error propagates", westErr: s3Broken, wantErr: true},
	}
	for _, k := range s3ConfigKinds {
		for _, tc := range cases {
			t.Run(k.op+"/"+tc.name, func(t *testing.T) {
				run := runS3ConfigKind(t, k, func(bucket string, _ *string) (any, error) {
					if bucket == "b-west" {
						return nil, tc.westErr
					}
					if tc.eastErr != nil {
						return nil, tc.eastErr
					}
					return k.page([]string{"e1"}, false, nil), nil
				})
				if tc.wantErr {
					if !isAPIErrorCode(run.err, "InvalidRequest") {
						t.Fatalf("err = %v; want the InvalidRequest error", run.err)
					}
					return
				}
				if run.err != nil {
					t.Fatalf("scan: %v", run.err)
				}
				if *run.warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *run.warns, tc.wantWarns)
				}
				if _, ok := rowsByNativeID(t, run.st, k.typ)[s3ConfigNativeID(k, "b-east", "e1")]; ok != tc.wantEast {
					t.Errorf("b-east row stored = %v; want %v", ok, tc.wantEast)
				}
			})
		}
	}
}

func TestS3HomedBuckets_LocationErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{name: "bucket gone skipped silently", err: s3NoBucket},
		{name: "denied warns once", err: s3Denied, wantWarns: 1},
		{name: "other error propagates", err: s3Broken, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "s3:GetBucketLocation")
			acct := newTestAccount(testAccountID)
			acct.cfg = s3StubConfig(map[string]func(string, any) (any, error){
				"GetBucketLocation": func(region string, in any) (any, error) {
					if name := sv(in.(*s3.GetBucketLocationInput).Bucket); name == "x1" || name == "x2" {
						return nil, tc.err
					}
					return s3LocationHandler(region, in)
				},
			})
			homed, err := s3HomedBuckets(context.Background(), acct, s3.NewFromConfig(acct.cfg), s3TestBuckets("b-east", "x1", "b-west", "x2"), st)
			if tc.wantErr {
				if !isAPIErrorCode(err, "InvalidRequest") {
					t.Fatalf("err = %v; want the InvalidRequest error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("s3HomedBuckets: %v", err)
			}
			if len(homed) != 2 || homed["b-east"].name != "b-east" || homed["b-west"].name != "b-west" {
				t.Errorf("homed = %v; want b-east and b-west only", homed)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// s3ServiceHandlers answers every call scanS3 makes for b-east and b-west:
// each bucket has a policy, b-east a default encryption, and one configuration
// "c" of each kind; failOp names a configuration op that fails instead.
func s3ServiceHandlers(failOp string) map[string]func(string, any) (any, error) {
	h := map[string]func(string, any) (any, error){
		"ListBuckets": func(string, any) (any, error) {
			return &s3.ListBucketsOutput{Buckets: s3TestBuckets("b-east", "b-west")}, nil
		},
		"GetBucketLocation": s3LocationHandler,
		"GetBucketPolicy": func(string, any) (any, error) {
			return &s3.GetBucketPolicyOutput{Policy: sdkaws.String("{}")}, nil
		},
		"GetBucketEncryption": func(_ string, in any) (any, error) {
			if sv(in.(*s3.GetBucketEncryptionInput).Bucket) != "b-east" {
				return nil, &smithy.GenericAPIError{Code: "ServerSideEncryptionConfigurationNotFoundError"}
			}
			return &s3.GetBucketEncryptionOutput{ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{}}, nil
		},
	}
	for _, k := range s3ConfigKinds {
		h[k.op] = func(string, any) (any, error) {
			if k.op == failOp {
				return nil, s3Broken
			}
			return k.page([]string{"c"}, false, nil), nil
		}
	}
	return h
}

func TestScanS3_StoresBucketConfigs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = s3StubConfig(s3ServiceHandlers(""))
	if _, _, err := scanS3(context.Background(), acct, st, testScanID); err != nil {
		t.Fatalf("scanS3: %v", err)
	}
	for _, k := range s3ConfigKinds {
		for bucket := range s3TestHomes {
			want := s3ConfigNativeID(k, bucket, "c")
			if _, ok := rowsByNativeID(t, st, k.typ)[want]; !ok {
				t.Errorf("no %s row %s", k.typ, want)
			}
			assertContainsNative(t, st, s3BucketARN("aws", bucket), want)
		}
	}
}

// A failing configuration op must leave the bucket, policy and encryption
// phases' results in place and must not stop the other configuration kinds.
func TestScanS3_ConfigFailureKeepsOtherPhases(t *testing.T) {
	for _, failing := range s3ConfigKinds {
		t.Run(failing.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = s3StubConfig(s3ServiceHandlers(failing.op))
			_, _, err := scanS3(context.Background(), acct, st, testScanID)
			if !isAPIErrorCode(err, "InvalidRequest") {
				t.Fatalf("err = %v; want the failing op's InvalidRequest", err)
			}
			if n := len(rowsByNativeID(t, st, TypeS3Bucket)); n != 2 {
				t.Errorf("buckets = %d; want 2", n)
			}
			if n := len(rowsByNativeID(t, st, TypeS3BucketPolicy)); n != 2 {
				t.Errorf("bucket policies = %d; want 2", n)
			}
			if _, ok := acct.s3BucketEncryption["b-east"]; !ok {
				t.Error("b-east encryption not collected")
			}
			for _, k := range s3ConfigKinds {
				want := 2
				if k.op == failing.op {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, k.typ)); n != want {
					t.Errorf("%s rows = %d; want %d", k.typ, n, want)
				}
			}
		})
	}
}

func TestScanS3BucketConfigs_LegacyEULocationUsesEUWest1(t *testing.T) {
	st := newTestStore(t)
	var (
		mu      sync.Mutex
		regions []string
	)
	acct := newTestAccount(testAccountID)
	acct.cfg = s3StubConfig(map[string]func(string, any) (any, error){
		"GetBucketLocation": func(string, any) (any, error) {
			return &s3.GetBucketLocationOutput{LocationConstraint: s3types.BucketLocationConstraintEu}, nil
		},
		"ListBucketMetricsConfigurations": func(region string, _ any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			regions = append(regions, region)
			return &s3.ListBucketMetricsConfigurationsOutput{}, nil
		},
	})
	ctx := context.Background()
	homed, err := s3HomedBuckets(ctx, acct, s3.NewFromConfig(acct.cfg), s3TestBuckets("b-eu"), st)
	if err != nil {
		t.Fatalf("s3HomedBuckets: %v", err)
	}
	if _, _, err := scanS3MetricsConfigs(ctx, acct, homed, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(regions) != 1 || regions[0] != "eu-west-1" {
		t.Errorf("request regions = %q; want [eu-west-1] for LocationConstraint EU", regions)
	}
}

func TestScanS3MetricsConfigs_NamesFilterMember(t *testing.T) {
	metrics := s3ConfigKinds[3]
	run := runS3ConfigKind(t, metrics, func(bucket string, _ *string) (any, error) {
		if bucket != "b-east" {
			return &s3.ListBucketMetricsConfigurationsOutput{}, nil
		}
		return &s3.ListBucketMetricsConfigurationsOutput{MetricsConfigurationList: []s3types.MetricsConfiguration{
			{Id: sdkaws.String("by-prefix"), Filter: &s3types.MetricsFilterMemberPrefix{Value: "logs/"}},
			{Id: sdkaws.String("by-ap"), Filter: &s3types.MetricsFilterMemberAccessPointArn{Value: "arn:aws:s3:us-east-1:123456789012:accesspoint/ap"}},
		}}, nil
	})
	if run.err != nil {
		t.Fatalf("scan: %v", run.err)
	}
	rows := rowsByNativeID(t, run.st, TypeS3BucketMetricsConfiguration)
	for id, want := range map[string][2]string{
		"by-prefix": {"Prefix", "logs/"},
		"by-ap":     {"AccessPointArn", "arn:aws:s3:us-east-1:123456789012:accesspoint/ap"},
	} {
		r, ok := rows[s3ConfigNativeID(metrics, "b-east", id)]
		if !ok {
			t.Fatalf("no row for %s; got %v", id, rows)
		}
		var attrs struct {
			ID           string `json:"Id"`
			Filter       struct{ Value string }
			FilterMember string `json:"filterMember"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
		}
		if attrs.FilterMember != want[0] || attrs.Filter.Value != want[1] || attrs.ID != id {
			t.Errorf("%s attrs = %s; want filterMember %q, Filter.Value %q, Id kept", id, r.AttributesJSON, want[0], want[1])
		}
	}
}

// s3Answer is the error S3 returns for an HTTP answer with status code and
// headers, wrapping cause (nil for an anonymous one).
func s3Answer(status int, header http.Header, cause error) error {
	if cause == nil {
		cause = errors.New(http.StatusText(status))
	}
	return &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status, Header: header}}, Err: cause}
}

// s3ForbiddenNoRegion is S3 answering HeadBucket without naming the region.
var s3ForbiddenNoRegion = s3Answer(http.StatusForbidden, http.Header{}, nil)

// An empty LocationConstraint is us-east-1 for aws only; elsewhere the region
// comes from HeadBucket (its BucketRegion, or the x-amz-bucket-region header of
// the 301 S3 answers from another region), and an answer naming none is
// unknown, never guessed. The legacy "EU" constraint is eu-west-1 and any
// other is itself.
func TestS3BucketRegion_PerPartition(t *testing.T) {
	headOK := func(any) (any, error) { return &s3.HeadBucketOutput{BucketRegion: sdkaws.String("us-gov-east-1")}, nil }
	headMoved := func(any) (any, error) {
		return nil, s3Answer(http.StatusMovedPermanently, http.Header{"X-Amz-Bucket-Region": {"cn-north-1"}}, nil)
	}
	headNoRegion := func(any) (any, error) { return nil, s3ForbiddenNoRegion }
	headNoBucketRegion := func(any) (any, error) { return &s3.HeadBucketOutput{}, nil }
	cases := []struct {
		name       string
		partition  string
		constraint s3types.BucketLocationConstraint
		head       func(any) (any, error) // nil: no HeadBucket handler
		want       string                 // "": region unknown
	}{
		{"aws empty is us-east-1", "aws", "", nil, "us-east-1"},
		{"gov empty reads HeadBucket", "aws-us-gov", "", headOK, "us-gov-east-1"},
		{"cn empty reads the 301 header", "aws-cn", "", headMoved, "cn-north-1"},
		{"gov empty, S3 answers without a region", "aws-us-gov", "", headNoRegion, ""},
		{"gov empty, HeadBucket succeeds without BucketRegion", "aws-us-gov", "", headNoBucketRegion, ""},
		{"aws legacy EU", "aws", s3types.BucketLocationConstraintEu, nil, "eu-west-1"},
		{"gov explicit", "aws-us-gov", "us-gov-east-1", nil, "us-gov-east-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handlers := map[string]func(any) (any, error){
				"GetBucketLocation": func(any) (any, error) {
					return &s3.GetBucketLocationOutput{LocationConstraint: c.constraint}, nil
				},
			}
			if c.head != nil {
				handlers["HeadBucket"] = c.head
			}
			cfg := opStubConfig(handlers, errors.New("test did not expect this call"))
			got, err := s3BucketRegion(context.Background(), testAccountIn(c.partition), s3.NewFromConfig(cfg), "b")
			if c.want == "" {
				var unknown *s3BucketRegionUnknownError
				if !errors.As(err, &unknown) || unknown.Bucket != "b" {
					t.Errorf("s3BucketRegion = %q, %v; want *s3BucketRegionUnknownError for b", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("s3BucketRegion(%s, %q) = %q, %v; want %q, nil", c.partition, c.constraint, got, err, c.want)
			}
		})
	}
}

// A HeadBucket failure that is not S3 answering without a region comes back
// raw, so the caller classifies it as it does every other per-bucket error: a
// cancelled scan, a transient failure, a vanished bucket, or no HTTP answer.
func TestS3BucketRegion_RawHeadBucketErrors(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		err   error
		check func(error) bool
	}{
		{"context cancelled", canceled, s3ForbiddenNoRegion, func(err error) bool { return err != nil }},
		{"transient", context.Background(), s3Answer(http.StatusServiceUnavailable, http.Header{}, &smithy.GenericAPIError{Code: "ServiceUnavailable"}), isTransientNetworkError},
		{"bucket gone", context.Background(), s3Answer(http.StatusNotFound, http.Header{}, &smithy.GenericAPIError{Code: "NotFound"}), s3BucketRegionGone},
		{"no HTTP answer", context.Background(), errors.New("dial failed"), func(err error) bool { return err != nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := opStubConfig(map[string]func(any) (any, error){
				"GetBucketLocation": func(any) (any, error) { return &s3.GetBucketLocationOutput{}, nil },
				"HeadBucket":        func(any) (any, error) { return nil, c.err },
			}, errors.New("test did not expect this call"))
			_, err := s3BucketRegion(c.ctx, testAccountIn("aws-us-gov"), s3.NewFromConfig(cfg), "b")
			var unknown *s3BucketRegionUnknownError
			if errors.As(err, &unknown) || !c.check(err) {
				t.Errorf("s3BucketRegion err = %v (%T); want the raw error, not *s3BucketRegionUnknownError", err, err)
			}
		})
	}
}

// A deadline hit while reading a bucket's region reaches the dispatcher raw
// through every phase that reads it, so the scan is classified outcomeDeadline,
// not a skipped bucket.
func TestScanS3_HeadBucketDeadlineIsDeadline(t *testing.T) {
	acct := testAccountIn("aws-us-gov")
	h := s3ServiceHandlers("")
	h["GetBucketLocation"] = func(string, any) (any, error) { return &s3.GetBucketLocationOutput{}, nil }
	h["HeadBucket"] = func(string, any) (any, error) { return nil, context.DeadlineExceeded }
	acct.cfg = s3StubConfig(h)
	client := s3.NewFromConfig(acct.cfg)
	buckets := s3TestBuckets("b-east")
	ctx := context.Background()
	st := newTestStore(t)
	sites := map[string]func() error{
		"scanS3": func() error { _, _, err := scanS3(ctx, acct, st, testScanID); return err },
		"scanS3BucketPolicies": func() error {
			_, _, err := scanS3BucketPolicies(ctx, acct, client, buckets, st, testScanID)
			return err
		},
		"scanS3BucketEncryptions": func() error { return scanS3BucketEncryptions(ctx, acct, client, buckets) },
		"s3HomedBuckets":          func() error { _, err := s3HomedBuckets(ctx, acct, client, buckets, st); return err },
	}
	for name, run := range sites {
		if err := run(); classifyServiceError(err) != outcomeDeadline {
			t.Errorf("%s: classifyServiceError(%v) = %v; want outcomeDeadline", name, err, classifyServiceError(err))
		}
	}
}

// A bucket that vanished before its region was read (HeadBucket answers 404
// NotFound) is skipped silently by every phase that reads the region, as a
// vanished bucket is everywhere else.
func TestScanS3_HeadBucketGoneIsSilent(t *testing.T) {
	acct := testAccountIn("aws-us-gov")
	h := s3ServiceHandlers("")
	h["GetBucketLocation"] = func(string, any) (any, error) { return &s3.GetBucketLocationOutput{}, nil }
	h["HeadBucket"] = func(string, any) (any, error) {
		return nil, s3Answer(http.StatusNotFound, http.Header{}, &smithy.GenericAPIError{Code: "NotFound"})
	}
	acct.cfg = s3StubConfig(h)
	client := s3.NewFromConfig(acct.cfg)
	buckets := s3TestBuckets("b-east")
	ctx := context.Background()
	st := newTestStore(t)
	var (
		mu    sync.Mutex
		warns int
	)
	st.OnWarn = func(store.ScanWarning) { mu.Lock(); warns++; mu.Unlock() }
	sites := map[string]func() error{
		"scanS3BucketPolicies": func() error {
			_, _, err := scanS3BucketPolicies(ctx, acct, client, buckets, st, testScanID)
			return err
		},
		"scanS3BucketEncryptions": func() error { return scanS3BucketEncryptions(ctx, acct, client, buckets) },
		"s3HomedBuckets":          func() error { _, err := s3HomedBuckets(ctx, acct, client, buckets, st); return err },
	}
	for name, run := range sites {
		if err := run(); err != nil {
			t.Errorf("%s: err = %v; want nil (vanished bucket skipped)", name, err)
		}
	}
	if warns != 0 {
		t.Errorf("warnings = %d; want 0", warns)
	}
}

// Outside aws, a bucket whose region cannot be read keeps its row, gets one
// warning naming it, and has no policy, encryption or configuration read;
// a sibling bucket with a known region is read in full.
func TestScanS3_UnknownRegionKeepsBucketSkipsReads(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "s3:HeadBucket")
	acct := testAccountIn("aws-us-gov")
	h := s3ServiceHandlers("")
	h["GetBucketLocation"] = func(_ string, in any) (any, error) {
		if sv(in.(*s3.GetBucketLocationInput).Bucket) == "b-west" {
			return &s3.GetBucketLocationOutput{LocationConstraint: "us-gov-east-1"}, nil
		}
		return &s3.GetBucketLocationOutput{}, nil
	}
	h["HeadBucket"] = func(string, any) (any, error) { return nil, s3ForbiddenNoRegion }
	acct.cfg = s3StubConfig(h)
	if _, _, err := scanS3(context.Background(), acct, st, testScanID); err != nil {
		t.Fatalf("scanS3: %v", err)
	}
	// The account is in aws-us-gov, so every S3 id the scan builds is too.
	const east, west = "arn:aws-us-gov:s3:::b-east", "arn:aws-us-gov:s3:::b-west"
	buckets := rowsByNativeID(t, st, TypeS3Bucket)
	_, okEast := buckets[east]
	_, okWest := buckets[west]
	if !okEast || !okWest || len(buckets) != 2 {
		t.Errorf("bucket rows = %v; want exactly %s and %s (the unknown-region bucket is kept)", slices.Sorted(maps.Keys(buckets)), east, west)
	}
	policies := rowsByNativeID(t, st, TypeS3BucketPolicy)
	if _, ok := policies[west+"/policy"]; !ok || len(policies) != 1 {
		t.Errorf("bucket policy rows = %v; want only %s", slices.Sorted(maps.Keys(policies)), west+"/policy")
	}
	if _, ok := acct.s3BucketEncryption["b-east"]; ok {
		t.Error("b-east encryption was read; its region is unknown")
	}
	for _, k := range s3ConfigKinds {
		rows := rowsByNativeID(t, st, k.typ)
		want := west + "/" + k.segment + "/c"
		if _, ok := rows[want]; !ok || len(rows) != 1 {
			t.Errorf("%s rows = %v; want only %s", k.typ, slices.Sorted(maps.Keys(rows)), want)
			continue
		}
		assertContainsNative(t, st, west, want)
	}
	if *warns != 1 {
		t.Errorf("s3:HeadBucket warnings = %d; want 1", *warns)
	}
}

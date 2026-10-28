package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	lambdacoretypes "github.com/aws/aws-sdk-go-v2/service/lambdacore/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdamicrovmstypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func lambdaVersion(fn, version string) lambdatypes.FunctionConfiguration {
	return lambdatypes.FunctionConfiguration{
		FunctionName: sdkaws.String(fn), Version: sdkaws.String(version),
		FunctionArn: sdkaws.String(lambdaFnARN(fn) + ":" + version),
	}
}

// Provisioned concurrency needs a published version, so only functions with
// one, or whose versions could not be listed, are handed to that phase. A
// function deleted since ListFunctions is skipped, not fatal.
func TestScanLambdaVersions_ProvisionedConcurrencyCandidates(t *testing.T) {
	st := newTestStore(t)
	stub := &stubLambdaPerFn{
		errFor: map[string]error{
			"denied": apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: lambda:ListVersionsByFunction"),
			"gone":   apiErr("ResourceNotFoundException", "Function not found"),
		},
		versions: map[string][][]lambdatypes.FunctionConfiguration{
			"latest-only": {{lambdaVersion("latest-only", "$LATEST")}},
			"published":   {{lambdaVersion("published", "$LATEST")}, {lambdaVersion("published", "1")}},
		},
	}
	var fns []lambdaFunctionSummary
	for _, name := range []string{"gone", "latest-only", "published", "denied"} {
		fns = append(fns, lambdaFunctionSummary{name: name, arn: lambdaFnARN(name)})
	}

	candidates, total, _, err := scanLambdaVersions(context.Background(), stub, newTestAccount(testAccountID), fns, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var names []string
	for _, c := range candidates {
		names = append(names, c.name)
	}
	if want := []string{"published", "denied"}; !slices.Equal(names, want) {
		t.Errorf("candidates=%v, want %v", names, want)
	}
	if total != 1 {
		t.Errorf("total=%d, want 1 (the published version; $LATEST skipped)", total)
	}
	if _, ok := lmvRows(t, st, TypeLambdaVersion)[lambdaFnARN("published")+":1"]; !ok {
		t.Error("published version row not stored")
	}
}

var (
	lambdaDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: lambda:ListTags")
	lambdaGone     = apiErr("ResourceNotFoundException", "Function not found")
	lambdaThrottle = apiErr("TooManyRequestsException", "Rate exceeded")
	lambdaInvalid  = apiErr("InvalidParameterValueException", "bad")
)

const lambdaImageURI = "123456789012.dkr.ecr.us-east-1.amazonaws.com/app:1"

// lambdaCodeLocation is GetFunction's Code.Location: a presigned URL whose
// signature disco must never store.
const lambdaCodeLocation = "https://awslambda-us-east-1-tasks.s3.us-east-1.amazonaws.com/snapshots/fn?X-Amz-Signature=presigned-secret"

// lambdaCalls is the state of one stubbed Lambda scan: what it was asked and
// what it answers. Every "-tagged" resource carries the tag type=<its kind>;
// every other has none.
type lambdaCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	pageSizes  map[string][]int32
	tagARNs    []string // sent to the Lambda client's ListTags
	imageARNs  []string // sent to the Lambda MicroVMs client's ListTags
	getARNs    []string // sent to GetFunction
	versionFns []string
	tagErrs    map[string]error
	// getErr fails every GetFunction call; getErrs, keyed by ARN, one.
	getErr  error
	getErrs map[string]error
	// tagsError, keyed by ARN, makes GetFunction answer a TagsError in place
	// of the function's tags.
	tagsError map[string]bool
	// noARN adds an element without an ARN to the first page of these ops.
	noARN map[string]bool
	// listErr, keyed by op, fails the op's page after its last.
	listErr map[string]error
}

func newLambdaCalls() *lambdaCalls {
	return &lambdaCalls{
		ops: map[string]int{}, pageSizes: map[string][]int32{}, tagErrs: map[string]error{}, noARN: map[string]bool{}, listErr: map[string]error{},
		getErrs: map[string]error{}, tagsError: map[string]bool{},
	}
}

func (c *lambdaCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if n := bacField[*int32](in, "MaxItems"); n != nil {
		c.pageSizes[op] = append(c.pageSizes[op], *n)
	}
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.pageSizes[op] = append(c.pageSizes[op], *n)
	}
}

func (c *lambdaCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// lambdaTaggedARN is the ARN of the tagged resource of kind; lambdaBareARN of
// the untagged one.
func lambdaTaggedARN(kind string) string { return lmvARN(kind, kind+"-tagged") }
func lambdaBareARN(kind string) string   { return lmvARN(kind, kind+"-bare") }

func (c *lambdaCalls) tagsOf(arn string, sent *[]string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*sent = append(*sent, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return lambdaStubTags(arn), nil
}

// lambdaStubTags is the tags every stub answers for arn.
func lambdaStubTags(arn string) map[string]string {
	for _, kind := range []string{"function", "code-signing-config", "capacity-provider", "event-source-mapping", "network-connector", "microvm-image"} {
		if arn == lambdaTaggedARN(kind) {
			return map[string]string{"type": kind}
		}
	}
	return nil
}

// lambdaPages serves pages after recording the call; the op's listErr fails
// the page after the last, and a noARN op gets an ARN-less item first.
func lambdaPages[T any](c *lambdaCalls, op string, in any, pages [][]T, noARN T) ([]T, *string, error) {
	c.record(op, in)
	if c.noARN[op] && len(pages) > 0 {
		pages = slices.Clone(pages)
		pages[0] = append([]T{noARN}, pages[0]...)
	}
	return stubPage(pages, bacField[*string](in, "Marker"), c.listErr[op])
}

func lambdaFn(name string, pkg lambdatypes.PackageType) lambdatypes.FunctionConfiguration {
	arn := lmvARN("function", name)
	return lambdatypes.FunctionConfiguration{FunctionName: sdkaws.String(name), FunctionArn: &arn, PackageType: pkg}
}

// lambdaHandlers serves a region holding, per tagged kind, one tagged and one
// untagged resource: functions function-tagged (with a published version 1,
// alias live and provisioned concurrency on version 1) and an image function
// "image-fn" on the first page, function-bare on the second; and one MicroVM
// image version, MicroVM and managed image.
func lambdaHandlers(c *lambdaCalls) map[string]func(in any) (any, error) {
	none := func(op string, out any) func(in any) (any, error) {
		return func(in any) (any, error) {
			c.record(op, in)
			return out, nil
		}
	}
	tagged := lmvARN("function", "function-tagged")
	return map[string]func(in any) (any, error){
		"ListFunctions": func(in any) (any, error) {
			items, next, err := lambdaPages(c, "ListFunctions", in, [][]lambdatypes.FunctionConfiguration{
				{lambdaFn("function-tagged", lambdatypes.PackageTypeZip), lambdaFn("image-fn", lambdatypes.PackageTypeImage)},
				{lambdaFn("function-bare", lambdatypes.PackageTypeZip)},
			}, lambdatypes.FunctionConfiguration{FunctionName: sdkaws.String("no-arn")})
			if err != nil {
				return nil, err
			}
			return &lambda.ListFunctionsOutput{Functions: items, NextMarker: next}, nil
		},
		"GetFunction": func(in any) (any, error) {
			c.record("GetFunction", in)
			arn := sv(in.(*lambda.GetFunctionInput).FunctionName)
			c.mu.Lock()
			c.getARNs = append(c.getARNs, arn)
			err := cmp.Or(c.getErr, c.getErrs[arn])
			tagsError := c.tagsError[arn]
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			out := &lambda.GetFunctionOutput{
				Configuration: &lambdatypes.FunctionConfiguration{FunctionArn: &arn, Description: sdkaws.String("from GetFunction")},
				Code:          &lambdatypes.FunctionCodeLocation{ImageUri: sdkaws.String(lambdaImageURI), Location: sdkaws.String(lambdaCodeLocation)},
				Tags:          lambdaStubTags(arn),
			}
			if tagsError {
				out.Tags = nil
				out.TagsError = &lambdatypes.TagsError{ErrorCode: sdkaws.String("AccessDeniedException"), Message: sdkaws.String("tags denied")}
			}
			return out, nil
		},
		"ListTags": func(in any) (any, error) {
			switch in := in.(type) {
			case *lambda.ListTagsInput:
				c.record("ListTags", in)
				tags, err := c.tagsOf(sv(in.Resource), &c.tagARNs)
				if err != nil {
					return nil, err
				}
				return &lambda.ListTagsOutput{Tags: tags}, nil
			case *lambdamicrovms.ListTagsInput:
				c.record("microvms:ListTags", in)
				tags, err := c.tagsOf(sv(in.Resource), &c.imageARNs)
				if err != nil {
					return nil, err
				}
				return &lambdamicrovms.ListTagsOutput{Tags: tags}, nil
			}
			return nil, errors.New("unexpected ListTags input")
		},
		"ListVersionsByFunction": func(in any) (any, error) {
			fn := sv(in.(*lambda.ListVersionsByFunctionInput).FunctionName)
			c.record("ListVersionsByFunction", in)
			c.mu.Lock()
			c.versionFns = append(c.versionFns, fn)
			c.mu.Unlock()
			out := &lambda.ListVersionsByFunctionOutput{}
			if fn == "function-tagged" {
				out.Versions = []lambdatypes.FunctionConfiguration{
					{FunctionName: &fn, Version: sdkaws.String("$LATEST"), FunctionArn: sdkaws.String(tagged + ":$LATEST")},
					{FunctionName: &fn, Version: sdkaws.String("1"), FunctionArn: sdkaws.String(tagged + ":1")},
				}
			}
			return out, nil
		},
		"ListAliases": func(in any) (any, error) {
			c.record("ListAliases", in)
			out := &lambda.ListAliasesOutput{}
			if sv(in.(*lambda.ListAliasesInput).FunctionName) == "function-tagged" {
				out.Aliases = []lambdatypes.AliasConfiguration{{Name: sdkaws.String("live"), AliasArn: sdkaws.String(tagged + ":live"), FunctionVersion: sdkaws.String("1")}}
			}
			return out, nil
		},
		"ListFunctionEventInvokeConfigs": none("ListFunctionEventInvokeConfigs", &lambda.ListFunctionEventInvokeConfigsOutput{}),
		"ListFunctionUrlConfigs":         none("ListFunctionUrlConfigs", &lambda.ListFunctionUrlConfigsOutput{}),
		"GetPolicy": func(in any) (any, error) {
			c.record("GetPolicy", in)
			return nil, lambdaGone
		},
		"ListCodeSigningConfigs": func(in any) (any, error) {
			items, next, err := lambdaPages(c, "ListCodeSigningConfigs", in, [][]lambdatypes.CodeSigningConfig{{
				{CodeSigningConfigArn: sdkaws.String(lambdaTaggedARN("code-signing-config")), CodeSigningConfigId: sdkaws.String("csc-tagged")},
				{CodeSigningConfigArn: sdkaws.String(lambdaBareARN("code-signing-config")), CodeSigningConfigId: sdkaws.String("csc-bare")},
			}}, lambdatypes.CodeSigningConfig{CodeSigningConfigId: sdkaws.String("csc-no-arn")})
			if err != nil {
				return nil, err
			}
			return &lambda.ListCodeSigningConfigsOutput{CodeSigningConfigs: items, NextMarker: next}, nil
		},
		"ListCapacityProviders": func(in any) (any, error) {
			items, next, err := lambdaPages(c, "ListCapacityProviders", in, [][]lambdatypes.CapacityProvider{{
				{CapacityProviderArn: sdkaws.String(lambdaTaggedARN("capacity-provider")), State: lambdatypes.CapacityProviderStateActive},
				{CapacityProviderArn: sdkaws.String(lambdaBareARN("capacity-provider")), State: lambdatypes.CapacityProviderStateActive},
			}}, lambdatypes.CapacityProvider{State: lambdatypes.CapacityProviderStateActive})
			if err != nil {
				return nil, err
			}
			return &lambda.ListCapacityProvidersOutput{CapacityProviders: items, NextMarker: next}, nil
		},
		"ListEventSourceMappings": func(in any) (any, error) {
			items, next, err := lambdaPages(c, "ListEventSourceMappings", in, [][]lambdatypes.EventSourceMappingConfiguration{{
				{EventSourceMappingArn: sdkaws.String(lambdaTaggedARN("event-source-mapping")), UUID: sdkaws.String("esm-tagged"), State: sdkaws.String("Enabled")},
				{EventSourceMappingArn: sdkaws.String(lambdaBareARN("event-source-mapping")), UUID: sdkaws.String("esm-bare"), State: sdkaws.String("Enabled")},
			}}, lambdatypes.EventSourceMappingConfiguration{UUID: sdkaws.String("esm-no-arn")})
			if err != nil {
				return nil, err
			}
			return &lambda.ListEventSourceMappingsOutput{EventSourceMappings: items, NextMarker: next}, nil
		},
		"ListLayers": none("ListLayers", &lambda.ListLayersOutput{}),
		"ListProvisionedConcurrencyConfigs": func(in any) (any, error) {
			c.record("ListProvisionedConcurrencyConfigs", in)
			out := &lambda.ListProvisionedConcurrencyConfigsOutput{}
			if sv(in.(*lambda.ListProvisionedConcurrencyConfigsInput).FunctionName) == "function-tagged" {
				out.ProvisionedConcurrencyConfigs = []lambdatypes.ProvisionedConcurrencyConfigListItem{{FunctionArn: sdkaws.String(tagged + ":1")}}
			}
			return out, nil
		},
		"ListNetworkConnectors": func(in any) (any, error) {
			items, next, err := lambdaPages(c, "ListNetworkConnectors", in, [][]lambdacoretypes.NetworkConnectorSummary{{
				{Arn: sdkaws.String(lambdaTaggedARN("network-connector")), Id: sdkaws.String("nc-tagged")},
				{Arn: sdkaws.String(lambdaBareARN("network-connector")), Id: sdkaws.String("nc-bare")},
			}}, lambdacoretypes.NetworkConnectorSummary{Id: sdkaws.String("nc-no-arn")})
			if err != nil {
				return nil, err
			}
			return &lambdacore.ListNetworkConnectorsOutput{NetworkConnectors: items, NextMarker: next}, nil
		},
		"ListMicrovmImages": func(in any) (any, error) {
			c.record("ListMicrovmImages", in)
			return &lambdamicrovms.ListMicrovmImagesOutput{Items: []lambdamicrovmstypes.MicrovmImageSummary{
				{ImageArn: sdkaws.String(lambdaTaggedARN("microvm-image")), Name: sdkaws.String("microvm-image-tagged")},
				{ImageArn: sdkaws.String(lambdaBareARN("microvm-image")), Name: sdkaws.String("microvm-image-bare")},
			}}, nil
		},
		"ListMicrovmImageVersions": func(in any) (any, error) {
			c.record("ListMicrovmImageVersions", in)
			image := sv(in.(*lambdamicrovms.ListMicrovmImageVersionsInput).ImageIdentifier)
			return &lambdamicrovms.ListMicrovmImageVersionsOutput{Items: []lambdamicrovmstypes.MicrovmImageVersionSummary{{ImageArn: &image, ImageVersion: sdkaws.String("1")}}}, nil
		},
		"ListMicrovms":             none("ListMicrovms", &lambdamicrovms.ListMicrovmsOutput{Items: []lambdamicrovmstypes.MicrovmItem{lmvMicrovm("vm-1")}}),
		"ListManagedMicrovmImages": none("ListManagedMicrovmImages", &lambdamicrovms.ListManagedMicrovmImagesOutput{Items: []lambdamicrovmstypes.ManagedMicrovmImageSummary{lmvManaged("base")}}),
	}
}

// lambdaScan runs the scan's real entry point against handlers; an op they
// do not answer is denied.
func lambdaScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lambdaDenied)
	return scanLambda(ctx, acct, testRegion, st, testScanID)
}

// lambdaRowCount is every row lambdaHandlers' region stores: 3 functions, a
// version, an alias, 2 code signing configs, 2 capacity providers, 2 event
// source mappings, a provisioned concurrency config, 2 network connectors, 2
// MicroVM images, 2 image versions, a MicroVM and a managed image.
const lambdaRowCount = 20

func lambdaRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// lambdaTaggedTypes is each tagged kind's type.
var lambdaTaggedTypes = map[string]string{
	"function":             TypeLambdaFunction,
	"code-signing-config":  TypeLambdaCodeSigningConfig,
	"capacity-provider":    TypeLambdaCapacityProvider,
	"event-source-mapping": TypeLambdaESM,
	"network-connector":    TypeLambdaNetworkConnector,
	"microvm-image":        TypeLambdaMicrovmImage,
}

// Every type ListTags takes is stored with its tags, read with the ARN its
// listing returned: the functions' from GetFunction, with no ListTags call
// for any of them, the MicroVM images' through the Lambda MicroVMs client,
// the rest through the Lambda client. An untagged resource has nil tags, and
// no other row (versions, aliases) has its tags read. A rescan inserts
// nothing.
func TestScanLambda_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newLambdaCalls()

	total, inserted, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != lambdaRowCount || inserted != lambdaRowCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, lambdaRowCount, lambdaRowCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for kind, typ := range lambdaTaggedTypes {
		r, ok := lambdaRowOf(t, st, typ, lambdaTaggedARN(kind))
		if !ok {
			t.Errorf("%s %s not stored", typ, lambdaTaggedARN(kind))
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"type": kind})
		if bare, ok := lambdaRowOf(t, st, typ, lambdaBareARN(kind)); !ok || bare.TagsJSON != nil {
			t.Errorf("%s %s stored = %v, TagsJSON = %v; want stored with nil tags", typ, lambdaBareARN(kind), ok, bare.TagsJSON)
		}
	}
	var want []string
	for _, kind := range []string{"code-signing-config", "capacity-provider", "event-source-mapping", "network-connector"} {
		want = append(want, lambdaTaggedARN(kind), lambdaBareARN(kind))
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("Lambda ListTags sent %v; want %v", got, slices.Sorted(slices.Values(want)))
	}
	wantFns := slices.Sorted(slices.Values([]string{lambdaTaggedARN("function"), lambdaBareARN("function"), lmvARN("function", "image-fn")}))
	if got := slices.Sorted(slices.Values(calls.getARNs)); !slices.Equal(got, wantFns) {
		t.Errorf("GetFunction sent %v; want %v", got, wantFns)
	}
	if n, m := calls.count("GetFunction"), calls.count("ListTags"); n != 3 || m != 8 {
		t.Errorf("GetFunction, ListTags calls = %d, %d; want 3, 8", n, m)
	}
	wantImages := []string{lambdaBareARN("microvm-image"), lambdaTaggedARN("microvm-image")}
	if got := slices.Sorted(slices.Values(calls.imageARNs)); !slices.Equal(got, wantImages) {
		t.Errorf("Lambda MicroVMs ListTags sent %v; want %v", got, wantImages)
	}

	total, inserted, err = lambdaScan(context.Background(), st, lambdaHandlers(newLambdaCalls()))
	if err != nil || total != lambdaRowCount || inserted != 0 {
		t.Errorf("rescan total, inserted, err = %d, %d, %v; want %d, 0, nil", total, inserted, err, lambdaRowCount)
	}
}

// Each account-wide listing sends its page cap.
func TestScanLambda_PageCaps(t *testing.T) {
	calls := newLambdaCalls()
	if _, _, err := lambdaScan(context.Background(), newTestStore(t), lambdaHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, want := range map[string]int32{
		"ListFunctions": 50, "ListCodeSigningConfigs": 10000, "ListCapacityProviders": 50,
		"ListEventSourceMappings": 100, "ListNetworkConnectors": 100, "ListMicrovmImages": 50,
	} {
		got := calls.pageSizes[op]
		if len(got) == 0 || slices.ContainsFunc(got, func(n int32) bool { return n != want }) {
			t.Errorf("%s page sizes = %v; want %d on every page", op, got, want)
		}
	}
}

// Each function page is written with its tags before the next page is read.
func TestScanLambda_FunctionsWrittenPageByPage(t *testing.T) {
	st := newTestStore(t)
	calls := newLambdaCalls()
	handlers := lambdaHandlers(calls)
	list := handlers["ListFunctions"]
	var storedFirst bool
	handlers["ListFunctions"] = func(in any) (any, error) {
		if bacField[*string](in, "Marker") != nil {
			rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{TypeLambdaFunction}, Limit: util.AllResources})
			storedFirst = err == nil && slices.ContainsFunc(rows, func(r store.Resource) bool {
				return r.NativeID == lambdaTaggedARN("function") && r.TagsJSON != nil
			})
		}
		return list(in)
	}
	if _, _, err := lambdaScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !storedFirst {
		t.Error("page 1's function was not stored with its tags before page 2 was read")
	}
}

// A failed tag read (for a function, the ListTags read after GetFunction
// answered a TagsError): ResourceNotFoundException, the op's modeled not-found,
// drops the row silently, and a dropped function is no parent of the
// per-function phases; a denial keeps the row untagged and warns once under
// lambda:ListTags; a throttle after retries keeps it and is a transient
// error; InvalidParameterValueException keeps it and is a hard error.
func TestScanLambda_TagReadFailures(t *testing.T) {
	for _, kind := range []string{"function", "microvm-image"} {
		typ, arn := lambdaTaggedTypes[kind], lambdaTaggedARN(kind)
		t.Run(kind, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantKept  bool
				wantWarns int
			}{
				{"not found", lambdaGone, -1, "", false, 0},
				{"denied", lambdaDenied, -1, "", true, 1},
				{"transient", lambdaThrottle, outcomeTransient, "TooManyRequestsException", true, 0},
				{"other error", lambdaInvalid, outcomeError, "InvalidParameterValueException", true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, lambdaListTagsOp)
					calls := newLambdaCalls()
					calls.tagErrs[arn] = c.err
					calls.tagsError[arn] = true // a function's tags fall back to ListTags

					total, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
					if c.outcome < 0 {
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					} else {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.code)
					}
					wantTotal := lambdaRowCount
					if !c.wantKept {
						wantTotal--
						if kind == "function" {
							wantTotal -= 3 // its version, alias and provisioned concurrency config
						} else {
							wantTotal-- // its image version
						}
					}
					if total != wantTotal {
						t.Errorf("total = %d; want %d", total, wantTotal)
					}
					r, ok := lambdaRowOf(t, st, typ, arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", lambdaListTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
					if kind == "function" && slices.Contains(calls.versionFns, "function-tagged") != c.wantKept {
						t.Errorf("versions listed for %v; want function-tagged listed = %v", calls.versionFns, c.wantKept)
					}
				})
			}
		})
	}
}

// Denied tag reads across phases and both clients send one warning.
func TestScanLambda_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, lambdaListTagsOp)
	calls := newLambdaCalls()
	for _, kind := range []string{"function", "code-signing-config", "microvm-image"} {
		calls.tagErrs[lambdaTaggedARN(kind)] = lambdaDenied
	}
	calls.tagsError[lambdaTaggedARN("function")] = true
	if _, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %d; want 1", lambdaListTagsOp, len(*warnings))
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanLambda_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lambdaListTagsOp)
	calls := newLambdaCalls()
	calls.tagErrs[lambdaTaggedARN("function")] = lambdaThrottle
	calls.tagsError[lambdaTaggedARN("function")] = true
	calls.tagErrs[lambdaBareARN("code-signing-config")] = lambdaInvalid

	_, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValueException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", lambdaListTagsOp, *warns)
	}
}

// GetFunction is called, with the function's ARN, for every function. The
// image function's Code.ImageUri is stored beside its listed element. A
// function GetFunction no longer finds is dropped and is no parent. A denial
// keeps the listed element and warns once under lambda:GetFunction; a
// throttle keeps it and is transient; any other error keeps it and is
// returned. Each failure kept reads that function's tags with ListTags.
func TestScanLambda_GetFunction(t *testing.T) {
	image := lmvARN("function", "image-fn")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		wantKept  bool
		wantCode  bool
		wantWarns int
	}{
		{"read", nil, -1, true, true, 0},
		{"not found", lambdaGone, -1, false, false, 0},
		{"denied", lambdaDenied, -1, true, false, 1},
		{"transient", lambdaThrottle, outcomeTransient, true, false, 0},
		{"other error", lambdaInvalid, outcomeError, true, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, lambdaGetFunctionOp)
			calls := newLambdaCalls()
			if c.err != nil {
				calls.getErrs[image] = c.err
			}

			_, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
			}
			wantGets := slices.Sorted(slices.Values([]string{lambdaTaggedARN("function"), lambdaBareARN("function"), image}))
			if got := slices.Sorted(slices.Values(calls.getARNs)); !slices.Equal(got, wantGets) {
				t.Errorf("GetFunction sent %v; want %v", got, wantGets)
			}
			var wantListTags []string
			if c.wantKept && c.err != nil {
				wantListTags = []string{image}
			}
			if got := lambdaFunctionTagReads(calls); !slices.Equal(got, wantListTags) {
				t.Errorf("ListTags sent functions %v; want %v", got, wantListTags)
			}
			r, ok := lambdaRowOf(t, st, TypeLambdaFunction, image)
			if ok != c.wantKept {
				t.Fatalf("image function stored = %v; want %v", ok, c.wantKept)
			}
			if slices.Contains(calls.versionFns, "image-fn") != c.wantKept {
				t.Errorf("versions listed for %v; want image-fn listed = %v", calls.versionFns, c.wantKept)
			}
			if len(*warnings) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("%s warnings = %d (all ops %d); want %d", lambdaGetFunctionOp, len(*warnings), *all, c.wantWarns)
			}
			if !ok {
				return
			}
			var attrs lambdaFunctionAttrs
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			if sv(attrs.FunctionName) != "image-fn" || attrs.PackageType != lambdatypes.PackageTypeImage || attrs.Description != nil {
				t.Errorf("attrs FunctionName, PackageType, Description = %q, %q, %v; want the listed element's", sv(attrs.FunctionName), attrs.PackageType, attrs.Description)
			}
			if got := attrs.Code != nil && sv(attrs.Code.ImageURI) == lambdaImageURI; got != c.wantCode {
				t.Errorf("attrs Code = %+v; want ImageUri stored = %v", attrs.Code, c.wantCode)
			}
		})
	}
}

// lambdaFunctionTagReads is the function ARNs sent to the Lambda client's
// ListTags, sorted.
func lambdaFunctionTagReads(calls *lambdaCalls) []string {
	var fns []string
	for _, arn := range calls.tagARNs {
		if strings.Contains(arn, ":function:") {
			fns = append(fns, arn)
		}
	}
	return slices.Sorted(slices.Values(fns))
}

// A zip function's stored attributes are its listed element, nothing of
// GetFunction's body; an image function's add Code.ImageUri only. No row
// stores GetFunction's Code.Location, a presigned URL.
func TestScanLambda_FunctionAttributesFromListing(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := lambdaScan(context.Background(), st, lambdaHandlers(newLambdaCalls())); err != nil {
		t.Fatalf("scan: %v", err)
	}
	decode := func(s string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("decode %s: %v", s, err)
		}
		return m
	}
	for _, name := range []string{"function-tagged", "function-bare"} {
		r, ok := lambdaRowOf(t, st, TypeLambdaFunction, lmvARN("function", name))
		if !ok {
			t.Fatalf("%s not stored", name)
		}
		if got, want := decode(r.AttributesJSON), decode(mustJSON(lambdaFn(name, lambdatypes.PackageTypeZip))); !reflect.DeepEqual(got, want) {
			t.Errorf("%s attrs = %v; want the listed element %v", name, got, want)
		}
	}
	image := lambdaFn("image-fn", lambdatypes.PackageTypeImage)
	r, ok := lambdaRowOf(t, st, TypeLambdaFunction, sv(image.FunctionArn))
	if !ok {
		t.Fatal("image-fn not stored")
	}
	want := decode(mustJSON(lambdaFunctionAttrs{FunctionConfiguration: image, Code: &lambdaFunctionCodeAttrs{ImageURI: sdkaws.String(lambdaImageURI)}}))
	if got := decode(r.AttributesJSON); !reflect.DeepEqual(got, want) {
		t.Errorf("image-fn attrs = %v; want %v", got, want)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if strings.Contains(r.AttributesJSON, "Location") || strings.Contains(r.AttributesJSON, "presigned-secret") {
			t.Errorf("%s %s stores Code.Location: %s", r.Type, r.NativeID, r.AttributesJSON)
		}
	}
}

// A TagsError in GetFunction's body reads that function's tags, and only
// its, with ListTags.
func TestScanLambda_FunctionTagsErrorFallsBackToListTags(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newLambdaCalls()
	calls.tagsError[lambdaTaggedARN("function")] = true

	if _, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := lambdaFunctionTagReads(calls); !slices.Equal(got, []string{lambdaTaggedARN("function")}) {
		t.Errorf("ListTags sent functions %v; want only %s", got, lambdaTaggedARN("function"))
	}
	r, ok := lambdaRowOf(t, st, TypeLambdaFunction, lambdaTaggedARN("function"))
	if !ok {
		t.Fatal("function-tagged not stored")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": "function"})
	if n, m := calls.count("GetFunction"), calls.count("ListTags"); n != 3 || m != 9 {
		t.Errorf("GetFunction, ListTags calls = %d, %d; want 3, 9", n, m)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A function whose GetFunction is denied still gets its tags through
// ListTags; one whose ListTags is denied too is stored untagged. Each denial
// warns once under its own op.
func TestScanLambda_GetFunctionDeniedTagsFromListTags(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	getWarns := bacWarnings(st, lambdaGetFunctionOp)
	tagWarns := bacWarnings(st, lambdaListTagsOp)
	calls := newLambdaCalls()
	calls.getErrs[lambdaTaggedARN("function")] = lambdaDenied
	calls.getErrs[lambdaBareARN("function")] = lambdaDenied
	calls.tagErrs[lambdaBareARN("function")] = lambdaDenied

	if _, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := lambdaRowOf(t, st, TypeLambdaFunction, lambdaTaggedARN("function"))
	if !ok {
		t.Fatal("function-tagged not stored")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": "function"})
	if bare, ok := lambdaRowOf(t, st, TypeLambdaFunction, lambdaBareARN("function")); !ok || bare.TagsJSON != nil {
		t.Errorf("function-bare stored = %v, TagsJSON = %v; want stored untagged", ok, bare.TagsJSON)
	}
	wantReads := slices.Sorted(slices.Values([]string{lambdaTaggedARN("function"), lambdaBareARN("function")}))
	if got := lambdaFunctionTagReads(calls); !slices.Equal(got, wantReads) {
		t.Errorf("ListTags sent functions %v; want %v", got, wantReads)
	}
	if n, m := calls.count("GetFunction"), calls.count("ListTags"); n != 3 || m != 10 {
		t.Errorf("GetFunction, ListTags calls = %d, %d; want 3, 10", n, m)
	}
	if len(*getWarns) != 1 || len(*tagWarns) != 1 || *all != 2 {
		t.Errorf("warnings %s, %s, all = %d, %d, %d; want 1, 1, 2", lambdaGetFunctionOp, lambdaListTagsOp, len(*getWarns), len(*tagWarns), *all)
	}
}

// Each type's row carries its Name and Status from the listed element.
func TestScanLambda_NameAndStatus(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := lambdaScan(context.Background(), st, lambdaHandlers(newLambdaCalls())); err != nil {
		t.Fatalf("scan: %v", err)
	}
	cpARN := lambdaTaggedARN("capacity-provider")
	for _, c := range []struct {
		typ, arn, name string
		status         *string
	}{
		{TypeLambdaFunction, lambdaTaggedARN("function"), "function-tagged", nil},
		{TypeLambdaCodeSigningConfig, lambdaTaggedARN("code-signing-config"), "csc-tagged", nil},
		{TypeLambdaCapacityProvider, cpARN, cpARN, sdkaws.String(string(lambdatypes.CapacityProviderStateActive))},
		{TypeLambdaESM, lambdaTaggedARN("event-source-mapping"), "esm-tagged", sdkaws.String("Enabled")},
	} {
		r, ok := lambdaRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Errorf("%s %s not stored", c.typ, c.arn)
			continue
		}
		if sv(r.Name) != c.name || (r.Status == nil) != (c.status == nil) || sv(r.Status) != sv(c.status) {
			t.Errorf("%s Name, Status = %q, %v; want %q, %v", c.typ, sv(r.Name), r.Status, c.name, c.status)
		}
	}
}

// Functions and MicroVM images of other partitions are read with the ARN
// their listing returned.
func TestScanLambda_OtherPartitionARNs(t *testing.T) {
	gov := "arn:aws-us-gov:lambda:us-gov-west-1:123456789012:function:gov-fn"
	cn := "arn:aws-cn:lambda:cn-north-1:123456789012:microvm-image/cn-image"
	calls := newLambdaCalls()
	handlers := lambdaHandlers(calls)
	handlers["ListFunctions"] = func(in any) (any, error) {
		calls.record("ListFunctions", in)
		return &lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{{FunctionName: sdkaws.String("gov-fn"), FunctionArn: &gov}}}, nil
	}
	handlers["ListMicrovmImages"] = func(in any) (any, error) {
		calls.record("ListMicrovmImages", in)
		return &lambdamicrovms.ListMicrovmImagesOutput{Items: []lambdamicrovmstypes.MicrovmImageSummary{{ImageArn: &cn, Name: sdkaws.String("cn-image")}}}, nil
	}
	if _, _, err := lambdaScan(context.Background(), newTestStore(t), handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(calls.getARNs, []string{gov}) {
		t.Errorf("GetFunction sent %v; want [%s]", calls.getARNs, gov)
	}
	if !slices.Equal(calls.imageARNs, []string{cn}) {
		t.Errorf("Lambda MicroVMs ListTags sent %v; want [%s]", calls.imageARNs, cn)
	}
}

// A failed later page keeps the pages before it, and every later phase still
// runs; the error is returned.
func TestScanLambda_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newLambdaCalls()
	calls.listErr["ListCodeSigningConfigs"] = lambdaInvalid
	handlers := lambdaHandlers(calls)
	handlers["ListMicrovmImages"] = func(in any) (any, error) {
		calls.record("ListMicrovmImages", in)
		return nil, lambdaInvalid
	}

	total, _, err := lambdaScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValueException")
	if want := lambdaRowCount - 4; total != want {
		t.Errorf("total = %d; want %d (no MicroVM images or image versions)", total, want)
	}
	if _, ok := lambdaRowOf(t, st, TypeLambdaCodeSigningConfig, lambdaTaggedARN("code-signing-config")); !ok {
		t.Error("the code signing config page before the failed one was not stored")
	}
	for _, typ := range []string{TypeLambdaMicrovm, TypeLambdaManagedMicrovmImage, TypeLambdaNetworkConnector} {
		if len(rowsByNativeID(t, st, typ)) == 0 {
			t.Errorf("%s not stored after the failed phases", typ)
		}
	}
}

// A repeated marker ends the function listing as an error, keeping the page
// read.
func TestScanLambda_RepeatedMarkerKeepsPage(t *testing.T) {
	st := newTestStore(t)
	handlers := lambdaHandlers(newLambdaCalls())
	handlers["ListFunctions"] = func(any) (any, error) {
		return &lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{lambdaFn("function-tagged", lambdatypes.PackageTypeZip)}, NextMarker: sdkaws.String("m")}, nil
	}
	_, _, err := lambdaScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := lambdaRowOf(t, st, TypeLambdaFunction, lambdaTaggedARN("function")); !ok {
		t.Error("the page read was not stored")
	}
}

// A listed element without an ARN is not stored, and one warning under its
// listing names it.
func TestScanLambda_ElementWithoutARNWarns(t *testing.T) {
	for op, typ := range map[string]string{
		"ListFunctions": TypeLambdaFunction, "ListCodeSigningConfigs": TypeLambdaCodeSigningConfig,
		"ListCapacityProviders": TypeLambdaCapacityProvider, "ListEventSourceMappings": TypeLambdaESM,
		"ListNetworkConnectors": TypeLambdaNetworkConnector,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "lambda:"+op)
			calls := newLambdaCalls()
			calls.noARN[op] = true

			total, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != lambdaRowCount || len(*warnings) != 1 || *all != 1 {
				t.Errorf("total = %d, %s warnings = %d (all %d); want %d, 1, 1", total, op, len(*warnings), *all, lambdaRowCount)
			}
			if _, ok := rowsByNativeID(t, st, typ)[""]; ok {
				t.Errorf("%s stored under an empty NativeID", typ)
			}
		})
	}
}

// A region whose endpoint does not route ListCapacityProviders answers its
// first page with the gateway's NotFoundException, read as an empty listing;
// the same answer on a later page is an error.
func TestScanLambda_CapacityProvidersRegionGap(t *testing.T) {
	gap := apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")
	for _, c := range []struct {
		name    string
		page    func(in any) (any, error)
		wantErr bool
	}{
		{"first page", func(any) (any, error) { return nil, gap }, false},
		{"later page", func(in any) (any, error) {
			if bacField[*string](in, "Marker") != nil {
				return nil, gap
			}
			return &lambda.ListCapacityProvidersOutput{CapacityProviders: []lambdatypes.CapacityProvider{{CapacityProviderArn: sdkaws.String(lambdaTaggedARN("capacity-provider"))}}, NextMarker: sdkaws.String("1")}, nil
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			handlers := lambdaHandlers(newLambdaCalls())
			handlers["ListCapacityProviders"] = c.page
			_, _, err := lambdaScan(context.Background(), st, handlers)
			if c.wantErr {
				assertAPICode(t, err, "NotFoundException")
				if _, ok := lambdaRowOf(t, st, TypeLambdaCapacityProvider, lambdaTaggedARN("capacity-provider")); !ok {
					t.Error("the page before the gap was not stored")
				}
				return
			}
			if err != nil || *warns != 0 {
				t.Errorf("err = %v, warnings = %d; want nil, 0", err, *warns)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanLambda_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeLambdaFunction)
	calls := newLambdaCalls()
	calls.tagErrs[lambdaTaggedARN("function")] = lambdaDenied
	calls.getErr = lambdaDenied

	_, _, err := lambdaScan(context.Background(), st, lambdaHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListVersionsByFunction", "ListCodeSigningConfigs"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanLambda_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newLambdaCalls()
	handlers := lambdaHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListFunctions"]
	handlers["ListFunctions"] = func(in any) (any, error) {
		if bacField[*string](in, "Marker") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := lambdaScan(ctx, st, handlers)
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
	if n := calls.count("ListVersionsByFunction"); n != 0 {
		t.Errorf("ListVersionsByFunction called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanLambda_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "lambda.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := lambdaHandlers(newLambdaCalls())
		handlers["ListEventSourceMappings"] = func(any) (any, error) { return nil, nx }
		total, _, err := lambdaScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != lambdaRowCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, lambdaRowCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		calls := newLambdaCalls()
		calls.tagErrs[lambdaBareARN("code-signing-config")] = nx
		_, _, err := lambdaScan(context.Background(), newTestStore(t), lambdaHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("GetFunction after the listing", func(t *testing.T) {
		calls := newLambdaCalls()
		calls.getErrs[lambdaBareARN("function")] = nx
		_, _, err := lambdaScan(context.Background(), newTestStore(t), lambdaHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanLambda(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// Drives scanLambda against real SDK clients: every phase gets the parents
// its parent phase stored, and a phase's failure leaves every other phase's
// rows stored.
func TestScanLambda_PhasesWiredAndIndependent(t *testing.T) {
	type row struct{ typ, nativeID string }
	fn := lambdaTaggedARN("function")
	version := row{TypeLambdaVersion, fn + ":1"}
	alias := row{TypeLambdaAlias, fn + ":live"}
	pc := row{TypeLambdaProvisionedConcurrencyConfig, fn + ":1/provisioned-concurrency"}
	imageVersion := row{TypeLambdaMicrovmImageVersion, lambdaTaggedARN("microvm-image") + "/version/1"}
	nc := row{TypeLambdaNetworkConnector, lambdaTaggedARN("network-connector")}
	vm := row{TypeLambdaMicrovm, lmvARN("microvm-image", "img") + "/microvm/vm-1"}
	all := []row{version, alias, pc, imageVersion, nc, vm}
	for _, tc := range []struct {
		name   string
		failOp string
		lost   row
	}{
		{name: "all succeed"},
		// Provisioned concurrency is still listed for the functions whose
		// versions could not be listed.
		{name: "versions fail", failOp: "ListVersionsByFunction", lost: version},
		{name: "provisioned concurrency fails", failOp: "ListProvisionedConcurrencyConfigs", lost: pc},
		{name: "network connectors fail", failOp: "ListNetworkConnectors", lost: nc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			handlers := lambdaHandlers(newLambdaCalls())
			if tc.failOp != "" {
				handlers[tc.failOp] = func(any) (any, error) { return nil, lambdaInvalid }
			}
			_, _, err := lambdaScan(context.Background(), st, handlers)
			if (tc.failOp != "") != isAPIErrorCode(err, "InvalidParameterValueException") {
				t.Fatalf("err = %v; want the failed phase's error = %v", err, tc.failOp != "")
			}
			for _, r := range all {
				_, stored := lambdaRowOf(t, st, r.typ, r.nativeID)
				if want := r != tc.lost; stored != want {
					t.Errorf("%s %s stored = %t; want %t", r.typ, r.nativeID, stored, want)
				}
			}
		})
	}
}

package aws

import (
	"context"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	lambdacoretypes "github.com/aws/aws-sdk-go-v2/service/lambdacore/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdamicrovmstypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	smithymw "github.com/aws/smithy-go/middleware"
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

func lambdaTopLevelResponses() map[string][]stubCall {
	none := func(out any) []stubCall { return []stubCall{{Output: out}} }
	return map[string][]stubCall{
		"ListFunctions": none(&lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{
			{FunctionName: sdkaws.String("f1"), FunctionArn: sdkaws.String(lambdaFnARN("f1"))},
			{FunctionName: sdkaws.String("f2"), FunctionArn: sdkaws.String(lambdaFnARN("f2"))},
		}}),
		// Per-function ops are called for f1, then f2. f2 has only $LATEST, so
		// it must never reach ListProvisionedConcurrencyConfigs, whose single
		// queued response is f1's.
		"ListVersionsByFunction": {
			{Output: &lambda.ListVersionsByFunctionOutput{Versions: []lambdatypes.FunctionConfiguration{lambdaVersion("f1", "$LATEST"), lambdaVersion("f1", "1")}}},
			{Output: &lambda.ListVersionsByFunctionOutput{Versions: []lambdatypes.FunctionConfiguration{lambdaVersion("f2", "$LATEST")}}},
		},
		"ListAliases": {
			{Output: &lambda.ListAliasesOutput{Aliases: []lambdatypes.AliasConfiguration{
				{Name: sdkaws.String("live"), AliasArn: sdkaws.String(lambdaFnARN("f1") + ":live"), FunctionVersion: sdkaws.String("1")},
			}}},
			{Output: &lambda.ListAliasesOutput{}},
		},
		"ListFunctionEventInvokeConfigs": {{Output: &lambda.ListFunctionEventInvokeConfigsOutput{}}, {Output: &lambda.ListFunctionEventInvokeConfigsOutput{}}},
		"ListFunctionUrlConfigs":         {{Output: &lambda.ListFunctionUrlConfigsOutput{}}, {Output: &lambda.ListFunctionUrlConfigsOutput{}}},
		"GetPolicy":                      {{Err: apiErr("ResourceNotFoundException", "no policy")}, {Err: apiErr("ResourceNotFoundException", "no policy")}},
		"ListCodeSigningConfigs": none(&lambda.ListCodeSigningConfigsOutput{CodeSigningConfigs: []lambdatypes.CodeSigningConfig{
			{CodeSigningConfigArn: sdkaws.String(lmvARN("code-signing-config", "csc-1")), CodeSigningConfigId: sdkaws.String("csc-1")},
		}}),
		"ListCapacityProviders":   none(&lambda.ListCapacityProvidersOutput{}),
		"ListEventSourceMappings": none(&lambda.ListEventSourceMappingsOutput{}),
		"ListLayers":              {{Output: &lambda.ListLayersOutput{}}, {Output: &lambda.ListLayersOutput{}}},
		"ListProvisionedConcurrencyConfigs": none(&lambda.ListProvisionedConcurrencyConfigsOutput{
			ProvisionedConcurrencyConfigs: []lambdatypes.ProvisionedConcurrencyConfigListItem{lambdaPC("f1", "1")},
		}),
		"ListNetworkConnectors": none(&lambdacore.ListNetworkConnectorsOutput{NetworkConnectors: []lambdacoretypes.NetworkConnectorSummary{
			{Arn: sdkaws.String(lmvARN("network-connector", "nc-1")), Id: sdkaws.String("nc-1")},
		}}),
		"ListMicrovmImages": none(&lambdamicrovms.ListMicrovmImagesOutput{Items: []lambdamicrovmstypes.MicrovmImageSummary{lmvImage("a")}}),
		"ListMicrovmImageVersions": none(&lambdamicrovms.ListMicrovmImageVersionsOutput{
			Items: []lambdamicrovmstypes.MicrovmImageVersionSummary{lmvVersion("a", "1")},
		}),
		"ListMicrovms":             none(&lambdamicrovms.ListMicrovmsOutput{Items: []lambdamicrovmstypes.MicrovmItem{lmvMicrovm("vm-1")}}),
		"ListManagedMicrovmImages": none(&lambdamicrovms.ListManagedMicrovmImagesOutput{Items: []lambdamicrovmstypes.ManagedMicrovmImageSummary{lmvManaged("base")}}),
	}
}

// Drives scanLambda against real SDK clients. The newest phases run after
// every established one, so a failure in any of them still leaves the rows of
// the established per-function (alias) and account-level (code signing
// config) phases stored.
func TestScanLambda_NewPhasesWiredAndOrdered(t *testing.T) {
	type row struct{ typ, nativeID string }
	fn := row{TypeLambdaFunction, lambdaFnARN("f1")}
	version := row{TypeLambdaVersion, lambdaFnARN("f1") + ":1"}
	alias := row{TypeLambdaAlias, lambdaFnARN("f1") + ":live"}
	csc := row{TypeLambdaCodeSigningConfig, lmvARN("code-signing-config", "csc-1")}
	pc := row{TypeLambdaProvisionedConcurrencyConfig, lambdaFnARN("f1") + ":1/provisioned-concurrency"}
	nc := row{TypeLambdaNetworkConnector, lmvARN("network-connector", "nc-1")}
	image := row{TypeLambdaMicrovmImage, lmvARN("microvm-image", "a")}
	imageVersion := row{TypeLambdaMicrovmImageVersion, lmvARN("microvm-image", "a") + "/version/1"}
	vm := row{TypeLambdaMicrovm, lmvARN("microvm-image", "img") + "/microvm/vm-1"}
	managed := row{TypeLambdaManagedMicrovmImage, sv(lmvManaged("base").ImageArn)}
	all := []row{fn, version, alias, csc, pc, nc, image, imageVersion, vm, managed}
	for _, tc := range []struct {
		name    string
		failOp  string
		want    []row
		wantErr bool
	}{
		{name: "all succeed", want: all},
		{name: "provisioned concurrency fails", failOp: "ListProvisionedConcurrencyConfigs", want: []row{fn, version, alias, csc}, wantErr: true},
		{name: "network connectors fail", failOp: "ListNetworkConnectors", want: []row{fn, version, alias, csc, pc}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			responses := lambdaTopLevelResponses()
			if tc.failOp != "" {
				responses[tc.failOp] = []stubCall{{Err: apiErr("ValidationException", "bad")}}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: sdkaws.Config{
				Region:           testRegion,
				Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
				RetryMaxAttempts: 1,
				APIOptions:       []func(*smithymw.Stack) error{stubResponses(t, responses)},
			}}

			_, _, err := scanLambda(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantErr != isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("scanLambda err=%v, wantErr=%t", err, tc.wantErr)
			}
			for _, r := range all {
				_, stored := lmvRows(t, st, r.typ)[r.nativeID]
				if want := slices.Contains(tc.want, r); stored != want {
					t.Errorf("%s %s stored=%t, want %t", r.typ, r.nativeID, stored, want)
				}
			}
		})
	}
}

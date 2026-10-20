package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/licensemanager"
	lmtypes "github.com/aws/aws-sdk-go-v2/service/licensemanager/types"
	"github.com/aws/smithy-go"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

var (
	lmDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: license-manager:List"}
	lmBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	lmThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
)

const lmTestLicenseArn = "arn:aws:license-manager::111122223333:license:l-0123456789abcdef0123456789abcdef"

func lmTestClient(handlers map[string]func(in any) (any, error)) *licensemanager.Client {
	return licensemanager.NewFromConfig(opStubConfig(handlers, lmDenied))
}

func lmGranted(arn, name string, status lmtypes.LicenseStatus) lmtypes.GrantedLicense {
	return lmtypes.GrantedLicense{LicenseArn: ptrStr(arn), LicenseName: ptrStr(name), Status: status, HomeRegion: ptrStr("us-east-1")}
}

func lmWithReceived(l lmtypes.GrantedLicense, rs lmtypes.ReceivedStatus) lmtypes.GrantedLicense {
	l.ReceivedMetadata = &lmtypes.ReceivedMetadata{ReceivedStatus: rs}
	return l
}

func lmToken(id, licenseArn, status string) lmtypes.TokenData {
	return lmtypes.TokenData{TokenId: ptrStr(id), LicenseArn: ptrStr(licenseArn), Status: ptrStr(status), RoleArns: []string{"arn:aws:iam::123456789012:role/r"}}
}

func scanLMReceivedForTest(t *testing.T, st *store.Store, list func(in *licensemanager.ListReceivedLicensesInput) (any, error)) (int, error) {
	t.Helper()
	client := lmTestClient(map[string]func(any) (any, error){
		"ListReceivedLicenses": func(in any) (any, error) { return list(in.(*licensemanager.ListReceivedLicensesInput)) },
	})
	total, _, err := scanLMReceivedLicenses(context.Background(), client, newTestAccount(testAccountID), "us-east-1", st, testScanID)
	return total, err
}

func scanLMTokensForTest(t *testing.T, st *store.Store, list func(in *licensemanager.ListTokensInput) (any, error)) (int, error) {
	t.Helper()
	client := lmTestClient(map[string]func(any) (any, error){
		"ListTokens": func(in any) (any, error) { return list(in.(*licensemanager.ListTokensInput)) },
	})
	total, _, err := scanLMTokens(context.Background(), client, newTestAccount(testAccountID), "us-east-1", st, testScanID)
	return total, err
}

func TestScanLMReceivedLicenses_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	arnB := "arn:aws:license-manager::444455556666:license:l-b"
	var pages int
	total, err := scanLMReceivedForTest(t, st, func(in *licensemanager.ListReceivedLicensesInput) (any, error) {
		pages++
		if in.MaxResults == nil || *in.MaxResults != 100 {
			return nil, lmBadInput // the model's page cap is 100
		}
		if sv(in.NextToken) == "" {
			return &licensemanager.ListReceivedLicensesOutput{
				Licenses:  []lmtypes.GrantedLicense{lmWithReceived(lmGranted(lmTestLicenseArn, "license-a", lmtypes.LicenseStatusAvailable), lmtypes.ReceivedStatusActive)},
				NextToken: ptrStr("p2"),
			}, nil
		}
		return &licensemanager.ListReceivedLicensesOutput{
			Licenses: []lmtypes.GrantedLicense{lmWithReceived(lmGranted(arnB, "license-b", lmtypes.LicenseStatusPendingDelete), lmtypes.ReceivedStatusRejected)},
		}, nil
	})
	if err != nil || total != 2 || pages != 2 {
		t.Fatalf("total, pages, err = %d, %d, %v; want 2, 2, nil", total, pages, err)
	}
	rows := rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	got, ok := rows[lmTestLicenseArn+"/received"]
	if !ok {
		t.Fatalf("no row keyed %s/received; have %v", lmTestLicenseArn, rows)
	}
	if got.Type != TypeLicenseManagerReceivedLicense || sv(got.Region) != "global" || sv(got.Name) != "license-a" || sv(got.Status) != "ACTIVE" {
		t.Errorf("row = type %q region %q name %q status %q", got.Type, sv(got.Region), sv(got.Name), sv(got.Status))
	}
	var attrs struct{ LicenseArn, HomeRegion string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	if attrs.LicenseArn != lmTestLicenseArn || attrs.HomeRegion != "us-east-1" {
		t.Errorf("attrs = %+v; want the issuer's LicenseArn and HomeRegion verbatim", attrs)
	}
	// The row status is this account's received status, not the issuer's
	// PENDING_DELETE.
	if sv(rows[arnB+"/received"].Status) != "REJECTED" {
		t.Errorf("page-2 row status = %q, want REJECTED", sv(rows[arnB+"/received"].Status))
	}
}

func TestScanLMReceivedLicenses_EmptyStoresNothing(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLMReceivedForTest(t, st, func(*licensemanager.ListReceivedLicensesInput) (any, error) {
		return &licensemanager.ListReceivedLicensesOutput{}, nil
	})
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)) != 0 {
		t.Errorf("total, err = %d, %v; want 0 rows, nil", total, err)
	}
}

func TestScanLMReceivedLicenses_SkipsDeletedAndArnless(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLMReceivedForTest(t, st, func(*licensemanager.ListReceivedLicensesInput) (any, error) {
		return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{
			lmGranted(lmTestLicenseArn, "kept", lmtypes.LicenseStatusExpired),
			lmGranted("arn:aws:license-manager::111122223333:license:l-gone", "gone", lmtypes.LicenseStatusDeleted),
			{LicenseName: ptrStr("no-arn"), Status: lmtypes.LicenseStatusAvailable},
		}}, nil
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)[lmTestLicenseArn+"/received"]; !ok {
		t.Error("EXPIRED license not stored")
	}
}

func TestScanLMReceivedLicenses_DeniedWarnsAndKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListReceivedLicenses")
	total, err := scanLMReceivedForTest(t, st, func(in *licensemanager.ListReceivedLicensesInput) (any, error) {
		if sv(in.NextToken) == "" {
			return &licensemanager.ListReceivedLicensesOutput{
				Licenses:  []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable)},
				NextToken: ptrStr("p2"),
			}, nil
		}
		return nil, lmDenied
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1", *warnings)
	}
}

func TestScanLMReceivedLicenses_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := scanLMReceivedForTest(t, st, func(*licensemanager.ListReceivedLicensesInput) (any, error) {
		return nil, lmBadInput
	})
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v, want the ValidationException", err)
	}
}

func TestScanLMReceivedLicenses_StopsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	var calls int
	_, err := scanLMReceivedForTest(t, st, func(*licensemanager.ListReceivedLicensesInput) (any, error) {
		calls++
		if calls > 2 {
			return nil, lmBadInput
		}
		return &licensemanager.ListReceivedLicensesOutput{
			Licenses:  []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable)},
			NextToken: ptrStr("same"),
		}, nil
	})
	if err != nil || calls != 2 {
		t.Errorf("calls, err = %d, %v; want 2, nil", calls, err)
	}
	if n := len(rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)); n != 1 {
		t.Errorf("stored rows = %d, want 1", n)
	}
}

func TestScanLMTokens_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages int
	total, err := scanLMTokensForTest(t, st, func(in *licensemanager.ListTokensInput) (any, error) {
		pages++
		if in.MaxResults == nil || *in.MaxResults != 100 {
			return nil, lmBadInput // the model's page cap is 100
		}
		if sv(in.NextToken) == "" {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("p2")}, nil
		}
		return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t2", lmTestLicenseArn, "")}}, nil
	})
	if err != nil || total != 2 || pages != 2 {
		t.Fatalf("total, pages, err = %d, %d, %v; want 2, 2, nil", total, pages, err)
	}
	rows := rowsByNativeID(t, st, TypeLicenseManagerToken)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	got, ok := rows[lmTestLicenseArn+"/token/t1"]
	if !ok {
		t.Fatalf("no row keyed %s/token/t1; have %v", lmTestLicenseArn, rows)
	}
	if got.Type != TypeLicenseManagerToken || sv(got.Region) != "global" || sv(got.Status) != "AVAILABLE" {
		t.Errorf("row = type %q region %q status %q", got.Type, sv(got.Region), sv(got.Status))
	}
	var attrs struct {
		TokenID  string `json:"TokenId"`
		RoleArns []string
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	if attrs.TokenID != "t1" || !slices.Equal(attrs.RoleArns, []string{"arn:aws:iam::123456789012:role/r"}) {
		t.Errorf("attrs = %+v; want TokenId and RoleArns verbatim", attrs)
	}
	if s := rows[lmTestLicenseArn+"/token/t2"].Status; s != nil {
		t.Errorf("status of a token without one = %q, want unset", *s)
	}
}

func TestScanLMTokens_EmptyStoresNothing(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLMTokensForTest(t, st, func(*licensemanager.ListTokensInput) (any, error) {
		return &licensemanager.ListTokensOutput{}, nil
	})
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeLicenseManagerToken)) != 0 {
		t.Errorf("total, err = %d, %v; want 0 rows, nil", total, err)
	}
}

func TestScanLMTokens_SkipsDeletedAndIncomplete(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLMTokensForTest(t, st, func(*licensemanager.ListTokensInput) (any, error) {
		return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{
			lmToken("kept", lmTestLicenseArn, "AVAILABLE"),
			lmToken("gone", lmTestLicenseArn, "DELETED"),
			lmToken("", lmTestLicenseArn, "AVAILABLE"),
			lmToken("no-license", "", "AVAILABLE"),
		}}, nil
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerToken)[lmTestLicenseArn+"/token/kept"]; !ok {
		t.Error("AVAILABLE token not stored")
	}
}

func TestScanLMTokens_DeniedWarnsAndKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListTokens")
	total, err := scanLMTokensForTest(t, st, func(in *licensemanager.ListTokensInput) (any, error) {
		if sv(in.NextToken) == "" {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("p2")}, nil
		}
		return nil, lmDenied
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1", *warnings)
	}
}

func TestScanLMTokens_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := scanLMTokensForTest(t, st, func(*licensemanager.ListTokensInput) (any, error) {
		return nil, lmBadInput
	})
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v, want the ValidationException", err)
	}
}

func TestScanLMTokens_StopsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	var calls int
	_, err := scanLMTokensForTest(t, st, func(*licensemanager.ListTokensInput) (any, error) {
		calls++
		if calls > 2 {
			return nil, lmBadInput
		}
		return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("same")}, nil
	})
	if err != nil || calls != 2 {
		t.Errorf("calls, err = %d, %v; want 2, nil", calls, err)
	}
	if n := len(rowsByNativeID(t, st, TypeLicenseManagerToken)); n != 1 {
		t.Errorf("stored rows = %d, want 1", n)
	}
}

// lmTopLevelHandlers answers every existing License Manager list op with one
// license and nothing else, and the two new ops with the given handlers.
func lmTopLevelHandlers(received, tokens func(any) (any, error)) map[string]func(any) (any, error) {
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(any) (any, error){
		"ListLicenses": empty(&licensemanager.ListLicensesOutput{Licenses: []lmtypes.License{
			{LicenseArn: ptrStr(lmTestLicenseArn), LicenseName: ptrStr("own"), Status: lmtypes.LicenseStatusAvailable},
		}}),
		"ListDistributedGrants":              empty(&licensemanager.ListDistributedGrantsOutput{}),
		"ListLicenseConfigurations":          empty(&licensemanager.ListLicenseConfigurationsOutput{}),
		"ListLicenseManagerReportGenerators": empty(&licensemanager.ListLicenseManagerReportGeneratorsOutput{}),
		"ListLicenseAssetGroups":             empty(&licensemanager.ListLicenseAssetGroupsOutput{}),
		"ListLicenseAssetRulesets":           empty(&licensemanager.ListLicenseAssetRulesetsOutput{}),
		"ListReceivedLicenses":               received,
		"ListTokens":                         tokens,
	}
}

func scanLicenseManagerForTest(t *testing.T, st *store.Store, received, tokens func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(lmTopLevelHandlers(received, tokens), lmDenied)
	total, _, err := scanLicenseManager(context.Background(), acct, "", st, testScanID)
	return total, err
}

func TestScanLicenseManager_StoresReceivedLicensesAndTokens(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLicenseManagerForTest(t, st,
		func(any) (any, error) {
			return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "self", lmtypes.LicenseStatusAvailable)}}, nil
		},
		func(any) (any, error) {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}}, nil
		})
	if err != nil || total != 3 {
		t.Fatalf("total, err = %d, %v; want 3 (license, received license, token), nil", total, err)
	}
	// A self-granted license is listed by both ListLicenses and
	// ListReceivedLicenses; the two rows must not merge.
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerLicense)[lmTestLicenseArn]; !ok {
		t.Error("issued license row missing")
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)[lmTestLicenseArn+"/received"]; !ok {
		t.Error("received license row missing")
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerToken)[lmTestLicenseArn+"/token/t1"]; !ok {
		t.Error("token row missing")
	}
}

func TestScanLicenseManager_NewPhaseErrorsKeepEveryOtherPhase(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLicenseManagerForTest(t, st,
		func(any) (any, error) { return nil, lmBadInput },
		func(any) (any, error) {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}}, nil
		})
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v, want the ListReceivedLicenses ValidationException", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (license, token)", total)
	}
	if len(rowsByNativeID(t, st, TypeLicenseManagerLicense)) != 1 || len(rowsByNativeID(t, st, TypeLicenseManagerToken)) != 1 {
		t.Error("an earlier phase's license or the sibling token phase's row is missing")
	}

	st = newTestStore(t)
	_, err = scanLicenseManagerForTest(t, st,
		func(any) (any, error) { return &licensemanager.ListReceivedLicensesOutput{}, nil },
		func(any) (any, error) { return nil, lmBadInput })
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v, want the ListTokens ValidationException", err)
	}
	if len(rowsByNativeID(t, st, TypeLicenseManagerLicense)) != 1 {
		t.Error("the license phase's row is missing after a token error")
	}
}

func TestScanLicenseManager_TransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListReceivedLicenses")
	_, err := scanLicenseManagerForTest(t, st,
		func(any) (any, error) { return nil, lmThrottle },
		func(any) (any, error) { return nil, lmBadInput })
	if !isAPIErrorCode(err, "ValidationException") || isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v, want only the ListTokens ValidationException", err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for the throttled ListReceivedLicenses", *warnings)
	}
}

// License Manager throttles with RateLimitExceededException (HTTP 429), which
// the SDK retryer does not retry, so the first throttle reaches the scanner.
func TestScanLicenseManager_RateLimitExceededWarnsAndKeepsRows(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListTokens")
	rateLimited := &lmtypes.RateLimitExceededException{Message: ptrStr("Too many requests have been submitted. Try again after a brief wait.")}
	total, err := scanLicenseManagerForTest(t, st,
		func(any) (any, error) {
			return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "self", lmtypes.LicenseStatusAvailable)}}, nil
		},
		func(any) (any, error) { return nil, rateLimited })
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2 (license, received license), nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for the rate-limited ListTokens", *warnings)
	}
	if len(rowsByNativeID(t, st, TypeLicenseManagerLicense)) != 1 || len(rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)) != 1 {
		t.Error("a row listed before the throttle is missing")
	}
}

// lmRetryingConfig answers each op from handlers below the SDK's Retry
// middleware, so the client's real retryer sees every error; opStubConfig
// answers at Initialize, above Retry, and allows one attempt. Handlers get a
// nil input. Backoff is zero to keep the test fast.
func lmRetryingConfig(handlers map[string]func(any) (any, error)) sdkaws.Config {
	stub := func(s *smithymw.Stack) error {
		return s.Finalize.Insert(smithymw.FinalizeMiddlewareFunc("discoLMStub",
			func(ctx context.Context, _ smithymw.FinalizeInput, _ smithymw.FinalizeHandler) (smithymw.FinalizeOutput, smithymw.Metadata, error) {
				h, ok := handlers[awsmw.GetOperationName(ctx)]
				if !ok {
					return smithymw.FinalizeOutput{}, smithymw.Metadata{}, lmDenied
				}
				out, err := h(nil)
				return smithymw.FinalizeOutput{Result: out}, smithymw.Metadata{}, err
			}), "Retry", smithymw.After)
	}
	return sdkaws.Config{
		Region:      testRegion,
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		Retryer: func() sdkaws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = 3
				o.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
			})
		},
		APIOptions: []func(*smithymw.Stack) error{stub},
	}
}

// A single throttle is retried by the client scanLicenseManager builds, so the
// retried call's rows are stored and no warning is recorded.
func TestScanLicenseManager_RateLimitExceededOnceIsRetried(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListTokens")
	var calls int
	handlers := lmTopLevelHandlers(
		func(any) (any, error) { return &licensemanager.ListReceivedLicensesOutput{}, nil },
		func(any) (any, error) {
			calls++
			if calls == 1 {
				return nil, &lmtypes.RateLimitExceededException{Message: ptrStr("Too many requests have been submitted. Try again after a brief wait.")}
			}
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}}, nil
		})
	acct := newTestAccount(testAccountID)
	acct.cfg = lmRetryingConfig(handlers)
	total, _, err := scanLicenseManager(context.Background(), acct, "", st, testScanID)
	if err != nil || total != 2 || calls != 2 {
		t.Fatalf("total, ListTokens calls, err = %d, %d, %v; want 2 (license, token), 2, nil", total, calls, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerToken)[lmTestLicenseArn+"/token/t1"]; !ok {
		t.Error("token row from the retried ListTokens is missing")
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d, want 0", *warnings)
	}
}

func TestScanLMReceivedLicenses_StatusUnsetWithoutReceivedMetadata(t *testing.T) {
	st := newTestStore(t)
	_, err := scanLMReceivedForTest(t, st, func(*licensemanager.ListReceivedLicensesInput) (any, error) {
		return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{
			lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable),
		}}, nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if s := rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)[lmTestLicenseArn+"/received"].Status; s != nil {
		t.Errorf("status = %q, want unset rather than the issuer's AVAILABLE", *s)
	}
}

func TestScanLMPhases_ServiceRoleMissingWarnsAndKeepsEarlierPages(t *testing.T) {
	notSetUp := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "Service role not found"}
	for _, tc := range []struct {
		name string
		op   string
		typ  string
		run  func(*store.Store) (int, error)
	}{
		{"received-licenses", "license-manager:ListReceivedLicenses", TypeLicenseManagerReceivedLicense, func(st *store.Store) (int, error) {
			return scanLMReceivedForTest(t, st, func(in *licensemanager.ListReceivedLicensesInput) (any, error) {
				if sv(in.NextToken) == "" {
					return &licensemanager.ListReceivedLicensesOutput{
						Licenses:  []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable)},
						NextToken: ptrStr("p2"),
					}, nil
				}
				return nil, notSetUp
			})
		}},
		{"tokens", "license-manager:ListTokens", TypeLicenseManagerToken, func(st *store.Store) (int, error) {
			return scanLMTokensForTest(t, st, func(in *licensemanager.ListTokensInput) (any, error) {
				if sv(in.NextToken) == "" {
					return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("p2")}, nil
				}
				return nil, notSetUp
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, tc.op)
			total, err := tc.run(st)
			if err != nil || total != 1 {
				t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
			}
			if n := len(rowsByNativeID(t, st, tc.typ)); n != 1 {
				t.Errorf("stored rows = %d, want the 1 from page 1", n)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
		})
	}
}

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"sync"
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

// lmListingDenials are the denials every License Manager listing models:
// AccessDeniedException and License Manager's own AuthorizationException.
var lmListingDenials = []struct {
	name string
	err  error
}{
	{"AccessDeniedException", lmDenied},
	{lmAuthorizationDenied, apiErr(lmAuthorizationDenied, "not permitted")},
}

func TestScanLMReceivedLicenses_DeniedWarnsAndKeepsEarlierPages(t *testing.T) {
	for _, denial := range lmListingDenials {
		t.Run(denial.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "license-manager:ListReceivedLicenses")
			total, err := scanLMReceivedForTest(t, st, func(in *licensemanager.ListReceivedLicensesInput) (any, error) {
				if sv(in.NextToken) == "" {
					return &licensemanager.ListReceivedLicensesOutput{
						Licenses:  []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable)},
						NextToken: ptrStr("p2"),
					}, nil
				}
				return nil, denial.err
			})
			if err != nil || total != 1 {
				t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
		})
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

// A repeated token fails the listing; the license already listed is stored.
func TestScanLMReceivedLicenses_FailsOnRepeatedToken(t *testing.T) {
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
	if !errors.Is(err, errRepeatedNextToken) || calls != 2 {
		t.Errorf("calls, err = %d, %v; want 2, errRepeatedNextToken", calls, err)
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
	for _, denial := range lmListingDenials {
		t.Run(denial.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "license-manager:ListTokens")
			total, err := scanLMTokensForTest(t, st, func(in *licensemanager.ListTokensInput) (any, error) {
				if sv(in.NextToken) == "" {
					return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("p2")}, nil
				}
				return nil, denial.err
			})
			if err != nil || total != 1 {
				t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
		})
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

// A repeated token fails the listing; the token already listed is stored.
func TestScanLMTokens_FailsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	var calls int
	_, err := scanLMTokensForTest(t, st, func(*licensemanager.ListTokensInput) (any, error) {
		calls++
		if calls > 2 {
			return nil, lmBadInput
		}
		return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("same")}, nil
	})
	if !errors.Is(err, errRepeatedNextToken) || calls != 2 {
		t.Errorf("calls, err = %d, %v; want 2, errRepeatedNextToken", calls, err)
	}
	if n := len(rowsByNativeID(t, st, TypeLicenseManagerToken)); n != 1 {
		t.Errorf("stored rows = %d, want 1", n)
	}
}

// lmTopLevelHandlers answers every existing License Manager list op with one
// license and nothing else, the two new ops with the given handlers, and
// every tag read with no tags.
func lmTopLevelHandlers(received, tokens func(any) (any, error)) map[string]func(any) (any, error) {
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(any) (any, error){
		"ListTagsForResource": empty(&licensemanager.ListTagsForResourceOutput{}),
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
// Alone, it is returned as a transient error, so the scan reads as partial,
// and the rows listed before it are kept.
func TestScanLicenseManager_RateLimitExceededIsTransientAndKeepsRows(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "license-manager:ListTokens")
	rateLimited := &lmtypes.RateLimitExceededException{Message: ptrStr("Too many requests have been submitted. Try again after a brief wait.")}
	total, err := scanLicenseManagerForTest(t, st,
		func(any) (any, error) {
			return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "self", lmtypes.LicenseStatusAvailable)}}, nil
		},
		func(any) (any, error) { return nil, rateLimited })
	if got := classifyServiceError(err); got != outcomeTransient || total != 2 {
		t.Fatalf("total, outcome = %d, %v (err %v); want 2 (license, received license), outcomeTransient", total, got, err)
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d, want 0: a lone transient is returned, not demoted", *warnings)
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

// lmGrantARN is the ARN of the test grant named id.
func lmGrantARN(id string) string {
	return "arn:aws:license-manager::" + testAccountID + ":grant:g-" + id
}

// scanLicenseManagerWith runs scanLicenseManager against lmTopLevelHandlers
// with empty received-license and token listings, overridden by h.
func scanLicenseManagerWith(t *testing.T, st *store.Store, h map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	handlers := lmTopLevelHandlers(
		func(any) (any, error) { return &licensemanager.ListReceivedLicensesOutput{}, nil },
		func(any) (any, error) { return &licensemanager.ListTokensOutput{}, nil })
	for op, f := range h {
		handlers[op] = f
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lmDenied)
	total, _, err := scanLicenseManager(context.Background(), acct, "", st, testScanID)
	return total, err
}

// A failing phase does not stop the phases after it: the first phase's error
// is returned with the rows of every later phase.
func TestScanLicenseManager_FailedPhaseRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLicenseManagerWith(t, st, map[string]func(any) (any, error){
		"ListLicenses": func(any) (any, error) { return nil, lmBadInput },
		"ListDistributedGrants": func(any) (any, error) {
			return &licensemanager.ListDistributedGrantsOutput{Grants: []lmtypes.Grant{{GrantArn: ptrStr(lmGrantARN("1")), GrantName: ptrStr("g1")}}}, nil
		},
		"ListTokens": func(any) (any, error) {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}}, nil
		},
	})
	assertAPICode(t, err, "ValidationException")
	if total != 2 {
		t.Errorf("total = %d, want 2 (grant, token)", total)
	}
	if len(rowsByNativeID(t, st, TypeLicenseManagerGrant)) != 1 || len(rowsByNativeID(t, st, TypeLicenseManagerToken)) != 1 {
		t.Error("a phase after the failed ListLicenses stored nothing")
	}
}

// A listing that fails or repeats its token after its first page keeps that
// page's rows and returns the error.
func TestScanLicenseManager_LaterPageErrorKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		page2   func() (any, error)
		wantErr func(error) bool
	}{
		{"error", func() (any, error) { return nil, lmBadInput }, func(err error) bool { return isAPIErrorCode(err, "ValidationException") }},
		{"repeated token", func() (any, error) {
			return &licensemanager.ListDistributedGrantsOutput{NextToken: ptrStr("p2")}, nil
		}, func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			_, err := scanLicenseManagerWith(t, st, map[string]func(any) (any, error){
				"ListDistributedGrants": func(in any) (any, error) {
					if in.(*licensemanager.ListDistributedGrantsInput).NextToken == nil {
						return &licensemanager.ListDistributedGrantsOutput{
							Grants:    []lmtypes.Grant{{GrantArn: ptrStr(lmGrantARN("1")), GrantName: ptrStr("g1")}},
							NextToken: ptrStr("p2"),
						}, nil
					}
					return tc.page2()
				},
			})
			if !tc.wantErr(err) {
				t.Errorf("err = %v, want the page 2 failure", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeLicenseManagerGrant)[lmGrantARN("1")]; !ok {
				t.Error("grant from page 1 not stored")
			}
		})
	}
}

// A received-license or token listing that fails after its first page keeps
// that page's rows and returns the error.
func TestScanLMReceivedAndTokens_LaterPageErrorKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLMReceivedForTest(t, st, func(in *licensemanager.ListReceivedLicensesInput) (any, error) {
		if in.NextToken == nil {
			return &licensemanager.ListReceivedLicensesOutput{
				Licenses:  []lmtypes.GrantedLicense{lmGranted(lmTestLicenseArn, "a", lmtypes.LicenseStatusAvailable)},
				NextToken: ptrStr("p2"),
			}, nil
		}
		return nil, lmBadInput
	})
	assertAPICode(t, err, "ValidationException")
	if total != 1 || len(rowsByNativeID(t, st, TypeLicenseManagerReceivedLicense)) != 1 {
		t.Errorf("received licenses: total = %d, want 1 stored from page 1", total)
	}

	st = newTestStore(t)
	total, err = scanLMTokensForTest(t, st, func(in *licensemanager.ListTokensInput) (any, error) {
		if in.NextToken == nil {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("t1", lmTestLicenseArn, "AVAILABLE")}, NextToken: ptrStr("p2")}, nil
		}
		return nil, lmBadInput
	})
	assertAPICode(t, err, "ValidationException")
	if total != 1 || len(rowsByNativeID(t, st, TypeLicenseManagerToken)) != 1 {
		t.Errorf("tokens: total = %d, want 1 stored from page 1", total)
	}
}

// "Service role not found" reports the service disabled only when no phase
// stored a row, whatever its order, and no phase failed otherwise; else it is a
// partial denial that warns once and keeps the other phases' rows.
func TestScanLicenseManager_NotSetUpSentinelOnlyWhenNothingElse(t *testing.T) {
	type handlers = map[string]func(any) (any, error)
	notSetUp := func(any) (any, error) {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "Service role not found"}
	}
	rateLimited := func(any) (any, error) {
		return nil, &lmtypes.RateLimitExceededException{Message: ptrStr("Too many requests have been submitted.")}
	}
	noLicenses := func(any) (any, error) { return &licensemanager.ListLicensesOutput{}, nil }
	configurations := func(any) (any, error) {
		return &licensemanager.ListLicenseConfigurationsOutput{LicenseConfigurations: []lmtypes.LicenseConfiguration{
			{LicenseConfigurationArn: ptrStr("arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-1"), Name: ptrStr("c1")},
		}}, nil
	}
	isNil := func(t *testing.T, err error) {
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	}
	for _, tc := range []struct {
		name      string
		h         handlers
		wantErr   func(*testing.T, error)
		wantTotal int
		wantWarns int
	}{
		{"nothing else stored", handlers{"ListLicenses": notSetUp}, func(t *testing.T, err error) {
			if !errors.Is(err, errServiceDisabled) {
				t.Errorf("err = %v, want errServiceDisabled", err)
			}
		}, 0, 0},
		{"after a stored row", handlers{"ListDistributedGrants": notSetUp}, isNil, 1, 1},
		{"before a later phase's row", handlers{"ListLicenses": noLicenses, "ListDistributedGrants": notSetUp, "ListLicenseConfigurations": configurations}, isNil, 1, 1},
		{"after a transient phase", handlers{"ListLicenses": rateLimited, "ListDistributedGrants": notSetUp}, func(t *testing.T, err error) {
			if got := classifyServiceError(err); got != outcomeTransient {
				t.Errorf("err = %v (outcome %v), want outcomeTransient", err, got)
			}
		}, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, lmListGrantsOp)
			total, err := scanLicenseManagerWith(t, st, tc.h)
			tc.wantErr(t, err)
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if *warnings != tc.wantWarns {
				t.Errorf("ListDistributedGrants warnings = %d, want %d", *warnings, tc.wantWarns)
			}
		})
	}
}

// An item listed without the identifier its row is keyed by is not stored and
// is warned about once under its op.
func TestScanLicenseManager_UnkeyedItemsWarn(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	warned := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warned[w.Service]++
		mu.Unlock()
	}
	_, err := scanLicenseManagerWith(t, st, map[string]func(any) (any, error){
		"ListDistributedGrants": func(any) (any, error) {
			return &licensemanager.ListDistributedGrantsOutput{Grants: []lmtypes.Grant{
				{GrantArn: ptrStr(lmGrantARN("1")), GrantName: ptrStr("g1")},
				{GrantName: ptrStr("no-arn")},
			}}, nil
		},
		"ListReceivedLicenses": func(any) (any, error) {
			return &licensemanager.ListReceivedLicensesOutput{Licenses: []lmtypes.GrantedLicense{{LicenseName: ptrStr("no-arn"), Status: lmtypes.LicenseStatusAvailable}}}, nil
		},
		"ListTokens": func(any) (any, error) {
			return &licensemanager.ListTokensOutput{Tokens: []lmtypes.TokenData{lmToken("", lmTestLicenseArn, "AVAILABLE")}}, nil
		},
	})
	if err != nil {
		t.Fatalf("scanLicenseManager: %v", err)
	}
	for _, op := range []string{"license-manager:ListDistributedGrants", "license-manager:ListReceivedLicenses", "license-manager:ListTokens"} {
		if warned[op] != 1 {
			t.Errorf("%s warnings = %d, want 1", op, warned[op])
		}
	}
	if n := len(rowsByNativeID(t, st, TypeLicenseManagerGrant)); n != 1 {
		t.Errorf("grants stored = %d, want 1", n)
	}
}

// lmTestTagTotal is the rows of lmTagHandlers: two per ARN-keyed listing.
const lmTestTagTotal = 2 * 6

// lmElem is one listed item of a test listing: its ARN, its name, and the
// tags a report generator's element carries (nil for none).
type lmElem struct {
	arn, name string
	tags      map[string]string
}

// lmKind is one ARN-keyed License Manager listing.
type lmKind struct {
	op, typ string
	// prefix is the ARN resource part before the item's id; arnField the
	// element field holding the ARN.
	prefix, arnField string
	// regional is whether the type's ARNs carry a region.
	regional bool
	// status is the Status of every row ("" for none).
	status string
	// tagRead is whether the scan reads the rows' tags: every kind but the
	// report generator, whose element carries them.
	tagRead bool
	// maxResults is the MaxResults the listing sends (0 for none).
	maxResults int32
	// page builds the op's output holding es.
	page func(es []lmElem) any
}

func lmKinds() []lmKind {
	return []lmKind{
		{
			op: "ListLicenses", typ: TypeLicenseManagerLicense, prefix: "license:l-", arnField: "LicenseArn", status: "AVAILABLE", tagRead: true, maxResults: 100,
			page: func(es []lmElem) any {
				out := &licensemanager.ListLicensesOutput{}
				for _, e := range es {
					out.Licenses = append(out.Licenses, lmtypes.License{LicenseArn: ptrStr(e.arn), LicenseName: ptrStr(e.name), Status: lmtypes.LicenseStatusAvailable})
				}
				return out
			},
		},
		{
			op: "ListDistributedGrants", typ: TypeLicenseManagerGrant, prefix: "grant:g-", arnField: "GrantArn", status: "ACTIVE", tagRead: true, maxResults: 100,
			page: func(es []lmElem) any {
				out := &licensemanager.ListDistributedGrantsOutput{}
				for _, e := range es {
					out.Grants = append(out.Grants, lmtypes.Grant{GrantArn: ptrStr(e.arn), GrantName: ptrStr(e.name), GrantStatus: lmtypes.GrantStatusActive})
				}
				return out
			},
		},
		{
			op: "ListLicenseConfigurations", typ: TypeLicenseManagerLicenseConfiguration, prefix: "license-configuration:lic-", arnField: "LicenseConfigurationArn", regional: true, status: "AVAILABLE", tagRead: true,
			page: func(es []lmElem) any {
				out := &licensemanager.ListLicenseConfigurationsOutput{}
				for _, e := range es {
					out.LicenseConfigurations = append(out.LicenseConfigurations, lmtypes.LicenseConfiguration{
						LicenseConfigurationArn: ptrStr(e.arn), Name: ptrStr(e.name), Status: ptrStr("AVAILABLE"), OwnerAccountId: ptrStr(lmTestARNAccount(e.arn)),
					})
				}
				return out
			},
		},
		{
			op: "ListLicenseManagerReportGenerators", typ: TypeLicenseManagerReportGenerator, prefix: "report-generator:r-", arnField: "LicenseManagerReportGeneratorArn", regional: true, maxResults: 100,
			page: func(es []lmElem) any {
				out := &licensemanager.ListLicenseManagerReportGeneratorsOutput{}
				for _, e := range es {
					out.ReportGenerators = append(out.ReportGenerators, lmtypes.ReportGenerator{LicenseManagerReportGeneratorArn: ptrStr(e.arn), ReportGeneratorName: ptrStr(e.name), Tags: lmTestTags(e.tags)})
				}
				return out
			},
		},
		{
			op: "ListLicenseAssetGroups", typ: TypeLicenseManagerLicenseAssetGroup, prefix: "license-asset-group:", arnField: "LicenseAssetGroupArn", regional: true, tagRead: true,
			page: func(es []lmElem) any {
				out := &licensemanager.ListLicenseAssetGroupsOutput{}
				for _, e := range es {
					out.LicenseAssetGroups = append(out.LicenseAssetGroups, lmtypes.LicenseAssetGroup{LicenseAssetGroupArn: ptrStr(e.arn), Name: ptrStr(e.name)})
				}
				return out
			},
		},
		{
			op: "ListLicenseAssetRulesets", typ: TypeLicenseManagerLicenseAssetRuleset, prefix: "license-asset-ruleset:", arnField: "LicenseAssetRulesetArn", regional: true, tagRead: true,
			page: func(es []lmElem) any {
				out := &licensemanager.ListLicenseAssetRulesetsOutput{}
				for _, e := range es {
					out.LicenseAssetRulesets = append(out.LicenseAssetRulesets, lmtypes.LicenseAssetRuleset{LicenseAssetRulesetArn: ptrStr(e.arn), Name: ptrStr(e.name)})
				}
				return out
			},
		},
	}
}

func lmKindOf(t *testing.T, op string) lmKind {
	t.Helper()
	for _, k := range lmKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no ARN-keyed License Manager listing", op)
	return lmKind{}
}

// arn is the ARN of k's item id in the Service Reference's format, owned by
// testAccountID: a license's and a grant's carry no region.
func (k lmKind) arn(partition, region, id string) string {
	if !k.regional {
		region = ""
	}
	return "arn:" + partition + ":license-manager:" + region + ":" + testAccountID + ":" + k.prefix + id
}

// lmTestARNAccount is the account field of arn.
func lmTestARNAccount(arn string) string {
	a, _ := parseARN(arn)
	return a.AccountID
}

// lmTestTags is m as License Manager tags, in key order.
func lmTestTags(m map[string]string) []lmtypes.Tag {
	var tags []lmtypes.Tag
	for _, k := range slices.Sorted(maps.Keys(m)) {
		tags = append(tags, lmtypes.Tag{Key: ptrStr(k), Value: ptrStr(m[k])})
	}
	return tags
}

// lmCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type lmCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	max     map[string]*int32
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newLMCalls tags the "tagged" item of every kind whose tags are read with
// its type.
func newLMCalls(partition, region string) *lmCalls {
	c := &lmCalls{ops: map[string]int{}, max: map[string]*int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range lmKinds() {
		if k.tagRead {
			c.tags[k.arn(partition, region, "tagged")] = map[string]string{"type": k.typ}
		}
	}
	return c
}

func (c *lmCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *lmCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.max[op] = bacField[*int32](in, "MaxResults")
}

func (c *lmCalls) listTags(in any) (any, error) {
	arn := sv(in.(*licensemanager.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &licensemanager.ListTagsForResourceOutput{Tags: lmTestTags(c.tags[arn])}, nil
}

// lmTagHandlers answers each kind's op with one page holding its "tagged"
// and "bare" items (a report generator's tagged element carries its tags),
// the received-license and token listings with nothing, and
// ListTagsForResource from c.
func lmTagHandlers(c *lmCalls, partition, region string) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListReceivedLicenses": func(in any) (any, error) {
			c.record("ListReceivedLicenses", in)
			return &licensemanager.ListReceivedLicensesOutput{}, nil
		},
		"ListTokens": func(in any) (any, error) {
			c.record("ListTokens", in)
			return &licensemanager.ListTokensOutput{}, nil
		},
	}
	for _, k := range lmKinds() {
		es := []lmElem{
			{k.arn(partition, region, "tagged"), k.typ + " tagged", map[string]string{"type": k.typ}},
			{k.arn(partition, region, "bare"), k.typ + " bare", nil},
		}
		h[k.op] = func(in any) (any, error) {
			c.record(k.op, in)
			return k.page(es), nil
		}
	}
	return h
}

// lmTagScan runs scanLicenseManager for an account of partition against h.
func lmTagScan(ctx context.Context, st *store.Store, partition string, h map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = partition
	acct.cfg = opStubConfig(h, lmDenied)
	return scanLicenseManager(ctx, acct, "", st, testScanID)
}

// Every ARN-keyed listing is stored with its tags: read per row with
// ListTagsForResource, sent exactly the listed ARNs, except the report
// generator's, which its element carries. A row without tags stores none.
// Received licenses and tokens are never sent.
func TestScanLicenseManager_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	calls := newLMCalls("aws", testRegion)
	total, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(calls, "aws", testRegion))
	if err != nil || total != lmTestTagTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal)
	}
	var wantARNs []string
	for _, k := range lmKinds() {
		if got := calls.max[k.op]; (k.maxResults == 0) != (got == nil) || (got != nil && *got != k.maxResults) {
			t.Errorf("%s MaxResults = %v; want %d (0: unset)", k.op, got, k.maxResults)
		}
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, id := range []string{"tagged", "bare"} {
			arn := k.arn("aws", testRegion, id)
			if k.tagRead {
				wantARNs = append(wantARNs, arn)
			}
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s not stored", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.typ+" "+id || sv(r.Status) != k.status || (k.status == "") != (r.Status == nil) || sv(r.Region) != "global" {
				t.Errorf("%s: name %q status %v region %q; want %q, %q, %q", arn, sv(r.Name), r.Status, sv(r.Region), k.typ+" "+id, k.status, "global")
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[k.arnField] != arn {
				t.Errorf("%s attrs %s = %v (%v); want the listed element's ARN", arn, k.arnField, attrs[k.arnField], err)
			}
			if id == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	got := slices.Sorted(slices.Values(calls.tagARNs))
	slices.Sort(wantARNs)
	if !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}
}

func TestScanLicenseManager_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(newLMCalls("aws", testRegion), "aws", testRegion)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(newLMCalls("aws", testRegion), "aws", testRegion))
	if err != nil || total != lmTestTagTotal || inserted != 0 {
		t.Errorf("rescan total, inserted, err = %d, %d, %v; want %d, 0, nil", total, inserted, err, lmTestTagTotal)
	}
}

// A failed tag read keeps the row untagged. A denial (AccessDeniedException,
// or License Manager's AuthorizationException) warns once under the tag op; a
// throttle is transient; any other error, a not-found code the op does not
// model included, is an error.
func TestScanLicenseManager_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantWarns int
	}
	var cases []tagCase
	for _, k := range lmKinds() {
		if !k.tagRead {
			continue
		}
		arn := k.arn("aws", testRegion, "tagged")
		cases = append(cases,
			tagCase{k.typ + "/denied", arn, k.typ, lmDenied, -1, "", 1},
			tagCase{k.typ + "/unauthorized", arn, k.typ, apiErr("AuthorizationException", "not permitted"), -1, "", 1},
			tagCase{k.typ + "/transient", arn, k.typ, lmThrottle, outcomeTransient, "ThrottlingException", 0},
			tagCase{k.typ + "/unmodeled not found", arn, k.typ, apiErr("ResourceNotFoundException", "gone"), outcomeError, "ResourceNotFoundException", 0},
			tagCase{k.typ + "/other error", arn, k.typ, lmBadInput, outcomeError, "ValidationException", 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLMCalls("aws", testRegion)
			calls.errs[c.arn] = c.err

			total, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(calls, "aws", testRegion))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != lmTestTagTotal {
				t.Errorf("total = %d; want %d", total, lmTestTagTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if !ok {
				t.Fatalf("row %s not stored", c.arn)
			}
			if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != lmTagsOp || x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning under %s in %s; want %s in %s/%s", x.Service, x.Scope, lmTagsOp, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan.
func TestScanLicenseManager_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lmTagsOp)
	calls := newLMCalls("aws", testRegion)
	licenses, rulesets := lmKindOf(t, "ListLicenses"), lmKindOf(t, "ListLicenseAssetRulesets")
	calls.errs[licenses.arn("aws", testRegion, "tagged")] = lmDenied
	calls.errs[rulesets.arn("aws", testRegion, "bare")] = lmDenied

	total, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(calls, "aws", testRegion))
	if err != nil || total != lmTestTagTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", lmTagsOp, *warns)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanLicenseManager_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lmTagsOp)
	calls := newLMCalls("aws", testRegion)
	calls.errs[lmKindOf(t, "ListLicenses").arn("aws", testRegion, "tagged")] = lmThrottle
	calls.errs[lmKindOf(t, "ListLicenseAssetRulesets").arn("aws", testRegion, "bare")] = lmBadInput

	_, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(calls, "aws", testRegion))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", lmTagsOp, *warns)
	}
}

// A license configuration another account owns (shared in through AWS RAM)
// whose tag read is refused with either License Manager denial is kept
// untagged without a warning. Only license configurations get that leniency:
// a refused license of another account still warns.
func TestScanLicenseManager_OtherAccountConfigurationUntaggedSilently(t *testing.T) {
	const other = "444455556666"
	sharedConfig := "arn:aws:license-manager:us-east-1:" + other + ":license-configuration:lic-shared"
	otherLicense := "arn:aws:license-manager::" + other + ":license:l-other"
	for _, denial := range lmListingDenials {
		for _, c := range []struct {
			op, arn   string
			wantWarns int
		}{
			{"ListLicenseConfigurations", sharedConfig, 0},
			{"ListLicenses", otherLicense, 1},
		} {
			t.Run(c.op+"/"+denial.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, lmTagsOp)
				calls := newLMCalls("aws", testRegion)
				calls.errs[c.arn] = denial.err
				h := lmTagHandlers(calls, "aws", testRegion)
				k := lmKindOf(t, c.op)
				h[c.op] = func(any) (any, error) { return k.page([]lmElem{{arn: c.arn, name: "other"}}), nil }

				total, _, err := lmTagScan(context.Background(), st, "aws", h)
				if err != nil || total != lmTestTagTotal-1 {
					t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal-1)
				}
				if r, ok := rowsByNativeID(t, st, k.typ)[c.arn]; !ok || r.TagsJSON != nil {
					t.Errorf("%s stored %v; want stored untagged", c.arn, ok)
				}
				if *warns != c.wantWarns {
					t.Errorf("%s warnings = %d; want %d", lmTagsOp, *warns, c.wantWarns)
				}
			})
		}
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows.
func TestScanLicenseManager_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeLicenseManagerLicenseConfiguration)
	calls := newLMCalls("aws", testRegion)
	calls.errs[lmKindOf(t, "ListLicenseConfigurations").arn("aws", testRegion, "tagged")] = lmDenied

	total, _, err := lmTagScan(context.Background(), st, "aws", lmTagHandlers(calls, "aws", testRegion))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListLicenseManagerReportGenerators", "ListLicenseAssetGroups", "ListLicenseAssetRulesets", "ListReceivedLicenses", "ListTokens"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// Licenses and grants, two each, ran before it.
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
}

// An aws-iso-b account sends the ARNs its listings return to the tag op
// unchanged and stores their tags. License Manager has no SDK-confirmed home
// in aws-us-gov or aws-cn, so no call is made there; aws-iso-b has one.
func TestScanLicenseManager_TagReadsKeepPartitionARNs(t *testing.T) {
	const partition, home = "aws-iso-b", "us-isob-east-1"
	st := newTestStore(t)
	calls := newLMCalls(partition, home)
	total, _, err := lmTagScan(context.Background(), st, partition, lmTagHandlers(calls, partition, home))
	if err != nil || total != lmTestTagTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal)
	}
	var want []string
	for _, k := range lmKinds() {
		if k.tagRead {
			want = append(want, k.arn(partition, home, "tagged"), k.arn(partition, home, "bare"))
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	configs := lmKindOf(t, "ListLicenseConfigurations")
	assertTagsMap(t, rowsByNativeID(t, st, configs.typ)[configs.arn(partition, home, "tagged")].TagsJSON, map[string]string{"type": configs.typ})
}

// Every listing returning nothing stores nothing, reads no tags and reports
// nothing.
func TestScanLicenseManager_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLMCalls("aws", testRegion)
	h := lmTagHandlers(calls, "aws", testRegion)
	for _, k := range lmKinds() {
		h[k.op] = func(any) (any, error) { return k.page(nil), nil }
	}
	total, _, err := lmTagScan(context.Background(), st, "aws", h)
	if err != nil || total != 0 {
		t.Fatalf("scan = %d, %v; want 0, nil", total, err)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// "Service role not found" on the first page of any listing, with every
// other listing empty, reports License Manager not set up and warns nothing.
func TestScanLicenseManager_NotSetUpOnAnyListingIsDisabled(t *testing.T) {
	notSetUp := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "Service role not found"}
	ops := []string{"ListReceivedLicenses", "ListTokens"}
	for _, k := range lmKinds() {
		ops = append(ops, k.op)
	}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			h := lmTagHandlers(newLMCalls("aws", testRegion), "aws", testRegion)
			for _, k := range lmKinds() {
				h[k.op] = func(any) (any, error) { return k.page(nil), nil }
			}
			h[op] = func(any) (any, error) { return nil, notSetUp }

			total, _, err := lmTagScan(context.Background(), st, "aws", h)
			if !errors.Is(err, errServiceDisabled) || total != 0 {
				t.Errorf("scan = %d, %v; want 0, errServiceDisabled", total, err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// although the phase in flight saw it in its listing.
func TestScanLicenseManager_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newLMCalls("aws", testRegion)
	h := lmTagHandlers(calls, "aws", testRegion)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	grants := lmKindOf(t, "ListDistributedGrants")
	h[grants.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		out := grants.page([]lmElem{{arn: grants.arn("aws", testRegion, "tagged"), name: "g"}}).(*licensemanager.ListDistributedGrantsOutput)
		out.NextToken = ptrStr("p2")
		return out, nil
	}

	_, _, err := lmTagScan(ctx, st, "aws", h)
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
	if n := calls.count("ListLicenseConfigurations"); n != 0 {
		t.Errorf("ListLicenseConfigurations called %d times after the stop; want 0", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeLicenseManagerGrant)[grants.arn("aws", testRegion, "tagged")]; !ok {
		t.Error("the grant of the page before the stop is not stored")
	}
}

// lmPageWithNext is out, a listing's output, with its NextToken set to next.
func lmPageWithNext(out any, next string) any {
	reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(&next))
	return out
}

// An ARN-keyed listing refused with License Manager's AuthorizationException
// is warned about once under its op, as AccessDeniedException is, and the
// scan does not fail: refused on its first page it stores nothing; refused
// after it, the row of the page read before it is kept with its tags.
func TestScanLicenseManager_AuthorizationExceptionListingWarns(t *testing.T) {
	refused := apiErr(lmAuthorizationDenied, "not permitted")
	for _, k := range lmKinds() {
		for _, afterFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after first page %v", k.op, afterFirst), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				h := lmTagHandlers(newLMCalls("aws", testRegion), "aws", testRegion)
				first := k.arn("aws", testRegion, "tagged")
				h[k.op] = func(in any) (any, error) {
					if !afterFirst || bacField[*string](in, "NextToken") != nil {
						return nil, refused
					}
					return lmPageWithNext(k.page([]lmElem{{first, "first", map[string]string{"type": k.typ}}}), "p2"), nil
				}

				total, _, err := lmTagScan(context.Background(), st, "aws", h)
				wantRows := 0
				if afterFirst {
					wantRows = 1
				}
				if err != nil || total != lmTestTagTotal-2+wantRows {
					t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal-2+wantRows)
				}
				rows := rowsByNativeID(t, st, k.typ)
				if len(rows) != wantRows {
					t.Fatalf("%s rows = %d; want %d", k.typ, len(rows), wantRows)
				}
				if afterFirst {
					assertTagsMap(t, rows[first].TagsJSON, map[string]string{"type": k.typ})
				}
				w := warns()
				if len(w) != 1 || w[0].Service != "license-manager:"+k.op || w[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warnings = %v; want one under license-manager:%s in %s/%s", w, k.op, testAccountID, testRegion)
				}
			})
		}
	}
}

// A report-generator listing refused after its first page, whose write was
// refused, fails the scan without warning about later pages: the rows the
// warning would speak for were lost with the write.
func TestScanLMReportGenerators_DenialAfterFailedWriteNotWarned(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeLicenseManagerReportGenerator)
	h := lmTagHandlers(newLMCalls("aws", testRegion), "aws", testRegion)
	k := lmKindOf(t, "ListLicenseManagerReportGenerators")
	h[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			return nil, apiErr(lmAuthorizationDenied, "not permitted")
		}
		return lmPageWithNext(k.page([]lmElem{{k.arn("aws", testRegion, "tagged"), "first", nil}}), "p2"), nil
	}

	_, _, err := lmTagScan(context.Background(), st, "aws", h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A tagged listing writes each page, with its rows' tags, before it reads
// the next, so a large listing is never held whole.
func TestScanLicenseManager_TaggedListingWritesEachPageBeforeTheNext(t *testing.T) {
	st := newTestStore(t)
	calls := newLMCalls("aws", testRegion)
	h := lmTagHandlers(calls, "aws", testRegion)
	licenses := lmKindOf(t, "ListLicenses")
	first, second := licenses.arn("aws", testRegion, "tagged"), licenses.arn("aws", testRegion, "bare")
	var atPage2 map[string]store.Resource
	h[licenses.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") == nil {
			return lmPageWithNext(licenses.page([]lmElem{{arn: first, name: "first"}}), "p2"), nil
		}
		atPage2 = rowsByNativeID(t, st, licenses.typ)
		return licenses.page([]lmElem{{arn: second, name: "second"}}), nil
	}

	total, _, err := lmTagScan(context.Background(), st, "aws", h)
	if err != nil || total != lmTestTagTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, lmTestTagTotal)
	}
	r, ok := atPage2[first]
	if len(atPage2) != 1 || !ok {
		t.Fatalf("licenses stored when page 2 was read = %v; want only %s", slices.Sorted(maps.Keys(atPage2)), first)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": licenses.typ})
	if _, ok := rowsByNativeID(t, st, licenses.typ)[second]; !ok {
		t.Errorf("license %s of page 2 not stored", second)
	}
}

// An NXDOMAIN on the first listing with nothing stored says the region does
// not serve License Manager: no later listing is called, nothing is warned
// about, and the scan reads as unavailable.
func TestScanLicenseManager_FirstListingNXDOMAINSkipsTheRest(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLMCalls("aws", testRegion)
	h := lmTagHandlers(calls, "aws", testRegion)
	h["ListLicenses"] = func(in any) (any, error) {
		calls.record("ListLicenses", in)
		return nil, &net.DNSError{Err: "no such host", Name: "license-manager.us-east-1.amazonaws.com", IsNotFound: true}
	}

	total, _, err := lmTagScan(context.Background(), st, "aws", h)
	assertGlueOutcome(t, err, outcomeUnavailable)
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
	if n := calls.count("ListLicenses"); n != 1 {
		t.Errorf("ListLicenses called %d times; want 1", n)
	}
	for _, op := range []string{"ListDistributedGrants", "ListLicenseConfigurations", "ListLicenseManagerReportGenerators", "ListLicenseAssetGroups", "ListLicenseAssetRulesets", "ListReceivedLicenses", "ListTokens", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the NXDOMAIN; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

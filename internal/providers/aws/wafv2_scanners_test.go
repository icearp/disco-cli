package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// The model allows any non-space key; "/" pins that the key goes last.
	wafKeyA  = "key-a/with+slash=="
	wafKeyB  = "key-b"
	wafKeyCF = "key-cf"
)

var wafKeyCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// wafAPIKeyCalls records each ListAPIKeys input.
type wafAPIKeyCalls struct {
	mu  sync.Mutex
	ins []wafv2.ListAPIKeysInput
}

func (c *wafAPIKeyCalls) add(in *wafv2.ListAPIKeysInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ins = append(c.ins, *in)
}

// wafv2Handlers answers every older WAFv2 phase with an empty listing and
// ListAPIKeys with two REGIONAL pages (one element without a key) and one
// CLOUDFRONT page.
func wafv2Handlers(calls *wafAPIKeyCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListWebACLs":               func(any) (any, error) { return &wafv2.ListWebACLsOutput{}, nil },
		"ListRuleGroups":            func(any) (any, error) { return &wafv2.ListRuleGroupsOutput{}, nil },
		"ListIPSets":                func(any) (any, error) { return &wafv2.ListIPSetsOutput{}, nil },
		"ListLoggingConfigurations": func(any) (any, error) { return &wafv2.ListLoggingConfigurationsOutput{}, nil },
		"ListRegexPatternSets":      func(any) (any, error) { return &wafv2.ListRegexPatternSetsOutput{}, nil },
		"ListManagedRuleSets":       func(any) (any, error) { return &wafv2.ListManagedRuleSetsOutput{}, nil },
		"ListAPIKeys": func(in any) (any, error) {
			req := in.(*wafv2.ListAPIKeysInput)
			calls.add(req)
			if req.Scope == types.ScopeCloudfront {
				return &wafv2.ListAPIKeysOutput{APIKeySummaries: []types.APIKeySummary{
					{APIKey: sdkaws.String(wafKeyCF), TokenDomains: []string{"cdn.example.com"}},
				}}, nil
			}
			if req.NextMarker == nil {
				return &wafv2.ListAPIKeysOutput{
					APIKeySummaries: []types.APIKeySummary{
						{APIKey: sdkaws.String(wafKeyA), TokenDomains: []string{"a.example.com", "example.org"}, CreationTimestamp: &wafKeyCreated, Version: 3},
						{TokenDomains: []string{"no-key.example.com"}},
					},
					NextMarker: sdkaws.String("m1"),
				}, nil
			}
			return &wafv2.ListAPIKeysOutput{APIKeySummaries: []types.APIKeySummary{
				{APIKey: sdkaws.String(wafKeyB), TokenDomains: []string{"b.example.com"}},
			}}, nil
		},
	}
}

// wafv2ScanResult is what one scanWAFv2 run returned and stored.
type wafv2ScanResult struct {
	total, inserted int
	rows            map[string]wafv2StoredKey
	keyWarns        int // warnings recorded for ListAPIKeys
	err             error
}

func scanWAFv2With(t *testing.T, h map[string]func(in any) (any, error)) wafv2ScanResult {
	t.Helper()
	st := newTestStore(t)
	warns := countOpWarnings(st, wafv2ListAPIKeysOp)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	var r wafv2ScanResult
	r.total, r.inserted, r.err = scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
	r.rows = storedWAFKeys(t, rowsByNativeID(t, st, TypeWAFv2APIKey))
	r.keyWarns = *warns
	return r
}

// wafv2StoredKey is the part of a stored API-key row the tests check.
type wafv2StoredKey struct {
	region    string
	createdAt string
	attrs     types.APIKeySummary
}

func storedWAFKeys(t *testing.T, rows map[string]store.Resource) map[string]wafv2StoredKey {
	t.Helper()
	out := map[string]wafv2StoredKey{}
	for id, r := range rows {
		var k wafv2StoredKey
		if err := json.Unmarshal([]byte(r.AttributesJSON), &k.attrs); err != nil {
			t.Fatalf("attrs of %s: %v", id, err)
		}
		k.region = sv(r.Region)
		k.createdAt = sv(r.CreatedAt)
		out[id] = k
	}
	return out
}

func TestScanWAFv2_APIKeysBothScopesPaged(t *testing.T) {
	var calls wafAPIKeyCalls
	res := scanWAFv2With(t, wafv2Handlers(&calls))
	if res.err != nil {
		t.Fatalf("scanWAFv2: %v", res.err)
	}
	rows := res.rows
	regA := "arn:aws:wafv2:us-east-1:" + testAccountID + ":regional/apikey/" + wafKeyA
	regB := "arn:aws:wafv2:us-east-1:" + testAccountID + ":regional/apikey/" + wafKeyB
	cf := "arn:aws:wafv2:us-east-1:" + testAccountID + ":global/apikey/" + wafKeyCF
	if res.total != 3 || res.inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", res.total, res.inserted)
	}
	if res.keyWarns != 1 {
		t.Errorf("%s warnings = %d; want 1 for the keyless summary", wafv2ListAPIKeysOp, res.keyWarns)
	}
	if len(rows) != 3 {
		t.Fatalf("api-key rows = %v; want exactly %s, %s, %s", rows, regA, regB, cf)
	}
	for _, id := range []string{regA, regB, cf} {
		r, ok := rows[id]
		if !ok {
			t.Errorf("no api-key row %s", id)
			continue
		}
		if r.region != "us-east-1" {
			t.Errorf("%s region = %q; want us-east-1", id, r.region)
		}
	}
	a := rows[regA]
	// The key is a public client token (registerType comment): a Redact rule
	// on it must fail here.
	if got := sv(a.attrs.APIKey); got != wafKeyA {
		t.Errorf("stored APIKey = %q; want %q unredacted", got, wafKeyA)
	}
	if !slices.Equal(a.attrs.TokenDomains, []string{"a.example.com", "example.org"}) || a.attrs.Version != 3 {
		t.Errorf("stored attrs = %+v; want TokenDomains [a.example.com example.org], Version 3", a.attrs)
	}
	if a.createdAt != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q; want 2026-01-02T03:04:05Z", a.createdAt)
	}

	var scopes []types.Scope
	for _, in := range calls.ins {
		scopes = append(scopes, in.Scope)
	}
	slices.Sort(scopes)
	if want := []types.Scope{types.ScopeCloudfront, types.ScopeRegional, types.ScopeRegional}; !slices.Equal(scopes, want) {
		t.Errorf("ListAPIKeys scopes = %v; want %v", scopes, want)
	}
}

func TestScanWAFv2_APIKeysRegionalOnlyOutsideUSEast1(t *testing.T) {
	var calls wafAPIKeyCalls
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(wafv2Handlers(&calls), apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	for _, in := range calls.ins {
		if in.Scope != types.ScopeRegional {
			t.Errorf("ListAPIKeys called with scope %s in eu-west-1; want REGIONAL only", in.Scope)
		}
	}
	if _, ok := rowsByNativeID(t, st, TypeWAFv2APIKey)["arn:aws:wafv2:eu-west-1:"+testAccountID+":regional/apikey/"+wafKeyB]; !ok {
		t.Error("no eu-west-1 regional api-key row")
	}
}

func TestScanWAFv2APIKeys_Empty(t *testing.T) {
	st := newTestStore(t)
	client := wafv2.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListAPIKeys": func(any) (any, error) { return &wafv2.ListAPIKeysOutput{}, nil },
	}, apiErr("AccessDeniedException", "unstubbed op")))
	total, inserted, err := scanWAFv2APIKeys(context.Background(), client, newTestAccount(testAccountID), testRegion, types.ScopeRegional, st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("scanWAFv2APIKeys = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if n := len(rowsByNativeID(t, st, TypeWAFv2APIKey)); n != 0 {
		t.Errorf("api-key rows = %d; want 0", n)
	}
}

func TestScanWAFv2APIKeys_ErrorAfterFirstPage(t *testing.T) {
	cases := []struct {
		name      string
		page2     func() (any, error)
		wantErr   func(error) bool
		wantWarns int
	}{
		{
			name: "denied warns and keeps page one",
			page2: func() (any, error) {
				return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListAPIKeys")
			},
			wantErr:   func(err error) bool { return err == nil },
			wantWarns: 1,
		},
		{
			name:    "modelled error propagates and keeps page one",
			page2:   func() (any, error) { return nil, apiErr("WAFInvalidOperationException", "bad") },
			wantErr: func(err error) bool { return isAPIErrorCode(err, "WAFInvalidOperationException") },
		},
		{
			name: "repeated marker fails and keeps page one",
			page2: func() (any, error) {
				return &wafv2.ListAPIKeysOutput{NextMarker: sdkaws.String("m1")}, nil
			},
			wantErr: func(err error) bool { return errors.Is(err, errRepeatedNextToken) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, wafv2ListAPIKeysOp)
			calls := 0 // the scanner pages sequentially
			client := wafv2.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
				"ListAPIKeys": func(in any) (any, error) {
					if calls++; calls > 5 {
						t.Errorf("ListAPIKeys called %d times; the scanner must stop by page 2", calls)
						return nil, apiErr("StubCallLimit", "stub call limit")
					}
					if in.(*wafv2.ListAPIKeysInput).NextMarker == nil {
						return &wafv2.ListAPIKeysOutput{
							APIKeySummaries: []types.APIKeySummary{{APIKey: sdkaws.String(wafKeyA)}},
							NextMarker:      sdkaws.String("m1"),
						}, nil
					}
					return tc.page2()
				},
			}, apiErr("AccessDeniedException", "unstubbed op")))
			total, _, err := scanWAFv2APIKeys(context.Background(), client, newTestAccount(testAccountID), testRegion, types.ScopeRegional, st, testScanID)
			if !tc.wantErr(err) {
				t.Errorf("err = %v; not the wanted error", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", wafv2ListAPIKeysOp, *warns, tc.wantWarns)
			}
			if total != 1 {
				t.Errorf("total = %d; want 1", total)
			}
			if _, ok := rowsByNativeID(t, st, TypeWAFv2APIKey)["arn:aws:wafv2:us-east-1:"+testAccountID+":regional/apikey/"+wafKeyA]; !ok {
				t.Error("page-one api-key row not stored")
			}
		})
	}
}

func TestScanWAFv2_APIKeysDeniedWarnsPerScope(t *testing.T) {
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	delete(h, "ListAPIKeys")
	st := newTestStore(t)
	warns := countOpWarnings(st, wafv2ListAPIKeysOp)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListAPIKeys"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if *warns != 2 {
		t.Errorf("%s warnings = %d; want 2 (REGIONAL and CLOUDFRONT)", wafv2ListAPIKeysOp, *warns)
	}
}

// The API-key phase runs after every older phase, and neither side's hard
// error skips the other.
func TestScanWAFv2_APIKeyAndOlderPhasesIndependent(t *testing.T) {
	acl := types.WebACL{ARN: sdkaws.String("arn:aws:wafv2:us-east-1:" + testAccountID + ":regional/webacl/a/1"), Name: sdkaws.String("a"), Id: sdkaws.String("1")}
	withACL := func(h map[string]func(in any) (any, error)) {
		h["ListWebACLs"] = func(in any) (any, error) {
			if in.(*wafv2.ListWebACLsInput).Scope != types.ScopeRegional {
				return &wafv2.ListWebACLsOutput{}, nil
			}
			return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{{ARN: acl.ARN, Name: acl.Name, Id: acl.Id}}}, nil
		}
		h["GetWebACL"] = func(any) (any, error) { return &wafv2.GetWebACLOutput{WebACL: &acl}, nil }
		h["ListResourcesForWebACL"] = func(any) (any, error) { return &wafv2.ListResourcesForWebACLOutput{}, nil }
	}

	t.Run("api-key failure keeps older rows", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		withACL(h)
		h["ListAPIKeys"] = func(any) (any, error) { return nil, apiErr("WAFInvalidParameterException", "bad") }
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		total, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
		if !isAPIErrorCode(err, "WAFInvalidParameterException") {
			t.Errorf("err = %v; want the ListAPIKeys WAFInvalidParameterException", err)
		}
		if total != 1 {
			t.Errorf("total = %d; want 1 (the web ACL)", total)
		}
		if _, ok := rowsByNativeID(t, st, TypeWAFv2WebACL)[sv(acl.ARN)]; !ok {
			t.Error("web ACL row not stored")
		}
	})

	t.Run("older failure still lists api keys", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		withACL(h)
		h["ListManagedRuleSets"] = func(any) (any, error) { return nil, apiErr("WAFInvalidOperationException", "bad") }
		res := scanWAFv2With(t, h)
		if !isAPIErrorCode(res.err, "WAFInvalidOperationException") {
			t.Errorf("err = %v; want the ListManagedRuleSets WAFInvalidOperationException", res.err)
		}
		if len(res.rows) != 3 || res.total != 4 {
			t.Errorf("api-key rows = %d, total = %d; want 3, 4 (the web ACL stored before the failure and 3 keys)", len(res.rows), res.total)
		}
	})

	t.Run("lone older transient reaches the dispatcher", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		h["ListWebACLs"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
		st := newTestStore(t)
		warns := countOpWarnings(st, "wafv2")
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeTransient {
			t.Errorf("classifyServiceError(%v) = %v; want outcomeTransient", err, got)
		}
		if *warns != 0 {
			t.Errorf("wafv2 warnings = %d; want 0, the lone transient is not demoted", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeWAFv2APIKey)); n != 3 {
			t.Errorf("api-key rows = %d; want 3", n)
		}
	})

	t.Run("lone api-key transient reaches the dispatcher", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		h["ListAPIKeys"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
		st := newTestStore(t)
		warns := countOpWarnings(st, wafv2ListAPIKeysOp)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		// eu-west-1 lists one scope, so one phase fails.
		_, _, err := scanWAFv2(context.Background(), acct, "eu-west-1", st, testScanID)
		if got := classifyServiceError(err); got != outcomeTransient {
			t.Errorf("classifyServiceError(%v) = %v; want outcomeTransient", err, got)
		}
		if *warns != 0 {
			t.Errorf("%s warnings = %d; want 0, the lone transient is not demoted", wafv2ListAPIKeysOp, *warns)
		}
	})

	t.Run("older transient does not mask api-key hard error", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		h["ListManagedRuleSets"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
		h["ListAPIKeys"] = func(any) (any, error) { return nil, apiErr("WAFInvalidParameterException", "bad") }
		st := newTestStore(t)
		warns := countOpWarnings(st, "wafv2")
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
		}
		if *warns != 1 {
			t.Errorf("wafv2 warnings = %d; want 1 for the demoted transient", *warns)
		}
	})

	t.Run("api-key transient does not mask older hard error", func(t *testing.T) {
		var calls wafAPIKeyCalls
		h := wafv2Handlers(&calls)
		h["ListManagedRuleSets"] = func(any) (any, error) { return nil, apiErr("WAFInvalidOperationException", "bad") }
		h["ListAPIKeys"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
		st := newTestStore(t)
		warns := countOpWarnings(st, wafv2ListAPIKeysOp)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
		}
		if *warns != 2 {
			t.Errorf("%s warnings = %d; want 2 demoted transients (one per scope)", wafv2ListAPIKeysOp, *warns)
		}
	})
}

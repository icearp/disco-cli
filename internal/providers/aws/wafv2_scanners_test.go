package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/icearp/disco-cli/internal/util"
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

// wafACLARN is the ARN of the regional test web ACL named name.
func wafACLARN(name string) string {
	return "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/webacl/" + name + "/1"
}

// wafWithACLs lists regional web ACLs a and b, whose GetWebACL fails for the
// names in fail, and associates no resources with either.
func wafWithACLs(h map[string]func(in any) (any, error), fail map[string]error) {
	h["ListWebACLs"] = func(in any) (any, error) {
		if in.(*wafv2.ListWebACLsInput).Scope != types.ScopeRegional {
			return &wafv2.ListWebACLsOutput{}, nil
		}
		return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{
			{ARN: sdkaws.String(wafACLARN("a")), Name: sdkaws.String("a"), Id: sdkaws.String("1"), LockToken: sdkaws.String("lock-a")},
			{ARN: sdkaws.String(wafACLARN("b")), Name: sdkaws.String("b"), Id: sdkaws.String("1"), LockToken: sdkaws.String("lock-b")},
		}}, nil
	}
	h["GetWebACL"] = func(in any) (any, error) {
		name := sv(in.(*wafv2.GetWebACLInput).Name)
		if err := fail[name]; err != nil {
			return nil, err
		}
		return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{ARN: sdkaws.String(wafACLARN(name)), Name: &name, Id: sdkaws.String("1")}}, nil
	}
	h["ListResourcesForWebACL"] = func(any) (any, error) { return &wafv2.ListResourcesForWebACLOutput{}, nil }
}

// A denied or transient GetWebACL keeps the web ACL as its ListWebACLs summary,
// warning once per cause; an ACL GetWebACL no longer finds is not stored; any
// other error fails the scan after the other ACLs are stored and the scope's
// later phases have run.
func TestScanWAFv2_GetWebACLErrors(t *testing.T) {
	arn, withACLs := wafACLARN, wafWithACLs
	cases := []struct {
		name      string
		fail      map[string]error
		wantACLs  []string // a * suffix marks the summary kept
		wantWarns int
		wantCode  string
	}{
		{"denied", map[string]error{"a": apiErr("AccessDeniedException", "not authorized to perform: wafv2:GetWebACL")}, []string{"a*", "b"}, 1, ""},
		{"transient", map[string]error{"a": apiErr("ThrottlingException", "Rate exceeded")}, []string{"a*", "b"}, 1, ""},
		{"gone", map[string]error{"a": apiErr("WAFNonexistentItemException", "gone")}, []string{"b"}, 0, ""},
		{"other error", map[string]error{"a": apiErr("WAFInvalidParameterException", "bad")}, []string{"b"}, 0, "WAFInvalidParameterException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			withACLs(h, tc.fail)
			var later atomic.Int32
			// Counted for the failing ACLs' scope only: the CLOUDFRONT scope
			// runs its own phases whatever the regional one returned.
			h["ListRuleGroups"] = func(in any) (any, error) {
				if in.(*wafv2.ListRuleGroupsInput).Scope == types.ScopeRegional {
					later.Add(1)
				}
				return &wafv2.ListRuleGroupsOutput{}, nil
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "wafv2:GetWebACL")
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("wafv2:GetWebACL warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeWAFv2WebACL)
			var got []string
			for _, name := range []string{"a", "b"} {
				r, ok := rows[arn(name)]
				if !ok {
					continue
				}
				var summary types.WebACLSummary
				if err := json.Unmarshal([]byte(r.AttributesJSON), &summary); err != nil {
					t.Fatal(err)
				}
				if summary.LockToken != nil {
					name += "*"
				}
				got = append(got, name)
			}
			if !slices.Equal(got, tc.wantACLs) || len(rows) != len(tc.wantACLs) {
				t.Errorf("web ACLs = %v (%d rows), want %v", got, len(rows), tc.wantACLs)
			}
			if later.Load() == 0 {
				t.Error("ListRuleGroups never called: the scope's later phases must run")
			}
		})
	}
}

// A denied ListWebACLs page keeps the web ACLs of the pages before it, warns
// once, and the later phases still run.
func TestScanWAFv2_ListWebACLsDeniedContinues(t *testing.T) {
	aclARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/webacl/a/1"
	ipSetARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/ipset/ip/1"
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	h["ListWebACLs"] = func(in any) (any, error) {
		i := in.(*wafv2.ListWebACLsInput)
		switch {
		case i.Scope != types.ScopeRegional:
			return &wafv2.ListWebACLsOutput{}, nil
		case i.NextMarker == nil:
			return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{{ARN: sdkaws.String(aclARN), Name: sdkaws.String("a"), Id: sdkaws.String("1")}}, NextMarker: sdkaws.String("m2")}, nil
		default:
			return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListWebACLs")
		}
	}
	h["GetWebACL"] = func(any) (any, error) {
		return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{ARN: sdkaws.String(aclARN), Name: sdkaws.String("a"), Id: sdkaws.String("1")}}, nil
	}
	h["ListResourcesForWebACL"] = func(any) (any, error) { return &wafv2.ListResourcesForWebACLOutput{}, nil }
	h["ListIPSets"] = func(in any) (any, error) {
		if in.(*wafv2.ListIPSetsInput).Scope != types.ScopeRegional {
			return &wafv2.ListIPSetsOutput{}, nil
		}
		return &wafv2.ListIPSetsOutput{IPSets: []types.IPSetSummary{{ARN: sdkaws.String(ipSetARN), Name: sdkaws.String("ip")}}}, nil
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, "wafv2:ListWebACLs")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if *warns != 1 {
		t.Errorf("wafv2:ListWebACLs warnings = %d, want 1", *warns)
	}
	if _, ok := rowsByNativeID(t, st, TypeWAFv2WebACL)[aclARN]; !ok {
		t.Error("web ACL from the page before the denial not stored")
	}
	if _, ok := rowsByNativeID(t, st, TypeWAFv2IPSet)[ipSetARN]; !ok {
		t.Error("IP set from a later phase not stored")
	}
}

// A regex pattern set listed without an ARN is not stored and is warned about
// once.
func TestScanWAFv2_ARNlessRegexPatternSetWarns(t *testing.T) {
	regexARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/regexpatternset/rx/1"
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	h["ListRegexPatternSets"] = func(in any) (any, error) {
		if in.(*wafv2.ListRegexPatternSetsInput).Scope != types.ScopeRegional {
			return &wafv2.ListRegexPatternSetsOutput{}, nil
		}
		return &wafv2.ListRegexPatternSetsOutput{RegexPatternSets: []types.RegexPatternSetSummary{
			{ARN: sdkaws.String(regexARN), Name: sdkaws.String("rx")}, {Name: sdkaws.String("arnless")},
		}}, nil
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, "wafv2:ListRegexPatternSets")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if *warns != 1 {
		t.Errorf("wafv2:ListRegexPatternSets warnings = %d, want 1", *warns)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2RegexPatternSet)); !slices.Equal(got, []string{regexARN}) {
		t.Errorf("regex pattern sets = %v, want only %s", got, regexARN)
	}
}

// A denied ListRuleGroups or ListIPSets keeps the pages read before it, warns
// once, and the later phases still run.
func TestScanWAFv2_ListDeniedWarnsAndContinues(t *testing.T) {
	ruleGroupARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/rulegroup/rg/1"
	ipSetARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/ipset/ip/1"
	regexARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/regexpatternset/rx/1"
	denied := apiErr("AccessDeniedException", "not authorized to perform: wafv2:List")
	// pagedThenDenied answers the REGIONAL scope with page, then denies the
	// next page; the CLOUDFRONT scope gets empty.
	pagedThenDenied := func(page, empty any) func(in any) (any, error) {
		return func(in any) (any, error) {
			var (
				scope  types.Scope
				marker *string
			)
			switch i := in.(type) {
			case *wafv2.ListRuleGroupsInput:
				scope, marker = i.Scope, i.NextMarker
			case *wafv2.ListIPSetsInput:
				scope, marker = i.Scope, i.NextMarker
			}
			switch {
			case scope != types.ScopeRegional:
				return empty, nil
			case marker == nil:
				return page, nil
			default:
				return nil, denied
			}
		}
	}
	for _, c := range []struct {
		op, typ, keptARN string
		handler          func(in any) (any, error)
	}{
		{"ListRuleGroups", TypeWAFv2RuleGroup, ruleGroupARN, pagedThenDenied(
			&wafv2.ListRuleGroupsOutput{RuleGroups: []types.RuleGroupSummary{{ARN: sdkaws.String(ruleGroupARN), Name: sdkaws.String("rg")}}, NextMarker: sdkaws.String("m2")},
			&wafv2.ListRuleGroupsOutput{})},
		{"ListIPSets", TypeWAFv2IPSet, ipSetARN, pagedThenDenied(
			&wafv2.ListIPSetsOutput{IPSets: []types.IPSetSummary{{ARN: sdkaws.String(ipSetARN), Name: sdkaws.String("ip")}}, NextMarker: sdkaws.String("m2")},
			&wafv2.ListIPSetsOutput{})},
	} {
		t.Run(c.op, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			h[c.op] = c.handler
			h["ListRegexPatternSets"] = func(in any) (any, error) {
				if in.(*wafv2.ListRegexPatternSetsInput).Scope != types.ScopeRegional {
					return &wafv2.ListRegexPatternSetsOutput{}, nil
				}
				return &wafv2.ListRegexPatternSetsOutput{RegexPatternSets: []types.RegexPatternSetSummary{{ARN: sdkaws.String(regexARN), Name: sdkaws.String("rx")}}}, nil
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "wafv2:"+c.op)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if *warns != 1 {
				t.Errorf("wafv2:%s warnings = %d, want 1", c.op, *warns)
			}
			if _, ok := rowsByNativeID(t, st, c.typ)[c.keptARN]; !ok {
				t.Errorf("%s row from the page before the denial not stored", c.typ)
			}
			if _, ok := rowsByNativeID(t, st, TypeWAFv2RegexPatternSet)[regexARN]; !ok {
				t.Error("regex pattern set from a later phase not stored")
			}
		})
	}
}

// A web ACL kept as its summary is warned about only once it is stored: a
// failed write returns the store error and no "stored as" warning.
func TestScanWAFv2_KeptWebACLWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "wafv2:GetWebACL")
	aclARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/webacl/a/1"
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	h["ListWebACLs"] = func(in any) (any, error) {
		if in.(*wafv2.ListWebACLsInput).Scope != types.ScopeRegional {
			return &wafv2.ListWebACLsOutput{}, nil
		}
		return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{{ARN: sdkaws.String(aclARN), Name: sdkaws.String("a"), Id: sdkaws.String("1")}}}, nil
	}
	h["GetWebACL"] = func(any) (any, error) {
		closeStore()
		return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:GetWebACL")
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("wafv2:GetWebACL warnings = %d, want 0: the web ACL was never stored", *warns)
	}
}

// Associations are listed only for the web ACLs stored: not for one GetWebACL
// no longer finds. A ListResourcesForWebACL that no longer finds the ACL
// (WAFNonexistentItemException, in the op's model) skips that ACL's
// associations and the scan goes on to the next.
func TestScanWAFv2_AssociationsOnlyForStoredACLs(t *testing.T) {
	gone := apiErr("WAFNonexistentItemException", "gone")
	cases := []struct {
		name               string
		getFail, assocFail map[string]error
		wantListed         []string // ACLs ListResourcesForWebACL was called for
		wantAssociated     []string // ACLs whose association was stored
	}{
		{"acl gone on get", map[string]error{"a": gone}, nil, []string{"b"}, []string{"b"}},
		{"acl gone on associations", nil, map[string]error{"a": gone}, []string{"a", "b"}, []string{"b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			wafWithACLs(h, tc.getFail)
			var listed parentCalls
			h["ListResourcesForWebACL"] = func(in any) (any, error) {
				acl := sv(in.(*wafv2.ListResourcesForWebACLInput).WebACLArn)
				listed.add(acl)
				for name, err := range tc.assocFail {
					if acl == wafACLARN(name) {
						return nil, err
					}
				}
				return &wafv2.ListResourcesForWebACLOutput{ResourceArns: []string{acl + "-alb"}}, nil
			}
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			var wantListed, wantAssoc []string
			for _, name := range tc.wantListed {
				wantListed = append(wantListed, wafACLARN(name))
			}
			for _, name := range tc.wantAssociated {
				a := wafACLARN(name)
				wantAssoc = append(wantAssoc, a+"/association/"+a+"-alb")
			}
			if got := listed.distinct(); !slices.Equal(got, wantListed) {
				t.Errorf("ListResourcesForWebACL called for %v, want %v", got, wantListed)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2WebACLAssociation)); !slices.Equal(got, wantAssoc) {
				t.Errorf("associations = %v, want %v", got, wantAssoc)
			}
		})
	}
}

// A web ACL whose stored shape has no ARN, the summary kept for a denied
// GetWebACL or the GetWebACL body, is not stored under an empty NativeID: it is
// warned about once under the op that returned it, and its sibling is stored.
func TestScanWAFv2_ARNlessWebACLWarns(t *testing.T) {
	cases := []struct {
		name, op string
		summary  bool // the summary lacks the ARN, else the body does
	}{
		{"summary", "wafv2:ListWebACLs", true},
		{"body", "wafv2:GetWebACL", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			wafWithACLs(h, nil)
			list, get := h["ListWebACLs"], h["GetWebACL"]
			h["ListWebACLs"] = func(in any) (any, error) {
				out, err := list(in)
				if err != nil || !tc.summary {
					return out, err
				}
				acls := out.(*wafv2.ListWebACLsOutput).WebACLs
				if len(acls) > 0 {
					acls[0].ARN = nil
				}
				return out, nil
			}
			h["GetWebACL"] = func(in any) (any, error) {
				if sv(in.(*wafv2.GetWebACLInput).Name) != "a" {
					return get(in)
				}
				if tc.summary {
					return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:GetWebACL")
				}
				return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{Name: sdkaws.String("a"), Id: sdkaws.String("1")}}, nil
			}
			st := newTestStore(t)
			warns := map[string]int{}
			var mu sync.Mutex
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				warns[w.Service]++
				mu.Unlock()
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2WebACL)); !slices.Equal(got, []string{wafACLARN("b")}) {
				t.Errorf("web ACLs = %q, want only %s", got, wafACLARN("b"))
			}
			// No "stored as their summary" warning for the denied GetWebACL in
			// the summary case: that summary was not stored.
			want := map[string]int{"wafv2:ListWebACLs": 0, "wafv2:GetWebACL": 0}
			want[tc.op]++
			for op, n := range want {
				if warns[op] != n {
					t.Errorf("%s warnings = %d, want %d", op, warns[op], n)
				}
			}
		})
	}
}

// A failed rule group listing still runs the IP sets and later phases, and the
// scan returns the counts of every row stored, not 0, 0.
func TestScanWAFv2_ListRuleGroupsFailureKeepsCounts(t *testing.T) {
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	wafWithACLs(h, nil)
	h["ListRuleGroups"] = func(any) (any, error) { return nil, apiErr("WAFInvalidParameterException", "bad") }
	h["ListAPIKeys"] = func(any) (any, error) { return &wafv2.ListAPIKeysOutput{}, nil }
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	total, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "WAFInvalidParameterException")
	all, lerr := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: util.AllResources})
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(all) == 0 || total != len(all) {
		t.Errorf("total = %d, want %d, every row the scan stored", total, len(all))
	}
}

// A rule group or IP set listed without an ARN is not stored under an empty
// NativeID; it is warned about once under its list op, and its sibling is
// stored.
func TestScanWAFv2_ARNlessSummariesWarn(t *testing.T) {
	rgARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/rulegroup/rg/1"
	ipARN := "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/ipset/ip/1"
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	regional := func(in any) bool {
		switch i := in.(type) {
		case *wafv2.ListRuleGroupsInput:
			return i.Scope == types.ScopeRegional
		case *wafv2.ListIPSetsInput:
			return i.Scope == types.ScopeRegional
		}
		return false
	}
	h["ListRuleGroups"] = func(in any) (any, error) {
		if !regional(in) {
			return &wafv2.ListRuleGroupsOutput{}, nil
		}
		return &wafv2.ListRuleGroupsOutput{RuleGroups: []types.RuleGroupSummary{
			{ARN: sdkaws.String(rgARN), Name: sdkaws.String("rg")}, {Name: sdkaws.String("arnless")},
		}}, nil
	}
	h["ListIPSets"] = func(in any) (any, error) {
		if !regional(in) {
			return &wafv2.ListIPSetsOutput{}, nil
		}
		return &wafv2.ListIPSetsOutput{IPSets: []types.IPSetSummary{
			{ARN: sdkaws.String(ipARN), Name: sdkaws.String("ip")}, {Name: sdkaws.String("arnless")},
		}}, nil
	}
	st := newTestStore(t)
	warns := map[string]int{}
	var mu sync.Mutex
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns[w.Service]++
		mu.Unlock()
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2RuleGroup)); !slices.Equal(got, []string{rgARN}) {
		t.Errorf("rule groups = %q, want only %s", got, rgARN)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2IPSet)); !slices.Equal(got, []string{ipARN}) {
		t.Errorf("IP sets = %q, want only %s", got, ipARN)
	}
	if warns["wafv2:ListRuleGroups"] != 1 || warns["wafv2:ListIPSets"] != 1 {
		t.Errorf("warnings: ListRuleGroups = %d, ListIPSets = %d; want 1 each", warns["wafv2:ListRuleGroups"], warns["wafv2:ListIPSets"])
	}
}

// A GetWebACL that succeeds without a WebACL keeps the ACL as its summary,
// warning once, instead of panicking.
func TestScanWAFv2_GetWebACLWithoutBodyKeepsSummary(t *testing.T) {
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	wafWithACLs(h, nil)
	get := h["GetWebACL"]
	h["GetWebACL"] = func(in any) (any, error) {
		if sv(in.(*wafv2.GetWebACLInput).Name) == "a" {
			return &wafv2.GetWebACLOutput{}, nil
		}
		return get(in)
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, "wafv2:GetWebACL")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeWAFv2WebACL)
	if got := nativeIDs(rows); !slices.Equal(got, []string{wafACLARN("a"), wafACLARN("b")}) {
		t.Fatalf("web ACLs = %q, want a and b", got)
	}
	var summary types.WebACLSummary
	if err := json.Unmarshal([]byte(rows[wafACLARN("a")].AttributesJSON), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.LockToken == nil {
		t.Errorf("a attrs = %s, want its ListWebACLs summary", rows[wafACLARN("a")].AttributesJSON)
	}
	if *warns != 1 {
		t.Errorf("wafv2:GetWebACL warnings = %d, want 1", *warns)
	}
}

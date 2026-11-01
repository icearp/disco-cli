package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
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

// wafv2Handlers answers every older WAFv2 phase with an empty listing,
// ListTagsForResource with no tags, and ListAPIKeys with two REGIONAL pages
// (one element without a key) and one CLOUDFRONT page.
func wafv2Handlers(calls *wafAPIKeyCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": func(in any) (any, error) {
			return &wafv2.ListTagsForResourceOutput{TagInfoForResource: &types.TagInfoForResource{ResourceARN: in.(*wafv2.ListTagsForResourceInput).ResourceARN}}, nil
		},
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
		warns := countOpWarnings(st, "wafv2:ListManagedRuleSets")
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
		_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
		}
		// One per scope, each under the op that failed.
		if *warns != 2 {
			t.Errorf("wafv2:ListManagedRuleSets warnings = %d; want 2 demoted transients (one per scope)", *warns)
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

// A failed GetWebACL keeps the web ACL as its ListWebACLs summary, except for
// an ACL GetWebACL no longer finds, which is not stored: a denial warns once;
// a transient failure, or any other error, is returned after the other ACLs
// are stored and the scope's later phases have run.
func TestScanWAFv2_GetWebACLErrors(t *testing.T) {
	arn, withACLs := wafACLARN, wafWithACLs
	cases := []struct {
		name      string
		fail      map[string]error
		wantACLs  []string // a * suffix marks the summary kept
		wantWarns int
		wantErr   func(error) bool // nil: the scan succeeds
	}{
		{"denied", map[string]error{"a": apiErr("AccessDeniedException", "not authorized to perform: wafv2:GetWebACL")}, []string{"a*", "b"}, 1, nil},
		{
			"transient",
			map[string]error{"a": apiErr("ThrottlingException", "Rate exceeded")},
			[]string{"a*", "b"},
			0,
			func(err error) bool {
				return classifyServiceError(err) == outcomeTransient && isAPIErrorCode(err, "ThrottlingException")
			},
		},
		{"gone", map[string]error{"a": apiErr("WAFNonexistentItemException", "gone")}, []string{"b"}, 0, nil},
		{
			"other error",
			map[string]error{"a": apiErr("WAFInvalidParameterException", "bad")},
			[]string{"a*", "b"},
			0,
			func(err error) bool {
				return classifyServiceError(err) == outcomeError && isAPIErrorCode(err, "WAFInvalidParameterException")
			},
		},
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
			if tc.wantErr == nil && err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if tc.wantErr != nil && !tc.wantErr(err) {
				t.Errorf("scanWAFv2 err = %v; not the wanted error", err)
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
				return wafALBAssociation(in)
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
			// A gone ACL is not asked again for its other resource types.
			for name := range tc.assocFail {
				acl := wafACLARN(name)
				if n := len(slices.DeleteFunc(slices.Clone(listed.ids), func(id string) bool { return id != acl })); n != 1 {
					t.Errorf("ListResourcesForWebACL calls for gone ACL %s = %d, want 1", name, n)
				}
			}
		})
	}
}

// Each web ACL's associations are listed once for every resource type the op
// lists for a regional ACL, never with the type left empty (the op then lists
// ALBs only), and an association of a non-ALB type is stored.
func TestScanWAFv2_AssociationsListedPerResourceType(t *testing.T) {
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	wafWithACLs(h, nil)
	var (
		mu        sync.Mutex
		requested = map[string][]string{} // web ACL -> resource types requested
	)
	h["ListResourcesForWebACL"] = func(in any) (any, error) {
		i := in.(*wafv2.ListResourcesForWebACLInput)
		mu.Lock()
		requested[sv(i.WebACLArn)] = append(requested[sv(i.WebACLArn)], string(i.ResourceType))
		mu.Unlock()
		return &wafv2.ListResourcesForWebACLOutput{ResourceArns: []string{sv(i.WebACLArn) + "-" + string(i.ResourceType)}}, nil
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	want := []string{
		"AGENTCORE_GATEWAY", "API_GATEWAY", "APPLICATION_LOAD_BALANCER", "APPSYNC",
		"APP_RUNNER_SERVICE", "COGNITO_USER_POOL", "VERIFIED_ACCESS_INSTANCE",
	}
	for _, name := range []string{"a", "b"} {
		got := slices.Sorted(slices.Values(requested[wafACLARN(name)]))
		if !slices.Equal(got, want) {
			t.Errorf("resource types requested for ACL %s = %v, want %v", name, got, want)
		}
	}
	rows := rowsByNativeID(t, st, TypeWAFv2WebACLAssociation)
	if len(rows) != 14 {
		t.Errorf("associations stored = %d, want 14 (2 ACLs x 7 resource types)", len(rows))
	}
	acl := wafACLARN("a")
	apigw, ok := rows[acl+"/association/"+acl+"-API_GATEWAY"]
	if !ok {
		t.Fatalf("API_GATEWAY association of ACL a not stored; got %v", nativeIDs(rows))
	}
	var attrs map[string]string
	if err := json.Unmarshal([]byte(apigw.AttributesJSON), &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["WebACLArn"] != acl || attrs["ResourceArn"] != acl+"-API_GATEWAY" {
		t.Errorf("API_GATEWAY association attrs = %v", attrs)
	}
}

// Every ResourceType value of the pinned SDK is either listed for regional web
// ACLs or is AMPLIFY, which only a CLOUDFRONT-scope ACL protects. disco does
// not list CLOUDFRONT-scope associations, so AMPLIFY associations are an open
// gap, not handled. A value a later SDK adds fails here until someone reads
// its doc and places it.
func TestWAFv2RegionalResourceTypesCoverEnum(t *testing.T) {
	got := []string{"AMPLIFY"}
	for _, e := range wafv2RegionalResourceTypes {
		got = append(got, string(e.rt))
	}
	slices.Sort(got)
	var want []string
	for _, rt := range types.ResourceType("").Values() {
		want = append(want, string(rt))
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("regional resource types + AMPLIFY = %v; the SDK enum = %v", got, want)
	}
}

// A resource type whose owning service AWS does not offer in the region is
// not requested there; the other types still are.
func TestScanWAFv2_AssociationTypesOnlyWhereOwnerOffered(t *testing.T) {
	// A commercial-shaped region in no SDK endpoint table, so only the catalog
	// below offers services there.
	const region = "us-nowhere-9"
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	wafWithACLs(h, nil)
	var (
		mu        sync.Mutex
		requested = map[string]bool{}
	)
	h["ListResourcesForWebACL"] = func(in any) (any, error) {
		mu.Lock()
		requested[string(in.(*wafv2.ListResourcesForWebACLInput).ResourceType)] = true
		mu.Unlock()
		return &wafv2.ListResourcesForWebACLOutput{}, nil
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	acct.availByCode = map[string]map[string]bool{}
	for _, code := range []string{"elasticloadbalancingv2", "apigateway", "appsync", "cognito", "ec2", "bedrock-agentcore"} {
		acct.availByCode[code] = map[string]bool{region: true}
	}
	if _, _, err := scanWAFv2(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	got := slices.Sorted(maps.Keys(requested))
	want := []string{
		"AGENTCORE_GATEWAY", "API_GATEWAY", "APPLICATION_LOAD_BALANCER", "APPSYNC",
		"COGNITO_USER_POOL", "VERIFIED_ACCESS_INSTANCE",
	}
	if !slices.Equal(got, want) {
		t.Errorf("resource types requested = %v, want %v (no APP_RUNNER_SERVICE: aws:apprunner not offered)", got, want)
	}
}

// One or two resource types' listings failing for every ACL cost only those
// types' associations: a denial, a transient failure or WAF rejecting the
// resource type warns once per cause (a rejection once per type) and the scan
// succeeds; any other error, including WAF rejecting another field, is returned.
func TestScanWAFv2_AssociationTypeFailureKeepsOtherTypes(t *testing.T) {
	typeRejected := &types.WAFInvalidParameterException{
		Message: sdkaws.String("bad type"), Field: types.ParameterExceptionFieldResourceType,
	}
	apigw := []types.ResourceType{types.ResourceTypeApiGateway}
	cases := []struct {
		name      string
		err       error
		failTypes []types.ResourceType
		wantCode  string // the API error returned, or "" for none
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: apigateway:GET"), apigw, "", 1},
		{"transient", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}, apigw, "", 1},
		{"other error", apiErr("WAFInvalidParameterException", "bad"), apigw, "WAFInvalidParameterException", 0},
		{"resource type rejected", typeRejected, apigw, "", 1},
		{"two resource types rejected", typeRejected, []types.ResourceType{types.ResourceTypeApiGateway, types.ResourceTypeAppsync}, "", 2},
		{"other field rejected", &types.WAFInvalidParameterException{
			Message: sdkaws.String("bad arn"), Field: types.ParameterExceptionFieldResourceArn,
		}, apigw, "WAFInvalidParameterException", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			wafWithACLs(h, nil)
			h["ListResourcesForWebACL"] = func(in any) (any, error) {
				i := in.(*wafv2.ListResourcesForWebACLInput)
				if slices.Contains(tc.failTypes, i.ResourceType) {
					return nil, tc.err
				}
				return &wafv2.ListResourcesForWebACLOutput{ResourceArns: []string{sv(i.WebACLArn) + "-" + string(i.ResourceType)}}, nil
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "wafv2:ListResourcesForWebACL")
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("scanWAFv2: %v", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if *warns != tc.wantWarns {
				t.Errorf("ListResourcesForWebACL warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeWAFv2WebACLAssociation)
			if want := 2 * (7 - len(tc.failTypes)); len(rows) != want {
				t.Errorf("associations stored = %d, want %d (2 ACLs x the types that did not fail)", len(rows), want)
			}
			acl := wafACLARN("a")
			if _, ok := rows[acl+"/association/"+acl+"-COGNITO_USER_POOL"]; !ok {
				t.Errorf("COGNITO_USER_POOL association of ACL a not stored; got %v", nativeIDs(rows))
			}
		})
	}
}

// A web ACL listed without an ARN is not stored under an empty NativeID: it
// is warned about once under ListWebACLs, is not read with GetWebACL or
// ListTagsForResource, and its sibling is stored. A GetWebACL body without
// an ARN does not change the row's NativeID, which is always the summary's.
func TestScanWAFv2_ARNlessWebACLWarns(t *testing.T) {
	cases := []struct {
		name       string
		summary    bool // the summary lacks the ARN, else the body does
		wantGets   []string
		wantStored []string // also the ARNs whose tags are read
		wantWarns  int      // ListWebACLs warnings
	}{
		{"summary", true, []string{"b"}, []string{wafACLARN("b")}, 1},
		{"body", false, []string{"a", "b"}, []string{wafACLARN("a"), wafACLARN("b")}, 0},
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
			var gets, tagged parentCalls
			h["GetWebACL"] = func(in any) (any, error) {
				name := sv(in.(*wafv2.GetWebACLInput).Name)
				gets.add(name)
				if name != "a" {
					return get(in)
				}
				return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{Name: sdkaws.String("a"), Id: sdkaws.String("1")}}, nil
			}
			tags := h["ListTagsForResource"]
			h["ListTagsForResource"] = func(in any) (any, error) {
				tagged.add(sv(in.(*wafv2.ListTagsForResourceInput).ResourceARN))
				return tags(in)
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
			if stored := nativeIDs(rowsByNativeID(t, st, TypeWAFv2WebACL)); !slices.Equal(stored, tc.wantStored) {
				t.Errorf("web ACLs = %q, want %q", stored, tc.wantStored)
			}
			if names := gets.distinct(); !slices.Equal(names, tc.wantGets) {
				t.Errorf("GetWebACL names = %q, want %q", names, tc.wantGets)
			}
			if arns := tagged.distinct(); !slices.Equal(arns, tc.wantStored) {
				t.Errorf("ListTagsForResource ARNs = %q, want %q", arns, tc.wantStored)
			}
			want := map[string]int{"wafv2:ListWebACLs": tc.wantWarns, "wafv2:GetWebACL": 0}
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
// instead of panicking, and the scan returns errWAFv2NoWebACLBody.
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
	if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); !errors.Is(err, errWAFv2NoWebACLBody) {
		t.Fatalf("scanWAFv2 err = %v; want errWAFv2NoWebACLBody", err)
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
	if *warns != 0 {
		t.Errorf("wafv2:GetWebACL warnings = %d, want 0: the error is returned", *warns)
	}
}

// wafv2RegionalTestARN is the ARN of the regional test item of kind named name.
func wafv2RegionalTestARN(kind, name string) string {
	return "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":regional/" + kind + "/" + name + "/1"
}

// wafExtendedLists answers the regional logging-configuration, regex and
// managed-rule-set listings with one item each.
func wafExtendedLists(h map[string]func(in any) (any, error)) {
	h["ListLoggingConfigurations"] = func(in any) (any, error) {
		if in.(*wafv2.ListLoggingConfigurationsInput).Scope != types.ScopeRegional {
			return &wafv2.ListLoggingConfigurationsOutput{}, nil
		}
		return &wafv2.ListLoggingConfigurationsOutput{LoggingConfigurations: []types.LoggingConfiguration{{ResourceArn: sdkaws.String(wafACLARN("a"))}}}, nil
	}
	h["ListRegexPatternSets"] = func(in any) (any, error) {
		if in.(*wafv2.ListRegexPatternSetsInput).Scope != types.ScopeRegional {
			return &wafv2.ListRegexPatternSetsOutput{}, nil
		}
		return &wafv2.ListRegexPatternSetsOutput{RegexPatternSets: []types.RegexPatternSetSummary{{ARN: sdkaws.String(wafv2RegionalTestARN("regexpatternset", "rx")), Name: sdkaws.String("rx")}}}, nil
	}
	h["ListManagedRuleSets"] = func(in any) (any, error) {
		if in.(*wafv2.ListManagedRuleSetsInput).Scope != types.ScopeRegional {
			return &wafv2.ListManagedRuleSetsOutput{}, nil
		}
		return &wafv2.ListManagedRuleSetsOutput{ManagedRuleSets: []types.ManagedRuleSetSummary{{ARN: sdkaws.String(wafv2RegionalTestARN("managedruleset", "m")), Name: sdkaws.String("m")}}}, nil
	}
	h["ListResourcesForWebACL"] = wafALBAssociation
}

// wafALBAssociation answers ListResourcesForWebACL with one load balancer,
// named after the ACL, for the APPLICATION_LOAD_BALANCER listing and nothing
// for every other resource type.
func wafALBAssociation(in any) (any, error) {
	i := in.(*wafv2.ListResourcesForWebACLInput)
	if i.ResourceType != types.ResourceTypeApplicationLoadBalancer {
		return &wafv2.ListResourcesForWebACLOutput{}, nil
	}
	return &wafv2.ListResourcesForWebACLOutput{ResourceArns: []string{sv(i.WebACLArn) + "-alb"}}, nil
}

// wafExtendedRows is the number of rows stored per extended type.
func wafExtendedRows(t *testing.T, st *store.Store) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, typ := range []string{TypeWAFv2LoggingConfiguration, TypeWAFv2RegexPatternSet, TypeWAFv2ManagedRuleSet, TypeWAFv2WebACLAssociation} {
		got[typ] = len(rowsByNativeID(t, st, typ))
	}
	return got
}

// A failing extended phase, or one web ACL's failing association listing,
// stops none of the others: their rows are stored and the hard error is
// returned.
func TestScanWAFv2_ExtendedPhasesIndependent(t *testing.T) {
	bad := apiErr("WAFInvalidParameterException", "bad")
	cases := []struct {
		name string
		fail string // the op that fails: every call, or the first web ACL's associations
		want map[string]int
	}{
		{"logging configurations", "ListLoggingConfigurations", map[string]int{
			TypeWAFv2LoggingConfiguration: 0, TypeWAFv2RegexPatternSet: 1, TypeWAFv2ManagedRuleSet: 1, TypeWAFv2WebACLAssociation: 2,
		}},
		{"regex pattern sets", "ListRegexPatternSets", map[string]int{
			TypeWAFv2LoggingConfiguration: 1, TypeWAFv2RegexPatternSet: 0, TypeWAFv2ManagedRuleSet: 1, TypeWAFv2WebACLAssociation: 2,
		}},
		{"associations of one ACL", "ListResourcesForWebACL", map[string]int{
			TypeWAFv2LoggingConfiguration: 1, TypeWAFv2RegexPatternSet: 1, TypeWAFv2ManagedRuleSet: 1, TypeWAFv2WebACLAssociation: 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			wafWithACLs(h, nil)
			wafExtendedLists(h)
			ok := h[tc.fail]
			// The ACLs' order is not fixed; failing the first one listed is
			// what proves the second is still listed.
			var listed atomic.Bool
			h[tc.fail] = func(in any) (any, error) {
				if _, isAssoc := in.(*wafv2.ListResourcesForWebACLInput); isAssoc && listed.Swap(true) {
					return ok(in)
				}
				return nil, bad
			}
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "WAFInvalidParameterException")
			if got := wafExtendedRows(t, st); !maps.Equal(got, tc.want) {
				t.Errorf("rows per type = %v, want %v", got, tc.want)
			}
		})
	}
}

// wafPaged answers the first call of the listing inScope picks with first; a
// later call, counted in later, fails with err or, when err is nil, gets
// repeat, a page that repeats the marker it was sent. From the third later
// call on it gets empty, so a listing that does not stop ends fast instead of
// looping. The other scope's listing gets empty.
func wafPaged[In any](inScope func(In) bool, sent func(In) *string, first, repeat, empty any, err error, later *atomic.Int32) func(any) (any, error) {
	return func(in any) (any, error) {
		i := in.(In)
		switch {
		case !inScope(i):
			return empty, nil
		case sent(i) == nil:
			return first, nil
		case later.Add(1) > 2:
			return empty, nil
		case err != nil:
			return nil, err
		default:
			return repeat, nil
		}
	}
}

// A listing of either scope whose second page repeats the marker it was
// sent, or fails, keeps the first page's item, and the repeating page's item,
// and returns the error, naming the scope that failed.
func TestScanWAFv2_LaterPageFailureKeepsEarlierPages(t *testing.T) {
	m1 := sdkaws.String("m1")
	cases := []struct {
		op      string
		handler func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error)
		typ     string
		// first is the first page's item, repeated the item of the page that
		// repeats its marker.
		first, repeated string
	}{
		{"ListWebACLs", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			acl := func(name string) types.WebACLSummary {
				return types.WebACLSummary{ARN: sdkaws.String(wafACLARN(name)), Name: sdkaws.String(name), Id: sdkaws.String("1")}
			}
			return wafPaged(func(i *wafv2.ListWebACLsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListWebACLsInput) *string { return i.NextMarker },
				&wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{acl("a")}, NextMarker: m1},
				&wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{acl("b")}, NextMarker: m1}, &wafv2.ListWebACLsOutput{}, err, later)
		}, TypeWAFv2WebACL, wafACLARN("a"), wafACLARN("b")},
		{"ListRuleGroups", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			rg := func(name string) types.RuleGroupSummary {
				return types.RuleGroupSummary{ARN: sdkaws.String(wafv2RegionalTestARN("rulegroup", name)), Name: sdkaws.String(name)}
			}
			return wafPaged(func(i *wafv2.ListRuleGroupsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListRuleGroupsInput) *string { return i.NextMarker },
				&wafv2.ListRuleGroupsOutput{RuleGroups: []types.RuleGroupSummary{rg("rg")}, NextMarker: m1},
				&wafv2.ListRuleGroupsOutput{RuleGroups: []types.RuleGroupSummary{rg("rg2")}, NextMarker: m1}, &wafv2.ListRuleGroupsOutput{}, err, later)
		}, TypeWAFv2RuleGroup, wafv2RegionalTestARN("rulegroup", "rg"), wafv2RegionalTestARN("rulegroup", "rg2")},
		{"ListIPSets", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			ip := func(name string) types.IPSetSummary {
				return types.IPSetSummary{ARN: sdkaws.String(wafv2RegionalTestARN("ipset", name)), Name: sdkaws.String(name)}
			}
			return wafPaged(func(i *wafv2.ListIPSetsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListIPSetsInput) *string { return i.NextMarker },
				&wafv2.ListIPSetsOutput{IPSets: []types.IPSetSummary{ip("ip")}, NextMarker: m1},
				&wafv2.ListIPSetsOutput{IPSets: []types.IPSetSummary{ip("ip2")}, NextMarker: m1}, &wafv2.ListIPSetsOutput{}, err, later)
		}, TypeWAFv2IPSet, wafv2RegionalTestARN("ipset", "ip"), wafv2RegionalTestARN("ipset", "ip2")},
		{"ListLoggingConfigurations", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			lc := func(name string) types.LoggingConfiguration {
				return types.LoggingConfiguration{ResourceArn: sdkaws.String(wafACLARN(name))}
			}
			return wafPaged(func(i *wafv2.ListLoggingConfigurationsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListLoggingConfigurationsInput) *string { return i.NextMarker },
				&wafv2.ListLoggingConfigurationsOutput{LoggingConfigurations: []types.LoggingConfiguration{lc("a")}, NextMarker: m1},
				&wafv2.ListLoggingConfigurationsOutput{LoggingConfigurations: []types.LoggingConfiguration{lc("b")}, NextMarker: m1}, &wafv2.ListLoggingConfigurationsOutput{}, err, later)
		}, TypeWAFv2LoggingConfiguration, wafACLARN("a") + "/logging-configuration", wafACLARN("b") + "/logging-configuration"},
		{"ListRegexPatternSets", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			rx := func(name string) types.RegexPatternSetSummary {
				return types.RegexPatternSetSummary{ARN: sdkaws.String(wafv2RegionalTestARN("regexpatternset", name)), Name: sdkaws.String(name)}
			}
			return wafPaged(func(i *wafv2.ListRegexPatternSetsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListRegexPatternSetsInput) *string { return i.NextMarker },
				&wafv2.ListRegexPatternSetsOutput{RegexPatternSets: []types.RegexPatternSetSummary{rx("rx")}, NextMarker: m1},
				&wafv2.ListRegexPatternSetsOutput{RegexPatternSets: []types.RegexPatternSetSummary{rx("rx2")}, NextMarker: m1}, &wafv2.ListRegexPatternSetsOutput{}, err, later)
		}, TypeWAFv2RegexPatternSet, wafv2RegionalTestARN("regexpatternset", "rx"), wafv2RegionalTestARN("regexpatternset", "rx2")},
		{"ListManagedRuleSets", func(scope types.Scope, err error, later *atomic.Int32) func(any) (any, error) {
			mrs := func(name string) types.ManagedRuleSetSummary {
				return types.ManagedRuleSetSummary{ARN: sdkaws.String(wafv2RegionalTestARN("managedruleset", name)), Name: sdkaws.String(name)}
			}
			return wafPaged(func(i *wafv2.ListManagedRuleSetsInput) bool { return i.Scope == scope },
				func(i *wafv2.ListManagedRuleSetsInput) *string { return i.NextMarker },
				&wafv2.ListManagedRuleSetsOutput{ManagedRuleSets: []types.ManagedRuleSetSummary{mrs("m")}, NextMarker: m1},
				&wafv2.ListManagedRuleSetsOutput{ManagedRuleSets: []types.ManagedRuleSetSummary{mrs("m2")}, NextMarker: m1}, &wafv2.ListManagedRuleSetsOutput{}, err, later)
		}, TypeWAFv2ManagedRuleSet, wafv2RegionalTestARN("managedruleset", "m"), wafv2RegionalTestARN("managedruleset", "m2")},
	}
	for _, scope := range []types.Scope{types.ScopeRegional, types.ScopeCloudfront} {
		for _, tc := range cases {
			for _, fail := range []struct {
				name    string
				err     error
				wantErr func(error) bool
			}{
				{"repeated marker", nil, func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
				{"error", apiErr("WAFInvalidParameterException", "bad"), func(err error) bool { return isAPIErrorCode(err, "WAFInvalidParameterException") }},
			} {
				t.Run(string(scope)+"/"+tc.op+"/"+fail.name, func(t *testing.T) {
					var calls wafAPIKeyCalls
					h := wafv2Handlers(&calls)
					h["GetWebACL"] = func(in any) (any, error) {
						name := sv(in.(*wafv2.GetWebACLInput).Name)
						return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{ARN: sdkaws.String(wafACLARN(name)), Name: &name, Id: sdkaws.String("1")}}, nil
					}
					h["ListResourcesForWebACL"] = func(any) (any, error) { return &wafv2.ListResourcesForWebACLOutput{}, nil }
					var later atomic.Int32
					h[tc.op] = tc.handler(scope, fail.err, &later)
					st := newTestStore(t)
					acct := newTestAccount(testAccountID)
					acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
					_, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
					if !fail.wantErr(err) {
						t.Errorf("err = %v, want the second page's failure", err)
					}
					// One member: a repeated marker is reported once, scoped,
					// not again by the pager's own unscoped guard.
					if n := len(bacTopMembers(err)); n != 1 {
						t.Errorf("error members = %d, want 1: %v", n, err)
					}
					var inScope wafv2ScopeError
					if !errors.As(err, &inScope) || inScope.scope != scope {
						t.Errorf("err = %v, want it to name scope %s", err, scope)
					}
					if n := later.Load(); n != 1 {
						t.Errorf("%s %s calls after the first page = %d, want 1", scope, tc.op, n)
					}
					want := []string{tc.first}
					if fail.err == nil {
						want = append(want, tc.repeated)
					}
					if got := nativeIDs(rowsByNativeID(t, st, tc.typ)); !slices.Equal(got, want) {
						t.Errorf("%s rows = %v, want %v", tc.typ, got, want)
					}
				})
			}
		}
	}
}

// A logging configuration or managed rule set listed without the ARN its row
// is keyed by is not stored and is warned about once under its op.
func TestScanWAFv2_ARNlessExtendedItemsWarn(t *testing.T) {
	var calls wafAPIKeyCalls
	h := wafv2Handlers(&calls)
	h["ListLoggingConfigurations"] = func(in any) (any, error) {
		if in.(*wafv2.ListLoggingConfigurationsInput).Scope != types.ScopeRegional {
			return &wafv2.ListLoggingConfigurationsOutput{}, nil
		}
		return &wafv2.ListLoggingConfigurationsOutput{LoggingConfigurations: []types.LoggingConfiguration{{ResourceArn: sdkaws.String(wafACLARN("a"))}, {}}}, nil
	}
	h["ListManagedRuleSets"] = func(in any) (any, error) {
		if in.(*wafv2.ListManagedRuleSetsInput).Scope != types.ScopeRegional {
			return &wafv2.ListManagedRuleSetsOutput{}, nil
		}
		return &wafv2.ListManagedRuleSetsOutput{ManagedRuleSets: []types.ManagedRuleSetSummary{{Name: sdkaws.String("no-arn")}}}, nil
	}
	st := newTestStore(t)
	var mu sync.Mutex
	warns := map[string]int{}
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
	if warns["wafv2:ListLoggingConfigurations"] != 1 || warns["wafv2:ListManagedRuleSets"] != 1 {
		t.Errorf("warnings: ListLoggingConfigurations = %d, ListManagedRuleSets = %d; want 1 each",
			warns["wafv2:ListLoggingConfigurations"], warns["wafv2:ListManagedRuleSets"])
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2LoggingConfiguration)); !slices.Equal(got, []string{wafACLARN("a") + "/logging-configuration"}) {
		t.Errorf("logging configurations = %q, want only a's", got)
	}
}

// Every web ACL's association listing denied, or still failing transiently,
// skips those ACLs with one warning per cause, and the scan succeeds; an
// associated resource with an empty ARN is not stored and warns once.
func TestScanWAFv2_AssociationSkipsWarnOncePerCause(t *testing.T) {
	failing := func(err error) func(any) (any, error) {
		return func(any) (any, error) { return nil, err }
	}
	cases := []struct {
		name     string
		handler  func(any) (any, error)
		wantRows int
	}{
		{"denied", failing(apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListResourcesForWebACL")), 0},
		{"transient", failing(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}), 0},
		{"empty resource ARN", func(in any) (any, error) {
			out, err := wafALBAssociation(in)
			if alb := out.(*wafv2.ListResourcesForWebACLOutput).ResourceArns; len(alb) > 0 {
				out = &wafv2.ListResourcesForWebACLOutput{ResourceArns: []string{"", alb[0]}}
			}
			return out, err
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls wafAPIKeyCalls
			h := wafv2Handlers(&calls)
			wafWithACLs(h, nil)
			wafExtendedLists(h)
			h["ListResourcesForWebACL"] = tc.handler
			st := newTestStore(t)
			warns := countOpWarnings(st, "wafv2:ListResourcesForWebACL")
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
			if _, _, err := scanWAFv2(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if *warns != 1 {
				t.Errorf("ListResourcesForWebACL warnings = %d, want 1", *warns)
			}
			if n := len(rowsByNativeID(t, st, TypeWAFv2WebACLAssociation)); n != tc.wantRows {
				t.Errorf("associations stored = %d, want %d", n, tc.wantRows)
			}
		})
	}
}

// The CLOUDFRONT scope is listed only from CloudFront's home in the account's
// partition (us-east-1 for aws); a region elsewhere, or a partition the SDK
// gives CloudFront no home in, makes no CLOUDFRONT call.
func TestScanWAFv2_CloudFrontScopeOnlyInCloudFrontHome(t *testing.T) {
	cases := []struct {
		partition, region string
		wantCloudFront    bool
	}{
		{"aws", "us-east-1", true},
		{"aws", "us-west-2", false},
		{"aws-us-gov", "us-gov-west-1", false},
		{"aws-iso", "us-iso-east-1", false},
		{"aws-iso-b", "us-isob-east-1", false},
		// The SDK gives CloudFront a cn home (cn-northwest-1); unverified live.
		{"aws-cn", "cn-northwest-1", true},
		{"aws-cn", "cn-north-1", false},
	}
	for _, c := range cases {
		t.Run(c.partition+"/"+c.region, func(t *testing.T) {
			var (
				mu     sync.Mutex
				scopes = map[types.Scope]bool{}
			)
			record := func(scope types.Scope) {
				mu.Lock()
				scopes[scope] = true
				mu.Unlock()
			}
			acct := testAccountIn(c.partition)
			acct.cfg = opStubConfig(map[string]func(any) (any, error){
				"ListWebACLs": func(in any) (any, error) {
					record(in.(*wafv2.ListWebACLsInput).Scope)
					return &wafv2.ListWebACLsOutput{}, nil
				},
				"ListAPIKeys": func(in any) (any, error) {
					record(in.(*wafv2.ListAPIKeysInput).Scope)
					return &wafv2.ListAPIKeysOutput{}, nil
				},
			}, apiErr("AccessDeniedException", "denied"))
			if _, _, err := scanWAFv2(context.Background(), acct, c.region, newTestStore(t), testScanID); err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if !scopes[types.ScopeRegional] {
				t.Errorf("no REGIONAL call; scopes called %v", scopes)
			}
			if scopes[types.ScopeCloudfront] != c.wantCloudFront {
				t.Errorf("CLOUDFRONT called = %v; want %v", scopes[types.ScopeCloudfront], c.wantCloudFront)
			}
		})
	}
}

// wafTagged is one listed item of the tag tests: its type, its ARN's kind
// and scope segments, name and id. A name ending in "-bare" has no tags.
type wafTagged struct {
	typ, kind, seg, name, id string
}

func (w wafTagged) scope() types.Scope {
	if w.seg == "global" {
		return types.ScopeCloudfront
	}
	return types.ScopeRegional
}

func (w wafTagged) arn() string {
	return "arn:aws:wafv2:" + testRegion + ":" + testAccountID + ":" + w.seg + "/" + w.kind + "/" + w.name + "/" + w.id
}

// wafTaggedItems are, per taggable type, one tagged and one untagged
// REGIONAL item, and one CLOUDFRONT web ACL.
var wafTaggedItems = []wafTagged{
	{TypeWAFv2WebACL, "webacl", "regional", "a", "11111111-0000-0000-0000-000000000001"},
	{TypeWAFv2WebACL, "webacl", "regional", "a-bare", "11111111-0000-0000-0000-000000000002"},
	{TypeWAFv2WebACL, "webacl", "global", "cf", "11111111-0000-0000-0000-000000000003"},
	{TypeWAFv2RuleGroup, "rulegroup", "regional", "rg", "1"},
	{TypeWAFv2RuleGroup, "rulegroup", "regional", "rg-bare", "2"},
	{TypeWAFv2IPSet, "ipset", "regional", "ip", "1"},
	{TypeWAFv2IPSet, "ipset", "regional", "ip-bare", "2"},
	{TypeWAFv2RegexPatternSet, "regexpatternset", "regional", "rx", "1"},
	{TypeWAFv2RegexPatternSet, "regexpatternset", "regional", "rx-bare", "2"},
}

// wafTaggedTotal is every row a scan of wafTagHandlers stores: the tagged
// items, one logging configuration, one managed rule set, the ALB
// association of each regional web ACL and three API keys.
const wafTaggedTotal = 9 + 1 + 1 + 2 + 3

// wafTagWant is the tags wafTagHandlers' default answer gives item.
func wafTagWant(w wafTagged) map[string]string {
	if strings.HasSuffix(w.name, "-bare") {
		return nil
	}
	return map[string]string{"team": w.name, "env": "prod"}
}

// wafTagCalls records the inputs the tag tests check.
type wafTagCalls struct {
	mu     sync.Mutex
	tags   []wafv2.ListTagsForResourceInput
	gets   []wafv2.GetWebACLInput
	limits map[string][]int32 // list op -> Limit sent on each call
}

func (c *wafTagCalls) limit(op string, l *int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limits == nil {
		c.limits = map[string][]int32{}
	}
	c.limits[op] = append(c.limits[op], sdkaws.ToInt32(l))
}

func (c *wafTagCalls) tagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var arns []string
	for _, in := range c.tags {
		arns = append(arns, sv(in.ResourceARN))
	}
	slices.Sort(arns)
	return arns
}

// wafTagHandlers lists items of each type in their scope, with the extended
// listings of wafExtendedLists and the API keys of wafv2Handlers, and answers
// ListTagsForResource with tags (nil: wafTagWant). Every input is recorded
// in rec.
func wafTagHandlers(items []wafTagged, rec *wafTagCalls, tags func(in *wafv2.ListTagsForResourceInput) (any, error)) map[string]func(in any) (any, error) {
	var keys wafAPIKeyCalls
	h := wafv2Handlers(&keys)
	wafExtendedLists(h)
	of := func(typ string, scope types.Scope) []wafTagged {
		return slices.DeleteFunc(slices.Clone(items), func(w wafTagged) bool { return w.typ != typ || w.scope() != scope })
	}
	byName, byARN := map[string]wafTagged{}, map[string]wafTagged{}
	for _, w := range items {
		byName[w.typ+"/"+w.name] = w
		byARN[w.arn()] = w
	}
	h["ListWebACLs"] = func(in any) (any, error) {
		i := in.(*wafv2.ListWebACLsInput)
		rec.limit("ListWebACLs", i.Limit)
		out := &wafv2.ListWebACLsOutput{}
		for _, w := range of(TypeWAFv2WebACL, i.Scope) {
			out.WebACLs = append(out.WebACLs, types.WebACLSummary{ARN: sdkaws.String(w.arn()), Name: sdkaws.String(w.name), Id: sdkaws.String(w.id), LockToken: sdkaws.String("lock-" + w.name)})
		}
		return out, nil
	}
	h["GetWebACL"] = func(in any) (any, error) {
		i := in.(*wafv2.GetWebACLInput)
		rec.mu.Lock()
		rec.gets = append(rec.gets, *i)
		rec.mu.Unlock()
		w := byName[TypeWAFv2WebACL+"/"+sv(i.Name)]
		return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{ARN: sdkaws.String(w.arn()), Name: i.Name, Id: i.Id, Description: sdkaws.String("body of " + w.name)}}, nil
	}
	h["ListRuleGroups"] = func(in any) (any, error) {
		i := in.(*wafv2.ListRuleGroupsInput)
		rec.limit("ListRuleGroups", i.Limit)
		out := &wafv2.ListRuleGroupsOutput{}
		for _, w := range of(TypeWAFv2RuleGroup, i.Scope) {
			out.RuleGroups = append(out.RuleGroups, types.RuleGroupSummary{ARN: sdkaws.String(w.arn()), Name: sdkaws.String(w.name), Id: sdkaws.String(w.id), LockToken: sdkaws.String("lock-" + w.name)})
		}
		return out, nil
	}
	h["ListIPSets"] = func(in any) (any, error) {
		i := in.(*wafv2.ListIPSetsInput)
		rec.limit("ListIPSets", i.Limit)
		out := &wafv2.ListIPSetsOutput{}
		for _, w := range of(TypeWAFv2IPSet, i.Scope) {
			out.IPSets = append(out.IPSets, types.IPSetSummary{ARN: sdkaws.String(w.arn()), Name: sdkaws.String(w.name), Id: sdkaws.String(w.id), LockToken: sdkaws.String("lock-" + w.name)})
		}
		return out, nil
	}
	h["ListRegexPatternSets"] = func(in any) (any, error) {
		i := in.(*wafv2.ListRegexPatternSetsInput)
		rec.limit("ListRegexPatternSets", i.Limit)
		out := &wafv2.ListRegexPatternSetsOutput{}
		for _, w := range of(TypeWAFv2RegexPatternSet, i.Scope) {
			out.RegexPatternSets = append(out.RegexPatternSets, types.RegexPatternSetSummary{ARN: sdkaws.String(w.arn()), Name: sdkaws.String(w.name), Id: sdkaws.String(w.id), LockToken: sdkaws.String("lock-" + w.name)})
		}
		return out, nil
	}
	h["ListTagsForResource"] = func(in any) (any, error) {
		i := in.(*wafv2.ListTagsForResourceInput)
		rec.mu.Lock()
		rec.tags = append(rec.tags, *i)
		rec.mu.Unlock()
		if tags != nil {
			return tags(i)
		}
		w, ok := byARN[sv(i.ResourceARN)]
		if !ok {
			return nil, apiErr("WAFInvalidParameterException", "not a listed item")
		}
		return wafTagsOut(i, wafTagWant(w)), nil
	}
	return h
}

// wafTagsOut is a ListTagsForResource answer for in with tags, sorted by key.
func wafTagsOut(in *wafv2.ListTagsForResourceInput, tags map[string]string) *wafv2.ListTagsForResourceOutput {
	info := &types.TagInfoForResource{ResourceARN: in.ResourceARN}
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		info.TagList = append(info.TagList, types.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
	}
	return &wafv2.ListTagsForResourceOutput{TagInfoForResource: info}
}

// wafTagScan runs scanWAFv2 in testRegion over h and returns its result and
// the warnings it sent.
func wafTagScan(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (warns func() []store.ScanWarning, total, inserted int, err error) {
	t.Helper()
	var (
		mu   sync.Mutex
		sent []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		sent = append(sent, w)
		mu.Unlock()
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	total, inserted, err = scanWAFv2(context.Background(), acct, testRegion, st, testScanID)
	return func() []store.ScanWarning {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sent)
	}, total, inserted, err
}

// wafWarnings is the warnings sent under op.
func wafWarnings(ws []store.ScanWarning, op string) []store.ScanWarning {
	return slices.DeleteFunc(ws, func(w store.ScanWarning) bool { return w.Service != op })
}

// Every taggable type, in both scopes, is stored with the tags
// ListTagsForResource returns for the ARN its listing returned, and with
// none when it has none; no other type is sent to the tag op. Each listing
// and tag read sends Limit 100, the model's cap, and GetWebACL is sent the
// summary's Id, Name and scope. A rescan inserts nothing.
func TestScanWAFv2_TagsStoredPerType(t *testing.T) {
	var rec wafTagCalls
	h := wafTagHandlers(wafTaggedItems, &rec, nil)
	st := newTestStore(t)
	warns, total, inserted, err := wafTagScan(t, st, h)
	if err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if total != wafTaggedTotal || inserted != wafTaggedTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, wafTaggedTotal, wafTaggedTotal)
	}
	// The one warning is wafv2Handlers' keyless API key.
	if w := warns(); len(w) != 1 || w[0].Service != wafv2ListAPIKeysOp {
		t.Errorf("warnings = %+v; want only the keyless API key's", w)
	}
	var wantARNs []string
	for _, w := range wafTaggedItems {
		wantARNs = append(wantARNs, w.arn())
		r, ok := rowsByNativeID(t, st, w.typ)[w.arn()]
		if !ok {
			t.Errorf("%s not stored", w.arn())
			continue
		}
		if want := wafTagWant(w); want == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", w.arn(), *r.TagsJSON)
			}
		} else {
			assertTagsMap(t, r.TagsJSON, want)
		}
		if sv(r.Name) != w.name || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, nil, nil", w.arn(), sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, w.name, testRegion)
		}
		if acct := newTestAccount(testAccountID); r.Provider != "aws" || r.AccountID != acct.ID || sv(r.AccountName) != acct.Name {
			t.Errorf("%s Provider, AccountID, AccountName = %q, %q, %q; want aws, %q, %q", w.arn(), r.Provider, r.AccountID, sv(r.AccountName), acct.ID, acct.Name)
		}
		var attrs struct {
			ID                     *string `json:"Id"`
			LockToken, Description *string
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatal(err)
		}
		// A web ACL's attributes are its GetWebACL body, the others' their summary.
		want := struct{ id, lock, desc string }{w.id, "lock-" + w.name, ""}
		if w.typ == TypeWAFv2WebACL {
			want.lock, want.desc = "", "body of "+w.name
		}
		if sv(attrs.ID) != want.id || sv(attrs.LockToken) != want.lock || sv(attrs.Description) != want.desc {
			t.Errorf("%s attrs = %s; want Id %q, LockToken %q, Description %q", w.arn(), r.AttributesJSON, want.id, want.lock, want.desc)
		}
	}
	slices.Sort(wantARNs)
	if got := rec.tagARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %q; want each of %q once", got, wantARNs)
	}
	for _, in := range rec.tags {
		if sdkaws.ToInt32(in.Limit) != 100 || in.NextMarker != nil {
			t.Errorf("ListTagsForResource %s Limit, NextMarker = %v, %v; want 100, nil", sv(in.ResourceARN), sdkaws.ToInt32(in.Limit), in.NextMarker)
		}
	}
	for _, op := range []string{"ListWebACLs", "ListRuleGroups", "ListIPSets", "ListRegexPatternSets"} {
		if got := rec.limits[op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s Limits = %v; want [100 100] (one call per scope)", op, got)
		}
	}
	var gets []string
	for _, in := range rec.gets {
		gets = append(gets, string(in.Scope)+" "+sv(in.Name)+" "+sv(in.Id))
	}
	slices.Sort(gets)
	wantGets := []string{
		"CLOUDFRONT cf 11111111-0000-0000-0000-000000000003",
		"REGIONAL a 11111111-0000-0000-0000-000000000001",
		"REGIONAL a-bare 11111111-0000-0000-0000-000000000002",
	}
	if !slices.Equal(gets, wantGets) {
		t.Errorf("GetWebACL inputs = %q; want %q", gets, wantGets)
	}

	_, total, inserted, err = wafTagScan(t, st, h)
	if err != nil || total != wafTaggedTotal || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want %d, 0, nil", total, inserted, err, wafTaggedTotal)
	}
}

// Web ACLs listed on a later ListWebACLs page are read with GetWebACL by
// their own Id, Name and scope, stored as their bodies, and have their
// associations listed and stored, like the first page's.
func TestScanWAFv2_WebACLsAcrossPages(t *testing.T) {
	var keys wafAPIKeyCalls
	h := wafv2Handlers(&keys)
	h["ListWebACLs"] = func(in any) (any, error) {
		i := in.(*wafv2.ListWebACLsInput)
		acl := func(name string) types.WebACLSummary {
			return types.WebACLSummary{ARN: sdkaws.String(wafACLARN(name)), Name: sdkaws.String(name), Id: sdkaws.String("id-" + name)}
		}
		switch {
		case i.Scope != types.ScopeRegional:
			return &wafv2.ListWebACLsOutput{}, nil
		case i.NextMarker == nil:
			return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{acl("a")}, NextMarker: sdkaws.String("m1")}, nil
		default:
			return &wafv2.ListWebACLsOutput{WebACLs: []types.WebACLSummary{acl("b")}}, nil
		}
	}
	var gets parentCalls
	h["GetWebACL"] = func(in any) (any, error) {
		i := in.(*wafv2.GetWebACLInput)
		gets.add(string(i.Scope) + " " + sv(i.Name) + " " + sv(i.Id))
		return &wafv2.GetWebACLOutput{WebACL: &types.WebACL{ARN: sdkaws.String(wafACLARN(sv(i.Name))), Name: i.Name, Id: i.Id, Description: sdkaws.String("body of " + sv(i.Name))}}, nil
	}
	var listed parentCalls
	h["ListResourcesForWebACL"] = func(in any) (any, error) {
		listed.add(sv(in.(*wafv2.ListResourcesForWebACLInput).WebACLArn))
		return wafALBAssociation(in)
	}
	st := newTestStore(t)
	if _, _, _, err := wafTagScan(t, st, h); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if got, want := gets.distinct(), []string{"REGIONAL a id-a", "REGIONAL b id-b"}; !slices.Equal(got, want) {
		t.Errorf("GetWebACL inputs = %q; want %q", got, want)
	}
	acls := rowsByNativeID(t, st, TypeWAFv2WebACL)
	if got, want := nativeIDs(acls), []string{wafACLARN("a"), wafACLARN("b")}; !slices.Equal(got, want) {
		t.Fatalf("web ACL rows = %v; want %v", got, want)
	}
	for _, name := range []string{"a", "b"} {
		var body types.WebACL
		if err := json.Unmarshal([]byte(acls[wafACLARN(name)].AttributesJSON), &body); err != nil {
			t.Fatal(err)
		}
		if sv(body.Id) != "id-"+name || sv(body.Description) != "body of "+name {
			t.Errorf("%s attrs Id, Description = %q, %q; want its GetWebACL body", name, sv(body.Id), sv(body.Description))
		}
	}
	if got, want := listed.distinct(), []string{wafACLARN("a"), wafACLARN("b")}; !slices.Equal(got, want) {
		t.Errorf("ListResourcesForWebACL called for %v; want %v", got, want)
	}
	var wantAssoc []string
	for _, name := range []string{"a", "b"} {
		a := wafACLARN(name)
		wantAssoc = append(wantAssoc, a+"/association/"+a+"-alb")
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWAFv2WebACLAssociation)); !slices.Equal(got, wantAssoc) {
		t.Errorf("associations = %v; want %v", got, wantAssoc)
	}
}

// Once ctx ends during the first page's tag reads, that page is stored
// without tags, no later phase starts, and the stop is the error's one
// member, reported under wafv2 with the phases not run, although the listing
// and each tag read saw it too.
func TestScanWAFv2_ContextEndStopsOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rec wafTagCalls
	h := wafTagHandlers(wafTaggedItems, &rec, func(*wafv2.ListTagsForResourceInput) (any, error) {
		cancel()
		return nil, context.Canceled
	})
	var ruleGroups atomic.Int32
	h["ListRuleGroups"] = func(any) (any, error) {
		ruleGroups.Add(1)
		return &wafv2.ListRuleGroupsOutput{}, nil
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	total, _, err := scanWAFv2(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if n := len(bacTopMembers(err)); n != 1 {
		t.Errorf("error members = %d; want 1 (err %v)", n, err)
	}
	// Every phase but the REGIONAL web ACLs: 6 more per scope, the
	// REGIONAL associations and both scopes' API keys.
	if !strings.Contains(err.Error(), "wafv2: 14 phases not run") {
		t.Errorf("err = %v; want it to say wafv2: 14 phases not run", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 REGIONAL web ACLs of the page in flight", total)
	}
	rows := rowsByNativeID(t, st, TypeWAFv2WebACL)
	var stored []string
	for _, w := range wafTaggedItems[:2] {
		stored = append(stored, w.arn())
		if r, ok := rows[w.arn()]; ok && r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", w.arn(), *r.TagsJSON)
		}
	}
	slices.Sort(stored)
	if got := nativeIDs(rows); !slices.Equal(got, stored) {
		t.Errorf("web ACL rows = %v; want %v", got, stored)
	}
	if n := ruleGroups.Load(); n != 0 {
		t.Errorf("ListRuleGroups calls = %d; want none after ctx ended", n)
	}
}

// A resource's tags are read across every page of ListTagsForResource; a
// repeated NextMarker, or a response without TagInfoForResource, keeps the
// row without tags and returns the error.
func TestScanWAFv2_TagPages(t *testing.T) {
	rg := wafTaggedItems[3]
	page := func(in *wafv2.ListTagsForResourceInput, tags map[string]string, next *string) (any, error) {
		out := wafTagsOut(in, tags)
		out.NextMarker = next
		return out, nil
	}
	cases := []struct {
		name     string
		page2    func(in *wafv2.ListTagsForResourceInput) (any, error)
		wantTags map[string]string
		wantErr  error
	}{
		{"two pages", func(in *wafv2.ListTagsForResourceInput) (any, error) {
			return page(in, map[string]string{"env": "prod"}, nil)
		}, map[string]string{"team": "rg", "env": "prod"}, nil},
		{"repeated marker", func(in *wafv2.ListTagsForResourceInput) (any, error) {
			return page(in, nil, sdkaws.String("t1"))
		}, nil, errRepeatedNextToken},
		{"no tag info", func(*wafv2.ListTagsForResourceInput) (any, error) {
			return &wafv2.ListTagsForResourceOutput{}, nil
		}, nil, errWAFv2NoTagInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec wafTagCalls
			calls := 0 // the pages of one resource are read in turn
			h := wafTagHandlers([]wafTagged{rg}, &rec, func(in *wafv2.ListTagsForResourceInput) (any, error) {
				if calls++; calls > 3 {
					t.Errorf("ListTagsForResource called %d times; the read must stop by page 2", calls)
					return nil, apiErr("StubCallLimit", "stub call limit")
				}
				if in.NextMarker == nil {
					return page(in, map[string]string{"team": "rg"}, sdkaws.String("t1"))
				}
				if sv(in.NextMarker) != "t1" || sdkaws.ToInt32(in.Limit) != 100 {
					t.Errorf("page 2 NextMarker, Limit = %q, %d; want t1, 100", sv(in.NextMarker), sdkaws.ToInt32(in.Limit))
				}
				return tc.page2(in)
			})
			st := newTestStore(t)
			_, _, _, err := wafTagScan(t, st, h)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v; want %v", err, tc.wantErr)
			}
			r, ok := rowsByNativeID(t, st, TypeWAFv2RuleGroup)[rg.arn()]
			switch {
			case !ok:
				t.Fatalf("rule group %s not stored", rg.arn())
			case tc.wantTags == nil && r.TagsJSON != nil:
				t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
			case tc.wantTags != nil:
				assertTagsMap(t, r.TagsJSON, tc.wantTags)
			}
		})
	}
}

// A failed tag read keeps the row without tags, except for a resource
// ListTagsForResource no longer finds, which is not stored (and a web ACL
// so dropped has no associations listed): denials, in any type and scope,
// are one warning for the scan; a transient failure is returned as one, and
// demoted to a warning beside a hard error; any other error is returned.
func TestScanWAFv2_TagReadErrors(t *testing.T) {
	a, cf, rg, ip := wafTaggedItems[0], wafTaggedItems[2], wafTaggedItems[3], wafTaggedItems[5]
	denied := apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListTagsForResource")
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name      string
		fail      map[string]error // by ARN
		dropped   []wafTagged
		wantTotal int
		wantErr   func(error) bool // nil: the scan succeeds
		wantWarns int              // warnings under wafv2:ListTagsForResource
	}{
		{"denied", map[string]error{a.arn(): denied, cf.arn(): denied, rg.arn(): denied}, nil, wafTaggedTotal, nil, 1},
		// a's association row goes with it.
		{"gone", map[string]error{a.arn(): apiErr("WAFNonexistentItemException", "gone"), rg.arn(): apiErr("WAFNonexistentItemException", "gone")}, []wafTagged{a, rg}, wafTaggedTotal - 3, nil, 0},
		{
			"other error",
			map[string]error{rg.arn(): apiErr("WAFInvalidOperationException", "bad")},
			nil, wafTaggedTotal,
			func(err error) bool {
				return classifyServiceError(err) == outcomeError && isAPIErrorCode(err, "WAFInvalidOperationException")
			}, 0,
		},
		{
			"transient",
			map[string]error{rg.arn(): throttled},
			nil, wafTaggedTotal,
			func(err error) bool {
				return classifyServiceError(err) == outcomeTransient && isAPIErrorCode(err, "ThrottlingException")
			}, 0,
		},
		{
			"transient beside other error",
			map[string]error{rg.arn(): throttled, ip.arn(): apiErr("WAFInvalidOperationException", "bad")},
			nil, wafTaggedTotal,
			func(err error) bool {
				return classifyServiceError(err) == outcomeError && isAPIErrorCode(err, "WAFInvalidOperationException") && !isAPIErrorCode(err, "ThrottlingException")
			}, 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				rec    wafTagCalls
				listed parentCalls
			)
			h := wafTagHandlers(wafTaggedItems, &rec, nil)
			ok := h["ListTagsForResource"]
			h["ListTagsForResource"] = func(in any) (any, error) {
				if err := tc.fail[sv(in.(*wafv2.ListTagsForResourceInput).ResourceARN)]; err != nil {
					return nil, err
				}
				return ok(in)
			}
			h["ListResourcesForWebACL"] = func(in any) (any, error) {
				listed.add(sv(in.(*wafv2.ListResourcesForWebACLInput).WebACLArn))
				return wafALBAssociation(in)
			}
			st := newTestStore(t)
			warns, total, _, err := wafTagScan(t, st, h)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("scanWAFv2: %v", err)
			}
			if tc.wantErr != nil && !tc.wantErr(err) {
				t.Errorf("err = %v; not the wanted error", err)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d", total, tc.wantTotal)
			}
			for _, w := range wafTaggedItems {
				r, stored := rowsByNativeID(t, st, w.typ)[w.arn()]
				if wantStored := !slices.Contains(tc.dropped, w); stored != wantStored {
					t.Errorf("%s stored = %v; want %v", w.arn(), stored, wantStored)
					continue
				}
				if _, failed := tc.fail[w.arn()]; stored && failed && r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s; want nil after a failed read", w.arn(), *r.TagsJSON)
				}
			}
			var wantListed []string
			for _, w := range wafTaggedItems[:2] {
				if !slices.Contains(tc.dropped, w) {
					wantListed = append(wantListed, w.arn())
				}
			}
			slices.Sort(wantListed)
			if got := listed.distinct(); !slices.Equal(got, wantListed) {
				t.Errorf("ListResourcesForWebACL called for %q; want %q", got, wantListed)
			}
			ws := wafWarnings(warns(), "wafv2:ListTagsForResource")
			if len(ws) != tc.wantWarns {
				t.Fatalf("wafv2:ListTagsForResource warnings = %+v; want %d", ws, tc.wantWarns)
			}
			for _, w := range ws {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A tag read denied before a failed write sends no warning: the row it
// names was never stored.
func TestScanWAFv2_TagDenialAfterFailedWriteNotWarned(t *testing.T) {
	st, closeStore := openClosingStore(t)
	var rec wafTagCalls
	h := wafTagHandlers(wafTaggedItems[3:4], &rec, func(*wafv2.ListTagsForResourceInput) (any, error) {
		closeStore()
		return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:ListTagsForResource")
	})
	warns, _, _, err := wafTagScan(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the store write failure", err)
	}
	if ws := wafWarnings(warns(), "wafv2:ListTagsForResource"); len(ws) != 0 {
		t.Errorf("wafv2:ListTagsForResource warnings = %+v; want none", ws)
	}
}

// GetWebACL denied in both scopes is one warning for the scan, and every
// ACL is kept as its summary.
func TestScanWAFv2_GetWebACLDenialsOneWarning(t *testing.T) {
	var rec wafTagCalls
	h := wafTagHandlers(wafTaggedItems, &rec, nil)
	h["GetWebACL"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: wafv2:GetWebACL")
	}
	st := newTestStore(t)
	warns, _, _, err := wafTagScan(t, st, h)
	if err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if ws := wafWarnings(warns(), "wafv2:GetWebACL"); len(ws) != 1 {
		t.Errorf("wafv2:GetWebACL warnings = %+v; want 1", ws)
	}
	rows := rowsByNativeID(t, st, TypeWAFv2WebACL)
	for _, w := range wafTaggedItems[:3] {
		var summary types.WebACLSummary
		if err := json.Unmarshal([]byte(rows[w.arn()].AttributesJSON), &summary); err != nil {
			t.Fatal(err)
		}
		if sv(summary.LockToken) != "lock-"+w.name {
			t.Errorf("%s attrs = %s; want its summary", w.arn(), rows[w.arn()].AttributesJSON)
		}
	}
}

// In GovCloud the ARN a listing returns reaches ListTagsForResource
// unchanged.
func TestScanWAFv2_GovCloudARNSentUnchanged(t *testing.T) {
	const region = "us-gov-west-1"
	govARN := "arn:aws-us-gov:wafv2:" + region + ":" + testAccountID + ":regional/rulegroup/rg/1"
	var sent parentCalls
	st := newTestStore(t)
	acct := testAccountIn("aws-us-gov")
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"ListRuleGroups": func(in any) (any, error) {
			return &wafv2.ListRuleGroupsOutput{RuleGroups: []types.RuleGroupSummary{{ARN: sdkaws.String(govARN), Name: sdkaws.String("rg")}}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			i := in.(*wafv2.ListTagsForResourceInput)
			sent.add(sv(i.ResourceARN))
			return wafTagsOut(i, map[string]string{"team": "gov"}), nil
		},
		"ListWebACLs":               func(any) (any, error) { return &wafv2.ListWebACLsOutput{}, nil },
		"ListIPSets":                func(any) (any, error) { return &wafv2.ListIPSetsOutput{}, nil },
		"ListLoggingConfigurations": func(any) (any, error) { return &wafv2.ListLoggingConfigurationsOutput{}, nil },
		"ListRegexPatternSets":      func(any) (any, error) { return &wafv2.ListRegexPatternSetsOutput{}, nil },
		"ListManagedRuleSets":       func(any) (any, error) { return &wafv2.ListManagedRuleSetsOutput{}, nil },
		"ListAPIKeys":               func(any) (any, error) { return &wafv2.ListAPIKeysOutput{}, nil },
	}, apiErr("AccessDeniedException", "unstubbed op"))
	if _, _, err := scanWAFv2(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scanWAFv2: %v", err)
	}
	if got := sent.distinct(); !slices.Equal(got, []string{govARN}) {
		t.Errorf("ListTagsForResource ARNs = %q; want [%s]", got, govARN)
	}
	r, ok := rowsByNativeID(t, st, TypeWAFv2RuleGroup)[govARN]
	if !ok {
		t.Fatalf("rule group %s not stored", govARN)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"team": "gov"})
}

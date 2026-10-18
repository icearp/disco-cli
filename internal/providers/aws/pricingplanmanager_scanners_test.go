package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricingplanmanager"
	ppmtypes "github.com/aws/aws-sdk-go-v2/service/pricingplanmanager/types"
	"github.com/icearp/disco-cli/store"
)

const (
	ppmSubA       = "arn:aws:pricingplanmanager::" + testAccountID + ":subscription/sub-a"
	ppmSubB       = "arn:aws:pricingplanmanager::" + testAccountID + ":subscription/sub-b"
	ppmDistARN    = "arn:aws:cloudfront::" + testAccountID + ":distribution/E1ABCDEF"
	ppmWebACLARN  = "arn:aws:wafv2:us-east-1:" + testAccountID + ":global/webacl/site/11111111-2222-3333-4444-555555555555"
	ppmZoneARN    = "arn:aws:route53:::hostedzone/Z0123456789"
	ppmKVSARN     = "arn:aws:cloudfront::" + testAccountID + ":key-value-store/22222222-3333-4444-5555-666666666666"
	ppmUnknownARN = "arn:aws:s3:::not-scanned-bucket"
)

// ppmSummary is a ListSubscriptions element for arn covering resourceARNs.
func ppmSummary(arn string, status ppmtypes.Status, resourceARNs ...string) ppmtypes.SubscriptionSummary {
	return ppmtypes.SubscriptionSummary{
		Arn:          ptrStr(arn),
		PlanFamily:   ptrStr("CloudFront"),
		PlanTier:     ptrStr("PRO"),
		Status:       status,
		ResourceArns: resourceARNs,
		CreatedAt:    sdkaws.Time(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)),
		UpdatedAt:    sdkaws.Time(time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)),
		ETag:         ptrStr("etag-1"),
	}
}

// runPricingPlanManager runs the registered aws:pricingplanmanager scanner
// against handlers, recording each call's region; an unstubbed op is denied.
func runPricingPlanManager(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, *parentCalls, error) {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:pricingplanmanager" })
	if idx < 0 {
		t.Fatal("aws:pricingplanmanager is not registered")
	}
	if !registeredServices[idx].global {
		t.Error("aws:pricingplanmanager is not registered global")
	}
	var regions parentCalls
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfig(handlers, apiErr("AccessDeniedException", "unstubbed op")), &regions)
	// A config region other than the pin, so the recorded region proves the
	// client overrides it.
	acct.cfg.Region = "eu-west-1"
	total, _, err := registeredServices[idx].fn(context.Background(), acct, "", st, testScanID)
	return total, &regions, err
}

// ppmPages answers ListSubscriptions with pages in order, checking that each
// call sends the token the previous page returned, and fails the test on a
// call past the last page.
func ppmPages(t *testing.T, pages ...func() (*pricingplanmanager.ListSubscriptionsOutput, error)) map[string]func(in any) (any, error) {
	var mu sync.Mutex
	calls := 0
	prevToken := ""
	return map[string]func(in any) (any, error){
		"ListSubscriptions": func(in any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			if calls >= len(pages) {
				t.Errorf("ListSubscriptions call %d, want at most %d", calls+1, len(pages))
				return nil, apiErr("ValidationException", "too many calls")
			}
			if got := sv(in.(*pricingplanmanager.ListSubscriptionsInput).NextToken); got != prevToken {
				t.Errorf("ListSubscriptions call %d NextToken = %q, want %q", calls+1, got, prevToken)
			}
			out, err := pages[calls]()
			calls++
			if out != nil {
				prevToken = sv(out.NextToken)
			}
			return out, err
		},
	}
}

func ppmPage(token string, subs ...ppmtypes.SubscriptionSummary) func() (*pricingplanmanager.ListSubscriptionsOutput, error) {
	return func() (*pricingplanmanager.ListSubscriptionsOutput, error) {
		out := &pricingplanmanager.ListSubscriptionsOutput{SubscriptionSummaries: subs}
		if token != "" {
			out.NextToken = &token
		}
		return out, nil
	}
}

// Both pages are stored, in the global region, from the us-east-1 endpoint;
// an ARN-less summary is skipped.
func TestScanPricingPlanManager_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	noARN := ppmSummary("", ppmtypes.StatusActive)
	noARN.Arn = nil
	total, regions, err := runPricingPlanManager(t, st, ppmPages(t,
		ppmPage("p1", ppmSummary(ppmSubA, ppmtypes.StatusActive, ppmDistARN, ppmWebACLARN)),
		ppmPage("", ppmSummary(ppmSubB, ppmtypes.StatusPendingApproval, ppmDistARN), noARN),
	))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	rows := rowsByNativeID(t, st, TypePricingPlanManagerSubscription)
	if len(rows) != 2 {
		t.Fatalf("subscriptions = %d, want 2: %v", len(rows), rows)
	}
	a := rows[ppmSubA]
	if a.Type != TypePricingPlanManagerSubscription || sv(a.Region) != "global" || sv(a.Status) != "ACTIVE" ||
		sv(a.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("sub-a type, region, status, created = %q, %q, %q, %q; want %s, global, ACTIVE, 2026-03-04T05:06:07Z",
			a.Type, sv(a.Region), sv(a.Status), sv(a.CreatedAt), TypePricingPlanManagerSubscription)
	}
	var attrs struct {
		PlanTier     string   `json:"PlanTier"`
		ResourceArns []string `json:"ResourceArns"`
	}
	if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode sub-a attributes: %v", err)
	}
	if attrs.PlanTier != "PRO" || !slices.Equal(attrs.ResourceArns, []string{ppmDistARN, ppmWebACLARN}) {
		t.Errorf("sub-a attrs PlanTier, ResourceArns = %q, %v; want PRO, [%s %s]", attrs.PlanTier, attrs.ResourceArns, ppmDistARN, ppmWebACLARN)
	}
	if b := rows[ppmSubB]; sv(b.Status) != "PENDING_APPROVAL" {
		t.Errorf("sub-b status = %q, want PENDING_APPROVAL", sv(b.Status))
	}
}

// A service that returns the token it was sent ends the listing instead of
// looping.
func TestScanPricingPlanManager_StopsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	total, _, err := runPricingPlanManager(t, st, ppmPages(t,
		ppmPage("p1", ppmSummary(ppmSubA, ppmtypes.StatusActive)),
		ppmPage("p2", ppmSummary(ppmSubB, ppmtypes.StatusActive)),
		ppmPage("p2"),
	))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

func TestScanPricingPlanManager_Empty(t *testing.T) {
	st := newTestStore(t)
	total, _, err := runPricingPlanManager(t, st, ppmPages(t, ppmPage("")))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if rows := rowsByNativeID(t, st, TypePricingPlanManagerSubscription); len(rows) != 0 {
		t.Errorf("subscriptions = %v, want none", rows)
	}
}

// A denial warns once and is not an error; one after a stored page keeps that
// page's rows.
func TestScanPricingPlanManager_AccessDenied(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pages []func() (*pricingplanmanager.ListSubscriptionsOutput, error)
		want  int
	}{
		{name: "first page", want: 0, pages: []func() (*pricingplanmanager.ListSubscriptionsOutput, error){
			func() (*pricingplanmanager.ListSubscriptionsOutput, error) {
				return nil, apiErr("AccessDeniedException", "not authorized to perform: pricingplanmanager:ListSubscriptions")
			},
		}},
		{name: "second page", want: 1, pages: []func() (*pricingplanmanager.ListSubscriptionsOutput, error){
			ppmPage("p1", ppmSummary(ppmSubA, ppmtypes.StatusActive)),
			func() (*pricingplanmanager.ListSubscriptionsOutput, error) {
				return nil, apiErr("AccessDeniedException", "not authorized to perform: pricingplanmanager:ListSubscriptions")
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "pricingplanmanager:ListSubscriptions")
			total, _, err := runPricingPlanManager(t, st, ppmPages(t, tc.pages...))
			if err != nil {
				t.Fatalf("scan: %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if total != tc.want {
				t.Errorf("total = %d, want %d", total, tc.want)
			}
			if rows := rowsByNativeID(t, st, TypePricingPlanManagerSubscription); len(rows) != tc.want {
				t.Errorf("subscriptions = %d, want %d", len(rows), tc.want)
			}
		})
	}
}

// Any other error is returned, after the pages read before it are stored.
func TestScanPricingPlanManager_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	total, _, err := runPricingPlanManager(t, st, ppmPages(t,
		ppmPage("p1", ppmSummary(ppmSubA, ppmtypes.StatusActive)),
		func() (*pricingplanmanager.ListSubscriptionsOutput, error) {
			return nil, apiErr("ResourceNotFoundException", "no such account")
		},
	))
	if !isAPIErrorCode(err, "ResourceNotFoundException") {
		t.Fatalf("err = %v, want ResourceNotFoundException", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if _, ok := rowsByNativeID(t, st, TypePricingPlanManagerSubscription)[ppmSubA]; !ok {
		t.Error("sub-a from the page before the error was not stored")
	}
}

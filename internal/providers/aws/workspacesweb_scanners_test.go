package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspacesweb"
	wswtypes "github.com/aws/aws-sdk-go-v2/service/workspacesweb/types"
	"github.com/icearp/disco-cli/store"
)

var (
	wswDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: workspaces-web:List")
	wswNotFound   = apiErr("ResourceNotFoundException", "gone")
	wswValidation = apiErr("ValidationException", "bad arn")
	wswThrottle   = apiErr("ThrottlingException", "slow down")
)

const (
	wswTagsOp              = "workspaces-web:ListTagsForResource"
	wswIdentityProvidersOp = "ListIdentityProviders"
	wswTotalCount          = 22
)

// wswKind is one account-wide listing of the fixture: the List op, the type
// it stores, the ARN resource prefix, the label its resources are named
// after, whether the listing returns a name (otherwise a row is named by its
// ARN), and page, which builds the op's output from an element's ARN and name
// and the next token.
type wswKind struct {
	op, typ, prefix, label string
	named                  bool
	page                   func(arn, name, next *string) any
}

func wswKinds() []wswKind {
	return []wswKind{
		{"ListPortals", TypeWSWPortal, "portal/", "portal", true, func(arn, name, next *string) any {
			return &workspacesweb.ListPortalsOutput{Portals: []wswtypes.PortalSummary{{PortalArn: arn, DisplayName: name}}, NextToken: next}
		}},
		{"ListBrowserSettings", TypeWSWBrowserSettings, "browserSettings/", "bs", false, func(arn, _, next *string) any {
			return &workspacesweb.ListBrowserSettingsOutput{BrowserSettings: []wswtypes.BrowserSettingsSummary{{BrowserSettingsArn: arn}}, NextToken: next}
		}},
		{"ListDataProtectionSettings", TypeWSWDataProtectionSettings, "dataProtectionSettings/", "dps", true, func(arn, name, next *string) any {
			return &workspacesweb.ListDataProtectionSettingsOutput{DataProtectionSettings: []wswtypes.DataProtectionSettingsSummary{{DataProtectionSettingsArn: arn, DisplayName: name}}, NextToken: next}
		}},
		{"ListIpAccessSettings", TypeWSWIPAccessSettings, "ipAccessSettings/", "ip", true, func(arn, name, next *string) any {
			return &workspacesweb.ListIpAccessSettingsOutput{IpAccessSettings: []wswtypes.IpAccessSettingsSummary{{IpAccessSettingsArn: arn, DisplayName: name}}, NextToken: next}
		}},
		{"ListNetworkSettings", TypeWSWNetworkSettings, "networkSettings/", "ns", false, func(arn, _, next *string) any {
			return &workspacesweb.ListNetworkSettingsOutput{NetworkSettings: []wswtypes.NetworkSettingsSummary{{NetworkSettingsArn: arn, VpcId: sdkaws.String("vpc-1")}}, NextToken: next}
		}},
		{"ListSessionLoggers", TypeWSWSessionLogger, "sessionLogger/", "sl", true, func(arn, name, next *string) any {
			return &workspacesweb.ListSessionLoggersOutput{SessionLoggers: []wswtypes.SessionLoggerSummary{{SessionLoggerArn: arn, DisplayName: name}}, NextToken: next}
		}},
		{"ListTrustStores", TypeWSWTrustStore, "trustStore/", "ts", false, func(arn, _, next *string) any {
			return &workspacesweb.ListTrustStoresOutput{TrustStores: []wswtypes.TrustStoreSummary{{TrustStoreArn: arn}}, NextToken: next}
		}},
		{"ListUserAccessLoggingSettings", TypeWSWUserAccessLoggingSettings, "userAccessLoggingSettings/", "ual", false, func(arn, _, next *string) any {
			return &workspacesweb.ListUserAccessLoggingSettingsOutput{UserAccessLoggingSettings: []wswtypes.UserAccessLoggingSettingsSummary{{UserAccessLoggingSettingsArn: arn}}, NextToken: next}
		}},
		{"ListUserSettings", TypeWSWUserSettings, "userSettings/", "us", false, func(arn, _, next *string) any {
			return &workspacesweb.ListUserSettingsOutput{UserSettings: []wswtypes.UserSettingsSummary{{UserSettingsArn: arn}}, NextToken: next}
		}},
	}
}

func wswKindOf(t *testing.T, op string) wswKind {
	t.Helper()
	for _, k := range wswKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return wswKind{}
}

// wswName is the listed name of the fixture's resource suffix ("tagged" or
// "bare") of kind k.
func wswName(k wswKind, suffix string) string { return k.label + "-" + suffix }

func wswARNIn(partition, region, resource string) string {
	return "arn:" + partition + ":workspaces-web:" + region + ":" + testAccountID + ":" + resource
}

func wswARN(k wswKind, suffix string) string {
	return wswARNIn("aws", testRegion, k.prefix+wswName(k, suffix))
}

// wswPortalARN is the ARN of the fixture's portal suffix.
func wswPortalARN(t *testing.T, suffix string) string {
	return wswARN(wswKindOf(t, "ListPortals"), suffix)
}

// wswIdentityProviderARN is the ARN of identity provider idp of the fixture's portal
// suffix.
func wswIdentityProviderARN(portal, idp string) string {
	return wswARNIn("aws", testRegion, "identityProvider/portal-"+portal+"/idp-"+idp)
}

// wswCalls records the stubbed calls: how often each op ran, the MaxResults
// each was sent, the portal ARNs sent to ListIdentityProviders, and the ARNs
// sent to ListTagsForResource, which it answers from tags or errs, both
// keyed by ARN. Tag reads and identity-provider listings run concurrently.
type wswCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]*int32
	portals    []string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newWSWCalls tags every "tagged" resource with its type.
func newWSWCalls() *wswCalls {
	c := &wswCalls{ops: map[string]int{}, maxResults: map[string][]*int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range wswKinds() {
		c.tags[wswARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	for _, portal := range []string{"tagged", "bare"} {
		c.tags[wswIdentityProviderARN(portal, "tagged")] = map[string]string{"type": TypeWSWIdentityProvider}
	}
	return c
}

func (c *wswCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
}

func (c *wswCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *wswCalls) listTags(in any) (any, error) {
	arn := sv(in.(*workspacesweb.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []wswtypes.Tag
	for k, v := range tags {
		list = append(list, wswtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &workspacesweb.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *wswCalls) twoPages(k wswKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := wswARN(k, suffix), wswName(k, suffix)
		return k.page(&arn, &name, next), nil
	}
}

// identityProviders answers each fixture portal with its "tagged" identity
// provider on the first page and its "bare" one on the second.
func (c *wswCalls) identityProviders(t *testing.T) func(any) (any, error) {
	byARN := map[string]string{wswPortalARN(t, "tagged"): "tagged", wswPortalARN(t, "bare"): "bare"}
	return func(in any) (any, error) {
		req := in.(*workspacesweb.ListIdentityProvidersInput)
		c.record(wswIdentityProvidersOp, in)
		c.mu.Lock()
		c.portals = append(c.portals, sv(req.PortalArn))
		c.mu.Unlock()
		portal, ok := byARN[sv(req.PortalArn)]
		if !ok {
			return nil, errors.New("test: unknown portal " + sv(req.PortalArn))
		}
		idp, next := "tagged", sdkaws.String("t2")
		if req.NextToken != nil {
			idp, next = "bare", nil
		}
		return &workspacesweb.ListIdentityProvidersOutput{IdentityProviders: []wswtypes.IdentityProviderSummary{{
			IdentityProviderArn:  sdkaws.String(wswIdentityProviderARN(portal, idp)),
			IdentityProviderName: sdkaws.String("idp-" + portal + "-" + idp),
			IdentityProviderType: wswtypes.IdentityProviderTypeSaml,
		}}, NextToken: next}, nil
	}
}

func wswHandlers(t *testing.T, c *wswCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags, wswIdentityProvidersOp: c.identityProviders(t)}
	for _, k := range wswKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// wswScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func wswScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wswDenied)
	return scanWorkSpacesWeb(ctx, acct, testRegion, st, testScanID)
}

func wswRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// wswWant is one row the full fixture stores: its type, ARN, Name and tags
// (nil for none).
type wswWant struct {
	typ, arn, name string
	tags           map[string]string
}

func wswWants() []wswWant {
	var want []wswWant
	for _, k := range wswKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := wswARN(k, suffix)
			w := wswWant{k.typ, arn, arn, nil}
			if k.named {
				w.name = wswName(k, suffix)
			}
			if suffix == "tagged" {
				w.tags = map[string]string{"type": k.typ}
			}
			want = append(want, w)
		}
	}
	for _, portal := range []string{"tagged", "bare"} {
		want = append(want,
			wswWant{TypeWSWIdentityProvider, wswIdentityProviderARN(portal, "tagged"), "idp-" + portal + "-tagged", map[string]string{"type": TypeWSWIdentityProvider}},
			wswWant{TypeWSWIdentityProvider, wswIdentityProviderARN(portal, "bare"), "idp-" + portal + "-bare", nil})
	}
	return want
}

// Every type stores both pages of its listing, named as it is listed (by its
// ARN when the listing has no name), with the tags ListTagsForResource
// returns for its ARN, and nil for a resource without tags. Exactly the ARN
// of every row is sent to the tag op; each portal's ARN is sent to
// ListIdentityProviders; no op is sent MaxResults. No error or warning.
func TestScanWorkSpacesWeb_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newWSWCalls()

	total, inserted, err := wswScan(context.Background(), st, wswHandlers(t, calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wswTotalCount || inserted != wswTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, wswTotalCount, wswTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range wswWants() {
		wantARNs = append(wantARNs, w.arn)
		r, ok := wswRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
			continue
		}
		if got := sv(r.Name); got != w.name {
			t.Errorf("%s %s Name = %q; want %q", w.typ, w.arn, got, w.name)
		}
		if sv(r.Region) != testRegion {
			t.Errorf("%s %s Region = %q; want %s", w.typ, w.arn, sv(r.Region), testRegion)
		}
		if w.tags != nil {
			assertTagsMap(t, r.TagsJSON, w.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for op, sent := range calls.maxResults {
		for _, n := range sent {
			if n != nil {
				t.Errorf("%s MaxResults = %d; want unset", op, *n)
			}
		}
	}
	slices.Sort(calls.portals)
	if want := []string{wswPortalARN(t, "bare"), wswPortalARN(t, "bare"), wswPortalARN(t, "tagged"), wswPortalARN(t, "tagged")}; !slices.Equal(calls.portals, want) {
		t.Errorf("%s PortalArns = %v; want %v", wswIdentityProvidersOp, calls.portals, want)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanWorkSpacesWeb_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newWSWCalls()
	if _, _, err := wswScan(context.Background(), st, wswHandlers(t, calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := wswScan(context.Background(), st, wswHandlers(t, calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != wswTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, wswTotalCount)
	}
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently; a denial keeps the row untagged and warns once
// under workspaces-web:ListTagsForResource; a throttle after retries keeps
// it and is a transient error; ValidationException keeps it and is a hard
// error. Each outcome holds for an account-wide listing's row and an
// identity provider's row.
func TestScanWorkSpacesWeb_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeWSWUserSettings, wswARN(wswKindOf(t, "ListUserSettings"), "tagged")},
		{TypeWSWIdentityProvider, wswIdentityProviderARN("bare", "tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", wswNotFound, 0, "", wswTotalCount - 1, false, 0},
				{"denied", wswDenied, 0, "", wswTotalCount, true, 1},
				{"transient", wswThrottle, outcomeTransient, "", wswTotalCount, true, 0},
				{"other error", wswValidation, outcomeError, "ValidationException", wswTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, wswTagsOp)
					calls := newWSWCalls()
					calls.errs[target.arn] = c.err

					total, _, err := wswScan(context.Background(), st, wswHandlers(t, calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := wswRowOf(t, st, target.typ, target.arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", wswTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Two denied tag reads in one phase send one warning.
func TestScanWorkSpacesWeb_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, wswTagsOp)
	calls := newWSWCalls()
	k := wswKindOf(t, "ListTrustStores")
	calls.errs[wswARN(k, "tagged")] = wswDenied
	calls.errs[wswARN(k, "bare")] = wswDenied

	if _, _, err := wswScan(context.Background(), st, wswHandlers(t, calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %d; want 1", wswTagsOp, len(*warnings))
	}
	if got := len(rowsByNativeID(t, st, k.typ)); got != 2 {
		t.Errorf("trust stores = %d; want 2", got)
	}
}

// A portal the tag read finds deleted is dropped and is not a parent: its
// identity providers are not listed.
func TestScanWorkSpacesWeb_PortalGoneHasNoIdentityProviders(t *testing.T) {
	st := newTestStore(t)
	calls := newWSWCalls()
	calls.errs[wswPortalARN(t, "bare")] = wswNotFound

	if _, _, err := wswScan(context.Background(), st, wswHandlers(t, calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := []string{wswPortalARN(t, "tagged"), wswPortalARN(t, "tagged")}; !slices.Equal(calls.portals, want) {
		t.Errorf("%s PortalArns = %v; want %v", wswIdentityProvidersOp, calls.portals, want)
	}
	if got := len(rowsByNativeID(t, st, TypeWSWIdentityProvider)); got != 2 {
		t.Errorf("identity providers = %d; want 2", got)
	}
}

// A denied listing warns once under its own op and stores nothing of its
// type; every other listing still runs. A denied portal listing leaves the
// identity providers unlisted; the identity-provider listing denied for both
// portals warns once.
func TestScanWorkSpacesWeb_DeniedListingWarnsUnderItsOp(t *testing.T) {
	type deniedOp struct {
		op, typ string
		lost    int
	}
	ops := []deniedOp{{wswIdentityProvidersOp, TypeWSWIdentityProvider, 4}}
	for _, k := range wswKinds() {
		lost := 2
		if k.op == "ListPortals" {
			lost = 6
		}
		ops = append(ops, deniedOp{k.op, k.typ, lost})
	}
	for _, c := range ops {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "workspaces-web:"+c.op)
			handlers := wswHandlers(t, newWSWCalls())
			delete(handlers, c.op)

			total, _, err := wswScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != wswTotalCount-c.lost {
				t.Errorf("total = %d; want %d", total, wswTotalCount-c.lost)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("workspaces-web:%s warnings = %v (all ops %d); want 1", c.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, c.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, got)
			}
		})
	}
}

// An identity-provider listing that fails for one portal keeps the other
// portal's identity providers: a denial warns once under the op after the
// write; any other error is returned, ResourceNotFoundException included,
// since the op models no not-found code.
func TestScanWorkSpacesWeb_IdentityProvidersPerPortal(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		code      string
		wantWarns int
	}{
		{"denied", wswDenied, "", 1},
		{"not found", wswNotFound, "ResourceNotFoundException", 0},
		{"other error", wswValidation, "ValidationException", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "workspaces-web:"+wswIdentityProvidersOp)
			handlers := wswHandlers(t, newWSWCalls())
			bare := wswPortalARN(t, "bare")
			next := handlers[wswIdentityProvidersOp]
			handlers[wswIdentityProvidersOp] = func(in any) (any, error) {
				if sv(bacField[*string](in, "PortalArn")) == bare {
					return nil, c.err
				}
				return next(in)
			}

			total, _, err := wswScan(context.Background(), st, handlers)
			if c.code != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != wswTotalCount-2 {
				t.Errorf("total = %d; want %d", total, wswTotalCount-2)
			}
			for _, idp := range []string{"tagged", "bare"} {
				if _, ok := wswRowOf(t, st, TypeWSWIdentityProvider, wswIdentityProviderARN("tagged", idp)); !ok {
					t.Errorf("identity provider %s of the tagged portal missing", idp)
				}
			}
			if len(*warnings) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("workspaces-web:%s warnings = %v (all ops %d); want %d", wswIdentityProvidersOp, *warnings, *all, c.wantWarns)
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanWorkSpacesWeb_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, wswTagsOp)
	calls := newWSWCalls()
	calls.errs[wswARN(wswKindOf(t, "ListNetworkSettings"), "tagged")] = wswThrottle
	calls.errs[wswARN(wswKindOf(t, "ListSessionLoggers"), "bare")] = wswValidation

	_, _, err := wswScan(context.Background(), st, wswHandlers(t, calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", wswTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanWorkSpacesWeb_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeWSWPortal)
	calls := newWSWCalls()
	calls.errs[wswPortalARN(t, "tagged")] = wswDenied

	_, _, err := wswScan(context.Background(), st, wswHandlers(t, calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{wswIdentityProvidersOp, "ListBrowserSettings"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanWorkSpacesWeb_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := wswHandlers(t, newWSWCalls())
	k := wswKindOf(t, "ListPortals")
	handlers[k.op] = failOnCall(2, wswValidation, handlers[k.op])

	total, _, err := wswScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != wswTotalCount-3 {
		t.Errorf("total = %d; want %d", total, wswTotalCount-3)
	}
	if _, ok := wswRowOf(t, st, k.typ, wswARN(k, "tagged")); !ok {
		t.Error("portal from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeWSWIdentityProvider)); got != 2 {
		t.Errorf("identity providers = %d; want 2", got)
	}
	if got := len(rowsByNativeID(t, st, TypeWSWUserSettings)); got != 2 {
		t.Errorf("user settings (last phase) = %d; want 2", got)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanWorkSpacesWeb_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := wswHandlers(t, newWSWCalls())
	k := wswKindOf(t, "ListBrowserSettings")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := wswARN(k, suffix), wswName(k, suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, _, err := wswScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != wswTotalCount {
		t.Errorf("total = %d; want %d", total, wswTotalCount)
	}
	for _, suffix := range []string{"tagged", "bare"} {
		if _, ok := wswRowOf(t, st, k.typ, wswARN(k, suffix)); !ok {
			t.Errorf("browser settings %s from the pages read missing", suffix)
		}
	}
}

// An element without an ARN is warned about under its listing, not stored;
// one without a name is stored, named by its ARN.
func TestScanWorkSpacesWeb_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	warns := bacWarnings(st, "workspaces-web:ListSessionLoggers")
	handlers := wswHandlers(t, newWSWCalls())
	unnamed := wswARNIn("aws", testRegion, "sessionLogger/unnamed")
	handlers["ListSessionLoggers"] = func(any) (any, error) {
		return &workspacesweb.ListSessionLoggersOutput{SessionLoggers: []wswtypes.SessionLoggerSummary{
			{DisplayName: sdkaws.String("no-arn")},
			{SessionLoggerArn: sdkaws.String(unnamed)},
		}}, nil
	}

	total, _, err := wswScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wswTotalCount-1 {
		t.Errorf("total = %d; want %d", total, wswTotalCount-1)
	}
	if r, ok := wswRowOf(t, st, TypeWSWSessionLogger, unnamed); !ok || sv(r.Name) != unnamed {
		t.Errorf("unnamed session logger stored = %v, Name = %q; want stored, named by its ARN", ok, sv(r.Name))
	}
	if len(*warns) != 1 || *all != 1 {
		t.Errorf("workspaces-web:ListSessionLoggers warnings = %v (all ops %d); want 1", *warns, *all)
	}
}

// An element with neither an ARN nor a name is not stored and is warned
// about once under its listing.
func TestScanWorkSpacesWeb_ElementWithNoIdentifierWarns(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	warns := bacWarnings(st, "workspaces-web:ListBrowserSettings")
	handlers := wswHandlers(t, newWSWCalls())
	handlers["ListBrowserSettings"] = func(any) (any, error) {
		return &workspacesweb.ListBrowserSettingsOutput{BrowserSettings: []wswtypes.BrowserSettingsSummary{{}}}, nil
	}

	total, _, err := wswScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wswTotalCount-2 {
		t.Errorf("total = %d; want %d", total, wswTotalCount-2)
	}
	if got := len(rowsByNativeID(t, st, TypeWSWBrowserSettings)); got != 0 {
		t.Errorf("browser settings rows = %d; want 0", got)
	}
	if len(*warns) != 1 || *all != 1 {
		t.Fatalf("workspaces-web:ListBrowserSettings warnings = %v (all ops %d); want 1", *warns, *all)
	}
	w := (*warns)[0]
	if w.Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
	}
	if key := "(an element with no identifier)"; !strings.Contains(w.Message, key) {
		t.Errorf("warning Message = %q; want it to name the element by key %q", w.Message, key)
	}
}

// Rows of an account in another partition are sent to the tag op under the
// ARNs their listings return, an identity provider's included.
func TestScanWorkSpacesWeb_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	st := newTestStore(t)
	calls := newWSWCalls()
	portal := wswARNIn("aws-us-gov", region, "portal/gov")
	idp := wswARNIn("aws-us-gov", region, "identityProvider/gov/idp")
	calls.tags[portal] = map[string]string{"k": "portal"}
	calls.tags[idp] = map[string]string{"k": "idp"}
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"ListPortals": func(any) (any, error) {
			return &workspacesweb.ListPortalsOutput{Portals: []wswtypes.PortalSummary{{PortalArn: &portal}}}, nil
		},
		wswIdentityProvidersOp: func(in any) (any, error) {
			if sv(bacField[*string](in, "PortalArn")) != portal {
				return nil, errors.New("test: wrong portal")
			}
			return &workspacesweb.ListIdentityProvidersOutput{IdentityProviders: []wswtypes.IdentityProviderSummary{{IdentityProviderArn: &idp}}}, nil
		},
	}, wswDenied)

	if _, _, err := scanWorkSpacesWeb(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(calls.tagARNs)
	if want := []string{idp, portal}; !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	for _, w := range []struct{ typ, arn, tag string }{{TypeWSWPortal, portal, "portal"}, {TypeWSWIdentityProvider, idp, "idp"}} {
		r, ok := wswRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Fatalf("%s %s missing", w.typ, w.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"k": w.tag})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanWorkSpacesWeb_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newWSWCalls()
	handlers := wswHandlers(t, calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := wswKindOf(t, "ListBrowserSettings")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := wswScan(ctx, st, handlers)
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
	for _, op := range []string{"ListDataProtectionSettings", "ListUserSettings"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanWorkSpacesWeb_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "workspaces-web.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := wswHandlers(t, newWSWCalls())
		handlers["ListTrustStores"] = func(any) (any, error) { return nil, nx }
		total, _, err := wswScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != wswTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, wswTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newWSWCalls()
		calls.errs[wswPortalARN(t, "bare")] = nx
		_, _, err := wswScan(context.Background(), st, wswHandlers(t, calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanWorkSpacesWeb(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

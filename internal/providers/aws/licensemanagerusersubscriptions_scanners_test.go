package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions"
	lmustypes "github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions/types"
	"github.com/icearp/disco-cli/store"
)

const (
	lmusTestOtherAccount = "210987654321"
	lmusTestDir1         = "d-1111111111"
	lmusTestDir2         = "d-2222222222"
	lmusTestInst1        = "i-0aaaaaaaaaaaaaaa1"
	lmusTestInst2        = "i-0bbbbbbbbbbbbbbb2"
)

func lmusTestARN(resource string) string {
	return lmusTestARNIn(testAccountID, resource)
}

func lmusTestARNIn(account, resource string) string {
	return fmt.Sprintf("arn:aws:license-manager-user-subscriptions:%s:%s:%s", testRegion, account, resource)
}

var (
	lmusTestIdpA = lmusTestARN("identity-provider/idp-a")
	lmusTestIdpB = lmusTestARN("identity-provider/idp-b")
	lmusTestIdpC = lmusTestARNIn(lmusTestOtherAccount, "identity-provider/idp-c")
	lmusTestLSE1 = lmusTestARN("license-server-endpoint/lse-1")
	lmusTestLSE2 = lmusTestARN("license-server-endpoint/lse-2")
	lmusTestPS1  = lmusTestARN("product-subscription/ps-1")
	lmusTestPS2  = lmusTestARN("product-subscription/ps-2")
	lmusTestPS3  = lmusTestARNIn(lmusTestOtherAccount, "product-subscription/ps-3")
	lmusTestIU1  = lmusTestARN("instance-user/iu-1")
	lmusTestIU2  = lmusTestARN("instance-user/iu-2")
	lmusTestIU3  = lmusTestARN("instance-user/iu-3")

	lmusDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: license-manager-user-subscriptions:List")
	lmusNoSLR    = apiErr("AccessDeniedException", "Service Linked role is not present")
	lmusThrottle = apiErr("ThrottlingException", "slow down")
	lmusBadInput = apiErr("ValidationException", "bad input")
	lmusGoneErr  = apiErr("ResourceNotFoundException", "gone")

	// lmusTestTags are the tags ListTagsForResource returns; every other
	// resource has none.
	lmusTestTags = map[string]map[string]string{
		lmusTestIdpA: {"env": "prod"},
		lmusTestLSE1: {"team": "ops"},
		lmusTestPS1:  {"cost": "7"},
		lmusTestIU1:  {"app": "vdi", "tier": "1"},
	}
)

// lmusTestTotal is every row lmusHandlers yields: 3 identity providers, 2
// license server endpoints, 3 product subscriptions and 3 instance users.
const lmusTestTotal = 11

func lmusAD(dir string) lmustypes.IdentityProvider {
	return &lmustypes.IdentityProviderMemberActiveDirectoryIdentityProvider{Value: lmustypes.ActiveDirectoryIdentityProvider{DirectoryId: sdkaws.String(dir)}}
}

// lmusDirOf is the directory id of an Active Directory identity provider.
func lmusDirOf(ip lmustypes.IdentityProvider) string {
	ad, ok := ip.(*lmustypes.IdentityProviderMemberActiveDirectoryIdentityProvider)
	if !ok {
		return ""
	}
	return sv(ad.Value.DirectoryId)
}

// lmusCalls records the calls lmusHandlers answers, and holds the tags and
// errors its ListTagsForResource returns per ARN on top of lmusTestTags, and
// the errors its per-parent listings return per directory or instance id.
type lmusCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent; subDirs the
	// directory of every ListProductSubscriptions call; assocs the
	// "instance/directory" of every ListUserAssociations call; lists every
	// listing op called.
	tagARNs, subDirs, assocs, lists parentCalls
	tags                            map[string]map[string]string
	tagErrs, subErrs, assocErrs     map[string]error

	mu sync.Mutex
	// maxResults is the MaxResults of every listing call, per op.
	maxResults map[string][]*int32
}

// listed records a listing call of op and reports whether it asks for the
// first page.
func (c *lmusCalls) listed(op string, in any) bool {
	c.lists.add(op)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxResults == nil {
		c.maxResults = map[string][]*int32{}
	}
	c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	return bacField[*string](in, "NextToken") == nil
}

func (c *lmusCalls) listTags(in any) (any, error) {
	arn := sv(in.(*licensemanagerusersubscriptions.ListTagsForResourceInput).ResourceArn)
	c.tagARNs.add(arn)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = lmusTestTags[arn]
	}
	return &licensemanagerusersubscriptions.ListTagsForResourceOutput{Tags: tags}, nil
}

// lmusHandlers answers a region with identity provider A (directory 1) on
// page 1, and B (directory 1 again, another product; its ARN in this account,
// its OwnerAccountId another) and C (directory 2; its ARN and OwnerAccountId
// another account's) on page 2; endpoint 1 on page 1 and 2 (no status) on
// page 2; directory 1's subscriptions 1 and 2 on two pages and directory 2's
// subscription 3 (its ARN another account's); instance 1 (directory 1) on page 1 and 2
// (directory 2) on page 2; instance 1's users 1 and 2 on two pages and
// instance 2's user 3.
func lmusHandlers(calls *lmusCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListIdentityProviders": func(in any) (any, error) {
			if calls.listed("ListIdentityProviders", in) {
				return &licensemanagerusersubscriptions.ListIdentityProvidersOutput{
					IdentityProviderSummaries: []lmustypes.IdentityProviderSummary{{
						IdentityProviderArn: &lmusTestIdpA, IdentityProvider: lmusAD(lmusTestDir1), Product: sdkaws.String("OFFICE_PROFESSIONAL_PLUS"),
						Status: sdkaws.String("REGISTERED"), OwnerAccountId: sdkaws.String(testAccountID),
						Settings: &lmustypes.Settings{SecurityGroupId: sdkaws.String("sg-1"), Subnets: []string{"subnet-1"}},
					}},
					NextToken: &page2,
				}, nil
			}
			return &licensemanagerusersubscriptions.ListIdentityProvidersOutput{IdentityProviderSummaries: []lmustypes.IdentityProviderSummary{
				{IdentityProviderArn: &lmusTestIdpB, IdentityProvider: lmusAD(lmusTestDir1), Product: sdkaws.String("VISUAL_STUDIO_ENTERPRISE"), Status: sdkaws.String("REGISTERED"), OwnerAccountId: sdkaws.String(lmusTestOtherAccount)},
				{IdentityProviderArn: &lmusTestIdpC, IdentityProvider: lmusAD(lmusTestDir2), Product: sdkaws.String("REMOTE_DESKTOP_SERVICES"), Status: sdkaws.String("REGISTERING"), OwnerAccountId: sdkaws.String(lmusTestOtherAccount)},
			}}, nil
		},
		"ListLicenseServerEndpoints": func(in any) (any, error) {
			if calls.listed("ListLicenseServerEndpoints", in) {
				return &licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput{
					LicenseServerEndpoints: []lmustypes.LicenseServerEndpoint{{
						LicenseServerEndpointArn: &lmusTestLSE1, LicenseServerEndpointId: sdkaws.String("lse-1"), IdentityProviderArn: &lmusTestIdpC,
						LicenseServerEndpointProvisioningStatus: lmustypes.LicenseServerEndpointProvisioningStatusProvisioned, ServerType: lmustypes.ServerTypeRdsSal,
					}},
					NextToken: &page2,
				}, nil
			}
			return &licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput{LicenseServerEndpoints: []lmustypes.LicenseServerEndpoint{
				{LicenseServerEndpointArn: &lmusTestLSE2, LicenseServerEndpointId: sdkaws.String("lse-2")},
			}}, nil
		},
		"ListProductSubscriptions": func(in any) (any, error) {
			first := calls.listed("ListProductSubscriptions", in)
			pin := in.(*licensemanagerusersubscriptions.ListProductSubscriptionsInput)
			dir := lmusDirOf(pin.IdentityProvider)
			calls.subDirs.add(dir)
			if err, ok := calls.subErrs[dir]; ok {
				return nil, err
			}
			// A Product would narrow the listing to one product.
			if pin.Product != nil {
				return nil, apiErr("ValidationException", "this stub answers only a listing sent no Product")
			}
			sub := func(arn *string, user string) lmustypes.ProductUserSummary {
				return lmustypes.ProductUserSummary{
					ProductUserArn: arn, Username: sdkaws.String(user), Product: sdkaws.String("OFFICE_PROFESSIONAL_PLUS"),
					IdentityProvider: lmusAD(dir), Status: sdkaws.String("SUBSCRIBED"), Domain: sdkaws.String("corp.example.com"),
				}
			}
			out := &licensemanagerusersubscriptions.ListProductSubscriptionsOutput{}
			switch {
			case dir == lmusTestDir1 && first:
				out.ProductUserSummaries = []lmustypes.ProductUserSummary{sub(&lmusTestPS1, "alice")}
				out.NextToken = &page2
			case dir == lmusTestDir1:
				out.ProductUserSummaries = []lmustypes.ProductUserSummary{sub(&lmusTestPS2, "bob")}
			case dir == lmusTestDir2:
				out.ProductUserSummaries = []lmustypes.ProductUserSummary{sub(&lmusTestPS3, "carol")}
			}
			return out, nil
		},
		"ListInstances": func(in any) (any, error) {
			if calls.listed("ListInstances", in) {
				return &licensemanagerusersubscriptions.ListInstancesOutput{
					InstanceSummaries: []lmustypes.InstanceSummary{{InstanceId: sdkaws.String(lmusTestInst1), IdentityProvider: lmusAD(lmusTestDir1), Status: sdkaws.String("ACTIVE")}},
					NextToken:         &page2,
				}, nil
			}
			return &licensemanagerusersubscriptions.ListInstancesOutput{
				InstanceSummaries: []lmustypes.InstanceSummary{{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir2), Status: sdkaws.String("ACTIVE")}},
			}, nil
		},
		"ListUserAssociations": func(in any) (any, error) {
			first := calls.listed("ListUserAssociations", in)
			uin := in.(*licensemanagerusersubscriptions.ListUserAssociationsInput)
			inst := sv(uin.InstanceId)
			calls.assocs.add(inst + "/" + lmusDirOf(uin.IdentityProvider))
			if err, ok := calls.assocErrs[inst]; ok {
				return nil, err
			}
			user := func(arn *string, name string) lmustypes.InstanceUserSummary {
				return lmustypes.InstanceUserSummary{
					InstanceUserArn: arn, Username: sdkaws.String(name), InstanceId: uin.InstanceId,
					IdentityProvider: uin.IdentityProvider, Status: sdkaws.String("ASSOCIATED"), AssociationDate: sdkaws.String("2026-01-02"),
				}
			}
			out := &licensemanagerusersubscriptions.ListUserAssociationsOutput{}
			switch {
			case inst == lmusTestInst1 && first:
				out.InstanceUserSummaries = []lmustypes.InstanceUserSummary{user(&lmusTestIU1, "alice")}
				out.NextToken = &page2
			case inst == lmusTestInst1:
				out.InstanceUserSummaries = []lmustypes.InstanceUserSummary{user(&lmusTestIU2, "bob")}
			case inst == lmusTestInst2:
				out.InstanceUserSummaries = []lmustypes.InstanceUserSummary{user(&lmusTestIU3, "carol")}
			}
			return out, nil
		},
		"ListTagsForResource": calls.listTags,
	}
}

// lmusScan runs the service scan entry point against handlers; an op without
// a handler is denied.
func lmusScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lmusDenied)
	return scanLicenseManagerUserSubscriptions(ctx, acct, testRegion, st, testScanID)
}

func lmusSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// lmusAllIDs is the NativeID of every row lmusHandlers yields, sorted: the
// ARNs sent to ListTagsForResource.
func lmusAllIDs() []string {
	return lmusSorted(lmusTestIdpA, lmusTestIdpB, lmusTestIdpC, lmusTestLSE1, lmusTestLSE2,
		lmusTestPS1, lmusTestPS2, lmusTestPS3, lmusTestIU1, lmusTestIU2, lmusTestIU3)
}

// lmusStored is the NativeID of every stored row of the service, sorted.
func lmusStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{
		TypeLicenseManagerUserSubscriptionsIdentityProvider, TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint,
		TypeLicenseManagerUserSubscriptionsProductSubscription, TypeLicenseManagerUserSubscriptionsInstanceUser,
	} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// lmusWithout is lmusAllIDs less drop.
func lmusWithout(drop ...string) []string {
	return slices.DeleteFunc(lmusAllIDs(), func(id string) bool { return slices.Contains(drop, id) })
}

// Every row is stored from every page under its listed ARN, named and with
// the status its element gives, its attributes the listed element, with the
// tags ListTagsForResource returns (nil for none). The tag op is sent exactly
// the ARN of each row, once. Identity providers A and B share an
// IdentityProvider, so its product subscriptions are listed once (two pages),
// beside directory 2's; each instance's users are listed with the
// IdentityProvider its summary names. Only ListLicenseServerEndpoints is sent
// a MaxResults (100). No error, no warning.
func TestScanLMUS_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls lmusCalls
	counted, upserts := countUpserts(st)
	total, err := lmusScan(context.Background(), counted, lmusHandlers(&calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != lmusTestTotal || inserted != lmusTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, lmusTestTotal, lmusTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, lmusAllIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, lmusAllIDs())
	}
	if n := len(calls.tagARNs.ids); n != lmusTestTotal {
		t.Errorf("ListTagsForResource calls = %d; want one per row, %d", n, lmusTestTotal)
	}
	if got, want := lmusSorted(calls.subDirs.ids...), []string{lmusTestDir1, lmusTestDir1, lmusTestDir2}; !slices.Equal(got, want) {
		t.Errorf("ListProductSubscriptions directories = %v; want %v", got, want)
	}
	if got, want := lmusSorted(calls.assocs.ids...), []string{lmusTestInst1 + "/" + lmusTestDir1, lmusTestInst1 + "/" + lmusTestDir1, lmusTestInst2 + "/" + lmusTestDir2}; !slices.Equal(got, want) {
		t.Errorf("ListUserAssociations inputs = %v; want %v", got, want)
	}
	for op, n := range map[string]int{"ListIdentityProviders": 2, "ListLicenseServerEndpoints": 2, "ListProductSubscriptions": 3, "ListInstances": 2, "ListUserAssociations": 3} {
		got := calls.maxResults[op]
		if len(got) != n {
			t.Errorf("%s calls = %d; want %d", op, len(got), n)
		}
		for _, m := range got {
			switch {
			case op == "ListLicenseServerEndpoints" && (m == nil || *m != 100):
				t.Errorf("%s MaxResults = %v; want 100", op, m)
			case op != "ListLicenseServerEndpoints" && m != nil:
				t.Errorf("%s MaxResults = %d; want none", op, *m)
			}
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	for _, c := range []struct {
		typ, id      string
		name, status string
		attr         func(string) bool
	}{
		{TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusTestIdpA, "OFFICE_PROFESSIONAL_PLUS", "REGISTERED", func(a string) bool {
			var s struct {
				Settings       lmustypes.Settings
				OwnerAccountID string `json:"OwnerAccountId"`
			}
			return json.Unmarshal([]byte(a), &s) == nil && sv(s.Settings.SecurityGroupId) == "sg-1" && s.OwnerAccountID == testAccountID
		}},
		{TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusTestIdpB, "VISUAL_STUDIO_ENTERPRISE", "REGISTERED", nil},
		{TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusTestIdpC, "REMOTE_DESKTOP_SERVICES", "REGISTERING", nil},
		{TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, lmusTestLSE1, "lse-1", "PROVISIONED", func(a string) bool {
			var e lmustypes.LicenseServerEndpoint
			return json.Unmarshal([]byte(a), &e) == nil && sv(e.IdentityProviderArn) == lmusTestIdpC && e.ServerType == lmustypes.ServerTypeRdsSal
		}},
		// An endpoint without a provisioning status keeps the empty status
		// the scanner always stored for it.
		{TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, lmusTestLSE2, "lse-2", "", nil},
		{TypeLicenseManagerUserSubscriptionsProductSubscription, lmusTestPS1, "alice", "SUBSCRIBED", func(a string) bool {
			var u struct{ Domain, Product string }
			return json.Unmarshal([]byte(a), &u) == nil && u.Domain == "corp.example.com" && u.Product == "OFFICE_PROFESSIONAL_PLUS"
		}},
		{TypeLicenseManagerUserSubscriptionsProductSubscription, lmusTestPS2, "bob", "SUBSCRIBED", nil},
		{TypeLicenseManagerUserSubscriptionsProductSubscription, lmusTestPS3, "carol", "SUBSCRIBED", nil},
		{TypeLicenseManagerUserSubscriptionsInstanceUser, lmusTestIU1, "alice", "ASSOCIATED", func(a string) bool {
			var u struct {
				InstanceID      string `json:"InstanceId"`
				AssociationDate string
			}
			return json.Unmarshal([]byte(a), &u) == nil && u.InstanceID == lmusTestInst1 && u.AssociationDate == "2026-01-02"
		}},
		{TypeLicenseManagerUserSubscriptionsInstanceUser, lmusTestIU2, "bob", "ASSOCIATED", nil},
		{TypeLicenseManagerUserSubscriptionsInstanceUser, lmusTestIU3, "carol", "ASSOCIATED", nil},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			if sv(r.Name) != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			if r.Status == nil || *r.Status != c.status {
				t.Errorf("Status = %v; want %q", r.Status, c.status)
			}
			if r.CreatedAt != nil {
				t.Errorf("CreatedAt = %v; want none", *r.CreatedAt)
			}
			if sv(r.Region) != testRegion || r.AccountID != testAccountID || r.ManagedByProvider {
				t.Errorf("Region, AccountID, ManagedByProvider = %q, %q, %v; want %s, %s, false", sv(r.Region), r.AccountID, r.ManagedByProvider, testRegion, testAccountID)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the listed element", r.AttributesJSON)
			}
			if want, tagged := lmusTestTags[c.id]; tagged {
				assertTagsMap(t, r.TagsJSON, want)
			} else if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
		})
	}
}

// A region with no identity provider, endpoint or instance stores nothing,
// reads no tags and makes no per-parent listing.
func TestScanLMUS_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls lmusCalls
	h := lmusHandlers(&calls)
	h["ListIdentityProviders"] = func(any) (any, error) { return &licensemanagerusersubscriptions.ListIdentityProvidersOutput{}, nil }
	h["ListLicenseServerEndpoints"] = func(any) (any, error) {
		return &licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput{}, nil
	}
	h["ListInstances"] = func(any) (any, error) { return &licensemanagerusersubscriptions.ListInstancesOutput{}, nil }
	counted, upserts := countUpserts(st)
	total, err := lmusScan(context.Background(), counted, h)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if len(calls.subDirs.ids) != 0 || len(calls.assocs.ids) != 0 {
		t.Errorf("per-parent listings = %v, %v; want none", calls.subDirs.ids, calls.assocs.ids)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource of
// any type deleted since it was listed is dropped silently. A denial keeps
// the row untagged and warns once under the tag op with the scan's scope; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanLMUS_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"identity provider/not found", lmusTestIdpA, TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusGoneErr, -1, "", false, false},
		{"license server endpoint/not found", lmusTestLSE2, TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, lmusGoneErr, -1, "", false, false},
		{"product subscription/not found", lmusTestPS2, TypeLicenseManagerUserSubscriptionsProductSubscription, lmusGoneErr, -1, "", false, false},
		{"instance user/not found", lmusTestIU3, TypeLicenseManagerUserSubscriptionsInstanceUser, lmusGoneErr, -1, "", false, false},
		{"identity provider/denied", lmusTestIdpB, TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusDenied, -1, "", true, true},
		{"product subscription/transient", lmusTestPS1, TypeLicenseManagerUserSubscriptionsProductSubscription, lmusThrottle, outcomeTransient, "ThrottlingException", true, false},
		{"instance user/other error", lmusTestIU1, TypeLicenseManagerUserSubscriptionsInstanceUser, lmusBadInput, outcomeError, "ValidationException", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := lmusCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, err := lmusScan(context.Background(), st, lmusHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := lmusAllIDs()
			if !c.wantKept {
				want = lmusWithout(c.arn)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := lmusStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if r, ok := rowsByNativeID(t, st, c.typ)[c.arn]; ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var wantOps []string
			if c.warn {
				wantOps = []string{lmusListTagsForResourceOp}
			}
			if got := amWarnings(w); !slices.Equal(got, wantOps) {
				t.Fatalf("warnings = %v; want %v", w, wantOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A row whose ARN names another account (identity provider C, product
// subscription 3): a denied or not-found tag read keeps it untagged, with no
// warning and no error, and any other failure of its read is still an error.
// A row whose ARN names this account is this account's whatever its
// OwnerAccountId says (identity provider B): a denied read warns, and a
// not-found read drops it, as it does identity provider A.
func TestScanLMUS_OtherAccountKeptUntagged(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		errs    map[string]error
		code    string // "": no error
		dropped []string
		warn    bool
	}{
		{"identity provider/denied", map[string]error{lmusTestIdpC: lmusDenied}, "", nil, false},
		{"identity provider/not found", map[string]error{lmusTestIdpC: lmusGoneErr, lmusTestIdpA: lmusGoneErr}, "", []string{lmusTestIdpA}, false},
		{"product subscription/denied", map[string]error{lmusTestPS3: lmusDenied}, "", nil, false},
		{"product subscription/not found", map[string]error{lmusTestPS3: lmusGoneErr}, "", nil, false},
		{"identity provider/other error", map[string]error{lmusTestIdpC: lmusBadInput}, "ValidationException", nil, false},
		{"owner elsewhere, ARN here/denied", map[string]error{lmusTestIdpB: lmusDenied}, "", nil, true},
		{"owner elsewhere, ARN here/not found", map[string]error{lmusTestIdpB: lmusGoneErr}, "", []string{lmusTestIdpB}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := lmusCalls{tagErrs: c.errs}
			_, err := lmusScan(context.Background(), st, lmusHandlers(&calls))
			if c.code == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.code)
			}
			if got, want := lmusStored(t, st), lmusWithout(c.dropped...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			for arn := range c.errs {
				for _, typ := range []string{TypeLicenseManagerUserSubscriptionsIdentityProvider, TypeLicenseManagerUserSubscriptionsProductSubscription} {
					if r, ok := rowsByNativeID(t, st, typ)[arn]; ok && r.TagsJSON != nil {
						t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
					}
				}
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{lmusListTagsForResourceOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanLMUS_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := lmusCalls{tagErrs: map[string]error{lmusTestIdpA: lmusDenied, lmusTestLSE1: lmusDenied, lmusTestPS2: lmusDenied, lmusTestIU2: lmusDenied}}
	total, err := lmusScan(context.Background(), st, lmusHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != lmusTestTotal {
		t.Errorf("total = %d; want %d", total, lmusTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{lmusListTagsForResourceOp}) {
		t.Errorf("warnings = %v; want one under %s", got, lmusListTagsForResourceOp)
	}
}

// A throttle beside a hard tag error is demoted to one warning under its own
// op, so the hard error decides the outcome: a throttled tag read, a
// throttled per-parent listing, and a throttled ListInstances.
func TestScanLMUS_ThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		calls     *lmusCalls
		throttled string // a listing op every call of which is throttled
		warn      string
	}{
		{"tag read", &lmusCalls{tagErrs: map[string]error{lmusTestLSE1: lmusThrottle, lmusTestIU3: lmusBadInput}}, "", lmusListTagsForResourceOp},
		{"product subscriptions", &lmusCalls{tagErrs: map[string]error{lmusTestIU3: lmusBadInput}, subErrs: map[string]error{lmusTestDir2: lmusThrottle}}, "", lmusListProductSubscriptionsOp},
		{"instances", &lmusCalls{tagErrs: map[string]error{lmusTestLSE2: lmusBadInput}}, "ListInstances", lmusListInstancesOp},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			h := lmusHandlers(c.calls)
			if c.throttled != "" {
				h[c.throttled] = func(any) (any, error) { return nil, lmusThrottle }
			}
			_, err := lmusScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if got := amWarnings(warns()); !slices.Equal(got, []string{c.warn}) {
				t.Errorf("warnings = %v; want one under %s", got, c.warn)
			}
		})
	}
}

// A missing service-linked role on the first page of ListIdentityProviders
// reports the service disabled: nothing is stored, no other op is called and
// nothing is warned about. On a later page it is the denial it then is: the
// first page is kept, the listing warns once, and the later phases run.
func TestScanLMUS_ServiceLinkedRoleMissing(t *testing.T) {
	t.Parallel()
	t.Run("first page", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls lmusCalls
		h := lmusHandlers(&calls)
		h["ListIdentityProviders"] = func(in any) (any, error) {
			calls.listed("ListIdentityProviders", in)
			return nil, lmusNoSLR
		}
		counted, upserts := countUpserts(st)
		total, err := lmusScan(context.Background(), counted, h)
		inserted := upserts()
		if !errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want errServiceDisabled", err)
		}
		if total != 0 || inserted != 0 {
			t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
		}
		if got := calls.lists.distinct(); !slices.Equal(got, []string{"ListIdentityProviders"}) {
			t.Errorf("listings = %v; want only ListIdentityProviders", got)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	// ctx ending after the refusal is what the scan reports, not the
	// disabled sentinel.
	t.Run("first page, then ctx ends", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls lmusCalls
		h := lmusHandlers(&calls)
		h["ListIdentityProviders"] = func(any) (any, error) {
			cancel()
			return nil, lmusNoSLR
		}
		_, err := lmusScan(ctx, st, h)
		if !errors.Is(err, context.Canceled) || errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want context.Canceled, not errServiceDisabled", err)
		}
	})
	t.Run("later page", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls lmusCalls
		h := lmusHandlers(&calls)
		list := h["ListIdentityProviders"]
		h["ListIdentityProviders"] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				return nil, lmusNoSLR
			}
			return list(in)
		}
		total, err := lmusScan(context.Background(), st, h)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		want := lmusWithout(lmusTestIdpB, lmusTestIdpC, lmusTestPS3)
		if total != len(want) {
			t.Errorf("total = %d; want %d", total, len(want))
		}
		if got := lmusStored(t, st); !slices.Equal(got, want) {
			t.Errorf("stored = %v; want %v", got, want)
		}
		if got := amWarnings(warns()); !slices.Equal(got, []string{lmusListIdentityProvidersOp}) {
			t.Errorf("warnings = %v; want one under %s", got, lmusListIdentityProvidersOp)
		}
	})
}

// A listing whose second page fails keeps its first page, and the later
// phases still run: a product subscription or user whose parent was on the
// lost page is not read.
func TestScanLMUS_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		lost []string
	}{
		{"ListIdentityProviders", []string{lmusTestIdpB, lmusTestIdpC, lmusTestPS3}},
		{"ListLicenseServerEndpoints", []string{lmusTestLSE2}},
		{"ListInstances", []string{lmusTestIU3}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			var calls lmusCalls
			h := lmusHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, lmusBadInput
				}
				return list(in)
			}
			total, err := lmusScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			want := lmusWithout(tc.lost...)
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := lmusStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; its first
// page, when it was read, is kept, and the later phases still run.
func TestScanLMUS_ListDeniedWarnsAndContinues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		page int // the page denied
		lost []string
	}{
		{"ListIdentityProviders", 1, []string{lmusTestIdpA, lmusTestIdpB, lmusTestIdpC, lmusTestPS1, lmusTestPS2, lmusTestPS3}},
		{"ListLicenseServerEndpoints", 2, []string{lmusTestLSE2}},
		{"ListInstances", 1, []string{lmusTestIU1, lmusTestIU2, lmusTestIU3}},
		{"ListInstances", 2, []string{lmusTestIU3}},
	} {
		t.Run(fmt.Sprintf("%s/page %d", tc.op, tc.page), func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls lmusCalls
			h := lmusHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") == nil) == (tc.page == 1) {
					return nil, lmusDenied
				}
				return list(in)
			}
			if _, err := lmusScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := lmusStored(t, st), lmusWithout(tc.lost...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if got, want := amWarnings(warns()), []string{"license-manager-user-subscriptions:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A per-parent listing fails per parent: a parent deleted since it was listed
// (ResourceNotFoundException, modeled) is skipped silently; denied parents
// warn once for the op; a throttle is transient; any other error is an error.
// Every other parent's rows are stored, and the first page of a parent whose
// second page fails is kept.
func TestScanLMUS_PerParentListingErrors(t *testing.T) {
	t.Parallel()
	type listing struct {
		op          string
		parentA     string // the parent with two pages
		parentB     string
		rowsA, rowB []string
		set         func(c *lmusCalls, errs map[string]error)
	}
	for _, l := range []listing{
		{lmusListProductSubscriptionsOp, lmusTestDir1, lmusTestDir2, []string{lmusTestPS1, lmusTestPS2}, []string{lmusTestPS3}, func(c *lmusCalls, errs map[string]error) { c.subErrs = errs }},
		{lmusListUserAssociationsOp, lmusTestInst1, lmusTestInst2, []string{lmusTestIU1, lmusTestIU2}, []string{lmusTestIU3}, func(c *lmusCalls, errs map[string]error) { c.assocErrs = errs }},
	} {
		for _, c := range []struct {
			name    string
			errs    map[string]error
			outcome serviceOutcome // -1: no error
			code    string
			warn    bool
			lostA   bool
		}{
			{"not found", map[string]error{l.parentB: lmusGoneErr}, -1, "", false, false},
			{"denied", map[string]error{l.parentA: lmusDenied, l.parentB: lmusDenied}, -1, "", true, true},
			{"transient", map[string]error{l.parentB: lmusThrottle}, outcomeTransient, "ThrottlingException", false, false},
			{"other error", map[string]error{l.parentB: lmusBadInput}, outcomeError, "ValidationException", false, false},
		} {
			t.Run(l.op+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				var calls lmusCalls
				l.set(&calls, c.errs)
				total, err := lmusScan(context.Background(), st, lmusHandlers(&calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				lost := l.rowB
				if c.lostA {
					lost = append(slices.Clone(lost), l.rowsA...)
				}
				want := lmusWithout(lost...)
				if total != len(want) {
					t.Errorf("total = %d; want %d", total, len(want))
				}
				if got := lmusStored(t, st); !slices.Equal(got, want) {
					t.Errorf("stored = %v; want %v", got, want)
				}
				var wantOps []string
				if c.warn {
					wantOps = []string{l.op}
				}
				if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
					t.Errorf("warnings = %v; want %v", got, wantOps)
				}
			})
		}
		t.Run(l.op+"/second page fails", func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			var calls lmusCalls
			h := lmusHandlers(&calls)
			op := l.op[len("license-manager-user-subscriptions:"):]
			list := h[op]
			h[op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, lmusBadInput
				}
				return list(in)
			}
			_, err := lmusScan(context.Background(), st, h)
			assertAPICode(t, err, "ValidationException")
			if got, want := lmusStored(t, st), lmusWithout(l.rowsA[1]); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
		})
	}
}

// A failed write stops the scan in each phase: no warning about the rows it
// lost (a denied tag read among them, and, for identity providers, one listed
// without an IdentityProvider), no later listing is called, and the earlier
// phases' rows stay counted.
func TestScanLMUS_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ        string
		deniedTag  string
		unreadIdp  bool // page 1 of ListIdentityProviders adds one without an IdentityProvider
		laterLists []string
		wantTotal  int
	}{
		{TypeLicenseManagerUserSubscriptionsIdentityProvider, lmusTestIdpA, true, []string{"ListLicenseServerEndpoints", "ListProductSubscriptions", "ListInstances", "ListUserAssociations"}, 0},
		{TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, lmusTestLSE1, false, []string{"ListProductSubscriptions", "ListInstances", "ListUserAssociations"}, 3},
		{TypeLicenseManagerUserSubscriptionsProductSubscription, lmusTestPS1, false, []string{"ListInstances", "ListUserAssociations"}, 3 + 2},
		{TypeLicenseManagerUserSubscriptionsInstanceUser, lmusTestIU3, false, nil, 3 + 2 + 3},
	} {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := lmusCalls{tagErrs: map[string]error{c.deniedTag: lmusDenied}}
			h := lmusHandlers(&calls)
			if c.unreadIdp {
				list := h["ListIdentityProviders"]
				h["ListIdentityProviders"] = func(in any) (any, error) {
					out, err := list(in)
					if o, ok := out.(*licensemanagerusersubscriptions.ListIdentityProvidersOutput); ok && bacField[*string](in, "NextToken") == nil {
						o.IdentityProviderSummaries = append(o.IdentityProviderSummaries, lmustypes.IdentityProviderSummary{IdentityProviderArn: sdkaws.String(lmusTestARN("identity-provider/idp-unread"))})
					}
					return out, err
				}
			}
			total, err := lmusScan(context.Background(), st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// The account-wide listings write a page before they read the next: the
// first page's row is stored when the second page is requested.
func TestScanLMUS_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls lmusCalls
	h := lmusHandlers(&calls)
	pageOne := map[string]string{"ListIdentityProviders": lmusTestIdpA, "ListLicenseServerEndpoints": lmusTestLSE1}
	var mu sync.Mutex
	lookups := map[string]error{}
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
				mu.Lock()
				lookups[op] = err
				mu.Unlock()
			}
			return list(in)
		}
	}
	if _, err := lmusScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// later phases still run on the parents read.
func TestScanLMUS_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListIdentityProviders", "ListLicenseServerEndpoints", "ListProductSubscriptions", "ListInstances", "ListUserAssociations"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			var calls lmusCalls
			h := lmusHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				// Of the per-parent listings only directory 1's and instance
				// 1's have two pages; the others would repeat their one page.
				switch pin := in.(type) {
				case *licensemanagerusersubscriptions.ListProductSubscriptionsInput:
					if lmusDirOf(pin.IdentityProvider) != lmusTestDir1 {
						return out, err
					}
				case *licensemanagerusersubscriptions.ListUserAssociationsInput:
					if sv(pin.InstanceId) != lmusTestInst1 {
						return out, err
					}
				}
				same := sdkaws.String("same")
				switch o := out.(type) {
				case *licensemanagerusersubscriptions.ListIdentityProvidersOutput:
					o.NextToken = same
				case *licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput:
					o.NextToken = same
				case *licensemanagerusersubscriptions.ListProductSubscriptionsOutput:
					o.NextToken = same
				case *licensemanagerusersubscriptions.ListInstancesOutput:
					o.NextToken = same
				case *licensemanagerusersubscriptions.ListUserAssociationsOutput:
					o.NextToken = same
				}
				return out, err
			}
			total, err := lmusScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != lmusTestTotal {
				t.Errorf("total = %d; want %d", total, lmusTestTotal)
			}
			if got := lmusStored(t, st); !slices.Equal(got, lmusAllIDs()) {
				t.Errorf("stored = %v; want %v", got, lmusAllIDs())
			}
		})
	}
}

// A listed element without the ARN its NativeID needs is not stored, and each
// listing warns once under its op; its siblings are stored and have their
// tags read. An identity provider without an ARN still has its product
// subscriptions read; one without an IdentityProvider is stored, warned about
// once, and has none read. An instance without an InstanceId or
// IdentityProvider is warned about once and has no users read.
func TestScanLMUS_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls lmusCalls
	h := lmusHandlers(&calls)
	h["ListIdentityProviders"] = func(any) (any, error) {
		return &licensemanagerusersubscriptions.ListIdentityProvidersOutput{IdentityProviderSummaries: []lmustypes.IdentityProviderSummary{
			{IdentityProvider: lmusAD(lmusTestDir1), Product: sdkaws.String("no-arn")},
			{IdentityProviderArn: &lmusTestIdpA},
			{IdentityProviderArn: &lmusTestIdpC, IdentityProvider: lmusAD(lmusTestDir2)},
		}}, nil
	}
	h["ListLicenseServerEndpoints"] = func(any) (any, error) {
		return &licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput{LicenseServerEndpoints: []lmustypes.LicenseServerEndpoint{
			{LicenseServerEndpointId: sdkaws.String("no-arn")}, {LicenseServerEndpointArn: &lmusTestLSE1},
		}}, nil
	}
	subs := h["ListProductSubscriptions"]
	h["ListProductSubscriptions"] = func(in any) (any, error) {
		out, err := subs(in)
		if o, ok := out.(*licensemanagerusersubscriptions.ListProductSubscriptionsOutput); ok {
			o.ProductUserSummaries = append(o.ProductUserSummaries, lmustypes.ProductUserSummary{Username: sdkaws.String("orphan")})
		}
		return out, err
	}
	h["ListInstances"] = func(any) (any, error) {
		return &licensemanagerusersubscriptions.ListInstancesOutput{InstanceSummaries: []lmustypes.InstanceSummary{
			{IdentityProvider: lmusAD(lmusTestDir1)},
			{InstanceId: sdkaws.String(lmusTestInst1)},
			{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir2)},
		}}, nil
	}
	users := h["ListUserAssociations"]
	h["ListUserAssociations"] = func(in any) (any, error) {
		out, err := users(in)
		if o, ok := out.(*licensemanagerusersubscriptions.ListUserAssociationsOutput); ok {
			o.InstanceUserSummaries = append(o.InstanceUserSummaries, lmustypes.InstanceUserSummary{Username: sdkaws.String("orphan")})
		}
		return out, err
	}
	if _, err := lmusScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := lmusSorted(lmusTestIdpA, lmusTestIdpC, lmusTestLSE1, lmusTestPS1, lmusTestPS2, lmusTestPS3, lmusTestIU3)
	if got := lmusStored(t, st); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	if got := calls.subDirs.distinct(); !slices.Equal(got, []string{lmusTestDir1, lmusTestDir2}) {
		t.Errorf("ListProductSubscriptions directories = %v; want both", got)
	}
	if got := calls.assocs.distinct(); !slices.Equal(got, []string{lmusTestInst2 + "/" + lmusTestDir2}) {
		t.Errorf("ListUserAssociations inputs = %v; want only instance 2's", got)
	}
	w := warns()
	// The identity providers listing warns twice: once for the element
	// without an ARN, once for the one without an IdentityProvider.
	wantOps := []string{lmusListIdentityProvidersOp, lmusListIdentityProvidersOp, lmusListLicenseServerEndpointsOp, lmusListProductSubscriptionsOp, lmusListInstancesOp, lmusListUserAssociationsOp}
	if got := amWarnings(w); !slices.Equal(got, wantOps) {
		t.Fatalf("warnings = %v; want %v", w, wantOps)
	}
}

// An instance listed twice with the same IdentityProvider has its users
// listed once; listed again with another IdentityProvider, it has them
// listed with that one too. Another instance on the same IdentityProvider has
// its own users listed.
func TestScanLMUS_InstanceListedPerIdentityProvider(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls lmusCalls
	h := lmusHandlers(&calls)
	h["ListInstances"] = func(any) (any, error) {
		return &licensemanagerusersubscriptions.ListInstancesOutput{InstanceSummaries: []lmustypes.InstanceSummary{
			{InstanceId: sdkaws.String(lmusTestInst1), IdentityProvider: lmusAD(lmusTestDir1)},
			{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir1)},
			{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir1)},
			{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir2)},
		}}, nil
	}
	h["ListUserAssociations"] = func(in any) (any, error) {
		uin := in.(*licensemanagerusersubscriptions.ListUserAssociationsInput)
		input := sv(uin.InstanceId) + "/" + lmusDirOf(uin.IdentityProvider)
		calls.assocs.add(input)
		return &licensemanagerusersubscriptions.ListUserAssociationsOutput{InstanceUserSummaries: []lmustypes.InstanceUserSummary{
			{InstanceUserArn: sdkaws.String(lmusTestARN("instance-user/" + input)), Username: sdkaws.String("u")},
		}}, nil
	}
	if _, err := lmusScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	inputs := []string{lmusTestInst1 + "/" + lmusTestDir1, lmusTestInst2 + "/" + lmusTestDir1, lmusTestInst2 + "/" + lmusTestDir2}
	if got := lmusSorted(calls.assocs.ids...); !slices.Equal(got, inputs) {
		t.Errorf("ListUserAssociations inputs = %v; want %v", got, inputs)
	}
	var want []string
	for _, in := range inputs {
		want = append(want, lmusTestARN("instance-user/"+in))
	}
	if got := lmusStored(t, st); !slices.Equal(got, lmusSorted(append(want, lmusWithout(lmusTestIU1, lmusTestIU2, lmusTestIU3)...)...)) {
		t.Errorf("stored = %v; want every other row and instance users %v", got, want)
	}
}

// A GovCloud or China ARN of every type reaches the tag op unchanged, and the
// tags read are stored.
func TestScanLMUS_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			t.Parallel()
			arn := func(res string) string {
				return partitionARN(pf.partition, "license-manager-user-subscriptions", pf.region, res)
			}
			idp, lse, ps, iu := arn("identity-provider/idp-gov"), arn("license-server-endpoint/lse-gov"), arn("product-subscription/ps-gov"), arn("instance-user/iu-gov")
			calls := lmusCalls{tags: map[string]map[string]string{}}
			for _, a := range []string{idp, lse, ps, iu} {
				calls.tags[a] = map[string]string{"env": pf.partition}
			}
			h := lmusHandlers(&calls)
			h["ListIdentityProviders"] = func(any) (any, error) {
				return &licensemanagerusersubscriptions.ListIdentityProvidersOutput{IdentityProviderSummaries: []lmustypes.IdentityProviderSummary{
					{IdentityProviderArn: &idp, IdentityProvider: lmusAD(lmusTestDir2)},
				}}, nil
			}
			h["ListLicenseServerEndpoints"] = func(any) (any, error) {
				return &licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput{LicenseServerEndpoints: []lmustypes.LicenseServerEndpoint{{LicenseServerEndpointArn: &lse}}}, nil
			}
			h["ListProductSubscriptions"] = func(any) (any, error) {
				return &licensemanagerusersubscriptions.ListProductSubscriptionsOutput{ProductUserSummaries: []lmustypes.ProductUserSummary{{ProductUserArn: &ps}}}, nil
			}
			h["ListInstances"] = func(any) (any, error) {
				return &licensemanagerusersubscriptions.ListInstancesOutput{InstanceSummaries: []lmustypes.InstanceSummary{
					{InstanceId: sdkaws.String(lmusTestInst2), IdentityProvider: lmusAD(lmusTestDir2)},
				}}, nil
			}
			h["ListUserAssociations"] = func(any) (any, error) {
				return &licensemanagerusersubscriptions.ListUserAssociationsOutput{InstanceUserSummaries: []lmustypes.InstanceUserSummary{{InstanceUserArn: &iu}}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, lmusDenied)
			if _, err := scanLicenseManagerUserSubscriptions(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := calls.tagARNs.distinct(), lmusSorted(idp, lse, ps, iu); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, a := range map[string]string{
				TypeLicenseManagerUserSubscriptionsIdentityProvider:      idp,
				TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint: lse,
				TypeLicenseManagerUserSubscriptionsProductSubscription:   ps,
				TypeLicenseManagerUserSubscriptionsInstanceUser:          iu,
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[a].TagsJSON, map[string]string{"env": pf.partition})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanLMUS_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls lmusCalls
	h := lmusHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListLicenseServerEndpoints"]
	h["ListLicenseServerEndpoints"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, err := lmusScan(ctx, st, h)
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
	for _, op := range []string{"ListProductSubscriptions", "ListInstances", "ListUserAssociations"} {
		if slices.Contains(calls.lists.distinct(), op) {
			t.Errorf("%s called after ctx ended", op)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanLMUS_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "license-manager-user-subscriptions.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		var calls lmusCalls
		h := lmusHandlers(&calls)
		h["ListLicenseServerEndpoints"] = func(any) (any, error) { return nil, nx }
		total, err := lmusScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := lmusTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		calls := lmusCalls{tagErrs: map[string]error{lmusTestIU3: nx}}
		_, err := lmusScan(context.Background(), st, lmusHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanLicenseManagerUserSubscriptions(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

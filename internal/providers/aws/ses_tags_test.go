package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mailmanager"
	mmtypes "github.com/aws/aws-sdk-go-v2/service/mailmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/store"
)

var (
	sesTagDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: ses:List")
	sesTagBadInput = apiErr("BadRequestException", "bad")
	sesTagThrottle = apiErr("TooManyRequestsException", "slow down")
	sesTagGone     = apiErr("NotFoundException", "gone")
	mmTagGone      = apiErr("ResourceNotFoundException", "gone")
)

// sesNames is the two resources each listing of the fixture serves,
// "tagged" on its first page and "bare" on its second; every tag source
// answers with tags for the "tagged" resource of every type.
var sesNames = []string{"tagged", "bare"}

// sesFixtureRow is one fixture row: its type, NativeID and the tag op that
// reads it ("" for a type whose tags are not read, sesGetIdentityOp or
// sesGetConfigSetOp for a type whose Get body carries them).
type sesFixtureRow struct{ typ, arn, tagOp string }

func sesTestARN(kind, name string) string {
	return sesARN("aws", testRegion, testAccountID, kind, name)
}

func mmTestARN(kind, name string) string { return mmARN("aws", testRegion, testAccountID, kind, name) }

// sesElementARN is the ARN a listed element carries for kind.
func sesElementARN(kind, name string) string {
	return "arn:aws:ses:" + testRegion + ":" + testAccountID + ":" + kind + "/" + name
}

func sesTenantARN(name string) string { return sesElementARN("tenant", name+"/id-"+name) }

// sesFixtureRows is every row the fixture stores for name.
func sesFixtureRows(name string) []sesFixtureRow {
	return []sesFixtureRow{
		{TypeSESEmailIdentity, sesTestARN("identity", name), sesGetIdentityOp},
		{TypeSESConfigurationSet, sesTestARN("configuration-set", name), sesGetConfigSetOp},
		{TypeSESContactList, sesTestARN("contact-list", name), sesListTagsOp},
		{TypeSESCustomVerificationEmailTemplate, sesTestARN("custom-verification-email-template", name), sesListTagsOp},
		{TypeSESDedicatedIPPool, sesTestARN("dedicated-ip-pool", name), sesListTagsOp},
		{TypeSESTemplate, sesTestARN("template", name), sesListTagsOp},
		{TypeSESTenant, sesTenantARN(name), sesListTagsOp},
		{TypeSESMailManagerAddonInstance, sesElementARN("addon-instance", name), mmListTagsOp},
		{TypeSESMailManagerAddonSubscription, sesElementARN("addon-subscription", name), mmListTagsOp},
		{TypeSESMailManagerAddressList, sesElementARN("mailmanager-address-list", name), mmListTagsOp},
		{TypeSESMailManagerArchive, mmTestARN("archive", name), mmListTagsOp},
		{TypeSESMailManagerIngressPoint, mmTestARN("ingress-point", name), mmListTagsOp},
		{TypeSESMailManagerRelay, mmTestARN("smtp-relay", name), mmListTagsOp},
		{TypeSESMailManagerRuleSet, mmTestARN("rule-set", name), mmListTagsOp},
		{TypeSESMailManagerTrafficPolicy, mmTestARN("traffic-policy", name), mmListTagsOp},
		{TypeSESMultiRegionEndpoint, sesTestARN("multi-region-endpoint", "id-"+name), ""},
		{TypeSESConfigurationSetEventDestination, sesTestARN("configuration-set", name) + "/event-destination/dest", ""},
	}
}

func sesAllFixtureRows() []sesFixtureRow {
	var rows []sesFixtureRow
	for _, n := range sesNames {
		rows = append(rows, sesFixtureRows(n)...)
	}
	return rows
}

// sesCalls records the stubbed calls: how often each op was called, the
// PageSize each listing sent, and the ARNs each tag op was sent. tags holds
// the tags every tag source answers with for a resource (by ARN), errs the
// error a tag read of an ARN fails with, getErrs the error a Get of a name
// fails with (keyed by op and name).
type sesCalls struct {
	mu        sync.Mutex
	ops       map[string]int
	pageSizes map[string][]*int32
	tagARNs   map[string][]string
	tags      map[string][]sesv2types.Tag
	errs      map[string]error
	getErrs   map[string]error
}

func newSESCalls() *sesCalls {
	c := &sesCalls{
		ops: map[string]int{}, pageSizes: map[string][]*int32{}, tagARNs: map[string][]string{},
		tags: map[string][]sesv2types.Tag{}, errs: map[string]error{}, getErrs: map[string]error{},
	}
	for _, r := range sesFixtureRows("tagged") {
		c.tags[r.arn] = []sesv2types.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(r.typ)}}
	}
	return c
}

func (c *sesCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// twoPages answers op with "tagged" on the first page and "bare" on the
// second, recording each call and its PageSize.
func (c *sesCalls) twoPages(op string, out func(name string, next *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.mu.Lock()
		c.ops[op]++
		c.pageSizes[op] = append(c.pageSizes[op], bacField[*int32](in, "PageSize"))
		c.mu.Unlock()
		if bacField[*string](in, "NextToken") == nil {
			return out("tagged", sdkaws.String("p2")), nil
		}
		return out("bare", nil), nil
	}
}

// get answers op for name with out, or fails as getErrs says.
func (c *sesCalls) get(op, name string, out func() any) (any, error) {
	c.mu.Lock()
	c.ops[op]++
	err := c.getErrs[op+" "+name]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return out(), nil
}

func (c *sesCalls) listTags(in any) (any, error) {
	var op, arn string
	switch p := in.(type) {
	case *sesv2.ListTagsForResourceInput:
		op, arn = sesListTagsOp, sv(p.ResourceArn)
	case *mailmanager.ListTagsForResourceInput:
		op, arn = mmListTagsOp, sv(p.ResourceArn)
	}
	c.mu.Lock()
	c.tagARNs[op] = append(c.tagARNs[op], arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if op == sesListTagsOp {
		return &sesv2.ListTagsForResourceOutput{Tags: tags}, nil
	}
	var mmTags []mmtypes.Tag
	for _, t := range tags {
		mmTags = append(mmTags, mmtypes.Tag{Key: t.Key, Value: t.Value})
	}
	return &mailmanager.ListTagsForResourceOutput{Tags: mmTags}, nil
}

// sesHandlers answers every op the fixture rows need; every other op (the
// receipt listings, GetAccount, the certificate listing) is denied.
func sesHandlers(c *sesCalls) map[string]func(any) (any, error) {
	s := sdkaws.String
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListEmailIdentities": c.twoPages("ListEmailIdentities", func(n string, next *string) any {
			return &sesv2.ListEmailIdentitiesOutput{EmailIdentities: []sesv2types.IdentityInfo{{
				IdentityName: s(n), IdentityType: sesv2types.IdentityTypeDomain, SendingEnabled: true, VerificationStatus: sesv2types.VerificationStatusSuccess,
			}}, NextToken: next}
		}),
		"GetEmailIdentity": func(in any) (any, error) {
			name := sv(in.(*sesv2.GetEmailIdentityInput).EmailIdentity)
			return c.get("GetEmailIdentity", name, func() any {
				return &sesv2.GetEmailIdentityOutput{
					IdentityType: sesv2types.IdentityTypeDomain, ConfigurationSetName: s("cs"),
					Tags: c.tags[sesTestARN("identity", name)],
				}
			})
		},
		"ListConfigurationSets": c.twoPages("ListConfigurationSets", func(n string, next *string) any {
			return &sesv2.ListConfigurationSetsOutput{ConfigurationSets: []string{n}, NextToken: next}
		}),
		"GetConfigurationSet": func(in any) (any, error) {
			name := sv(in.(*sesv2.GetConfigurationSetInput).ConfigurationSetName)
			return c.get("GetConfigurationSet", name, func() any {
				return &sesv2.GetConfigurationSetOutput{ConfigurationSetName: s(name), Tags: c.tags[sesTestARN("configuration-set", name)]}
			})
		},
		"GetConfigurationSetEventDestinations": func(in any) (any, error) {
			name := sv(in.(*sesv2.GetConfigurationSetEventDestinationsInput).ConfigurationSetName)
			return c.get("GetConfigurationSetEventDestinations", name, func() any {
				return &sesv2.GetConfigurationSetEventDestinationsOutput{EventDestinations: []sesv2types.EventDestination{{Name: s("dest")}}}
			})
		},
		"ListContactLists": c.twoPages("ListContactLists", func(n string, next *string) any {
			return &sesv2.ListContactListsOutput{ContactLists: []sesv2types.ContactList{{ContactListName: s(n)}}, NextToken: next}
		}),
		"ListCustomVerificationEmailTemplates": c.twoPages("ListCustomVerificationEmailTemplates", func(n string, next *string) any {
			return &sesv2.ListCustomVerificationEmailTemplatesOutput{CustomVerificationEmailTemplates: []sesv2types.CustomVerificationEmailTemplateMetadata{{TemplateName: s(n)}}, NextToken: next}
		}),
		"ListDedicatedIpPools": c.twoPages("ListDedicatedIpPools", func(n string, next *string) any {
			return &sesv2.ListDedicatedIpPoolsOutput{DedicatedIpPools: []string{n}, NextToken: next}
		}),
		"ListMultiRegionEndpoints": c.twoPages("ListMultiRegionEndpoints", func(n string, next *string) any {
			return &sesv2.ListMultiRegionEndpointsOutput{MultiRegionEndpoints: []sesv2types.MultiRegionEndpoint{{EndpointId: s("id-" + n), EndpointName: s(n)}}, NextToken: next}
		}),
		"ListEmailTemplates": c.twoPages("ListEmailTemplates", func(n string, next *string) any {
			return &sesv2.ListEmailTemplatesOutput{TemplatesMetadata: []sesv2types.EmailTemplateMetadata{{TemplateName: s(n)}}, NextToken: next}
		}),
		"ListTenants": c.twoPages("ListTenants", func(n string, next *string) any {
			return &sesv2.ListTenantsOutput{Tenants: []sesv2types.TenantInfo{{TenantArn: s(sesTenantARN(n)), TenantName: s(n), TenantId: s("id-" + n)}}, NextToken: next}
		}),
		"ListAddonInstances": c.twoPages("ListAddonInstances", func(n string, next *string) any {
			return &mailmanager.ListAddonInstancesOutput{AddonInstances: []mmtypes.AddonInstance{{AddonInstanceArn: s(sesElementARN("addon-instance", n)), AddonInstanceId: s(n)}}, NextToken: next}
		}),
		"ListAddonSubscriptions": c.twoPages("ListAddonSubscriptions", func(n string, next *string) any {
			return &mailmanager.ListAddonSubscriptionsOutput{AddonSubscriptions: []mmtypes.AddonSubscription{{AddonSubscriptionArn: s(sesElementARN("addon-subscription", n)), AddonSubscriptionId: s(n)}}, NextToken: next}
		}),
		"ListAddressLists": c.twoPages("ListAddressLists", func(n string, next *string) any {
			return &mailmanager.ListAddressListsOutput{AddressLists: []mmtypes.AddressList{{AddressListArn: s(sesElementARN("mailmanager-address-list", n)), AddressListId: s(n)}}, NextToken: next}
		}),
		"ListArchives": c.twoPages("ListArchives", func(n string, next *string) any {
			return &mailmanager.ListArchivesOutput{Archives: []mmtypes.Archive{{ArchiveId: s(n), ArchiveState: mmtypes.ArchiveStateActive}}, NextToken: next}
		}),
		"GetArchive": func(in any) (any, error) {
			id := sv(in.(*mailmanager.GetArchiveInput).ArchiveId)
			return c.get("GetArchive", id, func() any {
				return &mailmanager.GetArchiveOutput{ArchiveId: s(id), KmsKeyArn: s("arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k")}
			})
		},
		"ListIngressPoints": c.twoPages("ListIngressPoints", func(n string, next *string) any {
			return &mailmanager.ListIngressPointsOutput{IngressPoints: []mmtypes.IngressPoint{{IngressPointId: s(n)}}, NextToken: next}
		}),
		"GetIngressPoint": func(in any) (any, error) {
			id := sv(in.(*mailmanager.GetIngressPointInput).IngressPointId)
			return c.get("GetIngressPoint", id, func() any {
				return &mailmanager.GetIngressPointOutput{IngressPointId: s(id), RuleSetId: s("rs")}
			})
		},
		"ListRelays": c.twoPages("ListRelays", func(n string, next *string) any {
			return &mailmanager.ListRelaysOutput{Relays: []mmtypes.Relay{{RelayId: s(n)}}, NextToken: next}
		}),
		"ListRuleSets": c.twoPages("ListRuleSets", func(n string, next *string) any {
			return &mailmanager.ListRuleSetsOutput{RuleSets: []mmtypes.RuleSet{{RuleSetId: s(n)}}, NextToken: next}
		}),
		"ListTrafficPolicies": c.twoPages("ListTrafficPolicies", func(n string, next *string) any {
			return &mailmanager.ListTrafficPoliciesOutput{TrafficPolicies: []mmtypes.TrafficPolicy{{TrafficPolicyId: s(n)}}, NextToken: next}
		}),
	}
}

// sesTagScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func sesTagScan(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, sesTagDenied)
	return scanSES(context.Background(), acct, testRegion, st, testScanID)
}

func sesRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

func sesAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %s: %v", r.NativeID, r.AttributesJSON, err)
	}
	return attrs
}

// Every type the tag reads cover stores the tags its source answers with,
// and nil for a resource without tags: the identity and configuration set
// from their Get body, the others from their service's ListTagsForResource,
// which is sent exactly those rows' ARNs. The rows of the other types store
// no tags. Both pages of every listing are stored, every listing sends the
// PageSize cap its model documents and none where it documents none, the
// event destinations are read for the stored configuration sets without
// listing them again, and no tag read or Get warns.
func TestScanSES_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	var warns []*int
	for _, op := range []string{sesListTagsOp, mmListTagsOp, sesGetIdentityOp, sesGetConfigSetOp, "mailmanager:GetArchive", "mailmanager:GetIngressPoint"} {
		warns = append(warns, countOpWarnings(st, op))
	}
	calls := newSESCalls()

	total, inserted, err := sesTagScan(t, st, sesHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := len(sesAllFixtureRows())
	if total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	for _, w := range warns {
		if *w != 0 {
			t.Errorf("tag or Get warnings = %d; want 0", *w)
		}
	}
	wantARNs := map[string][]string{}
	for _, w := range sesAllFixtureRows() {
		if w.tagOp == sesListTagsOp || w.tagOp == mmListTagsOp {
			wantARNs[w.tagOp] = append(wantARNs[w.tagOp], w.arn)
		}
		r, ok := sesRowOf(t, st, w.typ, w.arn)
		switch {
		case !ok:
			t.Errorf("%s %s missing", w.typ, w.arn)
		case w.tagOp != "" && calls.tags[w.arn] != nil:
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		case r.TagsJSON != nil:
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	for _, op := range []string{sesListTagsOp, mmListTagsOp} {
		got := slices.Sorted(slices.Values(calls.tagARNs[op]))
		if want := slices.Sorted(slices.Values(wantARNs[op])); !slices.Equal(got, want) {
			t.Errorf("%s ARNs = %v\nwant %v", op, got, want)
		}
	}
	for op, want := range map[string]int32{
		"ListEmailIdentities": 1000, "ListCustomVerificationEmailTemplates": 50, "ListEmailTemplates": 100, "ListMultiRegionEndpoints": 1000,
		"ListAddonInstances": 50, "ListAddonSubscriptions": 50, "ListAddressLists": 50, "ListArchives": 50,
		"ListIngressPoints": 50, "ListRuleSets": 50, "ListTrafficPolicies": 50,
	} {
		sizes := calls.pageSizes[op]
		if len(sizes) != 2 {
			t.Errorf("%s pages = %d; want 2", op, len(sizes))
		}
		for _, n := range sizes {
			if n == nil || *n != want {
				t.Errorf("%s PageSize = %v; want %d", op, n, want)
			}
		}
	}
	for _, op := range []string{"ListConfigurationSets", "ListContactLists", "ListDedicatedIpPools", "ListTenants", "ListRelays"} {
		for _, n := range calls.pageSizes[op] {
			if n != nil {
				t.Errorf("%s PageSize = %d; want none, its model documents no cap", op, *n)
			}
		}
	}
	if n := calls.count("ListConfigurationSets"); n != 2 {
		t.Errorf("ListConfigurationSets called %d times; want 2, one listing of two pages", n)
	}
	identity, _ := sesRowOf(t, st, TypeSESEmailIdentity, sesTestARN("identity", "tagged"))
	if attrs := sesAttrs(t, identity); attrs["ConfigurationSetName"] != "cs" || sv(identity.Status) != "SUCCESS" {
		t.Errorf("identity attrs ConfigurationSetName = %v, Status = %q; want the Get body and the listed status", attrs["ConfigurationSetName"], sv(identity.Status))
	}
	archive, _ := sesRowOf(t, st, TypeSESMailManagerArchive, mmTestARN("archive", "tagged"))
	if attrs := sesAttrs(t, archive); attrs["KmsKeyArn"] == nil {
		t.Errorf("archive attrs = %v; want the GetArchive body", attrs)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanSES_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newSESCalls()
	if _, _, err := sesTagScan(t, st, sesHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := sesTagScan(t, st, sesHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if want := len(sesAllFixtureRows()); total != want || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, want)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under the tag op, scoped to the region; a throttle
// after retries is a transient error; any other code a hard one; the op's
// modeled not-found code drops the row silently.
func TestScanSES_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		row  sesFixtureRow
		gone error
	}{
		{sesFixtureRow{TypeSESContactList, sesTestARN("contact-list", "tagged"), sesListTagsOp}, sesTagGone},
		{sesFixtureRow{TypeSESMailManagerTrafficPolicy, mmTestARN("traffic-policy", "tagged"), mmListTagsOp}, mmTagGone},
	} {
		t.Run(target.row.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", sesTagDenied, true, 0, 1},
				{"not found", target.gone, false, 0, 0},
				{"other error", sesTagBadInput, true, outcomeError, 0},
				{"transient", sesTagThrottle, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.row.tagOp)
					calls := newSESCalls()
					calls.errs[target.row.arn] = c.err

					_, _, err := sesTagScan(t, st, sesHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					r, ok := sesRowOf(t, st, target.row.typ, target.row.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.row.tagOp, *warnings, c.wantWarns)
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

// A failed Get keeps the list element as the row unless the resource is
// gone: a denial warns once under the Get op, a throttle is a transient
// error, any other code a hard one. A kept row of a type whose tags come
// from the Get body has them read with ListTagsForResource instead.
func TestScanSES_GetFailuresKeepTheListElement(t *testing.T) {
	for _, target := range []struct {
		row       sesFixtureRow
		getOp     string
		stub      string
		gone      error
		listed    func(map[string]any) bool
		tagsAfter bool
	}{
		{
			sesFixtureRow{TypeSESEmailIdentity, sesTestARN("identity", "tagged"), sesListTagsOp},
			sesGetIdentityOp, "GetEmailIdentity", sesTagGone,
			func(a map[string]any) bool { return a["SendingEnabled"] == true && a["ConfigurationSetName"] == nil }, true,
		},
		{
			sesFixtureRow{TypeSESConfigurationSet, sesTestARN("configuration-set", "tagged"), sesListTagsOp},
			sesGetConfigSetOp, "GetConfigurationSet", sesTagGone,
			func(a map[string]any) bool { return len(a) == 1 && a["ConfigurationSetName"] == "tagged" }, true,
		},
		{
			sesFixtureRow{TypeSESMailManagerArchive, mmTestARN("archive", "tagged"), mmListTagsOp},
			"mailmanager:GetArchive", "GetArchive", mmTagGone,
			func(a map[string]any) bool { return a["ArchiveState"] == "ACTIVE" && a["KmsKeyArn"] == nil }, true,
		},
		{
			sesFixtureRow{TypeSESMailManagerIngressPoint, mmTestARN("ingress-point", "tagged"), mmListTagsOp},
			"mailmanager:GetIngressPoint", "GetIngressPoint", mmTagGone,
			func(a map[string]any) bool { return a["RuleSetId"] == nil }, true,
		},
	} {
		t.Run(target.row.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", sesTagDenied, true, 0, 1},
				{"not found", target.gone, false, 0, 0},
				{"other error", sesTagBadInput, true, outcomeError, 0},
				{"transient", sesTagThrottle, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.getOp)
					calls := newSESCalls()
					calls.getErrs[target.stub+" tagged"] = c.err

					_, _, err := sesTagScan(t, st, sesHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					r, ok := sesRowOf(t, st, target.row.typ, target.row.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if len(*warnings) != c.wantWarns {
						t.Errorf("%s warnings = %v; want %d", target.getOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
					if !ok {
						if slices.Contains(calls.tagARNs[target.row.tagOp], target.row.arn) {
							t.Errorf("%s sent %s, a resource the Get no longer finds", target.row.tagOp, target.row.arn)
						}
						return
					}
					if attrs := sesAttrs(t, r); !target.listed(attrs) {
						t.Errorf("attrs = %v; want the list element", attrs)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": target.row.typ})
					if !slices.Contains(calls.tagARNs[target.row.tagOp], target.row.arn) {
						t.Errorf("%s ARNs = %v; want %s, whose Get failed", target.row.tagOp, calls.tagARNs[target.row.tagOp], target.row.arn)
					}
				})
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSES_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sesListTagsOp)
	calls := newSESCalls()
	calls.errs[sesTestARN("contact-list", "tagged")] = sesTagThrottle
	calls.errs[sesTestARN("contact-list", "bare")] = sesTagBadInput

	_, _, err := sesTagScan(t, st, sesHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", sesListTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanSES_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeSESContactList)
	calls := newSESCalls()
	calls.errs[sesTestARN("contact-list", "tagged")] = sesTagDenied

	_, _, err := sesTagScan(t, st, sesHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListCustomVerificationEmailTemplates"); n != 0 {
		t.Errorf("ListCustomVerificationEmailTemplates called %d times after the failed write", n)
	}
}

// A failed write is returned with the Get errors of the rows it lost, not
// warned about or dropped: both reach the dispatcher in one error.
func TestScanSES_FailedWriteKeepsGetError(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeSESEmailIdentity)
	calls := newSESCalls()
	calls.getErrs["GetEmailIdentity tagged"] = sesTagBadInput

	_, _, err := sesTagScan(t, st, sesHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "BadRequestException")
}

// A listing's failed page keeps the pages read before it, and no phase's
// failure stops the phases after it, on either host.
func TestScanSES_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newSESCalls()
	handlers := sesHandlers(calls)
	handlers["ListContactLists"] = failOnCall(2, apiErr("BadRequestException", "page 2"), handlers["ListContactLists"])
	handlers["ListAddonInstances"] = func(any) (any, error) { return nil, apiErr("ValidationException", "instances") }

	_, _, err := sesTagScan(t, st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	if _, ok := sesRowOf(t, st, TypeSESContactList, sesTestARN("contact-list", "tagged")); !ok {
		t.Error("contact list of the first page missing")
	}
	if _, ok := sesRowOf(t, st, TypeSESContactList, sesTestARN("contact-list", "bare")); ok {
		t.Error("contact list of the failed page stored")
	}
	for _, w := range sesAllFixtureRows() {
		if w.typ == TypeSESContactList || w.typ == TypeSESMailManagerAddonInstance {
			continue
		}
		if _, ok := sesRowOf(t, st, w.typ, w.arn); !ok {
			t.Errorf("%s %s missing after the earlier failures", w.typ, w.arn)
		}
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanSES_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newSESCalls()
	handlers := sesHandlers(calls)
	handlers["ListEmailTemplates"] = func(any) (any, error) {
		return &sesv2.ListEmailTemplatesOutput{TemplatesMetadata: []sesv2types.EmailTemplateMetadata{{TemplateName: sdkaws.String("tagged")}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := sesTagScan(t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := sesRowOf(t, st, TypeSESTemplate, sesTestARN("template", "tagged")); !ok {
		t.Error("template of the pages read missing")
	}
}

// Listed items without a name, id or ARN are warned about once per op,
// naming a key that is never empty; their siblings are stored.
func TestScanSES_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	warnings := map[string]*[]store.ScanWarning{}
	for _, op := range []string{"sesv2:ListContactLists", "sesv2:ListTenants", "mailmanager:ListRelays"} {
		warnings[op] = bacWarnings(st, op)
	}
	calls := newSESCalls()
	handlers := sesHandlers(calls)
	s := sdkaws.String
	handlers["ListContactLists"] = func(any) (any, error) {
		return &sesv2.ListContactListsOutput{ContactLists: []sesv2types.ContactList{{}, {ContactListName: s("tagged")}}}, nil
	}
	handlers["ListTenants"] = func(any) (any, error) {
		return &sesv2.ListTenantsOutput{Tenants: []sesv2types.TenantInfo{{TenantName: s("no-arn")}, {TenantArn: s(sesTenantARN("tagged")), TenantName: s("tagged")}}}, nil
	}
	handlers["ListRelays"] = func(any) (any, error) {
		return &mailmanager.ListRelaysOutput{Relays: []mmtypes.Relay{{RelayName: s("no-id")}, {RelayId: s("tagged")}}}, nil
	}

	if _, _, err := sesTagScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, got := range warnings {
		if len(*got) != 1 {
			t.Errorf("%s warnings = %v; want 1", op, *got)
		}
	}
	for _, w := range []sesFixtureRow{
		{TypeSESContactList, sesTestARN("contact-list", "tagged"), ""},
		{TypeSESTenant, sesTenantARN("tagged"), ""},
		{TypeSESMailManagerRelay, mmTestARN("smtp-relay", "tagged"), ""},
	} {
		if _, ok := sesRowOf(t, st, w.typ, w.arn); !ok {
			t.Errorf("%s %s missing beside the unstorable item", w.typ, w.arn)
		}
	}
}

// A region without Mail Manager (NXDOMAIN on its first listing, nothing
// stored) skips its listings silently and keeps the SES rows; one without
// the email endpoint is reported unavailable; an NXDOMAIN from Mail Manager
// after it stored rows is a failure of this scan.
func TestScanSES_NXDOMAIN(t *testing.T) {
	nx := func(host string) error {
		return &net.DNSError{Err: "no such host", Name: host + "." + testRegion + ".amazonaws.com", IsNotFound: true}
	}
	mmOps := []string{"ListAddonInstances", "ListAddonSubscriptions", "ListAddressLists", "ListArchives", "ListIngressPoints", "ListRelays", "ListRuleSets", "ListTrafficPolicies"}

	t.Run("no mail manager", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSESCalls()
		handlers := sesHandlers(calls)
		for _, op := range mmOps {
			handlers[op] = func(any) (any, error) { return nil, nx("mail-manager") }
		}
		total, _, err := sesTagScan(t, st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total == 0 {
			t.Error("total = 0; want the SES rows counted")
		}
		if _, ok := sesRowOf(t, st, TypeSESContactList, sesTestARN("contact-list", "tagged")); !ok {
			t.Error("SES rows missing")
		}
		if n := calls.count("ListAddonSubscriptions"); n != 0 {
			t.Errorf("ListAddonSubscriptions called %d times; want 0 once the host is gone", n)
		}
	})
	t.Run("no email endpoint", func(t *testing.T) {
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx("email"))
		_, _, err := scanSES(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
	})
	// The first Mail Manager listing stores its first page before its second
	// page NXDOMAINs: the host resolved, so the later Mail Manager phases
	// still run and the NXDOMAIN is a failure of this scan.
	t.Run("mail manager first phase page 2", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSESCalls()
		handlers := sesHandlers(calls)
		handlers["ListAddonInstances"] = failOnCall(2, nx("mail-manager"), handlers["ListAddonInstances"])
		_, _, err := sesTagScan(t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if _, ok := sesRowOf(t, st, TypeSESMailManagerAddonInstance, sesElementARN("addon-instance", "tagged")); !ok {
			t.Error("add-on instance of the first page missing")
		}
		if n := calls.count("ListAddonSubscriptions"); n != 2 {
			t.Errorf("ListAddonSubscriptions called %d times; want 2, the host resolved", n)
		}
	})
	t.Run("late mail manager", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSESCalls()
		handlers := sesHandlers(calls)
		handlers["ListRuleSets"] = func(any) (any, error) { return nil, nx("mail-manager") }
		_, _, err := sesTagScan(t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if _, ok := sesRowOf(t, st, TypeSESMailManagerTrafficPolicy, mmTestARN("traffic-policy", "tagged")); !ok {
			t.Error("traffic policy, listed after the failure, missing")
		}
	})
}

// A configuration set deleted since it was listed has no event destinations
// and is skipped silently; its sibling's are stored.
func TestScanSES_EventDestinationsOfDeletedSetSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sesEventDestinationsOp)
	calls := newSESCalls()
	calls.getErrs["GetConfigurationSetEventDestinations tagged"] = sesTagGone

	if _, _, err := sesTagScan(t, st, sesHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSESConfigurationSetEventDestination)
	if got, want := nativeIDs(rows), []string{sesTestARN("configuration-set", "bare") + "/event-destination/dest"}; !slices.Equal(got, want) {
		t.Errorf("event destinations = %v; want %v", got, want)
	}
	if *warns != 0 {
		t.Errorf("%s warnings = %d; want 0", sesEventDestinationsOp, *warns)
	}
}

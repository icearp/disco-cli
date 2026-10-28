package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
	vltypes "github.com/aws/aws-sdk-go-v2/service/vpclattice/types"
	"github.com/icearp/disco-cli/store"
)

// vltNames is the two resources each account-wide listing of the fixture
// serves, "t" (tagged) on its first page and "b" (bare) on its second; the
// second page of service networks also carries sn-x, shared in from
// vltForeignAccount. ListTagsForResource answers with tags for every ARN
// ending in "-t" and for sn-x.
var vltNames = []string{"t", "b"}

// vltForeignAccount owns the service network shared in to the account.
const vltForeignAccount = "210987654321"

func vltARN(res string) string { return arnFor("aws", "vpc-lattice", testRegion, testAccountID, res) }

// vltGovGateway is a resource gateway the account owns in aws-us-gov.
func vltGovGateway() string {
	return arnFor("aws-us-gov", "vpc-lattice", "us-gov-west-1", testAccountID, "resourcegateway/rgw-gov-t")
}

func vltSharedNet() string {
	return arnFor("aws", "vpc-lattice", testRegion, vltForeignAccount, "servicenetwork/sn-x")
}

func vltTagged(arn string) bool { return strings.HasSuffix(arn, "-t") }

// vltLast is the last path segment of an ARN, the id the fixture's children
// are named after.
func vltLast(arn string) string { return arn[strings.LastIndex(arn, "/")+1:] }

func vltListener(svcARN string) string {
	return svcARN + "/listener/listener-" + strings.TrimPrefix(vltLast(svcARN), "svc-")
}

func vltRule(listenerARN string) string {
	return listenerARN + "/rule/rule-" + strings.TrimPrefix(vltLast(listenerARN), "listener-")
}

type vltRow struct{ typ, arn string }

// vltRows is every row the fixture stores, the shared network included.
func vltRows() []vltRow {
	var rows []vltRow
	nets := []string{vltARN("servicenetwork/sn-t"), vltARN("servicenetwork/sn-b"), vltSharedNet()}
	for _, n := range vltNames {
		svc := vltARN("service/svc-" + n)
		rows = append(rows,
			vltRow{TypeVpcLatticeService, svc},
			vltRow{TypeVpcLatticeListener, vltListener(svc)},
			vltRow{TypeVpcLatticeRule, vltRule(vltListener(svc))},
			vltRow{TypeVpcLatticeAccessLogSubscription, vltARN("accesslogsubscription/als-svc-" + n)},
			vltRow{TypeVpcLatticeTargetGroup, vltARN("targetgroup/tg-" + n)},
			vltRow{TypeVpcLatticeDomainVerification, vltARN("domainverification/dv-" + n)},
			vltRow{TypeVpcLatticeResourceConfiguration, vltARN("resourceconfiguration/rcfg-" + n)},
			vltRow{TypeVpcLatticeResourceEndpointAssociation, vltARN("resourceendpointassociation/rea-rcfg-" + n)},
			vltRow{TypeVpcLatticeResourceGateway, vltARN("resourcegateway/rgw-" + n)},
			vltRow{TypeVpcLatticeServiceNetworkResourceAssociation, vltARN("servicenetworkresourceassociation/snra-" + n)})
	}
	for _, net := range nets {
		rows = append(rows,
			vltRow{TypeVpcLatticeServiceNetwork, net},
			vltRow{TypeVpcLatticeAccessLogSubscription, vltARN("accesslogsubscription/als-" + vltLast(net))},
			vltRow{TypeVpcLatticeServiceNetworkServiceAssociation, vltARN("servicenetworkserviceassociation/snsa-" + vltLast(net))},
			vltRow{TypeVpcLatticeServiceNetworkVpcAssociation, vltARN("servicenetworkvpcassociation/snva-" + vltLast(net))})
	}
	return append(rows, vltRow{TypeVpcLatticeResourceGateway, vltGovGateway()})
}

// vltCalls records the stubbed calls (bacCalls), the identifiers each
// per-parent op is sent, and answers GetService from getErrs, keyed by the
// service ARN.
type vltCalls struct {
	*bacCalls
	parents map[string][]string
	getErrs map[string]error
}

func newVLTCalls() *vltCalls {
	c := &vltCalls{bacCalls: newBACCalls(), parents: map[string][]string{}, getErrs: map[string]error{}}
	for _, r := range vltRows() {
		if (vltTagged(r.arn) || r.arn == vltSharedNet()) && r.typ != TypeVpcLatticeDomainVerification {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *vltCalls) listTags(in any) (any, error) {
	arn := sv(in.(*vpclattice.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &vpclattice.ListTagsForResourceOutput{Tags: tags}, nil
}

// child answers a per-parent op with out(parent), one page, recording the
// parent.
func (c *vltCalls) child(op string, parent func(in any) string, out func(parent string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		p := parent(in)
		c.mu.Lock()
		c.parents[op] = append(c.parents[op], p)
		c.mu.Unlock()
		return out(p), nil
	}
}

func (c *vltCalls) parentsOf(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.parents[op])
	slices.Sort(got)
	return got
}

// twoPages answers op with "t" on the first page and "b" on the second.
func (c *vltCalls) twoPages(op string, out func(name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out("t", sdkaws.String("p2"))
		}
		return out("b", nil)
	})
}

// vltHandlers answers every op the scan calls with the fixture (vltRows).
func vltHandlers(c *vltCalls) map[string]func(any) (any, error) {
	s := sdkaws.String
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListServices": c.twoPages("ListServices", func(n string, next *string) any {
			return &vpclattice.ListServicesOutput{Items: []vltypes.ServiceSummary{{Arn: s(vltARN("service/svc-" + n)), Id: s("svc-" + n), Name: s("service " + n)}}, NextToken: next}
		}),
		"GetService": func(in any) (any, error) {
			c.called("GetService", in)
			id := sv(in.(*vpclattice.GetServiceInput).ServiceIdentifier)
			c.mu.Lock()
			err := c.getErrs[id]
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return &vpclattice.GetServiceOutput{Arn: s(id), Name: s("service"), CustomDomainName: s("described.example.com")}, nil
		},
		"ListServiceNetworks": c.twoPages("ListServiceNetworks", func(n string, next *string) any {
			items := []vltypes.ServiceNetworkSummary{{Arn: s(vltARN("servicenetwork/sn-" + n)), Id: s("sn-" + n), Name: s("network " + n)}}
			if n == "b" {
				items = append(items, vltypes.ServiceNetworkSummary{Arn: s(vltSharedNet()), Name: s("shared")})
			}
			return &vpclattice.ListServiceNetworksOutput{Items: items, NextToken: next}
		}),
		"ListListeners": c.child("ListListeners", func(in any) string { return sv(in.(*vpclattice.ListListenersInput).ServiceIdentifier) },
			func(svc string) any {
				return &vpclattice.ListListenersOutput{Items: []vltypes.ListenerSummary{{Arn: s(vltListener(svc)), Id: s("listener-id")}}}
			}),
		"ListRules": c.child("ListRules", func(in any) string {
			p := in.(*vpclattice.ListRulesInput)
			return sv(p.ServiceIdentifier) + " " + sv(p.ListenerIdentifier)
		}, func(pair string) any {
			_, listener, _ := strings.Cut(pair, " ")
			return &vpclattice.ListRulesOutput{Items: []vltypes.RuleSummary{{Arn: s(vltRule(listener)), Name: s("rule")}}}
		}),
		"ListTargetGroups": c.twoPages("ListTargetGroups", func(n string, next *string) any {
			return &vpclattice.ListTargetGroupsOutput{Items: []vltypes.TargetGroupSummary{{Arn: s(vltARN("targetgroup/tg-" + n)), Id: s("tg-" + n), Name: s("tg " + n)}}, NextToken: next}
		}),
		"ListAccessLogSubscriptions": c.child("ListAccessLogSubscriptions", func(in any) string {
			return sv(in.(*vpclattice.ListAccessLogSubscriptionsInput).ResourceIdentifier)
		}, func(parent string) any {
			id := "als-" + vltLast(parent)
			return &vpclattice.ListAccessLogSubscriptionsOutput{Items: []vltypes.AccessLogSubscriptionSummary{{Arn: s(vltARN("accesslogsubscription/" + id)), Id: s(id)}}}
		}),
		"GetAuthPolicy":     func(any) (any, error) { return &vpclattice.GetAuthPolicyOutput{}, nil },
		"GetResourcePolicy": func(any) (any, error) { return &vpclattice.GetResourcePolicyOutput{}, nil },
		"ListDomainVerifications": c.twoPages("ListDomainVerifications", func(n string, next *string) any {
			d := vltypes.DomainVerificationSummary{Arn: s(vltARN("domainverification/dv-" + n)), DomainName: s(n + ".example.com")}
			if n == "t" {
				d.Tags = map[string]string{"type": TypeVpcLatticeDomainVerification}
			}
			return &vpclattice.ListDomainVerificationsOutput{Items: []vltypes.DomainVerificationSummary{d}, NextToken: next}
		}),
		"ListResourceConfigurations": c.twoPages("ListResourceConfigurations", func(n string, next *string) any {
			return &vpclattice.ListResourceConfigurationsOutput{Items: []vltypes.ResourceConfigurationSummary{{Arn: s(vltARN("resourceconfiguration/rcfg-" + n)), Name: s("rc " + n)}}, NextToken: next}
		}),
		"ListResourceEndpointAssociations": c.child("ListResourceEndpointAssociations", func(in any) string {
			return sv(in.(*vpclattice.ListResourceEndpointAssociationsInput).ResourceConfigurationIdentifier)
		}, func(rc string) any {
			id := "rea-" + vltLast(rc)
			return &vpclattice.ListResourceEndpointAssociationsOutput{Items: []vltypes.ResourceEndpointAssociationSummary{{Arn: s(vltARN("resourceendpointassociation/" + id)), Id: s(id)}}}
		}),
		"ListResourceGateways": c.twoPages("ListResourceGateways", func(n string, next *string) any {
			items := []vltypes.ResourceGatewaySummary{{Arn: s(vltARN("resourcegateway/rgw-" + n)), Name: s("gw " + n)}}
			if n == "b" {
				items = append(items, vltypes.ResourceGatewaySummary{Arn: s(vltGovGateway()), Name: s("gov")})
			}
			return &vpclattice.ListResourceGatewaysOutput{Items: items, NextToken: next}
		}),
		"ListServiceNetworkResourceAssociations": c.twoPages("ListServiceNetworkResourceAssociations", func(n string, next *string) any {
			return &vpclattice.ListServiceNetworkResourceAssociationsOutput{Items: []vltypes.ServiceNetworkResourceAssociationSummary{{Arn: s(vltARN("servicenetworkresourceassociation/snra-" + n)), Id: s("snra-" + n)}}, NextToken: next}
		}),
		"ListServiceNetworkServiceAssociations": c.child("ListServiceNetworkServiceAssociations", func(in any) string {
			return sv(in.(*vpclattice.ListServiceNetworkServiceAssociationsInput).ServiceNetworkIdentifier)
		}, func(net string) any {
			id := "snsa-" + vltLast(net)
			return &vpclattice.ListServiceNetworkServiceAssociationsOutput{Items: []vltypes.ServiceNetworkServiceAssociationSummary{{Arn: s(vltARN("servicenetworkserviceassociation/" + id)), Id: s(id)}}}
		}),
		"ListServiceNetworkVpcAssociations": c.child("ListServiceNetworkVpcAssociations", func(in any) string {
			return sv(in.(*vpclattice.ListServiceNetworkVpcAssociationsInput).ServiceNetworkIdentifier)
		}, func(net string) any {
			id := "snva-" + vltLast(net)
			return &vpclattice.ListServiceNetworkVpcAssociationsOutput{Items: []vltypes.ServiceNetworkVpcAssociationSummary{{Arn: s(vltARN("servicenetworkvpcassociation/" + id)), Id: s(id)}}}
		}),
		"ListServiceNetworkVpcEndpointAssociations": c.answer("ListServiceNetworkVpcEndpointAssociations", func(any) any {
			return &vpclattice.ListServiceNetworkVpcEndpointAssociationsOutput{}
		}),
		"ListTargets": c.answer("ListTargets", func(any) any { return &vpclattice.ListTargetsOutput{} }),
	}
}

// vltScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func vltScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, vlDenied)
	return scanVpcLattice(ctx, acct, testRegion, st, testScanID)
}

func vltRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, and
// nil for a resource without tags; domain verifications keep the tags their
// list element carries. Every stored row except the domain verifications is
// sent to the tag op, the shared-in network included. Both pages of every listing are
// stored, every listing asks for the model's page cap of 100, services store
// their GetService body, and the scan reports no error or warning.
func TestScanVpcLattice_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newVLTCalls()

	total, inserted, err := vltScan(context.Background(), t, st, vltHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := vltRows()
	if total != len(rows) || inserted != len(rows) || len(rows) != 33 {
		t.Errorf("total, inserted = %d, %d; want %d (33 fixture rows)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := vltRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if vltTagged(want.arn) || want.arn == vltSharedNet() {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if want.typ != TypeVpcLatticeDomainVerification {
			wantTagARNs = append(wantTagARNs, want.arn)
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for op, caps := range calls.maxResults {
		for _, n := range caps {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
	for _, op := range []string{
		"ListServices", "ListServiceNetworks", "ListListeners", "ListRules", "ListTargetGroups", "ListAccessLogSubscriptions",
		"ListDomainVerifications", "ListResourceConfigurations", "ListResourceEndpointAssociations", "ListResourceGateways",
		"ListServiceNetworkResourceAssociations", "ListServiceNetworkServiceAssociations", "ListServiceNetworkVpcAssociations",
	} {
		if len(calls.maxResults[op]) == 0 {
			t.Errorf("%s sent no MaxResults", op)
		}
	}
	// Name is the list element's name, else its Id; the Id for the
	// subscriptions and associations, which have no name.
	for _, n := range []struct{ typ, arn, name string }{
		{TypeVpcLatticeService, vltARN("service/svc-t"), "service t"},
		{TypeVpcLatticeServiceNetwork, vltARN("servicenetwork/sn-t"), "network t"},
		{TypeVpcLatticeListener, vltListener(vltARN("service/svc-t")), "listener-id"},
		{TypeVpcLatticeRule, vltRule(vltListener(vltARN("service/svc-t"))), "rule"},
		{TypeVpcLatticeTargetGroup, vltARN("targetgroup/tg-t"), "tg t"},
		{TypeVpcLatticeAccessLogSubscription, vltARN("accesslogsubscription/als-svc-t"), "als-svc-t"},
		{TypeVpcLatticeDomainVerification, vltARN("domainverification/dv-t"), "t.example.com"},
		{TypeVpcLatticeResourceConfiguration, vltARN("resourceconfiguration/rcfg-t"), "rc t"},
		{TypeVpcLatticeResourceEndpointAssociation, vltARN("resourceendpointassociation/rea-rcfg-t"), "rea-rcfg-t"},
		{TypeVpcLatticeResourceGateway, vltARN("resourcegateway/rgw-t"), "gw t"},
		{TypeVpcLatticeServiceNetworkResourceAssociation, vltARN("servicenetworkresourceassociation/snra-t"), "snra-t"},
		{TypeVpcLatticeServiceNetworkServiceAssociation, vltARN("servicenetworkserviceassociation/snsa-sn-t"), "snsa-sn-t"},
		{TypeVpcLatticeServiceNetworkVpcAssociation, vltARN("servicenetworkvpcassociation/snva-sn-t"), "snva-sn-t"},
	} {
		if r, _ := vltRowOf(t, st, n.typ, n.arn); sv(r.Name) != n.name {
			t.Errorf("%s %s Name = %q; want %q", n.typ, n.arn, sv(r.Name), n.name)
		}
	}
	svc, _ := vltRowOf(t, st, TypeVpcLatticeService, vltARN("service/svc-t"))
	if !strings.Contains(svc.AttributesJSON, "described.example.com") {
		t.Errorf("service attributes = %s; want the GetService body", svc.AttributesJSON)
	}
	wantRules := []string{
		vltARN("service/svc-b") + " " + vltListener(vltARN("service/svc-b")),
		vltARN("service/svc-t") + " " + vltListener(vltARN("service/svc-t")),
	}
	if got := calls.parentsOf("ListRules"); !slices.Equal(got, wantRules) {
		t.Errorf("ListRules (service, listener) = %v; want %v", got, wantRules)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanVpcLattice_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newVLTCalls()
	if _, _, err := vltScan(context.Background(), t, st, vltHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := vltScan(context.Background(), t, st, vltHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 33 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 33, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under vpclattice:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A service network
// the tag read no longer finds is dropped, and its associations are never
// listed; a target group, from an account-wide listing, likewise.
func TestScanVpcLattice_TagReadFailures(t *testing.T) {
	for _, target := range []vltRow{
		{TypeVpcLatticeServiceNetwork, vltARN("servicenetwork/sn-t")},
		{TypeVpcLatticeTargetGroup, vltARN("targetgroup/tg-t")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", vlDenied, true, 0, 1},
				{"not found", vlGone, false, 0, 0},
				{"other error", vlBadInput, true, outcomeError, 0},
				{"transient", vlThrottle, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, vlListTagsOp)
					calls := newVLTCalls()
					calls.errs[target.arn] = c.err

					_, _, err := vltScan(context.Background(), t, st, vltHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := vltRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeVpcLatticeServiceNetwork {
						listed := calls.parentsOf("ListServiceNetworkVpcAssociations")
						if slices.Contains(listed, target.arn) != c.kept || !slices.Contains(listed, vltARN("servicenetwork/sn-b")) {
							t.Errorf("VPC associations listed of networks %v; want the network only when it is kept, its sibling always", listed)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", vlListTagsOp, *warnings, c.wantWarns)
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

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanVpcLattice_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, vlListTagsOp)
	calls := newVLTCalls()
	calls.errs[vltARN("resourcegateway/rgw-t")] = vlThrottle
	calls.errs[vltARN("servicenetworkresourceassociation/snra-t")] = vlBadInput

	_, _, err := vltScan(context.Background(), t, st, vltHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", vlListTagsOp, *warns)
	}
}

// A failed GetService keeps the service as its list element, tags still
// read, unless the service is gone: a denial warns once under
// vpclattice:GetService, a throttle is a transient error, any other code a
// hard one. A service GetService no longer finds is dropped and its
// listeners are never listed.
func TestScanVpcLattice_GetServiceFailures(t *testing.T) {
	svc := vltARN("service/svc-t")
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", vlDenied, true, 0, 1},
		{"not found", vlGone, false, 0, 0},
		{"other error", vlBadInput, true, outcomeError, 0},
		{"transient", vlThrottle, true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "vpclattice:GetService")
			calls := newVLTCalls()
			calls.getErrs[svc] = c.err

			_, _, err := vltScan(context.Background(), t, st, vltHandlers(calls))
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			r, ok := vltRowOf(t, st, TypeVpcLatticeService, svc)
			if ok != c.kept {
				t.Fatalf("service stored = %v; want %v", ok, c.kept)
			}
			if ok {
				if strings.Contains(r.AttributesJSON, "described") || !strings.Contains(r.AttributesJSON, `"Id":"svc-t"`) {
					t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeVpcLatticeService})
			}
			if listed := calls.parentsOf("ListListeners"); slices.Contains(listed, svc) != c.kept {
				t.Errorf("listeners listed of services %v; want the service only when it is kept", listed)
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", *warnings, c.wantWarns)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanVpcLattice_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeVpcLatticeListener)
	calls := newVLTCalls()
	calls.errs[vltListener(vltARN("service/svc-t"))] = vlDenied

	_, _, err := vltScan(context.Background(), t, st, vltHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListResourceConfigurations"); n != 0 {
		t.Errorf("ListResourceConfigurations called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A parent gone from an op that models no
// not-found fails its listing alone.
func TestScanVpcLattice_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newVLTCalls()
	handlers := vltHandlers(calls)
	handlers["ListServiceNetworks"] = failOnCall(2, apiErr("ConflictException", "page 2"), handlers["ListServiceNetworks"])
	handlers["ListResourceGateways"] = func(any) (any, error) { return nil, vlBadInput }
	reas := handlers["ListResourceEndpointAssociations"]
	handlers["ListResourceEndpointAssociations"] = func(in any) (any, error) {
		if sv(in.(*vpclattice.ListResourceEndpointAssociationsInput).ResourceConfigurationIdentifier) == vltARN("resourceconfiguration/rcfg-t") {
			return nil, vlGone
		}
		return reas(in)
	}

	_, _, err := vltScan(context.Background(), t, st, handlers)
	for _, code := range []string{"ConflictException", "ValidationException", "ResourceNotFoundException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range []vltRow{
		{TypeVpcLatticeServiceNetwork, vltARN("servicenetwork/sn-t")},
		{TypeVpcLatticeServiceNetworkVpcAssociation, vltARN("servicenetworkvpcassociation/snva-sn-t")},
		{TypeVpcLatticeResourceEndpointAssociation, vltARN("resourceendpointassociation/rea-rcfg-b")},
		{TypeVpcLatticeServiceNetworkResourceAssociation, vltARN("servicenetworkresourceassociation/snra-t")},
	} {
		if _, ok := vltRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []vltRow{
		{TypeVpcLatticeServiceNetwork, vltARN("servicenetwork/sn-b")},
		{TypeVpcLatticeResourceEndpointAssociation, vltARN("resourceendpointassociation/rea-rcfg-t")},
	} {
		if _, ok := vltRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// A service ListListeners no longer finds, and a listener ListRules no
// longer finds, are skipped silently; their siblings' children are stored.
func TestScanVpcLattice_DeletedParentSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	handlers := vltHandlers(newVLTCalls())
	listeners := handlers["ListListeners"]
	handlers["ListListeners"] = func(in any) (any, error) {
		if sv(in.(*vpclattice.ListListenersInput).ServiceIdentifier) == vltARN("service/svc-t") {
			return nil, vlGone
		}
		return listeners(in)
	}
	handlers["ListRules"] = func(any) (any, error) { return nil, vlGone }

	total, _, err := vltScan(context.Background(), t, st, handlers)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	// The fixture's 33 rows less svc-t's listener and both rules.
	if total != 30 {
		t.Errorf("total = %d; want 30", total)
	}
	if _, ok := vltRowOf(t, st, TypeVpcLatticeListener, vltListener(vltARN("service/svc-b"))); !ok {
		t.Error("listener of the sibling service missing")
	}
}

// Items listed without an ARN are not stored and are warned about once per
// listing, under a key that is never empty.
func TestScanVpcLattice_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "vpclattice:ListTargetGroups")
	handlers := vltHandlers(newVLTCalls())
	handlers["ListTargetGroups"] = func(any) (any, error) {
		return &vpclattice.ListTargetGroupsOutput{Items: []vltypes.TargetGroupSummary{{}, {}}}, nil
	}

	if _, _, err := vltScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, "(a target group with no name)") {
		t.Errorf("warnings = %v; want one naming the unnamed target group", *warnings)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanVpcLattice_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := vltHandlers(newVLTCalls())
	handlers["ListResourceGateways"] = func(any) (any, error) {
		return &vpclattice.ListResourceGatewaysOutput{Items: []vltypes.ResourceGatewaySummary{{Arn: sdkaws.String(vltARN("resourcegateway/rgw-t"))}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := vltScan(context.Background(), t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := vltRowOf(t, st, TypeVpcLatticeResourceGateway, vltARN("resourcegateway/rgw-t")); !ok {
		t.Error("resource gateway from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads.
func TestScanVpcLattice_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newVLTCalls()
	handlers := vltHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tgs := handlers["ListTargetGroups"]
	handlers["ListTargetGroups"] = func(in any) (any, error) {
		if in.(*vpclattice.ListTargetGroupsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return tgs(in)
	}

	_, _, err := vltScan(ctx, t, st, handlers)
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
	if n := calls.count("ListDomainVerifications"); n != 0 {
		t.Errorf("ListDomainVerifications called %d times after ctx ended", n)
	}
	// Target groups are phase 6 of 17, so 11 never started.
	if !strings.Contains(err.Error(), "vpclattice: 11 phases not run") {
		t.Errorf("err = %v; want it to say 11 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable,
// and no later phase is run.
func TestScanVpcLattice_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "vpc-lattice.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newVLTCalls()
		calls.errs[vltARN("targetgroup/tg-t")] = nx
		total, _, err := vltScan(context.Background(), t, st, vltHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 33 {
			t.Errorf("total = %d; want 33", total)
		}
	})
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := vltHandlers(newVLTCalls())
		handlers["ListResourceGateways"] = func(any) (any, error) { return nil, nx }
		total, _, err := vltScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 30 {
			t.Errorf("total = %d; want the 30 other rows counted", total)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newVLTCalls()
		handlers := map[string]func(any) (any, error){
			"ListServices":        func(any) (any, error) { return nil, nx },
			"ListServiceNetworks": calls.answer("ListServiceNetworks", func(any) any { return &vpclattice.ListServiceNetworksOutput{} }),
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanVpcLattice(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
		if n := calls.count("ListServiceNetworks"); n != 0 {
			t.Errorf("ListServiceNetworks called %d times after the region read as unavailable", n)
		}
	})
}

// A denied call about a resource another account owns (shared in through
// RAM) keeps what was read and is not warned about: no IAM change in this
// account can grant it. A not-found tag read of such a resource likewise
// keeps its row untagged and silent (an own row's is dropped:
// TestScanVpcLattice_TagReadFailures). A denial about the account's own
// resource, in any partition, still warns once under its op.
func TestScanVpcLattice_SharedResourceDenialSilent(t *testing.T) {
	ownNet := vltARN("servicenetwork/sn-t")
	for _, c := range []struct {
		name, op string
		deny     func(c *vltCalls, h map[string]func(any) (any, error))
		kept     vltRow
		warns    int
	}{
		{"tags of a shared network", vlListTagsOp, func(c *vltCalls, _ map[string]func(any) (any, error)) {
			c.errs[vltSharedNet()] = vlDenied
		}, vltRow{TypeVpcLatticeServiceNetwork, vltSharedNet()}, 0},
		{"tags of a shared network not found", vlListTagsOp, func(c *vltCalls, _ map[string]func(any) (any, error)) {
			c.errs[vltSharedNet()] = vlGone
		}, vltRow{TypeVpcLatticeServiceNetwork, vltSharedNet()}, 0},
		{"tags of an own network", vlListTagsOp, func(c *vltCalls, _ map[string]func(any) (any, error)) {
			c.errs[ownNet] = vlDenied
		}, vltRow{TypeVpcLatticeServiceNetwork, ownNet}, 1},
		{"tags of an own aws-us-gov gateway", vlListTagsOp, func(c *vltCalls, _ map[string]func(any) (any, error)) {
			c.errs[vltGovGateway()] = vlDenied
		}, vltRow{TypeVpcLatticeResourceGateway, vltGovGateway()}, 1},
		{"tags of a gateway in a partition the SDK does not know", vlListTagsOp, func(c *vltCalls, h map[string]func(any) (any, error)) {
			h["ListResourceGateways"] = func(any) (any, error) {
				return &vpclattice.ListResourceGatewaysOutput{Items: []vltypes.ResourceGatewaySummary{{Arn: sdkaws.String(vltUnknownGateway)}}}, nil
			}
			c.errs[vltUnknownGateway] = vlDenied
		}, vltRow{TypeVpcLatticeResourceGateway, vltUnknownGateway}, 1},
		{"VPC associations of a shared network", "vpclattice:ListServiceNetworkVpcAssociations", func(_ *vltCalls, h map[string]func(any) (any, error)) {
			h["ListServiceNetworkVpcAssociations"] = vltDenyParent(vltSharedNet(), h["ListServiceNetworkVpcAssociations"])
		}, vltRow{TypeVpcLatticeServiceNetworkVpcAssociation, vltARN("servicenetworkvpcassociation/snva-sn-t")}, 0},
		{"VPC associations of an own network", "vpclattice:ListServiceNetworkVpcAssociations", func(_ *vltCalls, h map[string]func(any) (any, error)) {
			h["ListServiceNetworkVpcAssociations"] = vltDenyParent(ownNet, h["ListServiceNetworkVpcAssociations"])
		}, vltRow{TypeVpcLatticeServiceNetworkVpcAssociation, vltARN("servicenetworkvpcassociation/snva-sn-b")}, 1},
		{"VPC endpoint associations of a shared network", vlListSNVpcEndpointAssocsOp, func(_ *vltCalls, h map[string]func(any) (any, error)) {
			h["ListServiceNetworkVpcEndpointAssociations"] = vltDenyParent(vltSharedNet(), h["ListServiceNetworkVpcEndpointAssociations"])
		}, vltRow{TypeVpcLatticeServiceNetwork, vltSharedNet()}, 0},
		{"VPC endpoint associations of an own network", vlListSNVpcEndpointAssocsOp, func(_ *vltCalls, h map[string]func(any) (any, error)) {
			h["ListServiceNetworkVpcEndpointAssociations"] = vltDenyParent(ownNet, h["ListServiceNetworkVpcEndpointAssociations"])
		}, vltRow{TypeVpcLatticeServiceNetwork, ownNet}, 1},
		{"details of a shared service", "vpclattice:GetService", func(c *vltCalls, h map[string]func(any) (any, error)) {
			list := h["ListServices"]
			h["ListServices"] = func(in any) (any, error) {
				out, err := list(in)
				if o, ok := out.(*vpclattice.ListServicesOutput); ok && o.NextToken == nil {
					o.Items = append(o.Items, vltypes.ServiceSummary{Arn: sdkaws.String(vltSharedService), Id: sdkaws.String("svc-x")})
				}
				return out, err
			}
			c.getErrs[vltSharedService] = vlDenied
		}, vltRow{TypeVpcLatticeService, vltSharedService}, 0},
		{"details of an own service", "vpclattice:GetService", func(c *vltCalls, _ map[string]func(any) (any, error)) {
			c.getErrs[vltARN("service/svc-t")] = vlDenied
		}, vltRow{TypeVpcLatticeService, vltARN("service/svc-t")}, 1},
		{"log subscriptions of a shared network", "vpclattice:ListAccessLogSubscriptions", func(_ *vltCalls, h map[string]func(any) (any, error)) {
			h["ListAccessLogSubscriptions"] = vltDenyParent(vltSharedNet(), h["ListAccessLogSubscriptions"])
		}, vltRow{TypeVpcLatticeAccessLogSubscription, vltARN("accesslogsubscription/als-sn-t")}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, c.op)
			calls := newVLTCalls()
			h := vltHandlers(calls)
			c.deny(calls, h)

			if _, _, err := vltScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.warns || *opWarns != c.warns {
				t.Errorf("warnings = %d (%s: %d); want %d", *warns, c.op, *opWarns, c.warns)
			}
			r, ok := vltRowOf(t, st, c.kept.typ, c.kept.arn)
			if !ok {
				t.Fatalf("%s %s missing", c.kept.typ, c.kept.arn)
			}
			if c.op == vlListTagsOp && r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil after the failed tag read", c.kept.arn, *r.TagsJSON)
			}
		})
	}
}

// vltUnknownGateway is an ARN parseARN rejects: its owner cannot be told, so
// a denial of it is not read as another account's.
var vltUnknownGateway = arnFor("aws-xx", "vpc-lattice", "xx-west-1", testAccountID, "resourcegateway/rgw-x")

// vltSharedService is a service shared in from vltForeignAccount.
var vltSharedService = arnFor("aws", "vpc-lattice", testRegion, vltForeignAccount, "service/svc-x")

// vltDenyParent denies next's calls for parent, the ARN the per-parent op is
// sent; every other parent is answered by next.
func vltDenyParent(parent string, next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		if sv(bacField[*string](in, "ServiceNetworkIdentifier")) == parent || sv(bacField[*string](in, "ResourceIdentifier")) == parent {
			return nil, vlDenied
		}
		return next(in)
	}
}

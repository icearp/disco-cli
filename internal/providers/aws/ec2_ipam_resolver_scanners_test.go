package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	plrA       = "ipam-prefix-list-resolver-0a"
	plrB       = "ipam-prefix-list-resolver-0b"
	plrC       = "ipam-prefix-list-resolver-0c"
	plrDeleted = "ipam-prefix-list-resolver-0d"
	plrNotFnd  = "InvalidIpamPrefixListResolverId.NotFound"
)

func plrARN(id string) string {
	return ec2ARN(testRegion, testAccountID, "ipam-prefix-list-resolver", id)
}

func plrResolver(id string, state ec2types.IpamPrefixListResolverState) ec2types.IpamPrefixListResolver {
	return ec2types.IpamPrefixListResolver{
		IpamPrefixListResolverId:  ptrStr(id),
		IpamPrefixListResolverArn: ptrStr(plrARN(id)),
		State:                     state,
		Tags:                      []ec2types.Tag{{Key: ptrStr("team"), Value: ptrStr("net")}},
	}
}

// The four rules of resolver A, built fresh per call because the scanner
// sorts the slices AWS returns in place. plrRulesSorted is the order the
// scanner must store them in: by encoding, rule 1 (no Conditions) sorts last,
// and rules 2, 4 and 3 differ only inside Conditions. Rule 2's conditions are
// stored owner first ("equals" sorts before "not-equals"); reordered serves
// them the other way round.
func plrRule1() ec2types.IpamPrefixListResolverRule {
	return ec2types.IpamPrefixListResolverRule{RuleType: ec2types.IpamPrefixListResolverRuleTypeStaticCidr, StaticCidr: ptrStr("10.0.0.0/8")}
}

func plrRule2(reordered bool) ec2types.IpamPrefixListResolverRule {
	owner := ec2types.IpamPrefixListResolverRuleCondition{Operation: ec2types.IpamPrefixListResolverRuleConditionOperationEquals, ResourceOwner: ptrStr(testAccountID)}
	notRegion := ec2types.IpamPrefixListResolverRuleCondition{Operation: ec2types.IpamPrefixListResolverRuleConditionOperationNotEquals, ResourceRegion: ptrStr("eu-west-1")}
	conds := []ec2types.IpamPrefixListResolverRuleCondition{owner, notRegion}
	if reordered {
		conds = []ec2types.IpamPrefixListResolverRuleCondition{notRegion, owner}
	}
	return ec2types.IpamPrefixListResolverRule{
		RuleType: ec2types.IpamPrefixListResolverRuleTypeIpamResourceCidr, IpamScopeId: ptrStr("ipam-scope-01"), ResourceType: ec2types.IpamResourceTypeVpc,
		Conditions: conds,
	}
}

func plrRegionRule(region string) ec2types.IpamPrefixListResolverRule {
	return ec2types.IpamPrefixListResolverRule{
		RuleType: ec2types.IpamPrefixListResolverRuleTypeIpamResourceCidr, IpamScopeId: ptrStr("ipam-scope-01"), ResourceType: ec2types.IpamResourceTypeVpc,
		Conditions: []ec2types.IpamPrefixListResolverRuleCondition{{Operation: ec2types.IpamPrefixListResolverRuleConditionOperationEquals, ResourceRegion: ptrStr(region)}},
	}
}

func plrRule3() ec2types.IpamPrefixListResolverRule { return plrRegionRule("us-east-1") }
func plrRule4() ec2types.IpamPrefixListResolverRule { return plrRegionRule("eu-west-1") }

var plrRulesSorted = []ec2types.IpamPrefixListResolverRule{plrRule2(false), plrRule4(), plrRule3(), plrRule1()}

// plrListHandler lists resolvers A and B over two pages, plus a
// delete-complete resolver and an ARN-less one that must not be stored.
func plrListHandler(in any) (any, error) {
	p := in.(*ec2.DescribeIpamPrefixListResolversInput)
	if sv32(p.MaxResults) != ec2IPAMPrefixListResolverPage {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if p.NextToken == nil {
		return &ec2.DescribeIpamPrefixListResolversOutput{NextToken: ptrStr(ipxPageNext), IpamPrefixListResolvers: []ec2types.IpamPrefixListResolver{
			plrResolver(plrA, ec2types.IpamPrefixListResolverStateCreateComplete),
			{IpamPrefixListResolverId: ptrStr("ipam-prefix-list-resolver-0e"), State: ec2types.IpamPrefixListResolverStateCreateComplete},
		}}, nil
	}
	return &ec2.DescribeIpamPrefixListResolversOutput{IpamPrefixListResolvers: []ec2types.IpamPrefixListResolver{
		plrResolver(plrB, ec2types.IpamPrefixListResolverStateModifyComplete),
		plrResolver(plrDeleted, ec2types.IpamPrefixListResolverStateDeleteComplete),
	}}, nil
}

// plrRulesHandler serves A's rules over two pages, and B none. reversed
// serves the rules in the reverse order and rule 2's conditions reordered.
// It records every resolver queried; errs fails a resolver's read.
func plrRulesHandler(calls *parentCalls, reversed bool, errs map[string]error) func(in any) (any, error) {
	page := func(first bool) []ec2types.IpamPrefixListResolverRule {
		switch {
		case !reversed && first:
			return []ec2types.IpamPrefixListResolverRule{plrRule1(), plrRule3()}
		case !reversed:
			return []ec2types.IpamPrefixListResolverRule{plrRule4(), plrRule2(false)}
		case first:
			return []ec2types.IpamPrefixListResolverRule{plrRule2(true), plrRule4()}
		default:
			return []ec2types.IpamPrefixListResolverRule{plrRule3(), plrRule1()}
		}
	}
	return func(in any) (any, error) {
		p := in.(*ec2.GetIpamPrefixListResolverRulesInput)
		id := sv(p.IpamPrefixListResolverId)
		calls.add(id)
		if sv32(p.MaxResults) != ec2IPAMPrefixListResolverPage {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		if err := errs[id]; err != nil {
			return nil, err
		}
		switch {
		case id != plrA:
			return &ec2.GetIpamPrefixListResolverRulesOutput{}, nil
		case p.NextToken == nil:
			return &ec2.GetIpamPrefixListResolverRulesOutput{NextToken: ptrStr(ipxPageNext), Rules: page(true)}, nil
		default:
			return &ec2.GetIpamPrefixListResolverRulesOutput{Rules: page(false)}, nil
		}
	}
}

func plrHandlers(calls *parentCalls, errs map[string]error) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeIpamPrefixListResolvers": plrListHandler,
		"GetIpamPrefixListResolverRules":  plrRulesHandler(calls, false, errs),
	}
}

func plrScan(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	return scanIPAMPrefixListResolvers(context.Background(), ipxClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// plrRules decodes the Rules a stored resolver carries; ok is false when the
// key is absent.
func plrRules(t *testing.T, r store.Resource) (rules []ec2types.IpamPrefixListResolverRule, ok bool) {
	t.Helper()
	raw, ok := ipxAttrs(t, r)["Rules"]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("re-encode Rules: %v", err)
	}
	if err := json.Unmarshal(b, &rules); err != nil {
		t.Fatalf("decode Rules %s: %v", b, err)
	}
	return rules, true
}

func TestScanIPAMPrefixListResolvers_EmbedsSortedRules(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		name := "API order"
		if reversed {
			name = "reversed API order"
		}
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := plrHandlers(&calls, nil)
			h["GetIpamPrefixListResolverRules"] = plrRulesHandler(&calls, reversed, nil)
			total, inserted, err := plrScan(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 || inserted != 3 {
				t.Errorf("total, inserted = %d, %d, want 3, 3 (A, B and the delete-complete resolver)", total, inserted)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{plrA, plrB}) {
				t.Errorf("rules read for %v, want %v (never the delete-complete resolver)", got, []string{plrA, plrB})
			}
			rows := rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)
			if len(rows) != 3 {
				t.Fatalf("rows = %d, want 3: %v", len(rows), rows)
			}
			a := rows[plrARN(plrA)]
			if sv(a.Region) != testRegion {
				t.Errorf("A region = %q, want %q", sv(a.Region), testRegion)
			}
			if got := sv(a.TagsJSON); got != `{"team":"net"}` {
				t.Errorf("A tags = %s, want the team tag", got)
			}
			if got := ipxAttrs(t, a)["IpamPrefixListResolverId"]; got != plrA {
				t.Errorf("A attrs IpamPrefixListResolverId = %v, want %s at the top level", got, plrA)
			}
			if got, ok := plrRules(t, a); !ok || !reflect.DeepEqual(got, plrRulesSorted) {
				t.Errorf("A Rules = %+v (present %v), want %+v", got, ok, plrRulesSorted)
			}
			if got, ok := plrRules(t, rows[plrARN(plrB)]); !ok || got == nil || len(got) != 0 {
				t.Errorf("B Rules = %#v (present %v), want an empty list", got, ok)
			}
			if _, ok := plrRules(t, rows[plrARN(plrDeleted)]); ok {
				t.Error("delete-complete resolver carries Rules, want the key absent (never read)")
			}
		})
	}
}

// A resolver whose rules read fails is never stored with an empty or missing
// Rules. A denial or a transient failure skips it with one warning however
// many fail; anything else fails the phase. The delete-complete resolver,
// never read, is stored either way.
func TestScanIPAMPrefixListResolvers_FailedRulesRead(t *testing.T) {
	notFound := apiErr(plrNotFnd, "The resolver does not exist")
	cases := []struct {
		name      string
		err       error
		isWant    func(error) bool // nil: the scan succeeds
		wantWarns int
	}{
		{"denied", ipxDenied, nil, 1},
		{"transient", io.EOF, nil, 1},
		{"not found is not modelled", notFound, func(err error) bool { return isAPIErrorCode(err, plrNotFnd) }, 0},
		{"other", errIpxTest, func(err error) bool { return errors.Is(err, errIpxTest) }, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			warnings := countOpWarnings(st, ec2IPAMPrefixListResolverRulesOp)
			// Both live resolvers fail, so a warning per resolver would show as 2.
			total, _, err := plrScan(t, st, plrHandlers(&calls, map[string]error{plrA: c.err, plrB: c.err}))
			if c.isWant == nil && err != nil || c.isWant != nil && !c.isWant(err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if *warnings != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", ec2IPAMPrefixListResolverRulesOp, *warnings, c.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)
			if _, ok := rows[plrARN(plrDeleted)]; !ok || total != 1 || len(rows) != 1 {
				t.Errorf("total = %d, rows = %d, delete-complete stored %v; want only it", total, len(rows), ok)
			}
		})
	}
}

// One failed resolver costs only its own row: the other resolver's read is
// neither cancelled nor discarded, whatever the cause, and a hard error is
// still returned.
func TestScanIPAMPrefixListResolvers_FailureKeepsSiblings(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"denied", ipxDenied, false},
		{"transient", io.EOF, false},
		{"hard", errIpxTest, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			total, _, err := plrScan(t, st, plrHandlers(&calls, map[string]error{plrB: c.err}))
			if c.wantErr != errors.Is(err, errIpxTest) || !c.wantErr && err != nil {
				t.Fatalf("err = %v, want the hard error %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)
			a, ok := rows[plrARN(plrA)]
			if !ok || total != 2 {
				t.Fatalf("total = %d, A stored %v; want 2 and A stored", total, ok)
			}
			if got, ok := plrRules(t, a); !ok || len(got) != len(plrRulesSorted) {
				t.Errorf("A Rules = %+v (present %v), want %d rules", got, ok, len(plrRulesSorted))
			}
			if _, ok := rows[plrARN(plrB)]; ok {
				t.Error("failed resolver B stored")
			}
		})
	}
}

func TestScanIPAMPrefixListResolvers_RepeatedRulesTokenFails(t *testing.T) {
	st := newTestStore(t)
	h := map[string]func(in any) (any, error){
		"DescribeIpamPrefixListResolvers": func(any) (any, error) {
			return &ec2.DescribeIpamPrefixListResolversOutput{IpamPrefixListResolvers: []ec2types.IpamPrefixListResolver{
				plrResolver(plrC, ec2types.IpamPrefixListResolverStateCreateComplete),
			}}, nil
		},
		"GetIpamPrefixListResolverRules": func(any) (any, error) {
			return &ec2.GetIpamPrefixListResolverRulesOutput{NextToken: ptrStr(ipxPageNext), Rules: []ec2types.IpamPrefixListResolverRule{plrRule1()}}, nil
		},
	}
	_, _, err := plrScan(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)); n != 0 {
		t.Errorf("rows = %d, want 0 (a partial rules list is never stored)", n)
	}
}

// A resolver listed without its ARN (the NativeID) or without the id its
// rules read takes is warned, once for however many, and not stored.
func TestScanIPAMPrefixListResolvers_IncompleteListingWarned(t *testing.T) {
	cases := []struct {
		name  string
		strip func(*ec2types.IpamPrefixListResolver)
	}{
		{"no ARN", func(r *ec2types.IpamPrefixListResolver) { r.IpamPrefixListResolverArn = nil }},
		{"no id", func(r *ec2types.IpamPrefixListResolver) { r.IpamPrefixListResolverId = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			warnings := countOpWarnings(st, ec2IPAMPrefixListResolversOp)
			r1 := plrResolver(plrC, ec2types.IpamPrefixListResolverStateCreateComplete)
			r2 := plrResolver(plrB, ec2types.IpamPrefixListResolverStateCreateComplete)
			c.strip(&r1)
			c.strip(&r2)
			h := plrHandlers(&calls, nil)
			h["DescribeIpamPrefixListResolvers"] = func(any) (any, error) {
				return &ec2.DescribeIpamPrefixListResolversOutput{IpamPrefixListResolvers: []ec2types.IpamPrefixListResolver{r1, r2}}, nil
			}
			total, _, err := plrScan(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 0 || *warnings != 1 {
				t.Errorf("total = %d, warnings = %d, want 0 and 1", total, *warnings)
			}
		})
	}
}

func TestScanIPAMPrefixListResolvers_ListingErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"denied", ipxDenied, false, 1},
		{"feature gap", apiErr("InvalidAction", "not supported in this Region"), false, 0},
		{"other", errIpxTest, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			warnings := countOpWarnings(st, ec2IPAMPrefixListResolversOp)
			h := plrHandlers(&calls, nil)
			h["DescribeIpamPrefixListResolvers"] = func(any) (any, error) { return nil, c.err }
			total, _, err := plrScan(t, st, h)
			if (err != nil) != c.wantErr || (c.wantErr && !errors.Is(err, c.err)) {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if total != 0 || *warnings != c.wantWarns {
				t.Errorf("total = %d, warnings = %d, want 0 and %d", total, *warnings, c.wantWarns)
			}
		})
	}
}

// A denial after a listed page keeps that page's resolvers, as the scanner
// did before rules were embedded; a gap after one is a hard error.
func TestScanIPAMPrefixListResolvers_ErrorAfterFirstPage(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantErr  bool
		wantRows int
	}{
		{"denied", ipxDenied, false, 1},
		{"feature gap", apiErr("InvalidAction", "not supported in this Region"), true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := plrHandlers(&calls, nil)
			h["DescribeIpamPrefixListResolvers"] = func(in any) (any, error) {
				if in.(*ec2.DescribeIpamPrefixListResolversInput).NextToken != nil {
					return nil, c.err
				}
				return plrListHandler(in)
			}
			_, _, err := plrScan(t, st, h)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if n := len(rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)); n != c.wantRows {
				t.Errorf("rows = %d, want %d", n, c.wantRows)
			}
		})
	}
}

// scanEC2 stores the resolvers with their rules, and a failed rules read
// neither cancels an older category nor the resolver targets.
func TestScanEC2_IPAMPrefixListResolvers(t *testing.T) {
	targetARN := ec2ARN(testRegion, testAccountID, "ipam-prefix-list-resolver-target", "ipam-prefix-list-resolver-target-01")
	handlers := func(calls *parentCalls, rulesErr error) map[string]func(in any) (any, error) {
		h := ipxTopLevelHandlers(calls)
		for op, fn := range plrHandlers(calls, map[string]error{plrA: rulesErr}) {
			h[op] = fn
		}
		h["DescribeIpamPrefixListResolverTargets"] = func(any) (any, error) {
			return &ec2.DescribeIpamPrefixListResolverTargetsOutput{IpamPrefixListResolverTargets: []ec2types.IpamPrefixListResolverTarget{{
				IpamPrefixListResolverTargetArn: ptrStr(targetARN), IpamPrefixListResolverId: ptrStr(plrA),
			}}}, nil
		}
		return h
	}
	t.Run("stored", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		if _, err := scanEC2TopLevel(t, st, handlers(&calls, nil)); err != nil {
			t.Fatalf("scanEC2: %v", err)
		}
		a, ok := rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolver)[plrARN(plrA)]
		if !ok {
			t.Fatal("resolver A not stored")
		}
		if got, ok := plrRules(t, a); !ok || len(got) != len(plrRulesSorted) {
			t.Errorf("A Rules = %+v (present %v), want %d rules", got, ok, len(plrRulesSorted))
		}
	})
	t.Run("rules read fails", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		h := handlers(&calls, errIpxTest)
		ctxHandlers := ignoreCtx(h)
		// DescribeIpams and the targets listing answer only after a pause, and
		// give up if their context is cancelled first, as it would be by a
		// sibling failure.
		for _, op := range []string{"DescribeIpams", "DescribeIpamPrefixListResolverTargets"} {
			ctxHandlers[op] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h[op](in)
				}
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfigCtx(ctxHandlers, ipxDenied)
		_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
		if !errors.Is(err, errIpxTest) {
			t.Fatalf("err = %v, want the rules read error", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeEC2IPAM)[ipxIpamARN]; !ok {
			t.Error("IPAM row missing: the rules failure cancelled an older category")
		}
		if _, ok := rowsByNativeID(t, st, TypeEC2IPAMPrefixListResolverTarget)[targetARN]; !ok {
			t.Error("resolver target missing: the rules failure cancelled the targets listing")
		}
	})
	t.Run("transient rules read beside a hard error", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		h := handlers(&calls, io.EOF)
		h["DescribeByoipCidrs"] = func(any) (any, error) { return nil, errIpxTest }
		warnings := countOpWarnings(st, ec2IPAMPrefixListResolverRulesOp)
		_, err := scanEC2TopLevel(t, st, h)
		if !errors.Is(err, errIpxTest) {
			t.Fatalf("err = %v, want the BYOIP error", err)
		}
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v, want outcomeError", got)
		}
		if *warnings != 1 {
			t.Errorf("%s warnings = %d, want 1", ec2IPAMPrefixListResolverRulesOp, *warnings)
		}
	})
}

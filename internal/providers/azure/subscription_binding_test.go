package azure

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Synthetic GUIDs, matching customerDirectory/discoDirectory in
// graph_tenant_test.go. This repository is public; a real directory or
// subscription id in a fixture is a fact about a deployment, not a test input.
const (
	bindCustomerDirectory = customerDirectory
	bindManagingDirectory = discoDirectory
	bindOtherDirectory    = "0f2c8b41-1d3e-4a77-9c5b-6e0a2f8d4b19"
	bindSubscription      = "3c4d5e6f-7a8b-4c9d-8e1f-2a3b4c5d6e7f"
	bindOtherSubscription = "8b7a6c5d-4e3f-4a2b-9c8d-1e0f9a8b7c6d"
)

// federatedBinding is the deployed shape: federated, with a customer
// directory named as the subscription owner.
func federatedBinding(dir string) wifConfig {
	return wifConfig{clientID: "c", tenantID: bindManagingDirectory, subscriptionTenantID: dir}
}

// noLookup fails the test if the ARM list is reached. Every refusal that can
// be decided from the configuration alone must be decided before the network,
// so a scan that is going to be refused is refused without spending a call —
// and, more importantly, so a malformed or missing directory cannot be turned
// into a scan by an ARM response.
func noLookup(t *testing.T) func() (map[string]string, error) {
	t.Helper()
	return func() (map[string]string, error) {
		t.Fatal("bindSubscriptions called ARM; the decision was available from the configuration alone")
		return nil, nil
	}
}

func ownersOf(m map[string]string) func() (map[string]string, error) {
	return func() (map[string]string, error) { return m, nil }
}

func TestBindSubscriptions_DecidesWithoutARMWhereItCan(t *testing.T) {
	subs := []subscription{{ID: bindSubscription}}

	tests := []struct {
		name string
		wif  wifConfig
		want error
	}{
		{
			// The standalone default. No variable set, no federation: the
			// check is opt-in and this path must be byte-identical to what
			// disco did before it existed.
			name: "unset and unfederated is a no-op",
			wif:  wifConfig{},
			want: nil,
		},
		{
			// The whole point. A federated credential holds delegations from
			// many tenants, so a pin selects from a cross-customer set and
			// nothing else in the process says whose the selection is.
			name: "unset under federation is refused",
			wif:  wifConfig{clientID: "c", tenantID: bindManagingDirectory},
			want: ErrSubscriptionTenantRequired,
		},
		{
			// A domain name is the realistic malformed value, and it is one
			// azidentity would accept for a token while meaning a different
			// thing here.
			name: "a directory name is not a directory GUID",
			wif:  federatedBinding("contoso.onmicrosoft.com"),
			want: ErrSubscriptionTenantMalformed,
		},
		{
			// "common"/"organizations" mean "whichever directory signs in",
			// which is the opposite of naming one. Refused for the same
			// reason graphTenantGUID refuses them.
			name: "a multi-tenant alias is refused",
			wif:  federatedBinding("organizations"),
			want: ErrSubscriptionTenantMalformed,
		},
		{
			name: "a padded GUID is refused rather than trimmed here",
			wif:  federatedBinding(" " + bindCustomerDirectory),
			want: ErrSubscriptionTenantMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bindSubscriptions(subs, tt.wif, noLookup(t))
			if !errors.Is(err, tt.want) {
				t.Fatalf("bindSubscriptions() = %v; want %v", err, tt.want)
			}
		})
	}
}

// TestBindSubscriptions_UnfederatedStillChecksWhenNamed pins that the check is
// not gated on federation once a directory is named. It is the only guard in
// this package that reads ARM rather than the env contract, so it is also the
// only one that survives a Lighthouse managing identity arriving as
// AZURE_CLIENT_ID/AZURE_CLIENT_SECRET — see [wifConfig.tenantScopeEnabled].
func TestBindSubscriptions_UnfederatedStillChecksWhenNamed(t *testing.T) {
	wif := wifConfig{subscriptionTenantID: bindCustomerDirectory}
	subs := []subscription{{ID: bindSubscription}}
	owners := map[string]string{bindSubscription: bindOtherDirectory}

	err := bindSubscriptions(subs, wif, ownersOf(owners))
	if !errors.Is(err, ErrSubscriptionNotBound) {
		t.Fatalf("bindSubscriptions() = %v; want ErrSubscriptionNotBound", err)
	}
}

func TestBindSubscriptions_AgainstARM(t *testing.T) {
	tests := []struct {
		name   string
		subs   []subscription
		owners map[string]string
		want   error
	}{
		{
			name:   "owned by the named directory",
			subs:   []subscription{{ID: bindSubscription}},
			owners: map[string]string{bindSubscription: bindCustomerDirectory},
			want:   nil,
		},
		{
			// ARM is not consistent about GUID case across the pin, the
			// config file and its own pages, and a case difference is not a
			// different directory.
			name:   "case differs on both sides",
			subs:   []subscription{{ID: strings.ToUpper(bindSubscription)}},
			owners: map[string]string{bindSubscription: strings.ToUpper(bindCustomerDirectory)},
			want:   nil,
		},
		{
			// The confused deputy: a real subscription, really delegated to
			// this deployment, owned by somebody else's directory.
			name:   "delegated to us but owned by another directory",
			subs:   []subscription{{ID: bindSubscription}},
			owners: map[string]string{bindSubscription: bindOtherDirectory},
			want:   ErrSubscriptionNotBound,
		},
		{
			// ARM lists what the caller can reach. Absence is a revoked
			// delegation, a delegation to a different managing tenant, or a
			// typo, and none of them is a scan. It refuses through the same
			// sentinel as a mismatch on purpose — see ErrSubscriptionNotBound.
			name:   "absent from the list",
			subs:   []subscription{{ID: bindSubscription}},
			owners: map[string]string{bindOtherSubscription: bindCustomerDirectory},
			want:   ErrSubscriptionNotBound,
		},
		{
			// Fail-closed. TenantID is READ-ONLY and populated for anything
			// the caller can reach, so an empty one means the response was not
			// the shape this check reads — which is no evidence of ownership,
			// not evidence of a match.
			//
			// No branch of its own in the code: an empty owner fails the same
			// EqualFold an absent one does. This pins the OUTCOME, so a future
			// "helpful" skip-on-empty is red rather than merely uncommented.
			name:   "listed with no owning directory",
			subs:   []subscription{{ID: bindSubscription}},
			owners: map[string]string{bindSubscription: ""},
			want:   ErrSubscriptionNotBound,
		},
		{
			// One bad subscription refuses the whole scan rather than being
			// dropped from the set: a partial scan of a pin is reported as a
			// scan of the pin.
			name: "one of several fails",
			subs: []subscription{{ID: bindSubscription}, {ID: bindOtherSubscription}},
			owners: map[string]string{
				bindSubscription:      bindCustomerDirectory,
				bindOtherSubscription: bindManagingDirectory,
			},
			want: ErrSubscriptionNotBound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bindSubscriptions(tt.subs, federatedBinding(bindCustomerDirectory), ownersOf(tt.owners))
			if !errors.Is(err, tt.want) {
				t.Fatalf("bindSubscriptions() = %v; want %v", err, tt.want)
			}
		})
	}
}

// TestBindSubscriptions_RefusalIsNotAnOracle pins that a mismatch never names
// the directory ARM answered.
//
// A caller may name any subscription id. Echoing the owner back would map
// subscription ids to the directories that own them for anyone who can trigger
// a scan — the cross-customer fact this check exists to withhold, handed over
// by the refusal that withholds it.
func TestBindSubscriptions_RefusalIsNotAnOracle(t *testing.T) {
	subs := []subscription{{ID: bindSubscription}}
	owners := map[string]string{bindSubscription: bindOtherDirectory}

	err := bindSubscriptions(subs, federatedBinding(bindCustomerDirectory), ownersOf(owners))
	if err == nil {
		t.Fatal("bindSubscriptions() = nil; want a mismatch")
	}
	if strings.Contains(err.Error(), bindOtherDirectory) {
		t.Errorf("refusal names the directory ARM answered: %q", err.Error())
	}
	// The two values it MAY name, so the assertion above cannot pass by the
	// message being empty or generic.
	for _, want := range []string{bindSubscription, bindCustomerDirectory} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %q", want, err.Error())
		}
	}
}

// TestBindSubscriptions_PropagatesTheLookupError pins that an ARM failure is a
// refusal rather than a pass. The list is the evidence; without it there is
// none.
func TestBindSubscriptions_PropagatesTheLookupError(t *testing.T) {
	boom := errors.New("armsubscriptions:Subscriptions.List: 403")
	err := bindSubscriptions([]subscription{{ID: bindSubscription}}, federatedBinding(bindCustomerDirectory),
		func() (map[string]string, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("bindSubscriptions() = %v; want the lookup error", err)
	}
}

// TestSubscriptionTenantID_IsNotPartOfTheFederationContract pins the one
// DISCO_AZURE_ variable partiallyConfigured must not count. Counting it would
// refuse an unfederated operator who set only this one — the standalone use
// bindSubscriptions supports on purpose.
func TestSubscriptionTenantID_IsNotPartOfTheFederationContract(t *testing.T) {
	cfg := wifConfig{subscriptionTenantID: bindCustomerDirectory}
	if cfg.partiallyConfigured() {
		t.Error("partiallyConfigured() = true for " + envSubscriptionTenantID + " alone; it opens nothing")
	}
	if cfg.configured() {
		t.Error("configured() = true without the WIF pair")
	}
}

// TestWifEnv_ReadsTheSubscriptionTenant pins the variable name and the trim.
// A value injected from a file or a secret store arrives with a trailing
// newline often enough to matter, and untrimmed it would fail the GUID match
// and refuse every scan.
func TestWifEnv_ReadsTheSubscriptionTenant(t *testing.T) {
	// The LITERAL, not the constant: a rename that edits both sides of a
	// constant-against-itself assertion stays green, and the name is a
	// deployment contract the SaaS sets from its own side.
	t.Setenv("DISCO_AZURE_SUBSCRIPTION_TENANT_ID", " "+bindCustomerDirectory+"\n")
	if got := wifEnv().subscriptionTenantID; got != bindCustomerDirectory {
		t.Fatalf("wifEnv().subscriptionTenantID = %q; want %q", got, bindCustomerDirectory)
	}
}

// TestOwnersFromPage pins the ARM-page-to-map step, which every comparison in
// bindSubscriptions depends on and which no table test above can reach: those
// inject the map directly.
//
// The lowercasing is the load-bearing part. Dropping it does not weaken the
// check, it breaks every scan — ARM answers an uppercase subscription id and
// the lowercased pin then matches nothing, so a correctly configured
// deployment refuses everything with ErrSubscriptionNotBound.
func TestOwnersFromPage(t *testing.T) {
	upperID := strings.ToUpper(bindSubscription)
	padded := " " + bindOtherSubscription + " "
	paddedTID := "\t" + bindCustomerDirectory + "\n"
	noTID := "5d6e7f8a-9b0c-4d1e-8f2a-3b4c5d6e7f8a"

	page := []*armsubscriptions.Subscription{
		{SubscriptionID: &upperID, TenantID: strPtr(bindCustomerDirectory)},
		{SubscriptionID: &padded, TenantID: &paddedTID},
		{SubscriptionID: &noTID}, // reachable, no owner in the response
		{SubscriptionID: nil, TenantID: strPtr(bindCustomerDirectory)},
		nil, // a nil element panics an unguarded loop
	}

	owners := map[string]string{}
	ownersFromPage(owners, page)

	want := map[string]string{
		bindSubscription:      bindCustomerDirectory,
		bindOtherSubscription: bindCustomerDirectory,
		noTID:                 "",
	}
	if len(owners) != len(want) {
		t.Errorf("ownersFromPage() produced %d entries, want %d: %v", len(owners), len(want), owners)
	}
	for id, dir := range want {
		got, ok := owners[id]
		if !ok {
			t.Errorf("owners[%q] absent; keys must be lowercased and trimmed: %v", id, owners)
			continue
		}
		if got != dir {
			t.Errorf("owners[%q] = %q, want %q", id, got, dir)
		}
	}

	// The map this builds is the one bindSubscriptions reads, so assert the
	// join rather than the map alone: an uppercase id from ARM must bind a
	// lowercase pin.
	if err := bindSubscriptions([]subscription{{ID: bindSubscription}}, federatedBinding(bindCustomerDirectory),
		func() (map[string]string, error) { return owners, nil }); err != nil {
		t.Errorf("bindSubscriptions() = %v; want nil for a subscription ARM listed in uppercase", err)
	}
	// And the entry with no owner must refuse, not pass.
	if err := bindSubscriptions([]subscription{{ID: noTID}}, federatedBinding(bindCustomerDirectory),
		func() (map[string]string, error) { return owners, nil }); !errors.Is(err, ErrSubscriptionNotBound) {
		t.Errorf("bindSubscriptions() = %v; want ErrSubscriptionNotBound for a listed subscription with no owner", err)
	}
}

func strPtr(s string) *string { return &s }

// TestLoadSubscriptions_CallsTheBinding pins the WIRING, which is the one
// mutation the behavioural tests above all survive: deleting the
// bindSubscriptions call from loadSubscriptions leaves every test in this file
// green and every scan unbound.
//
// A source-level assertion because the alternative is not available: exercising
// loadSubscriptions needs a credential, and the credential is built before the
// binding is reached. It reads the AST rather than grepping the text so a call
// inside a comment or a string cannot satisfy it.
//
// It asserts three things, and the third is what the earlier version only
// claimed: the call exists, its result is bound to a name, and that NAME is
// tested against nil and returned. Checking merely that some return statement
// sits inside the if admits an inverted condition and a swallowed refusal,
// both of which leave the scan unbound. The two accepted spellings are
// "if err := f(); err != nil" and an assignment followed by that if, so a
// stylistic rewrite between them does not turn this red — and each failure
// says which of the three is missing rather than blaming the call.
func TestLoadSubscriptions_CallsTheBinding(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "azure_config.go", nil, 0)
	if err != nil {
		t.Fatalf("parse azure_config.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "loadSubscriptions" {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("loadSubscriptions not found in azure_config.go; re-point this test rather than deleting it")
	}

	// The STATEMENT carrying the call, found by walking the statement list
	// rather than by inspecting the whole body. loadSubscriptions has three
	// other blocks testing `err != nil` and returning it over the same
	// name, and a whole-body search finds one of those and reports the
	// binding as guarded when its own refusal has been swallowed — measured,
	// as a surviving mutant, against the version of this test that did it.
	idx, errName := bindingStatement(fn.Body.List)
	if idx < 0 {
		if !bodyCalls(fn.Body, "bindSubscriptions") {
			t.Fatal("loadSubscriptions does not call bindSubscriptions; every scan is unbound")
		}
		t.Fatal("loadSubscriptions calls bindSubscriptions somewhere this test cannot follow; accepted shapes are `if err := bindSubscriptions(...); err != nil` and an assignment followed by that if")
	}

	// The if-statement that must return the refusal: the call's own, when it
	// carries the call in its Init, else the statement immediately after.
	guard, _ := fn.Body.List[idx].(*ast.IfStmt)
	if guard == nil || !isNotNilTest(guard.Cond, errName) {
		if idx+1 >= len(fn.Body.List) {
			t.Fatalf("nothing follows the bindSubscriptions call; %s is never tested", errName)
		}
		guard, _ = fn.Body.List[idx+1].(*ast.IfStmt)
	}
	if guard == nil || !isNotNilTest(guard.Cond, errName) {
		t.Fatalf("the bindSubscriptions call is not followed by `if %s != nil`; the refusal is not acted on", errName)
	}

	var returned bool
	ast.Inspect(guard.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, r := range ret.Results {
			if id, ok := r.(*ast.Ident); ok && id.Name == errName {
				returned = true
			}
		}
		return true
	})
	if !returned {
		t.Errorf("the guard on bindSubscriptions does not return %s; a refusal that is not returned is not a refusal", errName)
	}
}

// bindingStatement returns the index of the statement that calls
// bindSubscriptions and the name its error is bound to, or -1.
func bindingStatement(stmts []ast.Stmt) (int, string) {
	for i, stmt := range stmts {
		var assign *ast.AssignStmt
		switch s := stmt.(type) {
		case *ast.IfStmt:
			assign, _ = s.Init.(*ast.AssignStmt)
		case *ast.AssignStmt:
			assign = s
		}
		if assign == nil || !callsIdent(assign.Rhs, "bindSubscriptions") || len(assign.Lhs) != 1 {
			continue
		}
		if id, ok := assign.Lhs[0].(*ast.Ident); ok {
			return i, id.Name
		}
	}
	return -1, ""
}

// callsIdent reports whether any expression in exprs calls the named function.
func callsIdent(exprs []ast.Expr, name string) bool {
	var found bool
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
			return true
		})
	}
	return found
}

// bodyCalls reports whether the body calls the named function anywhere.
func bodyCalls(body *ast.BlockStmt, name string) bool {
	var found bool
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return true
	})
	return found
}

// isNotNilTest reports whether cond is exactly `name != nil`.
func isNotNilTest(cond ast.Expr, name string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	x, ok := bin.X.(*ast.Ident)
	if !ok || x.Name != name {
		return false
	}
	y, ok := bin.Y.(*ast.Ident)
	return ok && y.Name == "nil"
}

// TestLoadSubscriptionsError_NarrowsAnARMFailure pins what a failed
// subscription list puts on the scan record.
//
// Both list calls — the enumerate path and the owner lookup this binding adds
// — reach this one function, and its output is stored verbatim by scanrun and
// rendered to a customer by the SaaS. A 403 is the shape that matters:
// redaction covers 401 and AADSTS and passes AuthorizationFailed straight
// through, so before this narrowing the record carried azcore's whole request
// line and response body.
func TestLoadSubscriptionsError_NarrowsAnARMFailure(t *testing.T) {
	respErr := armResponseError(http.StatusForbidden, "AuthorizationFailed",
		"The client 'abc' with object id 'abc' does not have authorization to perform action 'Microsoft.Resources/subscriptions/read'.")
	got := loadSubscriptionsError(fmt.Errorf("armsubscriptions:Subscriptions.List: %w", respErr)).Error()

	// ARM's own message survives — it is the one thing the operator needs.
	if !strings.Contains(got, "does not have authorization") {
		t.Errorf("the ARM message was lost: %q", got)
	}
	if !strings.Contains(got, "403") || !strings.Contains(got, "AuthorizationFailed") {
		t.Errorf("status and code must survive: %q", got)
	}
	// azcore's dump must not. RESPONSE/GET/-------- are the markers of
	// ResponseError.Error(), which is what the %w wrap used to store.
	for _, leak := range []string{"RESPONSE 403", "--------", "GET https://"} {
		if strings.Contains(got, leak) {
			t.Errorf("azcore's raw dump reached the scan record (%q): %q", leak, got)
		}
	}
}

// TestLoadSubscriptionsError_KeepsANonARMErrorMatchable is the other half: the
// narrowing above must not swallow the chain for a configuration refusal,
// which is not an ARM error and which callers match with errors.Is.
func TestLoadSubscriptionsError_KeepsANonARMErrorMatchable(t *testing.T) {
	for _, sentinel := range []error{ErrIncompleteWIFConfig, ErrSubscriptionTenantRequired, ErrSubscriptionNotBound} {
		if err := loadSubscriptionsError(fmt.Errorf("wrapped: %w", sentinel)); !errors.Is(err, sentinel) {
			t.Errorf("loadSubscriptionsError() lost the chain for %v", sentinel)
		}
	}
}

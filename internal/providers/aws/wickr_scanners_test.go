package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/wickr"
	wickrtypes "github.com/aws/aws-sdk-go-v2/service/wickr/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

const (
	wkNetIDA = "10000001"
	wkNetIDB = "10000002"

	wkNetA    = "arn:aws:wickr:us-east-1:123456789012:network/" + wkNetIDA
	wkNetB    = "arn:aws:wickr:us-east-1:123456789012:network/" + wkNetIDB
	wkNetNoID = "arn:aws:wickr:us-east-1:123456789012:network/10000003"

	wkSGDefault = wkNetA + "/security-group/sg-default"
	wkSGOps     = wkNetA + "/security-group/sg-ops"
	wkSGB       = wkNetB + "/security-group/sg-b"
	wkBotA1     = wkNetA + "/bot/b-1"
	wkBotA2     = wkNetA + "/bot/b-2"
	wkBotB      = wkNetB + "/bot/b-b"
	wkSetA      = wkNetA + "/network-settings"
	wkSetB      = wkNetB + "/network-settings"
	wkUserA1    = wkNetA + "/user/101"
	wkUserA2    = wkNetA + "/user/102"
	wkUserB     = wkNetB + "/user/201"

	wkInviteCode = "INVITE-0000-not-real"
)

var (
	wkDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: wickr:List")
	wkForbidden  = apiErr("ForbiddenError", "forbidden")
	wkRateLimit  = apiErr("RateLimitError", "too many requests")
	wkThrottled  = apiErr("ThrottlingException", "slow down")
	wkGone       = apiErr("ResourceNotFoundError", "gone")
	wkValidation = apiErr("ValidationError", "bad field")

	wkAllNetworks = []string{wkNetA, wkNetB, wkNetNoID}

	// wkBotAsUser is a ListUsers element the model marks as not a user.
	wkBotAsUser = wickrtypes.User{UserId: sdkaws.String("103"), Username: sdkaws.String("helper-bot"), IsUser: sdkaws.Bool(false)}
)

// wkChild describes one child phase for the table tests: the handler key, the
// op label its warnings carry, the type it stores, and the rows each network
// contributes in wkHandlers.
type wkChild struct {
	handler, label, typ string
	rowsA, rowsB        []string
	paged               bool
}

var wkChildren = []wkChild{
	{"ListSecurityGroups", wickrListSecurityGroupsOp, TypeWickrSecurityGroup, []string{wkSGDefault, wkSGOps}, []string{wkSGB}, true},
	{"ListBots", wickrListBotsOp, TypeWickrBot, []string{wkBotA1, wkBotA2}, []string{wkBotB}, true},
	{"GetNetworkSettings", wickrGetNetworkSettingsOp, TypeWickrNetworkSetting, []string{wkSetA}, []string{wkSetB}, false},
	{"ListUsers", wickrListUsersOp, TypeWickrUser, []string{wkUserA1, wkUserA2}, []string{wkUserB}, true},
}

func (c wkChild) all() []string {
	out := slices.Concat(c.rowsA, c.rowsB)
	slices.Sort(out)
	return out
}

// wkNetworkOf returns the NetworkId and NextToken of any child op's input.
func wkNetworkOf(in any) (string, *string) {
	switch p := in.(type) {
	case *wickr.ListSecurityGroupsInput:
		return sdkaws.ToString(p.NetworkId), p.NextToken
	case *wickr.ListBotsInput:
		return sdkaws.ToString(p.NetworkId), p.NextToken
	case *wickr.GetNetworkSettingsInput:
		return sdkaws.ToString(p.NetworkId), nil
	case *wickr.ListUsersInput:
		return sdkaws.ToString(p.NetworkId), p.NextToken
	}
	panic("unexpected input")
}

// wkMaxResults returns the MaxResults of a list op's input.
func wkMaxResults(in any) int32 {
	switch p := in.(type) {
	case *wickr.ListNetworksInput:
		return sdkaws.ToInt32(p.MaxResults)
	case *wickr.ListSecurityGroupsInput:
		return sdkaws.ToInt32(p.MaxResults)
	case *wickr.ListBotsInput:
		return sdkaws.ToInt32(p.MaxResults)
	case *wickr.ListUsersInput:
		return sdkaws.ToInt32(p.MaxResults)
	}
	panic("unexpected input")
}

// wkHandlers answers every Wickr op the scan calls. ListNetworks has two pages
// (A, B; then a network without a NetworkId, stored but never queried, and one
// without an ARN, skipped). A's security groups, bots and users each come in
// two pages with an id-less element to skip (and, for users, one with IsUser
// false to skip); B has one of each, its user with IsUser true. Both networks
// have settings. Every child call is recorded in calls by handler key and
// network id, and every list op fails unless MaxResults is the model's cap.
func wkHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	badLimit := apiErr("ValidationError", "maxResults must be at most 100")
	for _, c := range wkChildren {
		calls[c.handler] = &parentCalls{}
	}
	record := func(handler string, in any) (string, *string, error) {
		id, token := wkNetworkOf(in)
		calls[handler].add(id)
		if handler != "GetNetworkSettings" && wkMaxResults(in) != wickrPageLimit {
			return "", nil, badLimit
		}
		if id != wkNetIDA && id != wkNetIDB {
			return "", nil, wkValidation
		}
		return id, token, nil
	}
	group := func(id, name string, isDefault bool) wickrtypes.SecurityGroup {
		return wickrtypes.SecurityGroup{Id: nonEmptyPtr(id), Name: sdkaws.String(name), IsDefault: sdkaws.Bool(isDefault), ActiveMembers: sdkaws.Int32(2)}
	}
	bot := func(id, username string) wickrtypes.Bot {
		return wickrtypes.Bot{BotId: nonEmptyPtr(id), Username: sdkaws.String(username), DisplayName: sdkaws.String(username + " display")}
	}
	user := func(id, username, invite string) wickrtypes.User {
		return wickrtypes.User{UserId: nonEmptyPtr(id), Username: sdkaws.String(username), FirstName: sdkaws.String("Ada"), InviteCode: nonEmptyPtr(invite)}
	}
	return map[string]func(in any) (any, error){
		"ListNetworks": func(in any) (any, error) {
			p := in.(*wickr.ListNetworksInput)
			if wkMaxResults(in) != wickrPageLimit {
				return nil, badLimit
			}
			network := func(arn, id, name string) wickrtypes.Network {
				return wickrtypes.Network{NetworkArn: nonEmptyPtr(arn), NetworkId: nonEmptyPtr(id), NetworkName: sdkaws.String(name), AccessLevel: wickrtypes.AccessLevelPremium}
			}
			var out wickr.ListNetworksOutput
			out.Networks, out.NextToken = evsPage(p.NextToken,
				[]wickrtypes.Network{network(wkNetA, wkNetIDA, "alpha"), network(wkNetB, wkNetIDB, "bravo")},
				[]wickrtypes.Network{network(wkNetNoID, "", "no id"), network("", "10000004", "no arn")})
			return &out, nil
		},
		"ListSecurityGroups": func(in any) (any, error) {
			id, token, err := record("ListSecurityGroups", in)
			if err != nil {
				return nil, err
			}
			var out wickr.ListSecurityGroupsOutput
			if id == wkNetIDA {
				out.SecurityGroups, out.NextToken = evsPage(token,
					[]wickrtypes.SecurityGroup{group("sg-default", "Default", true), group("", "no id", false)},
					[]wickrtypes.SecurityGroup{group("sg-ops", "ops", false)})
			} else {
				out.SecurityGroups = []wickrtypes.SecurityGroup{group("sg-b", "b", false)}
			}
			return &out, nil
		},
		"ListBots": func(in any) (any, error) {
			id, token, err := record("ListBots", in)
			if err != nil {
				return nil, err
			}
			var out wickr.ListBotsOutput
			if id == wkNetIDA {
				out.Bots, out.NextToken = evsPage(token,
					[]wickrtypes.Bot{bot("b-1", "one-bot"), bot("", "noid-bot")},
					[]wickrtypes.Bot{bot("b-2", "two-bot")})
			} else {
				out.Bots = []wickrtypes.Bot{bot("b-b", "b-bot")}
			}
			return &out, nil
		},
		"GetNetworkSettings": func(in any) (any, error) {
			id, _, err := record("GetNetworkSettings", in)
			if err != nil {
				return nil, err
			}
			return &wickr.GetNetworkSettingsOutput{Settings: []wickrtypes.Setting{{
				OptionName: sdkaws.String("dataRetention"), Type: sdkaws.String("boolean"), Value: sdkaws.String(id),
			}}}, nil
		},
		"ListUsers": func(in any) (any, error) {
			id, token, err := record("ListUsers", in)
			if err != nil {
				return nil, err
			}
			var out wickr.ListUsersOutput
			if id == wkNetIDA {
				out.Users, out.NextToken = evsPage(token,
					[]wickrtypes.User{user("101", "ada@example.com", wkInviteCode), user("", "noid@example.com", "")},
					[]wickrtypes.User{user("102", "bob@example.com", ""), wkBotAsUser})
			} else {
				cy := user("201", "cy@example.com", "")
				cy.IsUser = sdkaws.Bool(true)
				out.Users = []wickrtypes.User{cy}
			}
			return &out, nil
		},
	}
}

func scanWickrTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wkDenied)
	total, _, err := scanWickr(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func wkAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	return attrs
}

func TestScanWickr_StoresNetworkChildren(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	total, err := scanWickrTopLevel(t, st, wkHandlers(calls))
	if err != nil {
		t.Fatalf("scanWickr: %v", err)
	}
	// 3 networks + 3 security groups + 3 bots + 2 settings + 3 users
	if total != 14 {
		t.Errorf("total = %d, want 14", total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWickrNetwork)); !slices.Equal(got, wkAllNetworks) {
		t.Errorf("network rows = %v, want %v", got, wkAllNetworks)
	}
	for _, c := range wkChildren {
		if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, c.all()) {
			t.Errorf("%s rows = %v, want %v", c.typ, got, c.all())
		}
		if got, want := calls[c.handler].distinct(), []string{wkNetIDA, wkNetIDB}; !slices.Equal(got, want) {
			t.Errorf("%s networks = %v, want %v (not the network without a NetworkId)", c.handler, got, want)
		}
		for _, child := range c.rowsA {
			assertContainsNative(t, st, wkNetA, child)
		}
		for _, child := range c.rowsB {
			assertContainsNative(t, st, wkNetB, child)
		}
	}
	assertContainsEdgesDeclared(t, st)

	net := rowsByNativeID(t, st, TypeWickrNetwork)[wkNetA]
	if net.Type != TypeWickrNetwork || sv(net.Region) != testRegion || sv(net.Name) != "alpha" || wkAttrs(t, net)["AccessLevel"] != "PREMIUM" {
		t.Errorf("%s row = type %q region %q name %q attrs %s", wkNetA, net.Type, sv(net.Region), sv(net.Name), net.AttributesJSON)
	}

	groups := rowsByNativeID(t, st, TypeWickrSecurityGroup)
	if g := groups[wkSGDefault]; !g.ManagedByProvider || sv(g.Name) != "Default" || sv(g.Region) != testRegion {
		t.Errorf("default group = managed %v name %q region %q, want managed Default in %s", g.ManagedByProvider, sv(g.Name), sv(g.Region), testRegion)
	}
	if g := groups[wkSGOps]; g.ManagedByProvider || wkAttrs(t, g)["ActiveMembers"] != float64(2) {
		t.Errorf("ops group = managed %v attrs %s, want unmanaged with ActiveMembers 2", g.ManagedByProvider, g.AttributesJSON)
	}

	if b := rowsByNativeID(t, st, TypeWickrBot)[wkBotA2]; sv(b.Name) != "two-bot" || sv(b.Region) != testRegion || wkAttrs(t, b)["DisplayName"] != "two-bot display" {
		t.Errorf("%s row = name %q region %q attrs %s", wkBotA2, sv(b.Name), sv(b.Region), b.AttributesJSON)
	}

	set := rowsByNativeID(t, st, TypeWickrNetworkSetting)[wkSetB]
	settings, _ := wkAttrs(t, set)["Settings"].([]any)
	if !set.ManagedByProvider || sv(set.Region) != testRegion || len(settings) != 1 || settings[0].(map[string]any)["Value"] != wkNetIDB {
		t.Errorf("%s row = managed %v region %q attrs %s, want managed with B's one setting", wkSetB, set.ManagedByProvider, sv(set.Region), set.AttributesJSON)
	}

	u := rowsByNativeID(t, st, TypeWickrUser)[wkUserA1]
	attrs := wkAttrs(t, u)
	if sv(u.Name) != "ada@example.com" || sv(u.Region) != testRegion || attrs["InviteCode"] != redact.Placeholder || attrs["FirstName"] != "Ada" {
		t.Errorf("%s row = name %q region %q attrs %s, want InviteCode redacted and FirstName kept", wkUserA1, sv(u.Name), sv(u.Region), u.AttributesJSON)
	}
}

func TestScanWickr_NoChildren(t *testing.T) {
	st := newTestStore(t)
	h := wkHandlers(map[string]*parentCalls{})
	h["ListSecurityGroups"] = func(any) (any, error) { return &wickr.ListSecurityGroupsOutput{}, nil }
	h["ListBots"] = func(any) (any, error) { return &wickr.ListBotsOutput{}, nil }
	h["GetNetworkSettings"] = func(any) (any, error) { return &wickr.GetNetworkSettingsOutput{}, nil }
	h["ListUsers"] = func(any) (any, error) { return &wickr.ListUsersOutput{}, nil }
	total, err := scanWickrTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanWickr: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want the 3 networks only", total)
	}
	for _, c := range wkChildren {
		if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", c.typ, n)
		}
	}
}

// For each child phase: a network deleted since listing drops only its own
// rows, silently. A network denied (AccessDenied-coded or ForbiddenError),
// rate-limited or throttled after retries drops only its own rows and warns
// once however many networks hit it. Any other error, on any page, fails the
// phase and is returned. Either way the networks and every other phase's rows
// are stored.
func TestScanWickr_FailedChildNetwork(t *testing.T) {
	for _, c := range wkChildren {
		for _, tc := range []struct {
			name      string
			fail      map[string]error // network id -> error
			failPage2 bool             // fail only A's second page
			wantCode  string           // "" means the scan returns nil
			wantRows  []string         // checked only when wantCode is ""
			wantWarns int
		}{
			{"gone", map[string]error{wkNetIDB: wkGone}, false, "", c.rowsA, 0},
			{"denied", map[string]error{wkNetIDA: wkDenied, wkNetIDB: wkDenied}, false, "", nil, 1},
			{"forbidden", map[string]error{wkNetIDA: wkForbidden, wkNetIDB: wkForbidden}, false, "", nil, 1},
			{"rate limited", map[string]error{wkNetIDB: wkRateLimit}, false, "", c.rowsA, 1},
			{"throttled", map[string]error{wkNetIDB: wkThrottled}, false, "", c.rowsA, 1},
			{"other", map[string]error{wkNetIDB: wkValidation}, false, "ValidationError", nil, 0},
			{"other on page two", map[string]error{wkNetIDA: wkValidation}, true, "ValidationError", nil, 0},
		} {
			if tc.failPage2 && !c.paged {
				continue
			}
			t.Run(c.handler+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.label)
				h := wkHandlers(map[string]*parentCalls{})
				next := h[c.handler]
				h[c.handler] = func(in any) (any, error) {
					id, token := wkNetworkOf(in)
					if err, ok := tc.fail[id]; ok && (!tc.failPage2 || token != nil) {
						return nil, err
					}
					return next(in)
				}
				_, err := scanWickrTopLevel(t, st, h)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
					if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, tc.wantRows) {
						t.Errorf("%s rows = %v, want %v", c.typ, got, tc.wantRows)
					}
				} else {
					assertAPICode(t, err, tc.wantCode)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				if got := nativeIDs(rowsByNativeID(t, st, TypeWickrNetwork)); !slices.Equal(got, wkAllNetworks) {
					t.Errorf("network rows = %v, want %v", got, wkAllNetworks)
				}
				for _, other := range wkChildren {
					if other.typ == c.typ {
						continue
					}
					if got := nativeIDs(rowsByNativeID(t, st, other.typ)); !slices.Equal(got, other.all()) {
						t.Errorf("%s rows = %v, want %v (phases are independent)", other.typ, got, other.all())
					}
				}
			})
		}
	}
}

// A list op that keeps answering with the token it was sent stops after that
// page instead of paging forever; the handler fails a third call.
func TestScanWickr_RepeatedTokenStops(t *testing.T) {
	again := sdkaws.String("again")
	tooMany := apiErr("ValidationError", "called a third time")
	for _, op := range []string{"ListNetworks", "ListSecurityGroups", "ListBots", "ListUsers"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			h := wkHandlers(map[string]*parentCalls{})
			next := h[op]
			var calls parentCalls
			h[op] = func(in any) (any, error) {
				id := wkNetIDA
				if op != "ListNetworks" {
					id, _ = wkNetworkOf(in)
				}
				if id != wkNetIDA {
					return next(in)
				}
				calls.add(id)
				if len(calls.ids) > 2 {
					return nil, tooMany
				}
				switch out, err := next(in); o := out.(type) {
				case *wickr.ListNetworksOutput:
					o.NextToken = again
					return o, err
				case *wickr.ListSecurityGroupsOutput:
					o.NextToken = again
					return o, err
				case *wickr.ListBotsOutput:
					o.NextToken = again
					return o, err
				case *wickr.ListUsersOutput:
					o.NextToken = again
					return o, err
				}
				panic("unexpected output")
			}
			if _, err := scanWickrTopLevel(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if n := len(calls.ids); n != 2 {
				t.Errorf("%s calls = %d, want 2", op, n)
			}
		})
	}
}

// Hard errors from two child phases both reach the caller.
func TestScanWickr_ChildPhaseErrorsJoined(t *testing.T) {
	st := newTestStore(t)
	h := wkHandlers(map[string]*parentCalls{})
	h["ListBots"] = func(any) (any, error) { return nil, wkValidation }
	h["ListUsers"] = func(any) (any, error) { return nil, apiErr("BadRequestError", "bad") }
	_, err := scanWickrTopLevel(t, st, h)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("err = %v, want joined phase errors", err)
	}
	var codes []string
	for _, e := range joined.Unwrap() {
		var ae smithy.APIError
		if errors.As(e, &ae) {
			codes = append(codes, ae.ErrorCode())
		}
	}
	if want := []string{"ValidationError", "BadRequestError"}; !slices.Equal(codes, want) {
		t.Errorf("joined codes = %v, want %v", codes, want)
	}
}

// A failed network listing stores nothing and queries no network; a denial
// (AccessDenied-coded or ForbiddenError) or a RateLimitError still failing
// after the retries warns and returns nil, anything else is returned, also
// when only the second page fails.
func TestScanWickr_NetworkListErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		page2     bool
		wantCode  string
		wantWarns int
	}{
		{"denied", wkDenied, false, "", 1},
		{"forbidden", wkForbidden, false, "", 1},
		{"rate limited", wkRateLimit, false, "", 1},
		{"other", wkValidation, false, "ValidationError", 0},
		{"other on page two", wkValidation, true, "ValidationError", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, wickrListNetworksOp)
			calls := map[string]*parentCalls{}
			h := wkHandlers(calls)
			next := h["ListNetworks"]
			h["ListNetworks"] = func(in any) (any, error) {
				if !tc.page2 || in.(*wickr.ListNetworksInput).NextToken != nil {
					return nil, tc.err
				}
				return next(in)
			}
			total, err := scanWickrTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d warnings = %d, want 0 and %d", total, *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeWickrNetwork)); n != 0 {
				t.Errorf("network rows = %d, want 0", n)
			}
			for handler, c := range calls {
				if len(c.ids) != 0 {
					t.Errorf("%s called %d times after a failed network listing", handler, len(c.ids))
				}
			}
		})
	}
}

// The SDK retryer does not retry Wickr's 429 RateLimitError; the client the
// scanner builds does, and only that code, while keeping the retryer and
// attempt budget the account config sets.
func TestWickrClientOptions_RetriesRateLimitError(t *testing.T) {
	rateLimited := &wickrtypes.RateLimitError{Message: sdkaws.String("slow down")}
	if retry.NewStandard().IsErrorRetryable(rateLimited) {
		t.Fatal("the SDK's standard retryer retries RateLimitError; wickrClientOptions' retryer wrap is redundant")
	}
	for _, tc := range []struct {
		name         string
		cfg          sdkaws.Config
		wantAttempts int
	}{
		// The shape aws_config.go loads: adaptive mode, 10 attempts.
		{"account config", sdkaws.Config{RetryMode: sdkaws.RetryModeAdaptive, RetryMaxAttempts: 10}, 10},
		// A config-supplied retryer is wrapped, not replaced.
		{"config retryer", sdkaws.Config{Retryer: func() sdkaws.Retryer {
			return retry.AddWithMaxAttempts(retry.NewStandard(), 7)
		}}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := wickr.NewFromConfig(tc.cfg, wickrClientOptions(testRegion)).Options()
			if got := o.Retryer.MaxAttempts(); got != tc.wantAttempts {
				t.Errorf("MaxAttempts = %d, want %d", got, tc.wantAttempts)
			}
			if !o.Retryer.IsErrorRetryable(rateLimited) {
				t.Error("wickr client does not retry RateLimitError")
			}
			if o.Retryer.IsErrorRetryable(&wickrtypes.ValidationError{Message: sdkaws.String("bad")}) {
				t.Error("wickr client retries ValidationError")
			}
			if o.Region != testRegion {
				t.Errorf("region = %q, want %q", o.Region, testRegion)
			}
		})
	}
}

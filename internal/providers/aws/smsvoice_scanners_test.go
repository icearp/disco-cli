package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	svtypes "github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"
	"github.com/icearp/disco-cli/store"
)

var (
	svDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: sms-voice:Describe")
	svNotFound   = apiErr("ResourceNotFoundException", "gone")
	svValidation = apiErr("ValidationException", "bad arn")
	svThrottle   = apiErr("ThrottlingException", "slow down")
	svCreated    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

const (
	svTagsOp     = "sms-voice:ListTagsForResource"
	svOtherAcct  = "210987654321"
	svTotalCount = 22
)

// svKind is one listing of the fixture: the Describe op, the type it stores,
// the ARN resource kind, the label its ids start with, the ARN suffix after
// the id (the sender id's country code), whether its Describe op takes an
// Owner filter (shared rows), wantName, the Name a row of id stores, and
// page, which builds the op's output from one element's ARN (nil for an
// element without one), its id and the next token.
type svKind struct {
	op, typ, kind, label, arnSuffix string
	shared                          bool
	wantName                        func(id string) string
	page                            func(arn *string, id string, next *string) any
}

func svByID(id string) string     { return id }
func svByName(id string) string   { return "name-" + id }
func svNameOf(id string) *string  { return sdkaws.String(svByName(id)) }
func svTokenOf(in any) *string    { return bacField[*string](in, "NextToken") }
func svMaxResultsOf(in any) int32 { return sdkaws.ToInt32(bacField[*int32](in, "MaxResults")) }

func svKinds() []svKind {
	return []svKind{
		{"DescribeConfigurationSets", TypeSMSVoiceConfigurationSet, "configuration-set", "cs", "", false, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeConfigurationSetsOutput{ConfigurationSets: []svtypes.ConfigurationSetInformation{{ConfigurationSetArn: arn, ConfigurationSetName: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribeOptOutLists", TypeSMSVoiceOptOutList, "opt-out-list", "ool", "", true, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeOptOutListsOutput{OptOutLists: []svtypes.OptOutListInformation{{OptOutListArn: arn, OptOutListName: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribePhoneNumbers", TypeSMSVoicePhoneNumber, "phone-number", "pn", "", true, svByName, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribePhoneNumbersOutput{PhoneNumbers: []svtypes.PhoneNumberInformation{{PhoneNumberArn: arn, PhoneNumberId: sdkaws.String(id), PhoneNumber: svNameOf(id)}}, NextToken: next}
		}},
		{"DescribePools", TypeSMSVoicePool, "pool", "pool", "", true, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribePoolsOutput{Pools: []svtypes.PoolInformation{{PoolArn: arn, PoolId: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribeProtectConfigurations", TypeSMSVoiceProtectConfiguration, "protect-configuration", "pc", "", false, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeProtectConfigurationsOutput{ProtectConfigurations: []svtypes.ProtectConfigurationInformation{{ProtectConfigurationArn: arn, ProtectConfigurationId: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribeSenderIds", TypeSMSVoiceSenderID, "sender-id", "sid", "/US", true, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeSenderIdsOutput{SenderIds: []svtypes.SenderIdInformation{{SenderIdArn: arn, SenderId: sdkaws.String(id), IsoCountryCode: sdkaws.String("US")}}, NextToken: next}
		}},
		{"DescribeRegistrations", TypeSMSVoiceRegistration, "registration", "reg", "", false, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeRegistrationsOutput{Registrations: []svtypes.RegistrationInformation{{RegistrationArn: arn, RegistrationId: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribeRegistrationAttachments", TypeSMSVoiceRegistrationAttachment, "registration-attachment", "att", "", false, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeRegistrationAttachmentsOutput{RegistrationAttachments: []svtypes.RegistrationAttachmentsInformation{{RegistrationAttachmentArn: arn, RegistrationAttachmentId: sdkaws.String(id)}}, NextToken: next}
		}},
		{"DescribeVerifiedDestinationNumbers", TypeSMSVoiceVerifiedDestinationNumber, "verified-destination-number", "vdn", "", false, svByName, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersOutput{VerifiedDestinationNumbers: []svtypes.VerifiedDestinationNumberInformation{{VerifiedDestinationNumberArn: arn, VerifiedDestinationNumberId: sdkaws.String(id), DestinationPhoneNumber: svNameOf(id)}}, NextToken: next}
		}},
		{"DescribeNotifyConfigurations", TypeSMSVoiceNotifyConfiguration, "notify-configuration", "nc", "", false, svByName, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeNotifyConfigurationsOutput{NotifyConfigurations: []svtypes.NotifyConfigurationInformation{{
				NotifyConfigurationArn: arn, NotifyConfigurationId: sdkaws.String(id), DisplayName: svNameOf(id),
				Status: svtypes.NotifyConfigurationStatusActive, CreatedTimestamp: &svCreated,
			}}, NextToken: next}
		}},
		{"DescribeRcsAgents", TypeSMSVoiceRcsAgent, "rcs-agent", "rcs", "", true, svByID, func(arn *string, id string, next *string) any {
			return &pinpointsmsvoicev2.DescribeRcsAgentsOutput{RcsAgents: []svtypes.RcsAgentInformation{{
				RcsAgentArn: arn, RcsAgentId: sdkaws.String(id), Status: svtypes.RcsAgentStatusTesting, CreatedTimestamp: &svCreated,
			}}, NextToken: next}
		}},
	}
}

func svKindOf(t *testing.T, op string) svKind {
	t.Helper()
	for _, k := range svKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return svKind{}
}

func svID(k svKind, suffix string) string { return k.label + "-" + suffix }

func svARNIn(partition, region, account string, k svKind, id string) string {
	return "arn:" + partition + ":sms-voice:" + region + ":" + account + ":" + k.kind + "/" + id + k.arnSuffix
}

func svARN(k svKind, suffix string) string {
	return svARNIn("aws", testRegion, testAccountID, k, svID(k, suffix))
}

// svCalls records the stubbed calls: how often each op ran, the MaxResults
// each Describe op was sent, and the ARNs sent to ListTagsForResource, which
// it answers from tags or errs, both keyed by ARN. Tag reads run
// concurrently.
type svCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]int32
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newSVCalls tags every "tagged" resource with its type.
func newSVCalls() *svCalls {
	c := &svCalls{ops: map[string]int{}, maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range svKinds() {
		c.tags[svARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *svCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *svCalls) listTags(in any) (any, error) {
	arn := sv(in.(*pinpointsmsvoicev2.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.ops["ListTagsForResource"]++
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []svtypes.Tag
	for k, v := range tags {
		list = append(list, svtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &pinpointsmsvoicev2.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *svCalls) twoPages(k svKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.mu.Lock()
		c.ops[k.op]++
		c.maxResults[k.op] = append(c.maxResults[k.op], svMaxResultsOf(in))
		c.mu.Unlock()
		suffix, next := "tagged", sdkaws.String("t2")
		if svTokenOf(in) != nil {
			suffix, next = "bare", nil
		}
		return k.page(sdkaws.String(svARN(k, suffix)), svID(k, suffix), next), nil
	}
}

// svHandlers answers every op the scan calls with the fixture: two
// resources of every kind, on two pages.
func svHandlers(c *svCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range svKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// svScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func svScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, svDenied)
	return scanSMSVoice(ctx, acct, testRegion, st, testScanID)
}

func svRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores both pages of its listing, named as it is listed, with
// the tags ListTagsForResource returns for its ARN, and nil for a resource
// without tags. Exactly the ARNs of every row are sent to the tag op. Every
// Describe op is sent MaxResults 100. Notify configurations and RCS agents
// keep their Status and CreatedAt, and every row its element as attributes.
// No error or warning.
func TestScanSMSVoice_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newSVCalls()

	total, inserted, err := svScan(context.Background(), st, svHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != svTotalCount || inserted != svTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, svTotalCount, svTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, k := range svKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := svARN(k, suffix)
			wantARNs = append(wantARNs, arn)
			r, ok := svRowOf(t, st, k.typ, arn)
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if got, want := sv(r.Name), k.wantName(svID(k, suffix)); got != want {
				t.Errorf("%s %s Name = %q; want %q", k.typ, arn, got, want)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("%s %s Region = %q; want %s", k.typ, arn, sv(r.Region), testRegion)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || len(attrs) == 0 {
				t.Errorf("%s %s attributes = %s (err %v); want the listed element", k.typ, arn, r.AttributesJSON, err)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s %s TagsJSON = %q; want nil", k.typ, arn, *r.TagsJSON)
			}
			if !slices.Contains([]string{TypeSMSVoiceNotifyConfiguration, TypeSMSVoiceRcsAgent}, k.typ) {
				continue
			}
			if r.Status == nil || r.CreatedAt == nil {
				t.Errorf("%s %s Status, CreatedAt = %v, %v; want both set", k.typ, arn, r.Status, r.CreatedAt)
			} else if got, err := time.Parse(time.RFC3339, *r.CreatedAt); err != nil || !got.Equal(svCreated) {
				t.Errorf("%s %s CreatedAt = %q; want %s", k.typ, arn, *r.CreatedAt, svCreated)
			}
		}
	}
	if r, _ := svRowOf(t, st, TypeSMSVoiceNotifyConfiguration, svARN(svKindOf(t, "DescribeNotifyConfigurations"), "tagged")); sv(r.Status) != "ACTIVE" {
		t.Errorf("notify configuration Status = %q; want ACTIVE", sv(r.Status))
	}
	if r, _ := svRowOf(t, st, TypeSMSVoiceRcsAgent, svARN(svKindOf(t, "DescribeRcsAgents"), "tagged")); sv(r.Status) != "TESTING" {
		t.Errorf("RCS agent Status = %q; want TESTING", sv(r.Status))
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for _, k := range svKinds() {
		if got := calls.maxResults[k.op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v; want [100 100]", k.op, got)
		}
	}
}

// An element with an empty status enum stores no Status.
func TestScanSMSVoice_EmptyStatusStoresNil(t *testing.T) {
	for _, op := range []string{"DescribeNotifyConfigurations", "DescribeRcsAgents"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := svHandlers(newSVCalls())
			k := svKindOf(t, op)
			arn := svARN(k, "tagged")
			handlers[op] = func(any) (any, error) {
				switch out := k.page(&arn, svID(k, "tagged"), nil).(type) {
				case *pinpointsmsvoicev2.DescribeNotifyConfigurationsOutput:
					out.NotifyConfigurations[0].Status = ""
					return out, nil
				case *pinpointsmsvoicev2.DescribeRcsAgentsOutput:
					out.RcsAgents[0].Status = ""
					return out, nil
				}
				return nil, errors.New("test: unexpected kind")
			}
			if _, _, err := svScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := svRowOf(t, st, k.typ, arn)
			if !ok {
				t.Fatal("row missing")
			}
			if r.Status != nil {
				t.Errorf("Status = %q; want nil", *r.Status)
			}
		})
	}
}

// A rescan stores the same rows without inserting any.
func TestScanSMSVoice_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newSVCalls()
	if _, _, err := svScan(context.Background(), st, svHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := svScan(context.Background(), st, svHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != svTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, svTotalCount)
	}
}

// A failed tag read of an own-account row: ResourceNotFoundException, the
// op's modeled not-found, drops the row silently; a denial keeps the row
// untagged and warns once under sms-voice:ListTagsForResource; a throttle
// after retries keeps it and is a transient error; ValidationException keeps
// it and is a hard error. Each holds for a type whose listing can return
// shared rows and for one whose cannot.
func TestScanSMSVoice_TagReadFailures(t *testing.T) {
	for _, op := range []string{"DescribePools", "DescribeRegistrations"} {
		k := svKindOf(t, op)
		arn := svARN(k, "tagged")
		t.Run(op, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", svNotFound, 0, "", svTotalCount - 1, false, 0},
				{"denied", svDenied, 0, "", svTotalCount, true, 1},
				{"transient", svThrottle, outcomeTransient, "", svTotalCount, true, 0},
				{"other error", svValidation, outcomeError, "ValidationException", svTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, svTagsOp)
					calls := newSVCalls()
					calls.errs[arn] = c.err

					total, _, err := svScan(context.Background(), st, svHandlers(calls))
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
					r, ok := svRowOf(t, st, k.typ, arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", svTagsOp, *warnings, *all, c.wantWarns)
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

// A row of a type whose Describe op takes an Owner filter, listed under
// another account's ARN (shared with this one), is tag-read under that ARN.
// A denial or a not-found answer keeps it untagged without a warning; any
// other error keeps it and is returned.
func TestScanSMSVoice_SharedRowTagRead(t *testing.T) {
	for _, k := range svKinds() {
		if !k.shared {
			continue
		}
		for _, c := range []struct {
			name    string
			err     error
			wantErr bool
		}{
			{"denied", svDenied, false},
			{"not found", svNotFound, false},
			{"other error", svValidation, true},
		} {
			t.Run(k.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				calls := newSVCalls()
				foreign := svARNIn("aws", testRegion, svOtherAcct, k, svID(k, "shared"))
				calls.errs[foreign] = c.err
				handlers := svHandlers(calls)
				handlers[k.op] = func(any) (any, error) { return k.page(&foreign, svID(k, "shared"), nil), nil }

				_, _, err := svScan(context.Background(), st, handlers)
				if c.wantErr {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, "ValidationException")
				} else if err != nil {
					t.Fatalf("scan: %v", err)
				}
				r, ok := svRowOf(t, st, k.typ, foreign)
				if !ok {
					t.Fatal("shared row missing")
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				if !slices.Contains(calls.tagARNs, foreign) {
					t.Errorf("shared row %s not sent to the tag op", foreign)
				}
				if *warns != 0 {
					t.Errorf("warnings = %d; want 0", *warns)
				}
			})
		}
	}
}

// A row of a type whose Describe op takes no Owner filter keeps the usual
// rules even under another account's ARN: a not-found tag read drops it.
func TestScanSMSVoice_UnsharedTypeForeignRowNotFoundDropped(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newSVCalls()
	k := svKindOf(t, "DescribeConfigurationSets")
	foreign := svARNIn("aws", testRegion, svOtherAcct, k, svID(k, "foreign"))
	calls.errs[foreign] = svNotFound
	handlers := svHandlers(calls)
	handlers[k.op] = func(any) (any, error) { return k.page(&foreign, svID(k, "foreign"), nil), nil }

	if _, _, err := svScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := svRowOf(t, st, k.typ, foreign); ok {
		t.Error("configuration set the tag read did not find was stored")
	}
	if !slices.Contains(calls.tagARNs, foreign) {
		t.Errorf("%s not sent to the tag op", foreign)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// An element whose primary name field is empty is named by its fallback:
// the ARN, or the id for phone numbers, verified destination numbers and
// notify configurations.
func TestScanSMSVoice_NameFallbacks(t *testing.T) {
	for _, k := range svKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := svHandlers(newSVCalls())
			arn := svARN(k, "unnamed")
			id := svID(k, "unnamed")
			want := arn
			var out any
			switch k.op {
			case "DescribePhoneNumbers":
				out = &pinpointsmsvoicev2.DescribePhoneNumbersOutput{PhoneNumbers: []svtypes.PhoneNumberInformation{{PhoneNumberArn: &arn, PhoneNumberId: &id}}}
				want = id
			case "DescribeVerifiedDestinationNumbers":
				out = &pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersOutput{VerifiedDestinationNumbers: []svtypes.VerifiedDestinationNumberInformation{{VerifiedDestinationNumberArn: &arn, VerifiedDestinationNumberId: &id}}}
				want = id
			case "DescribeNotifyConfigurations":
				out = &pinpointsmsvoicev2.DescribeNotifyConfigurationsOutput{NotifyConfigurations: []svtypes.NotifyConfigurationInformation{{NotifyConfigurationArn: &arn, NotifyConfigurationId: &id}}}
				want = id
			default:
				out = k.page(&arn, "", nil)
			}
			handlers[k.op] = func(any) (any, error) { return out, nil }

			if _, _, err := svScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := svRowOf(t, st, k.typ, arn)
			if !ok {
				t.Fatal("row missing")
			}
			if sv(r.Name) != want {
				t.Errorf("Name = %q; want %q", sv(r.Name), want)
			}
		})
	}
}

// An element with neither an ARN nor an id is warned about under a key that
// is never empty.
func TestScanSMSVoice_ElementWithoutIdentifierWarnsWithKey(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, "sms-voice:DescribePools")
	handlers := svHandlers(newSVCalls())
	k := svKindOf(t, "DescribePools")
	handlers[k.op] = func(any) (any, error) { return k.page(nil, "", nil), nil }

	if _, _, err := svScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warns) != 1 {
		t.Fatalf("warnings = %v; want 1", *warns)
	}
	// The key is the value the warning names the element by, not prose.
	if !strings.Contains((*warns)[0].Message, "(an element with no identifier)") {
		t.Errorf("warning %q does not name the element by the fallback key", (*warns)[0].Message)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSMSVoice_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, svTagsOp)
	calls := newSVCalls()
	calls.errs[svARN(svKindOf(t, "DescribeSenderIds"), "tagged")] = svThrottle
	calls.errs[svARN(svKindOf(t, "DescribeConfigurationSets"), "bare")] = svValidation

	_, _, err := svScan(context.Background(), st, svHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", svTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and stores nothing of its
// type; every other listing still runs.
func TestScanSMSVoice_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, k := range svKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "sms-voice:"+k.op)
			handlers := svHandlers(newSVCalls())
			delete(handlers, k.op)

			total, _, err := svScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != svTotalCount-2 {
				t.Errorf("total = %d; want %d", total, svTotalCount-2)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("sms-voice:%s warnings = %v (all ops %d); want 1", k.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanSMSVoice_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeSMSVoiceConfigurationSet)
	calls := newSVCalls()
	calls.errs[svARN(svKindOf(t, "DescribeConfigurationSets"), "tagged")] = svDenied

	_, _, err := svScan(context.Background(), st, svHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("DescribeOptOutLists"); n != 0 {
		t.Errorf("DescribeOptOutLists called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanSMSVoice_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := svHandlers(newSVCalls())
	k := svKindOf(t, "DescribePhoneNumbers")
	handlers[k.op] = failOnCall(2, svValidation, handlers[k.op])

	total, _, err := svScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != svTotalCount-1 {
		t.Errorf("total = %d; want %d", total, svTotalCount-1)
	}
	if _, ok := svRowOf(t, st, k.typ, svARN(k, "tagged")); !ok {
		t.Error("phone number from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeSMSVoiceRcsAgent)); got != 2 {
		t.Errorf("RCS agents stored = %d; want 2", got)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanSMSVoice_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := svHandlers(newSVCalls())
	k := svKindOf(t, "DescribeRegistrationAttachments")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if svTokenOf(in) != nil {
			suffix = "bare"
		}
		return k.page(sdkaws.String(svARN(k, suffix)), svID(k, suffix), sdkaws.String("same")), nil
	}

	total, _, err := svScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != svTotalCount {
		t.Errorf("total = %d; want %d", total, svTotalCount)
	}
	for _, suffix := range []string{"tagged", "bare"} {
		if _, ok := svRowOf(t, st, k.typ, svARN(k, suffix)); !ok {
			t.Errorf("registration attachment %s from the pages read missing", suffix)
		}
	}
}

// An element without an ARN is warned about under its listing, not stored.
func TestScanSMSVoice_ElementWithoutARNWarns(t *testing.T) {
	for _, k := range svKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "sms-voice:"+k.op)
			handlers := svHandlers(newSVCalls())
			handlers[k.op] = func(any) (any, error) { return k.page(nil, svID(k, "no-arn"), nil), nil }

			total, _, err := svScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != svTotalCount-2 {
				t.Errorf("total = %d; want %d", total, svTotalCount-2)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Errorf("sms-voice:%s warnings = %v (all ops %d); want 1", k.op, *warns, *all)
			}
		})
	}
}

// Rows of an own account in another partition are sent to the tag op under
// the ARNs their listings return, and keep the usual own-account rules.
func TestScanSMSVoice_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	k := svKindOf(t, "DescribePools")
	tagged := svARNIn("aws-us-gov", region, testAccountID, k, "pool-gov")
	gone := svARNIn("aws-us-gov", region, testAccountID, k, "pool-gone")
	st := newTestStore(t)
	calls := newSVCalls()
	calls.tags[tagged] = map[string]string{"k": "v"}
	calls.errs[gone] = svNotFound
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"DescribePools": func(any) (any, error) {
			return &pinpointsmsvoicev2.DescribePoolsOutput{Pools: []svtypes.PoolInformation{
				{PoolArn: &tagged, PoolId: sdkaws.String("pool-gov")},
				{PoolArn: &gone, PoolId: sdkaws.String("pool-gone")},
			}}, nil
		},
	}, svDenied)

	if _, _, err := scanSMSVoice(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(calls.tagARNs)
	if want := []string{gone, tagged}; !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	rows := rowsByNativeID(t, st, TypeSMSVoicePool)
	r, ok := rows[tagged]
	if !ok {
		t.Fatal("gov pool missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	if _, ok := rows[gone]; ok {
		t.Error("gov pool the tag read did not find was stored")
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanSMSVoice_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newSVCalls()
	handlers := svHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pools := handlers["DescribePools"]
	handlers["DescribePools"] = func(in any) (any, error) {
		if svTokenOf(in) != nil {
			cancel()
			return nil, context.Canceled
		}
		return pools(in)
	}

	_, _, err := svScan(ctx, st, handlers)
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
	for _, op := range []string{"DescribeProtectConfigurations", "DescribeRcsAgents"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanSMSVoice_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "sms-voice.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := svHandlers(newSVCalls())
		handlers["DescribeRcsAgents"] = func(any) (any, error) { return nil, nx }
		total, _, err := svScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != svTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, svTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSVCalls()
		calls.errs[svARN(svKindOf(t, "DescribeConfigurationSets"), "bare")] = nx
		_, _, err := svScan(context.Background(), st, svHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanSMSVoice(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

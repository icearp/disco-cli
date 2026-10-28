package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotwireless"
	iwtypes "github.com/aws/aws-sdk-go-v2/service/iotwireless/types"
	"github.com/icearp/disco-cli/store"
)

const (
	iwTestARNPrefix   = "arn:aws:iotwireless:us-east-1:123456789012:"
	iwTestDevice      = iwTestARNPrefix + "WirelessDevice/dev-1"
	iwTestGateway     = iwTestARNPrefix + "WirelessGateway/gw-1"
	iwTestPartner     = iwTestARNPrefix + "SidewalkAccount/amzn-1"
	iwTestTaskDef     = iwTestARNPrefix + "WirelessGatewayTaskDefinition/td-1"
	iwTestCfgDevice   = iwTestDevice + "/event-configuration"
	iwTestCfgDevEui   = iwTestARNPrefix + "DevEui/a1b2c3d4e5f60708/event-configuration"
	iwTestCfgUnlisted = iwTestARNPrefix + "WirelessDevice/dev-unlisted/event-configuration"
	iwTestCfgGateway  = iwTestGateway + "/event-configuration"
	iwTestCfgGwEui    = iwTestARNPrefix + "GatewayEui/b1b2c3d4e5f60708/event-configuration"
	iwTestCfgPartner  = iwTestPartner + "/event-configuration"

	// iwListedCount is the rows the listings store: two per listing, one per
	// page.
	iwListedCount = 22
	// iwTotalCount adds the event configurations iwHandlers lists.
	iwTotalCount = iwListedCount + 6
)

var (
	iwDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: iotwireless:ListEventConfigurations")
	iwGated    = apiErr("AccessDeniedException", "Account 123456789012 is not authorized to call this API in this region us-west-2")
	iwBadInput = apiErr("ValidationException", "bad")
	iwBusy     = apiErr("InternalServerException", "try later")
	iwConflict = apiErr("ConflictException", "unexpected")
	iwThrottle = apiErr("ThrottlingException", "slow down")
	iwNotFound = apiErr("ResourceNotFoundException", "gone")

	// iwTestCfgRows are the event configurations iwHandlers lists, by resource type.
	iwTestCfgRows = map[iwtypes.EventNotificationResourceType][]string{
		iwtypes.EventNotificationResourceTypeWirelessDevice:  {iwTestCfgDevice, iwTestCfgDevEui, iwTestCfgUnlisted},
		iwtypes.EventNotificationResourceTypeWirelessGateway: {iwTestCfgGateway, iwTestCfgGwEui},
		iwtypes.EventNotificationResourceTypeSidewalkAccount: {iwTestCfgPartner},
	}
)

// iwKind is one listing of the fixture: its op, the type it stores, the ARN
// resource type and id base of its rows, and page, which builds the op's
// output holding the one element of id under arn, with next. nameOf is the
// Name a row is stored under; nil for the id.
type iwKind struct {
	op, typ, arnType, idBase string
	page                     func(arn, id string, next *string) any
	nameOf                   func(k iwKind, suffix string) string
}

// id is the id of the row on the listing's first page ("tagged") or second
// ("bare").
func (k iwKind) id(suffix string) string {
	if suffix == "tagged" {
		return k.idBase + "-1"
	}
	return k.idBase + "-2"
}

func (k iwKind) name(suffix string) string {
	if k.nameOf == nil {
		return k.id(suffix)
	}
	return k.nameOf(k, suffix)
}

// iwNamed is the element Name of a kind that lists both a Name and an Id: the
// id with a "name-" prefix, so a Name taken from the Id shows.
func iwNamed(id string) string { return "name-" + id }

// iwNameOrARN is nameOf for a kind whose "bare" element has no Name and is
// stored under its Arn.
func iwNameOrARN(k iwKind, suffix string) string {
	if suffix == "bare" {
		return k.arn(suffix)
	}
	return k.id(suffix)
}

func iwNameFirst(k iwKind, suffix string) string { return iwNamed(k.id(suffix)) }

func (k iwKind) arn(suffix string) string { return iwTestARNPrefix + k.arnType + "/" + k.id(suffix) }

func iwKinds() []iwKind {
	s := sdkaws.String
	return []iwKind{
		{"ListDestinations", TypeIoTWirelessDestination, "Destination", "dest", func(arn, id string, next *string) any {
			d := iwtypes.Destinations{Arn: s(arn)}
			if strings.HasSuffix(id, "-1") {
				d.Name = s(id)
			}
			return &iotwireless.ListDestinationsOutput{DestinationList: []iwtypes.Destinations{d}, NextToken: next}
		}, iwNameOrARN},
		{"ListDeviceProfiles", TypeIoTWirelessDeviceProfile, "DeviceProfile", "dp", func(arn, id string, next *string) any {
			return &iotwireless.ListDeviceProfilesOutput{DeviceProfileList: []iwtypes.DeviceProfile{{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}}, NextToken: next}
		}, iwNameFirst},
		{"ListFuotaTasks", TypeIoTWirelessFuotaTask, "FuotaTask", "fuota", func(arn, id string, next *string) any {
			return &iotwireless.ListFuotaTasksOutput{FuotaTaskList: []iwtypes.FuotaTask{{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}}, NextToken: next}
		}, iwNameFirst},
		{"ListMulticastGroups", TypeIoTWirelessMulticastGroup, "MulticastGroup", "mc", func(arn, id string, next *string) any {
			return &iotwireless.ListMulticastGroupsOutput{MulticastGroupList: []iwtypes.MulticastGroup{{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}}, NextToken: next}
		}, iwNameFirst},
		{"ListNetworkAnalyzerConfigurations", TypeIoTWirelessNetworkAnalyzerConfiguration, "NetworkAnalyzerConfiguration", "nac", func(arn, id string, next *string) any {
			n := iwtypes.NetworkAnalyzerConfigurations{Arn: s(arn)}
			if strings.HasSuffix(id, "-1") {
				n.Name = s(id)
			}
			return &iotwireless.ListNetworkAnalyzerConfigurationsOutput{NetworkAnalyzerConfigurationList: []iwtypes.NetworkAnalyzerConfigurations{n}, NextToken: next}
		}, iwNameOrARN},
		{"ListPartnerAccounts", TypeIoTWirelessPartnerAccount, "SidewalkAccount", "amzn", func(arn, id string, next *string) any {
			return &iotwireless.ListPartnerAccountsOutput{Sidewalk: []iwtypes.SidewalkAccountInfoWithFingerprint{{Arn: s(arn), AmazonId: s(id)}}, NextToken: next}
		}, nil},
		{"ListServiceProfiles", TypeIoTWirelessServiceProfile, "ServiceProfile", "sp", func(arn, id string, next *string) any {
			return &iotwireless.ListServiceProfilesOutput{ServiceProfileList: []iwtypes.ServiceProfile{{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}}, NextToken: next}
		}, iwNameFirst},
		{"ListWirelessDevices", TypeIoTWirelessWirelessDevice, "WirelessDevice", "dev", func(arn, id string, next *string) any {
			d := iwtypes.WirelessDeviceStatistics{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}
			if id == "dev-1" {
				d.LoRaWAN = &iwtypes.LoRaWANListDevice{DevEui: s("a1b2c3d4e5f60708")}
			}
			return &iotwireless.ListWirelessDevicesOutput{WirelessDeviceList: []iwtypes.WirelessDeviceStatistics{d}, NextToken: next}
		}, iwNameFirst},
		{"ListWirelessDeviceImportTasks", TypeIoTWirelessWirelessDeviceImportTask, "ImportTask", "imp", func(arn, id string, next *string) any {
			return &iotwireless.ListWirelessDeviceImportTasksOutput{WirelessDeviceImportTaskList: []iwtypes.WirelessDeviceImportTask{{Arn: s(arn), Id: s(id)}}, NextToken: next}
		}, nil},
		{"ListWirelessGateways", TypeIoTWirelessWirelessGateway, "WirelessGateway", "gw", func(arn, id string, next *string) any {
			g := iwtypes.WirelessGatewayStatistics{Arn: s(arn), Id: s(id), Name: s(iwNamed(id))}
			if id == "gw-1" {
				g.LoRaWAN = &iwtypes.LoRaWANGateway{GatewayEui: s("b1b2c3d4e5f60708")}
			}
			return &iotwireless.ListWirelessGatewaysOutput{WirelessGatewayList: []iwtypes.WirelessGatewayStatistics{g}, NextToken: next}
		}, iwNameFirst},
		{"ListWirelessGatewayTaskDefinitions", TypeIoTWirelessTaskDefinition, "WirelessGatewayTaskDefinition", "td", func(arn, id string, next *string) any {
			return &iotwireless.ListWirelessGatewayTaskDefinitionsOutput{TaskDefinitions: []iwtypes.UpdateWirelessGatewayTaskEntry{{Arn: s(arn), Id: s(id)}}, NextToken: next}
		}, nil},
	}
}

func iwKindOf(t *testing.T, op string) iwKind {
	t.Helper()
	for _, k := range iwKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return iwKind{}
}

// iwCalls records the stubbed calls: how often each op ran, the MaxResults
// each listing was sent, the event-configuration resource types listed, and
// the ARNs sent to ListTagsForResource, which it answers from tags or errs,
// both keyed by ARN. Tag reads run concurrently.
type iwCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]int32
	evTypes    parentCalls
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newIWCalls tags every "tagged" row with its type.
func newIWCalls() *iwCalls {
	c := &iwCalls{ops: map[string]int{}, maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range iwKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *iwCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], bacField[int32](in, "MaxResults"))
}

func (c *iwCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *iwCalls) listTags(in any) (any, error) {
	arn := sv(in.(*iotwireless.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []iwtypes.Tag
	for k, v := range tags {
		list = append(list, iwtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &iotwireless.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *iwCalls) twoPages(k iwKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.arn(suffix), k.id(suffix), next), nil
	}
}

// iwCfgRowsExcept returns the event configuration NativeIDs of every resource
// type but skip.
func iwCfgRowsExcept(skip ...iwtypes.EventNotificationResourceType) []string {
	var out []string
	for rt, ids := range iwTestCfgRows {
		if !slices.Contains(skip, rt) {
			out = append(out, ids...)
		}
	}
	return out
}

// iwHandlers answers every IoT Wireless op the scan calls. Each listing
// answers two pages (iwCalls.twoPages); the "tagged" wireless device and
// gateway carry a DevEui and a GatewayEui. ListEventConfigurations answers
// WirelessDevice in two pages (with an identifier-less item to skip and a
// device no listing returned), WirelessGateway by id and by EUI and
// SidewalkAccount by partner account id; every call records its resource
// type in c.evTypes.
func iwHandlers(c *iwCalls) map[string]func(in any) (any, error) {
	item := func(id string, idType iwtypes.IdentifierType) iwtypes.EventConfigurationItem {
		return iwtypes.EventConfigurationItem{
			Identifier: sdkaws.String(id), IdentifierType: idType,
			Events: &iwtypes.EventNotificationItemConfigurations{
				Join: &iwtypes.JoinEventConfiguration{WirelessDeviceIdEventTopic: iwtypes.EventNotificationTopicStatusEnabled},
			},
		}
	}
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListEventConfigurations": func(in any) (any, error) {
			p := in.(*iotwireless.ListEventConfigurationsInput)
			c.record("ListEventConfigurations", in)
			c.evTypes.add(string(p.ResourceType))
			var out iotwireless.ListEventConfigurationsOutput
			switch p.ResourceType {
			case iwtypes.EventNotificationResourceTypeWirelessDevice:
				out.EventConfigurationsList, out.NextToken = evsPage(p.NextToken,
					[]iwtypes.EventConfigurationItem{item("dev-1", iwtypes.IdentifierTypeWirelessDeviceId), {IdentifierType: iwtypes.IdentifierTypeWirelessDeviceId}},
					[]iwtypes.EventConfigurationItem{item("a1b2c3d4e5f60708", iwtypes.IdentifierTypeDevEui), item("dev-unlisted", iwtypes.IdentifierTypeWirelessDeviceId)})
			case iwtypes.EventNotificationResourceTypeWirelessGateway:
				out.EventConfigurationsList = []iwtypes.EventConfigurationItem{
					item("gw-1", iwtypes.IdentifierTypeWirelessGatewayId), item("b1b2c3d4e5f60708", iwtypes.IdentifierTypeGatewayEui),
				}
			case iwtypes.EventNotificationResourceTypeSidewalkAccount:
				partner := item("amzn-1", iwtypes.IdentifierTypePartnerAccountId)
				partner.PartnerType = iwtypes.EventNotificationPartnerTypeSidewalk
				out.EventConfigurationsList = []iwtypes.EventConfigurationItem{partner}
			default:
				return nil, iwConflict
			}
			return &out, nil
		},
	}
	for _, k := range iwKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// iwScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func iwScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iwDenied)
	return scanIoTWireless(ctx, acct, testRegion, st, testScanID)
}

func scanIoTWirelessTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := iwScan(context.Background(), st, handlers)
	return total, err
}

// failIWEventConfigs makes ListEventConfigurations fail with err for the
// resource types in failing and answer the others as before.
func failIWEventConfigs(handlers map[string]func(in any) (any, error), err error, failing ...iwtypes.EventNotificationResourceType) {
	base := handlers["ListEventConfigurations"]
	handlers["ListEventConfigurations"] = func(in any) (any, error) {
		if slices.Contains(failing, in.(*iotwireless.ListEventConfigurationsInput).ResourceType) {
			return nil, err
		}
		return base(in)
	}
}

// assertIWCfgRows fails unless the stored event configurations are exactly want.
func assertIWCfgRows(t *testing.T, st *store.Store, want []string) {
	t.Helper()
	var got []string
	for id := range rowsByNativeID(t, st, TypeIoTWirelessEventConfiguration) {
		got = append(got, id)
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("event configuration rows = %v; want %v", got, want)
	}
}

// Every listing stores both its pages, each row under its Arn and named by
// its name or id, with the tags ListTagsForResource returns for its Arn, and
// nil for a resource without tags. Exactly the Arn of every listed row is
// sent to the tag op; every listing is sent the model's page cap of 250. No
// error or warning.
func TestScanIoTWireless_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newIWCalls()

	total, inserted, err := iwScan(context.Background(), st, iwHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != iwTotalCount || inserted != iwTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, iwTotalCount, iwTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, k := range iwKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			wantARNs = append(wantARNs, arn)
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion {
				t.Errorf("%s Name, Region = %q, %q; want %q, %q", arn, sv(r.Name), sv(r.Region), k.name(suffix), testRegion)
			}
			if !strings.Contains(r.AttributesJSON, `"Arn":"`+arn+`"`) {
				t.Errorf("%s attrs = %s; want the listed element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, n := range calls.maxResults[k.op] {
			if n != 250 {
				t.Errorf("%s MaxResults = %d; want 250", k.op, n)
			}
		}
	}
	for _, n := range calls.maxResults["ListEventConfigurations"] {
		if n != 250 {
			t.Errorf("ListEventConfigurations MaxResults = %d; want 250", n)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanIoTWireless_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := iwScan(context.Background(), st, iwHandlers(newIWCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := iwScan(context.Background(), st, iwHandlers(newIWCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != iwTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, iwTotalCount)
	}
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently; a denial keeps the row untagged and warns once
// under iotwireless:ListTagsForResource; a throttle after retries keeps it
// and is a transient error; ValidationException keeps it and is a hard
// error.
func TestScanIoTWireless_TagReadFailures(t *testing.T) {
	for _, op := range []string{"ListDestinations", "ListWirelessDevices", "ListWirelessGatewayTaskDefinitions"} {
		k := iwKindOf(t, op)
		arn := k.arn("tagged")
		t.Run(k.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", iwNotFound, -1, "", iwTotalCount - 1, false, 0},
				{"denied", iwDenied, -1, "", iwTotalCount, true, 1},
				{"transient", iwThrottle, outcomeTransient, "ThrottlingException", iwTotalCount, true, 0},
				{"other error", iwBadInput, outcomeError, "ValidationException", iwTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, iwTagsOp)
					calls := newIWCalls()
					calls.errs[arn] = c.err

					total, _, err := iwScan(context.Background(), st, iwHandlers(calls))
					if c.outcome < 0 {
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					} else {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := rowsByNativeID(t, st, k.typ)[arn]
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", iwTagsOp, *warnings, *all, c.wantWarns)
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

// A wireless device, gateway or partner account the tag read finds deleted
// is no event-configuration parent: the configurations naming it are stored
// unlinked.
func TestScanIoTWireless_TagNotFoundIsNoEventParent(t *testing.T) {
	for _, c := range []struct{ parent, cfg string }{
		{iwTestDevice, iwTestCfgDevEui},
		{iwTestGateway, iwTestCfgGwEui},
		{iwTestPartner, iwTestCfgPartner},
	} {
		t.Run(c.parent, func(t *testing.T) {
			st := newTestStore(t)
			calls := newIWCalls()
			calls.errs[c.parent] = iwNotFound
			if _, _, err := iwScan(context.Background(), st, iwHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertIWCfgRows(t, st, iwCfgRowsExcept())
			assertNoIWContainer(t, st, c.cfg)
		})
	}
}

// Denied tag reads across several phases send one warning per phase, never
// one per row.
func TestScanIoTWireless_TagDenialsWarnOncePerPhase(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, iwTagsOp)
	calls := newIWCalls()
	k := iwKindOf(t, "ListFuotaTasks")
	calls.errs[k.arn("tagged")] = iwDenied
	calls.errs[k.arn("bare")] = iwDenied

	if _, _, err := iwScan(context.Background(), st, iwHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %d; want 1", iwTagsOp, len(*warnings))
	}
	if got := len(rowsByNativeID(t, st, k.typ)); got != 2 {
		t.Errorf("FUOTA tasks = %d; want 2", got)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanIoTWireless_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iwTagsOp)
	calls := newIWCalls()
	calls.errs[iwKindOf(t, "ListDeviceProfiles").arn("tagged")] = iwThrottle
	calls.errs[iwKindOf(t, "ListServiceProfiles").arn("bare")] = iwBadInput

	_, _, err := iwScan(context.Background(), st, iwHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iwTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanIoTWireless_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeIoTWirelessDestination)
	calls := newIWCalls()
	calls.errs[iwKindOf(t, "ListDestinations").arn("tagged")] = iwDenied

	_, _, err := iwScan(context.Background(), st, iwHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListDeviceProfiles"); n != 0 {
		t.Errorf("ListDeviceProfiles called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanIoTWireless_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	for _, op := range []string{"ListDestinations", "ListPartnerAccounts", "ListWirelessDevices", "ListWirelessGatewayTaskDefinitions"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newIWCalls()
			handlers := iwHandlers(calls)
			k := iwKindOf(t, op)
			handlers[op] = failOnCall(2, iwConflict, handlers[op])

			total, _, err := iwScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ConflictException")
			if total != iwTotalCount-1 {
				t.Errorf("total = %d; want %d", total, iwTotalCount-1)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			assertIWCfgRows(t, st, iwCfgRowsExcept())
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanIoTWireless_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(newIWCalls())
	k := iwKindOf(t, "ListWirelessDeviceImportTasks")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return k.page(k.arn(suffix), k.id(suffix), sdkaws.String("same")), nil
	}

	total, _, err := iwScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != iwTotalCount {
		t.Errorf("total = %d; want %d", total, iwTotalCount)
	}
}

// An element without an Arn is warned about under its listing, named by its
// name or id, or by the fallback text when it has none, and is not stored.
func TestScanIoTWireless_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		page          any
	}{
		{"named", "ListMulticastGroups", "mc-x", &iotwireless.ListMulticastGroupsOutput{MulticastGroupList: []iwtypes.MulticastGroup{{Name: sdkaws.String("mc-x")}}}},
		{"id only", "ListWirelessGatewayTaskDefinitions", "td-x", &iotwireless.ListWirelessGatewayTaskDefinitionsOutput{TaskDefinitions: []iwtypes.UpdateWirelessGatewayTaskEntry{{Id: sdkaws.String("td-x")}}}},
		{"no identifier", "ListDestinations", "(an element with no identifier)", &iotwireless.ListDestinationsOutput{DestinationList: []iwtypes.Destinations{{}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "iotwireless:"+c.op)
			handlers := iwHandlers(newIWCalls())
			handlers[c.op] = func(any) (any, error) { return c.page, nil }

			total, _, err := iwScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != iwTotalCount-2 {
				t.Errorf("total = %d; want %d", total, iwTotalCount-2)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("warnings = %v (all ops %d); want 1", *warns, *all)
			}
			if w := (*warns)[0]; !strings.Contains(w.Message, c.key) || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %q (Scope %q) does not name %q in %s/%s", w.Message, w.Scope, c.key, testAccountID, testRegion)
			}
		})
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the pages read before it. Every other listing still runs.
func TestScanIoTWireless_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct {
		op       string
		call     int
		wantKept int
	}{
		{"ListDeviceProfiles", 1, 0},
		{"ListDeviceProfiles", 2, 1},
		{"ListWirelessGateways", 2, 1},
	} {
		t.Run(fmt.Sprintf("%s/page%d", c.op, c.call), func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "iotwireless:"+c.op)
			handlers := iwHandlers(newIWCalls())
			k := iwKindOf(t, c.op)
			handlers[c.op] = failOnCall(c.call, iwDenied, handlers[c.op])

			total, _, err := iwScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := iwTotalCount - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("iotwireless:%s warnings = %v (all ops %d); want 1", c.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != c.wantKept {
				t.Errorf("%s rows = %d; want %d", k.typ, got, c.wantKept)
			}
		})
	}
}

// ListPartnerAccounts refused on its first page with the region-gated
// message (Sidewalk not offered in the region) ends silently; the same
// refusal after the first page keeps that page and is warned about as the
// denial it is.
func TestScanIoTWireless_PartnerRegionGate(t *testing.T) {
	k := iwKindOf(t, "ListPartnerAccounts")
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		all := countWarnings(st)
		handlers := iwHandlers(newIWCalls())
		handlers[k.op] = func(any) (any, error) { return nil, iwGated }
		total, _, err := iwScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != iwTotalCount-2 || *all != 0 {
			t.Errorf("total = %d, warnings = %d; want %d, 0", total, *all, iwTotalCount-2)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		warnings := bacWarnings(st, "iotwireless:"+k.op)
		handlers := iwHandlers(newIWCalls())
		handlers[k.op] = failOnCall(2, iwGated, handlers[k.op])
		if _, _, err := iwScan(context.Background(), st, handlers); err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
			t.Error("partner account from the first page missing")
		}
		if len(*warnings) != 1 {
			t.Errorf("warnings = %v; want 1", *warnings)
		}
	})
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanIoTWireless_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newIWCalls()
	handlers := iwHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListWirelessDevices"]
	handlers["ListWirelessDevices"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := iwScan(ctx, st, handlers)
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
	for _, op := range []string{"ListWirelessGateways", "ListEventConfigurations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanIoTWireless_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "api.iotwireless.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := iwHandlers(newIWCalls())
		handlers["ListServiceProfiles"] = func(any) (any, error) { return nil, nx }
		total, _, err := iwScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != iwTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, iwTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newIWCalls()
		calls.errs[iwKindOf(t, "ListDestinations").arn("bare")] = nx
		_, _, err := iwScan(context.Background(), st, iwHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanIoTWireless(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

func TestScanIoTWireless_StoresEventConfigurations(t *testing.T) {
	st := newTestStore(t)
	var warnings atomic.Int32
	st.OnWarn = func(store.ScanWarning) { warnings.Add(1) }
	calls := newIWCalls()
	total, err := scanIoTWirelessTopLevel(t, st, iwHandlers(calls))
	if err != nil {
		t.Fatalf("scanIoTWireless: %v", err)
	}
	if n := warnings.Load(); n != 0 {
		t.Errorf("warnings = %d; want 0", n)
	}
	if total != iwTotalCount {
		t.Errorf("total = %d; want %d", total, iwTotalCount)
	}
	assertIWCfgRows(t, st, iwCfgRowsExcept())
	if got, want := calls.evTypes.distinct(), []string{"SidewalkAccount", "WirelessDevice", "WirelessGateway"}; !slices.Equal(got, want) {
		t.Errorf("ListEventConfigurations resource types = %v; want %v", got, want)
	}

	row := rowsByNativeID(t, st, TypeIoTWirelessEventConfiguration)[iwTestCfgPartner]
	var attrs iwtypes.EventConfigurationItem
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode event configuration attrs: %v", err)
	}
	if row.Type != TypeIoTWirelessEventConfiguration || sv(row.Region) != testRegion || sv(row.Name) != "amzn-1" ||
		attrs.PartnerType != iwtypes.EventNotificationPartnerTypeSidewalk || attrs.Events == nil || attrs.Events.Join == nil ||
		attrs.Events.Join.WirelessDeviceIdEventTopic != iwtypes.EventNotificationTopicStatusEnabled {
		t.Errorf("partner event configuration row = type %q region %q name %q attrs %s; want %q %q %q with PartnerType Sidewalk and the Join topic enabled",
			row.Type, sv(row.Region), sv(row.Name), row.AttributesJSON, TypeIoTWirelessEventConfiguration, testRegion, "amzn-1")
	}

	assertContainsNative(t, st, iwTestDevice, iwTestCfgDevice)
	assertContainsNative(t, st, iwTestDevice, iwTestCfgDevEui)
	assertContainsNative(t, st, iwTestGateway, iwTestCfgGateway)
	assertContainsNative(t, st, iwTestGateway, iwTestCfgGwEui)
	assertContainsNative(t, st, iwTestPartner, iwTestCfgPartner)
	assertNoIWContainer(t, st, iwTestCfgUnlisted)
	assertContainsEdgesDeclared(t, st)
}

// assertNoIWContainer fails if any row contains the child.
func assertNoIWContainer(t *testing.T, st *store.Store, childNativeID string) {
	t.Helper()
	rels, err := st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	childID := store.ResourceID("aws", testAccountID, childNativeID)
	for _, r := range rels {
		if r.Kind == store.RelContains && r.ToID == childID {
			t.Errorf("%s is contained by %s; want no parent", childNativeID, r.FromID)
		}
	}
}

func TestScanIoTWireless_NoEventConfigurations(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(newIWCalls())
	handlers["ListEventConfigurations"] = func(any) (any, error) { return &iotwireless.ListEventConfigurationsOutput{}, nil }
	total, err := scanIoTWirelessTopLevel(t, st, handlers)
	if err != nil {
		t.Fatalf("scanIoTWireless: %v", err)
	}
	if total != iwListedCount {
		t.Errorf("total = %d; want %d (the listed rows)", total, iwListedCount)
	}
	assertIWCfgRows(t, st, nil)
}

// TestScanIoTWireless_EventConfigurationErrors fails ListEventConfigurations
// for the case's resource types and checks the warnings, the error and that
// the other resource types' rows are stored.
func TestScanIoTWireless_EventConfigurationErrors(t *testing.T) {
	gateway := iwtypes.EventNotificationResourceTypeWirelessGateway
	device := iwtypes.EventNotificationResourceTypeWirelessDevice
	cases := []struct {
		name      string
		failing   []iwtypes.EventNotificationResourceType
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "denied warns", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwDenied, wantWarns: 1},
		{name: "denied on two types warns once", failing: []iwtypes.EventNotificationResourceType{gateway, device}, err: iwDenied, wantWarns: 1},
		{name: "sidewalk region gap is silent", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwGated},
		{name: "validation exception is returned", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwBadInput, wantCode: "ValidationException"},
		{name: "transients warn once", failing: []iwtypes.EventNotificationResourceType{gateway, device}, err: iwBusy, wantWarns: 1},
		{name: "other error is returned", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwConflict, wantCode: "ConflictException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, iwListEventConfigurationsOp)
			handlers := iwHandlers(newIWCalls())
			failIWEventConfigs(handlers, tc.err, tc.failing...)
			_, err := scanIoTWirelessTopLevel(t, st, handlers)
			if tc.wantCode != "" {
				if !isAPIErrorCode(err, tc.wantCode) || isTransientNetworkError(err) {
					t.Fatalf("err = %v; want %s", err, tc.wantCode)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", iwListEventConfigurationsOp, *warns, tc.wantWarns)
			}
			assertIWCfgRows(t, st, iwCfgRowsExcept(tc.failing...))
		})
	}
}

// TestScanIoTWireless_EventConfigurationPageErrorKeepsRows fails the second
// WirelessDevice page and checks the first page's row is kept.
func TestScanIoTWireless_EventConfigurationPageErrorKeepsRows(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iwListEventConfigurationsOp)
	handlers := iwHandlers(newIWCalls())
	base := handlers["ListEventConfigurations"]
	handlers["ListEventConfigurations"] = func(in any) (any, error) {
		if in.(*iotwireless.ListEventConfigurationsInput).NextToken != nil {
			return nil, iwBusy
		}
		return base(in)
	}
	if _, err := scanIoTWirelessTopLevel(t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
	assertIWCfgRows(t, st, append(iwCfgRowsExcept(iwtypes.EventNotificationResourceTypeWirelessDevice), iwTestCfgDevice))
}

func TestScanIoTWireless_EventConfigurationRepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(newIWCalls())
	var calls atomic.Int32
	handlers["ListEventConfigurations"] = func(any) (any, error) {
		if calls.Add(1) > 10 {
			return nil, iwConflict
		}
		return &iotwireless.ListEventConfigurationsOutput{NextToken: sdkaws.String("same")}, nil
	}
	if _, err := scanIoTWirelessTopLevel(t, st, handlers); !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	// Each resource type: the first call, then one call with the repeated token.
	if n := calls.Load(); n != 6 {
		t.Errorf("ListEventConfigurations calls = %d; want 6", n)
	}
}

func TestScanIoTWireless_EventConfigurationErrorAfterEveryOlderPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(newIWCalls())
	handlers["ListEventConfigurations"] = func(any) (any, error) { return nil, iwConflict }
	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want ConflictException", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTWirelessTaskDefinition)[iwTestTaskDef]; !ok {
		t.Errorf("task definition %s not stored; the last listing must run before event configurations", iwTestTaskDef)
	}
}

func TestScanIoTWireless_OlderTransientDoesNotMaskEventConfigurationError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iotwireless:ListFuotaTasks")
	handlers := iwHandlers(newIWCalls())
	handlers["ListFuotaTasks"] = func(any) (any, error) { return nil, iwBusy }
	failIWEventConfigs(handlers, iwConflict, iwtypes.EventNotificationResourceTypeWirelessGateway)
	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") || isTransientNetworkError(err) {
		t.Fatalf("err = %v; want the event configuration ConflictException, not transient", err)
	}
	if *warns != 1 {
		t.Errorf("iotwireless:ListFuotaTasks warnings = %d; want 1 (the demoted error)", *warns)
	}
}

// A failed event-configuration write sends neither the denial nor the
// transient warning, which would claim rows the write lost.
func TestScanIoTWireless_EventConfigurationFailedWriteSendsNoWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iwListEventConfigurationsOp)
	refuseStoreWrites(t, st, TypeIoTWirelessEventConfiguration)
	handlers := iwHandlers(newIWCalls())
	failIWEventConfigs(handlers, iwDenied, iwtypes.EventNotificationResourceTypeWirelessGateway)
	failIWEventConfigs(handlers, iwBusy, iwtypes.EventNotificationResourceTypeSidewalkAccount)

	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("%s warnings = %d; want 0", iwListEventConfigurationsOp, *warns)
	}
}

// The region-gated refusal is silent only on a resource type's first page:
// on a later page it keeps the pages read and is warned about as a denial.
func TestScanIoTWireless_EventConfigurationRegionGateLaterPageWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iwListEventConfigurationsOp)
	handlers := iwHandlers(newIWCalls())
	base := handlers["ListEventConfigurations"]
	handlers["ListEventConfigurations"] = func(in any) (any, error) {
		if in.(*iotwireless.ListEventConfigurationsInput).NextToken != nil {
			return nil, iwGated
		}
		return base(in)
	}
	if _, err := scanIoTWirelessTopLevel(t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iwListEventConfigurationsOp, *warns)
	}
	assertIWCfgRows(t, st, append(iwCfgRowsExcept(iwtypes.EventNotificationResourceTypeWirelessDevice), iwTestCfgDevice))
}

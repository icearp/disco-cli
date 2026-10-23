package aws

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ivs"
	ivstypes "github.com/aws/aws-sdk-go-v2/service/ivs/types"
	"github.com/aws/aws-sdk-go-v2/service/ivsrealtime"
	ivsrealtimetypes "github.com/aws/aws-sdk-go-v2/service/ivsrealtime/types"
	"github.com/icearp/disco-cli/store"
)

var ivsTestTags = map[string]string{"team": "live"}

func ivsTestARN(kind, id string) string {
	return "arn:aws:ivs:us-east-1:123456789012:" + kind + "/" + id
}

type ivsElem struct {
	arn, name *string
	tags      map[string]string
}

// ivsPage is page p (0 or 1) of a listing of kind, ids prefixed by prefix:
// page 0 holds one tagged, named element; page 1 one untagged, unnamed
// element and, when noARN, one with no Arn.
func ivsPage(kind, prefix string, p int, noARN bool) []ivsElem {
	if p == 0 {
		return []ivsElem{{arn: ptrStr(ivsTestARN(kind, prefix+"tagged")), name: ptrStr(kind + "-" + prefix + "tagged"), tags: ivsTestTags}}
	}
	es := []ivsElem{{arn: ptrStr(ivsTestARN(kind, prefix+"untagged"))}}
	if noARN {
		es = append(es, ivsElem{name: ptrStr(kind + "-" + prefix + "orphan")})
	}
	return es
}

// ivsTwoPages serves a listing as two pages: a call without NextToken gets
// serve's page 0 with NextToken "t1", a call with "t1" gets page 1. A token
// not forwarded therefore repeats page 0 and ends the listing with
// errRepeatedNextToken. Every call must ask for wantMax, the op's model cap.
func ivsTwoPages(t *testing.T, op string, wantMax int32, serve func(in any, p int) any) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := reflect.ValueOf(in).Elem()
		if mr := req.FieldByName("MaxResults").Interface().(*int32); mr == nil || *mr != wantMax {
			t.Errorf("%s MaxResults = %v, want %d", op, mr, wantMax)
		}
		switch token := sv(req.FieldByName("NextToken").Interface().(*string)); token {
		case "":
			out := serve(in, 0)
			reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
			return out, nil
		case "t1":
			return serve(in, 1), nil
		default:
			t.Errorf("%s NextToken = %q, want t1", op, token)
			return nil, errors.New("bad token")
		}
	}
}

// ivsHandlers serves every IVS listing as two pages (ivsPage), with two
// channels, "tagged" and "untagged", each holding stream keys. A stream-key
// call for a channel not listed, or a filtered listing, fails the test.
func ivsHandlers(t *testing.T, noARN bool) map[string]func(in any) (any, error) {
	page := func(kind string, p int) []ivsElem { return ivsPage(kind, "", p, noARN) }
	return map[string]func(in any) (any, error){
		"ListChannels": ivsTwoPages(t, "ListChannels", 100, func(in any, p int) any {
			if req := in.(*ivs.ListChannelsInput); req.FilterByName != nil || req.FilterByAdConfigurationArn != nil ||
				req.FilterByPlaybackRestrictionPolicyArn != nil || req.FilterByRecordingConfigurationArn != nil {
				t.Errorf("ListChannels filtered: %+v; want every channel", req)
			}
			out := &ivs.ListChannelsOutput{}
			for _, e := range page("channel", p) {
				out.Channels = append(out.Channels, ivstypes.ChannelSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListStreamKeys": ivsTwoPages(t, "ListStreamKeys", 50, func(in any, p int) any {
			ch := sv(in.(*ivs.ListStreamKeysInput).ChannelArn)
			id, ok := strings.CutPrefix(ch, ivsTestARN("channel", ""))
			if !ok || (id != "tagged" && id != "untagged") {
				t.Errorf("ListStreamKeys ChannelArn = %q, want a listed channel", ch)
			}
			out := &ivs.ListStreamKeysOutput{}
			for _, e := range ivsPage("stream-key", id+"-", p, noARN) {
				out.StreamKeys = append(out.StreamKeys, ivstypes.StreamKeySummary{Arn: e.arn, ChannelArn: &ch, Tags: e.tags})
			}
			return out
		}),
		"ListAdConfigurations": ivsTwoPages(t, "ListAdConfigurations", 100, func(_ any, p int) any {
			out := &ivs.ListAdConfigurationsOutput{}
			for _, e := range page("ad-configuration", p) {
				out.AdConfigurations = append(out.AdConfigurations, ivstypes.AdConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListPlaybackKeyPairs": ivsTwoPages(t, "ListPlaybackKeyPairs", 100, func(_ any, p int) any {
			out := &ivs.ListPlaybackKeyPairsOutput{}
			for _, e := range page("playback-key", p) {
				out.KeyPairs = append(out.KeyPairs, ivstypes.PlaybackKeyPairSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListPlaybackRestrictionPolicies": ivsTwoPages(t, "ListPlaybackRestrictionPolicies", 100, func(_ any, p int) any {
			out := &ivs.ListPlaybackRestrictionPoliciesOutput{}
			for _, e := range page("playback-restriction-policy", p) {
				out.PlaybackRestrictionPolicies = append(out.PlaybackRestrictionPolicies, ivstypes.PlaybackRestrictionPolicySummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListRecordingConfigurations": ivsTwoPages(t, "ListRecordingConfigurations", 100, func(_ any, p int) any {
			out := &ivs.ListRecordingConfigurationsOutput{}
			for _, e := range page("recording-configuration", p) {
				out.RecordingConfigurations = append(out.RecordingConfigurations, ivstypes.RecordingConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListEncoderConfigurations": ivsTwoPages(t, "ListEncoderConfigurations", 100, func(_ any, p int) any {
			out := &ivsrealtime.ListEncoderConfigurationsOutput{}
			for _, e := range page("encoder-configuration", p) {
				out.EncoderConfigurations = append(out.EncoderConfigurations, ivsrealtimetypes.EncoderConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListIngestConfigurations": ivsTwoPages(t, "ListIngestConfigurations", 100, func(in any, p int) any {
			if req := in.(*ivsrealtime.ListIngestConfigurationsInput); req.FilterByStageArn != nil || req.FilterByState != "" {
				t.Errorf("ListIngestConfigurations filtered: %+v; want every ingest configuration", req)
			}
			out := &ivsrealtime.ListIngestConfigurationsOutput{}
			for _, e := range page("ingest-configuration", p) {
				out.IngestConfigurations = append(out.IngestConfigurations, ivsrealtimetypes.IngestConfigurationSummary{Arn: e.arn, Name: e.name})
			}
			return out
		}),
		"ListPublicKeys": ivsTwoPages(t, "ListPublicKeys", 100, func(_ any, p int) any {
			out := &ivsrealtime.ListPublicKeysOutput{}
			for _, e := range page("public-key", p) {
				out.PublicKeys = append(out.PublicKeys, ivsrealtimetypes.PublicKeySummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListStages": ivsTwoPages(t, "ListStages", 100, func(_ any, p int) any {
			out := &ivsrealtime.ListStagesOutput{}
			for _, e := range page("stage", p) {
				out.Stages = append(out.Stages, ivsrealtimetypes.StageSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
		"ListStorageConfigurations": ivsTwoPages(t, "ListStorageConfigurations", 100, func(_ any, p int) any {
			out := &ivsrealtime.ListStorageConfigurationsOutput{}
			for _, e := range page("storage-configuration", p) {
				out.StorageConfigurations = append(out.StorageConfigurations, ivsrealtimetypes.StorageConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags})
			}
			return out
		}),
	}
}

// ivsAccountWideTypes is every account-wide IVS type with the ARN kind
// ivsHandlers lists it under, the op that lists it, and whether its listed
// element carries Tags.
var ivsAccountWideTypes = []struct {
	typ, kind, op string
	hasTags       bool
}{
	{TypeIVSChannel, "channel", ivsListChannelsOp, true},
	{TypeIVSAdConfiguration, "ad-configuration", ivsListAdConfigurationsOp, true},
	{TypeIVSPlaybackKeyPair, "playback-key", ivsListPlaybackKeyPairsOp, true},
	{TypeIVSPlaybackRestrictionPolicy, "playback-restriction-policy", ivsListPlaybackRestrictionPoliciesOp, true},
	{TypeIVSRecordingConfiguration, "recording-configuration", ivsListRecordingConfigurationsOp, true},
	{TypeIVSEncoderConfiguration, "encoder-configuration", ivsListEncoderConfigurationsOp, true},
	{TypeIVSIngestConfiguration, "ingest-configuration", ivsListIngestConfigurationsOp, false},
	{TypeIVSPublicKey, "public-key", ivsListPublicKeysOp, true},
	{TypeIVSStage, "stage", ivsListStagesOp, true},
	{TypeIVSStorageConfiguration, "storage-configuration", ivsListStorageConfigurationsOp, true},
}

func ivsScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	cfg := opStubConfig(h, apiErr("AccessDeniedException", "not authorized to perform this action"))
	total, _, err := scanIVSAll(context.Background(), ivs.NewFromConfig(cfg), ivsrealtime.NewFromConfig(cfg), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// assertIVSRow checks the row stored under arn: its Name and its tags (nil
// for none).
func assertIVSRow(t *testing.T, rows map[string]store.Resource, typ, arn, wantName string, wantTags map[string]string) {
	t.Helper()
	r, ok := rows[arn]
	if !ok {
		t.Errorf("%s %s not stored", typ, arn)
		return
	}
	if got := sv(r.Name); got != wantName {
		t.Errorf("%s %s Name = %q, want %q", typ, arn, got, wantName)
	}
	if wantTags == nil {
		if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q, want nil", typ, arn, *r.TagsJSON)
		}
		return
	}
	assertTagsMap(t, r.TagsJSON, wantTags)
}

// TestScanIVSAll_StoresRowsWithTags pins, per type, the NativeID (the listed
// Arn), the Name (the listed Name, else the Arn) and the tags of the listed
// element, nil when it has none. Ingest configurations carry no Tags and
// store none.
func TestScanIVSAll_StoresRowsWithTags(t *testing.T) {
	st := newTestStore(t)
	total, err := ivsScanAll(t, st, ivsHandlers(t, false))
	if err != nil {
		t.Fatalf("scanIVSAll: %v", err)
	}
	if want := 2*len(ivsAccountWideTypes) + 4; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	for _, c := range ivsAccountWideTypes {
		rows := rowsByNativeID(t, st, c.typ)
		if len(rows) != 2 {
			t.Errorf("%s: stored %d rows, want 2", c.typ, len(rows))
		}
		wantTags := ivsTestTags
		if !c.hasTags {
			wantTags = nil
		}
		assertIVSRow(t, rows, c.typ, ivsTestARN(c.kind, "tagged"), c.kind+"-tagged", wantTags)
		untagged := ivsTestARN(c.kind, "untagged")
		assertIVSRow(t, rows, c.typ, untagged, untagged, nil)
	}
	keys := rowsByNativeID(t, st, TypeIVSStreamKey)
	if len(keys) != 4 {
		t.Errorf("stored %d stream keys, want 4", len(keys))
	}
	for _, ch := range []string{"tagged-", "untagged-"} {
		tagged, untagged := ivsTestARN("stream-key", ch+"tagged"), ivsTestARN("stream-key", ch+"untagged")
		assertIVSRow(t, keys, TypeIVSStreamKey, tagged, tagged, ivsTestTags)
		assertIVSRow(t, keys, TypeIVSStreamKey, untagged, untagged, nil)
	}
}

// TestScanIVSAll_EmptyARNWarnsOncePerOp pins that every listing warns once
// for its elements with no Arn and stores the others.
func TestScanIVSAll_EmptyARNWarnsOncePerOp(t *testing.T) {
	st := newTestStore(t)
	warns := map[string]*int{ivsListStreamKeysOp: countOpWarnings(st, ivsListStreamKeysOp)}
	for _, c := range ivsAccountWideTypes {
		warns[c.op] = countOpWarnings(st, c.op)
	}
	total, err := ivsScanAll(t, st, ivsHandlers(t, true))
	if err != nil {
		t.Fatalf("scanIVSAll: %v", err)
	}
	if want := 2*len(ivsAccountWideTypes) + 4; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	for op, n := range warns {
		if *n != 1 {
			t.Errorf("%s warnings = %d, want 1", op, *n)
		}
	}
	for _, c := range ivsAccountWideTypes {
		if _, ok := rowsByNativeID(t, st, c.typ)[""]; ok {
			t.Errorf("%s stored a row with an empty NativeID", c.typ)
		}
	}
	if _, ok := rowsByNativeID(t, st, TypeIVSStreamKey)[""]; ok {
		t.Errorf("%s stored a row with an empty NativeID", TypeIVSStreamKey)
	}
}

// TestScanIVSStages_ListOutcomes pins the listing outcomes every account-wide
// IVS phase shares: rows read before a failed page are kept and the error is
// returned; a repeated token ends the listing with errRepeatedNextToken; a
// denial is warned about, not returned, and keeps the rows read before it.
func TestScanIVSStages_ListOutcomes(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	denied := apiErr("AccessDeniedException", "not authorized to perform: ivs:ListStages")
	stagePage := func(next string, ids ...string) func() (*ivsrealtime.ListStagesOutput, error) {
		return func() (*ivsrealtime.ListStagesOutput, error) {
			out := &ivsrealtime.ListStagesOutput{}
			if next != "" {
				out.NextToken = ptrStr(next)
			}
			for _, id := range ids {
				out.Stages = append(out.Stages, ivsrealtimetypes.StageSummary{Arn: ptrStr(ivsTestARN("stage", id))})
			}
			return out, nil
		}
	}
	fail := func(err error) func() (*ivsrealtime.ListStagesOutput, error) {
		return func() (*ivsrealtime.ListStagesOutput, error) { return nil, err }
	}
	for _, c := range []struct {
		name      string
		pages     []func() (*ivsrealtime.ListStagesOutput, error)
		wantErr   error
		wantIDs   []string
		wantWarns int
	}{
		{"later page fails keeps rows read", []func() (*ivsrealtime.ListStagesOutput, error){stagePage("t1", "s1"), fail(bad)}, bad, []string{"s1"}, 0},
		{"repeated token keeps rows read", []func() (*ivsrealtime.ListStagesOutput, error){stagePage("t1", "s1"), stagePage("t1", "s2")}, errRepeatedNextToken, []string{"s1", "s2"}, 0},
		{"denied warns", []func() (*ivsrealtime.ListStagesOutput, error){fail(denied)}, nil, nil, 1},
		{"denied after a page keeps rows and warns", []func() (*ivsrealtime.ListStagesOutput, error){stagePage("t1", "s1"), fail(denied)}, nil, []string{"s1"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ivsListStagesOp)
			var call int
			h := map[string]func(in any) (any, error){
				"ListStages": func(any) (any, error) {
					if call >= len(c.pages) {
						t.Fatalf("ListStages called %d times, want %d", call+1, len(c.pages))
					}
					call++
					return c.pages[call-1]()
				},
			}
			client := ivsrealtime.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			total, _, err := scanIVSStages(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantIDs) {
				t.Errorf("total = %d, want %d", total, len(c.wantIDs))
			}
			rows := rowsByNativeID(t, st, TypeIVSStage)
			if len(rows) != len(c.wantIDs) {
				t.Errorf("stored %d stages, want %v", len(rows), c.wantIDs)
			}
			for _, id := range c.wantIDs {
				if _, ok := rows[ivsTestARN("stage", id)]; !ok {
					t.Errorf("stage %s not stored", id)
				}
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanIVSStreamKeys_ChannelOutcomes pins that one channel's failure keeps
// the other channel's stream keys: a hard error is returned after they are
// stored, a denied channel is warned about and not returned, and a channel
// deleted since it was listed (ResourceNotFoundException, modeled for
// ListStreamKeys) is skipped silently.
func TestScanIVSStreamKeys_ChannelOutcomes(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	for _, c := range []struct {
		name      string
		failErr   error
		wantErr   error
		wantWarns int
	}{
		{"hard error returned", bad, bad, 0},
		{"denied warns", apiErr("AccessDeniedException", "not authorized to perform: ivs:ListStreamKeys"), nil, 1},
		{"deleted channel skipped", apiErr("ResourceNotFoundException", "no channel"), nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			failed, ok := ivsTestARN("channel", "failed"), ivsTestARN("channel", "ok")
			h := map[string]func(in any) (any, error){
				"ListStreamKeys": func(in any) (any, error) {
					ch := sv(in.(*ivs.ListStreamKeysInput).ChannelArn)
					if ch == failed {
						return nil, c.failErr
					}
					return &ivs.ListStreamKeysOutput{StreamKeys: []ivstypes.StreamKeySummary{{Arn: ptrStr(ivsTestARN("stream-key", "ok-1")), ChannelArn: &ch}}}, nil
				},
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, ivsListStreamKeysOp)
			client := ivs.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			total, _, err := scanIVSStreamKeys(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{failed, ok})
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 1 {
				t.Errorf("total = %d, want 1", total)
			}
			if _, stored := rowsByNativeID(t, st, TypeIVSStreamKey)[ivsTestARN("stream-key", "ok-1")]; !stored {
				t.Errorf("the ok channel's stream key was not stored")
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanIVSAll_PhaseErrorRunsLaterPhases pins that a failed phase does not
// stop the phases after it: its error is returned and the other phases' rows
// are stored. A failed channel listing leaves no channels for stream keys.
func TestScanIVSAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	for _, c := range []struct {
		failOp    string
		stillHas  []string
		streamKey bool
	}{
		{"ListChannels", []string{TypeIVSAdConfiguration, TypeIVSStorageConfiguration}, false},
		{"ListAdConfigurations", []string{TypeIVSChannel, TypeIVSPlaybackKeyPair, TypeIVSStage, TypeIVSStorageConfiguration}, true},
		{"ListEncoderConfigurations", []string{TypeIVSIngestConfiguration, TypeIVSStorageConfiguration}, true},
	} {
		t.Run(c.failOp, func(t *testing.T) {
			h := ivsHandlers(t, false)
			h[c.failOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			_, err := ivsScanAll(t, st, h)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			for _, typ := range c.stillHas {
				if len(rowsByNativeID(t, st, typ)) == 0 {
					t.Errorf("%s not stored after %s failed", typ, c.failOp)
				}
			}
			if got := len(rowsByNativeID(t, st, TypeIVSStreamKey)) > 0; got != c.streamKey {
				t.Errorf("stream keys stored = %v, want %v", got, c.streamKey)
			}
		})
	}
}

// TestScanIVSAll_StoreWriteStops pins that a failed write stops the scan: no
// phase after it calls AWS.
func TestScanIVSAll_StoreWriteStops(t *testing.T) {
	for _, c := range []struct {
		closeIn  string
		notAfter []string
	}{
		{"ListChannels", []string{"ListStreamKeys", "ListAdConfigurations", "ListStorageConfigurations"}},
		{"ListAdConfigurations", []string{"ListPlaybackKeyPairs", "ListStages", "ListStorageConfigurations"}},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			calls := map[string]int{}
			h := ivsHandlers(t, false)
			for op, f := range h {
				h[op] = func(in any) (any, error) {
					calls[op]++
					return f(in)
				}
			}
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			if _, err := ivsScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			for _, op := range c.notAfter {
				if calls[op] != 0 {
					t.Errorf("%s called %d times after the failed write, want 0", op, calls[op])
				}
			}
		})
	}
}

// TestScanIVSAll_TransientBesideHardError pins that a throttled phase beside
// a hard-failing one is demoted to one warning under the throttled phase's
// own op, and the scan classified as an error, not as transient.
func TestScanIVSAll_TransientBesideHardError(t *testing.T) {
	phases := []struct{ sdkOp, label string }{{"ListStreamKeys", ivsListStreamKeysOp}}
	for _, c := range ivsAccountWideTypes {
		phases = append(phases, struct{ sdkOp, label string }{strings.TrimPrefix(c.op, "ivs:"), c.op})
	}
	for _, p := range phases {
		t.Run(p.sdkOp, func(t *testing.T) {
			hardOp := "ListStages"
			if p.sdkOp == hardOp {
				hardOp = "ListPublicKeys"
			}
			h := ivsHandlers(t, false)
			h[p.sdkOp] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hardOp] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
			st := newTestStore(t)
			var labels []string
			st.OnWarn = func(w store.ScanWarning) { labels = append(labels, w.Service) }
			_, err := ivsScanAll(t, st, h)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if len(labels) != 1 || labels[0] != p.label {
				t.Errorf("warnings under %v, want exactly one under %s", labels, p.label)
			}
		})
	}
}

// TestScanIVSAll_DeniedEveryOpWarnsWithRegion pins that a role denied every
// listing gets one warning per op, scoped to the account and region, and no
// error.
func TestScanIVSAll_DeniedEveryOpWarnsWithRegion(t *testing.T) {
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) { scopes = append(scopes, w.Service+" "+w.Scope) }
	if _, err := ivsScanAll(t, st, nil); err != nil {
		t.Fatalf("scanIVSAll: %v", err)
	}
	if len(scopes) != len(ivsAccountWideTypes) {
		t.Errorf("warnings = %v, want one per account-wide op (%d)", scopes, len(ivsAccountWideTypes))
	}
	for _, c := range ivsAccountWideTypes {
		want := c.op + " " + testAccountID + "/" + testRegion
		found := false
		for _, s := range scopes {
			found = found || s == want
		}
		if !found {
			t.Errorf("no warning %q in %v", want, scopes)
		}
	}
}

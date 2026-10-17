package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/repostspace"
	repostspacetypes "github.com/aws/aws-sdk-go-v2/service/repostspace/types"
	"github.com/icearp/disco-cli/store"
)

const (
	rpsTestSpaceA    = "arn:aws:repostspace:us-east-1:123456789012:space/SPaaaaaaaaaaaaaaaaaaaaaa"
	rpsTestSpaceB    = "arn:aws:repostspace:us-east-1:123456789012:space/SPbbbbbbbbbbbbbbbbbbbbbb"
	rpsTestSpaceNoID = "arn:aws:repostspace:us-east-1:123456789012:space/SPnoidnoidnoidnoidnoidno"

	rpsTestSpaceIDA = "SPaaaaaaaaaaaaaaaaaaaaaa"
	rpsTestSpaceIDB = "SPbbbbbbbbbbbbbbbbbbbbbb"

	rpsTestChanA1 = rpsTestSpaceA + "/channel/WSa1a1a1a1a1a1a1a1a1a1a1"
	rpsTestChanA2 = rpsTestSpaceA + "/channel/WSa2a2a2a2a2a2a2a2a2a2a2"
	rpsTestChanB1 = rpsTestSpaceB + "/channel/WSb1b1b1b1b1b1b1b1b1b1b1"
)

var (
	rpsDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: repostspace:List")
	rpsBadInput   = apiErr("ValidationException", "bad")
	rpsGone       = apiErr("ResourceNotFoundException", "gone")
	rpsUnexpected = apiErr("ConflictException", "unexpected") // a code the model does not list for ListChannels
	rpsCreated    = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	rpsAllChannels = []string{rpsTestChanA1, rpsTestChanA2, rpsTestChanB1}
	rpsAllSpaces   = []string{rpsTestSpaceA, rpsTestSpaceB, rpsTestSpaceNoID}
)

// rpsHandlers answers ListSpaces over two pages (A, B; then a space without a
// SpaceId, which is stored but has no channels listed) and ListChannels: A has
// two pages (with an id-less and a deleted channel to skip, and a deleting one
// to keep), B one page. Every ListChannels call is recorded in calls by space
// id. Both ops fail unless MaxResults is the model's cap.
func rpsHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	badLimit := apiErr("ValidationException", "maxResults must be at most 100")
	space := func(arn, id string) repostspacetypes.SpaceData {
		return repostspacetypes.SpaceData{Arn: sdkaws.String(arn), SpaceId: nonEmptyPtr(id), Name: sdkaws.String("space"), Status: sdkaws.String("CREATED")}
	}
	channel := func(spaceID, id, name string, status repostspacetypes.ChannelStatus) repostspacetypes.ChannelData {
		return repostspacetypes.ChannelData{
			SpaceId: sdkaws.String(spaceID), ChannelId: nonEmptyPtr(id), ChannelName: sdkaws.String(name), ChannelStatus: status,
			CreateDateTime: &rpsCreated, UserCount: sdkaws.Int32(3), GroupCount: sdkaws.Int32(1),
			ChannelDescription: sdkaws.String(name + " desc"),
		}
	}
	return map[string]func(in any) (any, error){
		"ListSpaces": func(in any) (any, error) {
			p := in.(*repostspace.ListSpacesInput)
			if sdkaws.ToInt32(p.MaxResults) != repostspacePageLimit {
				return nil, badLimit
			}
			var out repostspace.ListSpacesOutput
			out.Spaces, out.NextToken = evsPage(p.NextToken,
				[]repostspacetypes.SpaceData{space(rpsTestSpaceA, rpsTestSpaceIDA), space(rpsTestSpaceB, rpsTestSpaceIDB)},
				[]repostspacetypes.SpaceData{space(rpsTestSpaceNoID, "")})
			return &out, nil
		},
		"ListChannels": func(in any) (any, error) {
			p := in.(*repostspace.ListChannelsInput)
			calls.add(sdkaws.ToString(p.SpaceId))
			if sdkaws.ToInt32(p.MaxResults) != repostspacePageLimit {
				return nil, badLimit
			}
			var out repostspace.ListChannelsOutput
			switch sdkaws.ToString(p.SpaceId) {
			case rpsTestSpaceIDA:
				out.Channels, out.NextToken = evsPage(p.NextToken,
					[]repostspacetypes.ChannelData{
						channel(rpsTestSpaceIDA, "WSa1a1a1a1a1a1a1a1a1a1a1", "general", repostspacetypes.ChannelStatusCreated),
						channel(rpsTestSpaceIDA, "", "no id", repostspacetypes.ChannelStatusCreated),
						channel(rpsTestSpaceIDA, "WSdeldeldeldeldeldeldeld", "gone", repostspacetypes.ChannelStatusDeleted),
					},
					[]repostspacetypes.ChannelData{channel(rpsTestSpaceIDA, "WSa2a2a2a2a2a2a2a2a2a2a2", "retiring", repostspacetypes.ChannelStatusDeleting)})
			case rpsTestSpaceIDB:
				out.Channels = []repostspacetypes.ChannelData{channel(rpsTestSpaceIDB, "WSb1b1b1b1b1b1b1b1b1b1b1", "ops", repostspacetypes.ChannelStatusCreated)}
			default:
				return nil, rpsUnexpected
			}
			return &out, nil
		},
	}
}

func scanRepostspaceTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rpsDenied)
	total, _, err := scanRepostspace(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanRepostspace_StoresChannels(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	total, err := scanRepostspaceTopLevel(t, st, rpsHandlers(calls))
	if err != nil {
		t.Fatalf("scanRepostspace: %v", err)
	}
	// 3 spaces + 3 channels
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeRepostspaceSpace)); !slices.Equal(got, rpsAllSpaces) {
		t.Errorf("space rows = %v, want %v", got, rpsAllSpaces)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeRepostspaceChannel)); !slices.Equal(got, rpsAllChannels) {
		t.Errorf("channel rows = %v, want %v", got, rpsAllChannels)
	}
	wantParents := []string{rpsTestSpaceIDA, rpsTestSpaceIDB}
	if got := calls.distinct(); !slices.Equal(got, wantParents) {
		t.Errorf("ListChannels spaces = %v, want %v (not the space without a SpaceId)", got, wantParents)
	}

	r := rowsByNativeID(t, st, TypeRepostspaceChannel)[rpsTestChanA2]
	if r.Type != TypeRepostspaceChannel || sv(r.Region) != testRegion || sv(r.Name) != "retiring" ||
		sv(r.Status) != "DELETING" || sv(r.CreatedAt) != rpsCreated.Format(time.RFC3339) {
		t.Errorf("%s row = type %q region %q name %q status %q created %q", rpsTestChanA2, r.Type, sv(r.Region), sv(r.Name), sv(r.Status), sv(r.CreatedAt))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["ChannelDescription"] != "retiring desc" || attrs["SpaceId"] != rpsTestSpaceIDA {
		t.Errorf("attrs = %v, want the SDK element verbatim", attrs)
	}

	for _, pair := range [][2]string{{rpsTestSpaceA, rpsTestChanA1}, {rpsTestSpaceA, rpsTestChanA2}, {rpsTestSpaceB, rpsTestChanB1}} {
		assertContainsNative(t, st, pair[0], pair[1])
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanRepostspace_NoChannels(t *testing.T) {
	st := newTestStore(t)
	h := rpsHandlers(&parentCalls{})
	h["ListChannels"] = func(any) (any, error) { return &repostspace.ListChannelsOutput{}, nil }
	total, err := scanRepostspaceTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanRepostspace: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want the 3 spaces only", total)
	}
	if n := len(rowsByNativeID(t, st, TypeRepostspaceChannel)); n != 0 {
		t.Errorf("channel rows = %d, want 0", n)
	}
}

// A space deleted since listing drops only its own channels, silently. A
// space denied, failing transiently, or rejected with ValidationException
// (possibly not ready) drops only its own channels and warns once however
// many spaces hit it. An error code the model does not list, on any page,
// fails the channel phase (none of its rows stored) and is returned; the
// spaces stay stored either way.
func TestScanRepostspace_FailedChannelSpace(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	for _, tc := range []struct {
		name      string
		fail      map[string]error // space id -> error
		failPage2 bool             // fail only A's second page
		wantCode  string           // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"gone", map[string]error{rpsTestSpaceIDB: rpsGone}, false, "", []string{rpsTestChanA1, rpsTestChanA2}, 0},
		{"denied", map[string]error{rpsTestSpaceIDA: rpsDenied, rpsTestSpaceIDB: rpsDenied}, false, "", nil, 1},
		{"throttled", map[string]error{rpsTestSpaceIDB: throttled}, false, "", []string{rpsTestChanA1, rpsTestChanA2}, 1},
		{"not ready", map[string]error{rpsTestSpaceIDB: rpsBadInput}, false, "", []string{rpsTestChanA1, rpsTestChanA2}, 1},
		{"both not ready", map[string]error{rpsTestSpaceIDA: rpsBadInput, rpsTestSpaceIDB: rpsBadInput}, false, "", nil, 1},
		{"other", map[string]error{rpsTestSpaceIDB: rpsUnexpected}, false, "ConflictException", nil, 0},
		{"other on page two", map[string]error{rpsTestSpaceIDA: rpsUnexpected}, true, "ConflictException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, repostspaceListChannelsOp)
			h := rpsHandlers(&parentCalls{})
			next := h["ListChannels"]
			h["ListChannels"] = func(in any) (any, error) {
				p := in.(*repostspace.ListChannelsInput)
				if err, ok := tc.fail[sdkaws.ToString(p.SpaceId)]; ok && (!tc.failPage2 || p.NextToken != nil) {
					return nil, err
				}
				return next(in)
			}
			_, err := scanRepostspaceTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeRepostspaceChannel)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("channel rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeRepostspaceSpace)); !slices.Equal(got, rpsAllSpaces) {
				t.Errorf("space rows = %v, want %v", got, rpsAllSpaces)
			}
		})
	}
}

// A failed space listing stores nothing and lists no channels; a denial warns
// and returns nil, anything else is returned.
func TestScanRepostspace_SpaceListErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{"denied", rpsDenied, "", 1},
		{"other", rpsBadInput, "ValidationException", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "repostspace:ListSpaces")
			calls := &parentCalls{}
			h := rpsHandlers(calls)
			h["ListSpaces"] = func(any) (any, error) { return nil, tc.err }
			total, err := scanRepostspaceTopLevel(t, st, h)
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
			if n := len(calls.ids); n != 0 {
				t.Errorf("ListChannels called %d times after a failed space listing", n)
			}
		})
	}
}

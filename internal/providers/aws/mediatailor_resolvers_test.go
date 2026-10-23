package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	mttypes "github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
	"github.com/icearp/disco-cli/store"
)

const mtWestRegion = "us-west-2"

func mtFunctionARN(region, id string) string {
	return fmt.Sprintf("arn:aws:mediatailor:%s:%s:function/%s", region, testAccountID, id)
}

// mtSeqFunctionAttrs is a SEQUENTIAL_EXECUTOR function whose steps run the
// given child function ids.
func mtSeqFunctionAttrs(id string, children ...string) string {
	var steps []mttypes.FunctionRef
	for _, c := range children {
		steps = append(steps, mttypes.FunctionRef{FunctionId: ptrStr(c)})
	}
	return mustJSON(mttypes.Function{
		FunctionId: ptrStr(id), FunctionType: mttypes.FunctionTypeSequentialExecutor,
		SequentialExecutorConfiguration: &mttypes.SequentialExecutorConfiguration{Runtime: mttypes.RuntimeTypeJsonata, FunctionList: steps},
	})
}

func mtHTTPFunctionAttrs(id string) string {
	return mustJSON(mttypes.Function{FunctionId: ptrStr(id), FunctionType: mttypes.FunctionTypeHttpRequest})
}

func mtPlaybackAttrs(mapping map[string]string) string {
	return mustJSON(mttypes.PlaybackConfiguration{Name: ptrStr("pc"), FunctionMapping: mapping})
}

// outEdges returns the kinds of every edge leaving id, keyed by target.
func outEdges(t *testing.T, st *store.Store, id string) map[string]string {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	m := map[string]string{}
	for _, r := range rels {
		m[r.ToID] = r.Kind
	}
	return m
}

// A playback configuration uses each function its FunctionMapping names, and a
// sequential function uses each step's function, both matched by FunctionId in
// the referrer's region. A step naming an unscanned function, and a playback
// configuration in a region without that function, get no edge.
func TestResolveMediaTailorFunctionUses_PlaybackAndSteps(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	enrichID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorFunction, mtFunctionARN(testRegion, "enrich"), testRegion, mtHTTPFunctionAttrs("enrich"))
	pipeID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorFunction, mtFunctionARN(testRegion, "pipeline"), testRegion, mtSeqFunctionAttrs("pipeline", "enrich", "ghost"))
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/live", testRegion, acct.ID), testRegion,
		mtPlaybackAttrs(map[string]string{"PRE_SESSION_INITIALIZATION": "enrich", "PRE_ADS_REQUEST": "pipeline"}))
	westPCID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/live", mtWestRegion, acct.ID), mtWestRegion,
		mtPlaybackAttrs(map[string]string{"PRE_ADS_REQUEST": "pipeline"}))

	if err := resolveMediaTailorFunctionUses(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorFunctionUses: %v", err)
	}
	if got := outEdges(t, st, pcID); len(got) != 2 || got[enrichID] != store.RelUses || got[pipeID] != store.RelUses {
		t.Errorf("playback configuration edges = %v, want uses → enrich and pipeline", got)
	}
	if got := outEdges(t, st, pipeID); len(got) != 1 || got[enrichID] != store.RelUses {
		t.Errorf("pipeline edges = %v, want uses → enrich only", got)
	}
	if got := outEdges(t, st, enrichID); len(got) != 0 {
		t.Errorf("enrich edges = %v, want none", got)
	}
	if got := outEdges(t, st, westPCID); len(got) != 0 {
		t.Errorf("%s playback configuration edges = %v, want none", mtWestRegion, got)
	}
}

// Rows without function references produce no edges and no error, and an
// empty reference does not match a function row that carries no FunctionId.
func TestResolveMediaTailorFunctionUses_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorFunction, mtFunctionARN(testRegion, "f"), testRegion, "{}")
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/live", testRegion, acct.ID), testRegion, "{}")
	emptyRefID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/empty-ref", testRegion, acct.ID), testRegion,
		mtPlaybackAttrs(map[string]string{"PRE_ADS_REQUEST": ""}))
	if err := resolveMediaTailorFunctionUses(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorFunctionUses: %v", err)
	}
	for _, id := range []string{fnID, pcID, emptyRefID} {
		if got := outEdges(t, st, id); len(got) != 0 {
			t.Errorf("edges from %s = %v, want none", id, got)
		}
	}
}

// An undecodable function row and an undecodable playback configuration are
// both reported, and the rows that do decode still resolve.
func TestResolveMediaTailorFunctionUses_UndecodableRowsReported(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	enrichID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorFunction, mtFunctionARN(testRegion, "enrich"), testRegion, mtHTTPFunctionAttrs("enrich"))
	upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorFunction, mtFunctionARN(testRegion, "bad"), testRegion, "[]")
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/live", testRegion, acct.ID), testRegion,
		mtPlaybackAttrs(map[string]string{"PRE_ADS_REQUEST": "enrich"}))
	upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorPlaybackConfiguration,
		fmt.Sprintf("arn:aws:mediatailor:%s:%s:playbackConfiguration/bad", testRegion, acct.ID), testRegion, "[]")

	err := resolveMediaTailorFunctionUses(acct, st)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("err = %v, want two joined decode errors", err)
	}
	for _, e := range joined.Unwrap() {
		var ute *json.UnmarshalTypeError
		if !errors.As(e, &ute) {
			t.Errorf("joined error %v is not a JSON decode error", e)
		}
	}
	if got := outEdges(t, st, pcID); len(got) != 1 || got[enrichID] != store.RelUses {
		t.Errorf("playback configuration edges = %v, want uses → enrich", got)
	}
}

func TestResolveMediaTailorChannelPolicyToChannel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	chARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:channel/c1", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorChannel, chARN, testRegion, "{}")
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorChannelPolicy, chARN+"/policy", testRegion, "{}")
	if err := resolveMediaTailorChannelPolicyToChannel(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorChannelPolicyToChannel: %v", err)
	}
	rels, err := st.RelationshipsFrom(cpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", cpID, err)
	}
	assertRelationship(t, rels, cpID, chID, store.RelAttachedTo)
}

func TestResolveMediaTailorSourcesToSourceLocation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	slARN := mtSourceLocationARN(testRegion, acct.ID, "sl1")
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorSourceLocation, slARN, testRegion, "{}")
	lsARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:liveSource/sl1/ls1", testRegion, acct.ID)
	lsID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorLiveSource, lsARN, testRegion, `{"SourceLocationName":"sl1"}`)
	vsARN := fmt.Sprintf("arn:aws:mediatailor:%s:%s:vodSource/sl1/v1", testRegion, acct.ID)
	vsID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorVodSource, vsARN, testRegion, `{"SourceLocationName":"sl1"}`)
	if err := resolveMediaTailorSourcesToSourceLocation(acct, st); err != nil {
		t.Fatalf("resolveMediaTailorSourcesToSourceLocation: %v", err)
	}
	for _, c := range []string{lsID, vsID} {
		rels, err := st.RelationshipsFrom(c)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", c, err)
		}
		assertRelationship(t, rels, c, slID, store.RelAttachedTo)
	}
}

// An undecodable live source and an undecodable VOD source are both reported,
// and the sources named before and after them still resolve.
func TestResolveMediaTailorSourcesToSourceLocation_UndecodableRowsReported(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaTailorSourceLocation, mtSourceLocationARN(testRegion, acct.ID, "sl1"), testRegion, "{}")
	sourceAttrs := mustJSON(mttypes.LiveSource{SourceLocationName: ptrStr("sl1")})
	var good []string
	for _, src := range []struct{ typ, kind, name, attrs string }{
		{TypeMediaTailorLiveSource, "liveSource", "a", sourceAttrs},
		{TypeMediaTailorLiveSource, "liveSource", "b", "[]"},
		{TypeMediaTailorLiveSource, "liveSource", "c", sourceAttrs},
		{TypeMediaTailorVodSource, "vodSource", "a", sourceAttrs},
		{TypeMediaTailorVodSource, "vodSource", "b", "[]"},
		{TypeMediaTailorVodSource, "vodSource", "c", sourceAttrs},
	} {
		arn := fmt.Sprintf("arn:aws:mediatailor:%s:%s:%s/sl1/%s", testRegion, acct.ID, src.kind, src.name)
		id := upsertTestResourceNamed(t, st, src.typ, arn, testRegion, src.attrs, src.name)
		if src.attrs != "[]" {
			good = append(good, id)
		}
	}

	err := resolveMediaTailorSourcesToSourceLocation(acct, st)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("err = %v, want two joined decode errors", err)
	}
	for _, e := range joined.Unwrap() {
		var ute *json.UnmarshalTypeError
		if !errors.As(e, &ute) {
			t.Errorf("joined error %v is not a JSON decode error", e)
		}
	}
	for _, id := range good {
		if got := outEdges(t, st, id); len(got) != 1 || got[slID] != store.RelAttachedTo {
			t.Errorf("edges from %s = %v, want attached-to → sl1", id, got)
		}
	}
}

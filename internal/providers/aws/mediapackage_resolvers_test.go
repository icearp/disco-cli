package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	mptypes "github.com/aws/aws-sdk-go-v2/service/mediapackage/types"
	mpvodtypes "github.com/aws/aws-sdk-go-v2/service/mediapackagevod/types"
	"github.com/icearp/disco-cli/store"
)

func mpRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

// TestResolveMPV1OriginEndpointToChannel pins the endpoint → channel edge by
// ChannelId, built in the endpoint's own partition (aws-cn here); an
// endpoint with no ChannelId, or naming an unscanned channel, gets none.
func TestResolveMPV1OriginEndpointToChannel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnARN := func(kind, id string) string {
		return fmt.Sprintf("arn:aws-cn:mediapackage:cn-north-1:%s:%s/%s", acct.ID, kind, id)
	}
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageChannel, cnARN("channels", "ch1"), "cn-north-1", mustJSON(mptypes.Channel{Id: ptrStr("ch1")}))
	ep := func(id string, ch *string) string {
		return upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageOriginEndpoint, cnARN("origin_endpoints", id), "cn-north-1",
			mustJSON(mptypes.OriginEndpoint{Id: ptrStr(id), ChannelId: ch}))
	}
	linked, none, unscanned := ep("ep1", ptrStr("ch1")), ep("ep2", nil), ep("ep3", ptrStr("gone"))
	if err := resolveMPV1OriginEndpointToChannel(acct, st); err != nil {
		t.Fatalf("resolveMPV1OriginEndpointToChannel: %v", err)
	}
	assertRelationship(t, mpRelsFrom(t, st, linked), linked, chID, store.RelAttachedTo)
	for _, id := range []string{none, unscanned} {
		if rels := mpRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("%s: edges %v, want none", id, rels)
		}
	}
}

// TestResolveMPV1AssetRefs pins an asset's edges as the scanner stores it:
// to its packaging group under the mediapackage-vod ARN, to the bucket of
// its S3 source object and to its ingest role. An asset with no references
// gets none.
func TestResolveMPV1AssetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackagePackagingGroup, mpTestARN("mediapackage-vod", "packaging-groups", "pg1"), testRegion, "{}")
	bktARN := "arn:aws:s3:::vod-source"
	bktID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bktARN, "us-east-1", "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mp-vod", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageAsset, mpTestARN("mediapackage-vod", "assets", "a1"), testRegion,
		mustJSON(mpvodtypes.AssetShallow{Id: ptrStr("a1"), PackagingGroupId: ptrStr("pg1"), SourceArn: ptrStr(bktARN + "/path/to/file.smil"), SourceRoleArn: &roleARN}))
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageAsset, mpTestARN("mediapackage-vod", "assets", "a2"), testRegion,
		mustJSON(mpvodtypes.AssetShallow{Id: ptrStr("a2")}))
	if err := resolveMPV1AssetRefs(acct, st); err != nil {
		t.Fatalf("resolveMPV1AssetRefs: %v", err)
	}
	rels := mpRelsFrom(t, st, aID)
	assertRelationship(t, rels, aID, pgID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, bktID, store.RelUses)
	assertRelationship(t, rels, aID, roleID, store.RelUses)
	if len(rels) != 3 {
		t.Errorf("asset edges = %v, want 3", rels)
	}
	if rels := mpRelsFrom(t, st, bareID); len(rels) != 0 {
		t.Errorf("bare asset edges = %v, want none", rels)
	}
}

// TestResolveMPV1PackagingConfigToGroup pins the configuration → group edge
// under the mediapackage-vod ARN; a configuration with no PackagingGroupId
// gets none.
func TestResolveMPV1PackagingConfigToGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackagePackagingGroup, mpTestARN("mediapackage-vod", "packaging-groups", "pg1"), testRegion, "{}")
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackagePackagingConfiguration, mpTestARN("mediapackage-vod", "packaging-configurations", "pc1"), testRegion,
		mustJSON(mpvodtypes.PackagingConfiguration{Id: ptrStr("pc1"), PackagingGroupId: ptrStr("pg1")}))
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackagePackagingConfiguration, mpTestARN("mediapackage-vod", "packaging-configurations", "pc2"), testRegion,
		mustJSON(mpvodtypes.PackagingConfiguration{Id: ptrStr("pc2")}))
	if err := resolveMPV1PackagingConfigToGroup(acct, st); err != nil {
		t.Fatalf("resolveMPV1PackagingConfigToGroup: %v", err)
	}
	assertRelationship(t, mpRelsFrom(t, st, pcID), pcID, pgID, store.RelAttachedTo)
	if rels := mpRelsFrom(t, st, bareID); len(rels) != 0 {
		t.Errorf("bare configuration edges = %v, want none", rels)
	}
}

// mpV1ResolverCases is each v1 resolver that links a row to a sibling by Id:
// the row's type, ARN kind and service, the parent's type and ARN kind, and
// the attribute naming the parent's Id.
var mpV1ResolverCases = []struct {
	name, srcType, parentType, kind, parentKind, svc, refField string
	resolve                                                    func(*account, *store.Store) error
}{
	{"origin endpoint", TypeMediaPackageOriginEndpoint, TypeMediaPackageChannel, "origin_endpoints", "channels", "mediapackage", "ChannelId", resolveMPV1OriginEndpointToChannel},
	{"asset", TypeMediaPackageAsset, TypeMediaPackagePackagingGroup, "assets", "packaging-groups", "mediapackage-vod", "PackagingGroupId", resolveMPV1AssetRefs},
	{"packaging configuration", TypeMediaPackagePackagingConfiguration, TypeMediaPackagePackagingGroup, "packaging-configurations", "packaging-groups", "mediapackage-vod", "PackagingGroupId", resolveMPV1PackagingConfigToGroup},
}

// TestResolveMPV1_BadRowKeepsOtherRows pins, per v1 resolver, that a row
// whose attributes do not decode, or whose NativeID is not an ARN, is
// returned as an error while the good rows on both sides of it keep their
// edges. Rows are named so ListResources returns them in that order.
func TestResolveMPV1_BadRowKeepsOtherRows(t *testing.T) {
	for _, c := range mpV1ResolverCases {
		for _, bad := range []struct{ name, nativeID, attrs string }{
			{"undecodable attributes", mpTestARN(c.svc, c.kind, "b"), `{"` + c.refField + `":7}`},
			{"NativeID not an ARN", "not-an-arn", `{"` + c.refField + `":"p1"}`},
		} {
			t.Run(c.name+"/"+bad.name, func(t *testing.T) {
				st := newTestStore(t)
				parentID := upsertTestResource(t, st, "aws", testAccountID, c.parentType, mpTestARN(c.svc, c.parentKind, "p1"), testRegion, "{}")
				good := `{"` + c.refField + `":"p1"}`
				first := upsertTestResourceNamed(t, st, c.srcType, mpTestARN(c.svc, c.kind, "a"), testRegion, good, "a")
				upsertTestResourceNamed(t, st, c.srcType, bad.nativeID, testRegion, bad.attrs, "b")
				last := upsertTestResourceNamed(t, st, c.srcType, mpTestARN(c.svc, c.kind, "c"), testRegion, good, "c")
				err := c.resolve(newTestAccount(testAccountID), st)
				var typeErr *json.UnmarshalTypeError
				if bad.name == "undecodable attributes" && !errors.As(err, &typeErr) {
					t.Errorf("err = %v, want the decode error", err)
				}
				if err == nil {
					t.Errorf("err = nil, want the bad row's error")
				}
				for _, id := range []string{first, last} {
					assertRelationship(t, mpRelsFrom(t, st, id), id, parentID, store.RelAttachedTo)
				}
			})
		}
	}
}

// TestResolveMPV1_WriteFailureKeepsDecodeErrors pins, per v1 resolver, that
// an edge write failing after a row failed to decode returns both errors.
func TestResolveMPV1_WriteFailureKeepsDecodeErrors(t *testing.T) {
	for _, c := range mpV1ResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, c.parentType, mpTestARN(c.svc, c.parentKind, "p1"), testRegion, "{}")
			upsertTestResourceNamed(t, st, c.srcType, mpTestARN(c.svc, c.kind, "a"), testRegion, `{"`+c.refField+`":7}`, "a")
			upsertTestResourceNamed(t, st, c.srcType, mpTestARN(c.svc, c.kind, "b"), testRegion, `{"`+c.refField+`":"p1"}`, "b")
			if _, err := st.DB().Exec(`CREATE TRIGGER refuse_rel BEFORE INSERT ON relationships
				BEGIN SELECT RAISE(ABORT, 'test: relationship write refused'); END`); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			err := c.resolve(newTestAccount(testAccountID), st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Errorf("err = %v, want the decode error kept", err)
			}
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v, want the write error", err)
			}
		})
	}
	t.Run("asset role edge", func(t *testing.T) {
		st := newTestStore(t)
		roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mp-vod", testAccountID)
		upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, roleARN, "", "{}")
		upsertTestResourceNamed(t, st, TypeMediaPackageAsset, mpTestARN("mediapackage-vod", "assets", "a"), testRegion, `{"SourceRoleArn":7}`, "a")
		upsertTestResourceNamed(t, st, TypeMediaPackageAsset, mpTestARN("mediapackage-vod", "assets", "b"), testRegion,
			mustJSON(mpvodtypes.AssetShallow{Id: ptrStr("b"), SourceRoleArn: &roleARN}), "b")
		if _, err := st.DB().Exec(`CREATE TRIGGER refuse_rel BEFORE INSERT ON relationships
			BEGIN SELECT RAISE(ABORT, 'test: relationship write refused'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		err := resolveMPV1AssetRefs(newTestAccount(testAccountID), st)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) || !errors.Is(err, store.ErrStoreWrite) {
			t.Errorf("err = %v, want the decode error and the write error", err)
		}
	})
}

// TestResolveMPV1AssetRefs_NotARNKeepsDirectEdges pins that an asset whose
// NativeID is not an ARN loses only its packaging-group edge: the not-ARN
// error is returned and its S3 and role edges, which need no ARN of its own,
// are still written.
func TestResolveMPV1AssetRefs_NotARNKeepsDirectEdges(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeMediaPackagePackagingGroup, mpTestARN("mediapackage-vod", "packaging-groups", "pg1"), testRegion, "{}")
	bktARN := "arn:aws:s3:::vod-source"
	bktID := upsertTestResource(t, st, "aws", testAccountID, TypeS3Bucket, bktARN, "us-east-1", "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mp-vod", testAccountID)
	roleID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, roleARN, "", "{}")
	aID := upsertTestResource(t, st, "aws", testAccountID, TypeMediaPackageAsset, "not-an-arn", testRegion,
		mustJSON(mpvodtypes.AssetShallow{Id: ptrStr("a1"), PackagingGroupId: ptrStr("pg1"), SourceArn: ptrStr(bktARN + "/f.smil"), SourceRoleArn: &roleARN}))
	if err := resolveMPV1AssetRefs(newTestAccount(testAccountID), st); err == nil {
		t.Errorf("err = nil, want the not-an-ARN error")
	}
	rels := mpRelsFrom(t, st, aID)
	assertRelationship(t, rels, aID, bktID, store.RelUses)
	assertRelationship(t, rels, aID, roleID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("edges = %v, want the S3 and role edges only", rels)
	}
}

// TestResolveMPV1_NoRefNotARNIsNoError pins, per v1 resolver, that a row
// naming no parent is not reported for a NativeID that is not an ARN: it
// needs no sibling ARN built from it.
func TestResolveMPV1_NoRefNotARNIsNoError(t *testing.T) {
	for _, c := range mpV1ResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, c.parentType, mpTestARN(c.svc, c.parentKind, "p1"), testRegion, "{}")
			upsertTestResource(t, st, "aws", testAccountID, c.srcType, "not-an-arn", testRegion, "{}")
			if err := c.resolve(newTestAccount(testAccountID), st); err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

// TestMPSiblingARN pins the sibling ARN keeping the source's partition,
// service, region and account, and the rejection of a non-ARN.
func TestMPSiblingARN(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"arn:aws:mediapackage-vod:us-east-1:111122223333:assets/a1", "arn:aws:mediapackage-vod:us-east-1:111122223333:packaging-groups/pg", true},
		{"arn:aws-us-gov:mediapackage-vod:us-gov-west-1:111122223333:assets/a1", "arn:aws-us-gov:mediapackage-vod:us-gov-west-1:111122223333:packaging-groups/pg", true},
		{"arn:aws:mediapackage-vod:us-east-1", "", false},
		{"", "", false},
	} {
		got, ok := mpSiblingARN(c.in, "packaging-groups", "pg")
		if got != c.want || ok != c.ok {
			t.Errorf("mpSiblingARN(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveMPV2ChildrenToChannelGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cgARN := fmt.Sprintf("arn:aws:mediapackagev2:%s:%s:channelGroup/cg1", testRegion, acct.ID)
	cgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2ChannelGroup, cgARN, testRegion, "{}")
	chARN := cgARN + "/channel/c1"
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2Channel, chARN, testRegion, "{}")
	epARN := chARN + "/originEndpoint/ep1"
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2OriginEndpoint, epARN, testRegion, "{}")
	cpARN := chARN + "/policy"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2ChannelPolicy, cpARN, testRegion, "{}")
	epPolARN := epARN + "/policy"
	epPolID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaPackageV2OriginEndpointPolicy, epPolARN, testRegion, "{}")
	if err := resolveMPV2ChildrenToChannelGroup(acct, st); err != nil {
		t.Fatalf("resolveMPV2ChildrenToChannelGroup: %v", err)
	}
	assertRelationship(t, mpRelsFrom(t, st, chID), chID, cgID, store.RelAttachedTo)
	assertRelationship(t, mpRelsFrom(t, st, epID), epID, chID, store.RelAttachedTo)
	assertRelationship(t, mpRelsFrom(t, st, cpID), cpID, chID, store.RelAttachedTo)
	assertRelationship(t, mpRelsFrom(t, st, epPolID), epPolID, epID, store.RelAttachedTo)
}

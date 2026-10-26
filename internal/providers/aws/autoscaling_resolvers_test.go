package aws

import (
	"encoding/json"
	"errors"
	"testing"

	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveAutoScalingNotificationTopicEdges_ScannedTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, asTestTopicA, testRegion, `{}`)
	otherTopicID := store.ResourceID("aws", testAccountID, asTestTopicB)
	scanned := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion,
		mustJSON(autoScalingNotificationConfiguration{AutoScalingGroupName: "web", TopicARN: asTestTopicA, NotificationTypes: []string{asTestLaunch}}))
	unscanned := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyOrphanB, testRegion,
		mustJSON(autoScalingNotificationConfiguration{AutoScalingGroupName: "gone", TopicARN: asTestTopicB, NotificationTypes: []string{asTestLaunch}}))

	if err := resolveAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(scanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("edges from %s = %d, want 1: %+v", asTestNotifyWebA, len(rels), rels)
	}
	assertRelationship(t, rels, scanned, topicID, store.RelUses)

	rels, err = st.RelationshipsFrom(unscanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from %s to unscanned topic %s = %+v, want none", asTestNotifyOrphanB, otherTopicID, rels)
	}
}

func TestResolveAutoScalingNotificationTopicEdges_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, asTestTopicA, testRegion, `{}`)
	id := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion, `{}`)

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(id, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from attr-less row = %+v, want none", rels)
	}
}

func TestResolveAutoScalingNotificationTopicEdges_UndecodableAttrsIsError(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion, `{"TopicARN":7}`)

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err == nil {
		t.Fatal("resolveAutoScalingRelationships = nil, want the decode error")
	}
}

// countJSONSyntaxErrors counts the *json.SyntaxError leaves of a (joined) error.
func countJSONSyntaxErrors(err error) int {
	var syntaxErr *json.SyntaxError
	switch e := err.(type) {
	case nil:
		return 0
	case interface{ Unwrap() []error }:
		n := 0
		for _, inner := range e.Unwrap() {
			n += countJSONSyntaxErrors(inner)
		}
		return n
	default:
		if errors.As(err, &syntaxErr) {
			return 1
		}
		return 0
	}
}

// In every family, a row whose attrs do not decode is returned as an error and
// costs neither the rows on either side of it (rows are read in name order)
// nor the other families any edge.
func TestResolveAutoScalingRelationships_DecodeErrorsKeepOtherRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := "arn:aws:iam::123456789012:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling"
	roleID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, roleARN, "", `{}`)
	profileARN := "arn:aws:iam::123456789012:instance-profile/web"
	profileID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMInstanceProfile, profileARN, "", `{}`)
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, asTestTopicA, testRegion, `{}`)

	type want struct{ from, to, kind string }
	var wants []want
	asgIDs := map[string]string{}
	for _, name := range []string{"a-good", "c-good"} {
		asgARN := "arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:1:autoScalingGroupName/" + name
		asgIDs[name] = upsertTestResourceNamed(t, st, TypeAutoScalingGroup, asgARN, testRegion,
			mustJSON(astypes.AutoScalingGroup{AutoScalingGroupName: ptrStr(name), ServiceLinkedRoleARN: ptrStr(roleARN)}), name)
		wants = append(wants, want{asgIDs[name], roleID, store.RelUses})

		lcARN := "arn:aws:autoscaling:us-east-1:123456789012:launchConfiguration:1:launchConfigurationName/" + name
		lcID := upsertTestResourceNamed(t, st, TypeAutoScalingLaunchConfiguration, lcARN, testRegion,
			mustJSON(astypes.LaunchConfiguration{LaunchConfigurationName: ptrStr(name), IamInstanceProfile: ptrStr(profileARN)}), name)
		wants = append(wants, want{lcID, profileID, store.RelUses})

		hookID := upsertTestResourceNamed(t, st, TypeAutoScalingLifecycleHook, autoScalingLifecycleHookARN("aws", testRegion, testAccountID, name, "drain"), testRegion,
			mustJSON(astypes.LifecycleHook{AutoScalingGroupName: ptrStr(name), LifecycleHookName: ptrStr("drain")}), name)
		wants = append(wants, want{hookID, asgIDs[name], store.RelAttachedTo})

		notifyID := upsertTestResourceNamed(t, st, TypeAutoScalingNotificationConfiguration, autoScalingNotificationConfigurationARN("aws", testRegion, testAccountID, name, asTestTopicA), testRegion,
			mustJSON(autoScalingNotificationConfiguration{AutoScalingGroupName: name, TopicARN: asTestTopicA}), name)
		wants = append(wants, want{notifyID, topicID, store.RelUses})
	}
	for _, typ := range []string{TypeAutoScalingGroup, TypeAutoScalingLaunchConfiguration, TypeAutoScalingLifecycleHook, TypeAutoScalingNotificationConfiguration} {
		upsertTestResourceNamed(t, st, typ, typ+"/b-bad", testRegion, `not json`, "b-bad")
	}

	err := resolveAutoScalingRelationships(acct, st)
	if n := countJSONSyntaxErrors(err); n != 4 {
		t.Errorf("resolveAutoScalingRelationships = %v; want 4 decode errors, one per family, got %d", err, n)
	}
	for _, w := range wants {
		rels, rerr := st.RelationshipsFrom(w.from)
		if rerr != nil {
			t.Fatalf("RelationshipsFrom: %v", rerr)
		}
		assertRelationship(t, rels, w.from, w.to, w.kind)
	}
}

// A warm pool is stored as its WarmPoolConfiguration, which names no group,
// so its edge to the group comes from the NativeID the scanner keys on it.
func TestResolveAutoScalingChildEdges_WarmPoolToGroup(t *testing.T) {
	for _, c := range []struct {
		name, attrs string
	}{
		{"stored shape", mustJSON(astypes.WarmPoolConfiguration{MinSize: ptrInt32(1), PoolState: astypes.WarmPoolStateStopped})},
		{"no attrs", `{}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			groupID := upsertTestResourceNamed(t, st, TypeAutoScalingGroup, asTestGroupARN, testRegion, `{}`, "web")
			pool := upsertTestResourceNamed(t, st, TypeAutoScalingWarmPool, asTestWarmPoolWeb, testRegion, c.attrs, "web")
			orphan := upsertTestResourceNamed(t, st, TypeAutoScalingWarmPool,
				autoScalingWarmPoolARN("aws", testRegion, testAccountID, "gone"), testRegion, c.attrs, "gone")

			if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolveAutoScalingRelationships: %v", err)
			}
			rels, err := st.RelationshipsFrom(pool, store.RelAttachedTo)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 {
				t.Errorf("edges from %s = %+v, want one to its group", asTestWarmPoolWeb, rels)
			}
			assertRelationship(t, rels, pool, groupID, store.RelAttachedTo)
			if rels, err := st.RelationshipsFrom(orphan, store.RelAttachedTo); err != nil || len(rels) != 0 {
				t.Errorf("edges from a warm pool of an unscanned group = %+v (%v), want none", rels, err)
			}
		})
	}
}

// Group names are unique per region only: a child links to the same-named
// group in its own region and never to the one in another region.
func TestResolveAutoScalingChildEdges_SameGroupNameInTwoRegions(t *testing.T) {
	st := newTestStore(t)
	groupIDs := map[string]string{}
	for _, region := range []string{"us-east-1", "us-west-2"} {
		groupARN := "arn:aws:autoscaling:" + region + ":123456789012:autoScalingGroup:1:autoScalingGroupName/web"
		groupIDs[region] = upsertTestResourceNamed(t, st, TypeAutoScalingGroup, groupARN, region, `{}`, "web")
	}
	type child struct{ id, region string }
	var children []child
	for _, region := range []string{"us-east-1", "us-west-2"} {
		hook := upsertTestResourceNamed(t, st, TypeAutoScalingLifecycleHook, autoScalingLifecycleHookARN("aws", region, testAccountID, "web", "drain"), region,
			mustJSON(astypes.LifecycleHook{AutoScalingGroupName: ptrStr("web"), LifecycleHookName: ptrStr("drain")}), "drain")
		pool := upsertTestResourceNamed(t, st, TypeAutoScalingWarmPool, autoScalingWarmPoolARN("aws", region, testAccountID, "web"), region,
			mustJSON(astypes.WarmPoolConfiguration{MinSize: ptrInt32(1)}), "web")
		children = append(children, child{hook, region}, child{pool, region})
	}

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}
	for _, c := range children {
		rels, err := st.RelationshipsFrom(c.id, store.RelAttachedTo)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 1 {
			t.Errorf("edges from %s child = %+v, want one to its own region's group", c.region, rels)
		}
		assertRelationship(t, rels, c.id, groupIDs[c.region], store.RelAttachedTo)
	}
}

// A child links only to a group of the account being resolved: another
// account's same-named group in the same region gets no edge.
func TestResolveAutoScalingChildEdges_OtherAccountGroupIgnored(t *testing.T) {
	st := newTestStore(t)
	const otherAccount = "210987654321"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: otherAccount, Type: TypeAutoScalingGroup, Name: ptrStr("web"), Region: ptrStr(testRegion),
		NativeID:       "arn:aws:autoscaling:us-east-1:" + otherAccount + ":autoScalingGroup:1:autoScalingGroupName/web",
		AttributesJSON: `{}`, DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert other account's group: %v", err)
	}
	hook := upsertTestResourceNamed(t, st, TypeAutoScalingLifecycleHook, asTestHookWebDrain, testRegion,
		mustJSON(astypes.LifecycleHook{AutoScalingGroupName: ptrStr("web"), LifecycleHookName: ptrStr("drain")}), "drain")
	pool := upsertTestResourceNamed(t, st, TypeAutoScalingWarmPool, asTestWarmPoolWeb, testRegion,
		mustJSON(astypes.WarmPoolConfiguration{MinSize: ptrInt32(1)}), "web")

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}
	for _, id := range []string{hook, pool} {
		if rels, err := st.RelationshipsFrom(id, store.RelAttachedTo); err != nil || len(rels) != 0 {
			t.Errorf("edges from %s = %+v (%v), want none: its group is not in this account", id, rels, err)
		}
	}
}

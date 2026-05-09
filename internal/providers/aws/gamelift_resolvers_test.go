package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveGameLiftAliasFleet(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:fleet/fleet-1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftFleet, fARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:alias/alias-1", testRegion, acct.ID)
	attrs := `{"RoutingStrategy":{"FleetId":"fleet-1","Type":"SIMPLE"}}`
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftAlias, aARN, testRegion, attrs)
	if err := resolveGameLiftAliasFleet(acct, st); err != nil {
		t.Fatalf("resolveGameLiftAliasFleet: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, fID, store.RelRoutesTo)
}

func TestResolveGameLiftFleetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:build/b1", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftBuild, bARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/InstRole", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	fARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:fleet/fleet-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"BuildArn":%q,"InstanceRoleArn":%q}`, bARN, roleARN)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftFleet, fARN, testRegion, attrs)
	if err := resolveGameLiftFleetRefs(acct, st); err != nil {
		t.Fatalf("resolveGameLiftFleetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, bID, store.RelUses)
	assertRelationship(t, rels, fID, rID, store.RelAssumes)
}

func TestResolveGameLiftContainerFleetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/CFleet", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	cgdARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:containergroupdefinition/cgd1", testRegion, acct.ID)
	cgdID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftContainerGroupDefinition, cgdARN, testRegion, "{}")
	cfARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:containerfleet/cf1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"FleetRoleArn":%q,"GameServerContainerGroupDefinitionArn":%q}`, roleARN, cgdARN)
	cfID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftContainerFleet, cfARN, testRegion, attrs)
	if err := resolveGameLiftContainerFleetRefs(acct, st); err != nil {
		t.Fatalf("resolveGameLiftContainerFleetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfID)
	assertRelationship(t, rels, cfID, rID, store.RelAssumes)
	assertRelationship(t, rels, cfID, cgdID, store.RelUses)
}

func TestResolveGameLiftGameServerGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/GSG", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	asgARN := fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:abc:autoScalingGroupName/my-asg", testRegion, acct.ID)
	asgID := upsertTestResource(t, st, "aws", acct.ID, TypeAutoScalingGroup, asgARN, testRegion, "{}")
	gsgARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:gameservergroup/gsg1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RoleArn":%q,"AutoScalingGroupArn":%q}`, roleARN, asgARN)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftGameServerGroup, gsgARN, testRegion, attrs)
	if err := resolveGameLiftGameServerGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveGameLiftGameServerGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gID)
	assertRelationship(t, rels, gID, rID, store.RelAssumes)
	assertRelationship(t, rels, gID, asgID, store.RelAttachedTo)
}

func TestResolveGameLiftGameSessionQueueRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:fleet/f1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftFleet, fARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:alias/a1", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftAlias, aARN, testRegion, "{}")
	snsARN := fmt.Sprintf("arn:aws:sns:%s:%s:notify", testRegion, acct.ID)
	snsID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, snsARN, testRegion, "{}")
	qARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:gamesessionqueue/q1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Destinations":[{"DestinationArn":%q},{"DestinationArn":%q}],"NotificationTarget":%q}`, fARN, aARN, snsARN)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftGameSessionQueue, qARN, testRegion, attrs)
	if err := resolveGameLiftGameSessionQueueRefs(acct, st); err != nil {
		t.Fatalf("resolveGameLiftGameSessionQueueRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(qID)
	assertRelationship(t, rels, qID, fID, store.RelRoutesTo)
	assertRelationship(t, rels, qID, aID, store.RelRoutesTo)
	assertRelationship(t, rels, qID, snsID, store.RelRoutesTo)
}

func TestResolveGameLiftMatchmakingConfigRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	qARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:gamesessionqueue/q1", testRegion, acct.ID)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftGameSessionQueue, qARN, testRegion, "{}")
	rsARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:matchmakingruleset/rs1", testRegion, acct.ID)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftMatchmakingRuleSet, rsARN, testRegion, "{}")
	mcARN := fmt.Sprintf("arn:aws:gamelift:%s:%s:matchmakingconfiguration/mc1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"GameSessionQueueArns":[%q],"RuleSetArn":%q}`, qARN, rsARN)
	mcID := upsertTestResource(t, st, "aws", acct.ID, TypeGameLiftMatchmakingConfiguration, mcARN, testRegion, attrs)
	if err := resolveGameLiftMatchmakingConfigRefs(acct, st); err != nil {
		t.Fatalf("resolveGameLiftMatchmakingConfigRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mcID)
	assertRelationship(t, rels, mcID, qID, store.RelRoutesTo)
	assertRelationship(t, rels, mcID, rsID, store.RelUses)
}

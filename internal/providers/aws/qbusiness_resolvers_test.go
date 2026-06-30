package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveQBusinessChildrenToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:qbusiness:%s:%s:application/a1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessApplication, appARN, testRegion, "{}")
	idxARN := appARN + "/index/i1"
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessIndex, idxARN, testRegion, "{}")
	dsARN := appARN + "/index/i1/data-source/d1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessDataSource, dsARN, testRegion, "{}")
	crcARN := appARN + "/chat-response-configuration/c1"
	crcID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessChatResponseConfiguration, crcARN, testRegion, "{}")
	subARN := appARN + "/subscription/s1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessSubscription, subARN, testRegion, "{}")
	if err := resolveQBusinessChildrenToApp(acct, st); err != nil {
		t.Fatalf("resolveQBusinessChildrenToApp: %v", err)
	}
	rels, _ := st.RelationshipsFrom(idxID)
	assertRelationship(t, rels, idxID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(crcID)
	assertRelationship(t, rels, crcID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(subID)
	assertRelationship(t, rels, subID, appID, store.RelAttachedTo)
}

// TestResolveQBusinessChildrenToApp_NoRows guards the empty case — no children,
// no application: the resolver must return nil without emitting edges.
func TestResolveQBusinessChildrenToApp_NoRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveQBusinessChildrenToApp(acct, st); err != nil {
		t.Fatalf("resolveQBusinessChildrenToApp empty: %v", err)
	}
}

func TestResolveQBusinessDataSourceToIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idxARN := fmt.Sprintf("arn:aws:qbusiness:%s:%s:application/a1/index/i1", testRegion, acct.ID)
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessIndex, idxARN, testRegion, "{}")
	dsARN := idxARN + "/data-source/d1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessDataSource, dsARN, testRegion, "{}")
	if err := resolveQBusinessDataSourceToIndex(acct, st); err != nil {
		t.Fatalf("resolveQBusinessDataSourceToIndex: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, idxID, store.RelAttachedTo)
}

func TestResolveQBusinessDataAccessorRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/qb-isv", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	idcARN := fmt.Sprintf("arn:aws:sso::%s:application/ssoins-1/apl-abc", acct.ID)
	idcID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOApplication, idcARN, "", "{}")
	daARN := fmt.Sprintf("arn:aws:qbusiness:%s:%s:application/a1/data-accessor/da1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Principal":"%s","IdcApplicationArn":"%s"}`, roleARN, idcARN)
	daID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessDataAccessor, daARN, testRegion, attrs)
	if err := resolveQBusinessDataAccessorRefs(acct, st); err != nil {
		t.Fatalf("resolveQBusinessDataAccessorRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(daID)
	assertRelationship(t, rels, daID, roleID, store.RelUses)
	assertRelationship(t, rels, daID, idcID, store.RelAttachedTo)
}

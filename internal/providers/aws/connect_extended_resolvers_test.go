package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestConnectInstanceARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:connect:us-east-1:123:instance/abc/contact-flow/cf1", "arn:aws:connect:us-east-1:123:instance/abc"},
		{"arn:aws:connect:us-east-1:123:instance/abc", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := connectInstanceARNFromChild(c.in); got != c.want {
			t.Errorf("connectInstanceARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveConnectInstanceServiceRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/aws-service-role/connect.amazonaws.com/AWSServiceRoleForAmazonConnect_abc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	iARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Instance":{"Arn":%q,"ServiceRole":%q}}`, iARN, roleARN)
	iID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectInstance, iARN, testRegion, attrs)
	if err := resolveConnectInstanceServiceRole(acct, st); err != nil {
		t.Fatalf("resolveConnectInstanceServiceRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, rels, iID, roleID, store.RelAssumes)
}

func TestResolveConnectChildrenToInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc", testRegion, acct.ID)
	iID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectInstance, iARN, testRegion, "{}")
	cfARN := iARN + "/contact-flow/cf1"
	cfID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectContactFlow, cfARN, testRegion, "{}")
	hopARN := iARN + "/hours-of-operation/h1"
	hopID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectHoursOfOperation, hopARN, testRegion, "{}")
	if err := resolveConnectChildrenToInstance(acct, st); err != nil {
		t.Fatalf("resolveConnectChildrenToInstance: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfID)
	assertRelationship(t, rels, cfID, iID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(hopID)
	assertRelationship(t, rels, hopID, iID, store.RelAttachedTo)
}

func TestResolveConnectViewVersionToView(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc/view/v1", testRegion, acct.ID)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectView, vARN, testRegion, "{}")
	vvARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc/view-version/v1/1", testRegion, acct.ID)
	vvID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectViewVersion, vvARN, testRegion, "{}")
	if err := resolveConnectViewVersionToView(acct, st); err != nil {
		t.Fatalf("resolveConnectViewVersionToView: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vvID)
	assertRelationship(t, rels, vvID, vID, store.RelAttachedTo)
}

func TestResolveConnectDataTableChildrenToTable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc/data-table/t1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectDataTable, tARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/abc/data-table-attribute/t1/a1", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectDataTableAttribute, aARN, testRegion, "{}")
	if err := resolveConnectDataTableChildrenToTable(acct, st); err != nil {
		t.Fatalf("resolveConnectDataTableChildrenToTable: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, tID, store.RelAttachedTo)
}

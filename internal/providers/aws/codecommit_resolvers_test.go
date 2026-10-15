package aws

import (
	"testing"

	codecommittypes "github.com/aws/aws-sdk-go-v2/service/codecommit/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveCodeCommitTemplateRepos_LinksScannedRepositories(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := mustJSON(codeCommitTemplateAttrs{
		ApprovalRuleTemplate:      *ccTemplate("t-1", "id-t-1"),
		AssociatedRepositoryNames: []string{"repo-a", "repo-unscanned"},
	})
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitApprovalRuleTemplate, ccTemplateARN("id-t-1"), testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepository, ccRepoARN("repo-a"), testRegion, "{}")
	otherRegion := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepository, codeCommitRepoARN("us-west-2", acct.ID, "repo-unscanned"), "us-west-2", "{}")

	if err := resolveCodeCommitTemplateRepos(acct, st); err != nil {
		t.Fatalf("resolveCodeCommitTemplateRepos: %v", err)
	}
	rels, err := st.RelationshipsFrom(tID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tID, rID, store.RelAttachedTo)
	for _, r := range rels {
		if r.ToID == otherRegion {
			t.Errorf("template linked to a same-named repository in another region")
		}
	}
	if len(rels) != 1 {
		t.Errorf("relationships = %d, want 1", len(rels))
	}
}

func TestResolveCodeCommitTemplateRepos_NoAssociations(t *testing.T) {
	for name, attrs := range map[string]string{
		"no attrs":          "{}",
		"denied (null)":     mustJSON(codeCommitTemplateAttrs{ApprovalRuleTemplate: *ccTemplate("t-1", "id-t-1")}),
		"none (empty list)": mustJSON(codeCommitTemplateAttrs{ApprovalRuleTemplate: *ccTemplate("t-1", "id-t-1"), AssociatedRepositoryNames: []string{}}),
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			tID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitApprovalRuleTemplate, ccTemplateARN("id-t-1"), testRegion, attrs)
			upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepository, ccRepoARN("repo-a"), testRegion, "{}")
			if err := resolveCodeCommitTemplateRepos(acct, st); err != nil {
				t.Fatalf("resolveCodeCommitTemplateRepos: %v", err)
			}
			if rels, _ := st.RelationshipsFrom(tID); len(rels) != 0 {
				t.Errorf("relationships = %v, want none", rels)
			}
		})
	}
}

func TestCodeCommitTriggerTarget(t *testing.T) {
	for _, c := range []struct {
		in, wantID, wantType string
		wantOK               bool
	}{
		{"arn:aws:sns:us-east-1:123456789012:topic", "arn:aws:sns:us-east-1:123456789012:topic", TypeSNSTopic, true},
		{"arn:aws:lambda:us-east-1:123456789012:function:fn", "arn:aws:lambda:us-east-1:123456789012:function:fn", TypeLambdaFunction, true},
		{"arn:aws:lambda:us-east-1:123456789012:function:fn:prod", "arn:aws:lambda:us-east-1:123456789012:function:fn", TypeLambdaFunction, true},
		{"arn:aws:sqs:us-east-1:123456789012:q", "", "", false},
		{"arn:aws:lambda:us-east-1:123456789012:layer:l", "", "", false},
		{"", "", "", false},
		{"not-an-arn", "", "", false},
	} {
		id, typ, ok := codeCommitTriggerTarget(c.in)
		if id != c.wantID || typ != c.wantType || ok != c.wantOK {
			t.Errorf("codeCommitTriggerTarget(%q) = %q, %q, %v; want %q, %q, %v", c.in, id, typ, ok, c.wantID, c.wantType, c.wantOK)
		}
	}
}

func TestResolveCodeCommitTriggerDestinations_LinksScannedDestinations(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicARN := "arn:aws:sns:us-east-1:" + testAccountID + ":builds"
	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:notify"
	trigger := func(name, dest string) string {
		return mustJSON(codecommittypes.RepositoryTrigger{Name: ptrStr(name), DestinationArn: ptrStr(dest)})
	}
	snsTr := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepositoryTrigger, ccRepoARN("r")+"/trigger/sns", testRegion, trigger("sns", topicARN))
	fnTr := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepositoryTrigger, ccRepoARN("r")+"/trigger/fn", testRegion, trigger("fn", fnARN+":live"))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepositoryTrigger, ccRepoARN("r")+"/trigger/x", testRegion,
		trigger("x", "arn:aws:sns:us-east-1:999999999999:elsewhere"))
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveCodeCommitTriggerDestinations(acct, st); err != nil {
		t.Fatalf("resolveCodeCommitTriggerDestinations: %v", err)
	}
	for from, to := range map[string]string{snsTr: topicID, fnTr: fnID} {
		rels, _ := st.RelationshipsFrom(from)
		assertRelationship(t, rels, from, to, store.RelRoutesTo)
		if len(rels) != 1 {
			t.Errorf("relationships from %s = %d, want 1", from, len(rels))
		}
	}
	if rels, _ := st.RelationshipsFrom(unscanned); len(rels) != 0 {
		t.Errorf("unscanned destination produced %v, want none", rels)
	}
}

func TestResolveCodeCommitTriggerDestinations_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	trID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepositoryTrigger, ccRepoARN("r")+"/trigger/t", testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, "arn:aws:sns:us-east-1:"+testAccountID+":builds", testRegion, "{}")
	if err := resolveCodeCommitTriggerDestinations(acct, st); err != nil {
		t.Fatalf("resolveCodeCommitTriggerDestinations: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(trID); len(rels) != 0 {
		t.Errorf("relationships = %v, want none", rels)
	}
}

func TestResolveCodeCommitRepoKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repoARN := "arn:aws:codecommit:us-east-1:" + testAccountID + ":myrepo"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-cc"
	attrs := `{"KmsKeyId":"` + keyARN + `"}`

	rID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepository, repoARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveCodeCommitRepoKMS(acct, st); err != nil {
		t.Fatalf("resolveCodeCommitRepoKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, kID, store.RelUses)
}

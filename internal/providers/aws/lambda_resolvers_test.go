package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveLambdaRelationships verifies that a Lambda function's IAM role ARN
// is correctly extracted from AttributesJSON and produces an assumes relationship.
func TestResolveLambdaRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	roleARN := "arn:aws:iam::123456789012:role/my-lambda-role"
	fnARN := "arn:aws:lambda:us-east-1:123456789012:function:my-fn"
	attrsJSON := `{"Role": "` + roleARN + `"}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, "", attrsJSON)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != roleID || rels[0].Kind != store.RelAssumes {
		t.Errorf("expected fn -[assumes]-> role, got %+v", rels[0])
	}
}

// TestResolveLambdaRelationships_NoRole verifies that a function without a Role
// field produces no relationships and no error.
func TestResolveLambdaRelationships_NoRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	fnARN := "arn:aws:lambda:us-east-1:123456789012:function:bare-fn"
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, "", "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

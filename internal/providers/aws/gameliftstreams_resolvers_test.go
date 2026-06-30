package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	gameliftstreamstypes "github.com/aws/aws-sdk-go-v2/service/gameliftstreams/types"
)

func TestResolveGameLiftStreamsStreamGroupApplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	appARN := "arn:aws:gameliftstreams:us-east-1:123456789012:application/a-9ZY8X7Wv6"
	sgARN := "arn:aws:gameliftstreams:us-east-1:123456789012:streamgroup/sg-1AB2C3De4"

	appID := upsertTestResource(t, st, "aws", testAccountID, TypeGameLiftStreamsApplication, appARN, region,
		mustJSON(gameliftstreamstypes.ApplicationSummary{Arn: aws.String(appARN), Id: aws.String("a-9ZY8X7Wv6")}))
	sgID := upsertTestResource(t, st, "aws", testAccountID, TypeGameLiftStreamsStreamGroup, sgARN, region,
		mustJSON(gameliftstreamstypes.StreamGroupSummary{
			Arn:                aws.String(sgARN),
			Id:                 aws.String("sg-1AB2C3De4"),
			DefaultApplication: &gameliftstreamstypes.DefaultApplication{Arn: aws.String(appARN)},
		}))

	if err := resolveGameLiftStreamsStreamGroupApplication(acct, st); err != nil {
		t.Fatalf("resolveGameLiftStreamsStreamGroupApplication: %v", err)
	}

	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, sgID, appID, "uses")
}

func TestResolveGameLiftStreamsStreamGroupApplication_NoDefaultApplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	sgARN := "arn:aws:gameliftstreams:us-east-1:123456789012:streamgroup/sg-1AB2C3De4"
	sgID := upsertTestResource(t, st, "aws", testAccountID, TypeGameLiftStreamsStreamGroup, sgARN, region,
		mustJSON(gameliftstreamstypes.StreamGroupSummary{Arn: aws.String(sgARN), Id: aws.String("sg-1AB2C3De4")}))

	if err := resolveGameLiftStreamsStreamGroupApplication(acct, st); err != nil {
		t.Fatalf("resolveGameLiftStreamsStreamGroupApplication: %v", err)
	}

	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

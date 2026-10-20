package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidentitytypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentity/types"
)

func TestScanCognitoIdentityPools_StoresPoolWithoutResultMetadata(t *testing.T) {
	st := newTestStore(t)
	client := cognitoidentity.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListIdentityPools": func(any) (any, error) {
			return &cognitoidentity.ListIdentityPoolsOutput{IdentityPools: []cognitoidentitytypes.IdentityPoolShortDescription{
				{IdentityPoolId: ptrStr("us-east-1:pool-a"), IdentityPoolName: ptrStr("pool-a")},
			}}, nil
		},
		"DescribeIdentityPool": func(any) (any, error) {
			return &cognitoidentity.DescribeIdentityPoolOutput{IdentityPoolId: ptrStr("us-east-1:pool-a"), IdentityPoolName: ptrStr("pool-a")}, nil
		},
		"GetIdentityPoolRoles": func(any) (any, error) {
			return &cognitoidentity.GetIdentityPoolRolesOutput{Roles: map[string]string{"authenticated": "arn:aws:iam::123456789012:role/auth"}}, nil
		},
	}, bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: cognito-identity:Op")))

	ids, total, _, err := scanCognitoIdentityPools(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 || len(ids) != 1 {
		t.Fatalf("scan = %v, %d, %v; want [us-east-1:pool-a], 1, nil", ids, total, err)
	}
	arn := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/us-east-1:pool-a", testRegion, testAccountID)
	row, ok := rowsByNativeID(t, st, TypeCognitoIdentityPool)[arn]
	if !ok {
		t.Fatalf("no identity-pool row %s", arn)
	}
	var attrs struct {
		Pool  cognitoidentity.DescribeIdentityPoolOutput
		Roles map[string]string
	}
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
	}
	if sv(attrs.Pool.IdentityPoolName) != "pool-a" || attrs.Roles["authenticated"] == "" {
		t.Errorf("attrs = %s, want Pool.IdentityPoolName pool-a and the authenticated role", row.AttributesJSON)
	}
	if strings.Contains(row.AttributesJSON, "ResultMetadata") {
		t.Errorf("attrs carry the SDK's ResultMetadata envelope: %s", row.AttributesJSON)
	}
}

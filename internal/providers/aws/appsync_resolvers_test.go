package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestAppsyncApiARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appsync:us-east-1:123:apis/abc/datasources/my-ds", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"arn:aws:appsync:us-east-1:123:apis/abc/types/Query/resolvers/getX", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"arn:aws:appsync:us-east-1:123:apis/abc", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"", ""},
	}
	for _, c := range cases {
		if got := appsyncApiARNFromChild(c.in); got != c.want {
			t.Errorf("appsyncApiARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAppSyncApiChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apiARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/api-abc", testRegion, acct.ID)
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncGraphQLApi, apiARN, testRegion, "{}")
	dsARN := apiARN + "/datasources/my-ds"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDataSource, dsARN, testRegion, `{"Name":"my-ds"}`)

	if err := resolveAppSyncApiChildren(acct, st); err != nil {
		t.Fatalf("resolveAppSyncApiChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, apiID, store.RelAttachedTo)
}

func TestResolveAppSyncDataSourceTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/appsync-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	tblARN := dynamoTableARN(testRegion, acct.ID, "Users")
	tblID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, tblARN, testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	dsARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/api-abc/datasources/my-ds", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{
		"Name":"my-ds",
		"ServiceRoleArn":%q,
		"DynamodbConfig":{"TableName":"Users"},
		"LambdaConfig":{"LambdaFunctionArn":%q}
	}`, roleARN, fnARN)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDataSource, dsARN, testRegion, attrs)

	if err := resolveAppSyncDataSourceTargets(acct, st); err != nil {
		t.Fatalf("resolveAppSyncDataSourceTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, roleID, store.RelAssumes)
	assertRelationship(t, rels, dsID, tblID, store.RelUses)
	assertRelationship(t, rels, dsID, fnID, store.RelUses)
}

func TestResolveAppSyncResolverDataSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apiARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/api-z", testRegion, acct.ID)
	dsARN := apiARN + "/datasources/orders"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDataSource, dsARN, testRegion, `{"Name":"orders"}`)
	resARN := apiARN + "/types/Query/resolvers/getOrder"
	attrs := `{"DataSourceName":"orders"}`
	resID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncResolver, resARN, testRegion, attrs)

	if err := resolveAppSyncResolverDataSource(acct, st); err != nil {
		t.Fatalf("resolveAppSyncResolverDataSource: %v", err)
	}
	rels, _ := st.RelationshipsFrom(resID)
	assertRelationship(t, rels, resID, dsID, store.RelUses)
}

func TestResolveAppSyncDomainNameApiAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dnARN := fmt.Sprintf("arn:aws:appsync:%s:%s:domainnames/api.example.com", testRegion, acct.ID)
	dnID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDomainName, dnARN, testRegion, "{}")
	apiARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/api-1", testRegion, acct.ID)
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncGraphQLApi, apiARN, testRegion, "{}")

	assocARN := dnARN + "/apiassociation"
	attrs := `{"ApiId":"api-1"}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDomainNameApiAssociation, assocARN, testRegion, attrs)

	if err := resolveAppSyncDomainNameApiAssoc(acct, st); err != nil {
		t.Fatalf("resolveAppSyncDomainNameApiAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, dnID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, apiID, store.RelAttachedTo)
}

func TestResolveAppSyncGraphQLAPIRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apiARN := "arn:aws:appsync:us-east-1:" + testAccountID + ":apis/abc"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/appsync-logs"
	lambdaARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:authz"
	lambdaURI := lambdaARN + ":alias-v1"
	upARN := "arn:aws:cognito-idp:us-east-1:" + testAccountID + ":userpool/us-east-1_abc"
	attrs := `{"LogConfig":{"CloudWatchLogsRoleArn":"` + roleARN +
		`"},"LambdaAuthorizerConfig":{"AuthorizerUri":"` + lambdaURI +
		`"},"UserPoolConfig":{"UserPoolId":"us-east-1_abc","AwsRegion":"us-east-1"}}`

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncGraphQLApi, apiARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, upARN, testRegion, "{}")

	if err := resolveAppSyncGraphQLAPIRefs(acct, st); err != nil {
		t.Fatalf("resolveAppSyncGraphQLAPIRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
	assertRelationship(t, rels, aID, lID, store.RelUses)
	assertRelationship(t, rels, aID, uID, store.RelUses)
}

func TestResolveAppSyncEventApiRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apiARN := "arn:aws:appsync:us-east-1:" + testAccountID + ":apis/eventApi/api"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/eb-logs"
	lambdaARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:authz-event"
	upARN := "arn:aws:cognito-idp:us-east-1:" + testAccountID + ":userpool/us-east-1_evt"
	waclARN := "arn:aws:wafv2:us-east-1:" + testAccountID + ":regional/webacl/myWaf/abcd"
	attrs := `{"WafWebAclArn":"` + waclARN + `","EventConfig":{` +
		`"LogConfig":{"CloudWatchLogsRoleArn":"` + roleARN + `"},` +
		`"AuthProviders":[{"CognitoConfig":{"UserPoolId":"us-east-1_evt","AwsRegion":"us-east-1"}},` +
		`{"LambdaAuthorizerConfig":{"AuthorizerUri":"` + lambdaARN + `"}}]}}`

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncApi, apiARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, upARN, testRegion, "{}")
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, waclARN, testRegion, "{}")

	if err := resolveAppSyncEventApiRefs(acct, st); err != nil {
		t.Fatalf("resolveAppSyncEventApiRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
	assertRelationship(t, rels, aID, lID, store.RelUses)
	assertRelationship(t, rels, aID, uID, store.RelUses)
	assertRelationship(t, rels, aID, wID, store.RelUses)
}

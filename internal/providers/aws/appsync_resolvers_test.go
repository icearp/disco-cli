package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	appsynctypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
	"github.com/icearp/disco-cli/store"
)

func TestAppsyncApiARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appsync:us-east-1:123:apis/abc/datasources/my-ds", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"arn:aws:appsync:us-east-1:123:apis/abc/types/Query/resolvers/getX", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"arn:aws:appsync:us-east-1:123:apis/abc", "arn:aws:appsync:us-east-1:123:apis/abc"},
		{"", ""},
	}
	for _, c := range cases {
		if got := appsyncAPIARNFromChild(c.in); got != c.want {
			t.Errorf("appsyncAPIARNFromChild(%q) = %q, want %q", c.in, got, c.want)
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

	if err := resolveAppSyncAPIChildren(acct, st); err != nil {
		t.Fatalf("resolveAppSyncAPIChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, apiID, store.RelAttachedTo)
}

func TestResolveAppSyncDataSourceTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/appsync-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	// The table lives in the region DynamodbConfig names, not the data
	// source's; a same-named table in the data source's region gets no edge.
	tblARN := dynamoTableARN("aws", "eu-west-1", acct.ID, "Users")
	tblID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, tblARN, "eu-west-1", "{}")
	localTblID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, dynamoTableARN("aws", testRegion, acct.ID, "Users"), testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	dsARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/api-abc/datasources/my-ds", testRegion, acct.ID)
	attrs := mustJSON(appsynctypes.DataSource{
		Name: ptrStr("my-ds"), ServiceRoleArn: &roleARN,
		DynamodbConfig: &appsynctypes.DynamodbDataSourceConfig{TableName: ptrStr("Users"), AwsRegion: ptrStr("eu-west-1")},
		LambdaConfig:   &appsynctypes.LambdaDataSourceConfig{LambdaFunctionArn: &fnARN},
	})
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDataSource, dsARN, testRegion, attrs)

	if err := resolveAppSyncDataSourceTargets(acct, st); err != nil {
		t.Fatalf("resolveAppSyncDataSourceTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(dsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, dsID, roleID, store.RelAssumes)
	assertRelationship(t, rels, dsID, tblID, store.RelUses)
	assertRelationship(t, rels, dsID, fnID, store.RelUses)
	assertNoRelationshipTo(t, rels, localTblID)
}

// A row whose attributes cannot be decoded is returned as an error, and the
// rows before and after it still get their edges, in every AppSync resolver
// that decodes attributes.
func TestResolveAppSync_DecodeErrorKeepsOtherRows(t *testing.T) {
	acct := newTestAccount(testAccountID)
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	lambdaARN := "arn:aws:lambda:" + testRegion + ":" + testAccountID + ":function:f"
	apiARN := asyARN("apis/api-1")
	for _, tc := range []struct {
		name    string
		typ     string
		native  func(i int) string
		good    string
		target  string
		tgtType string
		kind    string
		seed    func(t *testing.T, st *store.Store)
		resolve func(*account, *store.Store) error
	}{
		{
			name: "data source", typ: TypeAppSyncDataSource,
			native: func(i int) string { return fmt.Sprintf("%s/datasources/ds%d", apiARN, i) },
			good:   mustJSON(appsynctypes.DataSource{ServiceRoleArn: &roleARN}), target: roleARN, tgtType: TypeIAMRole, kind: store.RelAssumes,
			resolve: resolveAppSyncDataSourceTargets,
		},
		{
			name: "resolver", typ: TypeAppSyncResolver,
			native: func(i int) string { return fmt.Sprintf("%s/types/Query/resolvers/f%d", apiARN, i) },
			good:   mustJSON(appsynctypes.Resolver{DataSourceName: ptrStr("ds")}), target: apiARN + "/datasources/ds", tgtType: TypeAppSyncDataSource, kind: store.RelUses,
			resolve: resolveAppSyncResolverDataSource,
		},
		{
			name: "function", typ: TypeAppSyncFunctionConfiguration,
			native: func(i int) string { return fmt.Sprintf("%s/functions/f%d", apiARN, i) },
			good:   mustJSON(appsynctypes.FunctionConfiguration{DataSourceName: ptrStr("ds")}), target: apiARN + "/datasources/ds", tgtType: TypeAppSyncDataSource, kind: store.RelUses,
			resolve: resolveAppSyncFunctionDataSource,
		},
		{
			name: "source api association", typ: TypeAppSyncSourceAPIAssociation,
			native: func(i int) string { return fmt.Sprintf("%s/sourceApiAssociations/a%d", asyARN("mergedApis/m"), i) },
			good:   mustJSON(appsynctypes.SourceApiAssociationSummary{MergedApiArn: &apiARN}), target: apiARN, tgtType: TypeAppSyncGraphQLApi, kind: store.RelAttachedTo,
			resolve: resolveAppSyncSourceAPIAssoc,
		},
		{
			name: "domain name association", typ: TypeAppSyncDomainNameAPIAssociation,
			native: func(i int) string { return asyARN(fmt.Sprintf("domainnames/d%d.example.com/apiassociation", i)) },
			good:   mustJSON(appsynctypes.ApiAssociation{ApiId: ptrStr("api-1")}), target: apiARN, tgtType: TypeAppSyncGraphQLApi, kind: store.RelAttachedTo,
			resolve: resolveAppSyncDomainNameAPIAssoc,
		},
		{
			name: "graphql api", typ: TypeAppSyncGraphQLApi,
			native: func(i int) string { return asyARN(fmt.Sprintf("apis/g%d", i)) },
			good:   mustJSON(appsynctypes.GraphqlApi{LambdaAuthorizerConfig: &appsynctypes.LambdaAuthorizerConfig{AuthorizerUri: &lambdaARN}}), target: lambdaARN, tgtType: TypeLambdaFunction, kind: store.RelUses,
			resolve: resolveAppSyncGraphQLAPIRefs,
		},
		{
			name: "event api", typ: TypeAppSyncAPI,
			native: func(i int) string { return asyARN(fmt.Sprintf("apis/e%d", i)) },
			good: mustJSON(appsynctypes.Api{EventConfig: &appsynctypes.EventConfig{AuthProviders: []appsynctypes.AuthProvider{
				{AuthType: appsynctypes.AuthenticationTypeAwsLambda, LambdaAuthorizerConfig: &appsynctypes.LambdaAuthorizerConfig{AuthorizerUri: &lambdaARN}},
			}}}), target: lambdaARN, tgtType: TypeLambdaFunction, kind: store.RelUses,
			resolve: resolveAppSyncEventAPIRefs,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var tgtAttrs string
			if tc.tgtType == TypeAppSyncDataSource {
				tgtAttrs = mustJSON(appsynctypes.DataSource{Name: ptrStr("ds")})
			} else {
				tgtAttrs = "{}"
			}
			tgtID := upsertTestResource(t, st, "aws", acct.ID, tc.tgtType, tc.target, testRegion, tgtAttrs)
			goodA := upsertTestResourceNamed(t, st, tc.typ, tc.native(1), testRegion, tc.good, "a")
			upsertTestResourceNamed(t, st, tc.typ, tc.native(2), testRegion, `[1]`, "b")
			goodC := upsertTestResourceNamed(t, st, tc.typ, tc.native(3), testRegion, tc.good, "c")
			err := tc.resolve(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("err = %v; want a *json.UnmarshalTypeError for the undecodable row", err)
			}
			for _, id := range []string{goodA, goodC} {
				rels, rerr := st.RelationshipsFrom(id)
				if rerr != nil {
					t.Fatalf("RelationshipsFrom: %v", rerr)
				}
				assertRelationship(t, rels, id, tgtID, tc.kind)
			}
		})
	}
}

// A data source whose attributes cannot be decoded is returned as an error
// by the resolvers that index data sources by name, and a data source
// indexed after it still gets its edge.
func TestResolveAppSync_DataSourceIndexDecodeError(t *testing.T) {
	acct := newTestAccount(testAccountID)
	apiARN := asyARN("apis/api-1")
	for _, tc := range []struct {
		name, typ, native, attrs string
		resolve                  func(*account, *store.Store) error
	}{
		{"resolver", TypeAppSyncResolver, apiARN + "/types/Query/resolvers/f", mustJSON(appsynctypes.Resolver{DataSourceName: ptrStr("dsc")}), resolveAppSyncResolverDataSource},
		{"function", TypeAppSyncFunctionConfiguration, apiARN + "/functions/f", mustJSON(appsynctypes.FunctionConfiguration{DataSourceName: ptrStr("dsc")}), resolveAppSyncFunctionDataSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResourceNamed(t, st, TypeAppSyncDataSource, apiARN+"/datasources/dsa", testRegion, mustJSON(appsynctypes.DataSource{Name: ptrStr("dsa")}), "a")
			upsertTestResourceNamed(t, st, TypeAppSyncDataSource, apiARN+"/datasources/dsb", testRegion, `[1]`, "b")
			dsC := upsertTestResourceNamed(t, st, TypeAppSyncDataSource, apiARN+"/datasources/dsc", testRegion, mustJSON(appsynctypes.DataSource{Name: ptrStr("dsc")}), "c")
			fromID := upsertTestResource(t, st, "aws", acct.ID, tc.typ, tc.native, testRegion, tc.attrs)
			err := tc.resolve(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("err = %v; want a *json.UnmarshalTypeError for the undecodable data source", err)
			}
			rels, rerr := st.RelationshipsFrom(fromID)
			if rerr != nil {
				t.Fatalf("RelationshipsFrom: %v", rerr)
			}
			assertRelationship(t, rels, fromID, dsC, store.RelUses)
		})
	}
}

// ARNs rebuilt from bare ids (a DynamoDB table, a Cognito user pool, the API
// a domain is associated with) take the partition of the row naming them.
func TestResolveAppSync_RebuiltARNsKeepPartition(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const region = "cn-north-1"
	cn := func(svc, rest string) string {
		return "arn:aws-cn:" + svc + ":" + region + ":" + testAccountID + ":" + rest
	}

	tblID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, cn("dynamodb", "table/Users"), region, "{}")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDataSource, cn("appsync", "apis/a1/datasources/ds"), region,
		mustJSON(appsynctypes.DataSource{DynamodbConfig: &appsynctypes.DynamodbDataSourceConfig{TableName: ptrStr("Users"), AwsRegion: ptrStr(region)}}))
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, cn("cognito-idp", "userpool/cn-north-1_p"), region, "{}")
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncGraphQLApi, cn("appsync", "apis/a1"), region,
		mustJSON(appsynctypes.GraphqlApi{UserPoolConfig: &appsynctypes.UserPoolConfig{UserPoolId: ptrStr("cn-north-1_p"), AwsRegion: ptrStr(region)}}))
	evtID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncAPI, cn("appsync", "apis/e1"), region,
		mustJSON(appsynctypes.Api{EventConfig: &appsynctypes.EventConfig{AuthProviders: []appsynctypes.AuthProvider{
			{AuthType: appsynctypes.AuthenticationTypeAmazonCognitoUserPools, CognitoConfig: &appsynctypes.CognitoConfig{UserPoolId: ptrStr("cn-north-1_p"), AwsRegion: ptrStr(region)}},
		}}}))
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDomainNameAPIAssociation, cn("appsync", "domainnames/api.example.cn/apiassociation"), region,
		mustJSON(appsynctypes.ApiAssociation{ApiId: ptrStr("a1")}))

	for _, tc := range []struct {
		name          string
		resolve       func(*account, *store.Store) error
		from, to, rel string
	}{
		{"data source table", resolveAppSyncDataSourceTargets, dsID, tblID, store.RelUses},
		{"graphql api user pool", resolveAppSyncGraphQLAPIRefs, apiID, poolID, store.RelUses},
		{"event api user pool", resolveAppSyncEventAPIRefs, evtID, poolID, store.RelUses},
		{"domain association api", resolveAppSyncDomainNameAPIAssoc, assocID, apiID, store.RelAttachedTo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.resolve(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(tc.from)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, tc.from, tc.to, tc.rel)
		})
	}
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
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncDomainNameAPIAssociation, assocARN, testRegion, attrs)

	if err := resolveAppSyncDomainNameAPIAssoc(acct, st); err != nil {
		t.Fatalf("resolveAppSyncDomainNameAPIAssoc: %v", err)
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

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeAppSyncAPI, apiARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, upARN, testRegion, "{}")
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, waclARN, testRegion, "{}")

	if err := resolveAppSyncEventAPIRefs(acct, st); err != nil {
		t.Fatalf("resolveAppSyncEventAPIRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
	assertRelationship(t, rels, aID, lID, store.RelUses)
	assertRelationship(t, rels, aID, uID, store.RelUses)
	assertRelationship(t, rels, aID, wID, store.RelUses)
}

// A row whose NativeID is not an ARN cannot give the partition of the ARN
// rebuilt from it: that is returned as an error, and the row's other edges
// are still emitted.
func TestResolveAppSync_UnparseableNativeIDReported(t *testing.T) {
	acct := newTestAccount(testAccountID)
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	pool := &appsynctypes.UserPoolConfig{UserPoolId: ptrStr("p"), AwsRegion: ptrStr(testRegion)}
	for _, tc := range []struct {
		name, typ, attrs string
		resolve          func(*account, *store.Store) error
	}{
		{"data source table", TypeAppSyncDataSource, mustJSON(appsynctypes.DataSource{
			ServiceRoleArn: &roleARN,
			DynamodbConfig: &appsynctypes.DynamodbDataSourceConfig{TableName: ptrStr("t"), AwsRegion: ptrStr(testRegion)},
		}), resolveAppSyncDataSourceTargets},
		{"graphql api user pool", TypeAppSyncGraphQLApi, mustJSON(appsynctypes.GraphqlApi{
			LogConfig: &appsynctypes.LogConfig{CloudWatchLogsRoleArn: &roleARN}, UserPoolConfig: pool,
		}), resolveAppSyncGraphQLAPIRefs},
		{"event api user pool", TypeAppSyncAPI, mustJSON(appsynctypes.Api{EventConfig: &appsynctypes.EventConfig{
			LogConfig: &appsynctypes.EventLogConfig{CloudWatchLogsRoleArn: &roleARN},
			AuthProviders: []appsynctypes.AuthProvider{
				{AuthType: appsynctypes.AuthenticationTypeAmazonCognitoUserPools, CognitoConfig: &appsynctypes.CognitoConfig{UserPoolId: ptrStr("p"), AwsRegion: ptrStr(testRegion)}},
			},
		}}), resolveAppSyncEventAPIRefs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
			id := upsertTestResource(t, st, "aws", acct.ID, tc.typ, "apis/g1/datasources/ds", testRegion, tc.attrs)
			if err := tc.resolve(acct, st); err == nil {
				t.Errorf("resolve: nil error; want the unparseable NativeID reported")
			}
			rels, err := st.RelationshipsFrom(id)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, id, roleID, store.RelAssumes)
		})
	}
}

// A failed edge write is returned joined with the decode errors of the rows
// before it.
func TestResolveAppSync_WriteFailureKeepsDecodeErrors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	upsertTestResourceNamed(t, st, TypeAppSyncDataSource, asyARN("apis/g1/datasources/a"), testRegion, `[1]`, "a")
	upsertTestResourceNamed(t, st, TypeAppSyncDataSource, asyARN("apis/g1/datasources/b"), testRegion, mustJSON(appsynctypes.DataSource{ServiceRoleArn: &roleARN}), "b")
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON relationships
		BEGIN SELECT RAISE(ABORT, 'test: edge write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	err := resolveAppSyncDataSourceTargets(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v; want the write failure joined with the earlier row's *json.UnmarshalTypeError", err)
	}
}

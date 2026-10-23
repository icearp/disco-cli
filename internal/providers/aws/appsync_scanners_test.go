package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/appsync"
	appsynctypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	asyDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: appsync:List"}
	asyUnauth    = &smithy.GenericAPIError{Code: "UnauthorizedException", Message: "You are not authorized to make this call."}
	asyBadReq    = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
	asyNotFound  = &smithy.GenericAPIError{Code: "NotFoundException", Message: "not found"}
	asyConflict  = &smithy.GenericAPIError{Code: "ConcurrentModificationException", Message: "busy"}
	asyThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

func asyARN(path string) string {
	return fmt.Sprintf("arn:aws:appsync:%s:%s:%s", testRegion, testAccountID, path)
}

// The fixture: GraphQL APIs g1 (tagged) and g2 (untagged, unnamed) over two
// pages, plus one listed without an Arn and one without an ApiId; Event APIs
// e1 (tagged) and e2; domain names d1 (tagged) and d2; and the children of
// g1, e1 and d1.
var (
	asyG1 = appsynctypes.GraphqlApi{
		ApiId: ptrStr("g1"), Arn: ptrStr(asyARN("apis/g1")), Name: ptrStr("gql-one"),
		ApiType: appsynctypes.GraphQLApiTypeGraphql, Tags: map[string]string{"env": "prod"},
	}
	asyG2     = appsynctypes.GraphqlApi{ApiId: ptrStr("g2"), Arn: ptrStr(asyARN("apis/g2"))}
	asyGNoArn = appsynctypes.GraphqlApi{ApiId: ptrStr("g3"), Name: ptrStr("no-arn")}
	asyGNoID  = appsynctypes.GraphqlApi{Arn: ptrStr(asyARN("apis/noid")), Name: ptrStr("no-id")}
	asyE1     = appsynctypes.Api{ApiId: ptrStr("e1"), ApiArn: ptrStr(asyARN("apis/e1")), Name: ptrStr("evt-one"), Tags: map[string]string{"team": "rt"}}
	asyE2     = appsynctypes.Api{ApiId: ptrStr("e2"), ApiArn: ptrStr(asyARN("apis/e2")), Name: ptrStr("evt-two")}
	asyD1     = appsynctypes.DomainNameConfig{
		DomainName: ptrStr("api.example.com"), DomainNameArn: ptrStr(asyARN("domainnames/api.example.com")),
		Tags: map[string]string{"zone": "pub"},
	}
	asyD2       = appsynctypes.DomainNameConfig{DomainName: ptrStr("two.example.com"), DomainNameArn: ptrStr(asyARN("domainnames/two.example.com"))}
	asyKey      = appsynctypes.ApiKey{Id: ptrStr("da2-key1"), Description: ptrStr("ci key"), Expires: 1700000000}
	asyKey2     = appsynctypes.ApiKey{Id: ptrStr("da2-key2"), Expires: 1700000000}
	asyCache    = appsynctypes.ApiCache{Type: appsynctypes.ApiCacheTypeT2Small, Ttl: 300}
	asyDS       = appsynctypes.DataSource{DataSourceArn: ptrStr(asyARN("apis/g1/datasources/ds1")), Name: ptrStr("ds1")}
	asyFn       = appsynctypes.FunctionConfiguration{FunctionArn: ptrStr(asyARN("apis/g1/functions/fn1")), FunctionId: ptrStr("fn1"), Name: ptrStr("fn-one")}
	asyAssoc    = appsynctypes.SourceApiAssociationSummary{AssociationArn: ptrStr(asyARN("mergedApis/g1/sourceApiAssociations/a1")), AssociationId: ptrStr("a1")}
	asyNS1      = appsynctypes.ChannelNamespace{ApiId: ptrStr("e1"), ChannelNamespaceArn: ptrStr(asyARN("apis/e1/channelNamespace/ns1")), Name: ptrStr("ns1"), Tags: map[string]string{"ch": "1"}}
	asyNS2      = appsynctypes.ChannelNamespace{ApiId: ptrStr("e1"), ChannelNamespaceArn: ptrStr(asyARN("apis/e1/channelNamespace/ns2")), Name: ptrStr("ns2")}
	asyDomAssoc = appsynctypes.ApiAssociation{ApiId: ptrStr("g1"), DomainName: ptrStr("api.example.com"), AssociationStatus: appsynctypes.AssociationStatusSuccess}
	asyResQuery = appsynctypes.Resolver{ResolverArn: ptrStr(asyARN("apis/g1/types/Query/resolvers/getX")), TypeName: ptrStr("Query"), FieldName: ptrStr("getX")}
	asyResMut   = appsynctypes.Resolver{ResolverArn: ptrStr(asyARN("apis/g1/types/Mutation/resolvers/setX")), TypeName: ptrStr("Mutation"), FieldName: ptrStr("setX")}
)

// asyPinPage fails a list call whose NextToken is neither absent (page one)
// nor "p2", or whose MaxResults is not the model's cap.
func asyPinPage(token *string, maxResults int32) error {
	if t := sv(token); t != "" && t != "p2" {
		return fmt.Errorf("unexpected NextToken %q", t)
	}
	if maxResults != appsyncPageMax {
		return fmt.Errorf("MaxResults = %d, want %d", maxResults, appsyncPageMax)
	}
	return nil
}

// asyPinAPI fails a per-API call keyed on an API the fixture's phases do not
// hand to it.
func asyPinAPI(id *string, want ...string) error {
	if !slices.Contains(want, sv(id)) {
		return fmt.Errorf("unexpected ApiId %q, want one of %v", sv(id), want)
	}
	return nil
}

// asyHandlers serves the fixture; over replaces the handler of an op.
func asyHandlers(over map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListGraphqlApis": func(in any) (any, error) {
			p := in.(*appsync.ListGraphqlApisInput)
			if err := asyPinPage(p.NextToken, p.MaxResults); err != nil {
				return nil, err
			}
			if p.Owner != "" || p.ApiType != "" {
				return nil, fmt.Errorf("Owner/ApiType = %q/%q, want no filter", p.Owner, p.ApiType)
			}
			if p.NextToken == nil {
				return &appsync.ListGraphqlApisOutput{GraphqlApis: []appsynctypes.GraphqlApi{asyG1}, NextToken: ptrStr("p2")}, nil
			}
			return &appsync.ListGraphqlApisOutput{GraphqlApis: []appsynctypes.GraphqlApi{asyG2, asyGNoArn, asyGNoID}}, nil
		},
		"ListApis": func(in any) (any, error) {
			p := in.(*appsync.ListApisInput)
			if err := asyPinPage(p.NextToken, p.MaxResults); err != nil {
				return nil, err
			}
			return &appsync.ListApisOutput{Apis: []appsynctypes.Api{asyE1, asyE2}}, nil
		},
		"ListDomainNames": func(in any) (any, error) {
			p := in.(*appsync.ListDomainNamesInput)
			if err := asyPinPage(p.NextToken, p.MaxResults); err != nil {
				return nil, err
			}
			return &appsync.ListDomainNamesOutput{DomainNameConfigs: []appsynctypes.DomainNameConfig{asyD1, asyD2}}, nil
		},
		"ListApiKeys": func(in any) (any, error) {
			p := in.(*appsync.ListApiKeysInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1", "g2")); err != nil {
				return nil, err
			}
			out := &appsync.ListApiKeysOutput{}
			if sv(p.ApiId) == "g1" {
				out.ApiKeys = []appsynctypes.ApiKey{asyKey, asyKey2}
			}
			return out, nil
		},
		"GetApiCache": func(in any) (any, error) {
			p := in.(*appsync.GetApiCacheInput)
			if err := asyPinAPI(p.ApiId, "g1", "g2"); err != nil {
				return nil, err
			}
			if sv(p.ApiId) == "g2" {
				return nil, asyNotFound
			}
			c := asyCache
			return &appsync.GetApiCacheOutput{ApiCache: &c}, nil
		},
		"ListDataSources": func(in any) (any, error) {
			p := in.(*appsync.ListDataSourcesInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1", "g2")); err != nil {
				return nil, err
			}
			out := &appsync.ListDataSourcesOutput{}
			if sv(p.ApiId) == "g1" {
				out.DataSources = []appsynctypes.DataSource{asyDS}
			}
			return out, nil
		},
		"ListFunctions": func(in any) (any, error) {
			p := in.(*appsync.ListFunctionsInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1", "g2")); err != nil {
				return nil, err
			}
			out := &appsync.ListFunctionsOutput{}
			if sv(p.ApiId) == "g1" {
				out.Functions = []appsynctypes.FunctionConfiguration{asyFn}
			}
			return out, nil
		},
		"ListSourceApiAssociations": func(in any) (any, error) {
			p := in.(*appsync.ListSourceApiAssociationsInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1", "g2")); err != nil {
				return nil, err
			}
			if sv(p.ApiId) == "g2" {
				return nil, asyBadReq
			}
			return &appsync.ListSourceApiAssociationsOutput{SourceApiAssociationSummaries: []appsynctypes.SourceApiAssociationSummary{asyAssoc}}, nil
		},
		"ListChannelNamespaces": func(in any) (any, error) {
			p := in.(*appsync.ListChannelNamespacesInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "e1", "e2")); err != nil {
				return nil, err
			}
			out := &appsync.ListChannelNamespacesOutput{}
			if sv(p.ApiId) == "e1" {
				out.ChannelNamespaces = []appsynctypes.ChannelNamespace{asyNS1, asyNS2}
			}
			return out, nil
		},
		"GetApiAssociation": func(in any) (any, error) {
			p := in.(*appsync.GetApiAssociationInput)
			switch sv(p.DomainName) {
			case "api.example.com":
				a := asyDomAssoc
				return &appsync.GetApiAssociationOutput{ApiAssociation: &a}, nil
			case "two.example.com":
				return nil, asyNotFound
			}
			return nil, fmt.Errorf("unexpected DomainName %q", sv(p.DomainName))
		},
		"ListTypes": func(in any) (any, error) {
			p := in.(*appsync.ListTypesInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1", "g2")); err != nil {
				return nil, err
			}
			if p.Format != appsynctypes.TypeDefinitionFormatSdl {
				return nil, fmt.Errorf("Format = %q, want SDL", p.Format)
			}
			switch {
			case sv(p.ApiId) == "g2":
				return &appsync.ListTypesOutput{}, nil
			case p.NextToken == nil:
				return &appsync.ListTypesOutput{Types: []appsynctypes.Type{{Name: ptrStr("Query")}}, NextToken: ptrStr("p2")}, nil
			}
			return &appsync.ListTypesOutput{Types: []appsynctypes.Type{{Name: ptrStr("Mutation")}, {}}}, nil
		},
		"ListResolvers": func(in any) (any, error) {
			p := in.(*appsync.ListResolversInput)
			if err := asyFirstErr(asyPinPage(p.NextToken, p.MaxResults), asyPinAPI(p.ApiId, "g1")); err != nil {
				return nil, err
			}
			switch sv(p.TypeName) {
			case "Query":
				return &appsync.ListResolversOutput{Resolvers: []appsynctypes.Resolver{asyResQuery}}, nil
			case "Mutation":
				return &appsync.ListResolversOutput{Resolvers: []appsynctypes.Resolver{asyResMut}}, nil
			}
			return nil, fmt.Errorf("unexpected TypeName %q", sv(p.TypeName))
		},
	}
	for op, f := range over {
		h[op] = f
	}
	return h
}

// asyFirstErr returns the first non-nil error of errs.
func asyFirstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// asyOpWarnings counts the scan warnings per op.
func asyOpWarnings(st *store.Store) func(op string) int {
	var (
		mu sync.Mutex
		n  = map[string]int{}
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		n[w.Service]++
	}
	return func(op string) int {
		mu.Lock()
		defer mu.Unlock()
		return n[op]
	}
}

func scanAppSyncTest(t *testing.T, st *store.Store, over map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	client := appsync.NewFromConfig(opStubConfig(asyHandlers(over), asyDenied))
	total, _, err := scanAppSyncAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// asyWantRow is a row the fixture stores: tags nil means none.
type asyWantRow struct {
	typ, nativeID, name string
	attrs               any
	tags                map[string]string
}

var asyAllRows = []asyWantRow{
	{TypeAppSyncGraphQLApi, asyARN("apis/g1"), "gql-one", asyG1, map[string]string{"env": "prod"}},
	{TypeAppSyncGraphQLApi, asyARN("apis/g2"), "g2", asyG2, nil},
	{TypeAppSyncGraphQLApi, asyARN("apis/noid"), "no-id", asyGNoID, nil},
	{TypeAppSyncAPI, asyARN("apis/e1"), "evt-one", asyE1, map[string]string{"team": "rt"}},
	{TypeAppSyncAPI, asyARN("apis/e2"), "evt-two", asyE2, nil},
	{TypeAppSyncDomainName, asyARN("domainnames/api.example.com"), "api.example.com", asyD1, map[string]string{"zone": "pub"}},
	{TypeAppSyncDomainName, asyARN("domainnames/two.example.com"), "two.example.com", asyD2, nil},
	{TypeAppSyncAPIKey, asyARN("apis/g1/apikeys/da2-key1"), "ci key", asyRedactedKey(asyKey), nil},
	{TypeAppSyncAPIKey, asyARN("apis/g1/apikeys/da2-key2"), "", asyRedactedKey(asyKey2), nil},
	{TypeAppSyncAPICache, asyARN("apis/g1/apicache/_"), "api-cache", asyCache, nil},
	{TypeAppSyncDataSource, asyARN("apis/g1/datasources/ds1"), "ds1", asyDS, nil},
	{TypeAppSyncFunctionConfiguration, asyARN("apis/g1/functions/fn1"), "fn-one", asyFn, nil},
	{TypeAppSyncGraphQLSchema, asyARN("apis/g1/schema/_"), "graphql-schema", map[string]string{"ApiId": "g1"}, nil},
	{TypeAppSyncGraphQLSchema, asyARN("apis/g2/schema/_"), "graphql-schema", map[string]string{"ApiId": "g2"}, nil},
	{TypeAppSyncSourceAPIAssociation, asyARN("mergedApis/g1/sourceApiAssociations/a1"), "a1", asyAssoc, nil},
	{TypeAppSyncChannelNamespace, asyARN("apis/e1/channelNamespace/ns1"), "ns1", asyNS1, map[string]string{"ch": "1"}},
	{TypeAppSyncChannelNamespace, asyARN("apis/e1/channelNamespace/ns2"), "ns2", asyNS2, nil},
	{TypeAppSyncDomainNameAPIAssociation, asyARN("domainnames/api.example.com/apiassociation"), "api.example.com", asyDomAssoc, nil},
	{TypeAppSyncResolver, asyARN("apis/g1/types/Query/resolvers/getX"), "getX", asyResQuery, nil},
	{TypeAppSyncResolver, asyARN("apis/g1/types/Mutation/resolvers/setX"), "setX", asyResMut, nil},
}

// asyRedactedKey is k as stored: its Id, the key itself, redacted.
func asyRedactedKey(k appsynctypes.ApiKey) appsynctypes.ApiKey {
	k.Id = ptrStr(redact.Placeholder)
	return k
}

// asyTypes is every type the scanner stores.
var asyTypes = []string{
	TypeAppSyncGraphQLApi, TypeAppSyncAPI, TypeAppSyncDomainName, TypeAppSyncAPIKey, TypeAppSyncAPICache,
	TypeAppSyncDataSource, TypeAppSyncFunctionConfiguration, TypeAppSyncGraphQLSchema,
	TypeAppSyncSourceAPIAssociation, TypeAppSyncChannelNamespace, TypeAppSyncDomainNameAPIAssociation, TypeAppSyncResolver,
}

// asyStored returns every stored AppSync row by NativeID.
func asyStored(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	all := map[string]store.Resource{}
	for _, typ := range asyTypes {
		for id, r := range rowsByNativeID(t, st, typ) {
			all[id] = r
		}
	}
	return all
}

// assertASRow checks a stored row's type, name ("" for none), region,
// attributes and tags.
func assertASRow(t *testing.T, got map[string]store.Resource, want asyWantRow) {
	t.Helper()
	r, ok := got[want.nativeID]
	if !ok {
		t.Errorf("%s not stored", want.nativeID)
		return
	}
	if (want.name == "") != (r.Name == nil) {
		t.Errorf("%s: name = %v; want %q (unset when empty)", want.nativeID, r.Name, want.name)
	}
	if r.Type != want.typ || sv(r.Name) != want.name || sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.attrs) {
		t.Errorf("%s: type/name/region = %s/%q/%q, attrs = %s; want %s/%q/%q, %s",
			want.nativeID, r.Type, sv(r.Name), sv(r.Region), r.AttributesJSON, want.typ, want.name, testRegion, mustJSON(want.attrs))
	}
	if want.tags == nil {
		if r.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", want.nativeID, *r.TagsJSON)
		}
		return
	}
	assertTagsMap(t, r.TagsJSON, want.tags)
}

// The whole service stores every listed resource with its own tags, warns
// once each for the GraphQL APIs listed without an Arn or an ApiId, the API
// whose source associations were refused and the type listed without a
// name, and skips the cache and domain association the service does not
// find without a warning.
func TestScanAppSyncAll_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := asyOpWarnings(st)
	total, err := scanAppSyncTest(t, st, nil)
	if err != nil {
		t.Fatalf("scanAppSyncAll: %v", err)
	}
	if total != len(asyAllRows) {
		t.Errorf("total = %d; want %d", total, len(asyAllRows))
	}
	got := asyStored(t, st)
	for _, want := range asyAllRows {
		assertASRow(t, got, want)
	}
	if len(got) != len(asyAllRows) {
		t.Errorf("stored %d rows; want %d", len(got), len(asyAllRows))
	}
	for op, want := range map[string]int{
		appsyncGraphqlApisOp: 2, appsyncSourceAssocsOp: 1, appsyncTypesOp: 1,
		appsyncAPICacheOp: 0, appsyncAPIAssociationOp: 0, appsyncEventApisOp: 0, appsyncDomainNamesOp: 0,
	} {
		if n := warns(op); n != want {
			t.Errorf("%s warnings = %d; want %d", op, n, want)
		}
	}
}

// A failed call stores what was read before it, does not stop the other
// parents or the later phases, and comes back as the phase's error.
func TestScanAppSyncAll_FailureKeepsRowsAndLaterPhases(t *testing.T) {
	failG1 := func(op string, err error, apiID func(any) string) map[string]func(any) (any, error) {
		base := asyHandlers(nil)[op]
		return map[string]func(any) (any, error){op: func(in any) (any, error) {
			if apiID(in) == "g1" {
				return nil, err
			}
			return base(in)
		}}
	}
	for _, tc := range []struct {
		name     string
		over     map[string]func(any) (any, error)
		code     string
		lost     []string
		wantKept []string
	}{
		{
			name: "graphql page two",
			over: map[string]func(any) (any, error){"ListGraphqlApis": func(in any) (any, error) {
				if in.(*appsync.ListGraphqlApisInput).NextToken != nil {
					return nil, asyBadReq
				}
				return asyHandlers(nil)["ListGraphqlApis"](in)
			}},
			code:     "BadRequestException",
			lost:     []string{asyARN("apis/g2"), asyARN("apis/g2/schema/_"), asyARN("apis/noid")},
			wantKept: []string{asyARN("apis/g1"), asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/e1"), asyARN("apis/g1/types/Mutation/resolvers/setX")},
		},
		{
			name:     "api keys",
			over:     failG1("ListApiKeys", asyBadReq, func(in any) string { return sv(in.(*appsync.ListApiKeysInput).ApiId) }),
			code:     "BadRequestException",
			lost:     []string{asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/g1/apikeys/da2-key2")},
			wantKept: []string{asyARN("apis/g1/datasources/ds1"), asyARN("apis/g1/types/Query/resolvers/getX")},
		},
		{
			name:     "api cache",
			over:     failG1("GetApiCache", asyConflict, func(in any) string { return sv(in.(*appsync.GetApiCacheInput).ApiId) }),
			code:     "ConcurrentModificationException",
			lost:     []string{asyARN("apis/g1/apicache/_")},
			wantKept: []string{asyARN("apis/g1/datasources/ds1"), asyARN("apis/g1/types/Query/resolvers/getX")},
		},
		{
			name: "types page two",
			over: map[string]func(any) (any, error){"ListTypes": func(in any) (any, error) {
				if in.(*appsync.ListTypesInput).NextToken != nil {
					return nil, asyBadReq
				}
				return asyHandlers(nil)["ListTypes"](in)
			}},
			code:     "BadRequestException",
			lost:     []string{asyARN("apis/g1/types/Mutation/resolvers/setX")},
			wantKept: []string{asyARN("apis/g1/types/Query/resolvers/getX")},
		},
		{
			name: "resolvers of one type",
			over: map[string]func(any) (any, error){"ListResolvers": func(in any) (any, error) {
				if sv(in.(*appsync.ListResolversInput).TypeName) == "Query" {
					return nil, asyBadReq
				}
				return asyHandlers(nil)["ListResolvers"](in)
			}},
			code:     "BadRequestException",
			lost:     []string{asyARN("apis/g1/types/Query/resolvers/getX")},
			wantKept: []string{asyARN("apis/g1/types/Mutation/resolvers/setX")},
		},
		{
			name:     "domain names",
			over:     map[string]func(any) (any, error){"ListDomainNames": func(any) (any, error) { return nil, asyBadReq }},
			code:     "BadRequestException",
			lost:     []string{asyARN("domainnames/api.example.com"), asyARN("domainnames/two.example.com"), asyARN("domainnames/api.example.com/apiassociation")},
			wantKept: []string{asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/e1/channelNamespace/ns1"), asyARN("apis/g1/types/Query/resolvers/getX")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			total, err := scanAppSyncTest(t, st, tc.over)
			if classifyServiceError(err) != outcomeError || !isAPIErrorCode(err, tc.code) {
				t.Fatalf("err = %v; want a hard %s", err, tc.code)
			}
			got := asyStored(t, st)
			for _, id := range tc.wantKept {
				if _, ok := got[id]; !ok {
					t.Errorf("%s not stored", id)
				}
			}
			for _, id := range tc.lost {
				if _, ok := got[id]; ok {
					t.Errorf("%s stored; want it lost with the failed call", id)
				}
			}
			if total != len(got) || total != len(asyAllRows)-len(tc.lost) {
				t.Errorf("total = %d, stored %d; want %d", total, len(got), len(asyAllRows)-len(tc.lost))
			}
		})
	}
}

// A denied op warns once for every parent it was denied on, whatever their
// number, and the scan goes on without an error. Each op denies with the code
// its model gives: UnauthorizedException, or AccessDeniedException for
// ListDomainNames and GetApiAssociation.
func TestScanAppSyncAll_DenialWarnsOncePerOp(t *testing.T) {
	g1Kids := []string{asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/g1/apicache/_"), asyARN("apis/g1/datasources/ds1")}
	resolvers := []string{asyARN("apis/g1/types/Query/resolvers/getX"), asyARN("apis/g1/types/Mutation/resolvers/setX")}
	for _, tc := range []struct {
		sdkOp, op string
		deny      error
		lost      []string
	}{
		{"ListGraphqlApis", appsyncGraphqlApisOp, asyUnauth, append(g1Kids, asyARN("apis/g1"))},
		{"ListApis", appsyncEventApisOp, asyUnauth, []string{asyARN("apis/e1"), asyARN("apis/e1/channelNamespace/ns1")}},
		{"ListDomainNames", appsyncDomainNamesOp, asyDenied, []string{asyARN("domainnames/api.example.com"), asyARN("domainnames/api.example.com/apiassociation")}},
		{"ListApiKeys", appsyncAPIKeysOp, asyUnauth, []string{asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/g1/apikeys/da2-key2")}},
		{"GetApiCache", appsyncAPICacheOp, asyUnauth, []string{asyARN("apis/g1/apicache/_")}},
		{"ListDataSources", appsyncDataSourcesOp, asyUnauth, []string{asyARN("apis/g1/datasources/ds1")}},
		{"ListFunctions", appsyncFunctionsOp, asyUnauth, []string{asyARN("apis/g1/functions/fn1")}},
		{"ListSourceApiAssociations", appsyncSourceAssocsOp, asyUnauth, []string{asyARN("mergedApis/g1/sourceApiAssociations/a1")}},
		{"ListChannelNamespaces", appsyncChannelNamespacesOp, asyUnauth, []string{asyARN("apis/e1/channelNamespace/ns1"), asyARN("apis/e1/channelNamespace/ns2")}},
		{"GetApiAssociation", appsyncAPIAssociationOp, asyDenied, []string{asyARN("domainnames/api.example.com/apiassociation")}},
		{"ListTypes", appsyncTypesOp, asyUnauth, resolvers},
		{"ListResolvers", appsyncResolversOp, asyUnauth, resolvers},
	} {
		t.Run(tc.sdkOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := asyOpWarnings(st)
			_, err := scanAppSyncTest(t, st, map[string]func(any) (any, error){tc.sdkOp: func(any) (any, error) { return nil, tc.deny }})
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if n := warns(tc.op); n != 1 {
				t.Errorf("%s warnings = %d; want 1", tc.op, n)
			}
			got := asyStored(t, st)
			for _, id := range tc.lost {
				if _, ok := got[id]; ok {
					t.Errorf("%s stored after its op was denied", id)
				}
			}
			if _, ok := got[asyARN("domainnames/two.example.com")]; !ok && tc.sdkOp != "ListDomainNames" {
				t.Errorf("domain name d2 not stored; a denial must not stop the other phases")
			}
		})
	}
}

// Every per-parent op skips a parent it does not find without a warning or
// an error, keeping its siblings' rows.
func TestScanAppSyncAll_NotFoundParentSkippedSilently(t *testing.T) {
	notFoundOn := func(sdkOp string, key func(any) string, parent string) map[string]func(any) (any, error) {
		base := asyHandlers(nil)[sdkOp]
		return map[string]func(any) (any, error){sdkOp: func(in any) (any, error) {
			if key(in) == parent {
				return nil, asyNotFound
			}
			return base(in)
		}}
	}
	for _, tc := range []struct {
		name, op string
		over     map[string]func(any) (any, error)
		lost     []string
	}{
		{
			"api keys", appsyncAPIKeysOp, notFoundOn("ListApiKeys", func(in any) string { return sv(in.(*appsync.ListApiKeysInput).ApiId) }, "g1"),
			[]string{asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/g1/apikeys/da2-key2")},
		},
		{
			"channel namespaces", appsyncChannelNamespacesOp, notFoundOn("ListChannelNamespaces", func(in any) string { return sv(in.(*appsync.ListChannelNamespacesInput).ApiId) }, "e1"),
			[]string{asyARN("apis/e1/channelNamespace/ns1"), asyARN("apis/e1/channelNamespace/ns2")},
		},
		{
			"types", appsyncTypesOp, notFoundOn("ListTypes", func(in any) string { return sv(in.(*appsync.ListTypesInput).ApiId) }, "g1"),
			[]string{asyARN("apis/g1/types/Query/resolvers/getX"), asyARN("apis/g1/types/Mutation/resolvers/setX")},
		},
		{
			"api cache absent from the response", appsyncAPICacheOp,
			map[string]func(any) (any, error){"GetApiCache": func(any) (any, error) { return &appsync.GetApiCacheOutput{}, nil }},
			[]string{asyARN("apis/g1/apicache/_")},
		},
		{
			"resolvers", appsyncResolversOp, notFoundOn("ListResolvers", func(in any) string { return sv(in.(*appsync.ListResolversInput).TypeName) }, "Query"),
			[]string{asyARN("apis/g1/types/Query/resolvers/getX")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := asyOpWarnings(st)
			total, err := scanAppSyncTest(t, st, tc.over)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if n := warns(tc.op); n != 0 {
				t.Errorf("%s warnings = %d; want 0", tc.op, n)
			}
			if total != len(asyAllRows)-len(tc.lost) {
				t.Errorf("total = %d; want %d", total, len(asyAllRows)-len(tc.lost))
			}
		})
	}
}

// A throttled phase beside a hard failure in another is demoted to one
// warning, so the hard error reaches the dispatcher as one; a throttle alone
// is returned, so the scan reads as partial, with every other row stored.
func TestScanAppSyncAll_TransientPhase(t *testing.T) {
	throttle := map[string]func(any) (any, error){"ListApis": func(any) (any, error) { return nil, asyThrottled }}
	t.Run("beside a hard error", func(t *testing.T) {
		st := newTestStore(t)
		warns := asyOpWarnings(st)
		over := map[string]func(any) (any, error){
			"ListDomainNames": func(any) (any, error) { return nil, asyBadReq },
			"ListApis":        throttle["ListApis"],
		}
		_, err := scanAppSyncTest(t, st, over)
		if classifyServiceError(err) != outcomeError || !isAPIErrorCode(err, "BadRequestException") {
			t.Errorf("err = %v; want only the hard BadRequestException", err)
		}
		if n := warns(appsyncEventApisOp); n != 1 {
			t.Errorf("%s warnings = %d; want 1", appsyncEventApisOp, n)
		}
	})
	t.Run("alone", func(t *testing.T) {
		st := newTestStore(t)
		total, err := scanAppSyncTest(t, st, throttle)
		if classifyServiceError(err) != outcomeTransient {
			t.Errorf("err = %v; want outcomeTransient", err)
		}
		// The two Event APIs and their two channel namespaces are lost.
		if total != len(asyAllRows)-4 {
			t.Errorf("total = %d; want %d", total, len(asyAllRows)-4)
		}
	})
}

// asyFailOnPage2 serves sdkOp's first page from the fixture's handler and
// fails a request carrying a NextToken with err.
func asyFailOnPage2(sdkOp string, token func(any) *string, err error) map[string]func(any) (any, error) {
	base := asyHandlers(nil)[sdkOp]
	return map[string]func(any) (any, error){sdkOp: func(in any) (any, error) {
		if token(in) != nil {
			return nil, err
		}
		return base(in)
	}}
}

// A parent listing denied after its first page keeps the APIs read before
// the denial and lists their children; the denial is one warning, not an
// error.
func TestScanAppSyncAll_ParentPageDeniedKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		over     map[string]func(any) (any, error)
		kept     []string
		lost     []string
	}{
		{
			name: "graphql apis", op: appsyncGraphqlApisOp,
			over: asyFailOnPage2("ListGraphqlApis", func(in any) *string { return in.(*appsync.ListGraphqlApisInput).NextToken }, asyUnauth),
			kept: []string{asyARN("apis/g1"), asyARN("apis/g1/apikeys/da2-key1"), asyARN("apis/g1/types/Mutation/resolvers/setX")},
			lost: []string{asyARN("apis/g2"), asyARN("apis/noid")},
		},
		{
			name: "event apis", op: appsyncEventApisOp,
			over: map[string]func(any) (any, error){"ListApis": func(in any) (any, error) {
				if in.(*appsync.ListApisInput).NextToken != nil {
					return nil, asyUnauth
				}
				return &appsync.ListApisOutput{Apis: []appsynctypes.Api{asyE1}, NextToken: ptrStr("p2")}, nil
			}},
			kept: []string{asyARN("apis/e1"), asyARN("apis/e1/channelNamespace/ns1")},
			lost: []string{asyARN("apis/e2")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var msgs []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == tc.op {
					msgs = append(msgs, w.Message)
				}
			}
			if _, err := scanAppSyncTest(t, st, tc.over); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			got := asyStored(t, st)
			for _, id := range tc.kept {
				if _, ok := got[id]; !ok {
					t.Errorf("%s not stored; the pages before the denial and their children must be", id)
				}
			}
			for _, id := range tc.lost {
				if _, ok := got[id]; ok {
					t.Errorf("%s stored; it is on the denied page", id)
				}
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0], "UnauthorizedException") {
				t.Errorf("%s warnings = %q; want one carrying the UnauthorizedException", tc.op, msgs)
			}
		})
	}
}

// The types a ListTypes listing read before it was denied or before the API
// vanished still have their resolvers listed; each cause is warned about
// once, and neither is an error.
func TestScanAppSyncAll_TypesReadBeforeFailureKept(t *testing.T) {
	token := func(in any) *string { return in.(*appsync.ListTypesInput).NextToken }
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"denied", asyUnauth},
		{"vanished", asyNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := asyOpWarnings(st)
			_, err := scanAppSyncTest(t, st, asyFailOnPage2("ListTypes", token, tc.err))
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			got := asyStored(t, st)
			if _, ok := got[asyARN("apis/g1/types/Query/resolvers/getX")]; !ok {
				t.Errorf("resolver of the type read before the failure not stored")
			}
			if _, ok := got[asyARN("apis/g1/types/Mutation/resolvers/setX")]; ok {
				t.Errorf("resolver of a type never read stored")
			}
			if n := warns(appsyncTypesOp); n != 1 {
				t.Errorf("%s warnings = %d; want 1", appsyncTypesOp, n)
			}
		})
	}
}

// A refused source association listing keeps the associations read before
// the refusal, warns once naming the first API in parent order whichever
// call failed first, and is no error.
func TestScanAppSyncAll_SourceAssocRefusal(t *testing.T) {
	st := newTestStore(t)
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == appsyncSourceAssocsOp {
			msgs = append(msgs, w.Message)
		}
	}
	over := map[string]func(any) (any, error){"ListSourceApiAssociations": func(in any) (any, error) {
		p := in.(*appsync.ListSourceApiAssociationsInput)
		if sv(p.ApiId) == "g1" && p.NextToken == nil {
			return &appsync.ListSourceApiAssociationsOutput{SourceApiAssociationSummaries: []appsynctypes.SourceApiAssociationSummary{asyAssoc}, NextToken: ptrStr("p2")}, nil
		}
		return nil, asyBadReq
	}}
	if _, err := scanAppSyncTest(t, st, over); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if _, ok := asyStored(t, st)[asyARN("mergedApis/g1/sourceApiAssociations/a1")]; !ok {
		t.Errorf("association read before the refusal not stored")
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "2 parent(s)") || !strings.Contains(msgs[0], "first g1:") {
		t.Errorf("warnings = %q; want one for both APIs naming g1 first", msgs)
	}
}

// Once ctx ends, the ListTypes reads not started are reported as an error.
func TestScanASCResolvers_ContextStopReturned(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := appsync.NewFromConfig(opStubConfig(asyHandlers(nil), asyDenied))
	apis := []childParent{{id: "g1", arn: asyARN("apis/g1")}}
	_, _, err := scanASCResolvers(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, apis)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}

// asyRefuseWrites makes every resource write of type typ fail. The abort is
// a constraint error, which the store does not retry.
func asyRefuseWrites(t *testing.T, st *store.Store, typ string) {
	t.Helper()
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_type BEFORE INSERT ON resources
		WHEN NEW.type = '` + typ + `'
		BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
}

// A failed store write stops the scan: no later phase runs, and the parents
// whose write failed are not handed to a child phase.
func TestScanAppSyncAll_StoreWriteStops(t *testing.T) {
	st := newTestStore(t)
	asyRefuseWrites(t, st, TypeAppSyncGraphQLApi)
	_, err := scanAppSyncTest(t, st, nil)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if n := len(asyStored(t, st)); n != 0 {
		t.Errorf("stored %d rows; want none after the first phase's write failed", n)
	}
	client := appsync.NewFromConfig(opStubConfig(asyHandlers(nil), asyDenied))
	_, _, parents, err := scanASCGraphqlApis(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || parents != nil {
		t.Errorf("parents = %v, err = %v; want none and the store write failure", parents, err)
	}
}

// A failed write of a phase's rows is returned with no warning: none may
// claim rows the write lost.
func TestScanAppSync_StoreWriteFailureWarnsNothing(t *testing.T) {
	apis := []childParent{{id: "g1", arn: asyARN("apis/g1")}, {id: "g2", arn: asyARN("apis/g2")}}
	for _, tc := range []struct {
		name, typ string
		over      map[string]func(any) (any, error)
		run       func(client appSyncAPI, st *store.Store) error
	}{
		{
			name: "graphql apis", typ: TypeAppSyncGraphQLApi,
			over: map[string]func(any) (any, error){"ListGraphqlApis": func(in any) (any, error) {
				if in.(*appsync.ListGraphqlApisInput).NextToken != nil {
					return nil, asyUnauth
				}
				return asyHandlers(nil)["ListGraphqlApis"](in)
			}},
			run: func(client appSyncAPI, st *store.Store) error {
				_, _, _, err := scanASCGraphqlApis(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "source associations", typ: TypeAppSyncSourceAPIAssociation,
			run: func(client appSyncAPI, st *store.Store) error {
				_, _, err := scanASCSourceAPIAssocs(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, apis)
				return err
			},
		},
		{
			name: "resolvers", typ: TypeAppSyncResolver,
			over: map[string]func(any) (any, error){"ListTypes": func(in any) (any, error) {
				if sv(in.(*appsync.ListTypesInput).ApiId) == "g2" {
					return nil, asyUnauth
				}
				return asyHandlers(nil)["ListTypes"](in)
			}},
			run: func(client appSyncAPI, st *store.Store) error {
				_, _, err := scanASCResolvers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, apis)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			n := 0
			st.OnWarn = func(store.ScanWarning) { n++ }
			asyRefuseWrites(t, st, tc.typ)
			client := appsync.NewFromConfig(opStubConfig(asyHandlers(tc.over), asyDenied))
			if err := tc.run(client, st); !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v; want the store write failure", err)
			}
			if n != 0 {
				t.Errorf("warnings = %d; want none after a failed write", n)
			}
		})
	}
}

// A key's NativeID is built from its API's ARN, so it keeps that ARN's
// partition.
func TestScanASCApiKeys_KeepsParentPartition(t *testing.T) {
	st := newTestStore(t)
	apiARN := "arn:aws-cn:appsync:cn-north-1:" + testAccountID + ":apis/g1"
	client := appsync.NewFromConfig(opStubConfig(asyHandlers(nil), asyDenied))
	if _, _, err := scanASCApiKeys(context.Background(), client, newTestAccount(testAccountID), "cn-north-1", st, testScanID, []childParent{{id: "g1", arn: apiARN}}); err != nil {
		t.Fatalf("scanASCApiKeys: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeAppSyncAPIKey)[apiARN+"/apikeys/da2-key1"]; !ok {
		t.Errorf("key rows = %v; want %s", nativeIDs(rowsByNativeID(t, st, TypeAppSyncAPIKey)), apiARN+"/apikeys/da2-key1")
	}
}

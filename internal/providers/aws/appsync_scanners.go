package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/appsync"
	appsynctypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appsync",
		fn:   scanAppSync,
		emits: []coverage.TypeDecl{
			{Service: "appsync", DiscoType: TypeAppSyncApi},
			{Service: "appsync", DiscoType: TypeAppSyncApiCache},
			{Service: "appsync", DiscoType: TypeAppSyncApiKey},
			{Service: "appsync", DiscoType: TypeAppSyncChannelNamespace},
			{Service: "appsync", DiscoType: TypeAppSyncDataSource},
			{Service: "appsync", DiscoType: TypeAppSyncDomainName},
			{Service: "appsync", DiscoType: TypeAppSyncDomainNameApiAssociation},
			{Service: "appsync", DiscoType: TypeAppSyncFunctionConfiguration},
			{Service: "appsync", DiscoType: TypeAppSyncGraphQLApi},
			{Service: "appsync", DiscoType: TypeAppSyncGraphQLSchema},
			{Service: "appsync", DiscoType: TypeAppSyncSourceApiAssociation},
			{Service: "appsync", DiscoType: TypeAppSyncResolver},
		},
	})
}

type appSyncAPI interface {
	ListApis(context.Context, *appsync.ListApisInput, ...func(*appsync.Options)) (*appsync.ListApisOutput, error)
	ListGraphqlApis(context.Context, *appsync.ListGraphqlApisInput, ...func(*appsync.Options)) (*appsync.ListGraphqlApisOutput, error)
	ListApiKeys(context.Context, *appsync.ListApiKeysInput, ...func(*appsync.Options)) (*appsync.ListApiKeysOutput, error)
	ListChannelNamespaces(context.Context, *appsync.ListChannelNamespacesInput, ...func(*appsync.Options)) (*appsync.ListChannelNamespacesOutput, error)
	ListDataSources(context.Context, *appsync.ListDataSourcesInput, ...func(*appsync.Options)) (*appsync.ListDataSourcesOutput, error)
	ListDomainNames(context.Context, *appsync.ListDomainNamesInput, ...func(*appsync.Options)) (*appsync.ListDomainNamesOutput, error)
	ListFunctions(context.Context, *appsync.ListFunctionsInput, ...func(*appsync.Options)) (*appsync.ListFunctionsOutput, error)
	ListSourceApiAssociations(context.Context, *appsync.ListSourceApiAssociationsInput, ...func(*appsync.Options)) (*appsync.ListSourceApiAssociationsOutput, error)
	GetApiCache(context.Context, *appsync.GetApiCacheInput, ...func(*appsync.Options)) (*appsync.GetApiCacheOutput, error)
	GetApiAssociation(context.Context, *appsync.GetApiAssociationInput, ...func(*appsync.Options)) (*appsync.GetApiAssociationOutput, error)
	ListTypes(context.Context, *appsync.ListTypesInput, ...func(*appsync.Options)) (*appsync.ListTypesOutput, error)
	ListResolvers(context.Context, *appsync.ListResolversInput, ...func(*appsync.Options)) (*appsync.ListResolversOutput, error)
}

// asyncSplitApis routes API IDs to GraphQL vs Event API buckets based
// on which list-op returned them.
type asyncSplitApis struct {
	graphqlIDs []string
	eventIDs   []string
}

func scanAppSync(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := appsync.NewFromConfig(acct.cfg, func(o *appsync.Options) { o.Region = region })

	apis := &asyncSplitApis{}
	t, i, ferr := scanASCGraphqlApis(ctx, client, acct, region, st, scanID, apis)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanASCEventApis(ctx, client, acct, region, st, scanID, apis)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	domains, t, i, ferr := scanASCDomainNames(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanASCApiKeys(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCApiCaches(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCDataSources(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCFunctions(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCSchemas(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCSourceAPIAssocs(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
		func() (int, int, error) {
			return scanASCChannelNamespaces(ctx, client, acct, region, st, scanID, apis.eventIDs)
		},
		func() (int, int, error) {
			return scanASCDomainNameAssocs(ctx, client, acct, region, st, scanID, domains)
		},
		func() (int, int, error) {
			return scanASCResolvers(ctx, client, acct, region, st, scanID, apis.graphqlIDs)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanASCGraphqlApis(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis *asyncSplitApis) (int, int, error) {
	pager := appsync.NewListGraphqlApisPaginator(client, &appsync.ListGraphqlApisInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appsync:ListGraphqlApis", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appsync:ListGraphqlApis: %w", perr)
		}
		for _, a := range out.GraphqlApis {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			id := sv(a.ApiId)
			if id != "" {
				apis.graphqlIDs = append(apis.graphqlIDs, id)
			}
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppSyncGraphQLApi, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appsync graphql-apis")
}

func scanASCEventApis(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis *asyncSplitApis) (int, int, error) {
	pager := appsync.NewListApisPaginator(client, &appsync.ListApisInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appsync:ListApis", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appsync:ListApis: %w", perr)
		}
		for _, a := range out.Apis {
			arn := sv(a.ApiArn)
			if arn == "" {
				continue
			}
			id := sv(a.ApiId)
			if id != "" {
				apis.eventIDs = append(apis.eventIDs, id)
			}
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppSyncApi, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appsync event-apis")
}

func appsyncApiARN(region, acct, apiID, kind, id string) string {
	return fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s/%s/%s", region, acct, apiID, kind, id)
}

func scanASCApiKeys(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		id := aid
		pager := appsync.NewListApiKeysPaginator(client, &appsync.ListApiKeysInput{ApiId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListApiKeys %s: %w", aid, perr)
			}
			for _, k := range out.ApiKeys {
				kid := sv(k.Id)
				if kid == "" {
					continue
				}
				arn := appsyncApiARN(region, acct.ID, aid, "apikeys", kid)
				label := kid
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppSyncApiKey, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "appsync api-keys")
}

// scanASCApiCaches — per GraphQL API singleton. NotFoundException when
// no cache configured.
func scanASCApiCaches(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		id := aid
		out, err := client.GetApiCache(ctx, &appsync.GetApiCacheInput{ApiId: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("appsync:GetApiCache %s: %w", aid, err)
		}
		if out.ApiCache == nil {
			continue
		}
		arn := appsyncApiARN(region, acct.ID, aid, "apicache", "_")
		name := "api-cache"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeAppSyncApiCache, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.ApiCache), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "appsync api-caches")
}

func scanASCDataSources(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		id := aid
		pager := appsync.NewListDataSourcesPaginator(client, &appsync.ListDataSourcesInput{ApiId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListDataSources %s: %w", aid, perr)
			}
			for _, d := range out.DataSources {
				arn := sv(d.DataSourceArn)
				if arn == "" {
					continue
				}
				label := sv(d.Name)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppSyncDataSource, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "appsync data-sources")
}

func scanASCFunctions(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		id := aid
		pager := appsync.NewListFunctionsPaginator(client, &appsync.ListFunctionsInput{ApiId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListFunctions %s: %w", aid, perr)
			}
			for _, f := range out.Functions {
				arn := sv(f.FunctionArn)
				if arn == "" {
					continue
				}
				label := sv(f.Name)
				if label == "" {
					label = sv(f.FunctionId)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppSyncFunctionConfiguration, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "appsync function-configurations")
}

// scanASCSchemas — per GraphQL API singleton, synth ARN. No SDK list op
// for schemas; emit one row per known API.
func scanASCSchemas(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		arn := appsyncApiARN(region, acct.ID, aid, "schema", "_")
		name := "graphql-schema"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeAppSyncGraphQLSchema, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(map[string]string{"ApiId": aid}), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "appsync graphql-schemas")
}

func scanASCSourceAPIAssocs(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range apiIDs {
		id := aid
		pager := appsync.NewListSourceApiAssociationsPaginator(client, &appsync.ListSourceApiAssociationsInput{ApiId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) || isAPIErrorCode(perr, "BadRequestException") {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListSourceApiAssociations %s: %w", aid, perr)
			}
			for _, s := range out.SourceApiAssociationSummaries {
				arn := sv(s.AssociationArn)
				if arn == "" {
					continue
				}
				label := sv(s.AssociationId)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppSyncSourceApiAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "appsync source-api-associations")
}

func scanASCChannelNamespaces(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, eventApiIDs []string) (int, int, error) {
	if len(eventApiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, aid := range eventApiIDs {
		id := aid
		pager := appsync.NewListChannelNamespacesPaginator(client, &appsync.ListChannelNamespacesInput{ApiId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListChannelNamespaces %s: %w", aid, perr)
			}
			for _, c := range out.ChannelNamespaces {
				arn := sv(c.ChannelNamespaceArn)
				if arn == "" {
					continue
				}
				label := sv(c.Name)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppSyncChannelNamespace, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "appsync channel-namespaces")
}

func scanASCDomainNames(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := appsync.NewListDomainNamesPaginator(client, &appsync.ListDomainNamesInput{})
	var domains []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appsync:ListDomainNames", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appsync:ListDomainNames: %w", perr)
		}
		for _, d := range out.DomainNameConfigs {
			arn := sv(d.DomainNameArn)
			if arn == "" {
				continue
			}
			dn := sv(d.DomainName)
			if dn != "" {
				domains = append(domains, dn)
			}
			label := dn
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppSyncDomainName, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appsync domain-names")
	return domains, t, i, err
}

func scanASCDomainNameAssocs(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	if len(domains) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, dn := range domains {
		d := dn
		out, err := client.GetApiAssociation(ctx, &appsync.GetApiAssociationInput{DomainName: &d})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("appsync:GetApiAssociation %s: %w", dn, err)
		}
		if out.ApiAssociation == nil {
			continue
		}
		arn := fmt.Sprintf("arn:aws:appsync:%s:%s:domainnames/%s/apiassociation", region, acct.ID, dn)
		label := dn
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeAppSyncDomainNameApiAssociation, NativeID: arn,
			Name: &label, Region: &region, AttributesJSON: mustJSON(out.ApiAssociation), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "appsync domain-name-api-associations")
}

// scanASCResolvers fans out per (api, type) to ListResolvers. Type names come
// from ListTypes(Format=SDL); a GraphQL schema with N user-defined types
// triggers N ListResolvers calls per API. Per-(api, type) errors tolerate
// AccessDenied + NotFoundException without aborting siblings.
func scanASCResolvers(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apiIDs []string) (int, int, error) {
	if len(apiIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, apiID := range apiIDs {
		// First enumerate type names via ListTypes(Format=SDL).
		var typeNames []string
		var typesToken *string
		for {
			tout, terr := client.ListTypes(ctx, &appsync.ListTypesInput{
				ApiId:     &apiID,
				Format:    appsynctypes.TypeDefinitionFormatSdl,
				NextToken: typesToken,
			})
			if terr != nil {
				if isAccessDenied(terr) {
					_ = skipIfAccessDenied(st, "appsync:ListTypes", acct.ID, region, terr)
					break
				}
				if isAPIErrorCode(terr, "NotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("appsync:ListTypes %s: %w", apiID, terr)
			}
			for _, t := range tout.Types {
				if name := sv(t.Name); name != "" {
					typeNames = append(typeNames, name)
				}
			}
			if tout.NextToken == nil || *tout.NextToken == "" {
				break
			}
			typesToken = tout.NextToken
		}

		// Per type, paginate resolvers.
		for _, typeName := range typeNames {
			var resToken *string
			for {
				rout, rerr := client.ListResolvers(ctx, &appsync.ListResolversInput{
					ApiId:     &apiID,
					TypeName:  &typeName,
					NextToken: resToken,
				})
				if rerr != nil {
					if isAccessDenied(rerr) || isAPIErrorCode(rerr, "NotFoundException") {
						break
					}
					return 0, 0, fmt.Errorf("appsync:ListResolvers %s/%s: %w", apiID, typeName, rerr)
				}
				for _, r := range rout.Resolvers {
					arn := sv(r.ResolverArn)
					if arn == "" {
						continue
					}
					label := sv(r.FieldName)
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAppSyncResolver, NativeID: arn,
						Name: &label, Region: &region,
						AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
					})
				}
				if rout.NextToken == nil || *rout.NextToken == "" {
					break
				}
				resToken = rout.NextToken
			}
		}
	}
	return upsertBatch(st, batch, "appsync resolvers")
}

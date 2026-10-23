package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/appsync"
	appsynctypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppSyncAPI, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncAPICache, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncAPIKey, Service: "appsync", Redact: []redact.Rule{{Path: "Id", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAppSyncChannelNamespace, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncDataSource, Service: "appsync", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeAppSyncDomainName, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncDomainNameAPIAssociation, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncFunctionConfiguration, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncGraphQLApi, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncGraphQLSchema, Service: "appsync"})
	registerType(restype.Descriptor{Type: TypeAppSyncSourceAPIAssociation, Service: "appsync", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeAppSyncResolver, Service: "appsync"})
	registerService(serviceEntry{
		name: "aws:appsync",
		fn:   scanAppSync,
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

// The ops of scanAppSync's phases, shared by each phase and the scanAppSync
// entry that warns under it if its error is demoted.
const (
	appsyncGraphqlApisOp       = "appsync:ListGraphqlApis"
	appsyncEventApisOp         = "appsync:ListApis"
	appsyncDomainNamesOp       = "appsync:ListDomainNames"
	appsyncAPIKeysOp           = "appsync:ListApiKeys"
	appsyncAPICacheOp          = "appsync:GetApiCache"
	appsyncDataSourcesOp       = "appsync:ListDataSources"
	appsyncFunctionsOp         = "appsync:ListFunctions"
	appsyncSchemasOp           = "appsync graphql-schemas"
	appsyncSourceAssocsOp      = "appsync:ListSourceApiAssociations"
	appsyncChannelNamespacesOp = "appsync:ListChannelNamespaces"
	appsyncAPIAssociationOp    = "appsync:GetApiAssociation"
	appsyncTypesOp             = "appsync:ListTypes"
	appsyncResolversOp         = "appsync:ListResolvers"
)

// appsyncPageMax is the MaxResults cap the Smithy model sets on every AppSync
// list op this file calls.
const appsyncPageMax = 25

// appsyncNotFound is the code every per-parent op this file calls models for
// a parent it does not find.
var appsyncNotFound = []string{"NotFoundException"}

// appsyncPhase is one phase of scanAppSync and the op it is warned under.
type appsyncPhase struct {
	op  string
	run func() (int, int, error)
}

func scanAppSync(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appsync.NewFromConfig(acct.cfg, func(o *appsync.Options) { o.Region = region })
	return scanAppSyncAll(ctx, client, acct, region, st, scanID)
}

// scanAppSyncAll discovers GraphQL APIs, Event APIs and custom domain names,
// then the children of each: API keys, caches, data sources, functions,
// schemas, source API associations and resolvers of the GraphQL APIs, the
// channel namespaces of the Event APIs and the API association of each
// domain name.
//
// Phases run in order. A phase's error does not stop the later phases; only a
// failed store write does, since every later write would fail too. A child
// phase reads the parents its parent phase stored, so a failed parent phase
// leaves it fewer parents.
func scanAppSyncAll(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var graphqlAPIs, eventAPIs, domains []childParent
	phases := []appsyncPhase{
		{appsyncGraphqlApisOp, func() (t, i int, err error) {
			t, i, graphqlAPIs, err = scanASCGraphqlApis(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{appsyncEventApisOp, func() (t, i int, err error) {
			t, i, eventAPIs, err = scanASCEventApis(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{appsyncDomainNamesOp, func() (t, i int, err error) {
			t, i, domains, err = scanASCDomainNames(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{appsyncAPIKeysOp, func() (int, int, error) {
			return scanASCApiKeys(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
		{appsyncAPICacheOp, func() (int, int, error) {
			return scanASCApiCaches(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
		{appsyncDataSourcesOp, func() (int, int, error) {
			return scanASCDataSources(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
		{appsyncFunctionsOp, func() (int, int, error) {
			return scanASCFunctions(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
		{appsyncSchemasOp, func() (int, int, error) {
			return scanASCSchemas(st, acct, region, scanID, graphqlAPIs)
		}},
		{appsyncSourceAssocsOp, func() (int, int, error) {
			return scanASCSourceAPIAssocs(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
		{appsyncChannelNamespacesOp, func() (int, int, error) {
			return scanASCChannelNamespaces(ctx, client, acct, region, st, scanID, eventAPIs)
		}},
		{appsyncAPIAssociationOp, func() (int, int, error) {
			return scanASCDomainNameAssocs(ctx, client, acct, region, st, scanID, domains)
		}},
		{appsyncResolversOp, func() (int, int, error) {
			return scanASCResolvers(ctx, client, acct, region, st, scanID, graphqlAPIs)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// appsyncRow builds the row of an AppSync resource.
func appsyncRow(acct *account, region, scanID, rtype, nativeID, name string, attrs any, tags map[string]string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}
}

// isAppSyncUnauthorized reports whether err is the UnauthorizedException ten
// of the ops this file calls model for a denial. accessDeniedCodes leaves the
// code out because other services give it other meanings, so the shared
// helpers do not treat it as one; the AccessDeniedException ListDomainNames
// and GetApiAssociation model instead is handled by them as usual.
func isAppSyncUnauthorized(err error) bool {
	return isAPIErrorCode(err, "UnauthorizedException")
}

// storeAppSyncParents is storeParentListDeniedBy with AppSync's denial code,
// UnauthorizedException.
func storeAppSyncParents[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, idField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	key func(Item) string,
) (int, int, []childParent, error) {
	return storeParentListDeniedBy(ctx, st, acct, region, op, what, idField, isAppSyncUnauthorized, page, row, key)
}

// scanASCGraphqlApis also returns the stored GraphQL APIs for the child phases.
func scanASCGraphqlApis(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeAppSyncParents(ctx, st, acct, region, appsyncGraphqlApisOp, "GraphQL APIs", "ApiId",
		func(ctx context.Context, token *string) ([]appsynctypes.GraphqlApi, *string, error) {
			out, err := client.ListGraphqlApis(ctx, &appsync.ListGraphqlApisInput{NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.GraphqlApis, out.NextToken, nil
		},
		func(a appsynctypes.GraphqlApi) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "GraphQL APIs have an empty Arn", sv(a.ApiId)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncGraphQLApi, *a.Arn, appsyncLabel(a.Name, a.ApiId), a, a.Tags), "", ""
		},
		func(a appsynctypes.GraphqlApi) string { return sv(a.ApiId) })
}

// scanASCEventApis also returns the stored Event APIs for the channel
// namespace phase.
func scanASCEventApis(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeAppSyncParents(ctx, st, acct, region, appsyncEventApisOp, "Event APIs", "ApiId",
		func(ctx context.Context, token *string) ([]appsynctypes.Api, *string, error) {
			out, err := client.ListApis(ctx, &appsync.ListApisInput{NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Apis, out.NextToken, nil
		},
		func(a appsynctypes.Api) (*store.Resource, string, string) {
			if sv(a.ApiArn) == "" {
				return nil, "Event APIs have an empty ApiArn", sv(a.ApiId)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncAPI, *a.ApiArn, appsyncLabel(a.Name, a.ApiId), a, a.Tags), "", ""
		},
		func(a appsynctypes.Api) string { return sv(a.ApiId) })
}

// scanASCDomainNames also returns the stored domain names for the API
// association phase.
func scanASCDomainNames(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeAppSyncParents(ctx, st, acct, region, appsyncDomainNamesOp, "domain names", "DomainName",
		func(ctx context.Context, token *string) ([]appsynctypes.DomainNameConfig, *string, error) {
			out, err := client.ListDomainNames(ctx, &appsync.ListDomainNamesInput{NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.DomainNameConfigs, out.NextToken, nil
		},
		func(d appsynctypes.DomainNameConfig) (*store.Resource, string, string) {
			if sv(d.DomainNameArn) == "" {
				return nil, "domain names have an empty DomainNameArn", sv(d.DomainName)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncDomainName, *d.DomainNameArn, appsyncLabel(d.DomainName, d.DomainNameArn), d, d.Tags), "", ""
		},
		func(d appsynctypes.DomainNameConfig) string { return sv(d.DomainName) })
}

// appsyncLabel is name, or fallback when name is empty.
func appsyncLabel(name, fallback *string) string {
	if n := sv(name); n != "" {
		return n
	}
	return sv(fallback)
}

// storeAppSyncChildren reads op's items for each parent concurrently through
// storeTokenEachParentSkip; row builds a child from the parent it was read
// from. A parent the service denies with UnauthorizedException or, when
// refuse is not nil, refuses with refuse.code keeps the rows read before the
// error, and each cause is warned about once, naming the first parent in
// parent order, after the write succeeded; storeTokenEachParentSkip handles
// the codes isAccessDenied matches the same way.
func storeAppSyncChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	page func(ctx context.Context, parentID string, token *string) ([]Item, *string, error),
	row func(parent childParent, it Item) (*store.Resource, string, string),
	refuse *skipCode,
) (int, int, error) {
	byID := make(map[string]childParent, len(parents))
	ids := make([]string, len(parents))
	for i, p := range parents {
		byID[p.id], ids[i] = p, p.id
	}
	var denied, refused skipTally
	total, inserted, err := storeTokenEachParentSkip(ctx, st, acct, region, op, appsyncNotFound, ids, page,
		func(id string, it Item) (*store.Resource, string, string) { return row(byID[id], it) },
		func(parent string, err error) bool {
			switch {
			case isAppSyncUnauthorized(err):
				denied.skipped(parent, err)
			case refuse != nil && isAPIErrorCode(err, refuse.code):
				refused.skipped(parent, err)
			default:
				return false
			}
			return true
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	denied.warnDeniedIn(st, op, acct, region, "parent(s)")
	if refuse != nil {
		refused.warnCode(st, op, acct, region, refuse.code, "parent(s)", refuse.why)
	}
	return total, inserted, err
}

// scanASCApiKeys lists each GraphQL API's keys. A key has no ARN; its
// NativeID is {apiARN}/apikeys/{keyId}. The key id is the key itself, a
// credential: the attributes' Id is redacted and the row is named by its
// Description, not the id. The NativeID still carries the id.
func scanASCApiKeys(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncAPIKeysOp, apis,
		func(ctx context.Context, id string, token *string) ([]appsynctypes.ApiKey, *string, error) {
			out, err := client.ListApiKeys(ctx, &appsync.ListApiKeysInput{ApiId: &id, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.ApiKeys, out.NextToken, nil
		},
		func(api childParent, k appsynctypes.ApiKey) (*store.Resource, string, string) {
			kid := sv(k.Id)
			if kid == "" {
				return nil, "API keys have an empty Id", api.arn
			}
			r := appsyncRow(acct, region, scanID, TypeAppSyncAPIKey, api.arn+"/apikeys/"+kid, "", k, nil)
			r.Name = nonEmptyPtr(sv(k.Description))
			return r, "", ""
		}, nil)
}

// scanASCApiCaches reads each GraphQL API's cache, a per-API singleton whose
// NativeID is {apiARN}/apicache/_. GetApiCache's modeled NotFoundException,
// for an API with no cache or one deleted since it was listed, stores
// nothing for that API. There is no listed row to keep for an API whose read
// fails, so every other failure is returned after the other caches are
// stored, except a denial, which is warned about once.
func scanASCApiCaches(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncAPICacheOp, apis,
		func(ctx context.Context, id string, _ *string) ([]appsynctypes.ApiCache, *string, error) {
			out, err := client.GetApiCache(ctx, &appsync.GetApiCacheInput{ApiId: &id})
			if err != nil || out.ApiCache == nil {
				return nil, nil, err
			}
			return []appsynctypes.ApiCache{*out.ApiCache}, nil, nil
		},
		func(api childParent, c appsynctypes.ApiCache) (*store.Resource, string, string) {
			return appsyncRow(acct, region, scanID, TypeAppSyncAPICache, api.arn+"/apicache/_", "api-cache", c, nil), "", ""
		}, nil)
}

func scanASCDataSources(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncDataSourcesOp, apis,
		func(ctx context.Context, id string, token *string) ([]appsynctypes.DataSource, *string, error) {
			out, err := client.ListDataSources(ctx, &appsync.ListDataSourcesInput{ApiId: &id, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSources, out.NextToken, nil
		},
		func(api childParent, d appsynctypes.DataSource) (*store.Resource, string, string) {
			if sv(d.DataSourceArn) == "" {
				return nil, "data sources have an empty DataSourceArn", api.arn + " " + sv(d.Name)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncDataSource, *d.DataSourceArn, appsyncLabel(d.Name, d.DataSourceArn), d, nil), "", ""
		}, nil)
}

func scanASCFunctions(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncFunctionsOp, apis,
		func(ctx context.Context, id string, token *string) ([]appsynctypes.FunctionConfiguration, *string, error) {
			out, err := client.ListFunctions(ctx, &appsync.ListFunctionsInput{ApiId: &id, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Functions, out.NextToken, nil
		},
		func(api childParent, f appsynctypes.FunctionConfiguration) (*store.Resource, string, string) {
			if sv(f.FunctionArn) == "" {
				return nil, "functions have an empty FunctionArn", api.arn + " " + sv(f.Name)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncFunctionConfiguration, *f.FunctionArn, appsyncLabel(f.Name, f.FunctionId), f, nil), "", ""
		}, nil)
}

// scanASCSchemas stores one schema row per GraphQL API the API phase stored:
// AppSync has no op that lists schemas, so the row carries only the API id,
// under the NativeID {apiARN}/schema/_.
func scanASCSchemas(st *store.Store, acct *account, region, scanID string, apis []childParent) (int, int, error) {
	batch := make([]*store.Resource, 0, len(apis))
	for _, api := range apis {
		batch = append(batch, appsyncRow(acct, region, scanID, TypeAppSyncGraphQLSchema, api.arn+"/schema/_", "graphql-schema", map[string]string{"ApiId": api.id}, nil))
	}
	return upsertBatch(st, batch, appsyncSchemasOp)
}

// scanASCSourceAPIAssocs lists each GraphQL API's source API associations.
// An API the service refuses with BadRequestException keeps the
// associations read before it and is warned about once for the phase: the
// code was tolerated silently before, and why the service returns it has not
// been verified.
func scanASCSourceAPIAssocs(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncSourceAssocsOp, apis,
		func(ctx context.Context, id string, token *string) ([]appsynctypes.SourceApiAssociationSummary, *string, error) {
			out, err := client.ListSourceApiAssociations(ctx, &appsync.ListSourceApiAssociationsInput{ApiId: &id, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.SourceApiAssociationSummaries, out.NextToken, nil
		},
		func(api childParent, s appsynctypes.SourceApiAssociationSummary) (*store.Resource, string, string) {
			if sv(s.AssociationArn) == "" {
				return nil, "source API associations have an empty AssociationArn", api.arn + " " + sv(s.AssociationId)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncSourceAPIAssociation, *s.AssociationArn, appsyncLabel(s.AssociationId, s.AssociationArn), s, nil), "", ""
		},
		&skipCode{code: "BadRequestException", why: "their source API associations past that point were not listed, and why the service refuses an API is unverified"})
}

func scanASCChannelNamespaces(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncChannelNamespacesOp, apis,
		func(ctx context.Context, id string, token *string) ([]appsynctypes.ChannelNamespace, *string, error) {
			out, err := client.ListChannelNamespaces(ctx, &appsync.ListChannelNamespacesInput{ApiId: &id, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.ChannelNamespaces, out.NextToken, nil
		},
		func(api childParent, c appsynctypes.ChannelNamespace) (*store.Resource, string, string) {
			if sv(c.ChannelNamespaceArn) == "" {
				return nil, "channel namespaces have an empty ChannelNamespaceArn", api.arn + " " + sv(c.Name)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncChannelNamespace, *c.ChannelNamespaceArn, appsyncLabel(c.Name, c.ChannelNamespaceArn), c, c.Tags), "", ""
		}, nil)
}

// scanASCDomainNameAssocs reads the API association of each domain name, a
// per-domain singleton whose NativeID is {domainARN}/apiassociation.
// GetApiAssociation's modeled NotFoundException, for a domain with no
// associated API or one deleted since it was listed, stores nothing for that
// domain. Every other failure is returned after the other associations are
// stored, except a denial, which is warned about once.
func scanASCDomainNameAssocs(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent) (int, int, error) {
	return storeAppSyncChildren(ctx, st, acct, region, appsyncAPIAssociationOp, domains,
		func(ctx context.Context, name string, _ *string) ([]appsynctypes.ApiAssociation, *string, error) {
			out, err := client.GetApiAssociation(ctx, &appsync.GetApiAssociationInput{DomainName: &name})
			if err != nil || out.ApiAssociation == nil {
				return nil, nil, err
			}
			return []appsynctypes.ApiAssociation{*out.ApiAssociation}, nil, nil
		},
		func(domain childParent, a appsynctypes.ApiAssociation) (*store.Resource, string, string) {
			return appsyncRow(acct, region, scanID, TypeAppSyncDomainNameAPIAssociation, domain.arn+"/apiassociation", domain.id, a, nil), "", ""
		}, nil)
}

// appsyncTypeNames is what one API's ListTypes listing read before err.
type appsyncTypeNames struct {
	names []string
	err   error
}

// scanASCResolvers lists each GraphQL API's types (ListTypes), then the
// resolvers of every (API, type) pair (ListResolvers), both concurrently: a
// schema with N types costs N ListResolvers listings. The types an API's
// ListTypes listing read before it failed still have their resolvers listed,
// as storeTokenEachParent keeps a parent's rows: an API ListTypes no longer
// finds was deleted since it was listed, silently when no type was read and
// with one warning otherwise; a denied API is warned about once; any other
// failure is returned after the resolvers are stored.
func scanASCResolvers(ctx context.Context, client appSyncAPI, acct *account, region string, st *store.Store, scanID string, apis []childParent) (int, int, error) {
	reads, stopped := readEachKey(ctx, apis, func(ctx context.Context, api childParent) appsyncTypeNames {
		names, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListTypes(ctx, &appsync.ListTypesInput{
				ApiId: &api.id, Format: appsynctypes.TypeDefinitionFormatSdl, NextToken: token, MaxResults: appsyncPageMax,
			})
			if err != nil {
				return nil, nil, err
			}
			names := make([]string, len(out.Types))
			for i, t := range out.Types {
				names[i] = sv(t.Name)
			}
			return names, out.NextToken, nil
		})
		return appsyncTypeNames{names, err}
	})
	var (
		errs                     []error
		denied, vanished, noName skipTally
		pairs                    []childParent
	)
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", appsyncTypesOp, stopped))
	}
	for i, read := range reads {
		api := apis[i]
		switch err := read.err; {
		case err == nil:
		case isAPIErrorCode(err, appsyncNotFound...) && len(read.names) == 0:
		case isAPIErrorCode(err, appsyncNotFound...):
			vanished.skipped(api.id, err)
		case isAppSyncUnauthorized(err):
			denied.skipped(api.id, err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", appsyncTypesOp, api.id, err))
		}
		for _, name := range read.names {
			if name == "" {
				noName.skipped(api.arn, nil)
				continue
			}
			// A GraphQL type name cannot contain "/" (the spec's Name
			// grammar), so the key's last "/" splits it back unambiguously.
			pairs = append(pairs, childParent{id: api.id + "/" + name, arn: api.arn})
		}
	}
	total, inserted, err := storeAppSyncChildren(ctx, st, acct, region, appsyncResolversOp, pairs,
		func(ctx context.Context, key string, token *string) ([]appsynctypes.Resolver, *string, error) {
			apiID, typeName := appsyncSplitTypeKey(key)
			out, err := client.ListResolvers(ctx, &appsync.ListResolversInput{ApiId: &apiID, TypeName: &typeName, NextToken: token, MaxResults: appsyncPageMax})
			if err != nil {
				return nil, nil, err
			}
			return out.Resolvers, out.NextToken, nil
		},
		func(pair childParent, r appsynctypes.Resolver) (*store.Resource, string, string) {
			if sv(r.ResolverArn) == "" {
				return nil, "resolvers have an empty ResolverArn", pair.id + "." + sv(r.FieldName)
			}
			return appsyncRow(acct, region, scanID, TypeAppSyncResolver, *r.ResolverArn, sv(r.FieldName), r, nil), "", ""
		}, nil)
	err = errors.Join(append(errs, err)...)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	denied.warnDeniedIn(st, appsyncTypesOp, acct, region, "GraphQL API(s) (resolvers not read past that point)")
	if vanished.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: appsyncTypesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d GraphQL API(s) were deleted while their types were read, so only part of their resolvers were stored; first %s: %v", vanished.count, vanished.first, vanished.err),
		})
	}
	if noName.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: appsyncTypesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d listed types have an empty Name, so their resolvers were not read; first in %s", noName.count, noName.first),
		})
	}
	return total, inserted, err
}

// appsyncSplitTypeKey splits a scanASCResolvers key into its API id and type
// name.
func appsyncSplitTypeKey(key string) (apiID, typeName string) {
	i := strings.LastIndexByte(key, '/')
	return key[:i], key[i+1:]
}

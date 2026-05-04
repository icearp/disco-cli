package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveAppSyncApiChildren,
		EdgeDecl{TypeAppSyncApiKey, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncApiKey, TypeAppSyncApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncApiCache, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDataSource, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncFunctionConfiguration, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncGraphQLSchema, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncSourceApiAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncChannelNamespace, TypeAppSyncApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncGraphQLApi, store.RelAttachedTo},
	)
	registerResolver(resolveAppSyncDataSourceTargets,
		EdgeDecl{TypeAppSyncDataSource, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncDataSource, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncDataSource, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeAppSyncDataSource, TypeEventsEventBus, store.RelRoutesTo},
	)
	registerResolver(resolveAppSyncResolverDataSource,
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncDataSource, store.RelUses},
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncFunctionConfiguration, store.RelUses},
	)
	registerResolver(resolveAppSyncFunctionDataSource,
		EdgeDecl{TypeAppSyncFunctionConfiguration, TypeAppSyncDataSource, store.RelUses},
	)
	registerResolver(resolveAppSyncSourceApiAssoc,
		EdgeDecl{TypeAppSyncSourceApiAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
	)
	registerResolver(resolveAppSyncDomainNameApiAssoc,
		EdgeDecl{TypeAppSyncDomainNameApiAssociation, TypeAppSyncDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDomainNameApiAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDomainNameApiAssociation, TypeAppSyncApi, store.RelAttachedTo},
	)
}

// appsyncApiARNFromChild extracts the parent api ARN from a child resource's
// NativeID of shape `arn:aws:appsync:r:a:apis/{apiID}/<kind>/<id>`.
func appsyncApiARNFromChild(arn string) string {
	const prefix = "apis/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return arn
	}
	return arn[:i] + prefix + tail[:end]
}

// resolveAppSyncApiChildren links each per-API sub-resource (api-key,
// api-cache, data-source, function, schema, source-api-assoc, channel-
// namespace, resolver) to its parent graphql-api or event-api by parsing
// the NativeID's `apis/{apiID}` segment. Channel-namespace lives on event
// APIs (TypeAppSyncApi); the rest live on graphql-apis. Try both target
// types per row — only the matching one will land via FK-safe set check.
func resolveAppSyncApiChildren(acct *account, st *store.Store) error {
	childTypes := []string{
		TypeAppSyncApiKey,
		TypeAppSyncApiCache,
		TypeAppSyncDataSource,
		TypeAppSyncFunctionConfiguration,
		TypeAppSyncGraphQLSchema,
		TypeAppSyncSourceApiAssociation,
		TypeAppSyncChannelNamespace,
		TypeAppSyncResolver,
	}
	graphqlSet, err := scannedIDSet(acct, st, TypeAppSyncGraphQLApi)
	if err != nil {
		return err
	}
	apiSet, err := scannedIDSet(acct, st, TypeAppSyncApi)
	if err != nil {
		return err
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := appsyncApiARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			gqlID := store.ResourceID("aws", acct.ID, TypeAppSyncGraphQLApi, parent)
			apiID := store.ResourceID("aws", acct.ID, TypeAppSyncApi, parent)
			switch {
			case graphqlSet[gqlID]:
				if err := st.UpsertRelationship(r.ID, gqlID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync %s→graphql-api: %w", ctype, err)
				}
			case apiSet[apiID]:
				if err := st.UpsertRelationship(r.ID, apiID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync %s→api: %w", ctype, err)
				}
			}
		}
	}
	return nil
}

// dynamoTableARN reconstructs a DynamoDB table ARN from region+account+name.
func dynamoTableARN(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, acctID, name)
}

// resolveAppSyncDataSourceTargets walks each data-source's typed sub-config
// (DynamodbConfig.TableName, LambdaConfig.LambdaFunctionArn,
// EventBridgeConfig.EventBusArn) plus ServiceRoleArn and emits the
// corresponding edges. FK-safe.
func resolveAppSyncDataSourceTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncDataSource},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	tableSet, err := scannedIDSet(acct, st, TypeDynamoDBTable)
	if err != nil {
		return err
	}
	busSet, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServiceRoleArn *string `json:"ServiceRoleArn"`
			DynamodbConfig *struct {
				TableName *string `json:"TableName"`
			} `json:"DynamodbConfig"`
			LambdaConfig *struct {
				LambdaFunctionArn *string `json:"LambdaFunctionArn"`
			} `json:"LambdaConfig"`
			EventBridgeConfig *struct {
				EventBusArn *string `json:"EventBusArn"`
			} `json:"EventBridgeConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if arn := sv(attrs.ServiceRoleArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync-ds→role: %w", err)
				}
			}
		}
		if attrs.DynamodbConfig != nil {
			if name := sv(attrs.DynamodbConfig.TableName); name != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeDynamoDBTable, dynamoTableARN(region, acct.ID, name))
				if tableSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync-ds→ddb: %w", err)
					}
				}
			}
		}
		if attrs.LambdaConfig != nil {
			if arn := sv(attrs.LambdaConfig.LambdaFunctionArn); arn != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, arn)
				if lambdaSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync-ds→lambda: %w", err)
					}
				}
			}
		}
		if attrs.EventBridgeConfig != nil {
			if arn := sv(attrs.EventBridgeConfig.EventBusArn); arn != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, arn)
				if busSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync-ds→eventbus: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// appsyncDataSourceARNByName indexes each scanned data-source row by its
// (apiID, name) pair so a resolver / function-config can link to it via the
// bare DataSourceName field.
func appsyncDataSourceARNByName(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncDataSource},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		name := sv(attrs.Name)
		if name == "" {
			continue
		}
		apiARN := appsyncApiARNFromChild(r.NativeID)
		if apiARN == "" {
			continue
		}
		idx[apiARN+"|"+name] = r.ID
	}
	return idx, nil
}

// resolveAppSyncResolverDataSource links each resolver to its DataSourceName
// (Unit resolvers) or pipeline FunctionConfigurations[] (Pipeline resolvers).
func resolveAppSyncResolverDataSource(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncResolver},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, err := appsyncDataSourceARNByName(acct, st)
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeAppSyncFunctionConfiguration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceName *string `json:"DataSourceName"`
			PipelineConfig *struct {
				Functions []string `json:"Functions"`
			} `json:"PipelineConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		apiARN := appsyncApiARNFromChild(r.NativeID)
		if dsName := sv(attrs.DataSourceName); dsName != "" && apiARN != "" {
			if dsID, ok := dsIdx[apiARN+"|"+dsName]; ok {
				if err := st.UpsertRelationship(r.ID, dsID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync-resolver→ds: %w", err)
				}
			}
		}
		if attrs.PipelineConfig != nil {
			for _, fnID := range attrs.PipelineConfig.Functions {
				if fnID == "" {
					continue
				}
				// Functions[] carries bare function IDs; reconstruct ARN as
				// apis/{apiID}/functions/{fnID}.
				if apiARN == "" {
					continue
				}
				fnARN := apiARN + "/functions/" + fnID
				tgtID := store.ResourceID("aws", acct.ID, TypeAppSyncFunctionConfiguration, fnARN)
				if !fnSet[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync-resolver→function: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAppSyncFunctionDataSource links each function-configuration to its
// DataSourceName.
func resolveAppSyncFunctionDataSource(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncFunctionConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, err := appsyncDataSourceARNByName(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceName *string `json:"DataSourceName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		apiARN := appsyncApiARNFromChild(r.NativeID)
		dsName := sv(attrs.DataSourceName)
		if dsName == "" || apiARN == "" {
			continue
		}
		dsID, ok := dsIdx[apiARN+"|"+dsName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, dsID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appsync-fn→ds: %w", err)
		}
	}
	return nil
}

// resolveAppSyncSourceApiAssoc links each source-api-association to the
// MergedApi side; the parent (SourceApi) is wired by resolveAppSyncApiChildren
// already since the row's NativeID encodes it.
func resolveAppSyncSourceApiAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncSourceApiAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	graphqlSet, err := scannedIDSet(acct, st, TypeAppSyncGraphQLApi)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			MergedApiArn *string `json:"MergedApiArn"`
			SourceApiArn *string `json:"SourceApiArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range []string{sv(attrs.MergedApiArn), sv(attrs.SourceApiArn)} {
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeAppSyncGraphQLApi, arn)
			if !graphqlSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appsync-sa-assoc→graphql-api: %w", err)
			}
		}
	}
	return nil
}

// resolveAppSyncDomainNameApiAssoc links each association to its parent
// domain-name (via NativeID parse) and to the associated api (ApiId attr).
func resolveAppSyncDomainNameApiAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncDomainNameApiAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dnSet, err := scannedIDSet(acct, st, TypeAppSyncDomainName)
	if err != nil {
		return err
	}
	graphqlSet, err := scannedIDSet(acct, st, TypeAppSyncGraphQLApi)
	if err != nil {
		return err
	}
	apiSet, err := scannedIDSet(acct, st, TypeAppSyncApi)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// Domain-name parent: strip trailing "/apiassociation".
		if dnARN := strings.TrimSuffix(r.NativeID, "/apiassociation"); dnARN != r.NativeID {
			tgtID := store.ResourceID("aws", acct.ID, TypeAppSyncDomainName, dnARN)
			if dnSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync-dn-assoc→domain-name: %w", err)
				}
			}
		}
		var attrs struct {
			ApiId *string `json:"ApiId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		apiID := sv(attrs.ApiId)
		if apiID == "" {
			continue
		}
		region := sv(r.Region)
		apiARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s", region, acct.ID, apiID)
		gqlID := store.ResourceID("aws", acct.ID, TypeAppSyncGraphQLApi, apiARN)
		evtID := store.ResourceID("aws", acct.ID, TypeAppSyncApi, apiARN)
		switch {
		case graphqlSet[gqlID]:
			if err := st.UpsertRelationship(r.ID, gqlID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appsync-dn-assoc→graphql-api: %w", err)
			}
		case apiSet[evtID]:
			if err := st.UpsertRelationship(r.ID, evtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appsync-dn-assoc→api: %w", err)
			}
		}
	}
	return nil
}

func init() {
	registerResolver(resolveAppSyncGraphQLAPIRefs,
		EdgeDecl{TypeAppSyncGraphQLApi, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeCognitoUserPool, store.RelUses},
	)
}

// resolveAppSyncGraphQLAPIRefs wires GraphqlApi → Lambda authorizer
// (LambdaAuthorizerConfig.AuthorizerUri), IAM CloudWatch logs role
// (LogConfig.CloudWatchLogsRoleArn), MergedApi execution role, and
// Cognito user pool (UserPoolConfig.UserPoolId / per-region).
func resolveAppSyncGraphQLAPIRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncGraphQLApi}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	upSet, err := scannedIDSet(acct, st, TypeCognitoUserPool)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			MergedApiExecutionRoleArn *string `json:"MergedApiExecutionRoleArn"`
			LambdaAuthorizerConfig    *struct {
				AuthorizerUri *string `json:"AuthorizerUri"`
			} `json:"LambdaAuthorizerConfig"`
			LogConfig *struct {
				CloudWatchLogsRoleArn *string `json:"CloudWatchLogsRoleArn"`
			} `json:"LogConfig"`
			UserPoolConfig *struct {
				UserPoolID *string `json:"UserPoolId"`
				AwsRegion  *string `json:"AwsRegion"`
			} `json:"UserPoolConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		emitRole := func(rarn string) error {
			if !strings.Contains(rarn, ":role/") {
				return nil
			}
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if !roleSet[tgt] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil)
		}
		if err := emitRole(sv(attrs.MergedApiExecutionRoleArn)); err != nil {
			return fmt.Errorf("upsert appsync→merged-role: %w", err)
		}
		if attrs.LogConfig != nil {
			if err := emitRole(sv(attrs.LogConfig.CloudWatchLogsRoleArn)); err != nil {
				return fmt.Errorf("upsert appsync→logs-role: %w", err)
			}
		}
		if attrs.LambdaAuthorizerConfig != nil {
			larn := sv(attrs.LambdaAuthorizerConfig.AuthorizerUri)
			if strings.Contains(larn, ":lambda:") && strings.Contains(larn, ":function:") {
				// Strip optional :version/:alias suffix to canonical function ARN.
				if i := strings.Index(larn, ":function:"); i > 0 {
					tail := larn[i+len(":function:"):]
					if j := strings.IndexByte(tail, ':'); j > 0 {
						larn = larn[:i+len(":function:")+j]
					}
				}
				tgt := store.ResourceID("aws", acct.ID, TypeLambdaFunction, larn)
				if lambdaSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync→lambda: %w", err)
					}
				}
			}
		}
		if attrs.UserPoolConfig != nil {
			upID := sv(attrs.UserPoolConfig.UserPoolID)
			upRegion := sv(attrs.UserPoolConfig.AwsRegion)
			if upID != "" && upRegion != "" {
				upARN := "arn:aws:cognito-idp:" + upRegion + ":" + acct.ID + ":userpool/" + upID
				tgt := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, upARN)
				if upSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync→user-pool: %w", err)
					}
				}
			}
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveAppSyncAPIChildren,
		EdgeDecl{TypeAppSyncAPIKey, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncAPIKey, TypeAppSyncAPI, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncAPICache, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDataSource, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncFunctionConfiguration, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncGraphQLSchema, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncSourceAPIAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncChannelNamespace, TypeAppSyncAPI, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncGraphQLApi, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppSyncDataSourceTargets,
		EdgeDecl{TypeAppSyncDataSource, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncDataSource, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncDataSource, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeAppSyncDataSource, TypeEventsEventBus, store.RelRoutesTo},
	)
	registerResolver(
		resolveAppSyncResolverDataSource,
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncDataSource, store.RelUses},
		EdgeDecl{TypeAppSyncResolver, TypeAppSyncFunctionConfiguration, store.RelUses},
	)
	registerResolver(
		resolveAppSyncFunctionDataSource,
		EdgeDecl{TypeAppSyncFunctionConfiguration, TypeAppSyncDataSource, store.RelUses},
	)
	registerResolver(
		resolveAppSyncSourceAPIAssoc,
		EdgeDecl{TypeAppSyncSourceAPIAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppSyncDomainNameAPIAssoc,
		EdgeDecl{TypeAppSyncDomainNameAPIAssociation, TypeAppSyncDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDomainNameAPIAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeAppSyncDomainNameAPIAssociation, TypeAppSyncAPI, store.RelAttachedTo},
	)
}

// appsyncAPIARNFromChild extracts the parent api ARN from a child resource's
// NativeID of shape `arn:aws:appsync:r:a:apis/{apiID}/<kind>/<id>`.
func appsyncAPIARNFromChild(arn string) string {
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

// resolveAppSyncAPIChildren links each per-API sub-resource (api-key,
// api-cache, data-source, function, schema, source-api-assoc, channel-
// namespace, resolver) to its parent graphql-api or event-api by parsing
// the NativeID's `apis/{apiID}` segment. Channel-namespace lives on event
// APIs (TypeAppSyncAPI); the rest live on graphql-apis. Try both target
// types per row — only the matching one will land via FK-safe set check.
func resolveAppSyncAPIChildren(acct *account, st *store.Store) error {
	childTypes := []string{
		TypeAppSyncAPIKey,
		TypeAppSyncAPICache,
		TypeAppSyncDataSource,
		TypeAppSyncFunctionConfiguration,
		TypeAppSyncGraphQLSchema,
		TypeAppSyncSourceAPIAssociation,
		TypeAppSyncChannelNamespace,
		TypeAppSyncResolver,
	}
	graphqlSet, err := scannedIDSet(acct, st, TypeAppSyncGraphQLApi)
	if err != nil {
		return err
	}
	apiSet, err := scannedIDSet(acct, st, TypeAppSyncAPI)
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
			parent := appsyncAPIARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			gqlID := store.ResourceID("aws", acct.ID, TypeAppSyncGraphQLApi, parent)
			apiID := store.ResourceID("aws", acct.ID, TypeAppSyncAPI, parent)
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
		apiARN := appsyncAPIARNFromChild(r.NativeID)
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
		apiARN := appsyncAPIARNFromChild(r.NativeID)
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
		apiARN := appsyncAPIARNFromChild(r.NativeID)
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

// resolveAppSyncSourceAPIAssoc links each source-api-association to the
// MergedApi side; the parent (SourceApi) is wired by resolveAppSyncAPIChildren
// already since the row's NativeID encodes it.
func resolveAppSyncSourceAPIAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncSourceAPIAssociation},
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
			MergedAPIArn *string `json:"MergedApiArn"`
			SourceAPIArn *string `json:"SourceApiArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range []string{sv(attrs.MergedAPIArn), sv(attrs.SourceAPIArn)} {
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

// resolveAppSyncDomainNameAPIAssoc links each association to its parent
// domain-name (via NativeID parse) and to the associated api (APIID attr).
func resolveAppSyncDomainNameAPIAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncDomainNameAPIAssociation},
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
	apiSet, err := scannedIDSet(acct, st, TypeAppSyncAPI)
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
			APIID *string `json:"ApiId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		apiID := sv(attrs.APIID)
		if apiID == "" {
			continue
		}
		region := sv(r.Region)
		apiARN := fmt.Sprintf("arn:aws:appsync:%s:%s:apis/%s", region, acct.ID, apiID)
		gqlID := store.ResourceID("aws", acct.ID, TypeAppSyncGraphQLApi, apiARN)
		evtID := store.ResourceID("aws", acct.ID, TypeAppSyncAPI, apiARN)
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
	registerResolver(
		resolveAppSyncGraphQLAPIRefs,
		EdgeDecl{TypeAppSyncGraphQLApi, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeCognitoUserPool, store.RelUses},
	)
}

// resolveAppSyncGraphQLAPIRefs wires GraphqlApi → Lambda authorizer
// (LambdaAuthorizerConfig.AuthorizerURI), IAM CloudWatch logs role
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
			MergedAPIExecutionRoleArn *string `json:"MergedApiExecutionRoleArn"`
			LambdaAuthorizerConfig    *struct {
				AuthorizerURI *string `json:"AuthorizerUri"`
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
		if err := emitRole(sv(attrs.MergedAPIExecutionRoleArn)); err != nil {
			return fmt.Errorf("upsert appsync→merged-role: %w", err)
		}
		if attrs.LogConfig != nil {
			if err := emitRole(sv(attrs.LogConfig.CloudWatchLogsRoleArn)); err != nil {
				return fmt.Errorf("upsert appsync→logs-role: %w", err)
			}
		}
		if attrs.LambdaAuthorizerConfig != nil {
			larn := sv(attrs.LambdaAuthorizerConfig.AuthorizerURI)
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

func init() {
	registerResolver(
		resolveAppSyncEventAPIRefs,
		EdgeDecl{TypeAppSyncAPI, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncAPI, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncAPI, TypeCognitoUserPool, store.RelUses},
		EdgeDecl{TypeAppSyncAPI, TypeWAFv2WebACL, store.RelUses},
	)
}

// resolveAppSyncEventAPIRefs walks each AppSync Event API's EventConfig
// for AuthProviders (Cognito + Lambda authorizer) and LogConfig
// (CloudWatchLogsRoleArn), plus top-level WafWebACLArn.
func resolveAppSyncEventAPIRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppSyncAPI}, Limit: util.AllResources,
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
	waclSet, err := scannedIDSet(acct, st, TypeWAFv2WebACL)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WafWebACLArn *string `json:"WafWebAclArn"`
			EventConfig  *struct {
				AuthProviders []struct {
					CognitoConfig *struct {
						UserPoolID *string `json:"UserPoolId"`
						AwsRegion  *string `json:"AwsRegion"`
					} `json:"CognitoConfig"`
					LambdaAuthorizerConfig *struct {
						AuthorizerURI *string `json:"AuthorizerUri"`
					} `json:"LambdaAuthorizerConfig"`
				} `json:"AuthProviders"`
				LogConfig *struct {
					CloudWatchLogsRoleArn *string `json:"CloudWatchLogsRoleArn"`
				} `json:"LogConfig"`
			} `json:"EventConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if wa := sv(attrs.WafWebACLArn); strings.Contains(wa, ":wafv2:") {
			tgt := store.ResourceID("aws", acct.ID, TypeWAFv2WebACL, wa)
			if waclSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync-api→waf: %w", err)
				}
			}
		}
		if attrs.EventConfig == nil {
			continue
		}
		if attrs.EventConfig.LogConfig != nil {
			rarn := sv(attrs.EventConfig.LogConfig.CloudWatchLogsRoleArn)
			if strings.Contains(rarn, ":role/") {
				tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
				if roleSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
						return fmt.Errorf("upsert appsync-api→logs-role: %w", err)
					}
				}
			}
		}
		for _, ap := range attrs.EventConfig.AuthProviders {
			if ap.LambdaAuthorizerConfig != nil {
				larn := sv(ap.LambdaAuthorizerConfig.AuthorizerURI)
				if strings.Contains(larn, ":lambda:") && strings.Contains(larn, ":function:") {
					if i := strings.Index(larn, ":function:"); i > 0 {
						tail := larn[i+len(":function:"):]
						if j := strings.IndexByte(tail, ':'); j > 0 {
							larn = larn[:i+len(":function:")+j]
						}
					}
					tgt := store.ResourceID("aws", acct.ID, TypeLambdaFunction, larn)
					if lambdaSet[tgt] {
						if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert appsync-api→lambda: %w", err)
						}
					}
				}
			}
			if ap.CognitoConfig != nil {
				upID := sv(ap.CognitoConfig.UserPoolID)
				upRegion := sv(ap.CognitoConfig.AwsRegion)
				if upID != "" && upRegion != "" {
					upARN := "arn:aws:cognito-idp:" + upRegion + ":" + acct.ID + ":userpool/" + upID
					tgt := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, upARN)
					if upSet[tgt] {
						if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert appsync-api→user-pool: %w", err)
						}
					}
				}
			}
		}
	}
	return nil
}

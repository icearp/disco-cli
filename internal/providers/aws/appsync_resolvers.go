package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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

// appsyncPartition returns the partition of an AppSync row's ARN NativeID,
// which the ARNs a resolver rebuilds from that row's bare ids share.
func appsyncPartition(nativeID string) (string, error) {
	a, err := arn.Parse(nativeID)
	if err != nil {
		return "", fmt.Errorf("parse NativeID: %w", err)
	}
	return a.Partition, nil
}

// appsyncDecodeErr reports a row whose attributes or NativeID could not be
// read, so its edges were not emitted.
func appsyncDecodeErr(r store.Resource, err error) error {
	return fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err)
}

// appsyncWriteErr returns a failed edge write joined with the decode errors
// collected before it, so an early return loses neither.
func appsyncWriteErr(decodeErrs []error, err error) error {
	return errors.Join(append(decodeErrs, err)...)
}

// appsyncCognitoPoolARN rebuilds the ARN of a Cognito user pool an AppSync
// API names by id and region, in the account and partition of the API row.
func appsyncCognitoPoolARN(apiNativeID, acctID, region, poolID string) (string, error) {
	partition, err := appsyncPartition(apiNativeID)
	if err != nil {
		return "", err
	}
	return "arn:" + partition + ":cognito-idp:" + region + ":" + acctID + ":userpool/" + poolID, nil
}

// resolveAppSyncAPIChildren links each per-API sub-resource (api-key,
// api-cache, data-source, function, schema, source-api-assoc, channel-
// namespace, resolver) to its parent graphql-api or event-api by parsing
// the NativeID's `apis/{apiID}` segment. Channel-namespace lives on event
// APIs (TypeAppSyncAPI); the rest on graphql-apis. Tries both target
// types per row — only the match lands via FK-safe set check.
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
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype},
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
			parentID := store.ResourceID("aws", acct.ID, parent)
			switch {
			case graphqlSet[parentID]:
				if err := st.UpsertRelationship(r.ID, parentID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync %s→graphql-api: %w", ctype, err)
				}
			case apiSet[parentID]:
				if err := st.UpsertRelationship(r.ID, parentID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appsync %s→api: %w", ctype, err)
				}
			}
		}
	}
	return nil
}

// dynamoTableARN reconstructs a DynamoDB table ARN from its parts.
func dynamoTableARN(partition, region, acctID, name string) string {
	return fmt.Sprintf("arn:%s:dynamodb:%s:%s:table/%s", partition, region, acctID, name)
}

// appsyncTarget is an edge a data source emits: the target's ResourceID, the
// edge kind and what the target is, for the write error.
type appsyncTarget struct{ id, kind, what string }

// resolveAppSyncDataSourceTargets walks each data-source's typed sub-config
// (DynamodbConfig.TableName in DynamodbConfig.AwsRegion,
// LambdaConfig.LambdaFunctionArn, EventBridgeConfig.EventBusArn) plus
// ServiceRoleArn and emits the corresponding edges. FK-safe. A row whose
// attributes or NativeID cannot be read is returned as an error after the
// other rows' edges are emitted.
func resolveAppSyncDataSourceTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncDataSource},
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
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ServiceRoleArn *string `json:"ServiceRoleArn"`
			DynamodbConfig *struct {
				TableName *string `json:"TableName"`
				AwsRegion *string `json:"AwsRegion"`
			} `json:"DynamodbConfig"`
			LambdaConfig *struct {
				LambdaFunctionArn *string `json:"LambdaFunctionArn"`
			} `json:"LambdaConfig"`
			EventBridgeConfig *struct {
				EventBusArn *string `json:"EventBusArn"`
			} `json:"EventBridgeConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		var targets []appsyncTarget
		if arn := sv(attrs.ServiceRoleArn); arn != "" {
			if id := store.ResourceID("aws", acct.ID, arn); roleSet[id] {
				targets = append(targets, appsyncTarget{id, store.RelAssumes, "role"})
			}
		}
		if c := attrs.DynamodbConfig; c != nil && sv(c.TableName) != "" && sv(c.AwsRegion) != "" {
			partition, err := appsyncPartition(r.NativeID)
			if err != nil {
				decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			} else if id := store.ResourceID("aws", acct.ID, dynamoTableARN(partition, *c.AwsRegion, acct.ID, *c.TableName)); tableSet[id] {
				targets = append(targets, appsyncTarget{id, store.RelUses, "ddb"})
			}
		}
		if c := attrs.LambdaConfig; c != nil && sv(c.LambdaFunctionArn) != "" {
			if id := store.ResourceID("aws", acct.ID, *c.LambdaFunctionArn); lambdaSet[id] {
				targets = append(targets, appsyncTarget{id, store.RelUses, "lambda"})
			}
		}
		if c := attrs.EventBridgeConfig; c != nil && sv(c.EventBusArn) != "" {
			if id := store.ResourceID("aws", acct.ID, *c.EventBusArn); busSet[id] {
				targets = append(targets, appsyncTarget{id, store.RelRoutesTo, "eventbus"})
			}
		}
		for _, t := range targets {
			if err := st.UpsertRelationship(r.ID, t.id, t.kind, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-ds→%s: %w", t.what, err))
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// appsyncDataSourceARNByName indexes each scanned data-source row by its
// (apiARN, name) pair so a resolver / function-config can link to it via the
// bare DataSourceName field; the API ARN carries the region and account. It
// returns the rows whose attributes cannot be read as decode errors beside
// the index of the others.
func appsyncDataSourceARNByName(acct *account, st *store.Store) (map[string]string, []error, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncDataSource},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, nil, err
	}
	var decodeErrs []error
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
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
	return idx, decodeErrs, nil
}

// resolveAppSyncResolverDataSource links each resolver to its DataSourceName
// (Unit resolvers) or pipeline FunctionConfigurations[] (Pipeline resolvers).
// Rows whose attributes cannot be read, resolvers and data sources alike,
// are returned as errors after the other rows' edges are emitted.
func resolveAppSyncResolverDataSource(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncResolver},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, decodeErrs, err := appsyncDataSourceARNByName(acct, st)
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeAppSyncFunctionConfiguration)
	if err != nil {
		return appsyncWriteErr(decodeErrs, err)
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceName *string `json:"DataSourceName"`
			PipelineConfig *struct {
				Functions []string `json:"Functions"`
			} `json:"PipelineConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		apiARN := appsyncAPIARNFromChild(r.NativeID)
		if apiARN == "" {
			continue
		}
		if dsID, ok := dsIdx[apiARN+"|"+sv(attrs.DataSourceName)]; ok {
			if err := st.UpsertRelationship(r.ID, dsID, store.RelUses, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-resolver→ds: %w", err))
			}
		}
		if attrs.PipelineConfig == nil {
			continue
		}
		for _, fnID := range attrs.PipelineConfig.Functions {
			// Functions[] carries bare function IDs; the function ARN is
			// apis/{apiID}/functions/{fnID}.
			tgtID := store.ResourceID("aws", acct.ID, apiARN+"/functions/"+fnID)
			if fnID == "" || !fnSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-resolver→function: %w", err))
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveAppSyncFunctionDataSource links each function-configuration to its
// DataSourceName. Rows whose attributes cannot be read, functions and data
// sources alike, are returned as errors after the other rows' edges are
// emitted.
func resolveAppSyncFunctionDataSource(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncFunctionConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, decodeErrs, err := appsyncDataSourceARNByName(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceName *string `json:"DataSourceName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
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
			return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-fn→ds: %w", err))
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveAppSyncSourceAPIAssoc links each source-api-association to the
// MergedApi side; the parent (SourceApi) is already wired by
// resolveAppSyncAPIChildren since the row's NativeID encodes it. A row whose
// attributes cannot be read is returned as an error after the other rows'
// edges are emitted.
func resolveAppSyncSourceAPIAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncSourceAPIAssociation},
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
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			MergedAPIArn *string `json:"MergedApiArn"`
			SourceAPIArn *string `json:"SourceApiArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		for _, arn := range []string{sv(attrs.MergedAPIArn), sv(attrs.SourceAPIArn)} {
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !graphqlSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-sa-assoc→graphql-api: %w", err))
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveAppSyncDomainNameAPIAssoc links each association to its parent
// domain-name (its NativeID minus "/apiassociation") and to the associated
// api (ApiId attr), whose ARN is rebuilt in the domain ARN's partition,
// region and account. A row whose attributes cannot be read is returned as
// an error after the other rows' edges are emitted.
func resolveAppSyncDomainNameAPIAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncDomainNameAPIAssociation},
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
	var decodeErrs []error
	for _, r := range rows {
		dnARN, ok := strings.CutSuffix(r.NativeID, "/apiassociation")
		if !ok {
			continue
		}
		if tgtID := store.ResourceID("aws", acct.ID, dnARN); dnSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-dn-assoc→domain-name: %w", err))
			}
		}
		var attrs struct {
			APIID *string `json:"ApiId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		prefix, _, found := strings.Cut(dnARN, ":domainnames/")
		apiID := sv(attrs.APIID)
		if apiID == "" || !found {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, prefix+":apis/"+apiID)
		switch {
		case graphqlSet[tgtID]:
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-dn-assoc→graphql-api: %w", err))
			}
		case apiSet[tgtID]:
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-dn-assoc→api: %w", err))
			}
		}
	}
	return errors.Join(decodeErrs...)
}

func init() {
	registerResolver(
		resolveAppSyncGraphQLAPIRefs,
		EdgeDecl{TypeAppSyncGraphQLApi, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAppSyncGraphQLApi, TypeCognitoUserPool, store.RelUses},
	)
}

// appsyncLambdaFunctionARN trims an optional :version / :alias qualifier off
// a Lambda authorizer URI, giving the function ARN; "" when the URI names no
// Lambda function.
func appsyncLambdaFunctionARN(uri string) string {
	const marker = ":function:"
	i := strings.Index(uri, marker)
	if !strings.Contains(uri, ":lambda:") || i <= 0 {
		return ""
	}
	if j := strings.IndexByte(uri[i+len(marker):], ':'); j > 0 {
		return uri[:i+len(marker)+j]
	}
	return uri
}

// resolveAppSyncGraphQLAPIRefs wires GraphqlApi → Lambda authorizer
// (LambdaAuthorizerConfig.AuthorizerURI), IAM CloudWatch logs role
// (LogConfig.CloudWatchLogsRoleArn), MergedApi execution role, and
// Cognito user pool (UserPoolConfig.UserPoolId in UserPoolConfig.AwsRegion,
// in the API row's account and partition). A row whose attributes or
// NativeID cannot be read is returned as an error after the other rows'
// edges are emitted.
func resolveAppSyncGraphQLAPIRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncGraphQLApi}, Limit: util.AllResources,
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
	var decodeErrs []error
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
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		emitRole := func(rarn string) error {
			if !strings.Contains(rarn, ":role/") {
				return nil
			}
			tgt := store.ResourceID("aws", acct.ID, rarn)
			if !roleSet[tgt] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil)
		}
		if err := emitRole(sv(attrs.MergedAPIExecutionRoleArn)); err != nil {
			return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync→merged-role: %w", err))
		}
		if attrs.LogConfig != nil {
			if err := emitRole(sv(attrs.LogConfig.CloudWatchLogsRoleArn)); err != nil {
				return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync→logs-role: %w", err))
			}
		}
		if attrs.LambdaAuthorizerConfig != nil {
			if larn := appsyncLambdaFunctionARN(sv(attrs.LambdaAuthorizerConfig.AuthorizerURI)); larn != "" {
				tgt := store.ResourceID("aws", acct.ID, larn)
				if lambdaSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync→lambda: %w", err))
					}
				}
			}
		}
		if c := attrs.UserPoolConfig; c != nil && sv(c.UserPoolID) != "" && sv(c.AwsRegion) != "" {
			upARN, err := appsyncCognitoPoolARN(r.NativeID, acct.ID, *c.AwsRegion, *c.UserPoolID)
			if err != nil {
				decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
				continue
			}
			if tgt := store.ResourceID("aws", acct.ID, upARN); upSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync→user-pool: %w", err))
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
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
// for AuthProviders (Cognito, in the API row's account and partition, and
// Lambda authorizer) and LogConfig (CloudWatchLogsRoleArn), plus top-level
// WafWebACLArn. A row whose attributes or NativeID cannot be read is
// returned as an error after the other rows' edges are emitted.
func resolveAppSyncEventAPIRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppSyncAPI}, Limit: util.AllResources,
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
	var decodeErrs []error
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
			decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
			continue
		}
		if wa := sv(attrs.WafWebACLArn); strings.Contains(wa, ":wafv2:") {
			tgt := store.ResourceID("aws", acct.ID, wa)
			if waclSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-api→waf: %w", err))
				}
			}
		}
		if attrs.EventConfig == nil {
			continue
		}
		if attrs.EventConfig.LogConfig != nil {
			rarn := sv(attrs.EventConfig.LogConfig.CloudWatchLogsRoleArn)
			if strings.Contains(rarn, ":role/") {
				tgt := store.ResourceID("aws", acct.ID, rarn)
				if roleSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
						return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-api→logs-role: %w", err))
					}
				}
			}
		}
		for _, ap := range attrs.EventConfig.AuthProviders {
			if ap.LambdaAuthorizerConfig != nil {
				if larn := appsyncLambdaFunctionARN(sv(ap.LambdaAuthorizerConfig.AuthorizerURI)); larn != "" {
					tgt := store.ResourceID("aws", acct.ID, larn)
					if lambdaSet[tgt] {
						if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
							return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-api→lambda: %w", err))
						}
					}
				}
			}
			c := ap.CognitoConfig
			if c == nil || sv(c.UserPoolID) == "" || sv(c.AwsRegion) == "" {
				continue
			}
			upARN, err := appsyncCognitoPoolARN(r.NativeID, acct.ID, *c.AwsRegion, *c.UserPoolID)
			if err != nil {
				decodeErrs = append(decodeErrs, appsyncDecodeErr(r, err))
				continue
			}
			if tgt := store.ResourceID("aws", acct.ID, upARN); upSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return appsyncWriteErr(decodeErrs, fmt.Errorf("upsert appsync-api→user-pool: %w", err))
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}

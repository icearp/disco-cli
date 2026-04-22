package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(func(acct *account, st *store.Store) error {
		if err := resolveLambdaRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaAliasRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaVersionRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaESMRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaEventInvokeConfigRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaFunctionURLRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveLambdaCodeSigningConfigRelationships(acct, st); err != nil {
			return err
		}
		return resolveLambdaLayerRelationships(acct, st)
	})
}

// lambdaStripQualifier strips the version or alias qualifier from a qualified
// Lambda ARN, returning the unqualified function ARN.
// "arn:aws:lambda:{r}:{acct}:function:{name}:{qualifier}" → "arn:aws:lambda:{r}:{acct}:function:{name}"
// Unqualified ARNs are returned unchanged.
func lambdaStripQualifier(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 8 {
		return strings.Join(parts[:7], ":")
	}
	return arn
}

// resolveLambdaRelationships links each function to its IAM execution role.
func resolveLambdaRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Role *string `json:"Role"` // IAM role ARN
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Role != nil {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.Role)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda→role relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveLambdaAliasRelationships links each alias to its parent function.
// The alias NativeID is a qualified ARN; stripping the qualifier yields the
// function ARN.
func resolveLambdaAliasRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue // no qualifier to strip; skip
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda alias→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaVersionRelationships links each published version to its parent
// function. The version NativeID is a qualified ARN ending in the version number.
func resolveLambdaVersionRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda version→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaESMRelationships links each event source mapping to its target
// function. The FunctionArn in the ESM attributes may be qualified; the
// qualifier is stripped to obtain the base function ARN.
func resolveLambdaESMRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaESM},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		var attrs struct {
			FunctionArn *string `json:"FunctionArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		fnARN := lambdaStripQualifier(sv(attrs.FunctionArn))
		if fnARN == "" {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda ESM→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaEventInvokeConfigRelationships links each async invocation config
// to its parent function. The NativeID is a qualified FunctionArn.
func resolveLambdaEventInvokeConfigRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaEventInvokeConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda event-invoke-config→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaFunctionURLRelationships links each function URL config to its
// parent function. The NativeID is a qualified FunctionArn.
func resolveLambdaFunctionURLRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaURL},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda url→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaCodeSigningConfigRelationships links each function that has a
// code signing config to that config via a "uses" relationship.
// CodeSigningConfigArn is extracted from the function's AttributesJSON.
func resolveLambdaCodeSigningConfigRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			CodeSigningConfigArn *string `json:"CodeSigningConfigArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cscARN := sv(attrs.CodeSigningConfigArn)
		if cscARN == "" {
			continue
		}
		cscID := store.ResourceID("aws", acct.ID, TypeLambdaCodeSigningConfig, cscARN)
		if err := st.UpsertRelationship(r.ID, cscID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda function→code-signing-config: %w", err)
		}
	}
	return nil
}

// resolveLambdaLayerRelationships links each function to the layer versions it
// uses. Layer ARNs are extracted from the Layers array in the function's
// AttributesJSON.
func resolveLambdaLayerRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Layers []struct {
				Arn *string `json:"Arn"`
			} `json:"Layers"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, layer := range attrs.Layers {
			layerARN := sv(layer.Arn)
			if layerARN == "" {
				continue
			}
			layerID := store.ResourceID("aws", acct.ID, TypeLambdaLayerVersion, layerARN)
			if err := st.UpsertRelationship(r.ID, layerID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda function→layer-version: %w", err)
			}
		}
	}
	return nil
}

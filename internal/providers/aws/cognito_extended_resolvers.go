package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCognitoUserPoolChildren,
		EdgeDecl{TypeCognitoUserPoolDomain, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoUserPoolGroup, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoUserPoolIdentityProvider, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoUserPoolResourceServer, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoUserPoolRiskConfigurationAttachment, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoUserPoolUICustomizationAttachment, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoLogDeliveryConfiguration, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoTerms, TypeCognitoUserPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoManagedLoginBranding, TypeCognitoUserPool, store.RelAttachedTo},
	)
	registerResolver(
		resolveCognitoUserPoolGroupRole,
		EdgeDecl{TypeCognitoUserPoolGroup, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveCognitoIdentityPoolRoleAttachment,
		EdgeDecl{TypeCognitoIdentityPoolRoleAttachment, TypeCognitoIdentityPool, store.RelAttachedTo},
		EdgeDecl{TypeCognitoIdentityPoolRoleAttachment, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveCognitoUserPoolRefs,
		EdgeDecl{TypeCognitoUserPool, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeCognitoUserPool, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeCognitoUserPool, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCognitoUserPool, TypeSESEmailIdentity, store.RelUses},
		EdgeDecl{TypeCognitoUserPool, TypeACMCertificate, store.RelUses},
	)
}

// cognitoUserPoolARNFromChild extracts the parent user-pool ARN from a child
// resource's NativeID of shape `arn:aws:cognito-idp:r:a:userpool/{poolID}/<kind>/<id>`.
// Returns "" if no userpool/{id}/... segment is present.
func cognitoUserPoolARNFromChild(arn string) string {
	const prefix = "userpool/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// resolveCognitoUserPoolChildren links each per-pool sub-resource (domain,
// group, identity-provider, resource-server, risk-config, ui-customization,
// log-delivery, terms, managed-login-branding) to its parent user-pool by
// parsing the NativeID's `userpool/{poolID}/...` segment.
func resolveCognitoUserPoolChildren(acct *account, st *store.Store) error {
	childTypes := []string{
		TypeCognitoUserPoolDomain,
		TypeCognitoUserPoolGroup,
		TypeCognitoUserPoolIdentityProvider,
		TypeCognitoUserPoolResourceServer,
		TypeCognitoUserPoolRiskConfigurationAttachment,
		TypeCognitoUserPoolUICustomizationAttachment,
		TypeCognitoLogDeliveryConfiguration,
		TypeCognitoTerms,
		TypeCognitoManagedLoginBranding,
	}
	poolSet, err := scannedIDSet(acct, st, TypeCognitoUserPool)
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
			parent := cognitoUserPoolARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, parent)
			if !poolSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito %s→user-pool: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveCognitoUserPoolGroupRole links each user-pool-group to its IAM role
// (RoleArn). FK-safe.
func resolveCognitoUserPoolGroupRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoUserPoolGroup},
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
	for _, r := range rows {
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.RoleArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert cognito-group→role: %w", err)
		}
	}
	return nil
}

// cogLambdaSubArn is the nested {LambdaArn} shape used by sender +
// pre-token-generation configs under LambdaConfig.
type cogLambdaSubArn struct {
	LambdaArn *string `json:"LambdaArn"`
}

// cogUserPoolAttrs mirrors the user-pool Describe fields the resolver walks.
type cogUserPoolAttrs struct {
	LambdaConfig *struct {
		CreateAuthChallenge         *string          `json:"CreateAuthChallenge"`
		CustomMessage               *string          `json:"CustomMessage"`
		DefineAuthChallenge         *string          `json:"DefineAuthChallenge"`
		PostAuthentication          *string          `json:"PostAuthentication"`
		PostConfirmation            *string          `json:"PostConfirmation"`
		PreAuthentication           *string          `json:"PreAuthentication"`
		PreSignUp                   *string          `json:"PreSignUp"`
		PreTokenGeneration          *string          `json:"PreTokenGeneration"`
		UserMigration               *string          `json:"UserMigration"`
		VerifyAuthChallengeResponse *string          `json:"VerifyAuthChallengeResponse"`
		CustomEmailSender           *cogLambdaSubArn `json:"CustomEmailSender"`
		CustomSMSSender             *cogLambdaSubArn `json:"CustomSMSSender"`
		InboundFederation           *cogLambdaSubArn `json:"InboundFederation"`
		PreTokenGenerationConfig    *cogLambdaSubArn `json:"PreTokenGenerationConfig"`
		KMSKeyID                    *string          `json:"KMSKeyID"`
	} `json:"LambdaConfig"`
	EmailConfiguration *struct {
		SourceArn *string `json:"SourceArn"`
	} `json:"EmailConfiguration"`
	SmsConfiguration *struct {
		SnsCallerArn *string `json:"SnsCallerArn"`
	} `json:"SmsConfiguration"`
	CustomDomainConfig *struct {
		CertificateArn *string `json:"CertificateArn"`
	} `json:"CustomDomainConfig"`
}

// cogUserPoolTargetSets bundles FK-safe id sets for the resolver helpers.
type cogUserPoolTargetSets struct {
	lamSet, roleSet, sesSet, acmSet map[string]bool
	kmsIdx                          *kmsResolveIndex
}

// resolveCognitoUserPoolRefs walks a user-pool's Describe body for outbound
// refs: Lambda triggers (LambdaConfig.* — bare ARN strings + nested *.LambdaArn
// for sender + pre-token configs), KMS key (LambdaConfig.KMSKeyID),
// SES source identity (EmailConfiguration.SourceArn), SNS caller IAM role
// (SmsConfiguration.SnsCallerArn), ACM cert (CustomDomainConfig.CertificateArn).
// All edges FK-safe.
func resolveCognitoUserPoolRefs(acct *account, st *store.Store) error {
	pools, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoUserPool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pools) == 0 {
		return nil
	}
	sets, err := loadCogUserPoolTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, p := range pools {
		var attrs cogUserPoolAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(p.Region)
		if err := emitCogLambdaConfigEdges(st, acct, p, region, attrs, sets); err != nil {
			return err
		}
		if err := emitCogEmailEdge(st, acct, p, attrs, sets); err != nil {
			return err
		}
		if err := emitCogSmsEdge(st, acct, p, attrs, sets); err != nil {
			return err
		}
		if err := emitCogCustomDomainEdge(st, acct, p, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadCogUserPoolTargetSets(acct *account, st *store.Store) (cogUserPoolTargetSets, error) {
	var sets cogUserPoolTargetSets
	var err error
	if sets.lamSet, err = scannedIDSet(acct, st, TypeLambdaFunction); err != nil {
		return sets, err
	}
	if sets.roleSet, err = scannedIDSet(acct, st, TypeIAMRole); err != nil {
		return sets, err
	}
	if sets.sesSet, err = scannedIDSet(acct, st, TypeSESEmailIdentity); err != nil {
		return sets, err
	}
	if sets.acmSet, err = scannedIDSet(acct, st, TypeACMCertificate); err != nil {
		return sets, err
	}
	if sets.kmsIdx, err = loadKMSResolveIndex(acct, st); err != nil {
		return sets, err
	}
	return sets, nil
}

// emitCogLambdaConfigEdges walks LambdaConfig.* (bare ARNs + nested sender
// LambdaArn) and emits user-pool → lambda edges, dedup'd by ARN. Also
// emits the KMS edge when LambdaConfig.KMSKeyID is set.
func emitCogLambdaConfigEdges(st *store.Store, acct *account, p store.Resource, region string, attrs cogUserPoolAttrs, sets cogUserPoolTargetSets) error {
	lc := attrs.LambdaConfig
	if lc == nil {
		return nil
	}
	seen := map[string]bool{}
	emit := func(arn string) error {
		if arn == "" || seen[arn] {
			return nil
		}
		seen[arn] = true
		tgt := store.ResourceID("aws", acct.ID, TypeLambdaFunction, arn)
		if !sets.lamSet[tgt] {
			return nil
		}
		if err := st.UpsertRelationship(p.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cognito user-pool→lambda: %w", err)
		}
		return nil
	}
	for _, s := range []*string{
		lc.CreateAuthChallenge, lc.CustomMessage, lc.DefineAuthChallenge,
		lc.PostAuthentication, lc.PostConfirmation, lc.PreAuthentication,
		lc.PreSignUp, lc.PreTokenGeneration, lc.UserMigration,
		lc.VerifyAuthChallengeResponse,
	} {
		if err := emit(sv(s)); err != nil {
			return err
		}
	}
	for _, sub := range []*cogLambdaSubArn{lc.CustomEmailSender, lc.CustomSMSSender, lc.InboundFederation, lc.PreTokenGenerationConfig} {
		if sub == nil {
			continue
		}
		if err := emit(sv(sub.LambdaArn)); err != nil {
			return err
		}
	}
	if k := sv(lc.KMSKeyID); k != "" {
		if keyID, ok := sets.kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
			if err := st.UpsertRelationship(p.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito user-pool→kms: %w", err)
			}
		}
	}
	return nil
}

func emitCogEmailEdge(st *store.Store, acct *account, p store.Resource, attrs cogUserPoolAttrs, sets cogUserPoolTargetSets) error {
	if attrs.EmailConfiguration == nil {
		return nil
	}
	arn := sv(attrs.EmailConfiguration.SourceArn)
	if arn == "" {
		return nil
	}
	tgt := store.ResourceID("aws", acct.ID, TypeSESEmailIdentity, arn)
	if !sets.sesSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(p.ID, tgt, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert cognito user-pool→ses: %w", err)
	}
	return nil
}

func emitCogSmsEdge(st *store.Store, acct *account, p store.Resource, attrs cogUserPoolAttrs, sets cogUserPoolTargetSets) error {
	if attrs.SmsConfiguration == nil {
		return nil
	}
	arn := sv(attrs.SmsConfiguration.SnsCallerArn)
	if arn == "" {
		return nil
	}
	tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
	if !sets.roleSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(p.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
		return fmt.Errorf("upsert cognito user-pool→sns-role: %w", err)
	}
	return nil
}

func emitCogCustomDomainEdge(st *store.Store, acct *account, p store.Resource, attrs cogUserPoolAttrs, sets cogUserPoolTargetSets) error {
	if attrs.CustomDomainConfig == nil {
		return nil
	}
	arn := sv(attrs.CustomDomainConfig.CertificateArn)
	if arn == "" {
		return nil
	}
	tgt := store.ResourceID("aws", acct.ID, TypeACMCertificate, arn)
	if !sets.acmSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(p.ID, tgt, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert cognito user-pool→acm: %w", err)
	}
	return nil
}

// resolveCognitoIdentityPoolRoleAttachment links each identity-pool-role-
// attachment to its parent identity-pool (via NativeID parse) and to the
// IAM roles named under `Roles` map values (auth/unauth role ARNs).
func resolveCognitoIdentityPoolRoleAttachment(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoIdentityPoolRoleAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	poolSet, err := scannedIDSet(acct, st, TypeCognitoIdentityPool)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// Parent identity-pool: strip trailing "/roleattachment".
		parent := strings.TrimSuffix(r.NativeID, "/roleattachment")
		if parent != r.NativeID {
			tgtID := store.ResourceID("aws", acct.ID, TypeCognitoIdentityPool, parent)
			if poolSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cognito-id-role-attach→identity-pool: %w", err)
				}
			}
		}
		var attrs struct {
			Roles map[string]string `json:"Roles"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, roleARN := range attrs.Roles {
			if roleARN == "" || seen[roleARN] {
				continue
			}
			seen[roleARN] = true
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if !roleSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito-id-role-attach→role: %w", err)
			}
		}
	}
	return nil
}

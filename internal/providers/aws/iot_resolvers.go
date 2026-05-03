package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveIoTThingRefs,
		EdgeDecl{TypeIoTThing, TypeIoTThingType, store.RelAttachedTo},
		EdgeDecl{TypeIoTThing, TypeIoTBillingGroup, store.RelAttachedTo},
	)
	registerResolver(resolveIoTThingGroupParent,
		EdgeDecl{TypeIoTThingGroup, TypeIoTThingGroup, store.RelContains},
	)
	registerResolver(resolveIoTAuthorizerLambda,
		EdgeDecl{TypeIoTAuthorizer, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(resolveIoTRoleAliasRole,
		EdgeDecl{TypeIoTRoleAlias, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(resolveIoTProvisioningTemplateRole,
		EdgeDecl{TypeIoTProvisioningTemplate, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(resolveIoTDomainConfigAuthorizer,
		EdgeDecl{TypeIoTDomainConfiguration, TypeIoTAuthorizer, store.RelUses},
	)
	registerResolver(resolveIoTPolicyPrincipalAttachmentRefs,
		EdgeDecl{TypeIoTPolicyPrincipalAttachment, TypeIoTPolicy, store.RelAttachedTo},
		EdgeDecl{TypeIoTPolicyPrincipalAttachment, TypeIoTCertificate, store.RelAttachedTo},
	)
	registerResolver(resolveIoTThingPrincipalAttachmentRefs,
		EdgeDecl{TypeIoTThingPrincipalAttachment, TypeIoTThing, store.RelAttachedTo},
		EdgeDecl{TypeIoTThingPrincipalAttachment, TypeIoTCertificate, store.RelAttachedTo},
	)
}

// iotARN builds a standard IoT-resource ARN: arn:aws:iot:{r}:{a}:{kind}/{id}.
func iotARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:iot:%s:%s:%s/%s", region, acctID, kind, id)
}

// resolveIoTThingRefs walks each Thing's ThingTypeName + BillingGroupName
// (top-level fields on DescribeThingOutput; SDK structs marshal as PascalCase)
// and emits attached-to edges to the corresponding ThingType / BillingGroup.
func resolveIoTThingRefs(acct *account, st *store.Store) error {
	things, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTThing},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	typeSet, err := scannedIDSet(acct, st, TypeIoTThingType)
	if err != nil {
		return err
	}
	bgSet, err := scannedIDSet(acct, st, TypeIoTBillingGroup)
	if err != nil {
		return err
	}
	for _, r := range things {
		var attrs struct {
			ThingTypeName    *string `json:"ThingTypeName"`
			BillingGroupName *string `json:"BillingGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if name := sv(attrs.ThingTypeName); name != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIoTThingType,
				iotARN(region, acct.ID, "thingtype", name))
			if typeSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-thing→thing-type: %w", err)
				}
			}
		}
		if name := sv(attrs.BillingGroupName); name != "" {
			// BillingGroup ARN: arn:aws:iot:{r}:{a}:billinggroup/{name}.
			tgtID := store.ResourceID("aws", acct.ID, TypeIoTBillingGroup,
				iotARN(region, acct.ID, "billinggroup", name))
			if bgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-thing→billing-group: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveIoTThingGroupParent walks each ThingGroup's ThingGroupMetadata
// .RootToParentThingGroups list. The last entry in that array is the
// immediate parent group; a missing entry means the group is a root.
// Emits parent → child contains via RecordHierarchyBatch (closure table).
func resolveIoTThingGroupParent(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTThingGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	groupSet, err := scannedIDSet(acct, st, TypeIoTThingGroup)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for _, r := range groups {
		var attrs struct {
			ThingGroupMetadata *struct {
				RootToParentThingGroups []struct {
					GroupName *string `json:"GroupName"`
					GroupArn  *string `json:"GroupArn"`
				} `json:"RootToParentThingGroups"`
			} `json:"ThingGroupMetadata"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ThingGroupMetadata == nil || len(attrs.ThingGroupMetadata.RootToParentThingGroups) == 0 {
			continue
		}
		// The last element is the immediate parent.
		parents := attrs.ThingGroupMetadata.RootToParentThingGroups
		parent := parents[len(parents)-1]
		parentARN := sv(parent.GroupArn)
		if parentARN == "" {
			continue
		}
		parentID := store.ResourceID("aws", acct.ID, TypeIoTThingGroup, parentARN)
		if !groupSet[parentID] {
			continue
		}
		// Avoid self-loops if the SDK ever returns the group itself.
		if parentID == r.ID {
			continue
		}
		pairs = append(pairs, [2]string{r.ID, parentID})
	}
	if len(pairs) == 0 {
		return nil
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return fmt.Errorf("record iot thing-group hierarchy: %w", err)
	}
	return nil
}

// resolveIoTAuthorizerLambda walks each Authorizer's AuthorizerFunctionArn.
// The scanner stores DescribeAuthorizerOutput; AuthorizerDescription is the
// nested struct holding the Lambda ARN.
func resolveIoTAuthorizerLambda(acct *account, st *store.Store) error {
	auths, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTAuthorizer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range auths {
		var attrs struct {
			AuthorizerDescription *struct {
				AuthorizerFunctionArn *string `json:"AuthorizerFunctionArn"`
			} `json:"AuthorizerDescription"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AuthorizerDescription == nil {
			continue
		}
		fnARN := sv(attrs.AuthorizerDescription.AuthorizerFunctionArn)
		if fnARN == "" {
			continue
		}
		// Strip trailing :version or :alias qualifier — Lambda function
		// rows are keyed on the unqualified ARN (7 colon-separated parts).
		// arn:aws:lambda:{r}:{a}:function:{name}[:{qual}]
		if parts := strings.Split(fnARN, ":"); len(parts) == 8 {
			fnARN = strings.Join(parts[:7], ":")
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if !fnSet[fnID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, fnID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-authorizer→lambda: %w", err)
		}
	}
	return nil
}

// resolveIoTRoleAliasRole walks each RoleAlias's RoleArn (IAM role ARN) and
// emits an `assumes` edge — IoT devices using the alias receive temporary
// credentials for the underlying role.
func resolveIoTRoleAliasRole(acct *account, st *store.Store) error {
	aliases, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTRoleAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range aliases {
		var attrs struct {
			RoleAliasDescription *struct {
				RoleArn *string `json:"RoleArn"`
			} `json:"RoleAliasDescription"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RoleAliasDescription == nil {
			continue
		}
		roleARN := sv(attrs.RoleAliasDescription.RoleArn)
		if roleARN == "" {
			continue
		}
		roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
		if !roleSet[roleID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-role-alias→iam-role: %w", err)
		}
	}
	return nil
}

// resolveIoTProvisioningTemplateRole walks each ProvisioningTemplate's
// ProvisioningRoleArn — the IAM role IoT assumes during fleet provisioning.
func resolveIoTProvisioningTemplateRole(acct *account, st *store.Store) error {
	templates, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTProvisioningTemplate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range templates {
		var attrs struct {
			ProvisioningRoleArn *string `json:"ProvisioningRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		roleARN := sv(attrs.ProvisioningRoleArn)
		if roleARN == "" {
			continue
		}
		roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
		if !roleSet[roleID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-provisioning-template→iam-role: %w", err)
		}
	}
	return nil
}

// resolveIoTDomainConfigAuthorizer walks each DomainConfiguration's
// AuthorizerConfig.DefaultAuthorizerName and emits a `uses` edge to the
// custom Authorizer that gates device connections on this domain.
func resolveIoTDomainConfigAuthorizer(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTDomainConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	authSet, err := scannedIDSet(acct, st, TypeIoTAuthorizer)
	if err != nil {
		return err
	}
	for _, r := range cfgs {
		var attrs struct {
			AuthorizerConfig *struct {
				DefaultAuthorizerName *string `json:"DefaultAuthorizerName"`
			} `json:"AuthorizerConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AuthorizerConfig == nil {
			continue
		}
		name := sv(attrs.AuthorizerConfig.DefaultAuthorizerName)
		if name == "" {
			continue
		}
		region := sv(r.Region)
		authARN := iotARN(region, acct.ID, "authorizer", name)
		authID := store.ResourceID("aws", acct.ID, TypeIoTAuthorizer, authARN)
		if !authSet[authID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, authID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-domain-config→authorizer: %w", err)
		}
	}
	return nil
}

// resolveIoTPolicyPrincipalAttachmentRefs links each policy-principal
// attachment to its parent Policy (by name) AND, when the principal is an
// IoT certificate ARN, to the corresponding Certificate resource. Cognito
// identity principals (no `:cert/` substring) skip the cert edge.
func resolveIoTPolicyPrincipalAttachmentRefs(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTPolicyPrincipalAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	policySet, err := scannedIDSet(acct, st, TypeIoTPolicy)
	if err != nil {
		return err
	}
	certSet, err := scannedIDSet(acct, st, TypeIoTCertificate)
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			PolicyName string `json:"PolicyName"`
			Principal  string `json:"Principal"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.PolicyName != "" {
			polARN := iotARN(region, acct.ID, "policy", attrs.PolicyName)
			polID := store.ResourceID("aws", acct.ID, TypeIoTPolicy, polARN)
			if policySet[polID] {
				if err := st.UpsertRelationship(r.ID, polID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-policy-principal→policy: %w", err)
				}
			}
		}
		// IoT certificate principal ARN shape:
		//   arn:aws:iot:{r}:{a}:cert/{certId}
		if strings.Contains(attrs.Principal, ":cert/") {
			certID := store.ResourceID("aws", acct.ID, TypeIoTCertificate, attrs.Principal)
			if certSet[certID] {
				if err := st.UpsertRelationship(r.ID, certID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-policy-principal→cert: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveIoTThingPrincipalAttachmentRefs links each thing-principal
// attachment to its parent Thing (by name) AND, when the principal is an
// IoT certificate ARN, to the corresponding Certificate resource.
func resolveIoTThingPrincipalAttachmentRefs(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTThingPrincipalAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	thingSet, err := scannedIDSet(acct, st, TypeIoTThing)
	if err != nil {
		return err
	}
	certSet, err := scannedIDSet(acct, st, TypeIoTCertificate)
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			ThingName string `json:"ThingName"`
			Principal string `json:"Principal"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.ThingName != "" {
			thingARN := iotARN(region, acct.ID, "thing", attrs.ThingName)
			thingID := store.ResourceID("aws", acct.ID, TypeIoTThing, thingARN)
			if thingSet[thingID] {
				if err := st.UpsertRelationship(r.ID, thingID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-thing-principal→thing: %w", err)
				}
			}
		}
		if strings.Contains(attrs.Principal, ":cert/") {
			certID := store.ResourceID("aws", acct.ID, TypeIoTCertificate, attrs.Principal)
			if certSet[certID] {
				if err := st.UpsertRelationship(r.ID, certID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-thing-principal→cert: %w", err)
				}
			}
		}
	}
	return nil
}

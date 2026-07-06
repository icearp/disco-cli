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
		resolveIoTThingRefs,
		EdgeDecl{TypeIoTThing, TypeIoTThingType, store.RelAttachedTo},
		EdgeDecl{TypeIoTThing, TypeIoTBillingGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveIoTThingGroupParent,
		EdgeDecl{TypeIoTThingGroup, TypeIoTThingGroup, store.RelContains},
	)
	registerResolver(
		resolveIoTAuthorizerLambda,
		EdgeDecl{TypeIoTAuthorizer, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		resolveIoTRoleAliasRole,
		EdgeDecl{TypeIoTRoleAlias, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveIoTProvisioningTemplateRole,
		EdgeDecl{TypeIoTProvisioningTemplate, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveIoTDomainConfigAuthorizer,
		EdgeDecl{TypeIoTDomainConfiguration, TypeIoTAuthorizer, store.RelUses},
	)
	registerResolver(
		resolveIoTPolicyPrincipalAttachmentRefs,
		EdgeDecl{TypeIoTPolicyPrincipalAttachment, TypeIoTPolicy, store.RelAttachedTo},
		EdgeDecl{TypeIoTPolicyPrincipalAttachment, TypeIoTCertificate, store.RelAttachedTo},
	)
	registerResolver(
		resolveIoTThingPrincipalAttachmentRefs,
		EdgeDecl{TypeIoTThingPrincipalAttachment, TypeIoTThing, store.RelAttachedTo},
		EdgeDecl{TypeIoTThingPrincipalAttachment, TypeIoTCertificate, store.RelAttachedTo},
	)
	registerResolver(
		resolveIoTTopicRuleActionRefs,
		EdgeDecl{TypeIoTTopicRule, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeFirehoseDeliveryStream, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeIoTTopicRule, TypeSFNStateMachine, store.RelUses},
	)
	registerResolver(
		resolveIoTMitigationActionRefs,
		EdgeDecl{TypeIoTMitigationAction, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIoTMitigationAction, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIoTMitigationAction, TypeIoTThingGroup, store.RelUses},
	)
	registerResolver(
		resolveIoTJobTemplateRole,
		EdgeDecl{TypeIoTJobTemplate, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveIoTAccountAuditConfigurationRefs,
		EdgeDecl{TypeIoTAccountAuditConfiguration, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIoTAccountAuditConfiguration, TypeSNSTopic, store.RelUses},
	)
}

// TypeSFNStateMachine alias declared in aws_types.go; referenced here to
// keep this file compiling against the canonical constant.

// stateMachineARNFromName builds a Step Functions state-machine ARN.
func stateMachineARNFromName(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:%s", region, acctID, name)
}

// kinesisStreamARNFromName builds a Kinesis stream ARN.
func kinesisStreamARNFromName(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", region, acctID, name)
}

// firehoseDeliveryStreamARNFromName builds a Firehose delivery-stream ARN.
func firehoseDeliveryStreamARNFromName(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", region, acctID, name)
}

// dynamodbTableARNFromName builds a DynamoDB table ARN.
func dynamodbTableARNFromName(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, acctID, name)
}

// s3BucketARNFromName builds the canonical bucket ARN.
func s3BucketARNFromName(name string) string {
	return "arn:aws:s3:::" + name
}

// sqsQueueARNFromURL parses an SQS queue URL — `https://sqs.{region}.amazonaws.com/{acct}/{name}`
// — into the canonical queue ARN. Returns "" if not a parseable URL.
func sqsQueueARNFromURL(u string) string {
	const prefix = "https://sqs."
	if !strings.HasPrefix(u, prefix) {
		return ""
	}
	rest := u[len(prefix):]
	region, after, ok := strings.Cut(rest, ".amazonaws.com/")
	if !ok || region == "" {
		return ""
	}
	acctID, name, ok := strings.Cut(after, "/")
	if !ok || acctID == "" || name == "" {
		return ""
	}
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, acctID, name)
}

// iotARN builds a standard IoT-resource ARN: arn:aws:iot:{r}:{a}:{kind}/{id}.
func iotARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:iot:%s:%s:%s/%s", region, acctID, kind, id)
}

// resolveIoTThingRefs walks each Thing's ThingTypeName + BillingGroupName
// (top-level DescribeThingOutput fields, PascalCase) and emits attached-to
// edges to the corresponding ThingType / BillingGroup.
func resolveIoTThingRefs(acct *account, st *store.Store) error {
	things, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTThing},
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

// resolveIoTThingGroupParent walks each ThingGroup's
// ThingGroupMetadata.RootToParentThingGroups; the last entry is the immediate
// parent (empty list = root group). Emits parent → child contains via
// RecordHierarchyBatch (closure table).
func resolveIoTThingGroupParent(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTThingGroup},
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

// resolveIoTAuthorizerLambda walks each Authorizer's AuthorizerFunctionArn,
// nested under AuthorizerDescription in the scanner's DescribeAuthorizerOutput.
func resolveIoTAuthorizerLambda(acct *account, st *store.Store) error {
	auths, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTAuthorizer},
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
		// Strip trailing :version/:alias qualifier — Lambda rows key on the
		// unqualified ARN (7 colon-separated parts):
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTRoleAlias},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTProvisioningTemplate},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTDomainConfiguration},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTPolicyPrincipalAttachment},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTThingPrincipalAttachment},
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

// iotTopicRuleAction mirrors the SDK Action union — every supported action
// type, plus the per-action role/target field. PascalCase JSON tags match the
// `mustJSON(GetTopicRuleOutput)` shape: `{"Rule":{"Actions":[{"Lambda":{...}}]}}`.
type iotTopicRuleAction struct {
	Lambda *struct {
		FunctionArn *string `json:"FunctionArn"`
	} `json:"Lambda"`
	Sns *struct {
		TargetArn *string `json:"TargetArn"`
		RoleArn   *string `json:"RoleArn"`
	} `json:"Sns"`
	Sqs *struct {
		QueueURL *string `json:"QueueUrl"`
		RoleArn  *string `json:"RoleArn"`
	} `json:"Sqs"`
	Kinesis *struct {
		StreamName *string `json:"StreamName"`
		RoleArn    *string `json:"RoleArn"`
	} `json:"Kinesis"`
	Firehose *struct {
		DeliveryStreamName *string `json:"DeliveryStreamName"`
		RoleArn            *string `json:"RoleArn"`
	} `json:"Firehose"`
	DynamoDB *struct {
		TableName *string `json:"TableName"`
		RoleArn   *string `json:"RoleArn"`
	} `json:"DynamoDB"`
	DynamoDBv2 *struct {
		PutItem *struct {
			TableName *string `json:"TableName"`
		} `json:"PutItem"`
		RoleArn *string `json:"RoleArn"`
	} `json:"DynamoDBv2"`
	S3 *struct {
		BucketName *string `json:"BucketName"`
		RoleArn    *string `json:"RoleArn"`
	} `json:"S3"`
	StepFunctions *struct {
		StateMachineName *string `json:"StateMachineName"`
		RoleArn          *string `json:"RoleArn"`
	} `json:"StepFunctions"`
	CloudwatchAlarm *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"CloudwatchAlarm"`
	CloudwatchLogs *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"CloudwatchLogs"`
	CloudwatchMetric *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"CloudwatchMetric"`
	IotAnalytics *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"IotAnalytics"`
	IotEvents *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"IotEvents"`
	IotSiteWise *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"IotSiteWise"`
	Republish *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"Republish"`
	Salesforce *struct{} `json:"Salesforce"`
	OpenSearch *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"OpenSearch"`
	Elasticsearch *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"Elasticsearch"`
	Kafka *struct { /* no role */
	} `json:"Kafka"`
	Timestream *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"Timestream"`
	Location *struct {
		RoleArn *string `json:"RoleArn"`
	} `json:"Location"`
	HTTP *struct{} `json:"Http"`
}

// iotTopicRuleTargetSets bundles all FK-safe id sets in one struct so the
// per-action helper signature stays tight. Mirrors the openSearchTargetSets
// pattern (providers/CLAUDE.md).
type iotTopicRuleTargetSets struct {
	role     map[string]bool
	lambda   map[string]bool
	sns      map[string]bool
	sqs      map[string]bool
	kinesis  map[string]bool
	firehose map[string]bool
	dynamodb map[string]bool
	s3       map[string]bool
	sfn      map[string]bool
}

// resolveIoTTopicRuleActionRefs walks each topic rule's Actions[] (+ ErrorAction)
// and emits an edge per typed-action target field. Tolerates absent action
// types — only populated branches emit edges.
func resolveIoTTopicRuleActionRefs(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTTopicRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	sets := iotTopicRuleTargetSets{}
	for _, p := range []struct {
		t   string
		dst *map[string]bool
	}{
		{TypeIAMRole, &sets.role},
		{TypeLambdaFunction, &sets.lambda},
		{TypeSNSTopic, &sets.sns},
		{TypeSQSQueue, &sets.sqs},
		{TypeKinesisStream, &sets.kinesis},
		{TypeFirehoseDeliveryStream, &sets.firehose},
		{TypeDynamoDBTable, &sets.dynamodb},
		{TypeS3Bucket, &sets.s3},
		{TypeSFNStateMachine, &sets.sfn},
	} {
		s, err := scannedIDSet(acct, st, p.t)
		if err != nil {
			return err
		}
		*p.dst = s
	}
	for _, r := range rules {
		var attrs struct {
			Rule struct {
				Actions     []iotTopicRuleAction `json:"Actions"`
				ErrorAction *iotTopicRuleAction  `json:"ErrorAction"`
			} `json:"Rule"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		actions := append([]iotTopicRuleAction{}, attrs.Rule.Actions...)
		if attrs.Rule.ErrorAction != nil {
			actions = append(actions, *attrs.Rule.ErrorAction)
		}
		for _, a := range actions {
			if err := emitIoTTopicRuleActionEdges(st, &r, a, sets, region, acct.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitIoTTopicRuleActionEdges fans out one Action entry to its typed target
// (and IAM role, when the typed action carries `RoleArn`).
func emitIoTTopicRuleActionEdges(st *store.Store, r *store.Resource, a iotTopicRuleAction, sets iotTopicRuleTargetSets, region, acctID string) error {
	roleARNs := iotCollectActionRoles(a)
	for _, ra := range roleARNs {
		if ra == "" {
			continue
		}
		rid := store.ResourceID("aws", acctID, TypeIAMRole, ra)
		if !sets.role[rid] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, rid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-topic-rule→iam-role: %w", err)
		}
	}
	// Typed targets dispatch.
	type emit struct {
		ok   bool
		typ  string
		set  map[string]bool
		arn  string
		desc string
	}
	var emits []emit
	if a.Lambda != nil {
		emits = append(emits, emit{true, TypeLambdaFunction, sets.lambda, sv(a.Lambda.FunctionArn), "lambda"})
	}
	if a.Sns != nil {
		emits = append(emits, emit{true, TypeSNSTopic, sets.sns, sv(a.Sns.TargetArn), "sns"})
	}
	if a.Sqs != nil {
		emits = append(emits, emit{true, TypeSQSQueue, sets.sqs, sqsQueueARNFromURL(sv(a.Sqs.QueueURL)), "sqs"})
	}
	if a.Kinesis != nil && sv(a.Kinesis.StreamName) != "" {
		emits = append(emits, emit{
			true, TypeKinesisStream, sets.kinesis,
			kinesisStreamARNFromName(region, acctID, sv(a.Kinesis.StreamName)), "kinesis",
		})
	}
	if a.Firehose != nil && sv(a.Firehose.DeliveryStreamName) != "" {
		emits = append(emits, emit{
			true, TypeFirehoseDeliveryStream, sets.firehose,
			firehoseDeliveryStreamARNFromName(region, acctID, sv(a.Firehose.DeliveryStreamName)), "firehose",
		})
	}
	if a.DynamoDB != nil && sv(a.DynamoDB.TableName) != "" {
		emits = append(emits, emit{
			true, TypeDynamoDBTable, sets.dynamodb,
			dynamodbTableARNFromName(region, acctID, sv(a.DynamoDB.TableName)), "dynamodb",
		})
	}
	if a.DynamoDBv2 != nil && a.DynamoDBv2.PutItem != nil && sv(a.DynamoDBv2.PutItem.TableName) != "" {
		emits = append(emits, emit{
			true, TypeDynamoDBTable, sets.dynamodb,
			dynamodbTableARNFromName(region, acctID, sv(a.DynamoDBv2.PutItem.TableName)), "dynamodb-v2",
		})
	}
	if a.S3 != nil && sv(a.S3.BucketName) != "" {
		emits = append(emits, emit{true, TypeS3Bucket, sets.s3, s3BucketARNFromName(sv(a.S3.BucketName)), "s3"})
	}
	if a.StepFunctions != nil && sv(a.StepFunctions.StateMachineName) != "" {
		emits = append(emits, emit{
			true, TypeSFNStateMachine, sets.sfn,
			stateMachineARNFromName(region, acctID, sv(a.StepFunctions.StateMachineName)), "step-functions",
		})
	}
	for _, e := range emits {
		if !e.ok || e.arn == "" {
			continue
		}
		tid := store.ResourceID("aws", acctID, e.typ, e.arn)
		if !e.set[tid] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-topic-rule→%s: %w", e.desc, err)
		}
	}
	return nil
}

// iotCollectActionRoles flattens RoleArn from every typed action (only the
// non-nil branch carries a value).
func iotCollectActionRoles(a iotTopicRuleAction) []string {
	var out []string
	add := func(p *string) {
		if p != nil && *p != "" {
			out = append(out, *p)
		}
	}
	if a.Sns != nil {
		add(a.Sns.RoleArn)
	}
	if a.Sqs != nil {
		add(a.Sqs.RoleArn)
	}
	if a.Kinesis != nil {
		add(a.Kinesis.RoleArn)
	}
	if a.Firehose != nil {
		add(a.Firehose.RoleArn)
	}
	if a.DynamoDB != nil {
		add(a.DynamoDB.RoleArn)
	}
	if a.DynamoDBv2 != nil {
		add(a.DynamoDBv2.RoleArn)
	}
	if a.S3 != nil {
		add(a.S3.RoleArn)
	}
	if a.StepFunctions != nil {
		add(a.StepFunctions.RoleArn)
	}
	if a.CloudwatchAlarm != nil {
		add(a.CloudwatchAlarm.RoleArn)
	}
	if a.CloudwatchLogs != nil {
		add(a.CloudwatchLogs.RoleArn)
	}
	if a.CloudwatchMetric != nil {
		add(a.CloudwatchMetric.RoleArn)
	}
	if a.IotAnalytics != nil {
		add(a.IotAnalytics.RoleArn)
	}
	if a.IotEvents != nil {
		add(a.IotEvents.RoleArn)
	}
	if a.IotSiteWise != nil {
		add(a.IotSiteWise.RoleArn)
	}
	if a.Republish != nil {
		add(a.Republish.RoleArn)
	}
	if a.OpenSearch != nil {
		add(a.OpenSearch.RoleArn)
	}
	if a.Elasticsearch != nil {
		add(a.Elasticsearch.RoleArn)
	}
	if a.Timestream != nil {
		add(a.Timestream.RoleArn)
	}
	if a.Location != nil {
		add(a.Location.RoleArn)
	}
	return out
}

// resolveIoTMitigationActionRefs walks each action's RoleArn (always present)
// and per-type ActionParams: PublishFindingToSns.TopicArn → SNS topic;
// AddThingsToThingGroup.ThingGroupNames[] → IoT thing-groups;
// EnableIoTLogging.RoleArnForLogging → IAM role.
func resolveIoTMitigationActionRefs(acct *account, st *store.Store) error {
	actions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTMitigationAction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	tgSet, err := scannedIDSet(acct, st, TypeIoTThingGroup)
	if err != nil {
		return err
	}
	for _, r := range actions {
		var attrs struct {
			RoleArn      *string `json:"RoleArn"`
			ActionParams *struct {
				AddThingsToThingGroupParams *struct {
					ThingGroupNames []string `json:"ThingGroupNames"`
				} `json:"AddThingsToThingGroupParams"`
				EnableIoTLoggingParams *struct {
					RoleArnForLogging *string `json:"RoleArnForLogging"`
				} `json:"EnableIoTLoggingParams"`
				PublishFindingToSnsParams *struct {
					TopicArn *string `json:"TopicArn"`
				} `json:"PublishFindingToSnsParams"`
			} `json:"ActionParams"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Action RoleArn — applied by IoT to perform the mitigation.
		if roleARN := sv(attrs.RoleArn); roleARN != "" {
			rid := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if roleSet[rid] {
				if err := st.UpsertRelationship(r.ID, rid, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-mitigation-action→iam-role: %w", err)
				}
			}
		}
		if attrs.ActionParams == nil {
			continue
		}
		if p := attrs.ActionParams.EnableIoTLoggingParams; p != nil {
			if roleARN := sv(p.RoleArnForLogging); roleARN != "" {
				rid := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
				if roleSet[rid] {
					if err := st.UpsertRelationship(r.ID, rid, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert iot-mitigation-action→logging-role: %w", err)
					}
				}
			}
		}
		if p := attrs.ActionParams.PublishFindingToSnsParams; p != nil {
			if topicARN := sv(p.TopicArn); topicARN != "" {
				tid := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
				if snsSet[tid] {
					if err := st.UpsertRelationship(r.ID, tid, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert iot-mitigation-action→sns: %w", err)
					}
				}
			}
		}
		if p := attrs.ActionParams.AddThingsToThingGroupParams; p != nil {
			for _, gn := range p.ThingGroupNames {
				if gn == "" {
					continue
				}
				tgARN := iotARN(region, acct.ID, "thinggroup", gn)
				tid := store.ResourceID("aws", acct.ID, TypeIoTThingGroup, tgARN)
				if !tgSet[tid] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tid, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-mitigation-action→thing-group: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveIoTJobTemplateRole walks each JobTemplate's PresignedURLConfig.RoleArn
// — IAM role IoT assumes to mint pre-signed S3 URLs for the job document.
func resolveIoTJobTemplateRole(acct *account, st *store.Store) error {
	tpls, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTJobTemplate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range tpls {
		var attrs struct {
			PresignedURLConfig *struct {
				RoleArn *string `json:"RoleArn"`
			} `json:"PresignedUrlConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PresignedURLConfig == nil {
			continue
		}
		roleARN := sv(attrs.PresignedURLConfig.RoleArn)
		if roleARN == "" {
			continue
		}
		rid := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
		if !roleSet[rid] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, rid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-job-template→iam-role: %w", err)
		}
	}
	return nil
}

// resolveIoTAccountAuditConfigurationRefs walks the per-region audit config's
// top-level RoleArn (the role IoT assumes when running audits) and every
// AuditNotificationTargetConfigurations entry's TargetArn (SNS topic) +
// RoleArn.
func resolveIoTAccountAuditConfigurationRefs(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTAccountAuditConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range cfgs {
		var attrs struct {
			RoleArn                               *string `json:"RoleArn"`
			AuditNotificationTargetConfigurations map[string]struct {
				TargetArn *string `json:"TargetArn"`
				RoleArn   *string `json:"RoleArn"`
				Enabled   *bool   `json:"Enabled"`
			} `json:"AuditNotificationTargetConfigurations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		emitRole := func(roleARN string) error {
			if roleARN == "" {
				return nil
			}
			rid := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if !roleSet[rid] {
				return nil
			}
			if err := st.UpsertRelationship(r.ID, rid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert iot-audit-config→iam-role: %w", err)
			}
			return nil
		}
		if err := emitRole(sv(attrs.RoleArn)); err != nil {
			return err
		}
		for _, t := range attrs.AuditNotificationTargetConfigurations {
			if err := emitRole(sv(t.RoleArn)); err != nil {
				return err
			}
			if topicARN := sv(t.TargetArn); topicARN != "" && strings.HasPrefix(topicARN, "arn:aws:sns:") {
				tid := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
				if !snsSet[tid] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tid, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot-audit-config→sns: %w", err)
				}
			}
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveIoTCertificateCA,
		EdgeDecl{TypeIoTCertificate, TypeIoTCACertificate, store.RelAttachedTo},
	)
}

// resolveIoTCertificateCA wires each device certificate to its issuing CA
// via CertificateDescription.CaCertificateId. CA cert NativeID is its full
// ARN, so build a (region, ID) → resource-id index from scanned CA rows,
// since CertificateId alone doesn't yield the ARN shape.
func resolveIoTCertificateCA(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTCertificate}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	caRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTCACertificate}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// Index CA certs by (region, certificate-id); fall back to bare-id index.
	caByRegionID := make(map[string]string, len(caRows))
	for _, c := range caRows {
		var caAttrs struct {
			CertificateDescription *struct {
				CertificateID *string `json:"CertificateId"`
			} `json:"CertificateDescription"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &caAttrs); err != nil {
			continue
		}
		if caAttrs.CertificateDescription == nil {
			continue
		}
		id := sv(caAttrs.CertificateDescription.CertificateID)
		if id == "" {
			continue
		}
		caByRegionID[sv(c.Region)+"\x00"+id] = c.ID
	}
	for _, r := range rows {
		var attrs struct {
			CertificateDescription *struct {
				CaCertificateID *string `json:"CaCertificateId"`
			} `json:"CertificateDescription"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CertificateDescription == nil {
			continue
		}
		caID := sv(attrs.CertificateDescription.CaCertificateID)
		if caID == "" {
			continue
		}
		tgt, ok := caByRegionID[sv(r.Region)+"\x00"+caID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert iot-cert→ca-cert: %w", err)
		}
	}
	return nil
}

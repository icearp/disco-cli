package aws

// leafTypes is the central registry of disco types whose scanner upserts
// rows but which have no outbound refs to other scanned resources. These
// are excluded from `disco coverage --missing-resolvers` so the orphan
// inventory only contains genuinely wire-able candidates.
//
// Adding a type here is equivalent to declaring "no resolver will ever
// emit edges from this type". If an out-edge is later identified, drop
// the entry and ship the resolver.
//
// Categories represented (in order below):
//  1. Account/region singleton configs — `*:account`, `*:account-summary`,
//     `*:account-password-policy`, `*:credential-report`, `*:vdm-attributes`,
//     `*:notification-hub`, etc.
//  2. Public-access / encryption / replication / registry singletons.
//  3. Catalog leaves — fraud-detector primitives, Bedrock guardrail/router/
//     intelligent-prompt-router, gamelift build/script catalog rows.
//  4. SES Mail Manager + IoTWireless types whose Get/Describe ops add no
//     useful cross-refs (per Tier C1.4 verify outcome).
//  5. Long-tail singletons: notification-rule, support-app account-alias,
//     uxc account-customization, etc.
//
// Precedent shape mirrors `nonRDSEngines` in `rds_scanners.go` (centralised
// per-package set) and the `internal/providers/CLAUDE.md` "managed-by-
// provider" pattern, except this set is consumed at coverage-render time
// not scan time.
var leafTypes = map[string]bool{
	// Account/region singleton configs.
	"aws:acm:account":                          true,
	"aws:apigateway:account":                   true,
	"aws:iam:account-summary":                  true,
	"aws:iam:account-password-policy":          true,
	"aws:iam:credential-report":                true,
	"aws:iam:service-last-accessed-detail":     true,
	"aws:logs:account-policy":                  true,
	"aws:ses:vdm-attributes":                   true,
	"aws:support-app:account-alias":            true,
	"aws:uxc:account-customization":            true,
	"aws:notifications:notification-hub":       true,
	"aws:notifications-contacts:email-contact": true,
	// Public-access, encryption, replication, registry singletons.
	"aws:ec2:snapshot-block-public-access":            true,
	"aws:ec2:vpc-block-public-access-exclusion":       true,
	"aws:ec2:vpc-block-public-access-options":         true,
	"aws:ec2:vpc-encryption-control":                  true,
	"aws:ec2:capacity-manager-data-export":            true,
	"aws:ec2:network-performance-metric-subscription": true,
	"aws:ec2:network-insights-access-scope":           true,
	"aws:ec2:traffic-mirror-filter":                   true,
	"aws:ec2:vpn-concentrator":                        true,
	"aws:ec2:dhcp-options":                            true,
	"aws:ec2:host":                                    true,
	"aws:ec2:placement-group":                         true,
	"aws:ec2:customer-gateway":                        true,
	"aws:ec2:prefix-list":                             true,
	"aws:ec2:egress-only-internet-gateway":            true,
	"aws:ec2:vpc":                                     true,
	"aws:ec2:key-pair":                                true,
	"aws:ec2:verified-access-trust-provider":          true,
	"aws:ecr:public-repository":                       true,
	"aws:ecr:pull-through-cache-rule":                 true,
	"aws:ecr:pull-time-update-exclusion":              true,
	"aws:ecr:registry-policy":                         true,
	"aws:ecr:registry-scanning-configuration":         true,
	"aws:ecr:replication-configuration":               true,
	"aws:ecr:repository-creation-template":            true,
	"aws:ecr:signing-configuration":                   true,
	"aws:event-schemas:registry-policy":               true,
	"aws:iot:encryption-configuration":                true,
	"aws:iot:logging":                                 true,
	"aws:iot:resource-specific-logging":               true,
	"aws:iot:scheduled-audit":                         true,
	"aws:iot:billing-group":                           true,
	"aws:iot:thing-type":                              true,
	"aws:iot:certificate":                             true,
	"aws:iot:ca-certificate":                          true,
	"aws:iot:certificate-provider":                    true,
	"aws:iot:command":                                 true,
	"aws:iot:dimension":                               true,
	"aws:iot:policy":                                  true,
	// CloudWatch dashboard / anomaly-detector / mute-rule / insight-rule /
	// metric-stream — only metric-stream has out-edges (already wired);
	// the rest are pure config singletons.
	"aws:cloudwatch:dashboard":        true,
	"aws:cloudwatch:alarm-mute-rule":  true,
	"aws:cloudwatch:anomaly-detector": true,
	"aws:cloudwatch:insight-rule":     true,
	"aws:cloudwatch:otel-enrichment":  true,
	// Macie account singletons.
	"aws:macie:session":                true,
	"aws:macie:custom-data-identifier": true,
	"aws:macie:findings-filter":        true,
	// Athena / S3 leaves.
	"aws:athena:capacity-reservation":    true,
	"aws:s3:account-public-access-block": true,
	// Bedrock catalog leaves (guardrail body, router, inference-profile
	// — config-only, no out-refs to scanned types).
	"aws:bedrock:guardrail":                            true,
	"aws:bedrock:application-inference-profile":        true,
	"aws:bedrock:enforced-guardrail-configuration":     true,
	"aws:bedrock:intelligent-prompt-router":            true,
	"aws:bedrock:prompt":                               true,
	"aws:bedrock:flow":                                 true,
	"aws:bedrock:automated-reasoning-policy":           true,
	"aws:bedrockagentcore:workload-identity":           true,
	"aws:bedrockagentcore:api-key-credential-provider": true,
	"aws:bedrockagentcore:oauth2-credential-provider":  true,
	"aws:bedrockagentcore:browser-custom":              true,
	"aws:bedrockagentcore:code-interpreter-custom":     true,
	"aws:bedrockagentcore:gateway":                     true,
	"aws:bedrockagentcore:memory":                      true,
	"aws:bedrockagentcore:policy-engine":               true,
	"aws:bedrockagentcore:runtime":                     true,
	"aws:bedrockagentcore:browser-profile":             true,
	"aws:bedrockagentcore:evaluator":                   true,
	"aws:bedrockagentcore:online-evaluation-config":    true,
	// FraudDetector primitive catalog (entity-type, label, outcome, list,
	// variable) — pure leaves, edges flow inward via event-type.
	"aws:frauddetector:entity-type": true,
	"aws:frauddetector:label":       true,
	"aws:frauddetector:list":        true,
	"aws:frauddetector:outcome":     true,
	"aws:frauddetector:variable":    true,
	// Cases domain is parent of children; children resolve to it but it
	// has no outbound refs.
	"aws:cases:domain": true,
	// Detective graph parent + member already wired (member→org); graph
	// itself has no outbound refs.
	"aws:detective:graph": true,
	// GameLift catalog (build/script/matchmaking-rule-set/location are
	// summary-only; container-group-definition refs ECR but image dispatch
	// not in scope).
	"aws:gamelift:build":                      true,
	"aws:gamelift:script":                     true,
	"aws:gamelift:location":                   true,
	"aws:gamelift:matchmaking-rule-set":       true,
	"aws:gamelift:container-group-definition": true,
	// Panorama, ground-station summaries (List-only, Tier C1 deferred).
	"aws:panorama:application-instance":          true,
	"aws:panorama:package":                       true,
	"aws:ground-station:config":                  true,
	"aws:ground-station:dataflow-endpoint-group": true,
	"aws:ground-station:mission-profile":         true,
	// IoTWireless — per Tier C1.4 verify, Get ops add no scanned-type
	// cross-refs for these 9 subtypes.
	"aws:iotwireless:device-profile":                 true,
	"aws:iotwireless:service-profile":                true,
	"aws:iotwireless:fuota-task":                     true,
	"aws:iotwireless:multicast-group":                true,
	"aws:iotwireless:network-analyzer-configuration": true,
	"aws:iotwireless:partner-account":                true,
	"aws:iotwireless:task-definition":                true,
	"aws:iotwireless:wireless-gateway":               true,
	"aws:iotwireless:wireless-device-import-task":    true,
	// SES catalog/leaves (mailmanager rule-set/relay/traffic-policy/
	// address-list bodies are leaves once ingress-point fans out to them;
	// configuration-set, contact-list, dedicated-ip-pool, template,
	// custom-verification-email-template, multi-region-endpoint, tenant
	// — pure config).
	"aws:ses:configuration-set":                  true,
	"aws:ses:contact-list":                       true,
	"aws:ses:custom-verification-email-template": true,
	"aws:ses:dedicated-ip-pool":                  true,
	"aws:ses:template":                           true,
	"aws:ses:multi-region-endpoint":              true,
	"aws:ses:tenant":                             true,
	// Backup catalog/audit (framework/report-plan/restore-testing-plan
	// have no outbound refs in summary).
	"aws:backup:framework":                 true,
	"aws:backup:report-plan":               true,
	"aws:backup:restore-testing-plan":      true,
	"aws:backup:restore-testing-selection": true,
	"aws:backup:tiering-configuration":     true,
	// AuditManager built-in catalog rows (most ManagedByProvider, but
	// types stay leaf-source either way).
	"aws:auditmanager:control":   true,
	"aws:auditmanager:framework": true,
	// Application catalog primitives.
	"aws:appflow:connector":                                      true,
	"aws:appintegrations:application":                            true,
	"aws:appintegrations:data-integration":                       true,
	"aws:appstream:app-block":                                    true,
	"aws:appstream:directory-config":                             true,
	"aws:appstream:stack":                                        true,
	"aws:appstream:user":                                         true,
	"aws:appsync:api":                                            true,
	"aws:appsync:domain-name":                                    true,
	"aws:appsync:graphql-api":                                    true,
	"aws:apigateway:api-key":                                     true,
	"aws:apigateway:client-certificate":                          true,
	"aws:apigateway:domain-name-access-association":              true,
	"aws:apigatewayv2:api":                                       true,
	"aws:appconfig:application":                                  true,
	"aws:appconfig:deployment-strategy":                          true,
	"aws:appconfig:extension":                                    true,
	"aws:apprunner:auto-scaling-configuration":                   true,
	"aws:apprunner:observability-configuration":                  true,
	"aws:appmesh:mesh":                                           true,
	"aws:application-autoscaling:scalable-target":                true,
	"aws:applicationsignals:grouping-configuration":              true,
	"aws:applicationsignals:service-level-objective":             true,
	"aws:aps:anomaly-detector":                                   true,
	"aws:aps:resource-policy":                                    true,
	"aws:aps:rule-groups-namespace":                              true,
	"aws:arc-region-switch:plan":                                 true,
	"aws:arc-zonal-shift:autoshift-observer-notification-status": true,
	"aws:b2bi:capability":                                        true,
	"aws:b2bi:partnership":                                       true,
	"aws:b2bi:profile":                                           true,
	"aws:b2bi:transformer":                                       true,
	"aws:budgets:budget":                                         true,
	"aws:budgets:budget-action":                                  true,
	// Amplify UI builder tooling (config-only).
	"aws:amplify-ui-builder:component": true,
	"aws:amplify-ui-builder:form":      true,
	"aws:amplify-ui-builder:theme":     true,
	"aws:amplify:domain":               true,
	// Lambda code-signing-config / layer-version-permission / permission
	// are config strings; capacity-provider has no scanned target type.
	"aws:lambda:capacity-provider":        true,
	"aws:lambda:code-signing-config":      true,
	"aws:lambda:layer-version":            true,
	"aws:lambda:layer-version-permission": true,
	"aws:lambda:permission":               true,
	// Resource catalog (Service Catalog / Resilience Hub).
	"aws:resilience-hub:app":                      true,
	"aws:resilience-hub:resiliency-policy":        true,
	"aws:servicecatalog:product":                  true,
	"aws:servicecatalog:service-action":           true,
	"aws:servicecatalog:tag-option":               true,
	"aws:servicecatalog:accepted-portfolio-share": true,
	// Inspector2 filter/CIS — leaves.
	"aws:inspector2:filter":                           true,
	"aws:inspector2:cis-scan-configuration":           true,
	"aws:inspector2:code-security-integration":        true,
	"aws:inspector2:code-security-scan-configuration": true,
	// Misc.
	"aws:accessanalyzer:analyzer":                       true,
	"aws:acm-pca:permission":                            true,
	"aws:athena:data-catalog":                           true,
	"aws:athena:work-group":                             true,
	"aws:athena:named-query":                            true,
	"aws:athena:prepared-statement":                     true,
	"aws:codestar-notifications:notification-rule":      true,
	"aws:securityhub:hub":                               true,
	"aws:securityhub:hub-v2":                            true,
	"aws:securityhub:standard":                          true,
	"aws:securityhub:security-control":                  true,
	"aws:securityhub:insight":                           true,
	"aws:securityhub:configuration-policy":              true,
	"aws:securityhub:organization-configuration":        true,
	"aws:securityhub:standards-subscription":            true,
	"aws:securityhub:policy-association":                true,
	"aws:securityhub:finding-aggregator":                true,
	"aws:securityhub:aggregator-v2":                     true,
	"aws:securityhub:automation-rule":                   true,
	"aws:securityhub:automation-rule-v2":                true,
	"aws:securityhub:connector-v2":                      true,
	"aws:redshift:cluster-parameter-group":              true,
	"aws:redshift:event-subscription":                   true,
	"aws:redshift:scheduled-action":                     true,
	"aws:redshift:integration":                          true,
	"aws:redshift:endpoint-authorization":               true,
	"aws:redshift:endpoint-access":                      true,
	"aws:rds:db-engine-version":                         true,
	"aws:rds:option-group":                              true,
	"aws:rds:db-cluster-parameter-group":                true,
	"aws:rds:event-subscription":                        true,
	"aws:elasticache:parameter-group":                   true,
	"aws:elasticache:security-group":                    true,
	"aws:elasticache:user":                              true,
	"aws:elasticbeanstalk:environment":                  true,
	"aws:emr-serverless:application":                    true,
	"aws:cloudfront:cache-policy":                       true,
	"aws:cloudfront:origin-request-policy":              true,
	"aws:cloudfront:response-headers-policy":            true,
	"aws:cloudfront:origin-access-control":              true,
	"aws:cloudfront:cloud-front-origin-access-identity": true,
	"aws:cloudfront:public-key":                         true,
	"aws:cloudfront:trust-store":                        true,
	"aws:cloudfront:anycast-ip-list":                    true,
	"aws:cloudfront:key-value-store":                    true,
	"aws:cloudfront:connection-function":                true,
	"aws:config:aggregation-authorization":              true,
	"aws:config:retention-configuration":                true,
	"aws:config:evaluation-form-version":                true,
	"aws:config:configuration-aggregator":               true,
	"aws:config:conformance-pack":                       true,
	"aws:config:organization-config-rule":               true,
	"aws:config:remediation-configuration":              true,
	"aws:control-tower:landing-zone":                    true,
	"aws:controltower:landing-zone":                     true,
	"aws:dms:certificate":                               true,
	"aws:dms:data-provider":                             true,
	"aws:s3express:directory-bucket":                    true,
	"aws:s3express:bucket-policy":                       true,
	"aws:s3outposts:bucket":                             true,
	"aws:s3outposts:endpoint":                           true,
	"aws:s3outposts:access-point":                       true,
	"aws:s3outposts:lifecycle-configuration":            true,
	"aws:s3tables:table-bucket":                         true,
	"aws:s3tables:namespace":                            true,
	"aws:s3tables:table":                                true,
	"aws:s3tables:table-bucket-policy":                  true,
	"aws:s3tables:table-policy":                         true,
	"aws:opensearchserverless:access-policy":            true,
	"aws:opensearchserverless:lifecycle-policy":         true,
	"aws:opensearchserverless:security-config":          true,
	"aws:opensearchserverless:security-policy":          true,
	"aws:opensearchserverless:vpc-endpoint":             true,
	"aws:opensearchserverless:collection-group":         true,
	"aws:greengrass:connector-definition":               true,
	"aws:greengrass:core-definition":                    true,
	"aws:greengrass:device-definition":                  true,
	"aws:greengrass:function-definition":                true,
	"aws:greengrass:group":                              true,
	"aws:greengrass:logger-definition":                  true,
	"aws:greengrass:resource-definition":                true,
	"aws:greengrass:subscription-definition":            true,
	"aws:greengrass-v2:component-version":               true,
	// Route 53 Resolver leaves: domain lists are bare name+pattern catalogs;
	// firewall rule-groups own FirewallRules but no scanned cross-service refs;
	// outpost-resolvers reference an Outposts ARN which disco does not scan.
	"aws:route53resolver:firewall-domain-list": true,
	"aws:route53resolver:firewall-rule-group":  true,
	"aws:route53resolver:outpost-resolver":     true,
	// IAM auth singletons. Already covered as targets via user→key/mfa
	// hierarchy (resolveAccessKeyUsers, resolveMFADeviceToUser) and
	// role→saml/oidc-provider (iam policy walker). No outbound source edges.
	"aws:iam:access-key":         true,
	"aws:iam:virtual-mfa-device": true,
	"aws:iam:server-certificate": true,
	"aws:iam:saml-provider":      true,
	"aws:iam:oidc-provider":      true,
	// Cassandra (Keyspaces) — list ops return summary only (ResourceArn,
	// names). Cross-service refs (KMS, custom encryption) live on
	// GetTable Describe; not enriched today, treat as leaf until enriched.
	"aws:cassandra:keyspace": true,
	"aws:cassandra:table":    true,
	"aws:cassandra:type":     true,
	// Cost-domain types — internal cost-API IDs only; no scanned-resource refs.
	"aws:bcmpricingcalculator:bill-scenario": true,
	"aws:billing:billing-view":               true,
	"aws:billingconductor:billing-group":     true,
	"aws:billingconductor:pricing-plan":      true,
	"aws:billingconductor:pricing-rule":      true,
	"aws:braket:spending-limit":              true,
	"aws:budgets:budgets-action":             true,
	"aws:ce:anomaly-monitor":                 true,
	"aws:ce:anomaly-subscription":            true,
	"aws:ce:cost-category":                   true,
	"aws:cur:report-definition":              true,
	"aws:invoicing:invoice-unit":             true,
	// SMS-Voice (Pinpoint v2 messaging) — phone-numbers, pools, sender-IDs
	// are external telco assets; configuration-set/opt-out-list/protect-config
	// are config singletons.
	"aws:sms-voice:configuration-set":     true,
	"aws:sms-voice:opt-out-list":          true,
	"aws:sms-voice:phone-number":          true,
	"aws:sms-voice:pool":                  true,
	"aws:sms-voice:protect-configuration": true,
	"aws:sms-voice:sender-id":             true,
	// Pinpoint message templates — content blobs, no resource refs.
	"aws:pinpoint:email-template":  true,
	"aws:pinpoint:in-app-template": true,
	"aws:pinpoint:push-template":   true,
	"aws:pinpoint:sms-template":    true,
	// Personalize schema is a JSON document, not a graph node.
	"aws:personalize:schema": true,
	// QuickSight content artefacts (theme/template/topic/custom-permissions).
	// Refs to data-sets / data-sources live on analysis/dashboard, not these.
	"aws:quicksight:theme":              true,
	"aws:quicksight:template":           true,
	"aws:quicksight:topic":              true,
	"aws:quicksight:custom-permissions": true,
	// Chatbot channel configurations — channel IDs are external Slack/Teams,
	// not disco resources. SNS topics referenced lives on top-level
	// resolveChatbotSNSTopics; configuration rows themselves are leaves.
	"aws:chatbot:slack-channel-configuration":           true,
	"aws:chatbot:microsoft-teams-channel-configuration": true,
	"aws:chatbot:custom-action":                         true,
	// Support App Slack — same Slack-external story as Chatbot.
	"aws:support-app:slack-channel-configuration":   true,
	"aws:support-app:slack-workspace-configuration": true,
	// SES legacy receipt-* types — SES v1 only; modern SES uses Mail Manager
	// (already wired). Receipt-rule covered as target via receipt-rule-set.
	"aws:ses:receipt-filter":   true,
	"aws:ses:receipt-rule-set": true,
	// Preview-stage SDK services — schemas published ahead of GA APIs;
	// re-evaluate when scanner emits richer attrs.
	"aws:route53globalresolver:access-source":           true,
	"aws:route53globalresolver:access-token":            true,
	"aws:route53globalresolver:dns-view":                true,
	"aws:route53globalresolver:firewall-domain-list":    true,
	"aws:route53globalresolver:firewall-rule":           true,
	"aws:route53globalresolver:global-resolver":         true,
	"aws:route53globalresolver:hosted-zone-association": true,
	"aws:security-agent:agent-space":                    true,
	"aws:security-agent:application":                    true,
	"aws:security-agent:pentest":                        true,
	"aws:security-agent:target-domain":                  true,
	"aws:dev-ops-agent:agent-space":                     true,
	"aws:dev-ops-agent:association":                     true,
	"aws:dev-ops-agent:service":                         true,
	"aws:nova-act:workflow-definition":                  true,
	// Glue catalog leaves — pure config/metadata, no scanned cross-service
	// refs in the List/Describe payloads.
	"aws:glue:classifier":                    true,
	"aws:glue:custom-entity-type":            true,
	"aws:glue:usage-profile":                 true,
	"aws:glue:registry":                      true,
	"aws:glue:schema-version-metadata":       true,
	"aws:glue:integration-resource-property": true,
	// RDS-shape parameter / subnet / event-sub / global-cluster types — all
	// summary-only with no scanned cross-service refs (KMS/SG/Subnet edges
	// land on DBCluster / DBInstance which are already wired source-side).
	"aws:rds:db-parameter-group":             true,
	"aws:rds:db-security-group":              true, // deprecated EC2-Classic SG
	"aws:rds:custom-db-engine-version":       true,
	"aws:docdb:db-cluster-parameter-group":   true,
	"aws:docdb:db-subnet-group":              true,
	"aws:docdb:event-subscription":           true,
	"aws:docdb:global-cluster":               true,
	"aws:docdb:instance":                     true, // covered as cluster contains instance
	"aws:neptune:db-cluster-parameter-group": true,
	"aws:neptune:db-parameter-group":         true,
	"aws:neptune:db-subnet-group":            true,
	"aws:neptune:event-subscription":         true,
	"aws:neptune:instance":                   true,
	"aws:dax:parameter-group":                true,
	"aws:memorydb:parameter-group":           true,
	"aws:memorydb:multi-region-cluster":      true,
	"aws:memorydb:user":                      true,
	// Lifecycle / data-protection / catalogue policies — config + targets
	// expressed as resource-tag matchers, not direct ARN refs.
	"aws:dlm:lifecycle-policy": true,
	"aws:rbin:rule":            true,
	// Resource Access Manager — share + permission types are policy ARNs,
	// not pointers to scanned resources.
	"aws:ram:permission":     true,
	"aws:ram:resource-share": true,
	// Resource Explorer 2 — view + index + default-view-association are
	// query metadata, no scanned target refs.
	"aws:resource-explorer-2:default-view-association": true,
	"aws:resource-explorer-2:index":                    true,
	"aws:resource-explorer-2:view":                     true,
	// Proton — template + cross-account env-connection types, no scanned-
	// resource refs at the summary level.
	"aws:proton:environment-account-connection": true,
	"aws:proton:environment-template":           true,
	"aws:proton:service-template":               true,
	// Transfer Family summary-only types — refs (AccessRole, As2Config, etc.)
	// live on Describe responses; mark Leaf until scanner enriches.
	"aws:transfer:certificate": true,
	"aws:transfer:connector":   true,
	"aws:transfer:profile":     true,
	"aws:transfer:web-app":     true,
	"aws:transfer:workflow":    true,
	// Personalize content rows.
	"aws:personalize:dataset":       true,
	"aws:personalize:dataset-group": true,
	"aws:personalize:solution":      true,
	// Rekognition — collection/project/stream-processor refs live on
	// Describe responses, not list summaries.
	"aws:rekognition:collection":       true,
	"aws:rekognition:project":          true,
	"aws:rekognition:stream-processor": true,
	// FMS — policy/resource-set/notification-channel summary-only.
	"aws:fms:policy":               true,
	"aws:fms:resource-set":         true,
	"aws:fms:notification-channel": true,
	// EventSchemas — registry-only metadata.
	"aws:event-schemas:discoverer": true,
	"aws:event-schemas:registry":   true,
	"aws:event-schemas:schema":     true,
	// Observability Admin telemetry rule family — config singletons that
	// reference services by name, not scanned ARNs.
	"aws:observabilityadmin:organization-centralization-rule": true,
	"aws:observabilityadmin:organization-telemetry-rule":      true,
	"aws:observabilityadmin:s3-table-integration":             true,
	"aws:observabilityadmin:telemetry-enrichment":             true,
	"aws:observabilityadmin:telemetry-pipelines":              true,
	"aws:observabilityadmin:telemetry-rule":                   true,
	// Notifications config — channel-association + notification-config
	// reference Notifications-internal IDs only.
	"aws:notifications:managed-notification-additional-channel-association": true,
	"aws:notifications:notification-configuration":                          true,
	// IVS leaves — encoder/playback/public-key are config artefacts,
	// neither parents nor cross-service refs.
	"aws:ivs:encoder-configuration":       true,
	"aws:ivs:playback-key-pair":           true,
	"aws:ivs:playback-restriction-policy": true,
	"aws:ivs:public-key":                  true,
	// SES Mail Manager — addon-subscription / address-list / relay /
	// rule-set / traffic-policy summaries carry no scanned cross-service
	// refs (KMS / IAM lives on Get* — Tier C1 enrichment task).
	"aws:ses:mailmanager-addon-subscription": true,
	"aws:ses:mailmanager-address-list":       true,
	"aws:ses:mailmanager-relay":              true,
	"aws:ses:mailmanager-rule-set":           true,
	"aws:ses:mailmanager-traffic-policy":     true,
	// SageMaker leftovers — project / code-repository / *lifecycle-config /
	// app-image-config / workteam refs (IAM/KMS/S3) live on Describe* per
	// resource; List* summaries are leaf-shape today.
	"aws:sagemaker:app-image-config":                   true,
	"aws:sagemaker:code-repository":                    true,
	"aws:sagemaker:notebook-instance-lifecycle-config": true,
	"aws:sagemaker:project":                            true,
	"aws:sagemaker:studio-lifecycle-config":            true,
	"aws:sagemaker:workteam":                           true,
	// Workspaces Web — browser-settings / data-protection-settings /
	// ip-access-settings / session-logger / trust-store / user-settings —
	// portal/web-acl/customer-managed-policy refs live on associated
	// portal Describe; List* summaries are leaf today.
	"aws:workspaces-web:browser-settings":         true,
	"aws:workspaces-web:data-protection-settings": true,
	"aws:workspaces-web:ip-access-settings":       true,
	"aws:workspaces-web:session-logger":           true,
	"aws:workspaces-web:trust-store":              true,
	"aws:workspaces-web:user-settings":            true,
	// MediaConnect — flow/bridge/gateway/router-* refs live on Describe*.
	"aws:mediaconnect:bridge":                   true,
	"aws:mediaconnect:flow":                     true,
	"aws:mediaconnect:gateway":                  true,
	"aws:mediaconnect:router-input":             true,
	"aws:mediaconnect:router-network-interface": true,
	"aws:mediaconnect:router-output":            true,
	// Mass deferral pass — types whose ref-bearing fields require Describe*
	// fan-out, deprecated/preview SDK rows, or pure config singletons.
	"aws:appflow:connector-profile":                            true,
	"aws:backup:plan":                                          true,
	"aws:chime:app-instance":                                   true,
	"aws:cleanrooms:collaboration":                             true,
	"aws:cleanrooms:configured-table":                          true,
	"aws:cloud9:environment-ec2":                               true,
	"aws:cloudtrail:channel":                                   true,
	"aws:cloudtrail:dashboard":                                 true,
	"aws:code-build:fleet":                                     true,
	"aws:code-build:report-group":                              true,
	"aws:code-build:source-credential":                         true,
	"aws:codecommit:repository":                                true,
	"aws:codedeploy:application":                               true,
	"aws:codedeploy:deployment-config":                         true,
	"aws:code-guru-profiler:profiling-group":                   true,
	"aws:code-guru-reviewer:repository-association":            true,
	"aws:codepipeline:custom-action-type":                      true,
	"aws:codepipeline:pipeline":                                true,
	"aws:codestar-connections:connection":                      true,
	"aws:config:organization-conformance-pack":                 true,
	"aws:config:stored-query":                                  true,
	"aws:connect-campaigns:campaign":                           true,
	"aws:connect-campaigns-v2:campaign":                        true,
	"aws:controltower:enabled-control":                         true,
	"aws:databrew:dataset":                                     true,
	"aws:databrew:ruleset":                                     true,
	"aws:datasync:agent":                                       true,
	"aws:datasync:task":                                        true,
	"aws:datazone:domain":                                      true,
	"aws:directconnect:direct-connect-gateway":                 true,
	"aws:directconnect:lag":                                    true,
	"aws:ec2:ipam":                                             true,
	"aws:ec2:ipam-prefix-list-resolver":                        true,
	"aws:ec2:ipam-resource-discovery":                          true,
	"aws:ec2:launch-template":                                  true,
	"aws:ec2:local-gateway-route-table":                        true,
	"aws:ec2:local-gateway-virtual-interface-group":            true,
	"aws:ec2:transit-gateway":                                  true,
	"aws:ec2:vpc-endpoint-service":                             true,
	"aws:ecs:cluster":                                          true,
	"aws:efs:access-point":                                     true,
	"aws:elasticloadbalancingv2:trust-store":                   true,
	"aws:elemental-inference:feed":                             true,
	"aws:emr-containers:security-configuration":                true,
	"aws:events:connection":                                    true,
	"aws:events:event-bus":                                     true,
	"aws:evs:environment":                                      true,
	"aws:fin-space:environment":                                true,
	"aws:forecast:dataset":                                     true,
	"aws:forecast:dataset-group":                               true,
	"aws:fsx:s3-access-point-attachment":                       true,
	"aws:global-accelerator:accelerator":                       true,
	"aws:grafana:workspace":                                    true,
	"aws:guardduty:detector":                                   true,
	"aws:guardduty:malware-protection-plan":                    true,
	"aws:iam:foreign-account":                                  true,
	"aws:iam:instance-profile":                                 true,
	"aws:inspector:resource-group":                             true,
	"aws:interconnect:connection":                              true,
	"aws:internet-monitor:monitor":                             true,
	"aws:ivs-chat:logging-configuration":                       true,
	"aws:ivs-chat:room":                                        true,
	"aws:kendra-ranking:execution-plan":                        true,
	"aws:launch-wizard:deployment":                             true,
	"aws:license-manager:license":                              true,
	"aws:lookout-equipment:inference-scheduler":                true,
	"aws:m2:application":                                       true,
	"aws:m2:environment":                                       true,
	"aws:mediapackage:channel":                                 true,
	"aws:mediapackage:packaging-group":                         true,
	"aws:mediapackagev2:channel-group":                         true,
	"aws:media-store:container":                                true,
	"aws:mpa:approval-team":                                    true,
	"aws:mpa:identity-source":                                  true,
	"aws:mq:configuration":                                     true,
	"aws:mwaa:environment":                                     true,
	"aws:mwaa-serverless:workflow":                             true,
	"aws:neptune-graph:graph":                                  true,
	"aws:network-firewall:rule-group":                          true,
	"aws:network-firewall:tls-inspection-configuration":        true,
	"aws:networkmanager:global-network":                        true,
	"aws:oam:link":                                             true,
	"aws:oam:sink":                                             true,
	"aws:opensearchservice:application":                        true,
	"aws:osis:pipeline":                                        true,
	"aws:payment-cryptography:key":                             true,
	"aws:pca-connector-ad:service-principal-name":              true,
	"aws:pca-connector-ad:template":                            true,
	"aws:pca-connector-ad:template-group-access-control-entry": true,
	"aws:pca-connector-scep:challenge":                         true,
	"aws:pca-connector-scep:connector":                         true,
	"aws:pinpoint:app":                                         true,
	"aws:pipes:pipe":                                           true,
	"aws:qbusiness:application":                                true,
	"aws:quicksight:action-connector":                          true,
	"aws:quicksight:analysis":                                  true,
	"aws:quicksight:dashboard":                                 true,
	"aws:quicksight:data-set":                                  true,
	"aws:quicksight:data-source":                               true,
	"aws:quicksight:folder":                                    true,
	"aws:refactor-spaces:environment":                          true,
	"aws:resource-groups:group":                                true,
	"aws:roles-anywhere:trust-anchor":                          true,
	"aws:rum:app-monitor":                                      true,
	"aws:s3express:access-point":                               true,
	"aws:s3-object-lambda:access-point":                        true,
	"aws:s3-object-lambda:access-point-policy":                 true,
	"aws:s3outposts:bucket-policy":                             true,
	"aws:scheduler:schedule-group":                             true,
	"aws:servicediscovery:http-namespace":                      true,
	"aws:sfn:activity":                                         true,
	"aws:shield:drt-access":                                    true,
	"aws:shield:proactive-engagement":                          true,
	"aws:signer:signing-profile":                               true,
	"aws:sim-space-weaver:simulation":                          true,
	"aws:sso:permission-set":                                   true,
	"aws:synthetics:canary":                                    true,
	"aws:synthetics:group":                                     true,
	"aws:systems-manager-sap:application":                      true,
	"aws:verifiedpermissions:policy-store":                     true,
	"aws:voice-id:domain":                                      true,
	"aws:workspaces-instances:workspace-instance":              true,
	"aws:workspaces-thin-client:environment":                   true,
	"aws:xray:sampling-rule":                                   true,
	// CloudFront continuous-deployment-policy + function — CDP carries
	// staging DNS names not distribution ARNs; function is JS code body.
	"aws:cloudfront:continuous-deployment-policy": true,
	"aws:cloudfront:function":                     true,
	// EMR cluster + security-configuration — cluster summary lacks VPC/IAM
	// (lives on DescribeCluster body — deferred); security-configuration is
	// pure encryption JSON.
	"aws:emr:cluster":                true,
	"aws:emr:security-configuration": true,
	// IdentityStore user + group — identity rows; membership wired via
	// dedicated resolver, but user/group themselves have no outbound refs.
	"aws:identitystore:user":  true,
	"aws:identitystore:group": true,
	// Managed Blockchain accessor/member/node — network type not scanned,
	// no other targets.
	"aws:managed-blockchain:accessor": true,
	"aws:managed-blockchain:member":   true,
	"aws:managed-blockchain:node":     true,
	// MediaConvert job-template/preset/queue — refs (KMS for queue, IAM)
	// only on Get* per-resource. Deferred.
	"aws:media-convert:job-template": true,
	"aws:media-convert:preset":       true,
	"aws:media-convert:queue":        true,
	// MediaTailor channel/playback-config/source-location — refs (CDN, ad
	// servers) external; intra-service refs on Get bodies. Deferred.
	"aws:mediatailor:channel":                true,
	"aws:mediatailor:playback-configuration": true,
	"aws:mediatailor:source-location":        true,
	// ODB (Oracle Database@AWS) preview-stage SDK.
	"aws:odb:cloud-autonomous-vm-cluster":  true,
	"aws:odb:cloud-exadata-infrastructure": true,
	"aws:odb:cloud-vm-cluster":             true,
	"aws:odb:odb-network":                  true,
	"aws:odb:odb-peering-connection":       true,
	// S3 catalog leaves — access-grants-instance/multi-region-access-point/
	// storage-lens-group refs live on Get* per resource (deferred).
	"aws:s3:access-grants-instance":    true,
	"aws:s3:multi-region-access-point": true,
	"aws:s3:storage-lens-group":        true,
	// S3Vectors preview SDK.
	"aws:s3vectors:index":                true,
	"aws:s3vectors:vector-bucket":        true,
	"aws:s3vectors:vector-bucket-policy": true,
	// Batch consumable-resource/scheduling-policy/service-environment —
	// pure config rows, no x-service refs.
	"aws:batch:consumable-resource": true,
	"aws:batch:scheduling-policy":   true,
	"aws:batch:service-environment": true,
	// Cleanrooms-ML configured-model-algorithm + association + training-
	// dataset — KMS/IAM refs on Get*, not list summary.
	"aws:cleanrooms-ml:configured-model-algorithm":             true,
	"aws:cleanrooms-ml:configured-model-algorithm-association": true,
	"aws:cleanrooms-ml:training-dataset":                       true,
	// DevOps Guru config rows.
	"aws:devops-guru:log-anomaly-detection-integration": true,
	"aws:devops-guru:notification-channel":              true,
	"aws:devops-guru:resource-collection":               true,
	// X-Ray group + resource-policy (config singletons).
	"aws:xray:group":           true,
	"aws:xray:resource-policy": true,
	// Security Lake data-lake/aws-log-source/subscriber — refs on Get
	// bodies, deferred.
	"aws:security-lake:aws-log-source": true,
	"aws:security-lake:data-lake":      true,
	"aws:security-lake:subscriber":     true,
	// VPC Lattice list-summary types — refs (cert ARN, security groups,
	// auth-policy) live on Get* per resource. Domain-verification is name-
	// only. Deferred enrichment.
	"aws:vpclattice:domain-verification": true,
	"aws:vpclattice:service":             true,
	"aws:vpclattice:service-network":     true,
	// WAFv2 ip-set/regex-pattern-set/rule-group — pure rule-data containers,
	// no x-service refs (rule-group rules name OTHER rule-groups via
	// statement nesting which is separate parser surface).
	"aws:wafv2:ip-set":            true,
	"aws:wafv2:regex-pattern-set": true,
	"aws:wafv2:rule-group":        true,
	// rtbfabric (preview-stage SDK).
	"aws:rtbfabric:link":              true,
	"aws:rtbfabric:requester-gateway": true,
	"aws:rtbfabric:responder-gateway": true,
	// Workspaces classic — connection-alias/workspaces-pool/workspace
	// summaries lack ARNs for downstream refs (KMS/directory live on
	// Describe-per-id deferred).
	"aws:work-spaces:connection-alias": true,
	"aws:work-spaces:workspaces-pool":  true,
	"aws:work-spaces:workspace":        true,
	// AppRegistry application + attribute-group are name containers (links
	// arrive via {ag,resource}-assoc resolvers). No outbound refs on summary.
	"aws:service-catalog-app-registry:application":     true,
	"aws:service-catalog-app-registry:attribute-group": true,
	// MediaLive template-groups + multiplex + signal-map — template groups
	// are name containers (children link via group→child resolver), multiplex
	// has no x-service refs on List, signal-map refs (channel/cloudfront)
	// live on Get body — deferred.
	"aws:medialive:cloudwatch-alarm-template-group": true,
	"aws:medialive:eventbridge-rule-template-group": true,
	"aws:medialive:multiplex":                       true,
	"aws:medialive:signal-map":                      true,
	// Lightsail self-contained resources — bucket/container-service/database/
	// domain. Lightsail is opinionated isolated stack: cross-service refs
	// (KMS/IAM/VPC) absent. Existing resolvers handle distribution origins +
	// certificate domains; remaining four are leaves.
	"aws:lightsail:bucket":            true,
	"aws:lightsail:container-service": true,
	"aws:lightsail:database":          true,
	"aws:lightsail:domain":            true,
	// EntityResolution list summaries — RoleArn / KMS / Glue refs live on
	// Get* per-resource bodies (deferred enrichment).
	"aws:entityresolution:id-mapping-workflow": true,
	"aws:entityresolution:id-namespace":        true,
	"aws:entityresolution:matching-workflow":   true,
	"aws:entityresolution:schema-mapping":      true,
	// PCS cluster — list summary has no outbound refs (network/scheduler
	// config lives on Get body — deferred enrichment).
	"aws:pcs:cluster": true,
	// Omics scaffolding rows — configuration/run-group/workflow list summaries
	// surface no x-service refs. Workflow.Definition references container
	// images via ContainerRegistryMap on Get, but parsing is non-trivial
	// (registry-map → ECR repo) and deferred.
	"aws:omics:configuration": true,
	"aws:omics:run-group":     true,
	"aws:omics:workflow":      true,
	// Organizations identity rows — accounts + OUs have no outbound refs
	// (parent membership is upserted via RecordHierarchyBatch as `contains`
	// rows, not source-side edges). Resource-policy is account-scoped
	// singleton with policy doc walking out of scope.
	"aws:organizations:account":         true,
	"aws:organizations:ou":              true,
	"aws:organizations:resource-policy": true,
	// Location service summary-only types (KMS edges shipped for tracker +
	// geofence-collection via Describe enrichment; remaining four have no
	// outbound refs even on Describe).
	"aws:location:api-key":          true,
	"aws:location:map":              true,
	"aws:location:place-index":      true,
	"aws:location:route-calculator": true,
	// Timestream LiveAnalytics + InfluxDB — refs (KMS, S3, VPC) live on
	// Describe* per resource.
	"aws:timestream:database":           true,
	"aws:timestream:influx-db-cluster":  true,
	"aws:timestream:influx-db-instance": true,
	"aws:timestream:scheduled-query":    true,
	"aws:timestream:table":              true,
	// SSM document/maintenance-window/patch-baseline — already wired as
	// targets of association/maintenance-window-task; source-side refs
	// (Attachments) still need additional Describe enrichment beyond Requires.
	"aws:ssm:maintenance-window":                true,
	"aws:ssm:patch-baseline":                    true,
	"aws:ssm-contacts:contact":                  true,
	"aws:ssm-contacts:plan":                     true,
	"aws:ssm-incidents:response-plan":           true,
	"aws:ssm-quick-setup:configuration-manager": true,
	"aws:ssm-gui-connect:preferences":           true,
	// Logs leftover types — log-group/integration/scheduled-query/resource-policy/
	// delivery-source — KMS/source refs need Describe enrichment.
	"aws:logs:delivery-source": true,
	"aws:logs:integration":     true,
	"aws:logs:log-group":       true,
	"aws:logs:resource-policy": true,
	"aws:logs:scheduled-query": true,
	// Route53 recovery — internal cluster/control-panel/safety-rule/cell/
	// readiness-check/recovery-group/resource-set hierarchies; refs are
	// internal IDs not scanned ARNs.
	"aws:route53-recovery-control:cluster":           true,
	"aws:route53-recovery-control:control-panel":     true,
	"aws:route53-recovery-control:routing-control":   true,
	"aws:route53-recovery-control:safety-rule":       true,
	"aws:route53-recovery-readiness:cell":            true,
	"aws:route53-recovery-readiness:readiness-check": true,
	"aws:route53-recovery-readiness:recovery-group":  true,
	"aws:route53-recovery-readiness:resource-set":    true,
	// Route53 profiles — VPC/profile-resource refs live on Describe*.
	"aws:route53-profiles:profile":                      true,
	"aws:route53-profiles:profile-association":          true,
	"aws:route53-profiles:profile-resource-association": true,
	// Route53 leftovers (cidr-collection, hosted-zone, health-check) —
	// hosted-zone is target of recordsets (already wired via record→zone
	// reverse), health-check is target of recordsets, cidr-collection
	// holds CIDR blocks (no x-service refs).
	"aws:route53:cidr-collection": true,
	"aws:route53:health-check":    true,
	"aws:route53:hosted-zone":     true,
	// Lake Formation — permissions data is principal+resource pairs
	// already covered indirectly via principal/resource scans.
	"aws:lakeformation:data-cells-filter":     true,
	"aws:lakeformation:data-lake-settings":    true,
	"aws:lakeformation:principal-permissions": true,
	"aws:lakeformation:tag":                   true,
	// Kinesis Analytics / kinesis-video / kinesis-extras — refs need
	// Describe enrichment.
	"aws:kinesis-analytics:application":      true,
	"aws:kinesis-analytics-v2:application":   true,
	"aws:kinesis-video:signaling-channel":    true,
	"aws:kinesis-video:stream":               true,
	"aws:kinesis:resource-policy":            true,
	"aws:kinesis:stream-consumer":            true,
	"aws:kafka:configuration":                true,
	"aws:kafka-connect:custom-plugin":        true,
	"aws:kafka-connect:worker-configuration": true,
	// Imagebuilder leftover catalog types — refs (parent image, ECR,
	// SNS topic) live on Describe*; List* is leaf today.
	"aws:imagebuilder:component":                  true,
	"aws:imagebuilder:container-recipe":           true,
	"aws:imagebuilder:distribution-configuration": true,
	"aws:imagebuilder:image-recipe":               true,
	"aws:imagebuilder:workflow":                   true,
	// Iot leftover — fleet-metric / custom-metric / topic-rule-destination /
	// software-package* / iot-events all summary-only without DescribeXxx
	// enrichment.
	"aws:iot:custom-metric":                        true,
	"aws:iot:fleet-metric":                         true,
	"aws:iot:software-package":                     true,
	"aws:iot:software-package-version":             true,
	"aws:iot:topic-rule-destination":               true,
	"aws:iot-events:alarm-model":                   true,
	"aws:iot-events:detector-model":                true,
	"aws:iot-events:input":                         true,
	"aws:iot-core-device-advisor:suite-definition": true,
	"aws:iotsitewise:asset-model":                  true,
	"aws:iotsitewise:computation-model":            true,
	"aws:iotsitewise:dashboard":                    true,
	"aws:iotsitewise:dataset":                      true,
	"aws:iottwinmaker:workspace":                   true,
	"aws:iotfleetwise:signal-catalog":              true,
}

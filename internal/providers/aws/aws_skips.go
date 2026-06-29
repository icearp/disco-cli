package aws

// Skips implements coverage.Skipper: upstream resource keys disco deliberately
// does not scan, each mapped to the reason it is not independently discoverable.
// Build reclassifies matching leftover-upstream rows from `uncovered` to
// `not-scannable`, so the uncovered bucket reflects genuine, actionable gaps.
//
// Reasons fall into a few families; keep the wording specific per entry:
//   - sub-resource:  a CFN association/property type with no standalone List API
//     (it is a field of a parent resource, often already an edge/attribute).
//   - ephemeral:     a task/quote/report/job-run record, not a persistent resource.
//   - no SDK:        a preview/private service with no public aws-sdk-go-v2 client.
//   - duplicate:     the same physical resource disco already scans under another
//     type. NOTE: most duplicates collapse automatically via canonical-identity
//     matching (CanonicalKey in aws_coverage.go) — do NOT hand-write them here.
//     Only the cases the canonicalizer can't detect need an entry (e.g. the
//     apigateway IAM prefix that fronts both v1 and v2, or the cross-service
//     account↔iam:account self-node modelling).
//
// Keys match upstream case-insensitively (CloudFormation `AWS::EC2::Route` or
// the Service Reference lowercase `AWS::ec2::export-image-task` shape). Entries
// are appended per-service as the A→Z scanner buildout classifies each service.
func (coverageProvider) Skips() map[string]string {
	return map[string]string{
		// a4b (Alexa for Business) — retired by AWS; the aws-sdk-go-v2
		// alexaforbusiness client is marked "retired and no longer supported".
		// Nothing left to scan; do not add the dead dependency.
		"AWS::a4b::addressbook":        a4bRetired,
		"AWS::a4b::conferenceprovider": a4bRetired,
		"AWS::a4b::contact":            a4bRetired,
		"AWS::a4b::device":             a4bRetired,
		"AWS::a4b::gateway":            a4bRetired,
		"AWS::a4b::gatewaygroup":       a4bRetired,
		"AWS::a4b::networkprofile":     a4bRetired,
		"AWS::a4b::profile":            a4bRetired,
		"AWS::a4b::room":               a4bRetired,
		"AWS::a4b::schedule":           a4bRetired,
		"AWS::a4b::skillgroup":         a4bRetired,
		"AWS::a4b::user":               a4bRetired,

		// account (AWS Account Management) — neither entry is a discoverable
		// resource collection. The account entity itself is already modelled as
		// the aws:iam:account self-node; accountInOrganization is the same account
		// addressed via the org management context (org membership is covered by
		// the organizations scanner), not a separate resource.
		"AWS::account::account":               "duplicate: account entity modelled as the aws:iam:account self-node",
		"AWS::account::accountInOrganization": "association: account's org membership, covered via the organizations scanner",

		// acm-pca / ACMPCA (Private CA). Issued certificates have no list API
		// (GetCertificate needs both CA and cert ARN), and
		// CertificateAuthorityActivation is the CFN-only association that installs
		// the signed cert chain on a CA, not a standalone resource. (The CA itself
		// is scanned; its duplicate spelling collapses via canonical matching.)
		"AWS::ACMPCA::Certificate":                    "no list API: issued private-CA certificates are not enumerable (GetCertificate requires CA ARN + cert ARN)",
		"AWS::ACMPCA::CertificateAuthorityActivation": "association: installs the signed cert chain on a CA (ImportCertificateAuthorityCertificate), not a standalone resource",

		// aco-automation — no public aws-sdk-go-v2 client, and Compute Optimizer's
		// SDK exposes no automation-rule list op; AutomationRule is modeled only in
		// CloudFormation / the Service Reference, not callable per the SDK mandate.
		"AWS::aco-automation::AutomationRule": "no SDK list op: automation rules are CFN/Service-Reference-only, not modeled in aws-sdk-go-v2",

		// airflow (MWAA) — rbac-role is an Airflow-internal RBAC role addressable
		// in IAM policies (airflow:role ARNs), not an AWS resource with an SDK list
		// op. (The environment's duplicate spelling collapses via canonical
		// matching against aws:mwaa:environment.)
		"AWS::airflow::rbac-role": "no SDK list op: Airflow-internal RBAC role (IAM policy reference), not a discoverable AWS resource",

		// amplify — jobs are deployment/build run records (ephemeral), not
		// infrastructure. (apps/branches/domains duplicate spellings collapse via
		// canonical matching; webhooks are scanned as aws:amplify:webhooks.)
		"AWS::amplify::jobs": "ephemeral: deployment/build job-run records, not a persistent resource",

		// amplifyuibuilder — CodegenJob is an async code-generation run record
		// (ephemeral). (component/form/theme "...Resource" spellings collapse via
		// canonical matching.)
		"AWS::amplifyuibuilder::CodegenJobResource": "ephemeral: async code-generation job-run record, not a persistent resource",

		// appmesh-preview — the deprecated App Mesh preview API namespace. No
		// aws-sdk-go-v2 client exists for it; GA App Mesh is the separate appmesh
		// service. Nothing to scan via the preview namespace.
		"AWS::appmesh-preview::mesh":           appmeshPreviewGone,
		"AWS::appmesh-preview::route":          appmeshPreviewGone,
		"AWS::appmesh-preview::gatewayRoute":   appmeshPreviewGone,
		"AWS::appmesh-preview::virtualGateway": appmeshPreviewGone,
		"AWS::appmesh-preview::virtualNode":    appmeshPreviewGone,
		"AWS::appmesh-preview::virtualRouter":  appmeshPreviewGone,
		"AWS::appmesh-preview::virtualService": appmeshPreviewGone,

		// apigatewayv2 ManagedOverrides — a CFN-only convenience resource for
		// editing API Gateway-managed stage/route/integration; no SDK list op.
		"AWS::ApiGatewayV2::ApiGatewayManagedOverrides": "no SDK list op: CFN-only managed-overrides resource, not independently discoverable",

		// app-integrations — the "-association" types are per-parent link records
		// (integration ↔ external client/target), not first-class discoverable
		// resources. (The application/data-integration/event-integration duplicate
		// spellings collapse via canonical matching against aws:appintegrations:*.)
		"AWS::app-integrations::application-association":       "association: per-parent application↔resource link record, not a first-class resource",
		"AWS::app-integrations::data-integration-association":  "association: per-parent data-integration↔client link record, not a first-class resource",
		"AWS::app-integrations::event-integration-association": "association: per-parent event-integration↔client link record, not a first-class resource",

		// amplifybackend (Amplify Gen1 backend management) — has no list-all API;
		// every op (GetBackend, GetBackendConfig, ...) requires an AppId, and the
		// returned api/auth/storage/config/backend/environment are configuration of
		// the aws:amplify:app already scanned, not independently-ARN'd resources
		// (cf. the rejected aws:s3:bucket-encryption). Jobs and tokens are ephemeral.
		"AWS::amplifybackend::backend":         amplifyBackendConfig,
		"AWS::amplifybackend::created-backend": amplifyBackendConfig,
		"AWS::amplifybackend::environment":     amplifyBackendConfig,
		"AWS::amplifybackend::api":             amplifyBackendConfig,
		"AWS::amplifybackend::auth":            amplifyBackendConfig,
		"AWS::amplifybackend::storage":         amplifyBackendConfig,
		"AWS::amplifybackend::config":          amplifyBackendConfig,
		"AWS::amplifybackend::job":             "ephemeral: Amplify backend job-run records, not a persistent resource",
		"AWS::amplifybackend::token":           "ephemeral: short-lived Amplify backend CLI auth token, not a resource",

		// aoss (OpenSearch Serverless, SR/IAM spelling) — Collection and
		// CollectionGroup collapse via canonical matching to the covered
		// aws:opensearchserverless:* types (serviceRenames aoss→opensearchserverless).
		// Dashboards is a collection's web endpoint (collection.dashboardEndpoint),
		// not a listable resource — no SDK op enumerates it.
		"AWS::aoss::Dashboards": "no SDK list op: OpenSearch Serverless Dashboards is a collection's web endpoint, not a discoverable resource",

		// application-signals — the SR `slo` is the lowercase abbreviation of the
		// CFN `ServiceLevelObjective` disco already scans as
		// aws:applicationsignals:service-level-objective; the canonicalizer can't
		// fold an abbreviation to its expansion, so skip it as a duplicate.
		"AWS::application-signals::slo": "duplicate: SR abbreviation of ServiceLevelObjective, scanned as aws:applicationsignals:service-level-objective",
		// Discovery is the CFN-only singleton that onboards an account to
		// Application Signals (creates the service-linked role); no list/describe
		// op enumerates it.
		"AWS::ApplicationSignals::Discovery": "no list API: CFN-only account-enablement singleton (onboards Application Signals), not a discoverable resource",
		// instrumentationConfig is ephemeral dynamic-instrumentation (breakpoint /
		// probe) config carrying an ExpiresAt; ListInstrumentationConfigurations
		// requires (Environment, Service, InstrumentationType) per call, only
		// enumerable via a per-service fan-out off the metrics-window ListServices
		// API — operational telemetry, not inventoried infrastructure.
		"AWS::application-signals::instrumentationConfig": "ephemeral: dynamic-instrumentation breakpoint/probe config (ExpiresAt); needs per-(environment,service,type) fan-out off the metrics-window ListServices, not standalone inventory",
		// application-signals-mcp is the Application Signals MCP server offering —
		// a Model Context Protocol endpoint, not an AWS account resource; no SDK.
		"AWS::application-signals-mcp::mcp-server": "no SDK: Application Signals MCP server offering, not a discoverable account resource",

		// appstudio — AWS App Studio (low-code app builder) is console-first; no
		// aws-sdk-go-v2 client exists, so none of its resources are scannable.
		"AWS::appstudio::application": appStudioNoSDK,
		"AWS::appstudio::connector":   appStudioNoSDK,
		"AWS::appstudio::instance":    appStudioNoSDK,

		// appsync — SR spells three resources disco already scans under their CFN
		// names: domain→aws:appsync:domain-name, function→function-configuration,
		// mergedApiAssociation→source-api-association (one association, viewed from
		// the merged-API side). The canonicalizer can't fold these word-drops.
		"AWS::appsync::domain":               "duplicate: SR spelling of DomainName, scanned as aws:appsync:domain-name",
		"AWS::appsync::function":             "duplicate: SR spelling of FunctionConfiguration, scanned as aws:appsync:function-configuration",
		"AWS::appsync::mergedApiAssociation": "duplicate: the SourceApiAssociation viewed from the merged API, scanned as aws:appsync:source-api-association",
		// type/field are GraphQL schema constructs, not infrastructure — the whole
		// schema is already captured as aws:appsync:graphql-schema. ListTypes needs
		// apiId+format per call; there is no ListFields op at all.
		"AWS::appsync::type":  "sub-resource: GraphQL schema type within an API (per-apiId/format ListTypes); the schema is scanned as aws:appsync:graphql-schema",
		"AWS::appsync::field": "sub-resource: GraphQL field within a type; no SDK list op, captured in aws:appsync:graphql-schema",

		// aps cluster — a CFN-only Amazon Managed Prometheus resource; the amp
		// SDK has no cluster op (only workspace/scraper/rule-groups-namespace/
		// anomaly-detector), so it is not scannable per the per-service-API mandate.
		"AWS::aps::cluster": "no SDK op: CFN-only APS cluster, amp SDK exposes no List/Describe cluster operation",

		// arc-zonal-shift ALB/NLB — these "resource types" are the Application and
		// Network Load Balancers disco already inventories as aws:elbv2:load-balancer.
		// ARC zonal shift acts on them (ListManagedResources reports per-LB zonal-
		// shift state); it is a capability view of an existing LB, not a new resource.
		"AWS::arc-zonal-shift::ALB": "duplicate: an Application Load Balancer (aws:elbv2:load-balancer) that ARC zonal shift can act on, not a distinct resource",
		"AWS::arc-zonal-shift::NLB": "duplicate: a Network Load Balancer (aws:elbv2:load-balancer) that ARC zonal shift can act on, not a distinct resource",

		// athena session — an interactive Athena-for-Spark session: ephemeral
		// compute that idle-terminates. ListSessions requires a WorkGroup (per-
		// workgroup fan-out) and the workgroup itself is scanned as
		// aws:athena:work-group.
		"AWS::athena::session": "ephemeral: interactive Athena-for-Spark session (idle-terminated); ListSessions needs a WorkGroup, scanned as aws:athena:work-group",

		// auditmanager assessmentControlSet — a control set nested within an
		// assessment, returned inside GetAssessment's body; no standalone list op.
		// The assessment is scanned as aws:auditmanager:assessment.
		"AWS::auditmanager::assessmentControlSet": "sub-resource: control set within an aws:auditmanager:assessment (GetAssessment body), no standalone list API",

		// aws-external-anthropic — an external-partner IAM service prefix (Anthropic
		// integration), not an AWS service with an aws-sdk-go-v2 client; nothing to scan.
		"AWS::aws-external-anthropic::workspace": "no SDK: external-partner (Anthropic) IAM service prefix, no aws-sdk-go-v2 client",

		// appconfig configuration — the deployed/rendered configuration returned by
		// the deprecated GetConfiguration (and appconfigdata GetLatestConfiguration);
		// no ListConfigurations op. The stored config is scanned as
		// aws:appconfig:hosted-configuration-version.
		"AWS::appconfig::configuration": "no list API: rendered config retrieval (GetConfiguration, deprecated); the stored config is aws:appconfig:hosted-configuration-version",

		// artifact agreement — the underlying AWS-published agreement template a
		// customer-agreement is based on (referenced by its AgreementArn). The
		// artifact SDK has no ListAgreements/GetAgreement op; only customer
		// agreements (aws:artifact:customer-agreement) and reports are listable.
		"AWS::artifact::agreement": "no list API: AWS-published agreement template referenced by customer-agreement.AgreementArn; artifact SDK has no agreement list op",
		// artifact compliance-inquiry — an on-demand compliance-info request action,
		// not a stored resource; the artifact SDK has no list/get op for it.
		"AWS::artifact::compliance-inquiry": "no list API: an on-demand compliance-inquiry action, not a discoverable resource",

		// aws-marketplace — an e-commerce catalog/billing domain, not security
		// infrastructure. The listable seller-catalog entities (Entity/Product/
		// Offer/Listing/OfferSet/PurchaseOption via marketplacecatalog ListEntities,
		// OwnershipType=SELF) carry no resource-graph edges and are populated only
		// for seller accounts; deliberately out of disco's inventory scope. The
		// remaining rows are abstract IAM groupings, console views, billing
		// artifacts, or have no list op.
		"AWS::aws-marketplace::Entity":                marketplaceCatalogOOS,
		"AWS::aws-marketplace::Product":               marketplaceCatalogOOS,
		"AWS::aws-marketplace::Offer":                 marketplaceCatalogOOS,
		"AWS::aws-marketplace::OfferSet":              marketplaceCatalogOOS,
		"AWS::aws-marketplace::Listing":               marketplaceCatalogOOS,
		"AWS::aws-marketplace::PurchaseOption":        marketplaceCatalogOOS,
		"AWS::aws-marketplace::ChangeSet":             "out-of-scope: marketplacecatalog change-set request workflow (ListChangeSets), seller e-commerce, not infrastructure",
		"AWS::aws-marketplace::AllListings":           "permission-only: abstract IAM wildcard grouping (All*), not a discoverable resource",
		"AWS::aws-marketplace::AllPurchaseOptions":    "permission-only: abstract IAM wildcard grouping (All*), not a discoverable resource",
		"AWS::aws-marketplace::Dashboard":             "console-only: AWS Marketplace console dashboard view, not a resource",
		"AWS::aws-marketplace::SellerDashboard":       "console-only: AWS Marketplace seller console dashboard view, not a resource",
		"AWS::aws-marketplace::DeploymentParameter":   "no list API: marketplacedeployment exposes only PutDeploymentParameter (write-only), no list/get op",
		"AWS::aws-marketplace::Assessment":            "no list API: AWS Marketplace seller verification artifact, no SDK list op",
		"AWS::aws-marketplace::VerificationEvidence":  "no list API: AWS Marketplace seller verification evidence, no SDK list op",
		"AWS::aws-marketplace::InvoiceSubmissionTask": "billing: AWS Marketplace seller invoicing task, not infrastructure",
		"AWS::aws-marketplace::IssuedTaxInvoice":      "billing: AWS Marketplace issued tax-invoice document, not infrastructure",

		// backup-search — both rows are async job records (searches over backup
		// metadata + their export jobs), not persistent resources.
		"AWS::backup-search::searchJob":       "ephemeral: async backup-metadata search job record, not a persistent resource",
		"AWS::backup-search::searchExportJob": "ephemeral: async backup-search export job record, not a persistent resource",

		// batch — job and service-job are execution records (ListJobs/ListServiceJobs
		// require a jobQueue + status, per-queue fan-out), ephemeral. job-definition-
		// revision is the versioned spelling of the ACTIVE job definitions disco
		// already scans as aws:batch:job-definition (each row is a name:revision).
		"AWS::batch::job":                     "ephemeral: Batch job execution record (per-queue ListJobs), not a persistent resource",
		"AWS::batch::service-job":             "ephemeral: Batch service-job execution record (per-queue ListServiceJobs), not a persistent resource",
		"AWS::batch::job-definition-revision": "duplicate: a revision of aws:batch:job-definition (already scanned per ACTIVE revision)",

		// bedrock — async invocations, flow executions, agent sessions, and the
		// whole *-job family are execution/history records, not persistent
		// resources (the jobs produce custom-model / imported-model rows, which
		// disco does scan). ResourcePolicy is a resource-based policy with only a
		// Get op (no list).
		"AWS::bedrock::async-invoke":                          bedrockEphemeralJob,
		"AWS::bedrock::flow-execution":                        bedrockEphemeralJob,
		"AWS::bedrock::session":                               bedrockEphemeralJob,
		"AWS::bedrock::advanced-prompt-optimization-job":      bedrockEphemeralJob,
		"AWS::bedrock::blueprint-optimization-invocation":     bedrockEphemeralJob,
		"AWS::bedrock::data-automation-invocation-job":        bedrockEphemeralJob,
		"AWS::bedrock::data-automation-library-ingestion-job": bedrockEphemeralJob,
		"AWS::bedrock::evaluation-job":                        bedrockEphemeralJob,
		"AWS::bedrock::model-evaluation-job":                  bedrockEphemeralJob,
		"AWS::bedrock::model-copy-job":                        bedrockEphemeralJob,
		"AWS::bedrock::model-customization-job":               bedrockEphemeralJob,
		"AWS::bedrock::model-import-job":                      bedrockEphemeralJob,
		"AWS::bedrock::model-invocation-job":                  bedrockEphemeralJob,
		"AWS::bedrock::resource-policy":                       bedrockResourcePolicyNoList,
		"AWS::Bedrock::ResourcePolicy":                        bedrockResourcePolicyNoList,
		// prompt-router + default-prompt-router are the SR split (custom vs AWS-
		// default) of the prompt routers disco already scans, custom + default,
		// as aws:bedrock:intelligent-prompt-router (ListPromptRouters). The
		// canonical deduper can't bridge intelligentpromptrouter↔promptrouter.
		"AWS::bedrock::prompt-router":         "duplicate: all prompt routers (custom + AWS-default) scanned as aws:bedrock:intelligent-prompt-router",
		"AWS::bedrock::default-prompt-router": "duplicate: AWS-default prompt routers scanned as aws:bedrock:intelligent-prompt-router (ManagedByProvider)",
		// guardrail-profile (cross-region guardrail profile) and system-tool
		// (AWS-provided agent tool) have no list op in the bedrock / bedrockagent
		// SDKs — not independently enumerable.
		"AWS::bedrock::guardrail-profile": "no list API: cross-region guardrail profile has no list op in the bedrock SDK",
		"AWS::bedrock::system-tool":       "no list API: AWS-provided agent system tool has no list op in the bedrock SDK",
		// data-automation-profile is the cross-region invocation profile passed to
		// InvokeDataAutomationAsync — neither the bedrockdataautomation control nor
		// runtime SDK exposes a list op for it.
		"AWS::bedrock::data-automation-profile": "no list API: data-automation invocation profile has no list op in the bedrockdataautomation SDK",
		// AgentCore resource-policy has only GetResourcePolicy (resourceArn input);
		// token-vault has only GetTokenVault/SetTokenVaultCMK — both singletons with
		// no list op. ab-test / batch-evaluate / recommendation / web-search /
		// workload-identity-directory have no op of any verb in the control or
		// runtime SDK (built-in tools / ephemeral evaluation actions).
		"AWS::BedrockAgentCore::ResourcePolicy":               "no list API: AgentCore resource-policy has only GetResourcePolicy (resourceArn input)",
		"AWS::bedrock-agentcore::token-vault":                 "no list API: AgentCore token vault has only GetTokenVault/SetTokenVaultCMK (per-account singleton)",
		"AWS::bedrock-agentcore::ab-test":                     "no SDK: no list/get op in the bedrockagentcore control or runtime SDK",
		"AWS::bedrock-agentcore::batch-evaluate":              "no SDK: ephemeral evaluation action, no list/get op in the bedrockagentcore SDK",
		"AWS::bedrock-agentcore::recommendation":              "no SDK: no list/get op in the bedrockagentcore control or runtime SDK",
		"AWS::bedrock-agentcore::web-search":                  "no SDK: built-in tool, no list/get op in the bedrockagentcore SDK",
		"AWS::bedrock-agentcore::workload-identity-directory": "no SDK: no list/get op for the workload-identity directory in the bedrockagentcore SDK",

		// bcm — CloudFormation files the Billing & Cost Management dashboard under
		// the "BCM" service while the Service Reference uses "bcm-dashboards";
		// disco scans it once as aws:bcmdashboards:dashboard (covering the SR key).
		"AWS::BCM::Dashboard": "duplicate: BCM dashboard scanned as aws:bcmdashboards:dashboard (CFN files it under BCM, SR under bcm-dashboards)",

		// bcm-data-exports — billingview is the same AWS billing-view resource the
		// IAM Service Reference also lists under the billing and ce prefixes; disco
		// scans it once as aws:billing:billing-view (covering AWS::billing::billingview).
		// table is AWS's published catalog of exportable table schemas
		// (COST_AND_USAGE_REPORT, …) with no ARN — not a user resource.
		"AWS::bcm-data-exports::billingview": "duplicate: AWS billing view catalogued under multiple IAM prefixes; scanned as aws:billing:billing-view",
		"AWS::bcm-data-exports::table":       "no ARN: AWS-published catalog of exportable table schemas (ListTables), not a user resource",

		// braket — SearchDevices only supports the deviceArn filter (no match-all),
		// so the AWS/partner quantum-device catalog can't be enumerated without
		// already knowing the ARNs; jobs and quantum-tasks are execution records.
		"AWS::braket::device":       "no enumerate API: SearchDevices only supports the deviceArn filter; the device catalog can't be listed without known ARNs",
		"AWS::braket::job":          "ephemeral: Braket hybrid-job execution record, not a persistent resource",
		"AWS::braket::quantum-task": "ephemeral: Braket quantum-task execution record, not a persistent resource",

		// billing — the aws-sdk-go-v2/service/billing client exposes only billing
		// views (ListBillingViews); there is no list/describe op for contract
		// (AWS private-pricing agreements), so it cannot be enumerated.
		"AWS::billing::contract": "no list API: billing client has no list/describe op for private-pricing contracts",

		// bugbust — CodeGuru BugBust (gamified bug-fixing events) has no
		// aws-sdk-go-v2 client and the service has been retired by AWS.
		"AWS::bugbust::Event": "service retired: CodeGuru BugBust has no aws-sdk-go-v2 client (service discontinued)",

		// budgets — the SR singular `budgetAction` is the same resource disco
		// already scans as aws:budgets:budgets-action (matching the CFN
		// `BudgetsAction` spelling); canonical dedup can't bridge the extra 's'
		// (budgetsaction vs budgetaction).
		"AWS::budgets::budgetAction": "duplicate: budget actions scanned as aws:budgets:budgets-action (CFN BudgetsAction spelling)",

		// cassandra (Keyspaces) — CDC streams have no enumerate API: the keyspaces
		// SDK exposes only ListKeyspaces/ListTables/ListTypes (no ListStreams), and
		// there is no keyspacesstreams control client. Stream config is a per-table
		// property, not independently listable.
		"AWS::cassandra::stream": "no list API: Keyspaces CDC streams have no list op in the keyspaces SDK",

		// ce (Cost Explorer) — billingview is the Cost Explorer service spelling of
		// the same billing view disco already scans as aws:billing:billing-view
		// (billing:ListBillingViews). Cross-service duplicate the canonicalizer
		// can't bridge (ce vs billing).
		"AWS::ce::billingview": "duplicate: billing views scanned as aws:billing:billing-view (billing:ListBillingViews)",

		// cloud9 — the SR generic `environment` is the umbrella over the EC2
		// environments disco already scans as aws:cloud9:environment-ec2 (CFN models
		// only EnvironmentEC2; the scanner filters DescribeEnvironments to EC2 type).
		// Cloud9 is closed to new customers (2024-07-31); the SSH-environment subset
		// the generic also covers is a deprecated-service edge case not worth a
		// second type.
		"AWS::cloud9::environment": "duplicate: cloud9 environments scanned as aws:cloud9:environment-ec2 (CFN EnvironmentEC2 spelling)",

		// chatbot — the SR generic `ChatbotConfiguration` is the umbrella over the
		// specific channel configs disco already scans as
		// aws:chatbot:slack-channel-configuration / microsoft-teams-channel-
		// configuration (matching the CFN per-platform types). The only subset not
		// covered is Chime webhook configs, tied to the discontinued Amazon Chime.
		"AWS::chatbot::ChatbotConfiguration": "duplicate: chatbot channel configs scanned as aws:chatbot:{slack,microsoft-teams}-channel-configuration (Chime-webhook subset tied to discontinued Amazon Chime)",

		// chime — channel is a messaging data-plane resource: ListChannels
		// requires a ChimeBearer (an AppInstanceUser identity to act as), which a
		// control-plane inventory scanner has no basis to assume. meeting has no
		// list API (ListAttendees needs a MeetingId) and meetings are ephemeral.
		"AWS::chime::channel": "data-plane: ListChannels requires a ChimeBearer AppInstanceUser identity, out of scope for control-plane inventory",
		"AWS::chime::meeting": "no list API: meetings are ephemeral; the SDK exposes only Create/Get/Delete + ListAttendees(MeetingId)",

		// cases (Amazon Connect Cases) — Case and RelatedItem are per-domain
		// support-ticket content (operational data fetched via SearchCases with a
		// domainId), not infrastructure. disco scans the Cases domain/template/
		// field/layout/case-rule configuration, not the case contents.
		"AWS::cases::Case":        "out-of-scope: support-case records are per-domain operational content, not infrastructure (the Cases domain/template/field/layout config is scanned)",
		"AWS::cases::RelatedItem": "out-of-scope: items attached to a support case (per-case content), not an infrastructure resource",

		// cleanrooms-ml — audience-generation and trained-model-inference jobs are
		// async run records (ephemeral), not persistent resources. The audience
		// models, configured audience models, ML input channels and trained models
		// they produce are scanned.
		"AWS::cleanrooms-ml::audiencegenerationjob":    "ephemeral: audience-generation job-run record, not a persistent resource",
		"AWS::cleanrooms-ml::TrainedModelInferenceJob": "ephemeral: trained-model inference job-run record, not a persistent resource",

		// cloudfront — the SR short `origin-access-identity` is the same legacy OAI
		// disco scans as aws:cloudfront:cloud-front-origin-access-identity (CFN
		// CloudFrontOriginAccessIdentity spelling). Canonical dedup can't bridge the
		// dropped "cloud-front-" prefix.
		"AWS::cloudfront::origin-access-identity": "duplicate: legacy OAI scanned as aws:cloudfront:cloud-front-origin-access-identity (CFN CloudFrontOriginAccessIdentity spelling)",

		// cloudfront-keyvaluestore — the same key-value store disco scans under the
		// cloudfront service (aws:cloudfront:key-value-store, via cloudfront:
		// ListKeyValueStores). The Service Reference files it under its own
		// data-plane service segment; cross-segment duplicate the canonicalizer
		// can't bridge.
		"AWS::cloudfront-keyvaluestore::key-value-store": "duplicate: scanned as aws:cloudfront:key-value-store (cloudfront:ListKeyValueStores)",

		// cloudshell — interactive browser shell; no aws-sdk-go-v2 client and no
		// list API for environments (they are per-user ephemeral sessions).
		"AWS::cloudshell::Environment": "no SDK: CloudShell is an interactive per-user session, no aws-sdk-go-v2 client or environment list API",
		// cloudtrail-data — the data-plane ingestion API (PutAuditEvents only); the
		// channel it ingests into is scanned as aws:cloudtrail:channel.
		"AWS::cloudtrail-data::channel": "duplicate: the ingestion channel is scanned as aws:cloudtrail:channel; cloudtraildata SDK exposes only PutAuditEvents",

		// cloudwatch — the unified-console branding of Application Signals + a
		// CFN-only alarm variant + a no-list dataset.
		"AWS::cloudwatch::slo":      "duplicate: the SLO is scanned as aws:applicationsignals:service-level-objective (cloudwatch is the unified-console branding of the same resource)",
		"AWS::cloudwatch::dataset":  "no list API: cloudwatch SDK exposes only GetDataset (by id); datasets cannot be enumerated",
		"AWS::CloudWatch::LogAlarm": "no SDK list op: a CFN-only alarm variant; DescribeAlarms returns only metric/composite alarms (scanned as aws:cloudwatch:alarm / composite-alarm)",

		// codeartifact — packages are software artifacts (npm/pypi/maven) stored in
		// a repository: content, not infrastructure. The repository is the resource
		// (scanned as aws:codeartifact:repository); per-package rows are unbounded
		// content, mirroring how ECR scans repositories but not individual images.
		"AWS::codeartifact::package": "content within repository: packages are software artifacts stored in aws:codeartifact:repository (unbounded content, not infrastructure — mirrors ECR repositories-not-images)",

		// codebuild — builds / batches / reports are execution run-history (the
		// project + report-group configs are the resources); sandbox is an
		// ephemeral interactive debug session.
		"AWS::codebuild::build":       "execution history: a build run of aws:codebuild:project (unbounded runtime events, not infrastructure)",
		"AWS::codebuild::build-batch": "execution history: a batch build run of aws:codebuild:project (unbounded runtime events, not infrastructure)",
		"AWS::codebuild::report":      "execution output: a test/coverage report produced by a build run; aws:codebuild:report-group is the resource",
		"AWS::codebuild::sandbox":     "ephemeral: an interactive CodeBuild sandbox debug session, not durable infrastructure",

		// codecatalyst — authenticates via AWS Builder ID personal access tokens
		// (smithy bearer auth), not the account's SigV4 IAM credentials disco scans
		// with; spaces/projects/connections are Builder-ID org constructs, not
		// AWS-account resources reachable from acct.cfg.
		"AWS::codecatalyst::space":                        "different auth: CodeCatalyst uses AWS Builder ID bearer tokens, not the account's SigV4 credentials; spaces are Builder-ID org constructs",
		"AWS::codecatalyst::project":                      "different auth: CodeCatalyst uses AWS Builder ID bearer tokens, not the account's SigV4 credentials; projects live under a Builder-ID space",
		"AWS::codecatalyst::connections":                  "different auth: CodeCatalyst uses AWS Builder ID bearer tokens, not the account's SigV4 credentials",
		"AWS::codecatalyst::identity-center-applications": "different auth: CodeCatalyst uses AWS Builder ID bearer tokens, not the account's SigV4 credentials",

		// codedeploy — an instance is a deployment-execution target (ListDeploymentInstances
		// is deprecated for ListDeploymentTargets); the EC2 instance is scanned as aws:ec2:instance.
		"AWS::codedeploy::instance": "execution target: a deployment's instance (ListDeploymentInstances is deprecated); the EC2 instance is scanned as aws:ec2:instance",

		// codeguru-reviewer — the repository association is scanned; a code-review is
		// an analysis run.
		"AWS::codeguru-reviewer::association": "duplicate: scanned as aws:code-guru-reviewer:repository-association (CFN twin AWS::CodeGuruReviewer::RepositoryAssociation)",
		"AWS::codeguru-reviewer::codereview":  "analysis run: a CodeGuru Reviewer code-review execution, not infrastructure (the repository-association is the resource)",

		// codeguru-security — a scan is a code-analysis job (findings + state); the
		// account-level configuration is the durable state.
		"AWS::codeguru-security::ScanName": "analysis run: a CodeGuru Security scan is a code-analysis job (findings + state), not infrastructure",

		// codestar — AWS CodeStar (classic project service) was retired July 2024;
		// the SDK is frozen and the service accepts no new projects.
		"AWS::CodeStar::GitHubRepository": "service retired: AWS CodeStar (classic) was discontinued July 2024; SDK frozen, no new projects",
		"AWS::codestar::project":          "service retired: AWS CodeStar (classic) was discontinued July 2024; SDK frozen, no new projects",
		"AWS::codestar::user":             "service retired: AWS CodeStar (classic) was discontinued July 2024; SDK frozen, no new projects",

		// codewhisperer — rebranded to Amazon Q Developer; there is no
		// aws-sdk-go-v2/service/codewhisperer module, and Q uses bearer auth.
		"AWS::codewhisperer::customization": "no SDK: CodeWhisperer was rebranded to Amazon Q Developer; no aws-sdk-go-v2 module, bearer auth",
		"AWS::codewhisperer::profile":       "no SDK: CodeWhisperer was rebranded to Amazon Q Developer; no aws-sdk-go-v2 module, bearer auth",

		// codepipeline — stages and actions are nested in the pipeline definition
		// (no ListStages/ListActions op; GetPipeline returns pipeline.stages[].actions[]);
		// action-types are the provider catalog.
		"AWS::codepipeline::stage":      "nested config: a stage embedded in aws:codepipeline:pipeline's definition (no independent list op)",
		"AWS::codepipeline::action":     "nested config: an action embedded in a pipeline stage (no independent list op)",
		"AWS::codepipeline::actiontype": "catalog: ListActionTypes is dominated by AWS/ThirdParty provider action-type definitions; custom action types are a niche extensibility mechanism, not core inventory",

		// cognito — end-user accounts / memberships are application data (PII,
		// unbounded), a principal-tag map is nested config, the webacl is a WAF
		// association, and Cognito Sync is a deprecated service.
		"AWS::Cognito::UserPoolUser":                  "application data: Cognito end-user accounts (unbounded, PII); the user pool is the resource (aws:cognito:user-pool)",
		"AWS::Cognito::UserPoolUserToGroupAttachment": "application data: end-user group membership within a user pool, not infrastructure",
		"AWS::Cognito::IdentityPoolPrincipalTag":      "nested config: a principal-tag attribute map per identity pool / provider (GetPrincipalTagAttributeMap), not an independent resource",
		"AWS::cognito-idp::webacl":                    "association: a WAF web ACL (scanned as aws:wafv2:web-acl) associated with a user pool",
		"AWS::cognito-sync::dataset":                  "deprecated service: Cognito Sync (replaced by AWS AppSync); datasets are end-user sync data",
		"AWS::cognito-sync::identity":                 "deprecated service: Cognito Sync (replaced by AWS AppSync); end-user identities",
		"AWS::cognito-sync::identitypool":             "deprecated service: Cognito Sync (replaced by AWS AppSync); the identity pool is scanned as aws:cognito:identity-pool",

		// comprehend — the *-detection-job / *-classification-job types are async
		// batch-inference run records (ephemeral); flywheel-dataset is training data
		// registered to a flywheel (content). The durable models (document-classifier,
		// entity-recognizer), inference endpoints and flywheels are scanned.
		"AWS::comprehend::document-classification-job":      comprehendJob,
		"AWS::comprehend::dominant-language-detection-job":  comprehendJob,
		"AWS::comprehend::entities-detection-job":           comprehendJob,
		"AWS::comprehend::events-detection-job":             comprehendJob,
		"AWS::comprehend::key-phrases-detection-job":        comprehendJob,
		"AWS::comprehend::pii-entities-detection-job":       comprehendJob,
		"AWS::comprehend::sentiment-detection-job":          comprehendJob,
		"AWS::comprehend::targeted-sentiment-detection-job": comprehendJob,
		"AWS::comprehend::topics-detection-job":             comprehendJob,
		"AWS::comprehend::flywheel-dataset":                 "content: a labeled training dataset registered to a flywheel, not infrastructure",

		// computeoptimizer — the Compute Optimizer SDK exposes only Get*Recommendations
		// ops; AutomationRule is modeled in CloudFormation / Service Reference only.
		"AWS::ComputeOptimizer::AutomationRule": "no SDK list op: Compute Optimizer automation rules are CFN/Service-Reference-only (SDK exposes only Get*Recommendations)",

		// config — the Connector resource is CFN/Service-Reference-only; the
		// configservice SDK exposes no connector list/describe op.
		"AWS::config::Connector": "no SDK list op: AWS Config connectors are CFN/Service-Reference-only, not modeled in aws-sdk-go-v2 configservice",

		// consoleapp (AWS Console Mobile Application) — no aws-sdk-go-v2 client
		// exists; device identities are per-user mobile-device registrations.
		"AWS::consoleapp::DeviceIdentity": "no SDK: AWS Console Mobile Application has no aws-sdk-go-v2 client; device identities are per-user mobile registrations",

		// connect — the wildcard-* types are IAM-policy wildcard ARN patterns; the
		// view variants are IAM ARN qualifications of the scanned View/ViewVersion;
		// contacts/evaluations/files are runtime interaction data; hierarchy-group
		// and legacy-phone-number are duplicates; ai-agent is a Wisdom/Q construct.
		"AWS::connect::wildcard-agent-status":           connectWildcard,
		"AWS::connect::wildcard-contact-flow":           connectWildcard,
		"AWS::connect::wildcard-legacy-phone-number":    connectWildcard,
		"AWS::connect::wildcard-phone-number":           connectWildcard,
		"AWS::connect::wildcard-queue":                  connectWildcard,
		"AWS::connect::wildcard-quick-connect":          connectWildcard,
		"AWS::connect::aws-managed-view":                connectView,
		"AWS::connect::customer-managed-view":           connectView,
		"AWS::connect::customer-managed-view-version":   connectView,
		"AWS::connect::qualified-aws-managed-view":      connectView,
		"AWS::connect::qualified-customer-managed-view": connectView,
		"AWS::connect::contact":                         "runtime: a contact (call/chat/task interaction), unbounded runtime data, not infrastructure",
		"AWS::connect::contact-evaluation":              "runtime: a per-contact evaluation-form submission, not infrastructure",
		"AWS::connect::attached-file":                   "content: a file attached to a contact/case, not infrastructure",
		"AWS::connect::hierarchy-group":                 "duplicate: scanned as aws:connect:user-hierarchy-group (ListUserHierarchyGroups)",
		"AWS::connect::legacy-phone-number":             "duplicate: legacy ARN form of a phone number, scanned as aws:connect:phone-number",
		"AWS::connect::use-case":                        "sub-resource: a use binding of an integration-association (ListUseCases requires the association id), not a first-class resource",
		"AWS::connect::ai-agent":                        "no connect SDK list op: Connect AI agents are a Wisdom / Amazon Q in Connect construct (qconnect SDK), not the connect SDK",

		// controlcatalog — the Control Catalog is AWS's global reference catalog of
		// available controls / common-controls / domains / objectives (read-only
		// AWS-published reference data), not account-specific resources.
		"AWS::controlcatalog::common-control": controlCatalog,
		"AWS::controlcatalog::control":        controlCatalog,
		"AWS::controlcatalog::domain":         controlCatalog,
		"AWS::controlcatalog::objective":      controlCatalog,

		// controltower — Baseline is the catalog of AWS-defined available baselines
		// (ListBaselines reference data); the account resource is the enabled
		// baseline, scanned as aws:controltower:enabled-baseline.
		"AWS::controltower::Baseline": "AWS-managed catalog: the available-baseline reference list; enabled baselines are scanned as aws:controltower:enabled-baseline",

		// cur — the Cost & Usage report is scanned as aws:cur:report-definition
		// (CFN twin AWS::CUR::ReportDefinition); "cur::cur" is the IAM spelling.
		"AWS::cur::cur": "duplicate: the Cost & Usage report is scanned as aws:cur:report-definition (CFN twin AWS::CUR::ReportDefinition)",

		// dataexchange — revisions/assets are versioned content within a data set
		// (like S3 object versions/objects, ListDataSetRevisions/ListRevisionAssets
		// require parent ids); entitled-* are consumer-side marketplace
		// subscriptions to other accounts' data, not owned resources; jobs are
		// import/export run records. Owned data-sets, data-grants and event-actions
		// are scanned.
		"AWS::dataexchange::revisions":          dataExchangeContent,
		"AWS::dataexchange::assets":             dataExchangeContent,
		"AWS::dataexchange::entitled-data-sets": dataExchangeEntitled,
		"AWS::dataexchange::entitled-revisions": dataExchangeEntitled,
		"AWS::dataexchange::entitled-assets":    dataExchangeEntitled,
		"AWS::dataexchange::jobs":               "ephemeral: import/export job-run records, not a persistent resource",

		// datasync — every location is scanned under its typed form
		// (aws:datasync:location-{s3,efs,nfs,smb,object-storage,fsx-*,hdfs,azure-blob})
		// via ListLocations + per-type Describe, so the generic "location" is an
		// umbrella. DataSync Discovery (storagesystem/discoveryjob) was removed by
		// AWS — the aws-sdk-go-v2 client has no such ops. taskexecution is a run.
		"AWS::datasync::location":      "umbrella: every DataSync location is scanned under its typed form (aws:datasync:location-*) via ListLocations + per-type Describe",
		"AWS::datasync::storagesystem": "no SDK op: AWS removed DataSync Discovery; aws-sdk-go-v2 has no ListStorageSystems/DescribeStorageSystem",
		"AWS::datasync::discoveryjob":  "no SDK op: AWS removed DataSync Discovery; aws-sdk-go-v2 has no ListDiscoveryJobs/DescribeDiscoveryJob",
		"AWS::datasync::taskexecution": "ephemeral: a task-execution is a single run of a DataSync task, not a persistent resource",

		// datazone — FormType is a catalog metadata-form schema (SearchTypes), a
		// definition not infrastructure. Owner and PolicyGrant are per-entity
		// association records (ListEntityOwners/ListPolicyGrants both require an
		// EntityType+EntityIdentifier), not first-class resources. Group/user
		// profiles ARE scanned.
		"AWS::DataZone::FormType":    "metadata schema: a catalog metadata-form-type definition (SearchTypes), config not a resource",
		"AWS::DataZone::Owner":       "association: a per-entity owner record (ListEntityOwners requires EntityType+EntityIdentifier), not a first-class resource",
		"AWS::DataZone::PolicyGrant": "association: a per-entity managed-policy grant (ListPolicyGrants requires EntityType+EntityIdentifier), not a first-class resource",

		// dax — DAX has no "application" resource; aws-sdk-go-v2 exposes only
		// clusters / parameter-groups / subnet-groups (all scanned).
		"AWS::dax::application": "not a resource: DAX has no application; the SDK exposes only cluster/parameter-group/subnet-group, all scanned",

		// detective — a member invitation is the act of adding a member to a
		// behavior graph; the resulting membership is scanned as
		// aws:detective:member (MemberDetail carries Status/InvitedTime).
		"AWS::Detective::MemberInvitation": "duplicate: the membership created by an invitation is scanned as aws:detective:member (ListMembers, with invitation status/time)",

		// deadline — a job is a submitted render-job run and a worker is a running
		// fleet node; both are ephemeral runtime, not persistent infrastructure.
		// Budgets and volumes are scanned.
		"AWS::deadline::job":    "ephemeral: a submitted render-job run (per farm/queue), not persistent infrastructure",
		"AWS::deadline::worker": "ephemeral: a running fleet worker node (per farm/fleet), not persistent infrastructure",

		// devicefarm — device is AWS's global catalog of test devices (reference
		// data, not account resources). run/job/suite/test/sample/artifact/session
		// and testgrid-session are the ephemeral test-run hierarchy. upload is the
		// uploaded app/test package content. Projects, pools, profiles, device
		// instances, VPCE configs and test-grid projects are scanned.
		"AWS::devicefarm::device":           "AWS-managed catalog: the global list of available test devices, not account resources",
		"AWS::devicefarm::run":              deviceFarmRun,
		"AWS::devicefarm::job":              deviceFarmRun,
		"AWS::devicefarm::suite":            deviceFarmRun,
		"AWS::devicefarm::test":             deviceFarmRun,
		"AWS::devicefarm::sample":           deviceFarmRun,
		"AWS::devicefarm::artifact":         deviceFarmRun,
		"AWS::devicefarm::session":          "ephemeral: a remote-access session to a device (per project), not persistent infrastructure",
		"AWS::devicefarm::testgrid-session": "ephemeral: a Selenium test-grid session run (per testgrid-project), not persistent infrastructure",
		"AWS::devicefarm::upload":           "content: an uploaded app/test package within a project, not infrastructure",

		// devops-guru — topic is the SNS topic an account registers as a DevOps
		// Guru notification channel; it is modelled as aws:sns:topic, and the
		// channel itself is scanned as aws:devops-guru:notification-channel.
		"AWS::devops-guru::topic": "reference: the SNS topic backing a notification channel (modelled as aws:sns:topic)",

		// drs (Elastic Disaster Recovery) — JobResource is an ephemeral
		// recovery/drill job-run record, not persistent infrastructure. (The
		// source servers, recovery instances, source networks, and replication /
		// launch templates are scanned.)
		"AWS::drs::JobResource": "ephemeral: a recovery/drill job-run record, not a persistent resource",

		// ds / ds-data — the generic "directory" resource is the same Directory
		// Service directory disco scans by type as aws:directory-service:microsoft-ad
		// and :simple-ad (DescribeDirectories). ds is the IAM/SR service spelling
		// for Directory Service; ds-data is the Directory Service Data API that
		// manages users/groups inside that same directory.
		"AWS::ds::directory":      "duplicate: a Directory Service directory, scanned by type as aws:directory-service:{microsoft-ad,simple-ad}",
		"AWS::ds-data::directory": "duplicate: the Directory Service Data API addresses the same directory scanned as aws:directory-service:{microsoft-ad,simple-ad}",

		// dynamodb — export/import are point-in-time data-movement job records
		// (the durable artifact is the S3 object, not a DynamoDB resource), and
		// index is a global/local secondary index embedded in DescribeTable, not
		// independently listable. On-demand backups are scanned (aws:dynamodb:backup).
		"AWS::dynamodb::export": "ephemeral: a point-in-time table export job to S3, not a persistent resource",
		"AWS::dynamodb::import": "ephemeral: a one-time table import job from S3, not a persistent resource",
		"AWS::dynamodb::index":  "sub-resource: a table secondary index, embedded in DescribeTable, not independently listable",

		// ebs — the EBS direct-read API's "snapshot" resource is the same EBS
		// snapshot disco scans via the EC2 DescribeSnapshots API as aws:ec2:snapshot.
		"AWS::ebs::snapshot": "duplicate: EBS snapshots are scanned as aws:ec2:snapshot (DescribeSnapshots)",

		// ec2 — CFN association/property types: a relationship or sub-field of a
		// parent resource, no standalone List API. disco models these as edges or
		// embedded attributes on the parent.
		"AWS::EC2::EIPAssociation":                       "sub-resource: an EIP↔instance/ENI association, modelled as an edge from the EIP",
		"AWS::EC2::EnclaveCertificateIamRoleAssociation": "sub-resource: an ACM-cert↔IAM-role association for Nitro Enclaves, no standalone List API",
		"AWS::EC2::GatewayRouteTableAssociation":         "sub-resource: a gateway↔route-table association, embedded in the route table",
		"AWS::EC2::IpPoolRouteTableAssociation":          "sub-resource: an IP-pool↔route-table association, no standalone List API",
		"AWS::EC2::NetworkAclEntry":                      "sub-resource: a NACL rule, embedded in the network-acl Entries[]",
		"AWS::EC2::NetworkInterfaceAttachment":           "sub-resource: an ENI↔instance attachment, modelled as an edge from the ENI",
		"AWS::EC2::Route":                                "sub-resource: a route-table route, embedded in the route table Routes[]",
		"AWS::EC2::RouteServerAssociation":               "sub-resource: a route-server↔VPC association, no standalone List API",
		"AWS::EC2::RouteServerPropagation":               "sub-resource: a route-server↔route-table propagation, no standalone List API",
		"AWS::EC2::SecurityGroupEgress":                  "sub-resource: a security-group egress rule, embedded in the security-group IpPermissionsEgress[]",
		"AWS::EC2::SecurityGroupIngress":                 "sub-resource: a security-group ingress rule, embedded in the security-group IpPermissions[]",
		"AWS::EC2::SubnetCidrBlock":                      "sub-resource: an IPv6 CIDR block on a subnet, embedded in the subnet",
		"AWS::EC2::SubnetNetworkAclAssociation":          "sub-resource: a subnet↔NACL association, modelled as an edge",
		"AWS::EC2::SubnetRouteTableAssociation":          "sub-resource: a subnet↔route-table association, embedded in the route table Associations[]",
		"AWS::EC2::VPCCidrBlock":                         "sub-resource: a secondary CIDR block on a VPC, embedded in the VPC",
		"AWS::EC2::VPCDHCPOptionsAssociation":            "sub-resource: a VPC↔DHCP-options association, modelled as an edge",
		"AWS::EC2::VPCGatewayAttachment":                 "sub-resource: an internet/VPN-gateway↔VPC attachment, modelled as an edge",
		"AWS::EC2::VPNConnectionRoute":                   "sub-resource: a static route on a VPN connection, embedded in the vpn-connection",
		"AWS::EC2::VPNGatewayRoutePropagation":           "sub-resource: a VPN-gateway↔route-table propagation, embedded in the route table",
		"AWS::EC2::VolumeAttachment":                     "sub-resource: a volume↔instance attachment, modelled as an edge from the volume",
		"AWS::ec2::security-group-rule":                  "sub-resource: a security-group rule, embedded in the security-group IpPermissions[]",
		"AWS::ec2::subnet-cidr-reservation":              "sub-resource: a CIDR reservation within a subnet, no standalone Describe API",
		"AWS::ec2::verified-access-endpoint-target":      "sub-resource: a target of a verified-access endpoint, embedded in the endpoint",
		"AWS::ec2::verified-access-policy":               "sub-resource: a policy attached to a verified-access group/endpoint, embedded in the parent",
		"AWS::ec2::vpc-endpoint-connection":              "sub-resource: a consumer connection to a VPC endpoint service, modelled as an edge",

		// ec2 — CFN-only types with no aws-sdk-go-v2 op (per the per-service-API mandate, CFN-only types are not scannable).
		"AWS::EC2::TransitGatewayMeteringPolicyEntry": "no SDK: a CFN-only sub-resource of the metering policy with no aws-sdk-go-v2 ec2 op",
		"AWS::EC2::SqlHaStandbyDetectedInstance":      "no SDK: a CFN-only detection record with no aws-sdk-go-v2 ec2 op",

		// ec2 — ephemeral task/quote/report records, not persistent resources.
		"AWS::ec2::capacity-reservation-cancellation-quote": "ephemeral: a one-shot cancellation quote, not a persistent resource",
		"AWS::ec2::declarative-policies-report":             "ephemeral: a generated declarative-policies report, not a persistent resource",
		"AWS::ec2::image-usage-report":                      "ephemeral: a generated AMI usage report, not a persistent resource",
		"AWS::ec2::export-image-task":                       "ephemeral: an in-progress AMI-export task record, not a persistent resource",
		"AWS::ec2::export-instance-task":                    "ephemeral: an in-progress instance-export task record, not a persistent resource",
		"AWS::ec2::import-image-task":                       "ephemeral: an in-progress AMI-import task record, not a persistent resource",
		"AWS::ec2::import-snapshot-task":                    "ephemeral: an in-progress snapshot-import task record, not a persistent resource",
		"AWS::ec2::mac-modification-task":                   "ephemeral: an in-progress dedicated-host MAC-modification task record, not a persistent resource",
		"AWS::ec2::replace-root-volume-task":                "ephemeral: an in-progress root-volume-replacement task record, not a persistent resource",

		// ec2 — services AWS has retired; no resources remain to scan.
		"AWS::ec2::elastic-gpu":       "retired: Amazon Elastic Graphics was discontinued by AWS",
		"AWS::ec2::elastic-inference": "retired: Amazon Elastic Inference was discontinued by AWS (April 2024)",

		// ec2 — a read-only catalog of sample VPN device configurations, not account resources.
		"AWS::ec2::vpn-connection-device-type": "catalog: AWS's read-only list of supported VPN device types, not an account resource",

		// ec2 — cross-service / abbreviation duplicates the canonicalizer can't bridge.
		// Same physical resource disco already scans under another type.
		"AWS::ec2::elastic-ip":            "duplicate: scanned as aws:ec2:eip (AWS::EC2::EIP)",
		"AWS::ec2::fleet":                 "duplicate: scanned as aws:ec2:ec2-fleet (AWS::EC2::EC2Fleet)",
		"AWS::ec2::dedicated-host":        "duplicate: scanned as aws:ec2:host (AWS::EC2::Host)",
		"AWS::ec2::vpc-flow-log":          "duplicate: scanned as aws:ec2:flow-log (AWS::EC2::FlowLog)",
		"AWS::ec2::spot-fleet-request":    "duplicate: scanned as aws:ec2:spot-fleet (AWS::EC2::SpotFleet)",
		"AWS::ec2::ipam-pool-allocation":  "duplicate: scanned as aws:ec2:ipam-allocation (AWS::EC2::IPAMAllocation)",
		"AWS::ec2::group":                 "duplicate: the SR ec2:group ARN is a resource-groups group, scanned as aws:resource-groups:group",
		"AWS::ec2::role":                  "duplicate: the SR ec2:role ARN is an IAM role, scanned as aws:iam:role",
		"AWS::ec2::certificate":           "duplicate: the SR ec2:certificate ARN is an ACM certificate, scanned as aws:acm:certificate",
		"AWS::ec2::license-configuration": "duplicate: the SR ec2:license-configuration ARN is a License Manager config, scanned as aws:license-manager:license-configuration",

		// ec2-instance-connect — the Service Reference lists the EC2 instances and
		// instance-connect endpoints disco already scans under aws:ec2:* (this
		// service only grants the SendSSHPublicKey action against them; the
		// resources themselves are EC2's).
		"AWS::ec2-instance-connect::instance":                  "duplicate: scanned as aws:ec2:instance (DescribeInstances)",
		"AWS::ec2-instance-connect::instance-connect-endpoint": "duplicate: scanned as aws:ec2:instance-connect-endpoint (DescribeInstanceConnectEndpoints)",

		// ecr-public — disco scans Public ECR repositories under the ecr service
		// (aws:ecr:public-repository, matching the CFN AWS::ECR::PublicRepository
		// type). The account-level registry container is not materialized as a
		// standalone resource (it carries only catalog metadata), mirroring the
		// private ECR registry which disco models via config rows, not a registry row.
		"AWS::ecr-public::repository": "duplicate: scanned as aws:ecr:public-repository (ecr-public:DescribeRepositories)",
		"AWS::ecr-public::registry":   "singleton: the account-level Public ECR registry container; disco scans its repositories, not the registry (matches the unmaterialized private ECR registry)",

		// ecs — express-gateway service has no List op (only Create/Delete/
		// Describe/Update), so it can't be enumerated; the *-deployment types are
		// ephemeral rollout records; the *-revision types are historical config
		// versions with no List op; primary-task-set is the primary designation of
		// a task set disco already scans (aws:ecs:task-set).
		"AWS::ECS::ExpressGatewayService": "no list API: only Create/Delete/Describe/Update ops — express-gateway services can't be enumerated",
		"AWS::ECS::PrimaryTaskSet":        "sub-resource: the primary designation of a service's task set, scanned as aws:ecs:task-set",
		"AWS::ecs::service-deployment":    "ephemeral: a service deployment rollout record, not a persistent resource",
		"AWS::ecs::daemon-deployment":     "ephemeral: a daemon deployment rollout record, not a persistent resource",
		"AWS::ecs::service-revision":      "no list API: a historical service-config revision, only DescribeServiceRevisions per ARN",
		"AWS::ecs::daemon-revision":       "no list API: a historical daemon-config revision, only DescribeDaemonRevisions per ARN",

		// eks — access-policy is AWS's read-only catalog of cluster access policies
		// (associated via access-entries, not an account resource); dashboard has no
		// aws-sdk-go-v2 eks op; the eks-auth service references the same clusters
		// disco scans as aws:eks:cluster.
		"AWS::eks::access-policy": "catalog: AWS's read-only list of EKS cluster access policies, not an account resource",
		"AWS::eks::dashboard":     "no SDK: no aws-sdk-go-v2 eks op backs the EKS dashboard",
		"AWS::eks-auth::cluster":  "duplicate: scanned as aws:eks:cluster (eks:DescribeCluster)",

		// directconnect — the Service Reference uses the IAM-ARN resource-type
		// abbreviations (dxcon / dxlag / dxvif / dx-gateway) for the same physical
		// resources disco already scans under their CloudFormation spellings. The
		// canonicalizer can't bridge the abbreviation, so collapse them by hand.
		"AWS::directconnect::dxcon":      "duplicate: SR abbreviation for a connection (aws:directconnect:connection)",
		"AWS::directconnect::dxlag":      "duplicate: SR abbreviation for a LAG (aws:directconnect:lag)",
		"AWS::directconnect::dxvif":      "duplicate: SR abbreviation for a virtual interface (aws:directconnect:{private,public,transit}-virtual-interface)",
		"AWS::directconnect::dx-gateway": "duplicate: SR abbreviation for a Direct Connect gateway (aws:directconnect:direct-connect-gateway)",

		// dlm — the Service Reference 'policy' is the IAM-ARN abbreviation for a
		// Data Lifecycle Manager lifecycle policy, already scanned as the CFN
		// spelling aws:dlm:lifecycle-policy.
		"AWS::dlm::policy": "duplicate: SR abbreviation for a lifecycle policy (aws:dlm:lifecycle-policy)",

		// dms — premigration assessment run records of a replication task: an
		// assessment run is a point-in-time evaluation, and an individual
		// assessment is one check within that run. Both are ephemeral run records,
		// not persistent infrastructure. (The replication task itself is scanned.)
		"AWS::dms::ReplicationTaskAssessmentRun":        "ephemeral: a premigration assessment run of a replication task, not a persistent resource",
		"AWS::dms::ReplicationTaskIndividualAssessment": "ephemeral: one check within a replication-task assessment run, not a persistent resource",

		// cloudformation — the registered types (RESOURCE/MODULE/HOOK) are scanned
		// as aws:cloudformation:type / type-hook; their versions, default-version
		// pointers, activations, configs and the publisher identity are sub-
		// resources / attributes / singletons of those, not independently listable.
		"AWS::CloudFormation::ResourceVersion":        "sub-resource: a registered type's version (the type + its DefaultVersionId are scanned as aws:cloudformation:type)",
		"AWS::CloudFormation::ModuleVersion":          "sub-resource: a registered module's version (the type is scanned as aws:cloudformation:type)",
		"AWS::CloudFormation::HookVersion":            "sub-resource: a registered hook's version (the hook is scanned as aws:cloudformation:type-hook)",
		"AWS::CloudFormation::ResourceDefaultVersion": "attribute: the default-version pointer is TypeSummary.DefaultVersionId on the scanned aws:cloudformation:type",
		"AWS::CloudFormation::ModuleDefaultVersion":   "attribute: the default-version pointer is TypeSummary.DefaultVersionId on the scanned aws:cloudformation:type",
		"AWS::CloudFormation::HookDefaultVersion":     "attribute: the default-version pointer is TypeSummary.DefaultVersionId on the scanned aws:cloudformation:type-hook",
		"AWS::CloudFormation::PublicTypeVersion":      "sub-resource: a published public version of a registered type (enumerable only per-type via ListTypeVersions); the type is scanned as aws:cloudformation:type",
		"AWS::CloudFormation::TypeActivation":         "duplicate: an activated public type is scanned as aws:cloudformation:type (Visibility=PRIVATE includes activated types)",
		"AWS::CloudFormation::HookTypeConfig":         "config: per-account hook configuration (BatchDescribeTypeConfigurations), not a standalone resource",
		"AWS::CloudFormation::Publisher":              "no list API: per-account publisher identity (DescribePublisher only)",
		"AWS::CloudFormation::GuardHook":              "duplicate: created Guard hooks are scanned as aws:cloudformation:type-hook (HOOK registry type)",
		"AWS::CloudFormation::LambdaHook":             "duplicate: created Lambda hooks are scanned as aws:cloudformation:type-hook (HOOK registry type)",
		// CFN template primitives — constructs declared inside a stack template,
		// not independently discoverable AWS resources.
		"AWS::CloudFormation::CustomResource":      "template primitive: a Lambda/SNS-backed custom resource declared in a stack, no standalone list API",
		"AWS::CloudFormation::Macro":               "no list API: a template transform macro, deployed via a stack with no ListMacros op",
		"AWS::CloudFormation::WaitCondition":       "template primitive: a provisioning-time wait condition, not a standalone resource",
		"AWS::CloudFormation::WaitConditionHandle": "template primitive: a provisioning-time wait-condition handle, not a standalone resource",
		// changeset is an ephemeral pending-change preview (auto-expires); stackset-
		// target is the OU/account a stack-set deploys to (an association).
		"AWS::cloudformation::changeset":       "ephemeral: a pending-change preview (per-stack ListChangeSets), auto-expires; not deployed infrastructure",
		"AWS::cloudformation::stackset-target": "association: the OU/account a stack-set deploys to, not a standalone resource",

		// bedrock-mantle — no aws-sdk-go-v2/service/bedrockmantle module exists,
		// so none of its types are scannable under disco's per-service-SDK mandate.
		"AWS::bedrock-mantle::project":          bedrockMantleNoSDK,
		"AWS::bedrock-mantle::customized-model": bedrockMantleNoSDK,
		"AWS::bedrock-mantle::reservation":      bedrockMantleNoSDK,
		// CloudFormation's concatenated spelling of the same no-SDK service.
		"AWS::BedrockMantle::Project": bedrockMantleNoSDK,

		// apptest — AWS Mainframe Modernization Application Testing has been
		// deprecated by AWS ("no longer available for use"); the aws-sdk-go-v2
		// client marks every symbol deprecated, so the service is not scannable.
		"AWS::apptest::TestCase":          appTestRetired,
		"AWS::apptest::TestConfiguration": appTestRetired,
		"AWS::apptest::TestRun":           appTestRetired,
		"AWS::apptest::TestSuite":         appTestRetired,

		// apprunner webacl — the WAFv2 web-ACL association for a service
		// (AssociateWebAcl); the web-ACL is scanned under aws:wafv2:web-acl and
		// App Runner exposes no op to enumerate the associations.
		"AWS::apprunner::webacl": "association: WAFv2 web-ACL association of an App Runner service; the web-ACL is scanned as aws:wafv2:web-acl",

		// apigateway — the SR lists v1 (REST) and v2 (HTTP/WebSocket) resources
		// under the one shared "apigateway" IAM prefix, so the v2 entries below are
		// the same physical resources disco already scans as aws:apigatewayv2:*
		// (the canonicalizer can't collapse these: the prefix is genuinely both v1
		// and v2, not a 1:1 service rename).
		"AWS::apigateway::Api":                  apigatewayV2Dup,
		"AWS::apigateway::Apis":                 apigatewayV2Dup,
		"AWS::apigateway::ApiMapping":           apigatewayV2Dup,
		"AWS::apigateway::ApiMappings":          apigatewayV2Dup,
		"AWS::apigateway::Integration":          apigatewayV2Dup,
		"AWS::apigateway::Integrations":         apigatewayV2Dup,
		"AWS::apigateway::IntegrationResponse":  apigatewayV2Dup,
		"AWS::apigateway::IntegrationResponses": apigatewayV2Dup,
		"AWS::apigateway::Route":                apigatewayV2Dup,
		"AWS::apigateway::Routes":               apigatewayV2Dup,
		"AWS::apigateway::RouteResponse":        apigatewayV2Dup,
		"AWS::apigateway::RouteResponses":       apigatewayV2Dup,
		"AWS::apigateway::RoutingRule":          apigatewayV2Dup,

		// apigateway sub-properties — fields of a parent resource (stage, method,
		// route), retrieved within the parent's body; no standalone List API.
		"AWS::apigateway::AccessLogSettings":     "sub-resource: stage access-log config, a field of aws:apigatewayv2:stage, not standalone",
		"AWS::apigateway::AuthorizersCache":      "sub-resource: per-stage authorizer cache (FlushStageAuthorizersCache), not a discoverable resource",
		"AWS::apigateway::Cors":                  "sub-resource: CORS config on a v2 API/route, a parent field, not standalone",
		"AWS::apigateway::MethodResponse":        "sub-resource: response of an aws:apigateway:method (GetMethodResponse needs the full method key), no list API",
		"AWS::apigateway::RouteRequestParameter": "sub-resource: request-parameter field of a v2 route, not standalone",
		"AWS::apigateway::RouteSettings":         "sub-resource: per-route settings on a v2 stage, a parent field, not standalone",
		"AWS::apigateway::Tags":                  "sub-resource: resource tags are attributes of the tagged resource, not a discoverable resource",

		// apigateway operation outputs — Get* results (export/SDK/template
		// generation), not persistent resources.
		"AWS::apigateway::ExportedAPI":   "operation output: GetExport result (an exported API definition), not a persistent resource",
		"AWS::apigateway::ModelTemplate": "operation output: GetModelTemplate result (a model's mapping template), not a persistent resource",
		"AWS::apigateway::Sdk":           "operation output: GetSdk result (a generated client SDK), not a persistent resource",
		"AWS::apigateway::Template":      "operation output: API export template, not a persistent resource",

		// apigateway Portal / Product / private-domain feature — modeled only in
		// CloudFormation / the Service Reference; no aws-sdk-go-v2 apigateway op
		// enumerates them, so they are not scannable per the SDK mandate.
		"AWS::apigateway::Portal":                  apigatewayNoSDK,
		"AWS::apigateway::PortalProduct":           apigatewayNoSDK,
		"AWS::apigateway::ProductPage":             apigatewayNoSDK,
		"AWS::apigateway::ProductRestEndpointPage": apigatewayNoSDK,
		"AWS::apigateway::PrivateDomainName":       apigatewayNoSDK,
		"AWS::apigateway::PrivateBasePathMapping":  apigatewayNoSDK,
		"AWS::apigateway::PrivateBasePathMappings": apigatewayNoSDK,
	}
}

const apigatewayV2Dup = "duplicate: API Gateway v2 resource scanned as aws:apigatewayv2:* (the SR lists v1 and v2 under the shared apigateway IAM prefix)"

const apigatewayNoSDK = "no SDK op: API Gateway Portal/Product/private-domain resource modeled only in CloudFormation/Service Reference, not in aws-sdk-go-v2"

const amplifyBackendConfig = "no list API: per-app Amplify Gen1 backend configuration (GetBackend requires AppId); config of the aws:amplify:app already scanned, not an independently-ARN'd resource"

const appmeshPreviewGone = "no SDK: appmesh-preview is the deprecated App Mesh preview API namespace (no aws-sdk-go-v2 client; GA App Mesh is the separate appmesh service)"

const appStudioNoSDK = "no SDK: AWS App Studio (low-code app builder) is console-first; no aws-sdk-go-v2 client exists"

const appTestRetired = "service retired: AWS Mainframe Modernization Application Testing deprecated by AWS (no longer available; aws-sdk-go-v2 client deprecated)"

const marketplaceCatalogOOS = "out-of-scope: AWS Marketplace seller e-commerce catalog entity (marketplacecatalog ListEntities, OwnershipType=SELF); not security infrastructure, no resource-graph edges"

const bedrockMantleNoSDK = "no SDK: bedrock-mantle has no aws-sdk-go-v2/service/bedrockmantle module (not in the public Go SDK)"

const bedrockEphemeralJob = "ephemeral: Bedrock async-invoke / flow-execution / session / *-job execution record, not a persistent resource"

const bedrockResourcePolicyNoList = "no list API: Bedrock resource-based policy has only GetResourcePolicy (no list op)"

const a4bRetired = "service retired: Alexa for Business no longer supported by AWS (SDK client retired)"

const comprehendJob = "ephemeral: an async Comprehend batch-inference job-run record, not a persistent resource"

const connectWildcard = "IAM policy wildcard ARN pattern: a wildcard resource form used in IAM authorization, not a discoverable resource"

const connectView = "IAM ARN qualification of the scanned aws:connect:view / aws:connect:view-version (managed/customer/qualified ARN spellings of the same View resources)"

const controlCatalog = "AWS-managed reference catalog: the Control Catalog lists AWS-published available controls/domains/objectives, not account-specific resources"

const dataExchangeContent = "content: versioned data within a data set (revisions/assets, like S3 object versions/objects), enumerated within the scanned aws:dataexchange:data-set"

const dataExchangeEntitled = "consumer subscription: entitled data is another account's published product you subscribe to (marketplace), not an owned resource"

const deviceFarmRun = "ephemeral: part of the Device Farm test-run hierarchy (run→job→suite→test with samples/artifacts), not persistent infrastructure"

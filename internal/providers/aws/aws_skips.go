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

		// artifact agreement — the underlying AWS-published agreement template a
		// customer-agreement is based on (referenced by its AgreementArn). The
		// artifact SDK has no ListAgreements/GetAgreement op; only customer
		// agreements (aws:artifact:customer-agreement) and reports are listable.
		"AWS::artifact::agreement": "no list API: AWS-published agreement template referenced by customer-agreement.AgreementArn; artifact SDK has no agreement list op",

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

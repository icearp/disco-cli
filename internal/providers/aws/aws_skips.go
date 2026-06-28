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
//   - duplicate:     the same physical resource disco already scans under another type.
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

		// access-analyzer (Service Reference hyphenated spelling) duplicates the
		// CFN-spelled AWS::AccessAnalyzer::Analyzer that disco already scans as
		// aws:accessanalyzer:analyzer. Same physical resource, redundant catalog
		// entry under a divergent service segment.
		"AWS::access-analyzer::Analyzer": "duplicate: already scanned as aws:accessanalyzer:analyzer (CFN spelling AWS::AccessAnalyzer::Analyzer)",

		// account (AWS Account Management) — neither entry is a discoverable
		// resource collection. The account entity itself is already modelled as
		// the aws:iam:account self-node; accountInOrganization is the same account
		// addressed via the org management context (org membership is covered by
		// the organizations scanner), not a separate resource.
		"AWS::account::account":               "duplicate: account entity modelled as the aws:iam:account self-node",
		"AWS::account::accountInOrganization": "association: account's org membership, covered via the organizations scanner",

		// acm — ACM's CloudFormation namespace is "CertificateManager", which
		// disco already scans as aws:acm:certificate. The Service Reference lists
		// the same resource under the short "acm" service segment.
		"AWS::acm::certificate": "duplicate: already scanned as aws:acm:certificate (CFN spelling AWS::CertificateManager::Certificate)",

		// acm-pca / ACMPCA (Private CA). The CA itself is already scanned as
		// aws:acm-pca:certificate-authority (CFN AWS::ACMPCA::CertificateAuthority);
		// the hyphenated Service Reference spelling is the duplicate. Issued
		// certificates have no list API (GetCertificate needs both CA and cert
		// ARN), and CertificateAuthorityActivation is the CFN-only association
		// that installs the signed cert chain on a CA, not a standalone resource.
		"AWS::acm-pca::certificate-authority":         "duplicate: already scanned as aws:acm-pca:certificate-authority (CFN spelling AWS::ACMPCA::CertificateAuthority)",
		"AWS::ACMPCA::Certificate":                    "no list API: issued private-CA certificates are not enumerable (GetCertificate requires CA ARN + cert ARN)",
		"AWS::ACMPCA::CertificateAuthorityActivation": "association: installs the signed cert chain on a CA (ImportCertificateAuthorityCertificate), not a standalone resource",

		// aco-automation — no public aws-sdk-go-v2 client, and Compute Optimizer's
		// SDK exposes no automation-rule list op; AutomationRule is modeled only in
		// CloudFormation / the Service Reference, not callable per the SDK mandate.
		"AWS::aco-automation::AutomationRule": "no SDK list op: automation rules are CFN/Service-Reference-only, not modeled in aws-sdk-go-v2",

		// DevOpsAgent (CloudFormation spelling) duplicates the private connection
		// disco scans as aws:aidevops:private-connection via the Service Reference
		// spelling. CFN models only PrivateConnection for this service.
		"AWS::DevOpsAgent::PrivateConnection": "duplicate: already scanned as aws:aidevops:private-connection (Service Reference spelling AWS::aidevops::private-connection)",

		// aiops — disco already scans aws:aiops:investigation-group, covered via
		// the CFN spelling AWS::AIOps::InvestigationGroup. The Service Reference
		// lists the same resource hyphenated under the lowercase service segment.
		"AWS::aiops::investigation-group": "duplicate: already scanned as aws:aiops:investigation-group (CFN spelling AWS::AIOps::InvestigationGroup)",

		// airflow (MWAA) — the environment is already scanned as aws:mwaa:environment
		// (CFN AWS::MWAA::Environment). rbac-role is an Airflow-internal RBAC role
		// addressable in IAM policies (airflow:role ARNs), not an AWS resource with
		// an SDK list op.
		"AWS::airflow::environment": "duplicate: already scanned as aws:mwaa:environment (CFN spelling AWS::MWAA::Environment)",
		"AWS::airflow::rbac-role":   "no SDK list op: Airflow-internal RBAC role (IAM policy reference), not a discoverable AWS resource",

		// airflow-serverless — the workflow is already scanned as
		// aws:mwaa-serverless:workflow (CFN spelling AWS::MWAAServerless::Workflow).
		// The Service Reference lists it under the hyphenated "airflow-serverless".
		"AWS::airflow-serverless::Workflow": "duplicate: already scanned as aws:mwaa-serverless:workflow (CFN spelling AWS::MWAAServerless::Workflow)",

		// amplify — the Service Reference plural spellings duplicate the CFN
		// singular types disco already scans (app/branch/domain). Jobs are
		// deployment/build run records (ephemeral), not infrastructure. Webhooks
		// are scanned (aws:amplify:webhooks).
		"AWS::amplify::apps":     "duplicate: already scanned as aws:amplify:app (CFN spelling AWS::Amplify::App)",
		"AWS::amplify::branches": "duplicate: already scanned as aws:amplify:branch (CFN spelling AWS::Amplify::Branch)",
		"AWS::amplify::domains":  "duplicate: already scanned as aws:amplify:domain (CFN spelling AWS::Amplify::Domain)",
		"AWS::amplify::jobs":     "ephemeral: deployment/build job-run records, not a persistent resource",

		// amplifyuibuilder — the Service Reference "...Resource"-suffixed spellings
		// duplicate the CFN types disco already scans (component/form/theme).
		// CodegenJob is an async code-generation run record (ephemeral).
		"AWS::amplifyuibuilder::ComponentResource":  "duplicate: already scanned as aws:amplify-ui-builder:component (CFN spelling AWS::AmplifyUIBuilder::Component)",
		"AWS::amplifyuibuilder::FormResource":       "duplicate: already scanned as aws:amplify-ui-builder:form (CFN spelling AWS::AmplifyUIBuilder::Form)",
		"AWS::amplifyuibuilder::ThemeResource":      "duplicate: already scanned as aws:amplify-ui-builder:theme (CFN spelling AWS::AmplifyUIBuilder::Theme)",
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
	}
}

const appmeshPreviewGone = "no SDK: appmesh-preview is the deprecated App Mesh preview API namespace (no aws-sdk-go-v2 client; GA App Mesh is the separate appmesh service)"

const a4bRetired = "service retired: Alexa for Business no longer supported by AWS (SDK client retired)"

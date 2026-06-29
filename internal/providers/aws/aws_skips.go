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
	}
}

const amplifyBackendConfig = "no list API: per-app Amplify Gen1 backend configuration (GetBackend requires AppId); config of the aws:amplify:app already scanned, not an independently-ARN'd resource"

const appmeshPreviewGone = "no SDK: appmesh-preview is the deprecated App Mesh preview API namespace (no aws-sdk-go-v2 client; GA App Mesh is the separate appmesh service)"

const a4bRetired = "service retired: Alexa for Business no longer supported by AWS (SDK client retired)"

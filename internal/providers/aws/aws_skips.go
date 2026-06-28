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
	}
}

const a4bRetired = "service retired: Alexa for Business no longer supported by AWS (SDK client retired)"

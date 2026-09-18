package sdkinv

// Pinned upstream refs. Bumping one changes the coverage denominator, so every
// report and the checked-in baseline print the pins; numbers are comparable
// only across identical pins. Azure has no monorepo tag, hence a commit SHA.
const (
	// AWSSDKRef is an aws-sdk-go-v2 release tag; the Smithy models under
	// codegen/sdk-codegen/aws-models are read from this snapshot.
	AWSSDKRef = "release-2026-09-15"
	// AzureSDKRef is an azure-sdk-for-go commit; sdk/resourcemanager/**/*_client.go
	// is read from this snapshot.
	AzureSDKRef = "f3847e8925003b3ae044a0ce2eaebd0e9983ed90"
	// AWSServiceReferenceDigest pins the AWS Service Reference catalog, which
	// is unversioned and served live: it is the first 12 hex digits of the
	// sha256 of service-reference/index.json. The catalog moves most days, so
	// a fresh fetch can disagree with this constant; the extractor reports the
	// disagreement and the report and baseline carry the digest the numbers
	// came from. Bump it in the same commit as the regenerated baseline.
	AWSServiceReferenceDigest = "5a4568127472"
	// GCPAPIRef is the google.golang.org/api version whose vendored Discovery
	// documents are read. The disco binary derives it from its own build info;
	// this constant is the fallback for binaries that do not link the module
	// (e.g. sdkinv's own tests) and is asserted against go.mod by a test.
	GCPAPIRef = "v0.292.0"
)

package awsinventory

// Pinned upstream refs. Bumping one changes the coverage denominator, so every
// report and the checked-in baseline print the pins; numbers are comparable
// only across identical pins.
const (
	// SDKRef is an aws-sdk-go-v2 release tag; the Smithy models under
	// codegen/sdk-codegen/aws-models are read from this snapshot.
	SDKRef = "release-2026-09-15"
	// ServiceReferenceDigest pins the AWS Service Reference catalog, which is
	// unversioned and served live: it is the first 12 hex digits of the sha256
	// of service-reference/index.json. The catalog moves most days, so a fresh
	// fetch can disagree with this constant; the extractor reports the
	// disagreement and the report and baseline carry the digest the numbers
	// came from. Bump it in the same commit as the regenerated baseline.
	ServiceReferenceDigest = "5a4568127472"
)

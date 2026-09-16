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
	// GCPAPIRef is the google.golang.org/api version whose vendored Discovery
	// documents are read. The disco binary derives it from its own build info;
	// this constant is the fallback for binaries that do not link the module
	// (e.g. sdkinv's own tests) and is asserted against go.mod by a test.
	GCPAPIRef = "v0.292.0"
)

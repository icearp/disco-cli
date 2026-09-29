package gcpinventory

// APIRef is the google.golang.org/api version whose vendored Discovery
// documents are read. The disco binary derives it from its own build info;
// this constant is the fallback for binaries that do not link the module
// (e.g. this package's own tests) and is asserted against go.mod by a test.
// Bumping it changes the coverage denominator.
const APIRef = "v0.292.0"

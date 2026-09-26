// Package all registers every SDK inventory extractor. Coverage tooling
// imports it for the side effect; the extractors import no cloud SDKs, so
// there is no slim-build gating here.
package all

import (
	// Registered for side effect: aws extractor.
	_ "github.com/icearp/disco-cli/internal/sdkinv/aws"
	// Registered for side effect: azure extractor.
	_ "github.com/icearp/disco-cli/internal/sdkinv/azure"
	// Registered for side effect: gcp extractor.
	_ "github.com/icearp/disco-cli/internal/sdkinv/gcp"
)

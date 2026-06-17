package azure

// Per-service fan-out concurrency cap. Distinct from the dispatcher-level
// caps (maxConcurrentSubscriptions, maxConcurrentServices) in azure_scanner.go,
// which bound how many subscriptions and services run in parallel.
//
// Mirrors AWS's fanoutHigh/Med/Low in internal/providers/aws/aws_concurrency.go,
// just collapsed to a single tier — Azure's per-service fan-outs (RG-fanout,
// per-VM extension calls, per-gallery image scans, etc.) all share the same
// shape, so a single tier saves the picking-a-tier judgment call.
const (
	// maxConcurrentFanout caps concurrent child API calls within a single
	// service (e.g. VM extension calls per VM, gallery image scans per
	// gallery). 50 keeps us well under Azure ARM rate limits (1200 req/min
	// per subscription) while cutting sequential fanout rounds compared to
	// the previous 20.
	maxConcurrentFanout = 50

	// maxConcurrentResolvers caps how many phase-2 relationship resolvers run
	// in parallel. Resolvers are read/parse/index-build bound and each may
	// materialise a ListResources index, so this is far lower than the per-API
	// fanout cap — 10 matches the AWS (fanoutMed) and GCP (maxConcurrentServices)
	// resolver dispatch caps for cross-provider parity.
	maxConcurrentResolvers = 10
)

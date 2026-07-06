package azure

// Per-service fan-out concurrency cap, distinct from the dispatcher-level caps
// (maxConcurrentSubscriptions, maxConcurrentServices in azure_scanner.go) that
// bound parallel subscriptions/services.
//
// Mirrors AWS's fanoutHigh/Med/Low (internal/providers/aws/aws_concurrency.go)
// collapsed to one tier — Azure's per-service fan-outs (RG-fanout, per-VM
// extension calls, per-gallery image scans, etc.) share the same shape, so one
// tier avoids a tier-picking judgment call.
const (
	// maxConcurrentFanout caps concurrent child API calls within one service
	// (e.g. VM extension calls per VM, gallery image scans per gallery). 50
	// stays well under Azure ARM rate limits (1200 req/min per subscription)
	// while cutting sequential fanout rounds vs the previous 20.
	maxConcurrentFanout = 50

	// maxConcurrentResolvers caps parallel phase-2 relationship resolvers.
	// Resolvers are read/parse/index-build bound and each may materialise a
	// ListResources index, so this is far lower than the per-API fanout cap —
	// 10 matches AWS's fanoutMed and GCP's maxConcurrentServices for
	// cross-provider parity.
	maxConcurrentResolvers = 10
)

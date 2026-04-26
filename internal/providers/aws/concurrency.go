package aws

// Per-call concurrency limits used inside individual scanners and resolvers
// when fanning out N+1 Describe/Get calls. These are distinct from
// maxConcurrentServices (in aws.go), which caps top-level service scanners
// per region.
//
// Values were tuned to balance throughput vs. AWS API throttling:
//   - fanoutHigh: chosen by APIs with generous TPS budgets (IAM Get* calls,
//     S3 per-bucket config fetches).
//   - fanoutMed: APIs with stricter throttles (S3 Control account-scope ops,
//     IAM AccessKeyLastUsed lookups in the resolver phase).
//   - fanoutLow: very slow or expensive APIs where parallelism has to stay
//     near-serial (CloudWatch Logs phase-2 enrichment).
const (
	fanoutHigh = 20
	fanoutMed  = 10
	fanoutLow  = 2
)

package aws

// Per-call concurrency limits used inside individual scanners and resolvers
// when fanning out N+1 Describe/Get calls. These are distinct from
// maxConcurrentServices (in aws.go), which caps top-level service scanners
// per region.
//
// Values were tuned to balance throughput vs. AWS API throttling:
//   - fanoutHigh: chosen by APIs with generous TPS budgets (IAM Get* calls,
//     S3 per-bucket config fetches).
//   - fanoutMed: APIs with stricter account-scoped throttles (S3 Control
//     account-scope ops, IAM AccessKeyLastUsed lookups), and per-parent
//     APIs whose TPS limit is per-parent rather than account-wide so N
//     concurrent parents consume N independent buckets (CloudWatch Logs
//     phase-2 — DescribeLogStreams / DescribeSubscriptionFilters /
//     GetTransformer are each 5 TPS *per log group*).
//   - fanoutLow: cardinality-unbounded per-parent fan-outs where bursty
//     parallelism risks DB-write or memory blow-up (Glue partition pages —
//     `glue_partitions_scanners.go`).
const (
	fanoutHigh = 20
	fanoutMed  = 10
	fanoutLow  = 2
)

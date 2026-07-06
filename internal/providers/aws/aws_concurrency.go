package aws

// Per-call concurrency limits for scanner/resolver N+1 Describe/Get
// fan-outs. Distinct from maxConcurrentServices (in aws.go), which caps
// top-level service scanners per region.
//
// Tuned to balance throughput vs. AWS API throttling:
//   - fanoutHigh: chosen by APIs with generous TPS budgets (IAM Get* calls,
//     S3 per-bucket config fetches).
//   - fanoutMed: APIs with stricter account-scoped throttles (S3 Control
//     account-scope ops, IAM AccessKeyLastUsed lookups), and per-parent
//     APIs whose TPS limit is per-parent (N concurrent parents consume N
//     independent buckets) — CloudWatch Logs phase-2 (DescribeLogStreams /
//     DescribeSubscriptionFilters / GetTransformer) is each 5 TPS *per log
//     group*.
//   - fanoutLow: cardinality-unbounded per-parent fan-outs where bursty
//     parallelism risks DB-write or memory blow-up (Glue partition pages —
//     `glue_partitions_scanners.go`).
const (
	fanoutHigh = 20
	fanoutMed  = 10
	fanoutLow  = 2

	// fanoutScope bounds the region-scoping preflight
	// (loadServiceRegionAvailability). SSM global-infrastructure params are
	// cheap, AWS-cached, read-only public reads with a generous throttle
	// budget, so wider fan-out than fanoutHigh is safe; throttles are
	// absorbed by SDK retry and the loader's per-code fail-open.
	fanoutScope = 32
)

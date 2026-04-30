package aws

import (
	"codeberg.org/icearp/disco/internal/store"
)

func init() { registerResolver(resolveApplicationSignalsRelationships) }

// resolveApplicationSignalsRelationships is a no-op by design.
//
// ServiceLevelObjectiveSummary fields surfaced by ListServiceLevelObjectives
// (Arn, Name, EvaluationType, OperationName, DependencyConfig, KeyAttributes,
// CompositeSliConfig) carry no cross-resource ARNs to resources that disco
// scans. KeyAttributes references CloudWatch Application Signals "service"
// concept (a synthetic grouping derived from telemetry), not a scanned
// resource. The full GetServiceLevelObjective response includes additional
// metric-source detail but still no ARN-bearing leaves to scanned resources.
//
// GroupingConfiguration carries (GroupingName, DefaultGroupingValue,
// GroupingSourceKeys) — purely string attribute-name metadata, no
// cross-resource ARNs.
//
// Audit: scanned applicationsignals SDK 2026-04-30. If a future SDK release
// adds an alarm ARN, lambda ARN, or other scanned-resource reference to
// SLO or GroupingConfiguration output, wire edges here.
func resolveApplicationSignalsRelationships(_ *account, _ *store.Store) error {
	return nil
}

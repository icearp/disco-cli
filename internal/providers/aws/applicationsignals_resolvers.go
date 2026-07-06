package aws

import (
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveApplicationSignalsRelationships) }

// resolveApplicationSignalsRelationships is a no-op by design.
//
// ListServiceLevelObjectives' ServiceLevelObjectiveSummary fields (Arn, Name,
// EvaluationType, OperationName, DependencyConfig, KeyAttributes,
// CompositeSliConfig) carry no cross-resource ARNs to scanned types.
// KeyAttributes refers to the Application Signals "service" — a synthetic
// telemetry-derived grouping, not a scanned resource. GetServiceLevelObjective
// adds metric-source detail but still no ARN-bearing leaves.
//
// GroupingConfiguration (GroupingName, DefaultGroupingValue,
// GroupingSourceKeys) is pure string metadata — no cross-resource ARNs.
//
// Audit: scanned applicationsignals SDK 2026-04-30. Wire edges here if a
// future SDK adds an alarm/lambda/other scanned-resource ARN to SLO or
// GroupingConfiguration output.
func resolveApplicationSignalsRelationships(_ *account, _ *store.Store) error {
	return nil
}

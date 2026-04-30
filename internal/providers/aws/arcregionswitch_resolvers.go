package aws

import (
	"codeberg.org/icearp/disco/internal/store"
)

func init() { registerResolver(resolveARCRegionSwitchRelationships) }

// resolveARCRegionSwitchRelationships is a no-op by design.
//
// AbbreviatedPlan fields surfaced by ListPlans (Arn, Name, Owner,
// RecoveryApproach, Regions, ActivePlanExecution, ExecutionRole, etc.) carry
// no cross-resource ARNs to scanned resources at this fidelity. The full
// GetPlan response embeds Workflow.Step actions with target ARNs (Lambda,
// EC2 ASG, ECS service, Route 53 health check), but that requires a per-plan
// Describe fan-out that warrants its own iteration.
//
// Audit: scanned arcregionswitch SDK 2026-04-30. Wire edges here once the
// per-plan Describe fan-out lands and the workflow-step targets are
// embedded.
func resolveARCRegionSwitchRelationships(_ *account, _ *store.Store) error {
	return nil
}

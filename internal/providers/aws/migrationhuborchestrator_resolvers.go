package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopMHOHierarchy,
		EdgeDecl{TypeMigrationHubOrchestratorWorkflow, TypeMigrationHubOrchestratorWorkflowStepGroup, store.RelContains},
		EdgeDecl{TypeMigrationHubOrchestratorTemplate, TypeMigrationHubOrchestratorTemplateStepGroup, store.RelContains},
		EdgeDecl{TypeMigrationHubOrchestratorWorkflowStepGroup, TypeMigrationHubOrchestratorWorkflowStep, store.RelContains},
		EdgeDecl{TypeMigrationHubOrchestratorTemplateStepGroup, TypeMigrationHubOrchestratorTemplateStep, store.RelContains},
	)
}

// noopMHOHierarchy emits nothing: the workflow → step-group → step and
// template → step-group → step contains edges are written at scan time by
// childFanOut (scanMHOStepGroups for the step groups, and the
// ListWorkflowSteps / ListTemplateSteps phases of scanMHOWithClient). It is
// registered only so those edges are declared to coverage tooling.
func noopMHOHierarchy(_ *account, _ *store.Store) error {
	return nil
}

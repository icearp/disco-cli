package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDataBrewRefs,
		EdgeDecl{TypeDataBrewJob, TypeDataBrewDataset, store.RelUses},
		EdgeDecl{TypeDataBrewJob, TypeDataBrewProject, store.RelUses},
		EdgeDecl{TypeDataBrewJob, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDataBrewProject, TypeDataBrewDataset, store.RelUses},
		EdgeDecl{TypeDataBrewProject, TypeDataBrewRecipe, store.RelUses},
		EdgeDecl{TypeDataBrewProject, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDataBrewRecipe, TypeDataBrewProject, store.RelAttachedTo},
		EdgeDecl{TypeDataBrewSchedule, TypeDataBrewJob, store.RelUses},
	)
}

func dbrARN(region, acct, kind, name string) string {
	return fmt.Sprintf("arn:aws:databrew:%s:%s:%s/%s", region, acct, kind, name)
}

// dataBrewTargetSets bundles FK-safe id sets for DataBrew per-source helpers.
type dataBrewTargetSets struct {
	dsSet, prjSet, rcpSet, jobSet, roleSet map[string]bool
}

func resolveDataBrewRefs(acct *account, st *store.Store) error {
	sets, err := loadDataBrewTargetSets(acct, st)
	if err != nil {
		return err
	}
	if err := emitDataBrewJobEdges(acct, st, sets); err != nil {
		return err
	}
	if err := emitDataBrewProjectEdges(acct, st, sets); err != nil {
		return err
	}
	if err := emitDataBrewRecipeEdges(acct, st, sets); err != nil {
		return err
	}
	return emitDataBrewScheduleEdges(acct, st, sets)
}

func loadDataBrewTargetSets(acct *account, st *store.Store) (dataBrewTargetSets, error) {
	var sets dataBrewTargetSets
	var err error
	if sets.dsSet, err = scannedIDSet(acct, st, TypeDataBrewDataset); err != nil {
		return sets, err
	}
	if sets.prjSet, err = scannedIDSet(acct, st, TypeDataBrewProject); err != nil {
		return sets, err
	}
	if sets.rcpSet, err = scannedIDSet(acct, st, TypeDataBrewRecipe); err != nil {
		return sets, err
	}
	if sets.jobSet, err = scannedIDSet(acct, st, TypeDataBrewJob); err != nil {
		return sets, err
	}
	if sets.roleSet, err = scannedIDSet(acct, st, TypeIAMRole); err != nil {
		return sets, err
	}
	return sets, nil
}

// emitDataBrewEdge upserts srcID→tgtType if tgtARN (dbrARN-style, may be empty)
// is in the FK-safe set.
func emitDataBrewEdge(st *store.Store, acct *account, srcID, tgtType, tgtARN string, set map[string]bool, kind string) error {
	if tgtARN == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
	if !set[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, kind, "directed", nil); err != nil {
		return fmt.Errorf("upsert databrew→%s: %w", tgtType, err)
	}
	return nil
}

func emitDataBrewJobEdges(acct *account, st *store.Store, sets dataBrewTargetSets) error {
	jobs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewJob}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range jobs {
		var attrs struct {
			DatasetName *string `json:"DatasetName"`
			ProjectName *string `json:"ProjectName"`
			RoleArn     *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if n := sv(attrs.DatasetName); n != "" {
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewDataset, dbrARN(region, acct.ID, "dataset", n), sets.dsSet, store.RelUses); err != nil {
				return err
			}
		}
		if n := sv(attrs.ProjectName); n != "" {
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewProject, dbrARN(region, acct.ID, "project", n), sets.prjSet, store.RelUses); err != nil {
				return err
			}
		}
		if err := emitDataBrewEdge(st, acct, r.ID, TypeIAMRole, sv(attrs.RoleArn), sets.roleSet, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

func emitDataBrewProjectEdges(acct *account, st *store.Store, sets dataBrewTargetSets) error {
	projects, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewProject}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range projects {
		var attrs struct {
			DatasetName *string `json:"DatasetName"`
			RecipeName  *string `json:"RecipeName"`
			RoleArn     *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if n := sv(attrs.DatasetName); n != "" {
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewDataset, dbrARN(region, acct.ID, "dataset", n), sets.dsSet, store.RelUses); err != nil {
				return err
			}
		}
		if n := sv(attrs.RecipeName); n != "" {
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewRecipe, dbrARN(region, acct.ID, "recipe", n), sets.rcpSet, store.RelUses); err != nil {
				return err
			}
		}
		if err := emitDataBrewEdge(st, acct, r.ID, TypeIAMRole, sv(attrs.RoleArn), sets.roleSet, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

func emitDataBrewRecipeEdges(acct *account, st *store.Store, sets dataBrewTargetSets) error {
	recipes, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewRecipe}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range recipes {
		var attrs struct {
			ProjectName *string `json:"ProjectName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if n := sv(attrs.ProjectName); n != "" {
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewProject, dbrARN(sv(r.Region), acct.ID, "project", n), sets.prjSet, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

func emitDataBrewScheduleEdges(acct *account, st *store.Store, sets dataBrewTargetSets) error {
	schedules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewSchedule}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range schedules {
		var attrs struct {
			JobNames []string `json:"JobNames"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, n := range attrs.JobNames {
			if n == "" {
				continue
			}
			if err := emitDataBrewEdge(st, acct, r.ID, TypeDataBrewJob, dbrARN(region, acct.ID, "job", n), sets.jobSet, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveDataBrewDatasetRefs,
		EdgeDecl{TypeDataBrewDataset, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveDataBrewRulesetTarget,
		EdgeDecl{TypeDataBrewRuleset, TypeDataBrewDataset, store.RelUses},
	)
}

// resolveDataBrewDatasetRefs wires each dataset to its S3 source bucket via
// Input.S3InputDefinition.Bucket. Glue-catalog-sourced datasets reference
// Glue tables, not first-class disco resources, so DataCatalogInputDefinition
// is skipped.
func resolveDataBrewDatasetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewDataset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Input *struct {
				S3InputDefinition *struct {
					Bucket *string `json:"Bucket"`
				} `json:"S3InputDefinition"`
			} `json:"Input"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Input == nil || attrs.Input.S3InputDefinition == nil {
			continue
		}
		b := sv(attrs.Input.S3InputDefinition.Bucket)
		if b == "" {
			continue
		}
		barn := "arn:aws:s3:::" + b
		tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, barn)
		if !bucketSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert databrew-dataset→s3: %w", err)
		}
	}
	return nil
}

// resolveDataBrewRulesetTarget wires each ruleset to its target dataset
// via TargetArn (a databrew dataset ARN).
func resolveDataBrewRulesetTarget(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataBrewRuleset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsSet, err := scannedIDSet(acct, st, TypeDataBrewDataset)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TargetArn *string `json:"TargetArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ta := sv(attrs.TargetArn)
		if ta == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeDataBrewDataset, ta)
		if !dsSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert databrew-ruleset→dataset: %w", err)
		}
	}
	return nil
}

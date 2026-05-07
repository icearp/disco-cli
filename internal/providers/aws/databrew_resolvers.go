package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
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

func resolveDataBrewRefs(acct *account, st *store.Store) error {
	dsSet, err := scannedIDSet(acct, st, TypeDataBrewDataset)
	if err != nil {
		return err
	}
	prjSet, err := scannedIDSet(acct, st, TypeDataBrewProject)
	if err != nil {
		return err
	}
	rcpSet, err := scannedIDSet(acct, st, TypeDataBrewRecipe)
	if err != nil {
		return err
	}
	jobSet, err := scannedIDSet(acct, st, TypeDataBrewJob)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}

	emit := func(srcID, tgtType, tgtARN string, set map[string]bool, kind string) error {
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

	// Jobs.
	jobs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewJob}, Limit: util.AllResources,
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
			if err := emit(r.ID, TypeDataBrewDataset, dbrARN(region, acct.ID, "dataset", n), dsSet, store.RelUses); err != nil {
				return err
			}
		}
		if n := sv(attrs.ProjectName); n != "" {
			if err := emit(r.ID, TypeDataBrewProject, dbrARN(region, acct.ID, "project", n), prjSet, store.RelUses); err != nil {
				return err
			}
		}
		if err := emit(r.ID, TypeIAMRole, sv(attrs.RoleArn), roleSet, store.RelUses); err != nil {
			return err
		}
	}

	// Projects.
	projects, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewProject}, Limit: util.AllResources,
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
			if err := emit(r.ID, TypeDataBrewDataset, dbrARN(region, acct.ID, "dataset", n), dsSet, store.RelUses); err != nil {
				return err
			}
		}
		if n := sv(attrs.RecipeName); n != "" {
			if err := emit(r.ID, TypeDataBrewRecipe, dbrARN(region, acct.ID, "recipe", n), rcpSet, store.RelUses); err != nil {
				return err
			}
		}
		if err := emit(r.ID, TypeIAMRole, sv(attrs.RoleArn), roleSet, store.RelUses); err != nil {
			return err
		}
	}

	// Recipes (back-edge to project).
	recipes, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewRecipe}, Limit: util.AllResources,
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
			if err := emit(r.ID, TypeDataBrewProject, dbrARN(sv(r.Region), acct.ID, "project", n), prjSet, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}

	// Schedules → jobs.
	schedules, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewSchedule}, Limit: util.AllResources,
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
			if err := emit(r.ID, TypeDataBrewJob, dbrARN(region, acct.ID, "job", n), jobSet, store.RelUses); err != nil {
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
// Input.S3InputDefinition.Bucket. Datasets sourced from Glue catalog
// reference Glue tables that are not first-class disco resources, so
// DataCatalogInputDefinition is skipped.
func resolveDataBrewDatasetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewDataset}, Limit: util.AllResources,
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDataBrewRuleset}, Limit: util.AllResources,
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

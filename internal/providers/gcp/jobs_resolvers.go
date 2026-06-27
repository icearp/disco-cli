package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveJobsRelationships) }

// resolveJobsRelationships derives:
//
//   - run.job   -[uses]-> service-account via template.template.serviceAccount
//   - batch.job -[uses]-> service-account via allocationPolicy.serviceAccount.email
//
// Cross-project SA refs skipped. Network edges (batch.job's
// allocationPolicy.network.networkInterfaces[].network/subnetwork) deferred —
// they reference compute network selfLinks but the lookup pattern is the same
// as compute_resolvers; landing it here would duplicate that resolver's
// shape without strong demand.
func resolveJobsRelationships(p *project, st *store.Store) error {
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	// Cloud Run Jobs.
	runJobs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudRunJob},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, j := range runJobs {
		var a struct {
			Template struct {
				Template struct {
					ServiceAccount string `json:"serviceAccount"`
				} `json:"template"`
			} `json:"template"`
		}
		if err := json.Unmarshal([]byte(j.AttributesJSON), &a); err != nil {
			continue
		}
		if email := a.Template.Template.ServiceAccount; email != "" {
			if saID, ok := saByEmail[email]; ok {
				if err := st.UpsertRelationship(j.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert run.job→SA: %w", err)
				}
			}
		}
	}

	// Batch Jobs.
	batchJobs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBatchJob},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, j := range batchJobs {
		var a struct {
			AllocationPolicy struct {
				ServiceAccount struct {
					Email string `json:"email"`
				} `json:"serviceAccount"`
			} `json:"allocationPolicy"`
		}
		if err := json.Unmarshal([]byte(j.AttributesJSON), &a); err != nil {
			continue
		}
		if email := a.AllocationPolicy.ServiceAccount.Email; email != "" {
			if saID, ok := saByEmail[email]; ok {
				if err := st.UpsertRelationship(j.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch.job→SA: %w", err)
				}
			}
		}
	}
	return nil
}

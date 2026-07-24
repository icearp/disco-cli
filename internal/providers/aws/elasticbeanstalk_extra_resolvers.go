package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBeanstalkApplicationVersionRelationships,
		EdgeDecl{TypeBeanstalkApplicationVersion, TypeBeanstalkApplication, store.RelAttachedTo},
	)
}

// resolveBeanstalkApplicationVersionRelationships wires each application version
// to its owning application (by ApplicationName — applications are ARN-keyed).
func resolveBeanstalkApplicationVersionRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBeanstalkApplicationVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appByName, err := ecByName(acct, st, TypeBeanstalkApplication)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ApplicationName *string `json:"ApplicationName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id, ok := appByName[sv(attrs.ApplicationName)]; ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert beanstalk version→application: %w", err)
			}
		}
	}
	return nil
}

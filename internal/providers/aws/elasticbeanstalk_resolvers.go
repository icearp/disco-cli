package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveBeanstalkEnvironmentTargets,
		EdgeDecl{TypeBeanstalkApplication, TypeBeanstalkEnvironment, store.RelContains},
	)
}

// beanstalkEnvAttrs mirrors verbatim EnvironmentDescription fields used
// by the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type beanstalkEnvAttrs struct {
	ApplicationName *string `json:"ApplicationName"`
	Resources       *struct {
		LoadBalancer *struct {
			LoadBalancerName *string `json:"LoadBalancerName"`
		} `json:"LoadBalancer"`
	} `json:"Resources"`
	CNAME *string `json:"CNAME"`
}

// resolveBeanstalkEnvironmentTargets emits environment outbound edges:
//   - environment → application (contains, reverse — emitted from app side
//     via ApplicationName lookup against scanned applications)
//   - environment → CloudFormation stack (uses) — Beanstalk creates an
//     underlying CFN stack named `awseb-<env-id>-stack`. Resolver does
//     NOT emit this edge here because the underlying stack ID is not
//     directly exposed in EnvironmentDescription; downstream stack→env
//     resource walker (`resolveCloudFormationStackResources` in
//     cloudformation_resolvers.go) handles the inverse if Beanstalk
//     environments ever land in `cfnTypeMap`.
//
// Application linkage: emitted as `contains` from app to env via the
// app's name keying scanned environments. FK-safe via name-keyed app
// id-set; cross-account / unscanned apps skip silently.
func resolveBeanstalkEnvironmentTargets(acct *account, st *store.Store) error {
	envs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBeanstalkEnvironment},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		return nil
	}

	// Index applications by name (Beanstalk environments reference the
	// app by name, not ARN).
	apps, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBeanstalkApplication},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	appByName := make(map[string]string, len(apps))
	for _, a := range apps {
		if a.Name != nil {
			appByName[*a.Name] = a.ID
		}
	}
	if len(appByName) == 0 {
		return nil
	}

	for _, e := range envs {
		var attrs beanstalkEnvAttrs
		if err := json.Unmarshal([]byte(e.AttributesJSON), &attrs); err != nil {
			continue
		}
		appName := sv(attrs.ApplicationName)
		if appName == "" {
			continue
		}
		appID, ok := appByName[appName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(appID, e.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert beanstalk app→env: %w", err)
		}
	}
	return nil
}

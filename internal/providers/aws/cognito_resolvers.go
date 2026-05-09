package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCognitoAppClientRelationships,
		EdgeDecl{TypeCognitoAppClient, TypeCognitoUserPool, store.RelAttachedTo},
	)
	registerResolver(
		resolveCognitoIdentityPoolRelationships,
		EdgeDecl{TypeCognitoIdentityPool, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCognitoIdentityPool, TypeCognitoUserPool, store.RelUses},
		EdgeDecl{TypeCognitoIdentityPool, TypeCognitoAppClient, store.RelUses},
	)
}

// resolveCognitoAppClientRelationships links each app client to its user pool.
// NativeID encodes both — "arn:aws:cognito-idp:<r>:<a>:userpool/<pool>/client/<id>"
// — so the pool ARN is derivable from the client's own ARN.
func resolveCognitoAppClientRelationships(acct *account, st *store.Store) error {
	clients, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCognitoAppClient},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clients {
		// Parse pool ARN from the client ARN: strip "/client/..." suffix.
		idx := strings.Index(r.NativeID, "/client/")
		if idx < 0 {
			continue
		}
		poolARN := r.NativeID[:idx]
		poolID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, poolARN)
		if err := st.UpsertRelationship(r.ID, poolID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cognito-client→user-pool: %w", err)
		}
	}
	return nil
}

// resolveCognitoIdentityPoolRelationships links each identity pool to:
//   - IAM roles from its role mappings (authenticated / unauthenticated / custom)
//   - the user pools feeding it via CognitoIdentityProviders[].ProviderName
//   - the user-pool app clients via CognitoIdentityProviders[].ClientID
func resolveCognitoIdentityPoolRelationships(acct *account, st *store.Store) error {
	pools, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCognitoIdentityPool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type idpProvider struct {
		ClientID     *string `json:"ClientID"`
		ProviderName *string `json:"ProviderName"`
	}
	type poolInner struct {
		CognitoIdentityProviders []idpProvider `json:"CognitoIdentityProviders"`
	}
	type attrs struct {
		Pool  poolInner         `json:"Pool"`
		Roles map[string]string `json:"Roles"`
	}

	for _, r := range pools {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		// IdentityPool → IAM roles
		for _, roleARN := range a.Roles {
			if roleARN == "" {
				continue
			}
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito-identity-pool→role: %w", err)
			}
		}
		// IdentityPool → UserPool + AppClient
		for _, p := range a.Pool.CognitoIdentityProviders {
			provider := sv(p.ProviderName)
			// ProviderName = "cognito-idp.<region>.amazonaws.com/<poolId>"
			slashIdx := strings.Index(provider, "/")
			if slashIdx > 0 && strings.HasPrefix(provider, "cognito-idp.") {
				// Extract region from the provider host.
				host := provider[:slashIdx]
				poolNative := provider[slashIdx+1:]
				parts := strings.SplitN(host, ".", 3)
				if len(parts) >= 3 && poolNative != "" {
					upRegion := parts[1]
					upARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", upRegion, acct.ID, poolNative)
					upID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, upARN)
					if err := st.UpsertRelationship(r.ID, upID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert cognito-identity-pool→user-pool: %w", err)
					}
					if clientID := sv(p.ClientID); clientID != "" {
						acARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/client/%s", upRegion, acct.ID, poolNative, clientID)
						acID := store.ResourceID("aws", acct.ID, TypeCognitoAppClient, acARN)
						if err := st.UpsertRelationship(r.ID, acID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert cognito-identity-pool→app-client: %w", err)
						}
					}
				}
			}
		}
	}
	return nil
}

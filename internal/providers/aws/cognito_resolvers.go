package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
// NativeID encodes both — "arn:{p}:cognito-idp:<r>:<a>:userpool/<pool>/client/<id>"
// — so the pool ARN is derivable from the client's own ARN.
func resolveCognitoAppClientRelationships(acct *account, st *store.Store) error {
	clients, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoAppClient},
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
		poolID := store.ResourceID("aws", acct.ID, poolARN)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCognitoIdentityPool},
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
			roleID := store.ResourceID("aws", acct.ID, roleARN)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito-identity-pool→role: %w", err)
			}
		}
		// IdentityPool → UserPool + AppClient
		for _, p := range a.Pool.CognitoIdentityProviders {
			upRegion, poolNative, ok := cognitoUserPoolFromProvider(acct.partition(), sv(p.ProviderName))
			if !ok {
				continue
			}
			upARN := cognitoUserPoolARN(acct.partition(), upRegion, acct.ID, poolNative)
			upID := store.ResourceID("aws", acct.ID, upARN)
			if err := st.UpsertRelationship(r.ID, upID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cognito-identity-pool→user-pool: %w", err)
			}
			if clientID := sv(p.ClientID); clientID != "" {
				acARN := cogUPARN(acct.partition(), upRegion, acct.ID, poolNative, "client", clientID)
				acID := store.ResourceID("aws", acct.ID, acARN)
				if err := st.UpsertRelationship(r.ID, acID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cognito-identity-pool→app-client: %w", err)
				}
			}
		}
	}
	return nil
}

// cognitoUserPoolFromProvider returns the region and pool id of a user pool's
// provider name, cognito-idp.{region}.{dnsSuffix}/{poolId}: an identity pool's
// ProviderName, and a JWT issuer URL once its https:// scheme is cut. ok is
// false for any other shape, including a host of another partition
// (hostRegion).
func cognitoUserPoolFromProvider(partition, provider string) (region, poolID string, ok bool) {
	host, poolID, ok := strings.Cut(provider, "/")
	if !ok || poolID == "" {
		return "", "", false
	}
	region, ok = hostRegion(partition, host, "cognito-idp.")
	if !ok {
		return "", "", false
	}
	return region, poolID, true
}

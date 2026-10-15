package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCognitoUserPool, Service: "cognito"})
	registerType(restype.Descriptor{
		Type: TypeCognitoAppClient, Service: "cognito",
		Redact: []redact.Rule{{Path: "ClientSecret", Mode: redact.RedactScalar}},
	})
	registerType(restype.Descriptor{Type: TypeCognitoIdentityPool, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolDomain, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolGroup, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolIdentityProvider, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolResourceServer, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolRiskConfigurationAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolUICustomizationAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoLogDeliveryConfiguration, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoTerms, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoIdentityPoolRoleAttachment, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoManagedLoginBranding, Service: "cognito"})
	registerType(restype.Descriptor{Type: TypeCognitoUserPoolReplica, Service: "cognito"})
	registerType(restype.Descriptor{
		Type: TypeCognitoUserPoolClientSecret, Service: "cognito",
		Redact: []redact.Rule{{Path: "ClientSecretValue", Mode: redact.RedactScalar}},
	})
	registerService(serviceEntry{
		name: "aws:cognito",
		fn:   scanCognito,
	})
}

// cognitoidpAPI is the narrow set of Cognito Identity Provider operations
// called by scanCognito. Distinct from cognitoidentityAPI — Cognito uses
// two separate SDK packages with disjoint method sets.
type cognitoidpAPI interface {
	ListUserPools(context.Context, *cognitoidp.ListUserPoolsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolsOutput, error)
	DescribeUserPool(context.Context, *cognitoidp.DescribeUserPoolInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolOutput, error)
	ListUserPoolClients(context.Context, *cognitoidp.ListUserPoolClientsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolClientsOutput, error)
	DescribeUserPoolClient(context.Context, *cognitoidp.DescribeUserPoolClientInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolClientOutput, error)
	DescribeManagedLoginBrandingByClient(context.Context, *cognitoidp.DescribeManagedLoginBrandingByClientInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeManagedLoginBrandingByClientOutput, error)
	DescribeUserPoolDomain(context.Context, *cognitoidp.DescribeUserPoolDomainInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeUserPoolDomainOutput, error)
	ListGroups(context.Context, *cognitoidp.ListGroupsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListGroupsOutput, error)
	ListIdentityProviders(context.Context, *cognitoidp.ListIdentityProvidersInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListIdentityProvidersOutput, error)
	ListResourceServers(context.Context, *cognitoidp.ListResourceServersInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListResourceServersOutput, error)
	DescribeRiskConfiguration(context.Context, *cognitoidp.DescribeRiskConfigurationInput, ...func(*cognitoidp.Options)) (*cognitoidp.DescribeRiskConfigurationOutput, error)
	GetUICustomization(context.Context, *cognitoidp.GetUICustomizationInput, ...func(*cognitoidp.Options)) (*cognitoidp.GetUICustomizationOutput, error)
	GetLogDeliveryConfiguration(context.Context, *cognitoidp.GetLogDeliveryConfigurationInput, ...func(*cognitoidp.Options)) (*cognitoidp.GetLogDeliveryConfigurationOutput, error)
	ListTerms(context.Context, *cognitoidp.ListTermsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListTermsOutput, error)
	ListUserPoolReplicas(context.Context, *cognitoidp.ListUserPoolReplicasInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolReplicasOutput, error)
	ListUserPoolClientSecrets(context.Context, *cognitoidp.ListUserPoolClientSecretsInput, ...func(*cognitoidp.Options)) (*cognitoidp.ListUserPoolClientSecretsOutput, error)
}

// cognitoidentityAPI is the narrow set of Cognito Identity (federated
// identity pools) operations called by scanCognito.
type cognitoidentityAPI interface {
	ListIdentityPools(context.Context, *cognitoidentity.ListIdentityPoolsInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.ListIdentityPoolsOutput, error)
	DescribeIdentityPool(context.Context, *cognitoidentity.DescribeIdentityPoolInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.DescribeIdentityPoolOutput, error)
	GetIdentityPoolRoles(context.Context, *cognitoidentity.GetIdentityPoolRolesInput, ...func(*cognitoidentity.Options)) (*cognitoidentity.GetIdentityPoolRolesOutput, error)
}

// scanCognito discovers Cognito user pools, user-pool app clients, and
// identity pools in one region. User pools and their app clients are
// list-then-described concurrently. Identity pools are paired with their role
// mappings via GetIdentityPoolRoles for the resolver.
func scanCognito(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	idpClient := cognitoidp.NewFromConfig(acct.cfg, func(o *cognitoidp.Options) { o.Region = region })
	idClient := cognitoidentity.NewFromConfig(acct.cfg, func(o *cognitoidentity.Options) { o.Region = region })
	return scanCognitoAll(ctx, idpClient, idClient, acct, region, st, scanID)
}

// scanCognitoAll holds the testable scan body — accepts both narrow ifaces
// so unit tests can swap in stubs without touching production wiring.
//
// The replica and client-secret phases run after every older phase, over the
// pools and clients the pool phase stored, so an identity-pool or extended
// phase failure does not skip them and their failure costs no older phase its
// rows. The older phases' transient error is demoted to a warning, since one
// transient member would make the dispatcher read the whole join as transient
// and drop every phase's counts; the errors are joined and the counts summed.
func scanCognitoAll(ctx context.Context, idpClient cognitoidpAPI, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pools, ferr := listCognitoUserPoolIDs(ctx, idpClient, acct, region, st)
	if ferr != nil {
		return 0, 0, ferr
	}
	total, inserted, stored, err := scanCognitoOlderPhases(ctx, idpClient, idClient, acct, region, st, scanID, pools.ids)
	errs := []error{demoteTransient(st, "cognito-idp", acct, region, err)}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanCognitoUserPoolReplicas(ctx, idpClient, acct, region, st, scanID, stored.replicatedPools(pools.replicated))
		},
		func() (int, int, error) {
			return scanCognitoUserPoolClientSecrets(ctx, idpClient, acct, region, st, scanID, stored.confidentialClients)
		},
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// scanCognitoOlderPhases runs the phases that predate the replica and
// client-secret phases, stopping at the first hard error. It also returns the
// pools and app clients the pool phase stored.
func scanCognitoOlderPhases(ctx context.Context, idpClient cognitoidpAPI, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (total, inserted int, stored cognitoStoredParents, err error) {
	stored, total, inserted, err = scanCognitoUserPoolsAndClients(ctx, idpClient, acct, region, st, scanID, poolIDs)
	if err != nil {
		return total, inserted, stored, err
	}
	idPoolIDs, t, i, err := scanCognitoIdentityPools(ctx, idClient, acct, region, st, scanID)
	total += t
	inserted += i
	if err != nil {
		return total, inserted, stored, err
	}
	t, i, err = scanCognitoExtended(ctx, idpClient, idClient, acct, region, st, scanID, poolIDs, idPoolIDs)
	return total + t, inserted + i, stored, err
}

// cognitoListedPools is the ListUserPools result: every pool ID, and the IDs
// whose ReplicaRegions names at least one region.
type cognitoListedPools struct {
	ids        []string
	replicated map[string]bool
}

func listCognitoUserPoolIDs(ctx context.Context, idpClient cognitoidpAPI, acct *account, region string, st *store.Store) (cognitoListedPools, error) {
	pools := cognitoListedPools{replicated: map[string]bool{}}
	pager := cognitoidp.NewListUserPoolsPaginator(idpClient, &cognitoidp.ListUserPoolsInput{MaxResults: aws.Int32(60)})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return cognitoListedPools{}, skipIfAccessDenied(st, "cognito-idp:ListUserPools", acct.ID, region, err)
			}
			return cognitoListedPools{}, fmt.Errorf("cognito-idp:ListUserPools: %w", err)
		}
		for _, p := range out.UserPools {
			pools.ids = append(pools.ids, sv(p.Id))
			if len(p.ReplicaRegions) > 0 && cognitoPoolCreatedIn(sv(p.Id), region) {
				pools.replicated[sv(p.Id)] = true
			}
		}
	}
	return pools, nil
}

// cognitoPoolCreatedIn reports whether the pool ID's "<region>_" prefix names
// region. ReplicaRegions may also be reported on a secondary replica's pool,
// whose ListUserPoolReplicas may be refused; only a pool created in this
// region is taken to be a primary. Inferred from the pool ID format, pending
// a live check of how a secondary's ID and ListUserPools entry look.
func cognitoPoolCreatedIn(poolID, region string) bool {
	prefix, _, ok := strings.Cut(poolID, "_")
	return ok && prefix == region
}

// cognitoStoredParents is what the pool phase stored: each user pool (id =
// pool ID, arn = its NativeID) and each app client that has a secret. Only
// those clients are asked for client secrets: the docs describe secrets as
// belonging to confidential clients, inferred here from a non-empty
// ClientSecret on DescribeUserPoolClient. Unverified for clients managed with
// the multi-secret APIs, which might not populate that field.
type cognitoStoredParents struct {
	pools               []childParent
	confidentialClients []cognitoAppClientRef
}

// cognitoAppClientRef is a stored app client: ListUserPoolClientSecrets keys
// on both the pool ID and the client ID.
type cognitoAppClientRef struct {
	poolID   string
	clientID string
	arn      string
}

// replicatedPools returns the stored pools that ListUserPools reported
// replicas for.
func (s cognitoStoredParents) replicatedPools(replicated map[string]bool) []childParent {
	var out []childParent
	for _, p := range s.pools {
		if replicated[p.id] {
			out = append(out, p)
		}
	}
	return out
}

// cognitoPoolBatches collects the pool phase's rows from its per-pool
// goroutines.
type cognitoPoolBatches struct {
	mu        sync.Mutex
	pools     []*store.Resource
	clients   []*store.Resource
	brandings []*store.Resource
	stored    cognitoStoredParents
}

func scanCognitoUserPoolsAndClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (cognitoStoredParents, int, int, error) {
	var b cognitoPoolBatches
	g, gctx := errgroup.WithContext(ctx)
	for _, poolID := range poolIDs {
		g.Go(func() error {
			return collectCognitoPoolAndClients(gctx, idpClient, acct, region, scanID, poolID, &b)
		})
	}
	if err := g.Wait(); err != nil {
		return cognitoStoredParents{}, 0, 0, err
	}
	var total, inserted int
	for _, kb := range []struct {
		label string
		batch []*store.Resource
	}{
		{"Cognito user pools", b.pools},
		{"Cognito app clients", b.clients},
		{"Cognito managed-login-branding", b.brandings},
	} {
		if len(kb.batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(kb.batch)
		if err != nil {
			return cognitoStoredParents{}, total, inserted, fmt.Errorf("upsert %s: %w", kb.label, err)
		}
		total += len(kb.batch)
		inserted += n
	}
	return b.stored, total, inserted, nil
}

func collectCognitoPoolAndClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID string, b *cognitoPoolBatches) error {
	poolOut, err := idpClient.DescribeUserPool(ctx, &cognitoidp.DescribeUserPoolInput{UserPoolId: &poolID})
	if err != nil {
		if isAccessDenied(err) {
			return nil
		}
		return fmt.Errorf("cognito-idp:DescribeUserPool %s: %w", poolID, err)
	}
	poolARN := sv(poolOut.UserPool.Arn)
	if poolARN == "" {
		poolARN = fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, acct.ID, poolID)
	}
	pr := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeCognitoUserPool,
		NativeID:       poolARN,
		Name:           poolOut.UserPool.Name,
		Region:         &region,
		AttributesJSON: mustJSON(poolOut.UserPool),
		DiscoveredBy:   scanID,
	}
	b.mu.Lock()
	b.pools = append(b.pools, pr)
	b.stored.pools = append(b.stored.pools, childParent{id: poolID, arn: poolARN})
	b.mu.Unlock()
	return collectCognitoAppClients(ctx, idpClient, acct, region, scanID, poolID, b)
}

func collectCognitoAppClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID string, b *cognitoPoolBatches) error {
	cPager := cognitoidp.NewListUserPoolClientsPaginator(idpClient, &cognitoidp.ListUserPoolClientsInput{
		UserPoolId: &poolID,
		MaxResults: aws.Int32(60),
	})
	for cPager.HasMorePages() {
		cOut, err := cPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("cognito-idp:ListUserPoolClients pool=%s: %w", poolID, err)
		}
		for _, c := range cOut.UserPoolClients {
			clientID := sv(c.ClientId)
			descOut, err := idpClient.DescribeUserPoolClient(ctx, &cognitoidp.DescribeUserPoolClientInput{
				UserPoolId: &poolID,
				ClientId:   &clientID,
			})
			if err != nil {
				continue
			}
			arn := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/client/%s", region, acct.ID, poolID, clientID)
			cr := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCognitoAppClient,
				NativeID:       arn,
				Name:           descOut.UserPoolClient.ClientName,
				Region:         &region,
				AttributesJSON: mustJSON(descOut.UserPoolClient),
				DiscoveredBy:   scanID,
			}
			b.mu.Lock()
			b.clients = append(b.clients, cr)
			// Inferred confidential-client gate; see cognitoStoredParents.
			if sv(descOut.UserPoolClient.ClientSecret) != "" {
				b.stored.confidentialClients = append(b.stored.confidentialClients, cognitoAppClientRef{poolID: poolID, clientID: clientID, arn: arn})
			}
			b.mu.Unlock()
			collectCognitoManagedLoginBranding(ctx, idpClient, acct, region, scanID, poolID, clientID, b)
		}
	}
	return nil
}

// collectCognitoManagedLoginBranding fetches the per-app-client branding
// row, silently skipping NoSuchManagedLoginBranding (default state when
// the client has no branding configured).
func collectCognitoManagedLoginBranding(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID, clientID string, b *cognitoPoolBatches) {
	mlbOut, mlbErr := idpClient.DescribeManagedLoginBrandingByClient(ctx, &cognitoidp.DescribeManagedLoginBrandingByClientInput{
		UserPoolId: &poolID,
		ClientId:   &clientID,
	})
	if mlbErr != nil || mlbOut.ManagedLoginBranding == nil {
		return
	}
	mlbID := sv(mlbOut.ManagedLoginBranding.ManagedLoginBrandingId)
	if mlbID == "" {
		return
	}
	mlbArn := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/branding/%s", region, acct.ID, poolID, mlbID)
	mlbLabel := mlbID
	b.mu.Lock()
	b.brandings = append(b.brandings, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCognitoManagedLoginBranding, NativeID: mlbArn,
		Name: &mlbLabel, Region: &region,
		AttributesJSON: mustJSON(mlbOut.ManagedLoginBranding), DiscoveredBy: scanID,
	})
	b.mu.Unlock()
}

func scanCognitoIdentityPools(ctx context.Context, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	type idPoolAttrs struct {
		Pool  any               `json:"Pool"`
		Roles map[string]string `json:"Roles"`
	}
	var batch []*store.Resource
	var idPoolIDs []string
	pager := cognitoidentity.NewListIdentityPoolsPaginator(idClient, &cognitoidentity.ListIdentityPoolsInput{MaxResults: aws.Int32(60)})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return nil, 0, 0, fmt.Errorf("cognito-identity:ListIdentityPools: %w", err)
		}
		for _, p := range out.IdentityPools {
			ipID := sv(p.IdentityPoolId)
			idPoolIDs = append(idPoolIDs, ipID)
			descOut, err := idClient.DescribeIdentityPool(ctx, &cognitoidentity.DescribeIdentityPoolInput{IdentityPoolId: &ipID})
			if err != nil {
				continue
			}
			var roles map[string]string
			if rOut, rErr := idClient.GetIdentityPoolRoles(ctx, &cognitoidentity.GetIdentityPoolRolesInput{IdentityPoolId: &ipID}); rErr == nil {
				roles = rOut.Roles
			}
			arn := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", region, acct.ID, ipID)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCognitoIdentityPool,
				NativeID:       arn,
				Name:           descOut.IdentityPoolName,
				Region:         &region,
				AttributesJSON: mustJSON(idPoolAttrs{Pool: descOut, Roles: roles}),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return idPoolIDs, 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return idPoolIDs, 0, 0, fmt.Errorf("upsert Cognito identity pools: %w", err)
	}
	return idPoolIDs, len(batch), n, nil
}

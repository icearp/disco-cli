package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCognitoUserPool, Service: "cognito", Upstream: "AWS::Cognito::UserPool"})
	registerType(restype.Descriptor{Type: TypeCognitoAppClient, Service: "cognito", Upstream: "AWS::Cognito::UserPoolClient"})
	registerType(restype.Descriptor{Type: TypeCognitoIdentityPool, Service: "cognito", Upstream: "AWS::Cognito::IdentityPool"})
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
func scanCognitoAll(ctx context.Context, idpClient cognitoidpAPI, idClient cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	poolIDs, ferr := listCognitoUserPoolIDs(ctx, idpClient, acct, region, st)
	if ferr != nil {
		return 0, 0, ferr
	}
	t, i, ferr := scanCognitoUserPoolsAndClients(ctx, idpClient, acct, region, st, scanID, poolIDs)
	total += t
	inserted += i
	if ferr != nil {
		return total, inserted, ferr
	}
	idPoolIDs, t, i, ferr := scanCognitoIdentityPools(ctx, idClient, acct, region, st, scanID)
	total += t
	inserted += i
	if ferr != nil {
		return total, inserted, ferr
	}
	t, i, ferr = scanCognitoExtended(ctx, idpClient, idClient, acct, region, st, scanID, poolIDs, idPoolIDs)
	total += t
	inserted += i
	return total, inserted, ferr
}

func listCognitoUserPoolIDs(ctx context.Context, idpClient cognitoidpAPI, acct *account, region string, st *store.Store) ([]string, error) {
	var poolIDs []string
	pager := cognitoidp.NewListUserPoolsPaginator(idpClient, &cognitoidp.ListUserPoolsInput{MaxResults: aws.Int32(60)})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied(st, "cognito-idp:ListUserPools", acct.ID, region, err)
			}
			return nil, fmt.Errorf("cognito-idp:ListUserPools: %w", err)
		}
		for _, p := range out.UserPools {
			poolIDs = append(poolIDs, sv(p.Id))
		}
	}
	return poolIDs, nil
}

func scanCognitoUserPoolsAndClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var (
		mu          sync.Mutex
		poolBatch   []*store.Resource
		clientBatch []*store.Resource
		mlbBatch    []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, poolID := range poolIDs {
		g.Go(func() error {
			return collectCognitoPoolAndClients(gctx, idpClient, acct, region, scanID, poolID, &mu, &poolBatch, &clientBatch, &mlbBatch)
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	var total, inserted int
	for _, kb := range []struct {
		label string
		batch []*store.Resource
	}{
		{"Cognito user pools", poolBatch},
		{"Cognito app clients", clientBatch},
		{"Cognito managed-login-branding", mlbBatch},
	} {
		if len(kb.batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(kb.batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", kb.label, err)
		}
		total += len(kb.batch)
		inserted += n
	}
	return total, inserted, nil
}

func collectCognitoPoolAndClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID string, mu *sync.Mutex, poolBatch, clientBatch, mlbBatch *[]*store.Resource) error {
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
	mu.Lock()
	*poolBatch = append(*poolBatch, pr)
	mu.Unlock()
	return collectCognitoAppClients(ctx, idpClient, acct, region, scanID, poolID, mu, clientBatch, mlbBatch)
}

func collectCognitoAppClients(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID string, mu *sync.Mutex, clientBatch, mlbBatch *[]*store.Resource) error {
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
			mu.Lock()
			*clientBatch = append(*clientBatch, cr)
			mu.Unlock()
			collectCognitoManagedLoginBranding(ctx, idpClient, acct, region, scanID, poolID, clientID, mu, mlbBatch)
		}
	}
	return nil
}

// collectCognitoManagedLoginBranding fetches the per-app-client branding
// row, silently skipping NoSuchManagedLoginBranding (default state when
// the client has no branding configured).
func collectCognitoManagedLoginBranding(ctx context.Context, idpClient cognitoidpAPI, acct *account, region, scanID, poolID, clientID string, mu *sync.Mutex, mlbBatch *[]*store.Resource) {
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
	mu.Lock()
	*mlbBatch = append(*mlbBatch, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCognitoManagedLoginBranding, NativeID: mlbArn,
		Name: &mlbLabel, Region: &region,
		AttributesJSON: mustJSON(mlbOut.ManagedLoginBranding), DiscoveredBy: scanID,
	})
	mu.Unlock()
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

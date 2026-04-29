package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cognito",
		fn:   scanCognito,
		emits: []coverage.TypeDecl{
			{Service: "cognito", DiscoType: TypeCognitoUserPool},
			{Service: "cognito", DiscoType: TypeCognitoAppClient},
			{Service: "cognito", DiscoType: TypeCognitoIdentityPool},
		},
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

	// Phase 1: list user pool IDs.
	var poolIDs []string
	upPager := cognitoidp.NewListUserPoolsPaginator(idpClient, &cognitoidp.ListUserPoolsInput{MaxResults: aws.Int32(60)})
	for upPager.HasMorePages() {
		out, err := upPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cognito-idp:ListUserPools", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cognito-idp:ListUserPools: %w", err)
		}
		for _, p := range out.UserPools {
			poolIDs = append(poolIDs, sv(p.Id))
		}
	}

	// Phase 2: describe user pools + list/describe app clients (concurrent per pool).
	var (
		mu          sync.Mutex
		poolBatch   []*store.Resource
		clientBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, poolID := range poolIDs {
		g.Go(func() error {
			poolOut, err := idpClient.DescribeUserPool(gctx, &cognitoidp.DescribeUserPoolInput{UserPoolId: &poolID})
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
			poolBatch = append(poolBatch, pr)
			mu.Unlock()

			// List app clients for this pool.
			cPager := cognitoidp.NewListUserPoolClientsPaginator(idpClient, &cognitoidp.ListUserPoolClientsInput{
				UserPoolId: &poolID,
				MaxResults: aws.Int32(60),
			})
			for cPager.HasMorePages() {
				cOut, err := cPager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						break
					}
					return fmt.Errorf("cognito-idp:ListUserPoolClients pool=%s: %w", poolID, err)
				}
				for _, c := range cOut.UserPoolClients {
					clientID := sv(c.ClientId)
					descOut, err := idpClient.DescribeUserPoolClient(gctx, &cognitoidp.DescribeUserPoolClientInput{
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
					clientBatch = append(clientBatch, cr)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(poolBatch) > 0 {
		n, err := st.UpsertResources(poolBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Cognito user pools: %w", err)
		}
		total += len(poolBatch)
		inserted += n
	}
	if len(clientBatch) > 0 {
		n, err := st.UpsertResources(clientBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Cognito app clients: %w", err)
		}
		total += len(clientBatch)
		inserted += n
	}

	// Phase 3: identity pools.
	var idBatch []*store.Resource
	ipPager := cognitoidentity.NewListIdentityPoolsPaginator(idClient, &cognitoidentity.ListIdentityPoolsInput{MaxResults: aws.Int32(60)})
	for ipPager.HasMorePages() {
		out, err := ipPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return 0, 0, fmt.Errorf("cognito-identity:ListIdentityPools: %w", err)
		}
		for _, p := range out.IdentityPools {
			ipID := sv(p.IdentityPoolId)
			descOut, err := idClient.DescribeIdentityPool(ctx, &cognitoidentity.DescribeIdentityPoolInput{IdentityPoolId: &ipID})
			if err != nil {
				continue
			}
			// Pair pool description with role mappings for the resolver.
			var roles map[string]string
			if rOut, rErr := idClient.GetIdentityPoolRoles(ctx, &cognitoidentity.GetIdentityPoolRolesInput{IdentityPoolId: &ipID}); rErr == nil {
				roles = rOut.Roles
			}
			type idPoolAttrs struct {
				Pool  any               `json:"Pool"`
				Roles map[string]string `json:"Roles"`
			}
			attrs := idPoolAttrs{Pool: descOut, Roles: roles}
			arn := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", region, acct.ID, ipID)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCognitoIdentityPool,
				NativeID:       arn,
				Name:           descOut.IdentityPoolName,
				Region:         &region,
				AttributesJSON: mustJSON(attrs),
				DiscoveredBy:   scanID,
			}
			idBatch = append(idBatch, r)
		}
	}
	if len(idBatch) > 0 {
		n, err := st.UpsertResources(idBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Cognito identity pools: %w", err)
		}
		total += len(idBatch)
		inserted += n
	}

	return total, inserted, nil
}

package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() { registerService(serviceEntry{name: "aws:ses", fn: scanSES}) }

// sesv2API is the narrow set of SES v2 operations called by the scanSES
// sub-phases.
type sesv2API interface {
	ListEmailIdentities(context.Context, *sesv2.ListEmailIdentitiesInput, ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
	GetEmailIdentity(context.Context, *sesv2.GetEmailIdentityInput, ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
	ListConfigurationSets(context.Context, *sesv2.ListConfigurationSetsInput, ...func(*sesv2.Options)) (*sesv2.ListConfigurationSetsOutput, error)
	GetConfigurationSet(context.Context, *sesv2.GetConfigurationSetInput, ...func(*sesv2.Options)) (*sesv2.GetConfigurationSetOutput, error)
}

// scanSES discovers SES v2 email identities and configuration sets in one
// region. Two phases run sequentially. Phase-level AccessDenied is tolerated
// via skipIfAccessDenied without barring the other phase.
func scanSES(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sesv2.NewFromConfig(acct.cfg, func(o *sesv2.Options) { o.Region = region })

	if t, i, ferr := scanSESEmailIdentities(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanSESConfigurationSets(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// sesEmailIdentityARN reconstructs the canonical email-identity ARN from an
// IdentityName (email or domain). SES v2 List/Get APIs return the bare name;
// the canonical ARN is needed for cross-resource edges (e.g. CloudFront
// authorizers, Pinpoint, IAM policy doc walkers).
func sesEmailIdentityARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", region, accountID, name)
}

func sesConfigurationSetARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", region, accountID, name)
}

func scanSESEmailIdentities(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := sesv2.NewListEmailIdentitiesPaginator(client, &sesv2.ListEmailIdentitiesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListEmailIdentities", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListEmailIdentities: %w", perr)
		}
		for _, id := range out.EmailIdentities {
			if id.IdentityName != nil {
				names = append(names, *id.IdentityName)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetEmailIdentity(gctx, &sesv2.GetEmailIdentityInput{EmailIdentity: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sesv2:GetEmailIdentity %s: %w", name, derr)
			}
			arn := sesEmailIdentityARN(region, acct.ID, name)
			status := string(out.VerificationStatus)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSESEmailIdentity,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses email identities: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSESConfigurationSets(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := sesv2.NewListConfigurationSetsPaginator(client, &sesv2.ListConfigurationSetsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListConfigurationSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListConfigurationSets: %w", perr)
		}
		names = append(names, out.ConfigurationSets...)
	}
	if len(names) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetConfigurationSet(gctx, &sesv2.GetConfigurationSetInput{ConfigurationSetName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sesv2:GetConfigurationSet %s: %w", name, derr)
			}
			arn := sesConfigurationSetARN(region, acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSESConfigurationSet,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses configuration sets: %w", uerr)
	}
	return len(batch), n, nil
}

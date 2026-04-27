package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/controltower"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() { registerService(serviceEntry{name: "aws:controltower", fn: scanControlTower}) }

// isControlTowerNotEnabled disambiguates the "Control Tower not deployed
// in this account" state from real errors. Two surface shapes seen in
// practice:
//
//   - AccessDeniedException with phrasing about management account /
//     landing zone setup (account is not the org management account).
//   - ValidationException with phrasing about the AWSControlTowerAdmin
//     role (Control Tower is not yet bootstrapped in the calling
//     account/region).
//
// Both treated as "service disabled" for progress reporting.
func isControlTowerNotEnabled(err error) bool {
	msg := err.Error()
	ctSetupHint := strings.Contains(msg, "AWSControlTowerAdmin") ||
		strings.Contains(msg, "management account") ||
		strings.Contains(msg, "landing zone") ||
		strings.Contains(msg, "AWS Control Tower")
	if !ctSetupHint {
		return false
	}
	return isAccessDenied(err) || isAPIErrorCode(err, "ValidationException")
}

// scanControlTower discovers Control Tower landing zones and enabled
// baselines in one region (typically the home region of the landing
// zone — usually us-east-1). Two phases. Enabled controls deferred —
// `ListEnabledControls` requires a per-OU `TargetIdentifier` parameter,
// so it would need a fan-out keyed off baseline targets or a separate
// org-OU enumeration; defensible to land in a follow-up iteration.
func scanControlTower(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := controltower.NewFromConfig(acct.cfg, func(o *controltower.Options) { o.Region = region })

	if t, i, ferr := scanControlTowerLandingZones(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanControlTowerEnabledBaselines(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanControlTowerLandingZones(ctx context.Context, client *controltower.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := controltower.NewListLandingZonesPaginator(client, &controltower.ListLandingZonesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isControlTowerNotEnabled(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "controltower:ListLandingZones", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("controltower:ListLandingZones: %w", perr)
		}
		for _, lz := range out.LandingZones {
			if lz.Arn != nil {
				arns = append(arns, *lz.Arn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetLandingZone(gctx, &controltower.GetLandingZoneInput{LandingZoneIdentifier: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("controltower:GetLandingZone %s: %w", arn, derr)
			}
			if out.LandingZone == nil {
				return nil
			}
			status := string(out.LandingZone.Status)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeControlTowerLandingZone,
				NativeID:       arn,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.LandingZone),
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
		return 0, 0, fmt.Errorf("upsert controltower landing zones: %w", uerr)
	}
	return len(batch), n, nil
}

func scanControlTowerEnabledBaselines(ctx context.Context, client *controltower.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := controltower.NewListEnabledBaselinesPaginator(client, &controltower.ListEnabledBaselinesInput{IncludeChildren: true})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isControlTowerNotEnabled(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "controltower:ListEnabledBaselines", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("controltower:ListEnabledBaselines: %w", perr)
		}
		for _, b := range out.EnabledBaselines {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeControlTowerEnabledBaseline,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert controltower enabled baselines: %w", uerr)
	}
	return len(batch), n, nil
}

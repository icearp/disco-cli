package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/auditmanager"
	amtypes "github.com/aws/aws-sdk-go-v2/service/auditmanager/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// isAuditManagerNotEnabled disambiguates the "Audit Manager not set up in
// this account" state from real IAM denial: both surface as
// AccessDeniedException, but the not-enabled variant carries the
// "complete AWS Audit Manager setup" message. Same pattern as
// isMacieNotEnabled (per aws/CLAUDE.md "Macie variant — code+message
// disambiguation").
func isAuditManagerNotEnabled(err error) bool {
	return isAccessDenied(err) && strings.Contains(err.Error(), "complete AWS Audit Manager setup")
}

func init() { registerService(serviceEntry{name: "aws:auditmanager", fn: scanAuditManager}) }

// auditmanagerAPI is the narrow set of Audit Manager operations called by
// the scanAuditManager sub-phases.
type auditmanagerAPI interface {
	ListAssessments(context.Context, *auditmanager.ListAssessmentsInput, ...func(*auditmanager.Options)) (*auditmanager.ListAssessmentsOutput, error)
	GetAssessment(context.Context, *auditmanager.GetAssessmentInput, ...func(*auditmanager.Options)) (*auditmanager.GetAssessmentOutput, error)
	ListAssessmentFrameworks(context.Context, *auditmanager.ListAssessmentFrameworksInput, ...func(*auditmanager.Options)) (*auditmanager.ListAssessmentFrameworksOutput, error)
	ListControls(context.Context, *auditmanager.ListControlsInput, ...func(*auditmanager.Options)) (*auditmanager.ListControlsOutput, error)
}

// scanAuditManager discovers Audit Manager assessments, custom frameworks,
// and custom controls in one region. Three phases run sequentially.
//
// Phase 1: ListAssessments (paginator, skeleton) → fan-out GetAssessment
// (errgroup + fanoutMed) for full Assessment body — needed for framework
// ARN, role list, and S3 reports destination edges.
// Phase 2: ListAssessmentFrameworks(type=Custom). Standard frameworks
// (PCI-DSS, HIPAA, etc.) deliberately skipped — AWS-managed catalogue,
// huge volume, low graph value.
// Phase 3: ListControls(type=Custom). Standard controls likewise skipped.
//
// Per-phase AccessDenied tolerated. Assessment reports, share requests,
// delegations, and evidence collection deferred — event/state data, not
// graph nodes.
func scanAuditManager(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := auditmanager.NewFromConfig(acct.cfg, func(o *auditmanager.Options) { o.Region = region })

	if t, i, ferr := scanAuditManagerAssessments(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanAuditManagerFrameworks(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanAuditManagerControls(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanAuditManagerAssessments(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := auditmanager.NewListAssessmentsPaginator(client, &auditmanager.ListAssessmentsInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAuditManagerNotEnabled(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "auditmanager:ListAssessments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("auditmanager:ListAssessments: %w", perr)
		}
		for _, a := range out.AssessmentMetadata {
			if a.Id != nil {
				ids = append(ids, *a.Id)
			}
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, id := range ids {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetAssessment(gctx, &auditmanager.GetAssessmentInput{AssessmentId: &id})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("auditmanager:GetAssessment %s: %w", id, derr)
			}
			if out.Assessment == nil {
				return nil
			}
			arn := sv(out.Assessment.Arn)
			if arn == "" {
				return nil
			}
			var name string
			status := ""
			if out.Assessment.Metadata != nil {
				name = sv(out.Assessment.Metadata.Name)
				status = string(out.Assessment.Metadata.Status)
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAuditManagerAssessment,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.Assessment),
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
		return 0, 0, fmt.Errorf("upsert auditmanager assessments: %w", uerr)
	}
	return len(batch), n, nil
}

func scanAuditManagerFrameworks(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := auditmanager.NewListAssessmentFrameworksPaginator(client, &auditmanager.ListAssessmentFrameworksInput{
		FrameworkType: amtypes.FrameworkTypeCustom,
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAuditManagerNotEnabled(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "auditmanager:ListAssessmentFrameworks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("auditmanager:ListAssessmentFrameworks: %w", perr)
		}
		for _, f := range out.FrameworkMetadataList {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			name := sv(f.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAuditManagerFramework,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert auditmanager frameworks: %w", uerr)
	}
	return len(batch), n, nil
}

func scanAuditManagerControls(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := auditmanager.NewListControlsPaginator(client, &auditmanager.ListControlsInput{
		ControlType: amtypes.ControlTypeCustom,
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAuditManagerNotEnabled(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "auditmanager:ListControls", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("auditmanager:ListControls: %w", perr)
		}
		for _, c := range out.ControlMetadataList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			name := sv(c.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAuditManagerControl,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert auditmanager controls: %w", uerr)
	}
	return len(batch), n, nil
}

package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/macie2"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// isMacieNotEnabled reports whether err is the AccessDeniedException variant
// that Macie raises when the service is not enabled in the calling region.
// Macie collides on AccessDeniedException for both real IAM denial AND the
// not-enabled feature-gate state, so disambiguate by message substring
// (precedent: isCacheSecurityGroupsNotPermitted in elasticache_scanners.go,
// per aws/CLAUDE.md "Smithy API-error-code predicates").
func isMacieNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "Macie is not enabled")
}

func init() {
	registerService(serviceEntry{
		name: "aws:macie",
		fn:   scanMacie,
		emits: []coverage.TypeDecl{
			// Macie session is a per-(account,region) singleton config that
			// CloudFormation does model as AWS::Macie::Session (per-region
			// enablement); synth NativeID since the API exposes no ARN.
			{Service: "macie", DiscoType: TypeMacieSession, Leaf: true},
			// Classification jobs are real but CFN models no
			// AWS::Macie::ClassificationJob type.
			{Service: "macie", DiscoType: TypeMacieClassificationJob, Synthetic: true},
			{Service: "macie", DiscoType: TypeMacieAllowList},
			{Service: "macie", DiscoType: TypeMacieCustomDataIdentifier, Leaf: true},
			{Service: "macie", DiscoType: TypeMacieFindingsFilter, Leaf: true},
		},
	})
}

// macie2API is the narrow set of Macie operations called by the scanMacie
// sub-phases.
type macie2API interface {
	GetMacieSession(context.Context, *macie2.GetMacieSessionInput, ...func(*macie2.Options)) (*macie2.GetMacieSessionOutput, error)
	ListClassificationJobs(context.Context, *macie2.ListClassificationJobsInput, ...func(*macie2.Options)) (*macie2.ListClassificationJobsOutput, error)
	DescribeClassificationJob(context.Context, *macie2.DescribeClassificationJobInput, ...func(*macie2.Options)) (*macie2.DescribeClassificationJobOutput, error)
	ListCustomDataIdentifiers(context.Context, *macie2.ListCustomDataIdentifiersInput, ...func(*macie2.Options)) (*macie2.ListCustomDataIdentifiersOutput, error)
	GetCustomDataIdentifier(context.Context, *macie2.GetCustomDataIdentifierInput, ...func(*macie2.Options)) (*macie2.GetCustomDataIdentifierOutput, error)
	ListAllowLists(context.Context, *macie2.ListAllowListsInput, ...func(*macie2.Options)) (*macie2.ListAllowListsOutput, error)
	GetAllowList(context.Context, *macie2.GetAllowListInput, ...func(*macie2.Options)) (*macie2.GetAllowListOutput, error)
	ListFindingsFilters(context.Context, *macie2.ListFindingsFiltersInput, ...func(*macie2.Options)) (*macie2.ListFindingsFiltersOutput, error)
}

// scanMacie discovers Macie session config, classification jobs, custom data
// identifiers, and allow lists in one region. Macie is regional. Accounts that
// have not enabled Macie in the region surface AccessDeniedException at every
// phase — tolerated via skipIfAccessDenied alongside the standard skip. Each
// describe phase fans out per-item Get calls under fanoutMed.
func scanMacie(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := macie2.NewFromConfig(acct.cfg, func(o *macie2.Options) { o.Region = region })

	// Phase 1: session singleton. If Macie is not enabled here every phase
	// will fail the same way, so a denied session bails the whole region.
	sessionPresent := false
	{
		t, i, present, ferr := scanMacieSession(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return 0, 0, ferr
		}
		total += t
		inserted += i
		sessionPresent = present
	}
	if !sessionPresent {
		return total, inserted, nil
	}

	// Phase 2: classification jobs.
	{
		t, i, ferr := scanMacieClassificationJobs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 3: custom data identifiers.
	{
		t, i, ferr := scanMacieCustomDataIdentifiers(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 4: allow lists.
	{
		t, i, ferr := scanMacieAllowLists(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 5: findings filters.
	{
		t, i, ferr := scanMacieFindingsFilters(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanMacieSession(ctx context.Context, client macie2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, present bool, err error) {
	out, derr := client.GetMacieSession(ctx, &macie2.GetMacieSessionInput{})
	if derr != nil {
		if isMacieNotEnabled(derr) {
			return 0, 0, false, markServiceDisabled(derr)
		}
		if isAccessDenied(derr) {
			_ = skipIfAccessDenied(st, "macie2:GetMacieSession", acct.ID, region, derr)
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("macie2:GetMacieSession: %w", derr)
	}
	if out == nil {
		return 0, 0, false, nil
	}
	status := string(out.Status)
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeMacieSession,
		NativeID:       macieSessionNativeID(acct.ID, region),
		Region:         &region,
		Status:         &status,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, false, fmt.Errorf("upsert macie session: %w", uerr)
	}
	return 1, n, true, nil
}

func scanMacieClassificationJobs(ctx context.Context, client macie2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := macie2.NewListClassificationJobsPaginator(client, &macie2.ListClassificationJobsInput{})
	var jobIDs []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "macie2:ListClassificationJobs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("macie2:ListClassificationJobs: %w", perr)
		}
		for _, j := range out.Items {
			if j.JobId != nil {
				jobIDs = append(jobIDs, *j.JobId)
			}
		}
	}
	if len(jobIDs) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, id := range jobIDs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeClassificationJob(gctx, &macie2.DescribeClassificationJobInput{JobId: &id})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("macie2:DescribeClassificationJob %s: %w", id, derr)
			}
			arn := sv(out.JobArn)
			if arn == "" {
				return nil
			}
			status := string(out.JobStatus)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMacieClassificationJob,
				NativeID:       arn,
				Name:           out.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			if len(out.Tags) > 0 {
				r.TagsJSON = mapTagsJSON(out.Tags)
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
	n, uerr := upsertMacieChildren(st, acct, region, batch, "classification jobs")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

func scanMacieCustomDataIdentifiers(ctx context.Context, client macie2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := macie2.NewListCustomDataIdentifiersPaginator(client, &macie2.ListCustomDataIdentifiersInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "macie2:ListCustomDataIdentifiers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("macie2:ListCustomDataIdentifiers: %w", perr)
		}
		for _, c := range out.Items {
			if c.Id != nil {
				ids = append(ids, *c.Id)
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
			out, derr := client.GetCustomDataIdentifier(gctx, &macie2.GetCustomDataIdentifierInput{Id: &id})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("macie2:GetCustomDataIdentifier %s: %w", id, derr)
			}
			arn := sv(out.Arn)
			if arn == "" {
				return nil
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMacieCustomDataIdentifier,
				NativeID:       arn,
				Name:           out.Name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			if len(out.Tags) > 0 {
				r.TagsJSON = mapTagsJSON(out.Tags)
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
	n, uerr := upsertMacieChildren(st, acct, region, batch, "custom data identifiers")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

func scanMacieAllowLists(ctx context.Context, client macie2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := macie2.NewListAllowListsPaginator(client, &macie2.ListAllowListsInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "macie2:ListAllowLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("macie2:ListAllowLists: %w", perr)
		}
		for _, a := range out.AllowLists {
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
			out, derr := client.GetAllowList(gctx, &macie2.GetAllowListInput{Id: &id})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("macie2:GetAllowList %s: %w", id, derr)
			}
			arn := sv(out.Arn)
			if arn == "" {
				return nil
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMacieAllowList,
				NativeID:       arn,
				Name:           out.Name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			if len(out.Tags) > 0 {
				r.TagsJSON = mapTagsJSON(out.Tags)
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
	n, uerr := upsertMacieChildren(st, acct, region, batch, "allow lists")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

// upsertMacieChildren persists a batch of session-child resources and links
// each to its parent session via the contains closure. Each child belongs to
// the singleton session in (acct, region). Returns the inserted count from
// UpsertResources.
func upsertMacieChildren(st *store.Store, acct *account, region string, batch []*store.Resource, kind string) (int, error) {
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, fmt.Errorf("upsert macie %s: %w", kind, err)
	}
	parentID := store.ResourceID("aws", acct.ID, TypeMacieSession, macieSessionNativeID(acct.ID, region))
	pairs := make([][2]string, len(batch))
	for i, c := range batch {
		pairs[i] = [2]string{c.ID, parentID}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, fmt.Errorf("closure macie %s: %w", kind, err)
	}
	return n, nil
}

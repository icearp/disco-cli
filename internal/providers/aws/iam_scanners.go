package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:iam",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanIAM(ctx, acct, st, scanID)
		},
	})
}

// scanIAM discovers IAM roles, users, and groups in parallel. IAM is a global
// service scanned once per account regardless of region.
func scanIAM(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iam.NewFromConfig(acct.cfg)
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { tt, nn, e := scanIAMRoles(ctx, client, acct, st, scanID); add(tt, nn); return e })
	g.Go(func() error { tt, nn, e := scanIAMUsers(ctx, client, acct, st, scanID); add(tt, nn); return e })
	g.Go(func() error { tt, nn, e := scanIAMGroups(ctx, client, acct, st, scanID); add(tt, nn); return e })
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

func scanIAMRoles(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListRoles", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListRoles: %w", err)
		}
		var batch []*store.Resource
		for _, role := range page.Roles {
			name := sv(role.RoleName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMRole,
				NativeID:       sv(role.Arn),
				Name:           &name,
				CreatedAt:      tp(role.CreateDate),
				TagsJSON:       awsTagsJSON(role.Tags),
				AttributesJSON: mustJSON(role),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM roles: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

func scanIAMUsers(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListUsersPaginator(client, &iam.ListUsersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListUsers", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListUsers: %w", err)
		}
		var batch []*store.Resource
		for _, user := range page.Users {
			name := sv(user.UserName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMUser,
				NativeID:       sv(user.Arn),
				Name:           &name,
				CreatedAt:      tp(user.CreateDate),
				AttributesJSON: mustJSON(user),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM users: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

func scanIAMGroups(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListGroups", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListGroups: %w", err)
		}
		var batch []*store.Resource
		for _, group := range page.Groups {
			name := sv(group.GroupName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMGroup,
				NativeID:       sv(group.Arn),
				Name:           &name,
				CreatedAt:      tp(group.CreateDate),
				AttributesJSON: mustJSON(group),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

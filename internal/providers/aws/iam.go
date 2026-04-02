package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// scanIAM discovers IAM roles, users, and groups. IAM is a global service
// scanned once per account regardless of region.
func scanIAM(ctx context.Context, acct *account, st *store.Store, scanID string) error {
	client := iam.NewFromConfig(acct.cfg)

	if err := scanIAMRoles(ctx, client, acct, st, scanID); err != nil {
		return err
	}
	if err := scanIAMUsers(ctx, client, acct, st, scanID); err != nil {
		return err
	}
	return scanIAMGroups(ctx, client, acct, st, scanID)
}

func scanIAMRoles(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) error {
	pager := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("iam:ListRoles", acct.ID, "global", err)
			}
			return fmt.Errorf("iam:ListRoles: %w", err)
		}
		var batch []*store.Resource
		for _, role := range page.Roles {
			name := sv(role.RoleName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:iam:role",
				NativeID:       sv(role.Arn),
				Name:           &name,
				TagsJSON:       iamTagsJSON(role.Tags),
				AttributesJSON: mustJSON(role),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert IAM roles: %w", err)
			}
		}
	}
	return nil
}

func scanIAMUsers(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) error {
	pager := iam.NewListUsersPaginator(client, &iam.ListUsersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("iam:ListUsers", acct.ID, "global", err)
			}
			return fmt.Errorf("iam:ListUsers: %w", err)
		}
		var batch []*store.Resource
		for _, user := range page.Users {
			name := sv(user.UserName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:iam:user",
				NativeID:       sv(user.Arn),
				Name:           &name,
				AttributesJSON: mustJSON(user),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert IAM users: %w", err)
			}
		}
	}
	return nil
}

func scanIAMGroups(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) error {
	pager := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("iam:ListGroups", acct.ID, "global", err)
			}
			return fmt.Errorf("iam:ListGroups: %w", err)
		}
		var batch []*store.Resource
		for _, group := range page.Groups {
			name := sv(group.GroupName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:iam:group",
				NativeID:       sv(group.Arn),
				Name:           &name,
				AttributesJSON: mustJSON(group),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert IAM groups: %w", err)
			}
		}
	}
	return nil
}

// iamTagsJSON converts IAM tag slices to a JSON-encoded map string pointer.
func iamTagsJSON(tags []iamtypes.Tag) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			m[*t.Key] = *t.Value
		}
	}
	s := mustJSON(m)
	return &s
}

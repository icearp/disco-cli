package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ssmcontacts"
	sctypes "github.com/aws/aws-sdk-go-v2/service/ssmcontacts/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ssm-contacts",
		fn:   scanSSMContacts,
		emits: []coverage.TypeDecl{
			{Service: "ssm-contacts", DiscoType: TypeSSMContactsContact},
			{Service: "ssm-contacts", DiscoType: TypeSSMContactsPlan},
			{Service: "ssm-contacts", DiscoType: TypeSSMContactsContactChannel},
			{Service: "ssm-contacts", DiscoType: TypeSSMContactsRotation},
		},
	})
}

type ssmContactsAPI interface {
	ListContacts(context.Context, *ssmcontacts.ListContactsInput, ...func(*ssmcontacts.Options)) (*ssmcontacts.ListContactsOutput, error)
	ListContactChannels(context.Context, *ssmcontacts.ListContactChannelsInput, ...func(*ssmcontacts.Options)) (*ssmcontacts.ListContactChannelsOutput, error)
	ListRotations(context.Context, *ssmcontacts.ListRotationsInput, ...func(*ssmcontacts.Options)) (*ssmcontacts.ListRotationsOutput, error)
}

// scanSSMContacts discovers SSM Contacts contacts and escalation plans (split
// by ContactType), per-contact channels, and rotations.
func scanSSMContacts(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmcontacts.NewFromConfig(acct.cfg, func(o *ssmcontacts.Options) { o.Region = region })

	contactARNs, t, i, ferr := scanSCContacts(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, ca := range contactARNs {
		t, i, ferr = scanSCContactChannels(ctx, client, acct, region, st, scanID, ca)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanSCRotations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSCContacts walks ListContacts and dispatches each row by ContactType:
// ESCALATION → AWS::SSMContacts::Plan; PERSONAL / ONCALL_SCHEDULE / unknown
// → AWS::SSMContacts::Contact. Returns ARNs of all rows for downstream
// per-contact fan-out (channels apply to both kinds).
func scanSCContacts(ctx context.Context, client ssmContactsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := ssmcontacts.NewListContactsPaginator(client, &ssmcontacts.ListContactsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "ssmcontacts:ListContacts", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("ssmcontacts:ListContacts: %w", err)
		}
		for _, c := range out.Contacts {
			arn := sv(c.ContactArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			dt := TypeSSMContactsContact
			if c.Type == sctypes.ContactTypeEscalation {
				dt = TypeSSMContactsPlan
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dt, NativeID: arn,
				Name: c.Alias, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "ssmcontacts contacts/plans")
	return arns, t, i, err
}

func scanSCContactChannels(ctx context.Context, client ssmContactsAPI, acct *account, region string, st *store.Store, scanID string, contactARN string) (int, int, error) {
	ca := contactARN
	pager := ssmcontacts.NewListContactChannelsPaginator(client, &ssmcontacts.ListContactChannelsInput{ContactId: &ca})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssmcontacts:ListContactChannels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssmcontacts:ListContactChannels: %w", err)
		}
		for _, ch := range out.ContactChannels {
			arn := sv(ch.ContactChannelArn)
			if arn == "" {
				continue
			}
			status := string(ch.ActivationStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMContactsContactChannel, NativeID: arn,
				Name: ch.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ch), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssmcontacts contact-channels")
}

func scanSCRotations(ctx context.Context, client ssmContactsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssmcontacts.NewListRotationsPaginator(client, &ssmcontacts.ListRotationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssmcontacts:ListRotations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssmcontacts:ListRotations: %w", err)
		}
		for _, r := range out.Rotations {
			arn := sv(r.RotationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMContactsRotation, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ssmcontacts rotations")
}

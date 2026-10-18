package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/store"
)

const sesCertificateOp = "sesv2:ListEmailIdentityCertificates"

// isSESIdentityDeleted reports an identity deleted since the identity phase
// listed it (NotFoundException, which the op models). The gateway's
// "Unable to determine service/operation name" NotFoundException is a region
// gap instead (isServiceNotAvailableInRegion), so it is excluded.
func isSESIdentityDeleted(err error) bool {
	return isAPIErrorCode(err, "NotFoundException") && !isServiceNotAvailableInRegion(err)
}

// isSESCertificateOpNotDeployed reports an endpoint that does not serve the
// op: it is new, and not every regional endpoint may serve it yet. A deleted
// identity's NotFoundException also arrives as an HTTP 404, which
// isOpNotDeployed accepts, so it is excluded.
func isSESCertificateOpNotDeployed(err error) bool {
	return isOpNotDeployed(err) && !isSESIdentityDeleted(err)
}

// sesCertificateTallies are the per-scan skips reported in one warning each.
type sesCertificateTallies struct {
	noFrom      skipTally // domain-identity associations without a FromAddress
	duplicates  skipTally // second association for an address, neither DEPROVISIONING
	notDeployed skipTally // identities whose listing was refused as not deployed
}

// scanSESEmailIdentityCertificates lists the S/MIME certificate associations
// of every identity the identity phase stored, concurrently, and links each
// under its identity.
//
// An identity deleted since listing is skipped silently. One refused as not
// deployed, on any page, is skipped and its rows dropped: when no identity
// listed successfully the region does not serve the op and the phase returns
// silently; when some did (a partial rollout), the skips are reported in one
// warning. That and the warnings for skipped associations are reported only
// when the phase succeeds. A denial warns once; transient failures are
// handled by childFanOutSkipTransient.
func scanSESEmailIdentityCertificates(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, identities []sesIdentity) (int, int, error) {
	var (
		tallies  sesCertificateTallies
		answered atomic.Int64
	)
	parents := make([]childParent, 0, len(identities))
	byParent := make(map[childParent]sesIdentity, len(identities))
	for _, identity := range identities {
		parents = append(parents, identity.childParent)
		byParent[identity.childParent] = identity
	}
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, sesCertificateOp, parents,
		isSESIdentityDeleted, byParentID,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := listSESEmailIdentityCertificates(ctx, client, acct, region, scanID, byParent[p], &tallies)
			switch {
			case isSESCertificateOpNotDeployed(err):
				tallies.notDeployed.skipped(p.id, err)
				return nil, nil
			case err == nil:
				answered.Add(1)
			}
			return rows, err
		})
	if err != nil {
		return total, inserted, err
	}
	tallies.warnSkips(st, acct, region, int(answered.Load()))
	return total, inserted, nil
}

// warnSkips reports each non-empty tally in one warning. Not-deployed skips
// are reported only when answered identities listed successfully; with none,
// the region does not serve the op.
func (t *sesCertificateTallies) warnSkips(st *store.Store, acct *account, region string, answered int) {
	warn := func(msg string) {
		st.ReportWarning(store.ScanWarning{Provider: "aws", Service: sesCertificateOp, Scope: acct.ID + "/" + region, Message: msg})
	}
	if t.noFrom.count > 0 {
		warn(fmt.Sprintf("%d certificate association(s) of a domain identity skipped without a FromAddress, first under identity %s",
			t.noFrom.count, t.noFrom.first))
	}
	if t.duplicates.count > 0 {
		warn(fmt.Sprintf("%d duplicate certificate association(s) for an address already listed, neither DEPROVISIONING, skipped after the first listed, first under identity %s",
			t.duplicates.count, t.duplicates.first))
	}
	if t.notDeployed.count > 0 && answered > 0 {
		warn(fmt.Sprintf("%d identity(ies) skipped: %s was refused as not deployed while %d other(s) answered; first %s: %v",
			t.notDeployed.count, sesCertificateOp, answered, t.notDeployed.first, t.notDeployed.err))
	}
}

// listSESEmailIdentityCertificates lists the certificate associations of one
// identity. An association has no ARN of its own (CertificateArn names the ACM
// certificate), and DisassociateEmailIdentityCertificate keys it on
// (EmailIdentity, FromAddress), so its NativeID is
// {identityARN}/certificate/{FromAddress}.
//
// AssociateEmailIdentityCertificate documents that an address has one
// association, except that a new one may be made while the old one is
// DEPROVISIONING. Both then share a NativeID, so only one is kept: the one
// not DEPROVISIONING; between two DEPROVISIONING ones the first listed, as
// the doc allows that too. Two that are neither DEPROVISIONING break the
// documented rule: the first listed is kept and the second tallied for a
// warning. Skips are tallied only when the whole listing succeeds, since a
// failed identity stores nothing.
func listSESEmailIdentityCertificates(ctx context.Context, client sesv2API, acct *account, region, scanID string, identity sesIdentity, tallies *sesCertificateTallies) ([]*store.Resource, error) {
	// No Limit: the model documents no PageSize bound, and a guessed one the
	// service rejects would fail every identity with BadRequestException.
	pager := sesv2.NewListEmailIdentityCertificatesPaginator(client,
		&sesv2.ListEmailIdentityCertificatesInput{EmailIdentity: &identity.id},
		func(o *sesv2.ListEmailIdentityCertificatesPaginatorOptions) { o.StopOnDuplicateToken = true })
	var noFrom int
	d := sesCertificateDedupe{kept: map[string]int{}, deprovisioning: map[string]bool{}}
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range out.Certificates {
			from := sesCertificateFromAddress(identity, c)
			if from == "" {
				noFrom++
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESEmailIdentityCertificate, NativeID: identity.arn + "/certificate/" + from,
				Name: &from, Region: &region, Status: nonEmptyPtr(string(c.Status)),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
			d.add(from, r, c.Status == sesv2types.IdentityCertificateStatusDeprovisioning)
		}
	}
	for range noFrom {
		tallies.noFrom.skipped(identity.id, nil)
	}
	for _, from := range d.duplicates {
		tallies.duplicates.skipped(identity.id+" ("+from+")", nil)
	}
	return d.rows, nil
}

// sesCertificateDedupe keeps one association per FromAddress of one identity,
// by the rule on listSESEmailIdentityCertificates.
type sesCertificateDedupe struct {
	rows           []*store.Resource
	kept           map[string]int // FromAddress -> index in rows
	deprovisioning map[string]bool
	duplicates     []string
}

func (d *sesCertificateDedupe) add(from string, r *store.Resource, isDeprovisioning bool) {
	i, seen := d.kept[from]
	switch {
	case !seen:
		d.kept[from] = len(d.rows)
		d.rows = append(d.rows, r)
		d.deprovisioning[from] = isDeprovisioning
	case isDeprovisioning:
		// The one already kept wins: it is either live or listed first.
	case d.deprovisioning[from]:
		d.rows[i] = r
		d.deprovisioning[from] = false
	default:
		d.duplicates = append(d.duplicates, from)
	}
}

// sesCertificateFromAddress is the address an association applies to. For an
// email-address identity FromAddress is optional on Associate and, when set,
// must equal the identity, so an empty one means the identity itself. Whether
// List then returns it empty has not been observed live.
func sesCertificateFromAddress(identity sesIdentity, c sesv2types.IdentityCertificate) string {
	if from := sv(c.FromAddress); from != "" || !identity.emailAddress {
		return from
	}
	return identity.id
}

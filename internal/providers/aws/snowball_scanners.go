package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/snowball"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSnowballAddress, Service: "snowball"})
	registerType(restype.Descriptor{Type: TypeSnowballCluster, Service: "snowball"})
	registerService(serviceEntry{name: "aws:snowball", fn: scanSnowball})
}

const (
	snowballDescribeAddressesOp = "snowball:DescribeAddresses"
	snowballListClustersOp      = "snowball:ListClusters"
	snowballDescribeClusterOp   = "snowball:DescribeCluster"
	// snowballPageCap is the model's MaxResults ceiling (ListLimit) for both list ops.
	snowballPageCap = 100
)

// snowballAPI is the narrow Snow Family surface the scanners use;
// *snowball.Client satisfies it.
type snowballAPI interface {
	DescribeAddresses(context.Context, *snowball.DescribeAddressesInput, ...func(*snowball.Options)) (*snowball.DescribeAddressesOutput, error)
	ListClusters(context.Context, *snowball.ListClustersInput, ...func(*snowball.Options)) (*snowball.ListClustersOutput, error)
	DescribeCluster(context.Context, *snowball.DescribeClusterInput, ...func(*snowball.Options)) (*snowball.DescribeClusterOutput, error)
}

func scanSnowball(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := snowball.NewFromConfig(acct.cfg, func(o *snowball.Options) { o.Region = region })
	return scanSnowballAll(ctx, client, acct, region, st, scanID)
}

// scanSnowballAll runs the cluster phase, then the address phase. Clusters go
// first because a US region's address phase may wait for another US region's
// call (scanSnowballAddressesOnce), and that wait must not spend the clusters'
// time budget. Both errors pass through demoteTransient before they are
// joined, so a throttle on one phase cannot make the dispatcher classify the
// whole service transient and report the other phase's hard error as a
// transient one.
func scanSnowballAll(ctx context.Context, client snowballAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, cerr := scanSnowballClusters(ctx, client, acct, region, st, scanID)
	t, i, aerr := scanSnowballAddressesOnce(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, snowballListClustersOp, acct, region, cerr),
		demoteTransient(st, snowballDescribeAddressesOp, acct, region, aerr),
	)
}

// snowballAddressGate lets one US region at a time list the account's
// addresses, and records that one of them stored every page. The slot is a
// 1-buffer channel rather than a mutex so that a waiting region can give up
// when its context ends. done is read and written only by the slot's holder.
type snowballAddressGate struct {
	once sync.Once
	slot chan struct{}
	done bool
}

// acquire takes the slot, or reports false when ctx ends first.
func (g *snowballAddressGate) acquire(ctx context.Context) bool {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })
	select {
	case g.slot <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (g *snowballAddressGate) release() { <-g.slot }

// scanSnowballAddressesOnce lists the account's shipping addresses, except
// in a US region once another US region of this scan has stored them all.
// The DescribeAddresses doc says a call in one US region returns the
// addresses of every US region, so each US region would store the same rows.
// The US regions take turns through the account's gate: the first whose
// listing stores every page ends the turns, and a region whose listing is
// denied or fails leaves them to the next. GovCloud regions are not treated
// as US: the doc does not say whether they share a list (unverified). That a
// non-US region lists only its own addresses is assumed; the model says
// nothing about them (unverified).
//
// A US region whose context ends while another holds the gate returns
// nothing for addresses: it made no call, and the holder reports its own
// call's outcome, so a deadline here would blame this region for a wait it
// did not cause.
func scanSnowballAddressesOnce(ctx context.Context, client snowballAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if !isSnowballUSRegion(region) {
		_, total, inserted, err := scanSnowballAddresses(ctx, client, acct, region, region, st, scanID)
		return total, inserted, err
	}
	gate := &acct.snowballUSAddresses
	if !gate.acquire(ctx) {
		return 0, 0, nil
	}
	defer gate.release()
	if gate.done {
		return 0, 0, nil
	}
	complete, total, inserted, err := scanSnowballAddresses(ctx, client, acct, region, snowballUSAddressRegion(acct.Regions, region), st, scanID)
	gate.done = complete
	return total, inserted, err
}

// snowballUSAddressRegion is the Region stored on a US address: the first
// US region in sort order among the scanned regions, whichever US region made
// the call, so that the address keeps one Region from scan to scan
// (`disco resources --regions` stays stable). region, the caller, is always a
// candidate.
func snowballUSAddressRegion(scanned []string, region string) string {
	label := region
	for _, r := range scanned {
		if isSnowballUSRegion(r) && r < label {
			label = r
		}
	}
	return label
}

func isSnowballUSRegion(region string) bool {
	return strings.HasPrefix(region, "us-") && !strings.HasPrefix(region, "us-gov-")
}

// snowballAddressNativeID builds the address identity. Snowball issues no
// address ARN, and the region is left out because a US address can be listed
// from any US region (scanSnowballAddressesOnce); a cluster's AddressId then
// maps to the address whichever region listed it.
func snowballAddressNativeID(accountID, addressID string) string {
	return fmt.Sprintf("arn:aws:snowball::%s:address/%s", accountID, addressID)
}

// snowballClusterNativeID builds the cluster identity; Snowball issues no
// cluster ARN.
func snowballClusterNativeID(region, accountID, clusterID string) string {
	return fmt.Sprintf("arn:aws:snowball:%s:%s:cluster/%s", region, accountID, clusterID)
}

// scanSnowballAddresses stores the account's shipping addresses a page at a
// time. An address holds a recipient's name, company, street address and
// phone number; none of it is a credential, so nothing is redacted. The
// recipient's name is not a resource name, so Name stays unset. An address
// does not say which region it was created in, so rowRegion, chosen by the
// caller, is stored as its Region; region is the region called. complete
// reports that every page was stored.
func scanSnowballAddresses(ctx context.Context, client snowballAPI, acct *account, region, rowRegion string, st *store.Store, scanID string) (complete bool, total, inserted int, err error) {
	pager := snowball.NewDescribeAddressesPaginator(client, &snowball.DescribeAddressesInput{},
		func(o *snowball.DescribeAddressesPaginatorOptions) {
			o.Limit = snowballPageCap
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return false, total, inserted, skipIfAccessDenied(st, snowballDescribeAddressesOp, acct.ID, region, perr)
			}
			// InvalidResourceException, which the model lists for this op, is
			// believed unreachable for an unfiltered listing (an account with
			// no addresses is expected to get an empty list; unverified), so
			// it stays a hard error.
			return false, total, inserted, fmt.Errorf("%s: %w", snowballDescribeAddressesOp, perr)
		}
		batch := make([]*store.Resource, 0, len(out.Addresses))
		for _, a := range out.Addresses {
			id := sv(a.AddressId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSnowballAddress, NativeID: snowballAddressNativeID(acct.ID, id), Region: &rowRegion,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "snowball addresses")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return false, total, inserted, uerr
		}
	}
	return true, total, inserted, nil
}

// scanSnowballClusters lists the cluster ids, then reads each cluster with
// DescribeCluster: only ClusterMetadata carries the KMS key, IAM role,
// addresses and SNS topics. Clusters in every state are stored, Complete and
// Cancelled included: the API has no delete, and the model documents no
// retention after which a finished cluster drops out of the listing. A
// failed listing still reads and stores the clusters listed before it.
func scanSnowballClusters(ctx context.Context, client snowballAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var ids []string
	var listErr error
	pager := snowball.NewListClustersPaginator(client, &snowball.ListClustersInput{},
		func(o *snowball.ListClustersPaginatorOptions) {
			o.Limit = snowballPageCap
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, snowballListClustersOp, acct.ID, region, err)
			} else {
				listErr = fmt.Errorf("%s: %w", snowballListClustersOp, err)
			}
			break
		}
		for _, c := range out.ClusterListEntries {
			if id := sv(c.ClusterId); id != "" {
				ids = append(ids, id)
			}
		}
	}
	rows, derr := describeFanOut(ctx, st, acct, region, snowballDescribeClusterOp, ids, isSnowballClusterGone,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, err := client.DescribeCluster(ctx, &snowball.DescribeClusterInput{ClusterId: &id})
			if err != nil {
				return nil, err
			}
			m := out.ClusterMetadata
			if m == nil {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSnowballCluster, NativeID: snowballClusterNativeID(region, acct.ID, id), Region: &region,
				Status: nonEmptyPtr(string(m.ClusterState)), CreatedAt: tp(m.CreationDate),
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}, nil
		})
	total, inserted, uerr := upsertBatch(st, rows, "snowball clusters")
	return total, inserted, errors.Join(listErr, derr, uerr)
}

// isSnowballClusterGone matches a cluster that disappeared between
// ListClusters and DescribeCluster: InvalidResourceException is the model's
// only DescribeCluster error, documented as "The specified resource can't be
// found".
func isSnowballClusterGone(err error) bool {
	return isAPIErrorCode(err, "InvalidResourceException")
}

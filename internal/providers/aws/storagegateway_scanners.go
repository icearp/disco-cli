package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/storagegateway"
	sgwtypes "github.com/aws/aws-sdk-go-v2/service/storagegateway/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageGatewayGateway, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayVolume, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayShare, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayTape, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayTapePool, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayFsAssociation, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayCacheReport, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayDevice, Service: "storagegateway"})
	registerType(restype.Descriptor{Type: TypeStorageGatewayAutomaticTapeCreationPolicy, Service: "storagegateway"})
	registerService(serviceEntry{
		name: "aws:storagegateway",
		fn:   scanStorageGateway,
	})
}

// storageGatewayAPI is the narrow surface the scanner uses. Every List op is
// account-wide; DescribeVTLDevices is per-gateway (gateway ARN required).
// Describe{NFS,SMB}FileShares are batch reads by file share ARN.
type storageGatewayAPI interface {
	ListGateways(context.Context, *storagegateway.ListGatewaysInput, ...func(*storagegateway.Options)) (*storagegateway.ListGatewaysOutput, error)
	ListVolumes(context.Context, *storagegateway.ListVolumesInput, ...func(*storagegateway.Options)) (*storagegateway.ListVolumesOutput, error)
	ListFileShares(context.Context, *storagegateway.ListFileSharesInput, ...func(*storagegateway.Options)) (*storagegateway.ListFileSharesOutput, error)
	ListTapes(context.Context, *storagegateway.ListTapesInput, ...func(*storagegateway.Options)) (*storagegateway.ListTapesOutput, error)
	ListTapePools(context.Context, *storagegateway.ListTapePoolsInput, ...func(*storagegateway.Options)) (*storagegateway.ListTapePoolsOutput, error)
	ListFileSystemAssociations(context.Context, *storagegateway.ListFileSystemAssociationsInput, ...func(*storagegateway.Options)) (*storagegateway.ListFileSystemAssociationsOutput, error)
	ListCacheReports(context.Context, *storagegateway.ListCacheReportsInput, ...func(*storagegateway.Options)) (*storagegateway.ListCacheReportsOutput, error)
	DescribeVTLDevices(context.Context, *storagegateway.DescribeVTLDevicesInput, ...func(*storagegateway.Options)) (*storagegateway.DescribeVTLDevicesOutput, error)
	DescribeNFSFileShares(context.Context, *storagegateway.DescribeNFSFileSharesInput, ...func(*storagegateway.Options)) (*storagegateway.DescribeNFSFileSharesOutput, error)
	DescribeSMBFileShares(context.Context, *storagegateway.DescribeSMBFileSharesInput, ...func(*storagegateway.Options)) (*storagegateway.DescribeSMBFileSharesOutput, error)
	DescribeTapeArchives(context.Context, *storagegateway.DescribeTapeArchivesInput, ...func(*storagegateway.Options)) (*storagegateway.DescribeTapeArchivesOutput, error)
	ListAutomaticTapeCreationPolicies(context.Context, *storagegateway.ListAutomaticTapeCreationPoliciesInput, ...func(*storagegateway.Options)) (*storagegateway.ListAutomaticTapeCreationPoliciesOutput, error)
	ListTagsForResource(context.Context, *storagegateway.ListTagsForResourceInput, ...func(*storagegateway.Options)) (*storagegateway.ListTagsForResourceOutput, error)
}

const (
	sgwTagsOp                 = "storagegateway:ListTagsForResource"
	sgwDescribeTapeArchivesOp = "storagegateway:DescribeTapeArchives"
)

func scanStorageGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := storagegateway.NewFromConfig(acct.cfg, func(o *storagegateway.Options) { o.Region = region })
	return scanStorageGatewayWith(ctx, client, acct, region, st, scanID)
}

// scanStorageGatewayWith runs every listing as its own phase, one at a time,
// in the order below: a phase's error does not stop the later ones; a failed
// write and ctx's end do (runHostPhases). The VTL device and tape creation
// policy phases read the gateways the gateway phase stored. The rows of the
// gateways, volumes, file shares, tapes, tape pools and file system
// associations have their tags read with ListTagsForResource (sgwTags; a file
// share takes them from its describe when complete), and the tag denials of
// the whole scan are one warning, as are its gateway-state refusals. A cache
// report's tags come with its listed element; VTL devices and tape creation
// policies are not taggable. No List op sends a Limit: the model sets no
// maximum.
func scanStorageGatewayWith(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		gateways    []sgwtypes.GatewayInfo
		unavailable sgwGatewaysUnavailable
	)
	tags := sgwTags(client).sharingDenials()
	// tagPhase is the phase op whose scan reads its rows' tags through tags,
	// its gateway-state refusals added to unavailable only once its writes
	// all succeeded, as its denials are: after a failed write a refusal could
	// name a row the write lost.
	tagPhase := func(op string, scan func(tags rowTagReader, p *phaseTotals) (storeWriteFailed bool)) hostPhase {
		return hostPhase{op: op, run: func(p *phaseTotals) bool {
			var refused sgwGatewaysUnavailable
			if scan(refused.tags(tags), p) {
				return true
			}
			unavailable.addAll(&refused.skipTally)
			return false
		}}
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "storagegateway", 0, []hostPhase{
		tagPhase("storagegateway:ListGateways", func(tags rowTagReader, p *phaseTotals) (failed bool) {
			gateways, failed = scanSGWGateways(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}),
		tagPhase("storagegateway:ListVolumes", func(tags rowTagReader, p *phaseTotals) bool {
			return scanSGWVolumes(ctx, client, acct, region, st, scanID, tags, p)
		}),
		tagPhase("storagegateway:ListFileShares", func(tags rowTagReader, p *phaseTotals) bool {
			return scanSGWFileShares(ctx, client, acct, region, st, scanID, tags, p)
		}),
		tagPhase("storagegateway:ListTapes", func(tags rowTagReader, p *phaseTotals) bool {
			return scanSGWTapes(ctx, client, acct, region, st, scanID, tags, p)
		}),
		tagPhase("storagegateway:ListTapePools", func(tags rowTagReader, p *phaseTotals) bool {
			return scanSGWTapePools(ctx, client, acct, region, st, scanID, tags, p)
		}),
		tagPhase("storagegateway:ListFileSystemAssociations", func(tags rowTagReader, p *phaseTotals) bool {
			return scanSGWFileSystemAssociations(ctx, client, acct, region, st, scanID, tags, p)
		}),
		countedPhase(0, "storagegateway:ListCacheReports", func() (int, int, error) {
			return scanSGWCacheReports(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "storagegateway:DescribeVTLDevices", func() (int, int, error) {
			gatewayARNs := make([]string, len(gateways))
			for k, g := range gateways {
				gatewayARNs[k] = sv(g.GatewayARN)
			}
			return scanSGWVTLDevices(ctx, client, gatewayARNs, acct, region, st, scanID)
		}),
		countedPhase(0, "storagegateway:ListAutomaticTapeCreationPolicies", func() (int, int, error) {
			return scanSGWAutomaticTapeCreationPolicies(ctx, client, gateways, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	unavailable.warn(st, acct, region)
	return total, inserted, err
}

// sgwTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned, every page read (Marker; no Limit
// is sent, the model sets no maximum). The op models no not-found error
// (only InternalServerError and InvalidGatewayRequestException), so no tag
// read drops a row: a resource deleted after its listing keeps its row,
// untagged, and its failed read is reported.
func sgwTags(client storageGatewayAPI) rowTagReader {
	return rowTagReader{
		op: sgwTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, marker *string) ([]sgwtypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &storagegateway.ListTagsForResourceInput{ResourceARN: &arn, Marker: marker})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.Marker, nil
			})
			if err != nil {
				return nil, err
			}
			return sgwTagsJSON(tags), nil
		},
	}
}

func sgwTagsJSON(tags []sgwtypes.Tag) *string {
	return kvTagsJSON(tags, func(t sgwtypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// sgwGatewaysUnavailable keeps a row untagged when ListTagsForResource
// refuses it for its gateway's state (isSGWGatewayUnavailable), and warns
// once after the scan instead of failing it, as the file share and tape
// archive describes do: an offline gateway would otherwise fail every scan
// for as long as it stays offline. Whether the tag op answers with these
// codes is unverified live. A phase's reads run concurrently, so its tally
// keeps the smallest ARN; the phases run one at a time, so the scan's tally
// names the first phase's.
type sgwGatewaysUnavailable struct{ skipTally }

// tags is tags with a read refused for its gateway's state giving no tags
// and no error, tallied in u.
func (u *sgwGatewaysUnavailable) tags(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if isSGWGatewayUnavailable(err) {
			u.skippedLeast(r.NativeID, err)
			return nil, nil
		}
		return got, err
	}
	return tags
}

// warn reports the refused reads once. Tally into u only the refusals of
// rows that were written.
func (u *sgwGatewaysUnavailable) warn(st *store.Store, acct *account, region string) {
	u.warnCode(st, sgwTagsOp, acct, region, "InvalidGatewayRequestException", "tag read(s)",
		"a gateway that is offline, deleted, faulty, out of date or of a type that does not serve the read can refuse it, so those rows were stored without tags")
}

// scanSGWGateways stores the gateways and returns those stored, for the
// phases that read per gateway.
func scanSGWGateways(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (stored []sgwtypes.GatewayInfo, storeWriteFailed bool) {
	listed := map[string]sgwtypes.GatewayInfo{}
	steps := tokenPageSteps{stored: func(rows []*store.Resource) {
		for _, r := range rows {
			stored = append(stored, listed[r.NativeID])
		}
	}}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "storagegateway:ListGateways",
		func(ctx context.Context, marker *string) ([]sgwtypes.GatewayInfo, *string, error) {
			out, err := client.ListGateways(ctx, &storagegateway.ListGatewaysInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.Gateways, out.Marker, nil
		},
		func(g sgwtypes.GatewayInfo) (*store.Resource, string, string) {
			arn := sv(g.GatewayARN)
			listed[arn] = g
			name := sv(g.GatewayName)
			state := sv(g.GatewayOperationalState)
			return arnRow("gateways", g.GatewayARN, g.GatewayName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayGateway, NativeID: arn,
				Name: &name, Region: &region, Status: &state,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
	return stored, failed
}

func scanSGWVolumes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "storagegateway:ListVolumes",
		func(ctx context.Context, marker *string) ([]sgwtypes.VolumeInfo, *string, error) {
			out, err := client.ListVolumes(ctx, &storagegateway.ListVolumesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.VolumeInfos, out.Marker, nil
		},
		func(v sgwtypes.VolumeInfo) (*store.Resource, string, string) {
			label := sv(v.VolumeId)
			return arnRow("volumes", v.VolumeARN, v.VolumeId, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayVolume, NativeID: sv(v.VolumeARN),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// sgwDescribeTagsShown is how many tags Describe{NFS,SMB}FileShares is
// documented to show in full: "For a gateway with more than 10 tags
// assigned, you can view all tags using the ListTagsForResource API
// operation." A describe showing this many may have left some out.
const sgwDescribeTagsShown = 10

// scanSGWFileShares stores one row per file share. ListFileShares returns a
// summary; the share's full configuration comes from the Describe op of its
// type and, because it describes the same share (same ARN), replaces the
// summary as the row's attributes rather than becoming a second row. The
// detail elements carry no FileShareType: an NFS row is told from an SMB row
// by its fields (ClientList / Squash vs Authentication / ValidUserList).
// The whole listing is read before the describes, which batch each gateway's
// shares whichever page listed them.
//
// A described share's tags are the describe's when it shows fewer than
// sgwDescribeTagsShown; any other share has them read with tags.
//
// A share whose describe fails keeps its summary, as kinesisanalyticsv2 and
// location do: the row's attributes then flip shape between scans (a version
// split when the describe recovers), but leaving the share out instead would
// read as its deletion.
func scanSGWFileShares(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	const op = "storagegateway:ListFileShares"
	shares, listErr := readTokenPages(ctx, func(ctx context.Context, marker *string) ([]sgwtypes.FileShareInfo, *string, error) {
		out, err := client.ListFileShares(ctx, &storagegateway.ListFileSharesInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		return out.FileShareInfoList, out.NextMarker, nil
	})
	var skips notStoredSkips
	rows := buildListedRows(shares, func(fs sgwtypes.FileShareInfo) (*store.Resource, string, string) {
		label := sv(fs.FileShareId)
		status := sv(fs.FileShareStatus)
		return arnRow("file shares", fs.FileShareARN, fs.FileShareId, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeStorageGatewayShare, NativeID: sv(fs.FileShareARN),
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: mustJSON(fs), DiscoveredBy: scanID,
		})
	}, nil, &skips)
	details, describes := describeSGWFileShares(ctx, client, shares)
	for _, r := range rows {
		if d, ok := details[r.NativeID]; ok {
			r.AttributesJSON = mustJSON(d.body)
		}
	}
	failed := storeRowsTags(ctx, st, acct, region, op, rows, listErr, func() {
		skips.warn(st, op, acct, region)
		for _, d := range describes {
			d.warn(st, acct, region)
		}
	}, sgwShareTags(details, tags), p)
	for _, d := range describes {
		p.errs.add(d.op, d.err)
	}
	return failed
}

// sgwShareTags is tags with a share whose describe showed all its tags given
// those instead of a read. A share whose describe showed sgwDescribeTagsShown
// tags and whose read then fails is stored without tags, not with the
// describe's: every outcome of a failed read (the denial and refusal
// warnings, the returned error) reports the row as stored without tags, and a
// set cut short would pass as the share's whole set.
func sgwShareTags(details map[string]sgwShareDetail, tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if d, ok := details[r.NativeID]; ok && len(d.tags) < sgwDescribeTagsShown {
			return sgwTagsJSON(d.tags), nil
		}
		return read(ctx, r)
	}
	return tags
}

// sgwShareDescribeCap is the FileShareARNList cap of DescribeNFSFileShares and
// DescribeSMBFileShares.
const sgwShareDescribeCap = 10

// sgwShareDetail is a file share's DescribeNFSFileShares or
// DescribeSMBFileShares element and the tags it carries.
type sgwShareDetail struct {
	body any
	tags []sgwtypes.Tag
}

// describeSGWFileShares maps each NFS and SMB share's ARN to its
// DescribeNFSFileShares / DescribeSMBFileShares element, and returns each
// op's failures (sgwChunkFailures).
func describeSGWFileShares(ctx context.Context, client storageGatewayAPI, shares []sgwtypes.FileShareInfo) (map[string]sgwShareDetail, []*sgwChunkFailures) {
	var mu sync.Mutex
	details := map[string]sgwShareDetail{}
	add := func(arn string, d sgwShareDetail) {
		mu.Lock()
		defer mu.Unlock()
		details[arn] = d
	}
	nfs := describeSGWShareChunks(ctx, "storagegateway:DescribeNFSFileShares", sgwShareChunks(shares, sgwtypes.FileShareTypeNfs),
		func(ctx context.Context, arns []string) error {
			out, err := client.DescribeNFSFileShares(ctx, &storagegateway.DescribeNFSFileSharesInput{FileShareARNList: arns})
			if err != nil {
				return err
			}
			for _, d := range out.NFSFileShareInfoList {
				add(sv(d.FileShareARN), sgwShareDetail{d, d.Tags})
			}
			return nil
		})
	smb := describeSGWShareChunks(ctx, "storagegateway:DescribeSMBFileShares", sgwShareChunks(shares, sgwtypes.FileShareTypeSmb),
		func(ctx context.Context, arns []string) error {
			out, err := client.DescribeSMBFileShares(ctx, &storagegateway.DescribeSMBFileSharesInput{FileShareARNList: arns})
			if err != nil {
				return err
			}
			for _, d := range out.SMBFileShareInfoList {
				add(sv(d.FileShareARN), sgwShareDetail{d, d.Tags})
			}
			return nil
		})
	return details, []*sgwChunkFailures{nfs, smb}
}

// sgwShareChunks splits the ARNs of the shares of type typ into describe calls,
// each holding one gateway's shares, so a call failing on one gateway leaves
// every other gateway's shares described. A share without an ARN is not
// stored, so it is not described.
func sgwShareChunks(shares []sgwtypes.FileShareInfo, typ sgwtypes.FileShareType) [][]string {
	byGateway := map[string][]string{}
	for _, fs := range shares {
		if fs.FileShareType == typ && sv(fs.FileShareARN) != "" {
			gw := sv(fs.GatewayARN)
			byGateway[gw] = append(byGateway[gw], sv(fs.FileShareARN))
		}
	}
	var chunks [][]string
	for _, gw := range slices.Sorted(maps.Keys(byGateway)) {
		chunks = slices.AppendSeq(chunks, slices.Chunk(byGateway[gw], sgwShareDescribeCap))
	}
	return chunks
}

// sgwChunkFailures is how one describe op's chunks failed, each chunk named
// by its first ARN. A chunk denied or refused for its gateway's state
// (isSGWGatewayUnavailable; not observed live, so warned rather than skipped
// silently) or still transient after retries keeps its shares' summaries and
// is warned about, one warning per cause; any other error, ctx's end
// included, is err.
type sgwChunkFailures struct {
	op        string
	refused   skipTally
	transient transientTally
	err       error
}

// describeSGWShareChunks runs describe for each chunk concurrently
// (readEachKey), so no chunk's failure cancels another.
func describeSGWShareChunks(ctx context.Context, op string, chunks [][]string, describe func(context.Context, []string) error) *sgwChunkFailures {
	f := &sgwChunkFailures{op: op}
	results, stopped := readEachKey(ctx, chunks, describe)
	var errs []error
	for i, err := range results {
		first := chunks[i][0]
		switch {
		case err == nil:
		case f.transient.dropUnlessNXDOMAIN(first, err):
		case isAccessDenied(err) || isSGWGatewayUnavailable(err):
			f.refused.skipped(first, err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", op, first, err))
		}
	}
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", op, stopped))
	}
	f.err = errors.Join(errs...)
	return f
}

// warn reports the refused and the transient chunks, one warning each. Call
// it only once the shares are stored.
func (f *sgwChunkFailures) warn(st *store.Store, acct *account, region string) {
	if f.refused.count > 0 {
		_ = skipIfAccessDenied(st, f.op, acct.ID, region, fmt.Errorf("%d batch(es) of file shares stored as their summaries, first %s: %w", f.refused.count, f.refused.first, f.refused.err))
	}
	f.transient.warnSkipped(st, f.op, acct, region)
}

// sgwGatewayUnavailableCodes are the InvalidGatewayRequestException detail
// codes (StorageGatewayError.ErrorCode) that describe the gateway, not the
// request, so a correct call can still meet them:
//   - GatewayNotConnected: the gateway appliance is offline or unreachable.
//   - GatewayNotFound: the gateway was deleted after it was listed.
//   - GatewayInternalError: the fault is inside the gateway appliance.
//   - NotSupported: the gateway's type or software does not serve the op.
//   - OutdatedGateway: the gateway's software is too old for the op.
//
// Every other code (InvalidParameters, …) names a defect in the request.
var sgwGatewayUnavailableCodes = []sgwtypes.ErrorCode{
	sgwtypes.ErrorCodeGatewayNotConnected,
	sgwtypes.ErrorCodeGatewayNotFound,
	sgwtypes.ErrorCodeGatewayInternalError,
	sgwtypes.ErrorCodeNotSupported,
	sgwtypes.ErrorCodeOutdatedGateway,
}

// isSGWGatewayUnavailable reports whether err is an
// InvalidGatewayRequestException whose detail code is one of
// sgwGatewayUnavailableCodes. Storage Gateway models only that exception and
// InternalServerError for its read ops, so the detail code is the only way to
// tell a gateway's state from a broken request.
func isSGWGatewayUnavailable(err error) bool {
	var ige *sgwtypes.InvalidGatewayRequestException
	return errors.As(err, &ige) && ige.Error_ != nil && slices.Contains(sgwGatewayUnavailableCodes, ige.Error_.ErrorCode)
}

// scanSGWTapes stores one row per tape, a page at a time. ListTapes lists the
// tapes of both the virtual tape library and the shelf (VTS), a shelved tape
// with no GatewayARN; DescribeTapeArchives describes the same shelved tapes
// (same ARN) in full, so its element replaces a shelved tape's summary as the
// row's attributes rather than becoming a second row. Only shelved tapes take
// it: a tape with a GatewayARN keeps the summary the gateway edge is resolved
// from. The archives are read once, when the listing first returns a shelved
// tape. A shelved tape whose archive read fails keeps its summary, as file
// shares do (see scanSGWFileShares for the trade-off).
func scanSGWTapes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	const op = "storagegateway:ListTapes"
	var (
		archives sgwTapeArchives
		listed   phaseTotals
	)
	failed := storeTokenPagesTags(ctx, st, acct, region, op,
		func(ctx context.Context, marker *string) ([]sgwtypes.TapeInfo, *string, error) {
			out, err := client.ListTapes(ctx, &storagegateway.ListTapesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if !archives.read && slices.ContainsFunc(out.TapeInfos, sgwShelved) {
				archives.load(ctx, client)
			}
			return out.TapeInfos, out.Marker, nil
		},
		func(tp sgwtypes.TapeInfo) (*store.Resource, string, string) {
			attrs := mustJSON(tp)
			if a, ok := archives.byARN[sv(tp.TapeARN)]; ok && sgwShelved(tp) {
				attrs = mustJSON(a)
			}
			label := sv(tp.TapeBarcode)
			status := sv(tp.TapeStatus)
			return arnRow("tapes", tp.TapeARN, tp.TapeBarcode, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayTape, NativeID: sv(tp.TapeARN),
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}, tags, &listed)
	if failed {
		// The archive read's error, of any kind, is joined into the write's,
		// as the tag reads' are (rowTagErrs.addPhase): warned about, or
		// demoted when transient, it would be a warning that could name
		// tapes the write lost.
		return p.addPhase(op, listed.total, listed.inserted, errors.Join(opErrs{listed.errs}, archives.err))
	}
	p.total += listed.total
	p.inserted += listed.inserted
	p.errs = append(p.errs, listed.errs...)
	archives.reportArchives(st, acct, region, p)
	return false
}

// sgwShelved reports whether tp is a stored tape on the shelf (VTS).
func sgwShelved(tp sgwtypes.TapeInfo) bool {
	return sv(tp.TapeARN) != "" && sv(tp.GatewayARN) == ""
}

// sgwTapeArchives is the DescribeTapeArchives elements of the shelved tapes
// by ARN, and the error that ended their read.
type sgwTapeArchives struct {
	read  bool
	byARN map[string]sgwtypes.TapeArchive
	err   error
}

func (a *sgwTapeArchives) load(ctx context.Context, client storageGatewayAPI) {
	a.read = true
	a.byARN, a.err = describeSGWTapeArchives(ctx, client)
}

// reportArchives hands on the archive read's error once the tapes are written: a
// denial or a gateway-state refusal (isSGWGatewayUnavailable; not observed
// live) is warned about under the op, and so is a transient error, which
// costs only the archive attributes of shelved tapes, stored from ListTapes
// either way, like a per-item enrichment failure. Any other error goes to p.
// Call it only after every write of the tapes succeeded: its warnings could
// otherwise name tapes a write lost.
func (a *sgwTapeArchives) reportArchives(st *store.Store, acct *account, region string, p *phaseTotals) {
	const op = sgwDescribeTapeArchivesOp
	switch {
	case a.err == nil:
	case isAccessDenied(a.err) || isSGWGatewayUnavailable(a.err):
		_ = skipIfAccessDenied(st, op, acct.ID, region, a.err)
	default:
		p.errs.add(op, demoteTransient(st, op, acct, region, a.err))
	}
}

// describeSGWTapeArchives maps each shelved tape's ARN to its
// DescribeTapeArchives element. It returns the elements read before a failed
// page with that page's error.
func describeSGWTapeArchives(ctx context.Context, client storageGatewayAPI) (map[string]sgwtypes.TapeArchive, error) {
	const op = sgwDescribeTapeArchivesOp
	archives := map[string]sgwtypes.TapeArchive{}
	err := eachTokenPage(ctx, func(ctx context.Context, marker *string) ([]sgwtypes.TapeArchive, *string, error) {
		out, err := client.DescribeTapeArchives(ctx, &storagegateway.DescribeTapeArchivesInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		return out.TapeArchives, out.Marker, nil
	}, func(page []sgwtypes.TapeArchive) error {
		for _, a := range page {
			archives[sv(a.TapeARN)] = a
		}
		return nil
	})
	if err != nil {
		return archives, fmt.Errorf("%s: %w", op, err)
	}
	return archives, nil
}

// scanSGWTapePools stores the tape pools. ListTapePools lists custom pools
// only, so no row is an AWS-defined pool.
func scanSGWTapePools(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "storagegateway:ListTapePools",
		func(ctx context.Context, marker *string) ([]sgwtypes.PoolInfo, *string, error) {
			out, err := client.ListTapePools(ctx, &storagegateway.ListTapePoolsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.PoolInfos, out.Marker, nil
		},
		func(pl sgwtypes.PoolInfo) (*store.Resource, string, string) {
			label := sv(pl.PoolName)
			status := string(pl.PoolStatus)
			return arnRow("tape pools", pl.PoolARN, pl.PoolName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayTapePool, NativeID: sv(pl.PoolARN),
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(pl), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanSGWFileSystemAssociations(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "storagegateway:ListFileSystemAssociations",
		func(ctx context.Context, marker *string) ([]sgwtypes.FileSystemAssociationSummary, *string, error) {
			out, err := client.ListFileSystemAssociations(ctx, &storagegateway.ListFileSystemAssociationsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.FileSystemAssociationSummaryList, out.NextMarker, nil
		},
		func(fa sgwtypes.FileSystemAssociationSummary) (*store.Resource, string, string) {
			label := sv(fa.FileSystemAssociationId)
			status := sv(fa.FileSystemAssociationStatus)
			return arnRow("file system associations", fa.FileSystemAssociationARN, fa.FileSystemAssociationId, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayFsAssociation, NativeID: sv(fa.FileSystemAssociationARN),
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(fa), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanSGWCacheReports stores the cache reports with the tags their listed
// element carries.
func scanSGWCacheReports(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "storagegateway:ListCacheReports",
		func(ctx context.Context, marker *string) ([]sgwtypes.CacheReportInfo, *string, error) {
			out, err := client.ListCacheReports(ctx, &storagegateway.ListCacheReportsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.CacheReportList, out.Marker, nil
		},
		func(cr sgwtypes.CacheReportInfo) (*store.Resource, string, string) {
			label := sv(cr.ReportName)
			status := string(cr.CacheReportStatus)
			return arnRow("cache reports", cr.CacheReportARN, cr.ReportName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayCacheReport, NativeID: sv(cr.CacheReportARN),
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(cr), TagsJSON: sgwTagsJSON(cr.Tags), DiscoveredBy: scanID,
			})
		})
}

// scanSGWVTLDevices fans out DescribeVTLDevices per gateway — only VTL (tape)
// gateways host devices, so non-VTL gateways reject the call; tolerate the
// per-gateway error and keep scanning siblings. VTLDeviceARN embeds the
// gateway ARN, so the resolver recovers the parent from NativeID.
func scanSGWVTLDevices(ctx context.Context, client storageGatewayAPI, gatewayARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(gatewayARNs) == 0 {
		return 0, 0, nil
	}
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, gwARN := range gatewayARNs {
		g.Go(func() error {
			marker := ""
			for {
				in := &storagegateway.DescribeVTLDevicesInput{GatewayARN: &gwARN}
				if marker != "" {
					in.Marker = &marker
				}
				out, derr := client.DescribeVTLDevices(gctx, in)
				if derr != nil {
					// Non-VTL (file/volume) gateways reject DescribeVTLDevices;
					// tolerate per-gateway, like an access denial.
					if isAccessDenied(derr) || isAPIErrorCode(derr, "InvalidGatewayRequestException") {
						return nil
					}
					return fmt.Errorf("storagegateway:DescribeVTLDevices %s: %w", gwARN, derr)
				}
				mu.Lock()
				for _, d := range out.VTLDevices {
					arn := sv(d.VTLDeviceARN)
					if arn == "" {
						continue
					}
					label := sv(d.VTLDeviceType)
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeStorageGatewayDevice, NativeID: arn,
						Name: &label, Region: &region,
						AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
					})
				}
				mu.Unlock()
				if out.Marker == nil || *out.Marker == "" {
					break
				}
				marker = *out.Marker
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	return upsertBatch(st, batch, "storagegateway vtl-devices")
}

// sgwAutomaticTapeCreationPolicyNativeID keys a gateway's automatic tape
// creation policy, which has no ARN of its own, under the gateway's ARN; the
// suffix keeps it from sharing the gateway row's identity.
func sgwAutomaticTapeCreationPolicyNativeID(gatewayARN string) string {
	return gatewayARN + "/automatic-tape-creation-policy"
}

// scanSGWAutomaticTapeCreationPolicies stores each tape gateway's automatic
// tape creation policy, contained by its gateway when that gateway was listed.
// The op serves tape gateways only, so it is called only when a listed gateway
// is of type VTL. The one call omits the optional GatewayARN to list the whole
// region; that it does so is unverified until a live run. A gateway-state
// refusal (isSGWGatewayUnavailable) is not observed live, so it is warned about
// rather than skipped silently.
func scanSGWAutomaticTapeCreationPolicies(ctx context.Context, client storageGatewayAPI, gateways []sgwtypes.GatewayInfo, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "storagegateway:ListAutomaticTapeCreationPolicies"
	listed := map[string]bool{}
	tape := false
	for _, g := range gateways {
		listed[sv(g.GatewayARN)] = true
		tape = tape || sv(g.GatewayType) == "VTL"
	}
	if !tape {
		return 0, 0, nil
	}
	out, err := client.ListAutomaticTapeCreationPolicies(ctx, &storagegateway.ListAutomaticTapeCreationPoliciesInput{})
	if err != nil {
		if isAccessDenied(err) || isSGWGatewayUnavailable(err) {
			return 0, 0, skipIfAccessDenied(st, op, acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("%s: %w", op, err)
	}
	var batch []*store.Resource
	var pairs [][2]string
	for _, p := range out.AutomaticTapeCreationPolicyInfos {
		gw := sv(p.GatewayARN)
		if gw == "" {
			continue
		}
		row := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeStorageGatewayAutomaticTapeCreationPolicy, NativeID: sgwAutomaticTapeCreationPolicyNativeID(gw),
			Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
		}
		batch = append(batch, row)
		if listed[gw] {
			pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, row.NativeID), store.ResourceID("aws", acct.ID, gw)})
		}
	}
	t, i, err := upsertBatch(st, batch, "storagegateway automatic-tape-creation-policies")
	if err != nil {
		return 0, 0, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure %s: %w", op, err)
	}
	return t, i, nil
}

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
// account-wide and, except ListAutomaticTapeCreationPolicies, paginator-native;
// DescribeVTLDevices is per-gateway (gateway ARN required) and has no
// paginator. Describe{NFS,SMB}FileShares are batch reads by file share ARN.
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
}

func scanStorageGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := storagegateway.NewFromConfig(acct.cfg, func(o *storagegateway.Options) { o.Region = region })
	return scanStorageGatewayWith(ctx, client, acct, region, st, scanID)
}

func scanStorageGatewayWith(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	gateways, t, i, ferr := scanSGWGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	gatewayARNs := make([]string, len(gateways))
	for k, g := range gateways {
		gatewayARNs[k] = sv(g.GatewayARN)
	}

	// The phases list independently, so one failing must not cost the others
	// their rows; each is joined, a transient one demoted to a warning.
	var errs []error
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{"storagegateway:ListVolumes", func() (int, int, error) { return scanSGWVolumes(ctx, client, acct, region, st, scanID) }},
		{"storagegateway:ListFileShares", func() (int, int, error) { return scanSGWFileShares(ctx, client, acct, region, st, scanID) }},
		{"storagegateway:ListTapes", func() (int, int, error) { return scanSGWTapes(ctx, client, acct, region, st, scanID) }},
		{"storagegateway:ListTapePools", func() (int, int, error) { return scanSGWTapePools(ctx, client, acct, region, st, scanID) }},
		{"storagegateway:ListFileSystemAssociations", func() (int, int, error) {
			return scanSGWFileSystemAssociations(ctx, client, acct, region, st, scanID)
		}},
		{"storagegateway:ListCacheReports", func() (int, int, error) { return scanSGWCacheReports(ctx, client, acct, region, st, scanID) }},
		{"storagegateway:DescribeVTLDevices", func() (int, int, error) {
			return scanSGWVTLDevices(ctx, client, gatewayARNs, acct, region, st, scanID)
		}},
		{"storagegateway:ListAutomaticTapeCreationPolicies", func() (int, int, error) {
			return scanSGWAutomaticTapeCreationPolicies(ctx, client, gateways, acct, region, st, scanID)
		}},
	} {
		pt, pi, perr := phase.fn()
		total += pt
		inserted += pi
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

func scanSGWGateways(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) ([]sgwtypes.GatewayInfo, int, int, error) {
	pager := storagegateway.NewListGatewaysPaginator(client, &storagegateway.ListGatewaysInput{})
	var batch []*store.Resource
	var gateways []sgwtypes.GatewayInfo
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "storagegateway:ListGateways", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("storagegateway:ListGateways: %w", perr)
		}
		for _, g := range out.Gateways {
			arn := sv(g.GatewayARN)
			if arn == "" {
				continue
			}
			gateways = append(gateways, g)
			name := sv(g.GatewayName)
			state := sv(g.GatewayOperationalState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayGateway, NativeID: arn,
				Name: &name, Region: &region, Status: &state,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "storagegateway gateways")
	return gateways, t, i, err
}

func scanSGWVolumes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListVolumesPaginator(client, &storagegateway.ListVolumesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListVolumes", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListVolumes: %w", perr)
		}
		for _, v := range out.VolumeInfos {
			arn := sv(v.VolumeARN)
			if arn == "" {
				continue
			}
			label := sv(v.VolumeId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayVolume, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway volumes")
}

// scanSGWFileShares stores one row per file share. ListFileShares returns a
// summary; the share's full configuration comes from the Describe op of its
// type and, because it describes the same share (same ARN), replaces the
// summary as the row's attributes rather than becoming a second row. The
// detail elements carry no FileShareType: an NFS row is told from an SMB row
// by its fields (ClientList / Squash vs Authentication / ValidUserList).
//
// A share whose describe fails keeps its summary, as kinesisanalyticsv2 and
// location do: the row's attributes then flip shape between scans (a version
// split when the describe recovers), but leaving the share out instead would
// read as its deletion.
func scanSGWFileShares(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListFileSharesPaginator(client, &storagegateway.ListFileSharesInput{})
	var shares []sgwtypes.FileShareInfo
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListFileShares", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListFileShares: %w", perr)
		}
		for _, fs := range out.FileShareInfoList {
			if sv(fs.FileShareARN) != "" {
				shares = append(shares, fs)
			}
		}
	}
	details, derr := describeSGWFileShares(ctx, client, acct, region, st, shares)
	batch := make([]*store.Resource, 0, len(shares))
	for _, fs := range shares {
		arn := sv(fs.FileShareARN)
		attrs := mustJSON(fs)
		if d, ok := details[arn]; ok {
			attrs = mustJSON(d)
		}
		label := sv(fs.FileShareId)
		status := sv(fs.FileShareStatus)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeStorageGatewayShare, NativeID: arn,
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: attrs, DiscoveredBy: scanID,
		})
	}
	t, i, err := upsertBatch(st, batch, "storagegateway file-shares")
	if err != nil {
		return 0, 0, err
	}
	return t, i, derr
}

// sgwShareDescribeCap is the FileShareARNList cap of DescribeNFSFileShares and
// DescribeSMBFileShares.
const sgwShareDescribeCap = 10

// describeSGWFileShares maps each NFS and SMB share's ARN to its
// DescribeNFSFileShares / DescribeSMBFileShares element.
func describeSGWFileShares(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, shares []sgwtypes.FileShareInfo) (map[string]any, error) {
	var mu sync.Mutex
	details := map[string]any{}
	add := func(arn string, d any) {
		mu.Lock()
		defer mu.Unlock()
		details[arn] = d
	}
	nfsErr := describeSGWShareChunks(ctx, st, acct, region, "storagegateway:DescribeNFSFileShares", sgwShareChunks(shares, sgwtypes.FileShareTypeNfs),
		func(ctx context.Context, arns []string) error {
			out, err := client.DescribeNFSFileShares(ctx, &storagegateway.DescribeNFSFileSharesInput{FileShareARNList: arns})
			if err != nil {
				return err
			}
			for _, d := range out.NFSFileShareInfoList {
				add(sv(d.FileShareARN), d)
			}
			return nil
		})
	smbErr := describeSGWShareChunks(ctx, st, acct, region, "storagegateway:DescribeSMBFileShares", sgwShareChunks(shares, sgwtypes.FileShareTypeSmb),
		func(ctx context.Context, arns []string) error {
			out, err := client.DescribeSMBFileShares(ctx, &storagegateway.DescribeSMBFileSharesInput{FileShareARNList: arns})
			if err != nil {
				return err
			}
			for _, d := range out.SMBFileShareInfoList {
				add(sv(d.FileShareARN), d)
			}
			return nil
		})
	return details, errors.Join(nfsErr, smbErr)
}

// sgwShareChunks splits the ARNs of the shares of type typ into describe calls,
// each holding one gateway's shares, so a call failing on one gateway leaves
// every other gateway's shares described.
func sgwShareChunks(shares []sgwtypes.FileShareInfo, typ sgwtypes.FileShareType) [][]string {
	byGateway := map[string][]string{}
	for _, fs := range shares {
		if fs.FileShareType == typ {
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

// describeSGWShareChunks runs describe for each chunk concurrently (fanoutMed).
// A chunk denied or refused for its gateway's state (isSGWGatewayUnavailable;
// not observed live, so warned rather than skipped silently; one warning per op
// covers both) or still transient after retries (one warning per fan-out)
// keeps its shares' summaries; any other error is returned.
func describeSGWShareChunks(ctx context.Context, st *store.Store, acct *account, region, op string, chunks [][]string, describe func(context.Context, []string) error) error {
	var (
		transient transientTally
		warnOnce  sync.Once
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, arns := range chunks {
		g.Go(func() error {
			err := describe(gctx, arns)
			switch {
			case err == nil:
			case isTransientNetworkError(err):
				transient.skipped(arns[0], err)
			case isAccessDenied(err) || isSGWGatewayUnavailable(err):
				warnOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
			default:
				return fmt.Errorf("%s %s: %w", op, arns[0], err)
			}
			return nil
		})
	}
	err := g.Wait()
	transient.warnSkipped(st, op, acct, region)
	return err
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

// scanSGWTapes stores one row per tape. ListTapes lists the tapes of both the
// virtual tape library and the shelf (VTS), a shelved tape with no
// GatewayARN; DescribeTapeArchives describes the same shelved tapes (same ARN)
// in full, so its element replaces a shelved tape's summary as the row's
// attributes rather than becoming a second row. Only shelved tapes take it: a
// tape with a GatewayARN keeps the summary the gateway edge is resolved from.
// A shelved tape whose archive read fails keeps its summary, as file shares do
// (see scanSGWFileShares for the trade-off).
func scanSGWTapes(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListTapesPaginator(client, &storagegateway.ListTapesInput{})
	var tapes []sgwtypes.TapeInfo
	shelved := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListTapes", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListTapes: %w", perr)
		}
		for _, tp := range out.TapeInfos {
			if sv(tp.TapeARN) == "" {
				continue
			}
			tapes = append(tapes, tp)
			shelved = shelved || sv(tp.GatewayARN) == ""
		}
	}
	var archives map[string]sgwtypes.TapeArchive
	var aerr error
	if shelved {
		archives, aerr = describeSGWTapeArchives(ctx, client, acct, region, st)
	}
	batch := make([]*store.Resource, 0, len(tapes))
	for _, tp := range tapes {
		arn := sv(tp.TapeARN)
		attrs := mustJSON(tp)
		if a, ok := archives[arn]; ok && sv(tp.GatewayARN) == "" {
			attrs = mustJSON(a)
		}
		label := sv(tp.TapeBarcode)
		status := sv(tp.TapeStatus)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeStorageGatewayTape, NativeID: arn,
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: attrs, DiscoveredBy: scanID,
		})
	}
	t, i, err := upsertBatch(st, batch, "storagegateway tapes")
	if err != nil {
		return 0, 0, err
	}
	return t, i, aerr
}

// describeSGWTapeArchives maps each shelved tape's ARN to its
// DescribeTapeArchives element. A denial, a gateway-state refusal
// (isSGWGatewayUnavailable; not observed live) or a transient error is warned
// about under this op, and any other error is returned; each keeps the
// elements read before it. The transient warning is raised here because the
// caller's phase would otherwise demote it under storagegateway:ListTapes.
func describeSGWTapeArchives(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store) (map[string]sgwtypes.TapeArchive, error) {
	const op = "storagegateway:DescribeTapeArchives"
	archives := map[string]sgwtypes.TapeArchive{}
	pager := storagegateway.NewDescribeTapeArchivesPaginator(client, &storagegateway.DescribeTapeArchivesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isSGWGatewayUnavailable(err) {
				return archives, skipIfAccessDenied(st, op, acct.ID, region, err)
			}
			return archives, demoteTransient(st, op, acct, region, fmt.Errorf("%s: %w", op, err))
		}
		for _, a := range out.TapeArchives {
			archives[sv(a.TapeARN)] = a
		}
	}
	return archives, nil
}

func scanSGWTapePools(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListTapePoolsPaginator(client, &storagegateway.ListTapePoolsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListTapePools", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListTapePools: %w", perr)
		}
		for _, p := range out.PoolInfos {
			arn := sv(p.PoolARN)
			if arn == "" {
				continue
			}
			label := sv(p.PoolName)
			status := string(p.PoolStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayTapePool, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway tape-pools")
}

func scanSGWFileSystemAssociations(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListFileSystemAssociationsPaginator(client, &storagegateway.ListFileSystemAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListFileSystemAssociations", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListFileSystemAssociations: %w", perr)
		}
		for _, fa := range out.FileSystemAssociationSummaryList {
			arn := sv(fa.FileSystemAssociationARN)
			if arn == "" {
				continue
			}
			label := sv(fa.FileSystemAssociationId)
			status := sv(fa.FileSystemAssociationStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayFsAssociation, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(fa), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway fs-associations")
}

func scanSGWCacheReports(ctx context.Context, client storageGatewayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := storagegateway.NewListCacheReportsPaginator(client, &storagegateway.ListCacheReportsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "storagegateway:ListCacheReports", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("storagegateway:ListCacheReports: %w", perr)
		}
		for _, cr := range out.CacheReportList {
			arn := sv(cr.CacheReportARN)
			if arn == "" {
				continue
			}
			label := sv(cr.ReportName)
			status := string(cr.CacheReportStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeStorageGatewayCacheReport, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(cr), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "storagegateway cache-reports")
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

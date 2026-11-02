package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/controltower"
	cttypes "github.com/aws/aws-sdk-go-v2/service/controltower/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeControlTowerLandingZone, Service: "controltower"})
	registerType(restype.Descriptor{Type: TypeControlTowerEnabledBaseline, Service: "controltower"})
	registerType(restype.Descriptor{Type: TypeControlTowerEnabledControl, Service: "controltower"})
	registerService(serviceEntry{
		name: "aws:controltower",
		fn:   scanControlTower,
	})
}

// controltowerAPI lists the Control Tower operations scanControlTower's
// sub-phases call.
type controltowerAPI interface {
	ListLandingZones(context.Context, *controltower.ListLandingZonesInput, ...func(*controltower.Options)) (*controltower.ListLandingZonesOutput, error)
	GetLandingZone(context.Context, *controltower.GetLandingZoneInput, ...func(*controltower.Options)) (*controltower.GetLandingZoneOutput, error)
	ListEnabledBaselines(context.Context, *controltower.ListEnabledBaselinesInput, ...func(*controltower.Options)) (*controltower.ListEnabledBaselinesOutput, error)
	ListEnabledControls(context.Context, *controltower.ListEnabledControlsInput, ...func(*controltower.Options)) (*controltower.ListEnabledControlsOutput, error)
	ListTagsForResource(context.Context, *controltower.ListTagsForResourceInput, ...func(*controltower.Options)) (*controltower.ListTagsForResourceOutput, error)
}

const (
	ctowerTagsOp           = "controltower:ListTagsForResource"
	ctowerListLandingZones = "controltower:ListLandingZones"
	ctowerGetLandingZone   = "controltower:GetLandingZone"
	ctowerListBaselines    = "controltower:ListEnabledBaselines"
	ctowerListControls     = "controltower:ListEnabledControls"

	// The maxResults maximum each listing's model sets: ListLandingZones
	// returns at most one landing zone.
	ctowerLandingZonePageCap = 1
	ctowerBaselinePageCap    = 100
	ctowerControlPageCap     = 200
)

// ctowerGone is the not-found code ListTagsForResource and GetLandingZone
// model for a resource deleted since it was listed, and the one
// ListEnabledControls models for a target that no longer exists.
var ctowerGone = []string{"ResourceNotFoundException"}

// errCtowerNoLandingZoneBody is the error of a GetLandingZone response
// without the LandingZone its model requires.
var errCtowerNoLandingZoneBody = errors.New("GetLandingZone response has no LandingZone")

// isControlTowerNotEnabled distinguishes "Control Tower not deployed in
// this account" from real errors. Two shapes seen in practice:
//
//   - AccessDeniedException: account is not the org management account.
//   - ValidationException: AWSControlTowerAdmin role not yet bootstrapped
//     in this account/region.
//
// Both map to "service disabled" for progress reporting.
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

func scanControlTower(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := controltower.NewFromConfig(acct.cfg, func(o *controltower.Options) { o.Region = region })
	return scanControlTowerAll(ctx, client, acct, region, st, scanID)
}

// scanControlTowerAll discovers the landing zone, the enabled baselines and
// the enabled controls of one region (typically the landing zone's home
// region). The landing zone and the baselines run as two phases, one at a
// time: a phase's error does not stop the other; a failed write and ctx's end
// do (runHostPhases). Every row has its tags read (ctowerTags), and those
// reads' denials are one warning for the whole scan.
//
// A first page answering that Control Tower is not set up
// (isControlTowerNotEnabled) before any row was stored or any phase failed
// makes the scan return markServiceDisabled with nothing stored
// (ctowerNotEnabledGap); after the landing-zone listing gives that answer the
// baselines are not listed.
func scanControlTowerAll(ctx context.Context, client controltowerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := ctowerTags(client).sharingDenials()
	var disabled error
	total, inserted, err := runHostPhases(ctx, st, acct, region, "controltower", 0, []hostPhase{
		{op: ctowerListLandingZones, run: func(p *phaseTotals) bool {
			return scanControlTowerLandingZones(ctx, client, acct, region, st, scanID, ctowerNotEnabledGap(p, &disabled), tags, p)
		}},
		{op: ctowerListBaselines, run: func(p *phaseTotals) bool {
			if disabled != nil {
				return false
			}
			return scanControlTowerEnabledBaselines(ctx, client, acct, region, st, scanID, ctowerNotEnabledGap(p, &disabled), tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	// err is set when ctx ended after the landing-zone listing was refused:
	// the stop is reported rather than the sentinel.
	if disabled != nil && err == nil {
		return 0, 0, disabled
	}
	return total, inserted, err
}

// ctowerNotEnabledGap is a phase's firstPageGapPages test: while no phase
// has stored a row or failed (p), a first page answering that Control Tower
// is not set up sets *disabled to markServiceDisabled and reads as an empty
// listing. Once a row was stored or a phase failed, the same answer is
// returned as the error it is, so the sentinel never discards counts or
// hides another failure.
func ctowerNotEnabledGap(p *phaseTotals, disabled *error) func(error) bool {
	return func(err error) bool {
		if err == nil || p.total > 0 || len(p.errs) > 0 || !isControlTowerNotEnabled(err) {
			return false
		}
		*disabled = markServiceDisabled(err)
		return true
	}
}

// ctowerTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists
// landing zone, enabled baseline and enabled control under
// ListTagsForResource, and CreateLandingZone, EnableBaseline and
// EnableControl take Tags. The op returns every tag in one response.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed. Whether the op answers for the controls and baselines
// Control Tower enables itself (mandatory controls, the child baselines of
// enrolled accounts) is unverified live; a refusal keeps the row untagged.
func ctowerTags(client controltowerAPI) rowTagReader {
	return rowTagReader{
		op:       ctowerTagsOp,
		notFound: ctowerGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &controltower.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// ctowerLandingZoneAttrs is a landing zone's GetLandingZone body with its
// Manifest rendered through the document's own marshaler: encoding/json
// renders a Smithy document as {}.
type ctowerLandingZoneAttrs struct {
	cttypes.LandingZoneDetail
	Manifest json.RawMessage `json:"Manifest"`
}

// scanControlTowerLandingZones stores the landing zone, each as its
// GetLandingZone body with its Status, and its tags. A landing zone
// GetLandingZone no longer finds was deleted since it was listed and is not
// stored; any other failure, a response without its LandingZone included,
// stores the listed element, which carries only the ARN, and no Status
// (rowDetailReader). It reports whether the write failed, after which the
// scan must stop.
func scanControlTowerLandingZones(ctx context.Context, client controltowerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTagsWith(ctx, st, acct, region, ctowerListLandingZones,
		firstPageGapPages(notEnabled, func(ctx context.Context, token *string) ([]cttypes.LandingZoneSummary, *string, error) {
			out, err := client.ListLandingZones(ctx, &controltower.ListLandingZonesInput{MaxResults: aws.Int32(ctowerLandingZonePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LandingZones, out.NextToken, nil
		}),
		func(lz cttypes.LandingZoneSummary) (*store.Resource, string, string) {
			return arnRow("landing zones", lz.Arn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeControlTowerLandingZone, NativeID: sv(lz.Arn), Region: &region,
				AttributesJSON: mustJSON(lz), DiscoveredBy: scanID,
			})
		},
		tokenPageSteps{details: rowDetailReader{
			op: ctowerGetLandingZone, what: "landing zone(s)", notFound: ctowerGone,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				arn := r.NativeID
				out, err := client.GetLandingZone(ctx, &controltower.GetLandingZoneInput{LandingZoneIdentifier: &arn})
				if err != nil {
					return nil, err
				}
				lz := out.LandingZone
				if lz == nil {
					return nil, errCtowerNoLandingZoneBody
				}
				var manifest json.RawMessage
				if lz.Manifest != nil {
					if manifest, err = lz.Manifest.MarshalSmithyDocument(); err != nil {
						return nil, fmt.Errorf("render Manifest: %w", err)
					}
				}
				// Each read sets only its own row, before any tag read runs.
				r.Status = nonEmptyPtr(string(lz.Status))
				return ctowerLandingZoneAttrs{LandingZoneDetail: *lz, Manifest: manifest}, nil
			},
		}},
		tags, p)
}

// ctowerBaselineAttrs is an enabled baseline's stored attributes: its listed
// element and the enabled controls listed for its target, embedded so that a
// rule can index them by control identifier.
type ctowerBaselineAttrs struct {
	Baseline        cttypes.EnabledBaselineSummary  `json:"Baseline"`
	EnabledControls []cttypes.EnabledControlSummary `json:"EnabledControls,omitempty"`
}

// scanControlTowerEnabledBaselines stores the enabled baselines, their
// children included (IncludeChildren), with their tags, and the enabled
// controls of their targets (scanControlTowerEnabledControls). Each baseline
// embeds the controls its target listed, so the controls are listed and
// stored first and the baselines are written after them; the baseline
// listing is held whole, one element per baseline per OU or enrolled
// account. A failed later page keeps the pages read before it. Each target
// is listed once, however many baselines name it. It reports whether a write
// failed, after which the scan must stop.
func scanControlTowerEnabledBaselines(ctx context.Context, client controltowerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	baselines, listErr := readTokenPages(ctx, firstPageGapPages(notEnabled, func(ctx context.Context, token *string) ([]cttypes.EnabledBaselineSummary, *string, error) {
		out, err := client.ListEnabledBaselines(ctx, &controltower.ListEnabledBaselinesInput{IncludeChildren: true, MaxResults: aws.Int32(ctowerBaselinePageCap), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.EnabledBaselines, out.NextToken, nil
	}))
	// A baseline without an ARN is not stored, but its target's controls
	// are: they are rows of their own. A baseline without a target is stored
	// with no controls to list.
	var (
		targets []childParent
		seen    = map[string]bool{}
	)
	for _, b := range baselines {
		target := sv(b.TargetIdentifier)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, childParent{id: target, arn: target})
	}
	controls, failed := scanControlTowerEnabledControls(ctx, client, acct, region, st, scanID, targets, tags, p)
	if failed {
		return true
	}
	var skips notStoredSkips
	rows := buildListedRows(baselines, func(b cttypes.EnabledBaselineSummary) (*store.Resource, string, string) {
		return arnRow("enabled baselines", b.Arn, b.BaselineIdentifier, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeControlTowerEnabledBaseline, NativeID: sv(b.Arn), Region: &region,
			AttributesJSON: mustJSON(ctowerBaselineAttrs{Baseline: b, EnabledControls: controls[sv(b.TargetIdentifier)]}),
			DiscoveredBy:   scanID,
		})
	}, nil, &skips)
	return storeRowsTags(ctx, st, acct, region, ctowerListBaselines, rows, listErr, func() {
		skips.warn(st, ctowerListBaselines, acct, region)
	}, tags, p)
}

// scanControlTowerEnabledControls stores the enabled controls of each target
// (an OU or account ARN) with their tags, the targets listed concurrently and
// each page written as soon as its tags are read (storeEachParentPagesTags),
// and returns every element each target listed, in listing order, for its
// baselines to embed. Each stored control costs one ListTagsForResource
// call, on top of one ListEnabledControls call per 200 controls per target.
// One target's failure costs the others none of theirs, and the elements it
// listed before failing are still returned. A target ListEnabledControls no
// longer finds is skipped; one whose first page it refuses with
// ValidationException is skipped, with one warning for all of them. A
// ValidationException on a later page is returned as the error it is, the
// pages before it kept. It reports whether a write failed, after which the
// scan must stop.
func scanControlTowerEnabledControls(ctx context.Context, client controltowerAPI, acct *account, region string, st *store.Store, scanID string, targets []childParent, tags rowTagReader, p *phaseTotals) (map[string][]cttypes.EnabledControlSummary, bool) {
	var (
		mu      sync.Mutex
		listed  = map[string][]cttypes.EnabledControlSummary{}
		refused skipTally
	)
	failed := storeEachParentPagesTags(ctx, st, acct, region, ctowerListControls, ctowerGone, targets,
		func(ctx context.Context, target string, token *string) ([]cttypes.EnabledControlSummary, *string, error) {
			out, err := client.ListEnabledControls(ctx, &controltower.ListEnabledControlsInput{TargetIdentifier: &target, MaxResults: aws.Int32(ctowerControlPageCap), NextToken: token})
			// Only a first page is a refused target: a later page's
			// refusal truncates a listing that did answer.
			if token == nil && isAPIErrorCode(err, "ValidationException") {
				refused.skippedLeast(target, err)
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.EnabledControls, out.NextToken, nil
		},
		func(target string, c cttypes.EnabledControlSummary) (*store.Resource, string, string) {
			mu.Lock()
			listed[target] = append(listed[target], c)
			mu.Unlock()
			arn := sv(c.Arn)
			return arnRow("enabled controls", c.Arn, c.ControlIdentifier, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeControlTowerEnabledControl, NativeID: arn,
				Name: &arn, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		},
		tags, p)
	if failed {
		return nil, true
	}
	refused.warnCode(st, ctowerListControls, acct, region, "ValidationException", "target(s)",
		"the service may refuse to list a target's enabled controls, so none were stored for those targets")
	return listed, false
}

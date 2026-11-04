package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/auditmanager"
	amtypes "github.com/aws/aws-sdk-go-v2/service/auditmanager/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isAuditManagerNotEnabled disambiguates "Audit Manager not set up in this
// account" from real IAM denial: both surface as AccessDeniedException, but
// the not-enabled variant carries the "complete AWS Audit Manager setup"
// message. Same pattern as isMacieNotEnabled (per aws/CLAUDE.md "Macie
// variant — code+message disambiguation").
func isAuditManagerNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "complete AWS Audit Manager setup")
}

func init() {
	registerType(restype.Descriptor{Type: TypeAuditManagerAssessment, Service: "auditmanager"})
	registerType(restype.Descriptor{Type: TypeAuditManagerControl, Service: "auditmanager"})
	registerType(restype.Descriptor{Type: TypeAuditManagerFramework, Service: "auditmanager"})
	registerService(serviceEntry{
		name: "aws:auditmanager",
		fn:   scanAuditManager,
	})
}

const (
	audmListAssessmentsOp  = "auditmanager:ListAssessments"
	audmGetAssessmentOp    = "auditmanager:GetAssessment"
	audmListFrameworksOp   = "auditmanager:ListAssessmentFrameworks"
	audmListControlsOp     = "auditmanager:ListControls"
	audmListTagsForResOp   = "auditmanager:ListTagsForResource"
	audmAssessmentsNoID    = "assessments have no id"
	audmAssessmentsNoARN   = "assessments were read without an ARN"
	audmListingPageMaximum = 1000
)

// audmGone is the not-found code GetAssessment and ListTagsForResource model,
// for a resource deleted since it was listed.
var audmGone = []string{"ResourceNotFoundException"}

// auditmanagerAPI is the narrow set of Audit Manager operations called by
// the scanAuditManager sub-phases.
type auditmanagerAPI interface {
	ListAssessments(context.Context, *auditmanager.ListAssessmentsInput, ...func(*auditmanager.Options)) (*auditmanager.ListAssessmentsOutput, error)
	GetAssessment(context.Context, *auditmanager.GetAssessmentInput, ...func(*auditmanager.Options)) (*auditmanager.GetAssessmentOutput, error)
	ListAssessmentFrameworks(context.Context, *auditmanager.ListAssessmentFrameworksInput, ...func(*auditmanager.Options)) (*auditmanager.ListAssessmentFrameworksOutput, error)
	ListControls(context.Context, *auditmanager.ListControlsInput, ...func(*auditmanager.Options)) (*auditmanager.ListControlsOutput, error)
	ListTagsForResource(context.Context, *auditmanager.ListTagsForResourceInput, ...func(*auditmanager.Options)) (*auditmanager.ListTagsForResourceOutput, error)
}

func scanAuditManager(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := auditmanager.NewFromConfig(acct.cfg, func(o *auditmanager.Options) { o.Region = region })
	return scanAuditManagerEntities(ctx, client, acct, region, st, scanID)
}

// scanAuditManagerEntities discovers Audit Manager assessments, frameworks
// and controls in one region, one phase at a time. A phase's error does not
// stop the later ones; a failed write and ctx's end do, and so does an
// NXDOMAIN of the first listing with nothing stored (the region does not
// serve the service) (runHostPhases). Standard frameworks and controls (the
// AWS-provided catalogue: PCI DSS, HIPAA, ...) are stored ManagedByProvider.
// Assessment reports, share requests, delegations and evidence are not
// listed: they are event and state data, not graph nodes.
//
// Audit Manager refuses an account that has not set it up with an
// AccessDeniedException asking to complete the setup
// (isAuditManagerNotEnabled). When a listing's first page is refused so
// before any row was stored, no later listing runs and the scan is reported
// disabled. Refused so after rows were stored, it is the denial it then
// reads as.
func scanAuditManagerEntities(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := audmTags(client).sharingDenials()
	var notEnabled error
	total, inserted, err := runHostPhases(ctx, st, acct, region, "auditmanager", 0, []hostPhase{
		{op: audmListAssessmentsOp, run: func(p *phaseTotals) bool {
			return scanAuditManagerAssessments(ctx, client, acct, region, st, scanID, &notEnabled, p)
		}},
		{op: audmListFrameworksOp, run: func(p *phaseTotals) bool {
			return scanAuditManagerFrameworks(ctx, client, acct, region, st, scanID, &notEnabled, tags, p)
		}},
		{op: audmListControlsOp, run: func(p *phaseTotals) bool {
			return scanAuditManagerControls(ctx, client, acct, region, st, scanID, &notEnabled, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	// notEnabled is set only while nothing was stored, and no listing runs
	// after it; err is then an earlier listing's error or ctx's end, if
	// anything, which is reported instead.
	if notEnabled != nil && err == nil {
		return 0, 0, markServiceDisabled(notEnabled)
	}
	return total, inserted, err
}

// audmNotEnabledGap reports whether err, a listing's first-page error, says
// Audit Manager is not set up while p holds no stored row, and records it in
// notEnabled.
func audmNotEnabledGap(notEnabled *error, p *phaseTotals) func(error) bool {
	return func(err error) bool {
		if p.total > 0 || !isAuditManagerNotEnabled(err) {
			return false
		}
		*notEnabled = err
		return true
	}
}

// audmTags reads a framework's or control's tags with ListTagsForResource,
// sent the row's NativeID: the ARN its listing returned. The Service
// Reference gives assessmentFramework and control the aws:ResourceTag
// condition key and lists both under TagResource; CreateAssessmentFramework
// and CreateControl take tags, and neither listed element carries them. Its
// ListTagsForResource entry names assessment and control only, but the Audit
// Manager user guide's "Reviewing a framework" page (review-frameworks.html)
// sends the op an assessmentFramework ARN, for a standard or a custom
// framework. Standard rows (ManagedByProvider) are read too: the user guide's
// "Reviewing a standard control" page
// (control-library-review-standard-controls.html) lists a standard control's
// tags and reads them with the op, and review-frameworks.html does the same
// for a standard framework. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
func audmTags(client auditmanagerAPI) rowTagReader {
	return rowTagReader{
		op:       audmListTagsForResOp,
		notFound: audmGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &auditmanager.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// audmAssessmentRead is one assessment's GetAssessment result.
type audmAssessmentRead struct {
	assessment *amtypes.Assessment
	err        error
}

// scanAuditManagerAssessments lists the assessments, reads each one with
// GetAssessment and stores the assessments with the tags GetAssessment
// returns. It reports whether the write failed, after which the scanner
// must stop.
//
// The listed element has no ARN, so an assessment whose read fails has no
// row to store. One GetAssessment no longer finds (ResourceNotFoundException,
// deleted after it was listed) is dropped silently. A denied read warns once,
// after the write succeeded; any other error is returned after the others
// are stored, a transient one apart so that it is demoted beside a hard
// error. A listed assessment without an id, or read without an ARN, is
// warned about.
func scanAuditManagerAssessments(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled *error, p *phaseTotals) (storeWriteFailed bool) {
	listed, listErr := readTokenPages(ctx, firstPageGapPages(audmNotEnabledGap(notEnabled, p), func(ctx context.Context, token *string) ([]amtypes.AssessmentMetadataItem, *string, error) {
		out, err := client.ListAssessments(ctx, &auditmanager.ListAssessmentsInput{MaxResults: sdkaws.Int32(audmListingPageMaximum), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.AssessmentMetadata, out.NextToken, nil
	}))
	var (
		listSkips, getSkips notStoredSkips
		ids                 []string
	)
	for _, a := range listed {
		if sv(a.Id) == "" {
			listSkips.skipped(audmAssessmentsNoID, cmp.Or(sv(a.Name), noIdentifier))
			continue
		}
		ids = append(ids, *a.Id)
	}
	reads, stopped := readEachKey(ctx, ids, func(ctx context.Context, id string) audmAssessmentRead {
		out, err := client.GetAssessment(ctx, &auditmanager.GetAssessmentInput{AssessmentId: sdkaws.String(id)})
		if err != nil {
			return audmAssessmentRead{err: err}
		}
		return audmAssessmentRead{assessment: out.Assessment}
	})
	var (
		rows       []*store.Resource
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for i, read := range reads {
		id := ids[i]
		// The listing reached the endpoint, so an NXDOMAIN now is a failure
		// of this scan, not a region gap.
		switch err := lateNXDOMAIN(audmGetAssessmentOp, read.err); {
		case err == nil:
			if r := audmAssessmentRow(acct, region, scanID, read.assessment); r != nil {
				rows = append(rows, r)
			} else {
				getSkips.skipped(audmAssessmentsNoARN, id)
			}
		case isAPIErrorCode(err, audmGone...):
		case isAccessDenied(err):
			denied.skipped(id, err)
		case transients.drop(id, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", audmGetAssessmentOp, id, err))
		}
	}
	// While a later phase follows, runHostPhases reports ctx's end itself, as
	// the phases it did not run. After a failed write it runs no later phase,
	// and this is what reports the assessments not read.
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", audmGetAssessmentOp, stopped))
	}
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d assessment(s) not read, so not stored, first %s: %w", audmGetAssessmentOp, t.count, t.first, t.err)
	}
	hard := errors.Join(errs...)
	total, inserted, err := storeReadRows(st, acct, region, audmListAssessmentsOp, rows, listErr, func() {
		listSkips.warn(st, audmListAssessmentsOp, acct, region)
		getSkips.warn(st, audmGetAssessmentOp, acct, region)
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, audmGetAssessmentOp, acct.ID, region, fmt.Errorf("%d assessment(s) not stored, first %s: %w", denied.count, denied.first, denied.err))
		}
	})
	if errors.Is(err, store.ErrStoreWrite) {
		return p.addPhase(audmListAssessmentsOp, total, inserted, errors.Join(err, hard, transient))
	}
	p.errs.add(audmGetAssessmentOp, hard)
	p.errs.add(audmGetAssessmentOp, transient)
	return p.addPhase(audmListAssessmentsOp, total, inserted, err)
}

// audmAssessmentRow is the row of assessment a GetAssessment returned; nil
// when it has no ARN to store it under.
func audmAssessmentRow(acct *account, region, scanID string, a *amtypes.Assessment) *store.Resource {
	if a == nil || sv(a.Arn) == "" {
		return nil
	}
	var name, status string
	if a.Metadata != nil {
		name = sv(a.Metadata.Name)
		status = string(a.Metadata.Status)
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeAuditManagerAssessment,
		NativeID:       *a.Arn,
		Name:           &name,
		Region:         &region,
		Status:         &status,
		TagsJSON:       mapTagsJSON(a.Tags),
		AttributesJSON: mustJSON(a),
		DiscoveredBy:   scanID,
	}
}

// scanAuditManagerFrameworks stores the custom and the standard frameworks,
// a page at a time, with their tags (audmTags). It reports whether a write
// failed, after which the scanner must stop.
func scanAuditManagerFrameworks(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled *error, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return audmStoreLibrary(ctx, st, acct, region, audmListFrameworksOp, "frameworks", amtypes.FrameworkTypeStandard,
		[]amtypes.FrameworkType{amtypes.FrameworkTypeCustom, amtypes.FrameworkTypeStandard}, notEnabled,
		func(ctx context.Context, ft amtypes.FrameworkType, token *string) ([]amtypes.AssessmentFrameworkMetadata, *string, error) {
			out, err := client.ListAssessmentFrameworks(ctx, &auditmanager.ListAssessmentFrameworksInput{
				FrameworkType: ft, MaxResults: sdkaws.Int32(audmListingPageMaximum), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.FrameworkMetadataList, out.NextToken, nil
		},
		func(what string, f amtypes.AssessmentFrameworkMetadata, managed bool) (*store.Resource, string, string) {
			return arnRow(what, f.Arn, f.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAuditManagerFramework, NativeID: sv(f.Arn), Name: sdkaws.String(sv(f.Name)), Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID, ManagedByProvider: managed,
			})
		},
		tags, p)
}

// scanAuditManagerControls stores the custom and the standard controls, a
// page at a time, with their tags (audmTags). It reports whether a write
// failed, after which the scanner must stop.
func scanAuditManagerControls(ctx context.Context, client auditmanagerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled *error, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return audmStoreLibrary(ctx, st, acct, region, audmListControlsOp, "controls", amtypes.ControlTypeStandard,
		[]amtypes.ControlType{amtypes.ControlTypeCustom, amtypes.ControlTypeStandard}, notEnabled,
		func(ctx context.Context, ct amtypes.ControlType, token *string) ([]amtypes.ControlMetadata, *string, error) {
			out, err := client.ListControls(ctx, &auditmanager.ListControlsInput{
				ControlType: ct, MaxResults: sdkaws.Int32(audmListingPageMaximum), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ControlMetadataList, out.NextToken, nil
		},
		func(what string, c amtypes.ControlMetadata, managed bool) (*store.Resource, string, string) {
			return arnRow(what, c.Arn, c.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAuditManagerControl, NativeID: sv(c.Arn), Name: sdkaws.String(sv(c.Name)), Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID, ManagedByProvider: managed,
			})
		},
		tags, p)
}

// audmStoreLibrary stores op's listing of what once per kind in kinds, in
// turn, a page at a time with each row's tags read through tags
// (storeTokenPagesTags); row builds a listed item's row, managed for the
// standard kind. One kind's failure does not stop the next; a failed write
// does, and so does a first-page refusal saying Audit Manager is not set up
// (audmNotEnabledGap). A denied listing ends that kind's listing, keeping its
// pages read. One warning under op, sent after the writes succeeded, carries
// every kind's denial, each named by its kind: the kinds can be denied at
// different pages, and only a denial after the first page says that later
// pages went unread.
func audmStoreLibrary[Kind ~string, Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string, standard Kind, kinds []Kind, notEnabled *error,
	page func(ctx context.Context, kind Kind, token *string) ([]Item, *string, error),
	row func(what string, it Item, managed bool) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var denials []error
	for _, kind := range kinds {
		if *notEnabled != nil {
			break
		}
		d := listDenial{isDenied: isAccessDenied, unread: what + " on later pages"}
		kindPage := func(ctx context.Context, token *string) ([]Item, *string, error) { return page(ctx, kind, token) }
		kindRow := func(it Item) (*store.Resource, string, string) { return row(what, it, kind == standard) }
		if storeTokenPagesTags(ctx, st, acct, region, op, deniedPages(&d, firstPageGapPages(audmNotEnabledGap(notEnabled, p), kindPage)), kindRow, tags, p) {
			return true
		}
		if d.err != nil {
			denials = append(denials, fmt.Errorf("%s %s: %w", kind, what, d.err))
		}
	}
	if len(denials) > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, errors.Join(denials...))
	}
	return false
}

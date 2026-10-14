package aws

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTAuditSuppression, Service: "iot"})
}

type iotAuditSuppressionAPI interface {
	ListAuditSuppressions(context.Context, *iot.ListAuditSuppressionsInput, ...func(*iot.Options)) (*iot.ListAuditSuppressionsOutput, error)
}

// iotAuditSuppressionPageCap is the documented maximum of maxResults.
const iotAuditSuppressionPageCap = 250

// iotAuditSuppressionNativeID builds the identity of a Device Defender audit
// suppression, which has no ARN or id: the API keys it by (checkName,
// resourceIdentifier) within an account and region. Shape:
//
//	arn:aws:iot:{region}:{account}:audit-suppression/{PathEscape(checkName)}/{identifier}
//
// {identifier} is iotResourceIdentifierKey's query-string encoding. Both parts
// are escaped, so the "/" between them stays the only separator and the id is
// injective. Returns "" when either part is empty.
func iotAuditSuppressionNativeID(region, accountID string, s iottypes.AuditSuppression) string {
	check := sv(s.CheckName)
	key := iotResourceIdentifierKey(s.ResourceIdentifier)
	if check == "" || key == "" {
		return ""
	}
	return fmt.Sprintf("arn:aws:iot:%s:%s:audit-suppression/%s/%s", region, accountID, url.PathEscape(check), key)
}

// iotResourceIdentifierKey encodes a ResourceIdentifier as a query string
// keyed by the API's wire member names, nested members dotted
// (policyVersionIdentifier.policyName=p&policyVersionIdentifier.policyVersionId=1).
// url.Values.Encode sorts keys and escapes values, so equal identifiers give
// equal keys whatever the field order. The API sets one member per identifier,
// but every listed member that is set is encoded, so identifiers differing in
// a listed member never share a key. A member not listed here (one a later SDK
// adds) is ignored: alone it leaves the key empty, which the caller skips and
// reports; beside a listed member, two suppressions differing only in it would
// share a key. TestIoTResourceIdentifierKey_EncodesEveryMember fails on an SDK
// bump that adds one.
func iotResourceIdentifierKey(ri *iottypes.ResourceIdentifier) string {
	if ri == nil {
		return ""
	}
	v := url.Values{}
	add := func(k string, p *string) {
		if s := sv(p); s != "" {
			v.Set(k, s)
		}
	}
	add("account", ri.Account)
	add("caCertificateId", ri.CaCertificateId)
	add("clientId", ri.ClientId)
	add("cognitoIdentityPoolId", ri.CognitoIdentityPoolId)
	add("deviceCertificateArn", ri.DeviceCertificateArn)
	add("deviceCertificateId", ri.DeviceCertificateId)
	add("iamRoleArn", ri.IamRoleArn)
	add("roleAliasArn", ri.RoleAliasArn)
	if p := ri.PolicyVersionIdentifier; p != nil {
		add("policyVersionIdentifier.policyName", p.PolicyName)
		add("policyVersionIdentifier.policyVersionId", p.PolicyVersionId)
	}
	if c := ri.IssuerCertificateIdentifier; c != nil {
		add("issuerCertificateIdentifier.issuerCertificateSerialNumber", c.IssuerCertificateSerialNumber)
		add("issuerCertificateIdentifier.issuerCertificateSubject", c.IssuerCertificateSubject)
		add("issuerCertificateIdentifier.issuerId", c.IssuerId)
	}
	return v.Encode()
}

// scanIoTAuditSuppressions stores every Device Defender audit suppression the
// region lists. The list element carries every field DescribeAuditSuppression
// returns, so there is no per-item describe. Expired suppressions are stored
// as listed; ExpirationDate and SuppressIndefinitely stay in attributes.
func scanIoTAuditSuppressions(ctx context.Context, client iotAuditSuppressionAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	const op = "iot:ListAuditSuppressions"
	pager := iot.NewListAuditSuppressionsPaginator(client, &iot.ListAuditSuppressionsInput{}, func(o *iot.ListAuditSuppressionsPaginatorOptions) {
		o.Limit = iotAuditSuppressionPageCap
	})
	unkeyed := 0
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
				break
			}
			// Device Defender is not routed in every region; the sibling Defender
			// phases see a bare 404 "No method found matching route" there.
			if firstPage && isHTTP404(perr) {
				return 0, 0, nil
			}
			err = fmt.Errorf("%s: %w", op, perr)
			break
		}
		batch := make([]*store.Resource, 0, len(out.Suppressions))
		for _, s := range out.Suppressions {
			nativeID := iotAuditSuppressionNativeID(region, acct.ID, s)
			if nativeID == "" {
				unkeyed++
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTAuditSuppression,
				NativeID:       nativeID,
				Name:           s.CheckName,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return total, inserted, fmt.Errorf("upsert iot audit suppressions: %w", uerr)
		}
		total += len(batch)
		inserted += n
	}
	if unkeyed > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws",
			Service:  op,
			Scope:    acct.ID + "/" + region,
			Message: fmt.Sprintf("skipped %d audit suppression(s): no NativeID could be built because checkName was empty "+
				"or resourceIdentifier set none of the members disco encodes", unkeyed),
		})
	}
	return total, inserted, err
}

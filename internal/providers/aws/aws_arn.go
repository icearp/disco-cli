package aws

import (
	"fmt"
	"strings"
)

// ARN and synthetic-NativeID builders shared across services. Every helper
// here is called from at least one resolver/scanner outside its owning
// service, so centralising the shape keeps scanner-side and resolver-side
// in sync — a shape bug fixes in one place rather than silently drifting.
//
// Conventions per service:
//   - ec2ARN: arn:aws:ec2:{region}:{account}:{kind}/{id} — slash separator.
//   - rdsARN: arn:aws:rds:{region}:{account}:{kind}:{id} — colon separator.
//   - apigatewayARN: arn:aws:apigateway:{region}::/{path...} — empty
//     account segment, leading slash, variadic path components joined "/".
//   - logGroupNativeIDFromName: arn:aws:logs:{region}:{account}:log-group:{name}.
//     SDK ARNs come back with a trailing ":*"; the NativeID strips it.
//   - macieSessionNativeID: arn:aws:macie2:{region}:{account}:session — the
//     Macie API exposes no session ARN; the synthetic form mirrors the
//     canonical macie2 shape so rescans dedupe.
//   - ssoAssignmentNativeID: {psArn}/account/{acct}/{principalType}/{principalID}.
//     AWS issues no canonical ARN for assignments; permission-set ARN already
//     embeds the instance id, so extending it carries enough context.
//   - identityStoreUserNativeID / identityStoreGroupNativeID:
//     arn:aws:identitystore::{ownerAcct}:{user|group}/{storeID}/{id}.
//     Identity Store APIs return no ARNs.

// ec2ARN builds a standard EC2 ARN: arn:aws:ec2:{region}:{account}:{type}/{id}.
func ec2ARN(region, accountID, resourceType, id string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:%s/%s", region, accountID, resourceType, id)
}

// rdsARN builds a standard RDS ARN. RDS uses ":" as the resource separator
// (e.g. arn:aws:rds:us-east-1:123456789012:cluster:my-cluster), unlike EC2
// which uses "/".
func rdsARN(region, accountID, resource, id string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:%s:%s", region, accountID, resource, id)
}

// apigatewayARN builds an API Gateway ARN of the form
// arn:aws:apigateway:{region}::/p1/p2/.... The account segment is always
// empty for API Gateway (REST and HTTP/WebSocket v2 share this shape).
func apigatewayARN(region string, path ...string) string {
	return "arn:aws:apigateway:" + region + "::/" + strings.Join(path, "/")
}

// logGroupNativeIDFromName reconstructs the log group ARN (NativeID) from its
// name: arn:aws:logs:{region}:{account}:log-group:{name}. The SDK appends ":*"
// to log-group ARNs in some response shapes; the NativeID is the clean form
// without that suffix.
func logGroupNativeIDFromName(accountID, region, name string) string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", region, accountID, name)
}

// macieSessionNativeID synthesises an ARN-shaped identifier for the per-region
// Macie session. The Macie API exposes no session ARN; the synthetic form
// matches the canonical macie2 ARN shape so rescans dedupe.
func macieSessionNativeID(accountID, region string) string {
	return fmt.Sprintf("arn:aws:macie2:%s:%s:session", region, accountID)
}

// ssoAssignmentNativeID synthesises a stable identifier for an account
// assignment. AWS does not issue a canonical ARN for assignments, so the
// permission-set ARN (which already carries the instance id) is extended
// with the account, principal type, and principal id.
func ssoAssignmentNativeID(psArn, accountID, principalType, principalID string) string {
	return psArn + "/account/" + accountID + "/" + principalType + "/" + principalID
}

// identityStoreUserNativeID and identityStoreGroupNativeID synthesise
// ARN-shaped IDs scoped by the instance owner account and identity-store
// id. Identity Store APIs do not return ARNs.
func identityStoreUserNativeID(ownerAccountID, identityStoreID, userID string) string {
	return "arn:aws:identitystore::" + ownerAccountID + ":user/" + identityStoreID + "/" + userID
}

func identityStoreGroupNativeID(ownerAccountID, identityStoreID, groupID string) string {
	return "arn:aws:identitystore::" + ownerAccountID + ":group/" + identityStoreID + "/" + groupID
}

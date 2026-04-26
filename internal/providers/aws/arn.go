package aws

import "strings"

// ARN helpers for services where the same ARN shape is rebuilt at many
// callsites. Each helper centralizes one service's canonical shape so a
// shape bug is fixed in one place rather than silently drifting between
// scanner and resolver.
//
// Service-specific notes:
//   - ec2ARN (in aws.go): `arn:aws:ec2:{region}:{account}:{kind}/{id}` — slash separator.
//   - rdsARN (in rds_scanners.go): `arn:aws:rds:{region}:{account}:{kind}:{id}` — colon separator.
//   - apigatewayARN (here): `arn:aws:apigateway:{region}::/{path}` — empty account
//     segment, leading slash on the path. Variadic path components are joined
//     with `/`, matching the REST surface (`/restapis/{id}/stages/{name}` etc.).

// apigatewayARN builds an API Gateway ARN of the form
// `arn:aws:apigateway:{region}::/p1/p2/...`. The account segment is always
// empty for API Gateway (REST and HTTP/WebSocket v2 share this shape).
func apigatewayARN(region string, path ...string) string {
	return "arn:aws:apigateway:" + region + "::/" + strings.Join(path, "/")
}

package aws

import (
	"fmt"
	"reflect"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	acotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizerautomation/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	firehosetypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	wellarchitectedtypes "github.com/aws/aws-sdk-go-v2/service/wellarchitected/types"
)

// awsTag is the set of AWS SDK tag types that carry Key and Value string pointers.
// Every member needs a matching case in awsTagKV; TestAWSTagKV_CoversUnion enforces it.
type awsTag interface {
	acmtypes.Tag | acotypes.Tag | cfntypes.Tag | cloudfronttypes.Tag | cloudhsmv2types.Tag | ec2types.Tag | ecrtypes.Tag | ecstypes.Tag | elasticachetypes.Tag | firehosetypes.Tag | iamtypes.Tag | kinesistypes.Tag | rdstypes.Tag | route53types.Tag | wellarchitectedtypes.Tag
}

// awsTagsJSON converts a slice of an awsTag union member to a JSON-encoded {key:value}
// map; see kvTagsJSON for the output contract. Use it for types already in the union.
// A new SDK tag type goes to kvTagsJSON with an accessor at its call site instead of
// into the union, so parallel additions do not collide on the union line.
func awsTagsJSON[T awsTag](tags []T) *string {
	return kvTagsJSON(tags, awsTagKV[T])
}

func awsTagKV[T awsTag](t T) (key, value *string) {
	switch tt := any(t).(type) {
	case acmtypes.Tag:
		return tt.Key, tt.Value
	case acotypes.Tag:
		return tt.Key, tt.Value
	case cfntypes.Tag:
		return tt.Key, tt.Value
	case cloudfronttypes.Tag:
		return tt.Key, tt.Value
	case cloudhsmv2types.Tag:
		return tt.Key, tt.Value
	case ec2types.Tag:
		return tt.Key, tt.Value
	case ecrtypes.Tag:
		return tt.Key, tt.Value
	case ecstypes.Tag:
		return tt.Key, tt.Value
	case elasticachetypes.Tag:
		return tt.Key, tt.Value
	case firehosetypes.Tag:
		return tt.Key, tt.Value
	case iamtypes.Tag:
		return tt.Key, tt.Value
	case kinesistypes.Tag:
		return tt.Key, tt.Value
	case rdstypes.Tag:
		return tt.Key, tt.Value
	case route53types.Tag:
		return tt.Key, tt.Value
	case wellarchitectedtypes.Tag:
		return tt.Key, tt.Value
	}
	// Unreachable while TestAWSTagKV_CoversUnion passes; a panic beats returning nil,
	// which would drop every tag of the uncovered type without a trace.
	rt := reflect.TypeOf(t)
	panic(fmt.Sprintf("awsTagKV: awsTag member %s.%s has no case; add one to awsTagKV", rt.PkgPath(), rt.Name()))
}

// kvTagsJSON converts a slice of any SDK tag type to a JSON-encoded {key:value} map,
// reading each tag through kv. Returns nil when no tag has a key. A tag with a nil
// Key is skipped (nothing to key it by). A nil Value is stored as "": several SDK
// models make the value optional (ElastiCache documents "May be null"), and dropping
// the tag would lose a key the resource carries.
func kvTagsJSON[T any](tags []T, kv func(T) (key, value *string)) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		k, v := kv(t)
		if k == nil {
			continue
		}
		m[*k] = sdkaws.ToString(v)
	}
	if len(m) == 0 {
		return nil
	}
	s := mustJSON(m)
	return &s
}

// mapTagsJSON converts a map[string]string tag map to a JSON-encoded {key:value}
// string pointer. Use it for services whose SDK returns tags as a plain map rather
// than a slice of typed Tag structs. Returns nil when the map is empty.
func mapTagsJSON(tags map[string]string) *string {
	if len(tags) == 0 {
		return nil
	}
	s := mustJSON(tags)
	return &s
}

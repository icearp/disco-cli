package coverage

import (
	"reflect"
	"testing"
)

func TestDiffRegions(t *testing.T) {
	cases := []struct {
		name   string
		static []string
		live   []string
		want   []RegionRow
	}{
		{
			name:   "empty",
			static: nil,
			live:   nil,
			want:   []RegionRow{},
		},
		{
			name:   "covered",
			static: []string{"us-east-1", "us-west-2"},
			live:   []string{"us-east-1", "us-west-2"},
			want: []RegionRow{
				{Region: "us-east-1", Status: RegionCovered},
				{Region: "us-west-2", Status: RegionCovered},
			},
		},
		{
			name:   "stale",
			static: []string{"us-east-1", "old-region"},
			live:   []string{"us-east-1"},
			want: []RegionRow{
				{Region: "old-region", Status: RegionStale},
				{Region: "us-east-1", Status: RegionCovered},
			},
		},
		{
			name:   "missing",
			static: []string{"us-east-1"},
			live:   []string{"us-east-1", "ap-southeast-99"},
			want: []RegionRow{
				{Region: "ap-southeast-99", Status: RegionMissing},
				{Region: "us-east-1", Status: RegionCovered},
			},
		},
		{
			name:   "mix",
			static: []string{"a", "b", "c"},
			live:   []string{"b", "c", "d"},
			want: []RegionRow{
				{Region: "a", Status: RegionStale},
				{Region: "b", Status: RegionCovered},
				{Region: "c", Status: RegionCovered},
				{Region: "d", Status: RegionMissing},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffRegions(c.static, c.live)
			if got == nil {
				got = []RegionRow{}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

package database

import "testing"

func TestNames(t *testing.T) {
	cases := []struct{ zone, region, name, family, series string }{
		{"us-east-1a", "us-east-1", "c7i.48xlarge", "c7i", "c7i"},
		{"us-east-1f", "us-east-1", "c7i.metal-48xl", "c7i", "c7i.metal"},
		{"ap-southeast-2c", "ap-southeast-2", "u-6tb1.112xlarge", "u-6tb1", "u-6tb1"},
		{"us-east-1-bos-1a", "us-east-1", "m7g.metal", "m7g", "m7g.metal"},
	}
	for _, c := range cases {
		if got := RegionOf(c.zone); got != c.region {
			t.Errorf("RegionOf(%s) = %s, want %s", c.zone, got, c.region)
		}
		if got := FamilyOf(c.name); got != c.family {
			t.Errorf("FamilyOf(%s) = %s, want %s", c.name, got, c.family)
		}
		if got := SeriesOf(c.name); got != c.series {
			t.Errorf("SeriesOf(%s) = %s, want %s", c.name, got, c.series)
		}
	}
}

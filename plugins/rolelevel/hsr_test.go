package rolelevel

import "testing"

func TestHSRPolicyRanges(t *testing.T) {
	for _, tc := range []struct {
		key            string
		valid, invalid []any
	}{
		{"hsrUidLimit", []any{0, 1, 100, int64(2)}, []any{-1, 101, 1.5, "1", nil}},
		{"hsrRefreshIntervalMinutes", []any{1, 10, 1440, int64(60)}, []any{0, 1441, 1.5, "10", nil}},
	} {
		for _, v := range tc.valid {
			if _, err := defaultCatalog.NormalizeConst(tc.key, v); err != nil {
				t.Fatalf("%s有効値を拒否: %v", tc.key, err)
			}
		}
		for _, v := range tc.invalid {
			if _, err := defaultCatalog.NormalizeConst(tc.key, v); err == nil {
				t.Fatalf("%s不正値を許可: %v", tc.key, v)
			}
		}
	}
	for _, tc := range []struct {
		key              string
		base, additional float64
		valid            bool
	}{
		{"hsrUidLimit", 1, 1, true}, {"hsrUidLimit", 0, -1, false}, {"hsrUidLimit", 100, 1, false},
		{"hsrRefreshIntervalMinutes", 10, 1, true}, {"hsrRefreshIntervalMinutes", 1, -1, false}, {"hsrRefreshIntervalMinutes", 1440, 1, false},
	} {
		err := validateRangeRules([]PolicyRange{{Key: tc.key, Type: RangeMultiplier, Start: 1, End: 5, Base: tc.base, Additional: tc.additional}}, defaultCatalog)
		if (err == nil) != tc.valid {
			t.Fatalf("%s倍率範囲の判定不正: %v", tc.key, err)
		}
	}
}

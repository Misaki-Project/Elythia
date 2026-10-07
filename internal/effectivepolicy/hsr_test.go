package effectivepolicy

import (
	"math"
	"testing"
)

func TestHSRPolicyBounds(t *testing.T) {
	for _, tc := range []struct {
		key            string
		defaultValue   int
		valid, invalid []any
	}{
		{"hsrUidLimit", 1, []any{0, 1, 100, int64(2), float64(3)}, []any{-1, 101, 1.5, "1", true, nil, math.NaN(), math.Inf(1)}},
		{"hsrRefreshIntervalMinutes", 10, []any{1, 10, 1440, int64(60), float64(5)}, []any{0, -1, 1441, 1.5, "10", true, nil, math.NaN(), math.Inf(1)}},
	} {
		if Defaults()[tc.key] != tc.defaultValue {
			t.Fatalf("%sの既定値が不正", tc.key)
		}
		for _, v := range tc.valid {
			if !ValidatePolicyValue(tc.key, v) {
				t.Errorf("%s: 有効値を拒否: %v", tc.key, v)
			}
		}
		for _, v := range tc.invalid {
			if ValidatePolicyValue(tc.key, v) {
				t.Errorf("%s: 不正値を許可: %v", tc.key, v)
			}
		}
	}
}

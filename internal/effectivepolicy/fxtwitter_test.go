package effectivepolicy

import (
	"math"
	"testing"
)

func TestFxTwitterPolicyBounds(t *testing.T) {
	for _, tc := range []struct {
		key            string
		defaultValue   any
		valid, invalid []any
	}{
		{"canUseFxTwitterPreview", true, []any{true, false}, []any{1, "true", nil}},
		{"fxTwitterColdFetchesPerHour", 100, []any{1, 100, 10000, int64(50), float64(3)}, []any{0, -1, 10001, int64(0), int64(10001), float64(0), 1.5, "1", true, nil, math.NaN(), math.Inf(1)}},
		{"fxTwitterColdFetchesPerDay", 600, []any{1, 600, 100000, int64(50), float64(3)}, []any{0, -1, 100001, int64(0), int64(100001), float64(0), 1.5, "1", true, nil, math.NaN(), math.Inf(1)}},
	} {
		if Defaults()[tc.key] != tc.defaultValue {
			t.Fatalf("%sの既定値が不正: %v", tc.key, Defaults()[tc.key])
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

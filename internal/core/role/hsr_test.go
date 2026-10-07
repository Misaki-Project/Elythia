package role

import "testing"

func TestHSRPolicyAggregation(t *testing.T) {
	if got := aggregatePolicyValues("hsrRefreshIntervalMinutes", 10, []any{60, 5, int64(30)}); got != 5 {
		t.Fatalf("取得間隔は同優先度min: %v", got)
	}
	if got := aggregatePolicyValues("hsrRefreshIntervalMinutes", 10, []any{0, 1441, 1.5}); got != 10 {
		t.Fatalf("不正値を集約: %v", got)
	}
	if got := aggregatePolicyValues("hsrUidLimit", 1, []any{0, 2, 5}); got != 5 {
		t.Fatalf("UID上限は同優先度max: %v", got)
	}
}

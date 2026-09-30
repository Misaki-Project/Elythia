package rolelevel

import (
	"errors"
	"fmt"
	"sort"
)

// RangeType names one level-based policy rule shape.
type RangeType string

const (
	// RangeBase keeps the instance / native default. **置換 contribution は出さない**
	// ので、native の静的 role contribution はそのまま生き残る (Task 9)。
	RangeBase RangeType = "base"
	// RangeConst uses a fixed value.
	RangeConst RangeType = "const"
	// RangeMultiplier scales from Base by Additional per stage inside the range.
	RangeMultiplier RangeType = "multiplier"
)

// PolicyRange replaces the native policy of Key for the progression stages
// [Start, End). Stages are 1-based, Start is inclusive and End is exclusive, so
// the ranges of one config tile stages 1..levelUps+1 as [1, levelUps+2).
type PolicyRange struct {
	Type  RangeType `json:"type"`
	Key   string    `json:"key,omitempty"`
	Start int64     `json:"start"`
	End   int64     `json:"end"`
	// Value is the const value; it is only meaningful for RangeConst.
	Value any `json:"value,omitempty"`
	// Base and Additional drive RangeMultiplier only. They are read as float64
	// because the multiplier is computed in floating point and floored.
	Base       float64 `json:"base,omitempty"`
	Additional float64 `json:"additional,omitempty"`
}

// MaxPolicyRanges bounds how many rules one config may carry. Each range is one
// policy key over a stage interval, so a sane configuration is a handful; the
// bound exists so a pathological payload cannot make validation itself the
// expensive part of a save.
const MaxPolicyRanges = 256

// validateRanges validates independently ranged policy keys.
//
// Different policy keys may overlap: one level can change driveCapacityMb and
// antennaLimit at the same time. Ranges for the same key must not overlap.
// Missing stages mean "keep the native policy"; explicit base ranges are also
// accepted as no-op compatibility entries.
func validateRanges(ranges []PolicyRange, levelUps int64, cat *Catalog) error {
	if len(ranges) == 0 {
		return invalid(CodeInvalidRanges, "policyRanges",
			"最低1個必要です (既定は全範囲を覆う1個の base range)")
	}
	if len(ranges) > MaxPolicyRanges {
		return invalid(CodeInvalidRanges, "policyRanges",
			"%d 個以下で指定してください (%d)", MaxPolicyRanges, len(ranges))
	}
	wantEnd := levelUps + 2
	byKey := make(map[string][]PolicyRange)
	for i, r := range ranges {
		if r.Start < 1 {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d].start は 1 以上である必要があります (%d)", i, r.Start)
		}
		if r.End <= r.Start {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d]: end (%d) は start (%d) より大きい必要があります", i, r.End, r.Start)
		}
		if r.End > wantEnd {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d].end は到達可能範囲の終端 %d 以下である必要があります (%d)", i, wantEnd, r.End)
		}
		if r.Type != RangeBase {
			byKey[r.Key] = append(byKey[r.Key], r)
		}
	}
	for key, keyed := range byKey {
		sort.Slice(keyed, func(i, j int) bool { return keyed[i].Start < keyed[j].Start })
		for i := 1; i < len(keyed); i++ {
			if keyed[i].Start < keyed[i-1].End {
				return invalid(CodeInvalidRanges, "policyRanges",
					"policy %q の範囲 [%d,%d) と [%d,%d) が重複しています",
					key, keyed[i-1].Start, keyed[i-1].End, keyed[i].Start, keyed[i].End)
			}
		}
	}
	return validateRangeRules(ranges, cat)
}

// validateRangeRules rejects rules the host could not accept: unknown native keys,
// multipliers on non-numeric keys, and constants or multiplier results that do
// not match the native type.
func validateRangeRules(ranges []PolicyRange, cat *Catalog) error {
	for i, r := range ranges {
		field := fmt.Sprintf("policyRanges[%d]", i)
		switch r.Type {
		case RangeBase:
			// instance / native default を使うので key も値も要らない。
		case RangeConst:
			if _, known := cat.Kind(r.Key); !known {
				return invalid(CodeUnknownPolicyKey, field+".key",
					"%q はネイティブの policy key ではありません", r.Key)
			}
			if _, err := cat.NormalizeConst(r.Key, r.Value); err != nil {
				return reField(err, field+".value")
			}
		case RangeMultiplier:
			kind, known := cat.Kind(r.Key)
			if !known {
				return invalid(CodeUnknownPolicyKey, field+".key",
					"%q はネイティブの policy key ではありません", r.Key)
			}
			if !kind.Numeric() {
				return invalid(CodeMultiplierNotNumeric, field,
					"%q は %s なので multiplier は使えません (const で指定してください)", r.Key, kind)
			}
			// 両端だけ見る。range 内の値は n の1次式なので単調で、端点が
			// 受理されるなら中間も受理される。**全 stage を走査しないのは
			// levelUps が 9e15 になりうるため** (走査すると保存が落ちる)。
			for _, stage := range []int64{r.Start, r.End - 1} {
				v, err := rangeValue(r, stage)
				if err != nil {
					return err
				}
				if !cat.AcceptsValue(r.Key, v) {
					return invalid(CodeInvalidRangeValue, field,
						"stage %d の値 %v がネイティブの %q の型・範囲に合いません", stage, v, r.Key)
				}
			}
		default:
			return invalid(CodeInvalidRanges, field+".type",
				"type %q は %s|%s|%s のいずれかです", r.Type, RangeBase, RangeConst, RangeMultiplier)
		}
	}
	return nil
}

// reField re-labels a validation error with a more specific field path.
func reField(err error, field string) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return &ValidationError{Code: ve.Code, Field: field, Err: ve.Err}
	}
	return err
}

// rangesForStage returns every policy-key range covering stage. At most one
// non-base range per key can match because validation rejects same-key overlap.
func rangesForStage(ranges []PolicyRange, stage int64) []PolicyRange {
	out := make([]PolicyRange, 0)
	for _, r := range ranges {
		if r.Type != RangeBase && stage >= r.Start && stage < r.End {
			out = append(out, r)
		}
	}
	return out
}

// rangeForStage remains the single-range helper used by focused tests and old
// callers. New policy resolution uses rangesForStage.
func rangeForStage(ranges []PolicyRange, stage int64) (PolicyRange, bool) {
	matched := rangesForStage(ranges, stage)
	if len(matched) > 0 {
		return matched[0], true
	}
	return PolicyRange{}, false
}

// rangeValue resolves one range into the value the native policy should take.
//
// **base は nil を返す。** 呼び出し側は nil を「置換しない = native policy を
// そのまま使う」として扱う。base なのか「値が落ちた」のかを区別できるので、
// base の区間を黙って置換する形的ミスが起きない。
func rangeValue(r PolicyRange, stage int64) (any, error) {
	switch r.Type {
	case RangeBase:
		return nil, nil
	case RangeConst:
		return r.Value, nil
	case RangeMultiplier:
		// offset は range 内の0始まり位置。**baseLevel は見ない** (旧実装の
		// `effectiveLevel - startLevel` ずれは再現しない)。
		offset := float64(stage - r.Start)
		return nativeNumber(r.Base + r.Additional*offset)
	}
	return nil, invalid(CodeInvalidRanges, "policyRanges.type",
		"type %q は %s|%s|%s のいずれかです", r.Type, RangeBase, RangeConst, RangeMultiplier)
}

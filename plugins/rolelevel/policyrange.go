package rolelevel

import (
	"errors"
	"fmt"
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

// validateRanges rejects a range list that does not tile the reachable progression
// exactly, then applies the per-key rules.
//
// **継ぎ目だけを見て合計長は最後の End から判定する。** 半開区間なので
// `ranges[i].Start == ranges[i-1].End` が重複も欠落も無いことの証明になる。
func validateRanges(ranges []PolicyRange, levelUps int64, cat *Catalog) error {
	if len(ranges) == 0 {
		return invalid(CodeInvalidRanges, "policyRanges",
			"最低1個必要です (既定は全範囲を覆う1個の base range)")
	}
	if len(ranges) > MaxPolicyRanges {
		return invalid(CodeInvalidRanges, "policyRanges",
			"%d 個以下で指定してください (%d)", MaxPolicyRanges, len(ranges))
	}
	next := int64(1)
	for i, r := range ranges {
		if r.Start != next {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d].start は %d であるべきですが %d です (重複または欠落があります)",
				i, next, r.Start)
		}
		if r.End <= r.Start {
			return invalid(CodeInvalidRanges, "policyRanges",
				"policyRanges[%d]: end (%d) は start (%d) より大きい必要があります", i, r.End, r.Start)
		}
		next = r.End
	}
	if want := levelUps + 1; next-1 != want {
		return invalid(CodeInvalidRanges, "policyRanges",
			"policyRanges の合計長 (%d) は到達可能 level 数 (%d) と一致する必要があります", next-1, want)
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

// rangeForStage returns the range covering stage. The list is validated to tile
// the progression without gaps, so at most one range matches.
func rangeForStage(ranges []PolicyRange, stage int64) (PolicyRange, bool) {
	for _, r := range ranges {
		if stage >= r.Start && stage < r.End {
			return r, true
		}
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

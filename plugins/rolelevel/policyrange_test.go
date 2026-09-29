package rolelevel

import (
	"math"
	"strings"
	"testing"
)

func rangeCfg(ranges ...PolicyRange) Config {
	return Config{
		BaseLevel:       0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 4, Base: 100}},
		PolicyRanges:    ranges,
	}
}

// cfgWithLevelUps builds a config whose reachable stages are 1..levelUps+1, so an
// exact tiling of the policy ranges has to end at levelUps+2. curveBase is 1 rather
// than 100 so a 9e15 level-up count still fits the MaxExperience aggregate bound.
func cfgWithLevelUps(levelUps, curveBase int64, ranges ...PolicyRange) Config {
	return Config{
		BaseLevel:       0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: levelUps, Base: curveBase}},
		PolicyRanges:    ranges,
	}
}

// baseRanges tiles n reachable stages with n single-stage base ranges, i.e. an
// exact tiling of [1, n+1) that needs n rules.
func baseRanges(n int64) []PolicyRange {
	out := make([]PolicyRange, 0, n)
	for i := int64(1); i <= n; i++ {
		out = append(out, PolicyRange{Type: RangeBase, Start: i, End: i + 1})
	}
	return out
}

// **range は半開区間で、stage 1 から levelUps+1 までを
// [1, levelUps+2) として重複も欠落も無く敷き詰める。**
// 旧実装の inclusive boundary は level 境界で必ず重複していた。
func TestValidateRangesRequiresAnExactTiling(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ranges []PolicyRange
	}{
		{"空", nil},
		{"先頭が 2 から始まる", []PolicyRange{{Type: RangeBase, Start: 2, End: 6}}},
		{"1 を飛ばす", []PolicyRange{{Type: RangeBase, Start: 1, End: 2}, {Type: RangeBase, Start: 3, End: 6}}},
		{"重複", []PolicyRange{{Type: RangeBase, Start: 1, End: 3}, {Type: RangeBase, Start: 2, End: 6}}},
		{"途中が欠ける", []PolicyRange{{Type: RangeBase, Start: 1, End: 2}, {Type: RangeBase, Start: 4, End: 6}}},
		{"合計が足りない", []PolicyRange{{Type: RangeBase, Start: 1, End: 5}}},
		{"合計が多い", []PolicyRange{{Type: RangeBase, Start: 1, End: 7}}},
		{"空の range がある", []PolicyRange{{Type: RangeBase, Start: 1, End: 1}, {Type: RangeBase, Start: 1, End: 6}}},
		{"end < start", []PolicyRange{{Type: RangeBase, Start: 6, End: 1}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(tt.ranges...).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("受理しています: %+v", tt.ranges)
			}
			// **「落ちた」だけでは足りない。** 並びの不備の code (ranges) と、
			// 並びとは無関係に落ちる code (未知の key / 非数値への multiplier /
			// 値型) が混ざると、frontend の分岐と説明がずれる。全部同じ code で
			// 落ちることまで固定する。
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRanges {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRanges, err)
			}
		})
	}
}

func TestValidateRangesAcceptsOneCoveringBaseRange(t *testing.T) {
	if err := rangeCfg(PolicyRange{Type: RangeBase, Start: 1, End: 6}).Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
}

// **未知の native policy key は拒否する。** 拒否しないと、host 側が見ない key に
// contribution して「設定したのに効かない」状態になる。
func TestValidateRangesRejectsUnknownKey(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeConst, Start: 1, End: 6, Key: "noSuchPolicyKey", Value: true}).
		Validate(defaultCatalog)
	if err == nil {
		t.Fatal("未知の key を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeUnknownPolicyKey {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeUnknownPolicyKey, err)
	}
}

// **未知の native policy key は multiplier でも拒否する。** const と同じ code を
// 使うので、frontend は1本の分岐で説明できる。
func TestValidateRangesRejectsUnknownMultiplierKey(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
		Key: "noSuchPolicyKey", Base: 1, Additional: 1}).Validate(defaultCatalog)
	if err == nil {
		t.Fatal("未知の key に multiplier を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeUnknownPolicyKey {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeUnknownPolicyKey, err)
	}
	if ve.Field != "policyRanges[0].key" {
		t.Fatalf("field = %q, want policyRanges[0].key", ve.Field)
	}
}

// **enum ではない string にも multiplier は使えない。** 判定するのは kind だけなので
// enum の有無では結果が変わらない。**実際の native schema には enum 無し string が1つも無い**
// (string kind は chatAvailability だけで enum 付き) ので、catalog を直接組み立てて
// 同じ分岐を通す。defaultCatalog を書き換えないのは host drift gate を壊さないため。
func TestValidateRangesRejectsMultiplierOnNonEnumStringKey(t *testing.T) {
	cat := &Catalog{byKey: map[string]catalogEntry{
		"plainText": {Key: "plainText", Kind: KindString},
	}}
	err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
		Key: "plainText", Base: 1, Additional: 1}).Validate(cat)
	if err == nil {
		t.Fatal("enum 無し string に multiplier を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeMultiplierNotNumeric {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeMultiplierNotNumeric, err)
	}
}

// **未知の type は「範囲の並び」の検査を通った後に落とす。** type だけを先に検査すると
// 範囲の重複と type 不正の code が混ざるので、並びの不正を先に報告する。
func TestValidateRangesRejectsUnknownRangeType(t *testing.T) {
	for _, tt := range []struct {
		name  string
		rtype RangeType
	}{
		{"空文字", ""},
		{"typo", "multiplier2"},
		{"大文字小文字の違い", "Const"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(PolicyRange{Type: tt.rtype, Start: 1, End: 6}).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("type %q を受理しています", tt.rtype)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRanges {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRanges, err)
			}
			if ve.Field != "policyRanges[0].type" {
				t.Fatalf("field = %q, want policyRanges[0].type", ve.Field)
			}
		})
	}
}

// **boolean と enum には const だけ。** 乗算の意味が無い上に、host 側の集約も乗算を
// 受けない。
func TestValidateRangesRejectsMultiplierOnNonNumericKey(t *testing.T) {
	for _, tt := range []struct{ key, name string }{
		{"canPublicNote", "boolean"},
		{"chatAvailability", "enum"},
		{"uploadableFileTypes", "string set"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
				Key: tt.key, Base: 1, Additional: 1}).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("%s に multiplier を受理しています", tt.key)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeMultiplierNotNumeric {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeMultiplierNotNumeric, err)
			}
		})
	}
}

func TestValidateRangesRejectsMismatchedConst(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule PolicyRange
	}{
		{"boolean に文字列", PolicyRange{Type: RangeConst, Start: 1, End: 6, Key: "canPublicNote", Value: "true"}},
		{"数値に文字列", PolicyRange{Type: RangeConst, Start: 1, End: 6, Key: "noteEachClipsLimit", Value: "10"}},
		{"enum に未知値", PolicyRange{Type: RangeConst, Start: 1, End: 6, Key: "chatAvailability", Value: "nope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(tt.rule).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("受理しています: %+v", tt.rule)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRangeValue {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRangeValue, err)
			}
		})
	}
}

func TestValidateRangesAcceptsFractionalNativeNumber(t *testing.T) {
	err := rangeCfg(PolicyRange{Type: RangeConst, Start: 1, End: 6,
		Key: "rateLimitFactor", Value: 1.5}).Validate(defaultCatalog)
	if err != nil {
		t.Fatal(err)
	}
}

// **multiplier は全 stage で native の数値範囲に収まらなければならない。** 端点2個だけ
// 見れば十分 (range 内で n の1次式なので単調で、中間にだけ範囲外になる値は無い)。
//
// **有限値のまま int 範囲外になる場合**。非有限とは別の条文で落とすので、両方の
// テストが条文まで見る (片方だけ見ると、もう片方の分岐が素通りする)。
func TestValidateRangesRejectsOutOfRangeMultiplier(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule PolicyRange
	}{
		{"最終端点が範囲外", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: 1, Additional: 1e19}},
		{"第1端点は範囲内で最終端点だけ範囲外", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: 9e18, Additional: 1e18}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(tt.rule).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("int 範囲外の multiplier を受理しています: %+v", tt.rule)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRangeValue {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRangeValue, err)
			}
			if !strings.Contains(ve.Err.Error(), "int の範囲外です") {
				t.Fatalf("条文 = %q, want int 範囲外の条文 (%v)", ve.Err, err)
			}
		})
	}
}

// multiplier の計算結果が非有限値になる設定は保存時に落とす。
//
// **「有限値だが int 範囲外」とは別の条文で落とす**ので、両方のテストが条文まで見る。
// **base: 1e308 のような有限値だけでは非有限の分岐に到達しない** (先に int 範囲で落ち
// るので、非有限分岐が未証明のまま通ってしまう)。だから下の case は実際に +Inf / NaN を
// 計算結果として出す:
//
//   - 最後の端点で 4 * 1e308 が +Inf に overflow する (JSON からも到達可能)
//   - 0 * Inf が NaN になる
//   - base が既に +Inf / NaN である (Go 側の値としてのみ到達可能)
func TestValidateRangesRejectsNonFiniteMultiplier(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule PolicyRange
	}{
		{"最後の端点で +Inf に overflow", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: 0, Additional: 1e308}},
		{"0 * Inf が NaN", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: 0, Additional: math.Inf(1)}},
		{"base が +Inf", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: math.Inf(1)}},
		{"base が NaN", PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
			Key: "noteEachClipsLimit", Base: math.NaN()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := rangeCfg(tt.rule).Validate(defaultCatalog)
			if err == nil {
				t.Fatalf("非有限の multiplier を受理しています: %+v", tt.rule)
			}
			ve, ok := err.(*ValidationError)
			if !ok || ve.Code != CodeInvalidRangeValue {
				t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRangeValue, err)
			}
			// int 範囲の条文で通っていないことまで見て、「有限値だが範囲外」で
			// 済ませていないことを確認する。
			if !strings.Contains(ve.Err.Error(), "有限値ではありません") {
				t.Fatalf("条文 = %q, want 非有限の条文 (%v)", ve.Err, err)
			}
		})
	}
}

// **MaxPolicyRanges 個は受理する。** 上限は「これ以上受理しない」であって
// 「上限の少し手前で弾く」ではない。
func TestValidateRangesAcceptsMaxPolicyRanges(t *testing.T) {
	ranges := baseRanges(MaxPolicyRanges)
	if int64(len(ranges)) != MaxPolicyRanges {
		t.Fatalf("fixture = %d ranges, want %d", len(ranges), MaxPolicyRanges)
	}
	// levelUps = MaxPolicyRanges-1 なので到達可能 stage は 1..MaxPolicyRanges。
	if err := cfgWithLevelUps(MaxPolicyRanges-1, 1, ranges...).Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
}

// **1個超過で落とす。** 敷き詰めは正しいので、MaxPolicyRanges の超過だけが理由になる
// (敷き詰め违反の code が先に返らないことも固定する)。
func TestValidateRangesRejectsMoreThanMaxPolicyRanges(t *testing.T) {
	ranges := baseRanges(MaxPolicyRanges + 1)
	err := cfgWithLevelUps(MaxPolicyRanges, 1, ranges...).Validate(defaultCatalog)
	if err == nil {
		t.Fatalf("%d 個を受理しています (want reject)", len(ranges))
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeInvalidRanges {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidRanges, err)
	}
	if !strings.Contains(ve.Err.Error(), "256") {
		t.Fatalf("条文 = %q, want 上限 256 の言及 (%v)", ve.Err, err)
	}
}

// **validation は stage を走査しない。** levelUps が 9e15 程度でも保存が終わること。
// 走査する実装はここで止まる (go test の 10分 timeout に達する) ので、正しい
// 敷き詰めと正しい端点だけを持つ設定が通ることを固定する。
//
// **巨大区間を base ではなく numeric な multiplier に載せる。** base は端点計算が
// 無いので、base が巨大だと「stage を走査していない」ことを示せない。numeric の
// multiplier なら端点2個の値を計算しない限り判定が終わらないので、巨大区間でも
// 素直に終わる = 端点だけを見た谓之証拠になる。Additional を 0 にしてあるので
// 両端点とも 5 (有限かつ host の数値範囲内) で、値側の検査にも掛からない。
func TestValidateRangesDoesNotIterateStages(t *testing.T) {
	levelUps := MaxExperience - 2
	// 到達可能 stage は 1..levelUps+1、敷き詰める区間は [1, levelUps+2) =
	// [1, MaxExperience)。**1個の範囲だけで全期間を受ける**。
	ranges := []PolicyRange{{
		Type: RangeMultiplier, Start: 1, End: MaxExperience,
		Key: "noteEachClipsLimit", Base: 5, Additional: 0,
	}}
	if err := cfgWithLevelUps(levelUps, 1, ranges...).Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	// 最終端点でも、offset が 9e15 でも値が 5 のままであることを併せて見る。
	got, err := rangeValue(ranges[0], MaxExperience-1)
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(5) {
		t.Fatalf("最終端点 = %v (%T), want 5", got, got)
	}
}

// **multiplier の計算結果は丸めない。** host 側も float64 のまま保持するので、
// rangeValue は端数を持った float64 を返す。int に潰されると rateLimitFactor のような
// 小数に意味がある key が壊れる。
func TestRangeValueKeepsFractionalMultiplier(t *testing.T) {
	rule := PolicyRange{Type: RangeMultiplier, Start: 1, End: 6,
		Key: "rateLimitFactor", Base: 0.25, Additional: 0.5}
	if err := rangeCfg(rule).Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		stage int64
		want  float64
	}{
		{1, 0.25}, {2, 0.75}, {3, 1.25}, {5, 2.25},
	} {
		got, err := rangeValue(rule, tt.stage)
		if err != nil {
			t.Fatalf("stage %d: %v", tt.stage, err)
		}
		f, isFloat := got.(float64)
		if !isFloat {
			t.Fatalf("stage %d = %#v (%T), want float64", tt.stage, got, got)
		}
		if f != tt.want {
			t.Fatalf("stage %d = %v, want %v", tt.stage, f, tt.want)
		}
	}
}

// **multiplier offset は range 内0始まりで、baseLevel の値に依存しない。**
func TestRangeValueMultiplierIgnoresBaseLevel(t *testing.T) {
	ranges := []PolicyRange{
		{Type: RangeBase, Start: 1, End: 2},
		{Type: RangeMultiplier, Start: 2, End: 6, Key: "noteEachClipsLimit", Base: 10, Additional: 5},
	}
	cfg := Config{BaseLevel: -10,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 4, Base: 100}},
		PolicyRanges:    ranges}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		stage int64
		want  float64
	}{
		{2, 10}, {3, 15}, {4, 20}, {5, 25},
	} {
		got, err := rangeValue(ranges[1], tt.stage)
		if err != nil {
			t.Fatalf("stage %d: %v", tt.stage, err)
		}
		if got != tt.want {
			t.Fatalf("stage %d = %v (%T), want %v", tt.stage, got, got, tt.want)
		}
	}
	// baseLevel を変えても stage 1 は同じで、offset は baseLevel を見ていない。
	other := cfg
	other.BaseLevel = 1000
	exp, err := other.Experience(0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.ProgressionStage != 1 {
		t.Fatalf("stage = %d, want 1", exp.ProgressionStage)
	}
	if got, _ := rangeValue(ranges[1], 3); got != float64(15) {
		t.Fatalf("baseLevel を変えても offset がずれた: %v", got)
	}
}

// **base range は nil を返す。** nil は「置換しない = native policy をそのまま
// 使う」を意味するので、resolver が native の静的 contribution を落とさない。
func TestRangeValueBaseIsNil(t *testing.T) {
	got, err := rangeValue(PolicyRange{Type: RangeBase, Start: 1, End: 5}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("base range が値を返しました: %#v", got)
	}
}

// **stage は「受け取れる」だけでなく「正しい range を返す」。** 単に範囲内かどうかを
// 見るだけだと、最初の1個を返す実装でも通ってしまう。
//
// **重複があるときの一意性はここでは見ていない。** それは validateRanges が
// CodeInvalidRanges で落とすので (TestValidateRangesRequiresAnExactTiling の「重複」)、
// このテストは「到達可能 stage はすべて1つで、外には range がない」だけを固定する。
func TestRangeForStageCoversReachableStagesOnly(t *testing.T) {
	ranges := []PolicyRange{
		{Type: RangeConst, Start: 1, End: 3, Key: "canPublicNote", Value: false},
		{Type: RangeBase, Start: 3, End: 4},
		{Type: RangeConst, Start: 4, End: 6, Key: "canPublicNote", Value: true},
	}
	for stage, want := range map[int64]int{1: 0, 2: 0, 3: 1, 4: 2, 5: 2} {
		got, ok := rangeForStage(ranges, stage)
		if !ok {
			t.Fatalf("stage %d を受ける range がありません", stage)
		}
		if got != ranges[want] {
			t.Fatalf("stage %d = %+v, want ranges[%d] = %+v", stage, got, want, ranges[want])
		}
	}
	if _, ok := rangeForStage(ranges, 6); ok {
		t.Fatal("stage 6 を受ける range がある")
	}
	if _, ok := rangeForStage(ranges, 0); ok {
		t.Fatal("stage 0 を受ける range がある")
	}
}

package rolelevel

import (
	"math"
	"testing"
)

// **既定値は全作成経路で統一する。** baseLevel 1 / const 100 XP × 99 level-ups /
// effective level 1..100。
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BaseLevel != 1 {
		t.Fatalf("baseLevel = %d, want 1", cfg.BaseLevel)
	}
	if cfg.TotalLevelUps() != 99 {
		t.Fatalf("level-ups = %d, want 99", cfg.TotalLevelUps())
	}
	if cfg.MinLevel() != 1 || cfg.MaxLevel() != 100 {
		t.Fatalf("level range = %d..%d, want 1..100", cfg.MinLevel(), cfg.MaxLevel())
	}
	if len(cfg.PolicyRanges) != 1 || cfg.PolicyRanges[0].Start != 1 || cfg.PolicyRanges[0].End != 101 {
		t.Fatalf("default policy range = %+v, want [1, 101)", cfg.PolicyRanges)
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatalf("既定の設定が validation を通りません: %v", err)
	}

	exp, err := cfg.Experience(0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 1 || exp.ProgressionStage != 1 {
		t.Fatalf("XP 0 のとき = %+v, want currentLevel 1 / stage 1", exp)
	}
	// 99 × 100 = 9900 で最大 level に届く。
	exp, err = cfg.Experience(9900)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 100 || exp.NextLevelExp != nil || exp.CurrentLevelExp != 0 {
		t.Fatalf("最大 level = %+v, want currentLevel 100 / nextLevelExp nil / exp 0", exp)
	}
	// 最大 level 超過の余剰は currentLevelExp に持ち越す。
	exp, err = cfg.Experience(9950)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 100 || exp.CurrentLevelExp != 50 || exp.ProgressionStage != 100 {
		t.Fatalf("最大 level 超過 = %+v", exp)
	}
	if exp.NextLevelExp != nil {
		t.Fatalf("nextLevelExp = %d, want nil", *exp.NextLevelExp)
	}
}

// **baseLevel は負数・0・正数を許可する。** stage は常に 1 始まり。
func TestNegativeAndZeroBaseLevel(t *testing.T) {
	for _, tt := range []struct {
		name      string
		baseLevel int64
	}{
		{"負数", -10},
		{"0", 0},
		{"正数", 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BaseLevel = tt.baseLevel
			if err := cfg.Validate(defaultCatalog); err != nil {
				t.Fatal(err)
			}
			exp, err := cfg.Experience(0)
			if err != nil {
				t.Fatal(err)
			}
			if exp.CurrentLevel != tt.baseLevel {
				t.Fatalf("XP 0 = %+v, want currentLevel %d", exp, tt.baseLevel)
			}
			if exp.MaxLevel != tt.baseLevel+99 {
				t.Fatalf("maxLevel = %d, want %d", exp.MaxLevel, tt.baseLevel+99)
			}
			if exp.ProgressionStage != 1 {
				t.Fatalf("stage = %d, want 1 (baseLevel に依らない)", exp.ProgressionStage)
			}
			exp, err = cfg.Experience(9900)
			if err != nil {
				t.Fatal(err)
			}
			if exp.CurrentLevel != tt.baseLevel+99 || exp.ProgressionStage != 100 {
				t.Fatalf("到達 = %+v, want currentLevel %d / stage 100", exp, tt.baseLevel+99)
			}
		})
	}
}

// **明示的に空の curve は baseLevel に固定する。** nextLevelExp は無い。
func TestEmptyCurvePinsTheLevel(t *testing.T) {
	cfg := Config{BaseLevel: 7, ExperienceCurve: []Curve{},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 2}}}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	for _, total := range []int64{0, 1, 1000000} {
		exp, err := cfg.Experience(total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != 7 || exp.NextLevelExp != nil || exp.ProgressionStage != 1 {
			t.Fatalf("total %d = %+v, want currentLevel 7 / nextLevelExp nil / stage 1", total, exp)
		}
		if exp.CurrentLevelExp != total {
			t.Fatalf("total %d の余剰 = %d, want %d", total, exp.CurrentLevelExp, total)
		}
	}
}

func TestCurveCostsAreFractional(t *testing.T) {
	t.Run("const", func(t *testing.T) {
		c := Curve{Type: CurveConst, LevelUps: 3, Base: 100}
		for n, want := range []float64{100, 100, 100} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(3); err != nil || got != 300 {
			t.Fatalf("cum(3) = %v %v, want 300", got, err)
		}
	})
	t.Run("linear", func(t *testing.T) {
		c := Curve{Type: CurveLinear, LevelUps: 4, Base: 100, Additional: 50}
		for n, want := range []float64{100, 150, 200, 250} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(4); err != nil || got != 700 {
			t.Fatalf("cum(4) = %v %v, want 700", got, err)
		}
	})
	// **1 level ごとに切り上げない。** コストは実数のまま積算する。
	t.Run("exponential keeps the fraction", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 4, Base: 1, Additional: 1, Exponential: 1.5}
		for n, want := range []float64{2, 2.5, 3.25, 4.375} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		// 2 + 2.5 + 3.25 + 4.375 = 12.125
		if got, err := c.SegmentCumExp(4); err != nil || math.Abs(got-12.125) > 1e-9 {
			t.Fatalf("cum(4) = %v %v, want 12.125", got, err)
		}
	})
	// **e == 1 は専用処理する。** (e^k - 1)/(e - 1) は 0/0 になる。
	t.Run("exponential with ratio 1", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 3, Base: 10, Additional: 5, Exponential: 1}
		for n, want := range []float64{15, 15, 15} {
			got, err := c.LevelUpCost(int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("cost(%d) = %v, want %v", n, got, want)
			}
		}
		if got, err := c.SegmentCumExp(3); err != nil || got != 45 {
			t.Fatalf("cum(3) = %v %v, want 45", got, err)
		}
	})
	t.Run("exponential with ratio 1 avoids cancellation", func(t *testing.T) {
		c := Curve{
			Type: CurveExponential, LevelUps: 5,
			Base: MaxExperience, Additional: -MaxExperience + 1, Exponential: 1,
		}
		if got, err := c.SegmentCumExp(5); err != nil || got != 5 {
			t.Fatalf("cum(5) = %v %v, want 5", got, err)
		}
	})
	// **閉形式は整数の等比数列で検算できる。** e = 2 の和は 2^k - 1。
	t.Run("exponential closed form", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 10, Base: 0, Additional: 1, Exponential: 2}
		got, err := c.SegmentCumExp(10)
		if err != nil {
			t.Fatal(err)
		}
		if got != 1023 {
			t.Fatalf("cum(10) = %v, want 1023", got)
		}
	})
	// **1 に近い ratio でも桁が飛ばない。** 素の `(Pow(e,k)-1)/(e-1)` は
	// e = 1+1e-12, k = 100 で 4 桁以上落ちる。Log1p / Expm1 の等価形なら
	// 100.0000000049 がそのまま出る。
	t.Run("exponential near 1 stays stable", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 100, Base: 0, Additional: 1,
			Exponential: 1.000000000001}
		if err := c.ValidateCosts(); err != nil {
			t.Fatalf("検証で弾かれました: %v", err)
		}
		got, err := c.SegmentCumExp(100)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-100.0000000049) > 1e-6 {
			t.Fatalf("cum(100) = %v, want 100.0000000049 付近 (誤差 1e-6 以内)", got)
		}
	})
	t.Run("exponential next to 1 avoids cancellation", func(t *testing.T) {
		c := Curve{
			Type: CurveExponential, LevelUps: 2,
			Base: MaxExperience, Additional: -MaxExperience + 1,
			Exponential: math.Nextafter(1, 0),
		}
		// Exact value is 3-2^-52, which rounds to 3 at this magnitude.
		want := float64(3)
		if got, err := c.SegmentCumExp(2); err != nil || got != want {
			t.Fatalf("cum(2) = %.17g %v, want %.17g", got, err, want)
		}
	})
	t.Run("exponential with zero additional is constant", func(t *testing.T) {
		c := Curve{Type: CurveExponential, LevelUps: 3, Base: 1, Exponential: math.MaxFloat64}
		if err := c.ValidateCosts(); err != nil {
			t.Fatal(err)
		}
		if got, err := c.SegmentCumExp(3); err != nil || got != 3 {
			t.Fatalf("cum(3) = %v %v, want 3", got, err)
		}
	})
}

// **整数XP は小数しきい値に ceil で到達し、currentLevelExp は floor(整数XP - しきい値)。**
// 次の level までは ceil(次のしきい値) - 整数XP。
func TestFractionsRoundCorrectly(t *testing.T) {
	cfg := Config{
		BaseLevel:       0,
		ExperienceCurve: []Curve{{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: 1.5}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 4}},
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	// cost = 2, 2.5 → 累積 2, 4.5
	for _, tt := range []struct {
		total       int64
		wantLevel   int64
		wantCurrent int64
		wantNext    int64
	}{
		{0, 0, 0, 2},
		{1, 0, 1, 1},
		{2, 1, 0, 3},
		{3, 1, 1, 2},
		{4, 1, 2, 1},
		// 最大 level 到達。しきい値 4.5 に対して余剰は floor(5 - 4.5) = 0。
		{5, 2, 0, -1},
	} {
		exp, err := cfg.Experience(tt.total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != tt.wantLevel {
			t.Fatalf("total %d: currentLevel = %d, want %d", tt.total, exp.CurrentLevel, tt.wantLevel)
		}
		if exp.CurrentLevelExp != tt.wantCurrent {
			t.Fatalf("total %d: currentLevelExp = %d, want %d", tt.total, exp.CurrentLevelExp, tt.wantCurrent)
		}
		if tt.wantNext < 0 {
			if exp.NextLevelExp != nil {
				t.Fatalf("total %d: nextLevelExp = %d, want nil", tt.total, *exp.NextLevelExp)
			}
			continue
		}
		if exp.NextLevelExp == nil {
			t.Fatalf("total %d: nextLevelExp がありません", tt.total)
		}
		if *exp.NextLevelExp != tt.wantNext {
			t.Fatalf("total %d: nextLevelExp = %d, want %d", tt.total, *exp.NextLevelExp, tt.wantNext)
		}
	}
}

// **rule をまたぐ offset は float64 で持ち越す。** 1つ目が 2.5 まで伸びたら、
// 2つ目は 0 ではなく 2.5 から積算する (2.5 + 3.25 = 5.75)。
func TestOffsetCarriesAcrossRules(t *testing.T) {
	cfg := Config{
		BaseLevel: 0,
		ExperienceCurve: []Curve{
			// cost = 1, 1.5 → 累積 2.5
			{Type: CurveExponential, LevelUps: 2, Base: 0, Additional: 1, Exponential: 1.5},
			// cost = 1, 2.25 → この rule の寄与 3.25
			{Type: CurveExponential, LevelUps: 2, Base: 0, Additional: 1, Exponential: 2.25},
		},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 6}},
	}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatal(err)
	}
	total, err := cfg.TotalExperienceThreshold()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(total-5.75) > 1e-9 {
		t.Fatalf("累積しきい値 = %v, want 5.75 (2.5 + 3.25)", total)
	}
	// 累積は 1 / 2.5 / 3.5 / 5.75
	for _, tt := range []struct {
		total       int64
		wantLevel   int64
		wantCurrent int64
		wantNext    int64
	}{
		{0, 0, 0, 1},
		{1, 1, 0, 2},
		{2, 1, 1, 1},
		// 2 つ目の rule の先頭。しきい値 2.5 に対して余剰は floor(3 - 2.5) = 0。
		{3, 2, 0, 1},
		// 3 つ目の level の途中。しきい値 3.5 に対して floor(5 - 3.5) = 1。
		{5, 3, 1, 1},
		{6, 4, 0, -1},
	} {
		exp, err := cfg.Experience(tt.total)
		if err != nil {
			t.Fatal(err)
		}
		if exp.CurrentLevel != tt.wantLevel {
			t.Fatalf("total %d: currentLevel = %d, want %d", tt.total, exp.CurrentLevel, tt.wantLevel)
		}
		if exp.CurrentLevelExp != tt.wantCurrent {
			t.Fatalf("total %d: currentLevelExp = %d, want %d", tt.total, exp.CurrentLevelExp, tt.wantCurrent)
		}
		if tt.wantNext < 0 {
			if exp.NextLevelExp != nil {
				t.Fatalf("total %d: nextLevelExp = %d, want nil", tt.total, *exp.NextLevelExp)
			}
			continue
		}
		if exp.NextLevelExp == nil || *exp.NextLevelExp != tt.wantNext {
			t.Fatalf("total %d: nextLevelExp = %v, want %d", tt.total, exp.NextLevelExp, tt.wantNext)
		}
	}
}

// **rule ごとに walk し、その中で binary search する。** 大きい level 数でも
// O(S log L) で答えが出る。
func TestLargeLevelUpCountUsesClosedForm(t *testing.T) {
	small := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 1, Base: 10}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 3}}}
	large := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 100_000_000, Base: 10}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 100_000_002}}}

	gotSmall, err := small.Experience(9_999_999)
	if err != nil {
		t.Fatal(err)
	}
	if gotSmall.CurrentLevel != 1 {
		t.Fatalf("1 段の curve = %+v, want currentLevel 1 (10 XP で最大)", gotSmall)
	}
	gotLarge, err := large.Experience(9_999_999)
	if err != nil {
		t.Fatal(err)
	}
	if gotLarge.CurrentLevel != 999_999 || gotLarge.ProgressionStage != 1_000_000 {
		t.Fatalf("1 億段の curve = %+v, want currentLevel 999999 / stage 1000000", gotLarge)
	}
	// e == 1 の巨大な rule も誤差なく扱える (3 XP × 5000 万 = 1.5 億)。
	geo := Curve{Type: CurveExponential, LevelUps: 50_000_000, Base: 1, Additional: 2, Exponential: 1}
	if got, err := geo.SegmentCumExp(50_000_000); err != nil || got != 150_000_000 {
		t.Fatalf("exponential cum = %v %v, want 150000000", got, err)
	}
}

// **保存時に全部落とす。** NaN / Infinity / overflow / コスト 0 以下を
// 「policy 解決のとき初めて起きる」形に持たない。
func TestCurveValidationRejectsUnsafeCurves(t *testing.T) {
	for _, tt := range []struct {
		name string
		seg  Curve
		span int64
	}{
		{"未知の type", Curve{Type: "quadratic", LevelUps: 1, Base: 1}, 2},
		{"levelUps 0", Curve{Type: CurveConst, LevelUps: 0, Base: 1}, 1},
		{"levelUps 負", Curve{Type: CurveConst, LevelUps: -1, Base: 1}, 1},
		{"コストが 0", Curve{Type: CurveConst, LevelUps: 1, Base: 0}, 2},
		{"コストが負", Curve{Type: CurveConst, LevelUps: 1, Base: -5}, 2},
		{"コストが 0 を横切る", Curve{Type: CurveLinear, LevelUps: 3, Base: 1, Additional: -1}, 4},
		{"累積が上限超", Curve{Type: CurveConst, LevelUps: 1_000_000, Base: MaxExperience}, 1_000_001},
		{"exponential が 0", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: 0}, 3},
		{"exponential が負", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: -2}, 3},
		{"exponential が NaN", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: math.NaN()}, 3},
		{"exponential が Inf", Curve{Type: CurveExponential, LevelUps: 2, Base: 1, Additional: 1, Exponential: math.Inf(1)}, 3},
		{"exponential が有限値を超える", Curve{Type: CurveExponential, LevelUps: 8, Base: 1, Additional: 1, Exponential: 1e300}, 9},
		{"exponential のコストが 0 になる", Curve{Type: CurveExponential, LevelUps: 4, Base: 10, Additional: -10, Exponential: 2}, 5},
		{"base が safe integer 超", Curve{Type: CurveConst, LevelUps: 2, Base: MaxExperience, Additional: 0}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{BaseLevel: 0, ExperienceCurve: []Curve{tt.seg},
				PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: tt.span}}}
			err := cfg.Validate(defaultCatalog)
			if err == nil {
				t.Fatal("拒否していません")
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("ValidationError ではありません: %v", err)
			}
			if ve.Code != CodeInvalidCurve {
				t.Fatalf("code = %q, want %q (%v)", ve.Code, CodeInvalidCurve, err)
			}
		})
	}
}

// **負の additional は許す。** コストが 0 より大きければ通る。
func TestCurveValidationAllowsNegativeAdditional(t *testing.T) {
	cfg := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{{Type: CurveLinear, LevelUps: 4, Base: 100, Additional: -20}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 6}}}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatalf("負の additional を拒否しました: %v", err)
	}
	exp, err := cfg.Experience(100 + 80 + 60 + 40)
	if err != nil {
		t.Fatal(err)
	}
	if exp.CurrentLevel != 4 || exp.CurrentLevelExp != 0 {
		t.Fatalf("= %+v, want currentLevel 4", exp)
	}
}

// **rule をまたいでも累積が上限内に収まること。** 各 rule ごとではなく合計で見る。
func TestCurveValidationRejectsGrandTotalOverflow(t *testing.T) {
	cfg := Config{BaseLevel: 0,
		ExperienceCurve: []Curve{
			{Type: CurveConst, LevelUps: 5, Base: 1_000_000_000_000_000},
			{Type: CurveConst, LevelUps: 5, Base: 1_000_000_000_000_000},
		},
		PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: 12}}}
	err := cfg.Validate(defaultCatalog)
	if err == nil {
		t.Fatal("rule をまたいで上限を超える累積を受理しています")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Code != CodeInvalidCurve {
		t.Fatalf("code = %+v, want %s (%v)", err, CodeInvalidCurve, err)
	}
}

func TestCurveValidationRejectsUnsafeLevelResults(t *testing.T) {
	for _, tt := range []struct {
		name      string
		cfg       Config
		wantField string
	}{
		{
			name: "segment levelUps が safe integer を超える",
			cfg: Config{ExperienceCurve: []Curve{{
				Type: CurveConst, LevelUps: MaxExperience + 1, Base: 1,
			}}},
			wantField: "experienceCurve[0].levelUps",
		},
		{
			name: "progressionStage が safe integer を超える",
			cfg: Config{ExperienceCurve: []Curve{{
				Type: CurveConst, LevelUps: MaxExperience, Base: 1,
			}}},
		},
		{
			name: "policy range end が safe integer を超える",
			cfg: Config{ExperienceCurve: []Curve{{
				Type: CurveConst, LevelUps: MaxExperience - 1, Base: 1,
			}}},
		},
		{
			name: "maxLevel が safe integer を超える",
			cfg: Config{BaseLevel: MaxExperience, ExperienceCurve: []Curve{{
				Type: CurveConst, LevelUps: 1, Base: 1,
			}}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate(defaultCatalog)
			if err == nil {
				t.Fatal("unsafe な level 結果を受理しています")
			}
			if tt.wantField != "" {
				ve, ok := err.(*ValidationError)
				if !ok || ve.Field != tt.wantField {
					t.Fatalf("field = %+v, want %q (%v)", ve, tt.wantField, err)
				}
			}
		})
	}
}

func TestCurveValidationAcceptsLargestSafeAggregate(t *testing.T) {
	cfg := Config{ExperienceCurve: []Curve{{
		Type: CurveConst, LevelUps: MaxExperience - 2, Base: 1,
	}}, PolicyRanges: []PolicyRange{{Type: RangeBase, Start: 1, End: MaxExperience}}}
	if err := cfg.Validate(defaultCatalog); err != nil {
		t.Fatalf("safe な level-up count 上限を拒否しました: %v", err)
	}
}

func TestExperienceRejectsUnsafeTotal(t *testing.T) {
	for _, total := range []int64{-1, MaxExperience + 1} {
		if _, err := DefaultConfig().Experience(total); err == nil {
			t.Fatalf("範囲外の総経験値 %d を受理しています", total)
		}
	}
}

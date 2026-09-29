package rolelevel

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// MaxExperience is the largest experience value the plugin stores or returns.
//
// **JSON number / Go safe integer の上限**。frontend へ返すと number になり、2^53 を
// 超えると 1 しか変わらない精度になるため。string や full uint64 にはしない。
const MaxExperience int64 = 1<<53 - 1

// MaxExperienceFloat is MaxExperience as a float64, for threshold comparisons.
const MaxExperienceFloat = 9007199254740991.0

// CurveType names one XP curve segment shape.
type CurveType string

const (
	// CurveConst costs Base for every level-up.
	CurveConst CurveType = "const"
	// CurveLinear costs Base + Additional*n.
	CurveLinear CurveType = "linear"
	// CurveExponential costs Base + Additional*Exponential**n.
	CurveExponential CurveType = "exponential"
)

// Curve is one XP curve segment. LevelUps is how many level-ups it covers and the
// cost of its n-th level-up (0-based, n restarts at every rule) is:
//
//	const:       Base
//	linear:      Base + Additional*n
//	exponential: Base + Additional*Exponential**n
//
// **コストは実数のまま積算する。1 level ごとに切り上げない。** 1.5 刻みの curve を
// 設定すると level の切り替わりが 2.5 / 5.75 のように半端になる。保存される XP は整数で
// あり、しきい値には ceil で到達し (spec)、currentLevelExp は
// floor(整数XP - しきい値) で表示する (TestFractionsRoundCorrectly が固定する)。
//
// Base と Additional は整数で受ける。JSON の数として往復しても 2^53 を超える精度を
// 保持できないため、整数であることが検証の前提になる。
type Curve struct {
	Type        CurveType `json:"type"`
	LevelUps    int64     `json:"levelUps"`
	Base        int64     `json:"base"`
	Additional  int64     `json:"additional"`
	Exponential float64   `json:"exponential,omitempty"`
}

// Config is one role's level configuration, the plugin-owned counterpart of the
// native role row.
type Config struct {
	RoleID          string        `json:"roleId"`
	BaseLevel       int64         `json:"baseLevel"`
	ExperienceCurve []Curve       `json:"experienceCurve"`
	PolicyRanges    []PolicyRange `json:"policyRanges"`
	Revision        int64         `json:"revision"`
	UpdatedBy       string        `json:"updatedBy"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

// DefaultConfig returns the configuration every creation path starts from.
func DefaultConfig() Config {
	return Config{
		BaseLevel:       1,
		ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 99, Base: 100}},
		PolicyRanges:    []PolicyRange{{Type: RangeBase, Start: 1, End: 101}},
	}
}

// TotalLevelUps is how many level-ups the segment contains.
func (c Curve) TotalLevelUps() int64 { return c.LevelUps }

// TotalLevelUps is how many level-ups the curve contains. An empty curve is 0,
// which pins the level at baseLevel.
func (c Config) TotalLevelUps() int64 {
	var total int64
	for _, seg := range c.ExperienceCurve {
		total += seg.LevelUps
	}
	return total
}

// MinLevel is the level an assignment with 0 experience has.
func (c Config) MinLevel() int64 { return c.BaseLevel }

// MaxLevel is the level the last level-up reaches.
func (c Config) MaxLevel() int64 { return c.BaseLevel + c.TotalLevelUps() }

// LevelUpCost returns the experience the n-th level-up of the segment costs.
//
// **切り上げない。** 実数のまま返す。n は rule 内 0 始まり。
func (c Curve) LevelUpCost(n int64) (float64, error) {
	var cost float64
	switch c.Type {
	case CurveConst:
		cost = float64(c.Base)
	case CurveLinear:
		cost = float64(c.Base) + float64(c.Additional)*float64(n)
	case CurveExponential:
		if c.Additional == 0 {
			cost = float64(c.Base)
		} else {
			cost = float64(c.Base) + float64(c.Additional)*math.Pow(c.Exponential, float64(n))
		}
	default:
		return 0, fmt.Errorf("rolelevel: curve type %q が不正です", c.Type)
	}
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, fmt.Errorf("rolelevel: %s segment の cost(%d) が有限値になりません", c.Type, n)
	}
	return cost, nil
}

// SegmentCumExp returns the fractional experience threshold for completing k
// level-ups of the segment. k must be 0..LevelUps.
//
// **3型とも閉形式**なので、level 数に比例する loop をしない:
//
//	const:   k*Base
//	linear:  k*Base + (k*(k-1)/2)*Additional
//	exponential: k*Base + Additional * (e**k - 1)/(e - 1)
//
// 指数の和は geoSum に閉じ込めてあるので、e == 1 と 1 に近い e の扱いが 1 箇所に
// 集まる。
func (c Curve) SegmentCumExp(k int64) (float64, error) {
	if k < 0 || k > c.LevelUps {
		return 0, fmt.Errorf("rolelevel: k=%d は segment の範囲 (0..%d) 外です", k, c.LevelUps)
	}
	kf := float64(k)
	var total float64
	switch c.Type {
	case CurveConst:
		total = kf * float64(c.Base)
	case CurveLinear:
		pairs := kf * (kf - 1) / 2
		total = kf*float64(c.Base) + pairs*float64(c.Additional)
	case CurveExponential:
		if c.Additional == 0 {
			total = kf * float64(c.Base)
		} else {
			// Split sum(e^n) into k + sum(e^n-1). This adds Base and
			// Additional before multiplying and keeps the small geometric delta
			// separate, avoiding cancellation when e is equal or close to 1.
			total = kf*(float64(c.Base)+float64(c.Additional)) +
				float64(c.Additional)*geoSumDelta(c.Exponential, kf)
		}
	default:
		return 0, fmt.Errorf("rolelevel: curve type %q が不正です", c.Type)
	}
	if math.IsNaN(total) || math.IsInf(total, 0) {
		return 0, fmt.Errorf("rolelevel: %s segment の累積 (k=%d) が有限値になりません", c.Type, k)
	}
	return total, nil
}

// geoSumDelta returns sum(e**n - 1) for n in [0, k). It is the difference
// between the geometric sum and k.
//
// **e == 1 は 0**。`k*(e-1)` が小さい領域では二項展開の tail を固定上限で
// 足す。商を先に丸めてから k を引くと、e が 1 に隣接する場合に delta 全体が
// 消えるため。展開項数は level-up count に比例せず、最大64回で打ち切る。
func geoSumDelta(e, k float64) float64 {
	d := e - 1
	if d == 0 || k <= 1 {
		return 0
	}
	if math.Abs(k*d) <= 0.5 {
		term := (k * (k - 1) / 2) * d
		total := term
		for j := 2.0; j < 64 && j < k; j++ {
			term *= ((k - j) / (j + 1)) * d
			total += term
			if math.Abs(term) <= math.Abs(total)*1e-16 {
				break
			}
		}
		return total
	}
	return math.Expm1(k*math.Log1p(d))/d - k
}

// completedLevelUps returns how many level-ups of the segment the given budget pays
// for. It binary searches the closed-form cumulative, so the cost does not depend
// on how deep the segment is.
//
// budget is the *remaining* budget after the previous rules, so a fractional offset
// carries across rule boundaries.
func (c Curve) completedLevelUps(budget float64) (int64, error) {
	if budget <= 0 {
		return 0, nil
	}
	lo, hi := int64(0), c.LevelUps
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		sum, err := c.SegmentCumExp(mid)
		if err != nil {
			return 0, err
		}
		if sum <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, nil
}

// Experience is the response shape for one role and one experience total.
type Experience struct {
	CurrentLevel    int64 `json:"currentLevel"`
	CurrentLevelExp int64 `json:"currentLevelExp"`
	// NextLevelExp is null at the maximum level or when there is no curve.
	NextLevelExp     *int64 `json:"nextLevelExp"`
	TotalExp         int64  `json:"totalExp"`
	MinLevel         int64  `json:"minLevel"`
	MaxLevel         int64  `json:"maxLevel"`
	ProgressionStage int64  `json:"progressionStage"`
}

// TotalExperienceThreshold returns the fractional experience needed to reach the
// maximum level. It is the sum of every rule's cumulative, so a fractional offset
// from one rule carries into the next (2.5 + 3.25 = 5.75).
func (c Config) TotalExperienceThreshold() (float64, error) {
	var offset float64
	for _, seg := range c.ExperienceCurve {
		sum, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return 0, err
		}
		offset += sum
	}
	return offset, nil
}

// Experience maps a stored (integer) experience total onto the level model.
//
// **rule を順に walk し、その中で binary search する。** rule 数 S、1 rule の最大 level
// 数 L として O(S log L) 時間・O(1) メモリ。累積はすべて閉形式で求める。
//
// **整数XP は小数しきい値に ceil で到達する。** 累積が 2 / 4.5 のとき XP 4 は level 1 で
// currentLevelExp 2、XP 5 で level 2。currentLevelExp は floor(整数XP - しきい値)。
//
// **JSON で表現できない NaN を返す経路が無い。** すべてのしきい値が有限であることを
// Validate で保証してからしかここに到達しない。
func (c Config) Experience(totalExp int64) (Experience, error) {
	if totalExp < 0 || totalExp > MaxExperience {
		return Experience{}, fmt.Errorf("rolelevel: 総経験値 %d は 0..%d の範囲外です", totalExp, MaxExperience)
	}
	levelUps := c.TotalLevelUps()
	maxLevel := c.MaxLevel()
	total := float64(totalExp)
	// **offset は rule をまたいで持ち越す。** ここを 0 に戻すと前の rule で 0.5 余った
	// 分が消える (2.5 + 3.25 が 3.25 になってしまう)。
	offset := 0.0
	completed := int64(0)

	for _, seg := range c.ExperienceCurve {
		segTotal, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return Experience{}, err
		}
		if total < offset+segTotal {
			budget := total - offset
			if budget < 0 {
				budget = 0
			}
			k, err := seg.completedLevelUps(budget)
			if err != nil {
				return Experience{}, err
			}
			done, err := seg.SegmentCumExp(k)
			if err != nil {
				return Experience{}, err
			}
			threshold := offset + done
			// **次のしきい値は「次の1 level の cost を足したもの」**。rule 全体の
			// 累積 (segTotal) ではない — それは rule を走り終えた先で、まだ到達して
			// いない。`seg.SegmentCumExp(k+1)` を使う。
			nextThreshold, err := seg.SegmentCumExp(k + 1)
			if err != nil {
				return Experience{}, err
			}
			next := int64(math.Ceil(offset+nextThreshold)) - totalExp
			return Experience{
				CurrentLevel:     c.BaseLevel + completed + k,
				CurrentLevelExp:  floorExperience(total - threshold),
				NextLevelExp:     &next,
				TotalExp:         totalExp,
				MinLevel:         c.MinLevel(),
				MaxLevel:         maxLevel,
				ProgressionStage: completed + k + 1,
			}, nil
		}
		offset += segTotal
		completed += seg.LevelUps
	}

	// 最大 level 到達後の余剰は currentLevelExp に持ち越す。nextLevelExp は無い。
	return Experience{
		CurrentLevel:     maxLevel,
		CurrentLevelExp:  floorExperience(total - offset),
		NextLevelExp:     nil,
		TotalExp:         totalExp,
		MinLevel:         c.MinLevel(),
		MaxLevel:         maxLevel,
		ProgressionStage: levelUps + 1,
	}, nil
}

// floorExperience computes floor(整数XP - 小数しきい値) with a floor at 0.
//
// **`totalExp - floor(threshold)` ではない。** 小数しきい値の端が次の level の
// 進捗として残るので、XP を先に floor すると余りを取り違える (しきい値 2.5 に
// 整数XP 4 なら floor(4 - 2.5) = 1)。
func floorExperience(remaining float64) int64 {
	f := math.Floor(remaining)
	if f < 0 {
		return 0
	}
	return int64(f)
}

// Validate rejects a configuration the plugin could not evaluate later.
//
// **保存時にだけ走る。** policy 解決のたびに防御しないのは、そのための設定が保存時点で
// 拒まれているから。
func (c Config) Validate(cat *Catalog) error {
	if c.BaseLevel < -MaxExperience || c.BaseLevel > MaxExperience {
		return invalid(CodeInvalidBaseLevel, "baseLevel",
			"%d..%d の範囲で指定してください (%d)", -MaxExperience, MaxExperience, c.BaseLevel)
	}
	var offset float64
	var levelUps int64
	for i, seg := range c.ExperienceCurve {
		if err := validateCurveSegment(seg); err != nil {
			// field 名に index を足すだけで、code と条文は segment 検査が持つ。
			var ve *ValidationError
			if errors.As(err, &ve) {
				return &ValidationError{Code: ve.Code,
					Field: fmt.Sprintf("experienceCurve[%d].%s", i, ve.Field), Err: ve.Err}
			}
			return err
		}
		// progressionStage is one-based and policy ranges use an exclusive
		// endpoint, so the JSON-safe aggregate must leave room for both +1s.
		if seg.LevelUps > MaxExperience-2-levelUps {
			return invalid(CodeInvalidCurve, "experienceCurve",
				"level-up count の合計は %d 以下にしてください", MaxExperience-2)
		}
		levelUps += seg.LevelUps
		sum, err := seg.SegmentCumExp(seg.LevelUps)
		if err != nil {
			return invalid(CodeInvalidCurve, "experienceCurve", "%s", err)
		}
		offset += sum
		// **rule をまたいだ合計も見ておく。** 各 rule 之内だけなら通るが、合計が上限を
		// 超えると Experience の currentLevelExp が壊れる。
		if math.IsNaN(offset) || math.IsInf(offset, 0) || offset > MaxExperienceFloat {
			return invalid(CodeInvalidCurve, "experienceCurve",
				"curve 全体の累積 (%v) が有限かつ %v 以下である必要があります", offset, MaxExperienceFloat)
		}
	}
	if c.BaseLevel > MaxExperience-levelUps {
		return invalid(CodeInvalidBaseLevel, "baseLevel",
			"baseLevel + level-up count が %d 以下になるよう指定してください", MaxExperience)
	}
	return validateRanges(c.PolicyRanges, levelUps, cat)
}

// validateCurveSegment rejects one segment whose evaluation would leave the
// safe-integer range or produce a non-positive, NaN or infinite level-up cost.
func validateCurveSegment(seg Curve) error {
	switch seg.Type {
	case CurveConst, CurveLinear, CurveExponential:
	default:
		return invalid(CodeInvalidCurve, "type",
			"type %q は %s|%s|%s のいずれかです", seg.Type, CurveConst, CurveLinear, CurveExponential)
	}
	if seg.LevelUps < 1 || seg.LevelUps > MaxExperience {
		return invalid(CodeInvalidCurve, "levelUps", "1..%d の範囲で指定してください (%d)", MaxExperience, seg.LevelUps)
	}
	if seg.Base < -MaxExperience || seg.Base > MaxExperience {
		return invalid(CodeInvalidCurve, "base", "%d..%d の範囲で指定してください (%d)",
			-MaxExperience, MaxExperience, seg.Base)
	}
	if seg.Additional < -MaxExperience || seg.Additional > MaxExperience {
		return invalid(CodeInvalidCurve, "additional", "%d..%d の範囲で指定してください (%d)",
			-MaxExperience, MaxExperience, seg.Additional)
	}
	if seg.Type == CurveExponential {
		if math.IsNaN(seg.Exponential) || math.IsInf(seg.Exponential, 0) || seg.Exponential <= 0 {
			return invalid(CodeInvalidCurve, "exponential",
				"0 より大きい有限数にしてください (%v)", seg.Exponential)
		}
	}
	return seg.ValidateCosts()
}

// ValidateCosts checks that every level-up cost of the segment is finite and greater
// than zero.
//
// **両端だけ見る。** コストは n の1次式 (const / linear) か単調な指数関数
// (exponential) なので、最小と最大は必ず n = 0 と n = LevelUps-1 にある。両端が条件を
// 満たしていれば中間も満たす。**全 level を走査しない**のは、level 数が 1 億でも O(1)
// で済ませるため。
func (c Curve) ValidateCosts() error {
	check := func(n int64) error {
		cost, err := c.LevelUpCost(n)
		if err != nil {
			return invalid(CodeInvalidCurve, "levelUps", "%s", err)
		}
		if cost <= 0 {
			return invalid(CodeInvalidCurve, "levelUps",
				"%d 番目の level-up の必要経験値 %v が 0 より大きい必要があります", n, cost)
		}
		return nil
	}
	if err := check(0); err != nil {
		return err
	}
	return check(c.LevelUps - 1)
}

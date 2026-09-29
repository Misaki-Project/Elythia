package rolelevel

import (
	"math"
	"testing"
)

// **未知の native policy key は拒否する** ので、catalog に無い key を range に
// 書けてはいけない。catalog 自体が host の既定とずれていたら「拒否すべき key を
// 受け入れる」ことになる。Task 2 Step 6 の drift gate がそれを host 側から止める。
func TestCatalogCoversOnlyKnownKinds(t *testing.T) {
	for _, key := range defaultCatalog.Keys() {
		kind, ok := defaultCatalog.Kind(key)
		if !ok {
			t.Fatalf("%q が Kind を持ちません", key)
		}
		switch kind {
		case KindBoolean, KindNumber, KindString, KindStringSet:
		default:
			t.Fatalf("%q の kind %q が不正です", key, kind)
		}
	}
	if _, ok := defaultCatalog.Kind("noSuchPolicyKey"); ok {
		t.Fatal("未知の key を admitted しています")
	}
}

func TestCatalogKeysAreSortedAndUnique(t *testing.T) {
	keys := defaultCatalog.Keys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("ソート順が崩れています (%q >= %q)", keys[i-1], keys[i])
		}
	}
}

// multiplier は **numeric な key だけ**に許される。boolean や enum に「1段ごとに
// 1.5倍」は意味が無いし、host 側の集約も乗算を受けない。
func TestCatalogNumericKeys(t *testing.T) {
	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"canPublicNote", false},
		{"chatAvailability", false},
		{"uploadableFileTypes", false},
		{"noteEachClipsLimit", true},
		{"userListLimit", true},
		{"rateLimitFactor", true},
		{"noSuchPolicyKey", false},
	} {
		if got := defaultCatalog.NumericKey(tt.key); got != tt.want {
			t.Fatalf("NumericKey(%q) = %t, want %t", tt.key, got, tt.want)
		}
	}
}

// JSON 経由の const は float64 / bool / string / []any で届く。整数のfloat64は
// int64へ寄せ、小数はhostが許す範囲ならそのまま保持する。
func TestCatalogNormalizeConst(t *testing.T) {
	got, err := defaultCatalog.NormalizeConst("noteEachClipsLimit", float64(200))
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(200) {
		t.Fatalf("NormalizeConst = %#v (%T), want int64(200)", got, got)
	}
	if _, err := defaultCatalog.NormalizeConst("canPublicNote", "true"); err == nil {
		t.Fatal("boolean に string を受理しています")
	}
	if _, err := defaultCatalog.NormalizeConst("chatAvailability", "nope"); err == nil {
		t.Fatal("enum に未知の値を受理しています")
	}
	if v, err := defaultCatalog.NormalizeConst("chatAvailability", "readonly"); err != nil || v != "readonly" {
		t.Fatalf("enum の受理: %v %v", v, err)
	}
	if v, err := defaultCatalog.NormalizeConst("uploadableFileTypes", []string{"text/*"}); err != nil || v == nil {
		t.Fatalf("string set の受理: %v %v", v, err)
	}
}

func TestCatalogNormalizeConstMatchesHostNumericValidation(t *testing.T) {
	if got, err := defaultCatalog.NormalizeConst("rateLimitFactor", 1.5); err != nil || got != 1.5 {
		t.Fatalf("host が許す小数を拒否しました: got=%#v err=%v", got, err)
	}
	if _, err := defaultCatalog.NormalizeConst("noteEachClipsLimit", math.Exp2(63)); err == nil {
		t.Fatal("host の int 上限外を受理しています")
	}
}

func TestCatalogNormalizeConstRejectsBlankStringSetItems(t *testing.T) {
	for _, value := range []any{[]string{""}, []string{" \t"}, []any{"image/*", " "}} {
		if _, err := defaultCatalog.NormalizeConst("uploadableFileTypes", value); err == nil {
			t.Fatalf("空白だけの string set 要素を受理しています: %#v", value)
		}
	}
}

func TestNativeNumberUsesHostNumberRangeWithoutRounding(t *testing.T) {
	got, err := nativeNumber(1.5)
	if err != nil || got != 1.5 {
		t.Fatalf("nativeNumber(1.5) = %#v, %v; want 1.5, nil", got, err)
	}
	if _, err := nativeNumber(math.Exp2(63)); err == nil {
		t.Fatal("nativeNumber は host の int 上限外を受理しています")
	}
	if _, err := nativeNumber(math.Inf(1)); err == nil {
		t.Fatal("nativeNumber は Infinity を受理しています")
	}
}

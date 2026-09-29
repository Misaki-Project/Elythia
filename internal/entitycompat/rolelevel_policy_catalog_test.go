package entitycompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/shiroha-a/mk/internal/effectivepolicy"
)

// roleLevel plugin は `internal/` を import できないので、native policy の schema を
// **自前の JSON ファイル**として持つ。**二重管理は放置できない。** ずれると plugin は
// 「拒否すべき key を受け入れる」「native が受け付けない型を contribution する」状態
// になる。host 側はそれを検証しない — `ValidateContributions` は宣言された key しか
// 見ないので、catalog が host とずれていても通ってしまう。
//
// そのため **host側の既定値と突き合わせる gate をここに置く**。
func TestRoleLevelCatalogMatchesNativeDefaults(t *testing.T) {
	type entry struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	}
	var doc struct {
		Keys []entry `json:"keys"`
	}
	path := filepath.Join("..", "..", "plugins", "rolelevel", "native_policy_catalog.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Keys) == 0 {
		t.Fatalf("%s に key がありません (列挙が壊れています)", path)
	}

	want := map[string]string{}
	for key, native := range effectivepolicy.Defaults() {
		switch native.(type) {
		case bool:
			want[key] = "boolean"
		case int:
			want[key] = "number"
		case string:
			want[key] = "string"
		case []string:
			want[key] = "stringSet"
		default:
			// 新しい kind が host 側に入ったのに catalog が unaware なら、plugin は
			// その key を level で変えられない。黙って無視すると「その policy は
			// level では動かない」ことが運営者に説明できない。
			t.Fatalf("native default %q の型 %T は catalog の kind に対応していません", key, native)
		}
	}

	got := map[string]string{}
	for _, e := range doc.Keys {
		if _, dup := got[e.Key]; dup {
			t.Errorf("%s に %q が重複しています", path, e.Key)
		}
		got[e.Key] = e.Kind
	}

	for key, kind := range want {
		gotKind, ok := got[key]
		if !ok {
			t.Errorf("native default %q (%s) が %s にありません", key, kind, path)
			continue
		}
		if gotKind != kind {
			t.Errorf("%q の kind が %s ですが native 側では %s です", key, gotKind, kind)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s に %q がありますが native default にありません", path, key)
		}
	}
}

// 制約付き文字列の enum は、**native 側の既定値自身を受け入れられること**。
// 見ているのは「enum を取りこぼしていないか」で、enum の一覧そのものは
// `effectivepolicy.valueValid` の手写字なので二重に持たない。
func TestRoleLevelCatalogEnumsAcceptTheNativeDefault(t *testing.T) {
	type entry struct {
		Key  string   `json:"key"`
		Enum []string `json:"enum,omitempty"`
	}
	var doc struct {
		Keys []entry `json:"keys"`
	}
	path := filepath.Join("..", "..", "plugins", "rolelevel", "native_policy_catalog.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	defaults := effectivepolicy.Defaults()
	var constrained []string
	for _, e := range doc.Keys {
		if len(e.Enum) == 0 {
			continue
		}
		constrained = append(constrained, e.Key)
		def, ok := defaults[e.Key].(string)
		if !ok {
			t.Errorf("%q に enum がありますが native default が文字列ではありません", e.Key)
			continue
		}
		if !enumContains(e.Enum, def) {
			t.Errorf("%q の enum に native default %q が含まれていません (%v)", e.Key, def, e.Enum)
		}
	}
	sort.Strings(constrained)
	// 制約付き enum を持つのは今のところ chatAvailability だけ。増えたときは値を
	// 丸めて通さないよう、この行を明示的に直す。
	if len(constrained) != 1 || constrained[0] != "chatAvailability" {
		t.Errorf("enum を持つ key が %v です。chatAvailability 以外が増えたら、この gate を明示的に直す必要があります", constrained)
	}
}

// enumContains is named to avoid clashing with the package's existing
// containsString helper in webpush_producer_test.go.
func enumContains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// Package deliverytargets lets the author of a note choose, per note, the
// remote servers it federates to: only the listed ones (include) or every one
// but the listed ones (exclude). It ports the "delivery targets" of CherryPick.
//
// 保存と判定はこのプラグインが持ち、配送と照会への適用は本体
// (plugin.Definition.NoteDelivery) が行う。本体は絞ることしかしないので、この
// プラグインの不具合で配送先が増えることは無い。
package deliverytargets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/elythia-network/elythia/plugin"
)

// Name is the plugin's name, which is also its key in notes/create pluginOptions.
const Name = "delivery-targets"

// MaxHosts caps the hosts one note may list. 照会のたびに全件を読むので、
// 際限なく並べられる形にしない。
const MaxHosts = 100

// Plugin is the plugin definition the build-time generator registers.
var Plugin = plugin.Definition{
	Name:       Name,
	Version:    "1.0.0",
	APIVersion: plugin.APIVersion,
	Migrations: []plugin.Migration{{
		Version: 1,
		SQL: `CREATE TABLE note_targets (
	note_id    varchar(32) PRIMARY KEY,
	user_id    varchar(32) NOT NULL,
	mode       text NOT NULL CHECK (mode IN ('include', 'exclude')),
	hosts      jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
)`,
	}},
	NoteDelivery: noteDelivery,
	Routes:       routes,
}

// Option is what the post form sends as pluginOptions["delivery-targets"].
type Option struct {
	Mode  string   `json:"mode"`
	Hosts []string `json:"hosts"`
}

// routes registers POST /available, which the frontend asks before showing
// its button, and POST /follower-hosts (see followerHostsHandler).
//
// 設定の enabled: false でバックエンドを止めても、frontend はビルドに含まれた
// まま読み込まれる。そのままボタンを出すと、選んで投稿したときに notes/create
// が pluginOptions を受け取る相手がおらず INVALID_PARAM になる。プラグインが
// 止まっていればこのルートも登録されないので、答えたときだけボタンを出す。
func routes(ctx plugin.Context, r plugin.Router) error {
	r.POST("/available", func(plugin.Request) (any, error) {
		return map[string]bool{"available": true}, nil
	})
	r.POST("/follower-hosts", followerHostsHandler(ctx.API(), ctx.Logger(), newFollowerHostsCache()))
	return nil
}

func noteDelivery(ctx plugin.Context) (plugin.NoteDeliveryRegistration, error) {
	store := &store{db: ctx.Storage().DB()}
	return plugin.NoteDeliveryRegistration{Prepare: store.prepare, Targets: store.targets}, nil
}

type store struct {
	db *sql.DB
}

// prepare validates the author's choice and stores it under the note's ID.
//
// サーバーを1つも指定していなければ、include でも exclude でも何も絞らない
// (指定が無いのと同じく、すべてのサーバーへ配送し照会も許す)。行も作らない。
func (s *store) prepare(ctx context.Context, req plugin.NoteDeliveryPrepareRequest) error {
	opt, err := ParseOption(req.Option)
	if err != nil {
		return fmt.Errorf("%w: %w", plugin.ErrBadNoteOption, err)
	}
	if len(opt.Hosts) == 0 {
		return nil
	}
	hosts, err := json.Marshal(opt.Hosts)
	if err != nil {
		return err
	}
	// 同じIDは二度来ない (本体がノートごとに1回だけ呼ぶ)。来たら後の指定で
	// 置き換える。前の指定が残るより、利用者がいま選んだものが効くほうがよい。
	_, err = s.db.ExecContext(ctx, `INSERT INTO note_targets (note_id, user_id, mode, hosts) VALUES ($1, $2, $3, $4)
ON CONFLICT (note_id) DO UPDATE SET user_id = EXCLUDED.user_id, mode = EXCLUDED.mode, hosts = EXCLUDED.hosts`,
		req.NoteID, req.UserID, opt.Mode, string(hosts))
	return err
}

// targets reads the stored choices of ids.
func (s *store) targets(ctx context.Context, ids []string) (map[string]plugin.DeliveryTargets, error) {
	out := make(map[string]plugin.DeliveryTargets)
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT note_id, mode, hosts FROM note_targets WHERE note_id IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 読み取りだけなので Close の失敗で結果は変わらない
	for rows.Next() {
		var id, mode string
		var raw []byte
		if err := rows.Scan(&id, &mode, &raw); err != nil {
			return nil, err
		}
		var hosts []string
		if err := json.Unmarshal(raw, &hosts); err != nil {
			return nil, fmt.Errorf("note %s: hosts: %w", id, err)
		}
		out[id] = plugin.DeliveryTargets{Mode: plugin.DeliveryTargetsMode(mode), Hosts: hosts}
	}
	return out, rows.Err()
}

// errMode reports a mode other than include / exclude.
var errMode = errors.New(`mode must be "include" or "exclude"`)

// ParseOption decodes and normalizes a pluginOptions value.
func ParseOption(raw json.RawMessage) (Option, error) {
	var opt Option
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opt); err != nil {
		return Option{}, err
	}
	if opt.Mode != string(plugin.DeliveryTargetsInclude) && opt.Mode != string(plugin.DeliveryTargetsExclude) {
		return Option{}, errMode
	}
	if len(opt.Hosts) > MaxHosts {
		return Option{}, fmt.Errorf("hosts: more than %d", MaxHosts)
	}
	hosts := make([]string, 0, len(opt.Hosts))
	seen := make(map[string]bool, len(opt.Hosts))
	for _, h := range opt.Hosts {
		n, err := NormalizeHost(h)
		if err != nil {
			return Option{}, err
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		hosts = append(hosts, n)
	}
	opt.Hosts = hosts
	return opt, nil
}

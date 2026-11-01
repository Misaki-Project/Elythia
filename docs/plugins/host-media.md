# プラグインの認証付きメディアとロール参照

`plugin.Request`の必須メソッドは変わらない。ロールに応じたアクセスや制限が必要なプラグインは、任意の拡張`plugin.ActiveRoleRequest`へ型アサーションする。`ActiveRoleIDs()`は、認証済み利用者についてホストのロールサービスが評価した有効な手動・条件付きロールのIDを、重複を除いてソートして返す。匿名利用者には空の一覧を返す。認証済み利用者の取得失敗・サービス未配線はエラーになる。拡張が無い場合やエラーの場合、プラグインはロール制限を緩めず処理を拒否する。この参照によってロールの付与やポリシーは変更されない。

テスト用の`plugintest.Request`には`ActiveRoleIDs`と`ActiveRoleError`を指定できる。返された一覧の変更は後続の参照へ影響しない。

フロントエンドの`PluginHost.fetch(path, options)`は、ログイン中の利用者のBearer認証で同一オリジンのプラグインAPIを呼ぶ。`path`は`/api/plugin/<自分の名前>/`以下のルート相対パスに限る。外部URL、他プラグイン、ドットによる階層移動、エスケープされたパス、フラグメント、認証用の`i`クエリは拒否する。Cookieは送らず、リダイレクトは拒否する。呼び出し元のAuthorizationはホストの認証に置き換え、`signal`を含むその他のリクエスト設定は保持する。戻り値は`Response`なので、HTTPステータスを確認して`blob()`を呼ぶ。

`PluginHost.openMedia(items, initialId?)`は、取得済みの`Blob`を既存のライトボックスで表示する。`PluginMediaItem`には`id`、`type`(`image`/`video`/`audio`)、`blob`、任意の寸法・ファイル名・`isSensitive`・`initiallyRevealed`を渡す。MIMEはホストのブラウザ表示用許可一覧と`type`の両方に一致する必要があり、SVGは含まない。生成したObject URLは閉じたときや起動失敗時に解放する。`initiallyRevealed`は呼び出し元で既に開示済みの項目だけに指定する。

任意の`PluginMediaItem.alt`はBlobメディアの代替テキストとしてviewerへ引き継ぐ。`filename`は表示名・ダウンロード名として独立して扱い、代替テキストには使わない。既存のDriveFileがある場合は`comment`を優先し、それが未設定なら`alt`を使う。どちらも未指定なら従来どおり代替テキストを追加しない。

`MkA`、`MkUrl`、`MkSensitiveMediaGuard`も`@/plugin-api.js`から公開する。ガードは`isSensitive`と`kind`(`image`/`video`)を受け取り、`v-model:revealed`で開示状態を返す。センシティブ表示設定と確認ダイアログはホストと共通で、ライトボックスでもドライブファイルと同じ設定を使う。メディアの取得・プレビューの開始条件はプラグインが既存の`PreviewContext`とキャンセルを使って守る。

## 無効化されたプラグインの選択的な同梱

`Dockerfile.bundled`のビルド引数`PLUGIN_INCLUDE_DISABLED_DIR`は、既存の生成ツールの`-include-disabled-dir`へ渡される。既定値は空で、`disabled: true`のプラグインを通常どおり除外する。明示した場合も、指定したディレクトリだけが追加対象になる。

```bash
docker build -f Dockerfile.bundled \
  --build-arg PLUGIN_INCLUDE_DISABLED_DIR=plugins/fxtwitter-preview \
  -t elythia:plugin-review .
```

この指定はビルドへの同梱だけを行う。プラグインの実行時設定や外部取得は有効にしない。外部取得の初期値がOFFのプラグインは、その設定を保持する。構築・確認は本番の配信元と分離したチェックアウトで行う。

# プラグインの認証付きメディアと checked role policy

`plugin.Request`と`plugin.Context`の必須メソッドは変わらない。native role policyを使って認可するプラグインは任意拡張`plugin.CheckedRolePolicyRequest`を使い、`CheckedRolePolicies(keys...)`で必要なキーだけを解決する。DB・provider・policy入力の取得失敗はエラーになり、プラグインは許可へ倒さず拒否する。バックグラウンド処理では`plugin.CheckedUserRolePolicyContext`を使い、投入時のactorのpolicyを処理直前に再確認できる。

`plugintest.Request`の`CheckedRolePolicies`と`CheckedRolePolicyError`、`Harness.WithCheckedUserRolePolicies`で成功値と失敗をテストできる。

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

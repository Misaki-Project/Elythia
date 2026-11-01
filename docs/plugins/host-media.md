# プラグインの認証付きメディア

## 取得と表示

`PluginHost.fetch(path, options?)`は、ログイン中の利用者のBearer認証で同一オリジンのプラグインAPIを呼ぶ。`path`は`/api/plugin/<自分の名前>/`以下のルート相対パスに限る。外部URL、他プラグイン、ドットによる階層移動、エスケープされたパス、フラグメント、認証用の`i`クエリは拒否する。`mode: 'same-origin'`、`credentials: 'omit'`、`redirect: 'error'`、Authorizationはホストが決め、呼び出し元から変更できない。`Accept`や`signal`を含むその他の設定は保持する。未ログインなら通信前に失敗する。

戻り値は`Response`なので、HTTPステータスを確認して`blob()`を呼ぶ。リダイレクトや通信・中止のエラーは呼び出し元へ返し、自動再試行しない。

```ts
const controller = new AbortController();
const response = await host.fetch('/api/plugin/myplugin/media/1', {
	signal: controller.signal,
	headers: { Accept: 'image/png' },
});
if (!response.ok) throw new Error(`Media request failed: ${response.status}`);
await host.openMedia([{
	id: 'image-1', type: 'image', blob: await response.blob(),
	filename: 'photo.png', alt: '湖の上を飛ぶ赤い凧', isSensitive: true,
}], 'image-1');
```

`PluginHost.openMedia(items, initialId?)`は、取得済みの`Blob`を既存のライトボックスで表示する。`PluginMediaItem`には`id`、`type`(`image`/`video`/`audio`)、`blob`、任意の`width`・`height`・`filename`・`alt`・`isSensitive`・`initiallyRevealed`を渡す。MIMEはホストのブラウザ表示用許可一覧と`type`の両方に一致する必要があり、SVGは含まない。全件を検証してからObject URLを生成し、閉じたとき、割り当てや起動に失敗したときに解放する。画像だけをサムネイルとして渡し、動画と音声には画像用URLを付けない。

`initialId`が一致しなければ先頭を選ぶ。`initiallyRevealed`は呼び出し元で既に開示済みの項目だけに指定する。センシティブ表示設定と確認ダイアログはドライブファイルと共通で、未開示の動画・音声はプレースホルダーから開く。

`alt`は代替テキストとして引き継ぐ。`filename`は表示名・ダウンロード名として独立して扱い、代替テキストには使わない。共有ビューアに既存のDriveFileがある場合は`comment`を優先し、それが未設定なら`alt`を使う。どちらも未指定なら従来どおり代替テキストを追加しない。

## 共有コンポーネント

`MkA`、`MkUrl`、`MkSensitiveMediaGuard`も`@/plugin-api.js`から遅延読み込みで公開する。ガードは`isSensitive`と`kind`(`image`/`video`)を受け取り、`v-model:revealed`で開示状態を返す。センシティブ表示設定と確認ダイアログはホストと共通で、隠している間はスロットをぼかして操作を無効にする。

メディアの取得・プレビューの開始条件はプラグインが既存の`PreviewContext`とキャンセルを使って守る。これらのAPI自体は外部取得やプレビューを自動で開始しない。呼び出し元の有効なロールの参照は[作者向けの任意拡張](authoring.md#ロールの付与を確認する)を参照。

# tor-exit-lookup アーキテクチャ

本書は tor-exit-lookup が *なぜ* この設計なのかを説明します。各パッケージが
*何を* するかは、パッケージの doc コメントと [AGENTS.md](../../AGENTS.md) を
参照してください。

## 目的

一つの問いにオフラインで高速に答える——**この IP は Tor Exit node か？** 外部
依存ゼロの安価な membership 判定に最適化し、asn-lookup / abuse-lookup と並んで
ログトリアージ・IR・シェルパイプラインに組み込めることを狙います。

## なぜオフラインリストか（ライブ判定ではなく）

ライブサービス（`https://check.torproject.org/`）は「*あなた* が Tor 経由か」を
答えますが、これは呼び出し元自身の接続しか見ません。ログ中の *任意の* IP を
分類するには exit-node 集合全体が必要です。Tor Project はそれを
`torbulkexitlist`（1 行 1 IP の素のテキスト）として配布しています。

したがってモデルは abuse-lookup ではなく asn-lookup を踏襲します——**データ
セット全体を一度ダウンロードし、ローカルで照会する**。以降の `check` は純粋・
オフライン・O(1) の判定です。これは「クエリごとにポーリングしない」という Tor
Project の要望にも沿います（取得は `update` 時のみ、あとはキャッシュ）。

## なぜ認証情報が不要か

エンドポイントは公開・認証不要です。asn-lookup（ipinfo トークン）や
abuse-lookup（AbuseIPDB キー）と違い、保存・秘匿・漏洩を気にすべき秘密が
ありません。これにより設定とセキュリティの一群の懸念が消えます。`config` に
キー項目はなく、fetcher にも秘匿すべきトークンはありません。

## ストア

数千件の exit アドレスにバイナリ索引は不要です。ストアは小さな JSON:

```json
{ "generated_at": "…Z", "source": "…/torbulkexitlist", "count": 1392, "exits": ["…"] }
```

読み込み時に `map[netip.Addr]struct{}` へパースし、`Contains` はマップ照合です。
意図的な2つの選択:

- **決定論的シリアライズ。** `Serialize` はアドレスをソートし `generatedUnix` を
  引数で受け取ります——時計を読むのは `engine.Update` のみ。同一入力は常に
  バイト同一の出力になり、ストアは diff 可能でテストは hermetic を保ちます。
- **鮮度は mtime ではなくレコード内に。** `generated_at` はファイル内にあるため、
  コピー・復元しても本当の古さを保ちます。`StaleAfter` は 24h: 上流は約 30 分
  ごとに更新されるので、1 日前のコピーは明確に古い（asn-lookup は月次 DB のため
  30 日）。

書き込みは temp + rename を経由し、書き込み途中のクラッシュでも切り詰められた
ストアを読み戻すことはありません。

## アドレスの正規化

すべてのアドレスはパース時・照会時ともに `Unmap()` で正規化され、v4-in-v6 形式
（`::ffff:1.2.3.4`）が素の v4 エントリと一致します。torbulkexitlist は現状
IPv4 中心ですが、IPv6 も同様に扱います。

## レイヤリング

```
app  →  engine  →  { torproject (fetch), exitlist (parse/store) }
                     ↑
                   config
```

`engine` は CLI と（Phase 2）MCP サーバーが共有する唯一のフローで、両者の
振る舞いは乖離しません。fetcher はインターフェースなので、engine は fake で
テストされます——テストスイートにネットワークは登場しません。`exitlist` は
純粋で、最も高いテストカバレッジを持ちます。

## 終了コードの契約

`check` は grep の慣例に従います: `0` = 一致、`1` = 不一致、`2` = エラー。POSIX の
終了コードは符号なし 0–255 なので、3 状態は `{0,1,2}` に写像します（負値は表現
不可）。これにより `if tor-exit-lookup check "$ip"` が自然に読めます。

この 3 状態は **単一の位置引数 IP をテキストモードで判定する場合のみ**（＝
スクリプト向けの経路）適用されます。バッチ形（複数 IP・stdin・`--json`）では
結果を stdout に移し、終了コードはエラーのみ（`0`/`2`）を表すため、パイプした
ファイル中の 1 行のタイポで全体が失敗することはありません。

## 2 ソース・1 membership

membership は `torbulkexitlist` のみから決まり、`exit-addresses` は exit アドレス
をキーとしたノード別メタデータ（fingerprint、Published、LastStatus）を任意で
供給します。両フィードには既知の差異があるため、規則は意図的です——**`is_exit`
は `torbulkexitlist` 単独で決定**し、メタデータはヒットを補完するだけ。メタデータ
の欠損は正常で、答えを変えません。メタデータは非本質的なので `exit-addresses` の
取得失敗は *ソフト*: `update` は membership を書き込み `MetaWarning` を surface
します。一方 `torbulkexitlist` の失敗はハードエラーです。`exit_addresses_url = ""`
でメタデータを完全に無効化できます。

## 自動再取得と礼儀

上流のリストは頻繁に変わるため、`check` は `engine.EnsureFresh` を呼びます。
キャッシュが TTL より古ければ先に再取得します。2 つの安全策で礼儀と堅牢性を
保ちます。TTL は 30 分（`config.MinTTL`）がフロアなので、設定に関わらず
自動再取得がエンドポイントを叩きすぎることはありません。また再取得の失敗は
キャッシュがあれば致命的でなく——`EnsureFresh` は失敗時に stale な集合を
エラーと共に返すため、`check` は警告してオフラインで答えます。`status` は
一切取得せず、キャッシュの状態をそのまま報告します。

## MCP 面

`tor-exit-lookup mcp` は標準ライブラリの JSON-RPC 2.0 stdio ループで同じ `engine`
を駆動するため、CLI と MCP の答えは乖離しません。結果は小さい（yes/no ＋わずかな
メタデータ）ので、asn-lookup の逆引きと違い file-mediation もワークスペースも
ありません。サーバーはストアの mtime をキーにパース済み集合をキャッシュし、
`update_list` 後に再読込します。`get_usage` マニュアルは埋め込みで、広告される
ツール集合に対するテストで固定されています。

## セキュリティとプライバシー

認証情報なし、テレメトリなし。ネットワーク egress は 2 つの公開リストの `update`
／自動再取得のみ。キャッシュはローカル限りで、再配布はしません。

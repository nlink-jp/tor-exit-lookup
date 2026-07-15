# tor-exit-lookup

指定した IP アドレスは **Tor Exit node** か？ `tor-exit-lookup` は Tor Project
配布の [`torbulkexitlist`](https://check.torproject.org/torbulkexitlist)
をローカルにキャッシュし、[`exit-addresses`](https://check.torproject.org/exit-addresses)
のノード別メタデータで補強してオフラインで判定します。`update` で一度リストを
ダウンロードすれば（あるいは自動再取得に任せれば）、以降の `check` はメモリ上の
集合照合だけ——ネットワーク接続も認証情報も不要です。

[`asn-lookup`](https://github.com/nlink-jp/asn-lookup)（AS / 国）・
[`abuse-lookup`](https://github.com/nlink-jp/abuse-lookup)（評判）の、オフライン
かつ membership 判定に特化した姉妹品。CLI パイプと MCP の両面で、3 つを
組み合わせて IP を多角的にプロファイルします。

## インストール

未リリース。ソースからビルドする場合（Go 1.25+）:

```sh
git clone https://github.com/nlink-jp/tor-exit-lookup
cd tor-exit-lookup
make build          # → dist/tor-exit-lookup
```

## クイックスタート

```sh
# 1. Tor exit list をダウンロード（公開エンドポイント・認証不要）:
tor-exit-lookup update

# 2. アドレスを判定:
tor-exit-lookup check 2.56.10.36
# → 2.56.10.36 is a Tor Exit node  [5B324A627C4F…, last seen 2026-07-15 01:00]

tor-exit-lookup check 8.8.8.8
# → 8.8.8.8 is not a Tor Exit node        (終了コード 1)

# 3. 終了コードをスクリプトで利用:
if tor-exit-lookup check "$ip"; then
  echo "$ip は Tor 経由です"
fi

# 4. ログ中の IP を一括フィルタ:
cut -f1 access.log | tor-exit-lookup check --json | jq 'select(.is_exit)'
```

## コマンド

| コマンド | 説明 |
|----------|------|
| `check <IP>...` | 各 IP が Tor Exit node かを判定（引数なしなら stdin） |
| `update` | exit list + メタデータをダウンロードしローカルストアを再構築 |
| `status` | キャッシュの鮮度・件数・ソースを表示 |
| `mcp` | stdio 経由のローカル MCP サーバーとして起動 |
| `version` | バージョンを表示 |

### `check` のモードと終了コード

単一の位置引数 IP をテキストモードで判定する場合、grep の慣例に従い、シェルで
自然に合成できます:

| コード | 意味 |
|--------|------|
| `0` | その IP は Tor Exit node **である** |
| `1` | その IP は Tor Exit node **ではない** |
| `2` | エラー（不正な IP、ローカルリスト無し など） |

それ以外の形（複数 IP・stdin 入力・`--json`）は **バッチモード**: 各 IP の結果を
1 行ずつ stdout に出し、終了コードはエラー有無のみ（`0` / `2`）。`--json` は
1 IP 1 オブジェクトの JSON Lines を出力します
（`{ip, is_exit, fingerprint?, published?, last_status?, checked_at, list_updated_at}`）。

## 自動再取得

Tor exit list は頻繁に変わります（上流は約 30 分ごとに更新）。既定では `check`
はキャッシュが TTL（既定 1 時間、取得礼儀のため 30 分がフロア）より古いときに
自動再取得します。再取得に失敗した場合（オフライン等）は、失敗させず警告付きで
キャッシュを使います。1 回だけ無効化するなら `--no-update`、恒久的に無効化する
なら `[torproject] auto_update = false`。

## MCP サーバー

`tor-exit-lookup mcp` は stdio 上で JSON-RPC 2.0 を話します（標準ライブラリのみ）。
ツール: `check_ip`、`list_status`、`update_list`、`get_usage`（埋め込みの操作
マニュアル。initialize の `instructions` フィールドでも案内）。登録例:

```json
{
  "mcpServers": {
    "tor-exit-lookup": { "command": "tor-exit-lookup", "args": ["mcp"] }
  }
}
```

## 設定

認証情報は不要です（エンドポイントは公開）。すべて既定値があり、設定ファイル・
環境変数・フラグで上書きできます。

```toml
# ~/.config/tor-exit-lookup/config.toml
[torproject]
# bulk_url = "https://check.torproject.org/torbulkexitlist"
# exit_addresses_url = "https://check.torproject.org/exit-addresses"  # "" でメタデータ無効
# ttl_minutes = 60      # 自動再取得のしきい値（30 がフロア）
# auto_update = true    # 古いとき check で自動再取得

[store]
# path = "~/.local/share/tor-exit-lookup/exitlist.json"
```

| 設定 | 環境変数 | フラグ | 既定値 |
|------|----------|--------|--------|
| リスト URL | `TOR_EXIT_LOOKUP_URL` | `--url`（update） | `…/torbulkexitlist` |
| メタデータ URL | `TOR_EXIT_LOOKUP_EXIT_ADDRESSES_URL` | — | `…/exit-addresses` |
| ストアパス | `TOR_EXIT_LOOKUP_STORE` | `--store` | `~/.local/share/tor-exit-lookup/exitlist.json` |
| TTL（分） | `TOR_EXIT_LOOKUP_TTL_MINUTES` | — | `60`（最小 30） |
| 自動更新 | `TOR_EXIT_LOOKUP_AUTO_UPDATE` | `--no-update`（無効化） | `true` |
| 設定パス | — | `-c`, `--config` | `~/.config/tor-exit-lookup/config.toml` |

## 仕組み

`update` はリスト全体（membership）と exit-addresses（メタデータ）を取得し、
`netip.Addr` の集合＋メタデータの副表へパースして、小さな決定論的 JSON ストアを
書き出します（temp + rename のアトミック書き込み）。`check` はその集合を読み込み
O(1) の membership 判定を行います——数千件なら索引は不要です。membership の正本は
`torbulkexitlist` で、メタデータの欠損は yes/no の答えを変えません。鮮度はファイル
の mtime ではなくストア内の `generated_at` に記録され、コピーしても保持されます。
`status` はローカルコピーが 24 時間より古くなると警告します。

## 開発

```sh
make test        # go test -race -cover ./...
make check       # lint + test + build-all
```

外部依存ゼロ（標準ライブラリのみ）。設計の背景は
[docs/ja/architecture.ja.md](docs/ja/architecture.ja.md) を参照。

## ライセンス

MIT — [LICENSE](LICENSE) 参照。Exit list データは
[Tor Project](https://www.torproject.org/) が提供しています。キャッシュはローカル
限りで、再配布はしません。

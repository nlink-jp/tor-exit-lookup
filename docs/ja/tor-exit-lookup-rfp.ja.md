# RFP: tor-exit-lookup

> Generated: 2026-07-15
> Status: Draft

## 1. Problem Statement

指定した IP アドレスが **Tor Exit node の IP かどうか** をオフラインで即座に判定する
CLI 兼 MCP サーバー。Tor Project が配布する `torbulkexitlist`（素の IP 一覧）を
membership の正本、`exit-addresses`（fingerprint / LastStatus 付き）をメタデータ源
としてローカルにキャッシュし、外部依存ゼロ・認証不要で照合する。想定ユーザーは
IR / SOC 業務やログ分析で「このアクセス元は Tor 経由か？」を素早く確認したい
セキュリティ担当。asn-lookup（AS / 国）・abuse-lookup（IP 評判）と組み合わせて
IP を多角的にプロファイルする 3 点セットの一枚を担う。

## 2. Functional Specification

### Commands / API Surface

**CLI**

| コマンド | 説明 |
|----------|------|
| `tor-exit-lookup <IP>` | 単一 IP が Tor Exit node かを判定 |
| `tor-exit-lookup --json <IP>` | JSON 出力（`{ip, is_exit, fingerprint?, last_status?, checked_at, list_updated_at}`） |
| `cat ips.txt \| tor-exit-lookup` | 標準入力バッチ（1 行 1 IP、パイプ連携） |
| `tor-exit-lookup update` | `torbulkexitlist` / `exit-addresses` を再取得しキャッシュ更新 |
| `tor-exit-lookup status` | キャッシュの鮮度（最終更新、件数、古さ） |

**終了コード（単一 IP 判定時）**

- `0` = 対象 IP は Tor Exit node（ヒット）
- `1` = Exit node ではない（非ヒット）
- `2` = エラー（不正な IP／キャッシュ無し／取得失敗）

`if tor-exit-lookup $ip; then ...` が grep 風に自然に書ける。バッチ／JSON モードは
結果を stdout に出すため、終了コードはエラー有無のみ（正常 `0` / エラー `2`）。

**MCP tools**（asn-lookup にミラー）

| tool | 説明 |
|------|------|
| `check_ip` | 単一 IP の Tor Exit 判定（メタデータ含む） |
| `list_status` | キャッシュ鮮度確認（asn-lookup の `db_status` 相当） |
| `update_list` | リスト再取得（`update_db` 相当） |
| `get_usage` | ツールリファレンス／エラー復旧表 |

### Input / Output

- 入力: 単一 IP（引数）または stdin バッチ（1 行 1 IP）
- 出力: 人間可読テキスト（既定）／ `--json`
- JSON スキーマ（単一）: `{ip, is_exit, fingerprint?, published?, last_status?, checked_at, list_updated_at}`
- メタデータは判定ヒット時に exit-addresses から補完。無い場合は判定のみ返す

### Configuration

- キャッシュ保存先: XDG 準拠のローカルデータディレクトリ（asn-lookup 準拠）
- TTL: 既定値をフラグ／環境変数で上書き可。**TTL フロアは 30 分**（Tor 側の更新間隔と礼儀に合わせる）
- credential 不要（設定ファイルにトークン等を持たない）

### External Dependencies

- 出典 1: `https://check.torproject.org/torbulkexitlist`（membership 正本、IP のみ、公開・認証不要）
- 出典 2: `https://check.torproject.org/exit-addresses`（メタデータ源、fingerprint / Published / LastStatus、公開・認証不要）
- 実装依存: Go 標準ライブラリのみ（`net/http` + `net/netip`）。外部依存ゼロ

## 3. Design Decisions

- **言語 = Go**。姉妹品（asn-lookup / abuse-lookup）と統一、単一バイナリ、
  `make build` → 署名 + notarize の既存リリース動線に乗る、外部依存ゼロ
- **データモデル**: 判定は `torbulkexitlist` を正本に `netip.Addr` のハッシュ集合で
  membership テスト（件数は数千規模、索引不要でメモリ照合即応）。メタデータは
  exit-addresses を IP キーで補完。両リストの既知差異は「判定 = torbulkexitlist、
  メタ = exit-addresses（欠損許容）」で吸収
- **補完関係**: asn-lookup（AS / 国）・abuse-lookup（評判）と 3 点セット。
  CLI パイプ / MCP の両面で相互運用
- **スコープ外**: Tor relay / bridge / guard の判定（Exit のみ）、リアルタイム接続
  確認、Tor 経由の通信そのもの、GUI

## 4. Development Plan

### Phase 1: Core

- `torbulkexitlist` 取得・キャッシュ・membership 照合（純関数）
- 単一 IP 判定 + 終了コード（0/1/2）
- `update` / `status`
- テスト: 照合ロジックは実データ隔離のフィクスチャで純関数テスト

### Phase 2: Features

- stdin バッチ、`--json`
- exit-addresses メタデータ統合（fingerprint / LastStatus）
- TTL 超過時の自動再取得（フロア 30 分）
- MCP サーバー（`check_ip` / `list_status` / `update_list` / `get_usage`）

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md、docs/{en,ja}
- notarize + 4 プラットフォームビルド
- submodule ポインタ / org profile / web catalog / Homebrew tap / check-org.sh

独立レビュー可能な単位: Phase 1（CLI コア）と Phase 2 の MCP は別々にレビュー可。

## 5. Required API Scopes / Permissions

**None.** 両エンドポイントは公開・認証不要。API キー／トークンは一切不要
（asn-lookup の ipinfo トークン、abuse-lookup の AbuseIPDB キーとは異なり
credential ゼロ）。

## 6. Series Placement

Series: **cybersecurity-series**

Reason: Tor Exit 判定は脅威インテリジェンス寄りのシグナルであり、IP 評判調査の
abuse-lookup と同系統。オフライン照合方式は asn-lookup（util-series）に似るが、
用途がセキュリティ判定である点を優先して cybersecurity-series に配置する。

## 7. External Platform Constraints

- Tor Project は取得の礼儀を求める（過度なポーリング禁止、キャッシュ推奨）。
  `torbulkexitlist` は概ね ~30 分間隔で更新される
- → **TTL フロアを 30 分**に設定し、自動再取得がエンドポイントを叩きすぎない
  ようにする。適切な User-Agent を付与
- IPv6: リストに含まれる場合があるため `netip.Addr` で v4 / v6 両対応
- ネットワーク不通時: 既存キャッシュで判定継続、`status` で古さを明示

---

## Discussion Log

- **ツール名**: `tor-exit-lookup` に決定。姉妹品 asn-lookup / abuse-lookup の
  `<対象>-lookup` 命名パターンに忠実。候補 `tor-lookup`（Tor 全般に読めてスコープ
  曖昧化）、`torexit-check`（lookup 系と語感ずれ）は不採用
- **シリーズ**: cybersecurity-series に決定。オフライン照合は asn-lookup（util）に
  似るが、Tor Exit 判定 = 脅威シグナルのため abuse-lookup と同系統を優先
- **鮮度処理**: TTL 超過時の自動再取得を採用。ただし Tor 側の礼儀に配慮し TTL
  フロア 30 分で叩きすぎを防止
- **終了コード**: ユーザー案「>0 / <0 / 0 の 3 状態」を受け、POSIX 終了コードは
  0–255 で負値不可のため grep モデルに寄せて {0 = ヒット, 1 = 非ヒット, 2 = エラー}
  に整理して合意
- **メタデータ**: v1 から exit-addresses も取得する方針。判定 = torbulkexitlist を
  正本、メタ = exit-addresses（欠損許容）の 2 ソース構成に決定
- **credential**: 両エンドポイント公開・認証不要のため API スコープは None

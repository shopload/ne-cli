# ne-cli

[ネクストエンジン API](https://developer.next-engine.com/) を、人間と AI エージェントの両方が使えるようにしたコマンドラインツールです。
[larksuite/cli](https://github.com/larksuite/cli) の設計（3 層のコマンド・`schema` による自己記述・JSON の出力契約・Agent Skills）を参考にしています。

- **全 131 エンドポイントに対応** — 公式の OpenAPI 仕様から生成したカタログをバイナリに埋め込み、エンドポイントごとにコマンドがあります
- **エージェント向け** — 出力は JSON、エラーは構造化（種別・コード・ヒント・リトライ可否）、`schema` でオフラインに仕様を引け、`--dry-run` で送信前に確認できます
- **間違いを送信前に止める** — 項目名の誤り、文字列型の区分コードへの範囲演算子（`004002`）などを API を呼ぶ前に検出します
- **トークンを自動で更新・保存** — API 応答で更新された `access_token` / `refresh_token` を保存し、標準出力には出しません

## インストール

Go 1.27 以上が必要です。

```bash
go install github.com/shopload/ne-cli@latest
# またはソースから
git clone https://github.com/shopload/ne-cli && cd ne-cli && make build
```

## はじめかた

ネクストエンジンの「アプリを作る」→ 該当アプリの「編集」→「API」タブにある、クライアント ID・クライアントシークレット・Redirect URI を使います。

```bash
ne-cli config init          # client_id / client_secret / redirect_uri を保存
ne-cli auth login           # ブラウザでログインしてトークンを保存
ne-cli auth status --verify
ne-cli master goods search --fields goods_id,goods_name -p 'goods_id-like=A%' --limit 10 --format table
```

### ログインの方法

`auth login` は Redirect URI に応じて動きを変えます。

| Redirect URI | 動き |
| --- | --- |
| `https://localhost:8443/callback` など localhost（ポート付き） | そのポートで待ち受け、リダイレクトを受け取って自動で完了します。https の場合は使い捨ての自己署名証明書を使うため、ブラウザに一度警告が出ます |
| それ以外 | ログイン URL を表示して終了します。ログイン後にリダイレクトされた URL を `ne-cli auth login --callback-url '<URL>'` に渡します（`state` の有効期限は 5 分） |

エージェントから使うときは、待ち受けをしない `ne-cli auth login --no-wait` を使います。

別のシステムで取得済みのトークンは、`ne-cli auth import`（標準入力に JSON）か、環境変数 `NE_ACCESS_TOKEN` / `NE_REFRESH_TOKEN` で渡せます。

access_token は 1 日、refresh_token は 3 日で切れます。有効な refresh_token があれば API を呼んだときに自動で更新されるので、無人運用では 3 日以内に 1 回 `ne-cli auth refresh` を実行してください。

## コマンドの 3 層

```text
ショートカット   ne-cli receiveorder +list --status 20 --with-rows
API コマンド     ne-cli receiveorder base search --fields … -p …
生の API         ne-cli api /api_v1_receiveorder_base/search -p …
```

### API コマンド

パス `/api_v1_<service>_<resource>/<method>` が、そのまま `ne-cli <service> <resource> <method>` になります。

```bash
ne-cli receiveorder base count -p receive_order_order_status_id-in=20,40 -p receive_order_cancel_type_id-eq=0
ne-cli system orderstatus info --format table
ne-cli master goods upload --data-type csv --data @goods.csv     # @ でファイルの内容を送る
```

パラメータの渡し方は 3 つあり、併用できます（下のものほど優先）。

| 渡し方 | 例 |
| --- | --- |
| `--params` に JSON | `--params '{"fields":"goods_id","goods_id-in":["A1","A2"]}'`（配列はカンマ区切りに変換。`@file`・`-` で標準入力） |
| 仕様にあるパラメータ名のフラグ | `--fields goods_id --limit 10 --receive-order-id 123` |
| `-p key=value`（繰り返し可） | `-p goods_id-like=A%` `-p goods_jan_code-blank` |

検索では `--fields all` で全項目、`--page-all` で offset を進めて全件を取得します（`--page-size` / `--page-limit` / `--page-delay`）。

### ショートカット

| コマンド | 内容 |
| --- | --- |
| `receiveorder +list` | 受注を検索。キャンセルを既定で除外し、`--with-rows` で明細を結合 |
| `receiveorder +update` | `--set 項目=値` から XML を組み立てて受注を更新。最終更新日（楽観ロック）は自動で取得 |
| `master +stock` | 商品コードから在庫数を取得 |
| `system +que-wait` | アップロード API が返す `que_id` の処理完了を待つ |

### schema

カタログはバイナリに入っているので、API を呼ばずに仕様を引けます。

```bash
ne-cli schema --format table                       # 全エンドポイント
ne-cli schema --search 在庫
ne-cli schema receiveorder.base.search --grep date # 項目名・型・説明
ne-cli schema receiveorder.base.update             # XML に書ける項目 (data_fields)
ne-cli schema errors 004002                        # メッセージコード
```

## 共通フラグ

| フラグ | 内容 |
| --- | --- |
| `--format json\|pretty\|table\|ndjson\|csv` | 出力形式（既定は `json`） |
| `--dry-run` | 送信する内容を表示するだけで、API を呼ばない（トークンは伏せ字） |
| `--yes` | 取り消しにくい更新（出荷確定・同梱・分割・一括更新）の実行を確認する |
| `--wait` | `wait_flag=1` を送り、混雑時に `003002` で失敗せずサーバー側で待つ |
| `--no-validate` | 送信前の検証を行わない |
| `--profile` | 設定プロファイル（複数のアプリ・企業の切り替え） |
| `--timeout` | 1 リクエストのタイムアウト（既定 5 分） |

## 出力の契約

成功時は標準出力に JSON、終了コード 0 です。API の応答からトークンを除き、`"ok": true` を足したものです。

```json
{"ok":true,"result":"success","count":"1","data":[{"goods_id":"A001","goods_name":"りんご"}]}
```

失敗時は標準エラーに JSON、終了コードは 0 以外です。

```json
{"ok":false,"error":{"type":"api_error","code":"003002","message":"…","hint":"…","retryable":"yes"}}
```

| 終了コード | 意味 |
| --- | --- |
| 1 | API が `result=error` を返した |
| 2 | 使い方の誤り・検証エラー・確認（`--yes`）が必要 |
| 3 | 未ログイン、または再ログインが必要（`result=redirect`） |
| 4 | 通信エラー、想定外の HTTP 応答 |

## 設定と環境変数

設定は OS の設定ディレクトリ配下（Linux では `~/.config/ne-cli/config.json`）に、権限 `0600` で保存します。トークンとクライアントシークレットは平文で入るので、このファイルは共有しないでください。

| 環境変数 | 内容 |
| --- | --- |
| `NE_CLIENT_ID` / `NE_CLIENT_SECRET` / `NE_REDIRECT_URI` | アプリの認証情報 |
| `NE_ACCESS_TOKEN` / `NE_REFRESH_TOKEN` | トークン。更新されると新しい組を設定ファイルに保存し、以後はそちらを使います |
| `NE_PROFILE` | プロファイル名 |
| `NE_CLI_CONFIG_DIR` | 設定ディレクトリ |
| `NE_API_HOST` / `NE_BASE_HOST` | API ホスト・認証ホストの上書き |

## AI エージェントから使う

`skills/` に Agent Skills があります。

```bash
npx skills add shopload/ne-cli -y -g
```

| スキル | 内容 |
| --- | --- |
| `ne-shared` | 認証、出力の契約、検索条件のルール、安全上の決まり（他のスキルの前提） |
| `ne-receiveorder` | 受注の検索・集計・更新・出荷確定 |
| `ne-master` | 商品・在庫・店舗・アップロード |

このリポジトリで作業するエージェント向けの案内は [AGENTS.md](AGENTS.md) にあります。

## 開発

```bash
make test    # go vet + go test（API はテスト用サーバーで代用）
make lint    # gofmt
make meta    # 公式の openapi.yaml から internal/registry/meta.json を再生成（PyYAML が必要）
```

API が追加・変更されたら `make meta` を実行します。新しいエンドポイントは、コードを書かなくてもコマンドと `schema` に現れます。カタログに無いパスも `ne-cli api <path>` で呼べます。

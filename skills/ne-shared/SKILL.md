---
name: ne-shared
description: ネクストエンジン (Next Engine) API を ne-cli で操作するときの共通ルール。認証、出力とエラーの読み方、検索条件の書き方、更新時の安全上の決まり。受注・商品・在庫・店舗などネクストエンジンのデータを扱う作業では、最初にこれを読む。
---

# ne-cli 共通ルール

`ne-cli` はネクストエンジン API の全エンドポイントを呼べる CLI です。推測でパラメータを書かず、`ne-cli schema` で確かめてから呼びます。

## 1. 最初に確認すること

```bash
ne-cli auth status          # logged_in が true か
```

- `logged_in: false`、または終了コード 3 のとき: `ne-cli auth login --no-wait` を実行し、出力の `url` をユーザーに開いてもらいます。ログイン後にリダイレクトされた URL を受け取り、`ne-cli auth login --callback-url '<URL>'` を実行します（`state` は 5 分で切れます）。
- `configured: false` のとき: ユーザーに `ne-cli config init` を実行してもらいます。クライアントシークレットやトークンをチャットに貼らせてはいけません。
- トークンは CLI が保存・更新します。出力には含まれません。設定ファイルを読んでトークンを取り出してはいけません。

## 2. 仕様の引き方（API を呼ばない）

```bash
ne-cli schema --search 受注                         # エンドポイントを探す
ne-cli schema receiveorder.base.search --grep date  # 使える項目名・型・説明
ne-cli schema receiveorder.base.update              # 更新できる項目 (data_fields)
ne-cli schema errors 004002                         # メッセージコードの意味
```

コマンドは `ne-cli <service> <resource> <method>` で、パス `/api_v1_<service>_<resource>/<method>` と対応します。`schema` の `command` にそのまま書いてあります。

## 3. 出力の読み方

- 成功: 標準出力に `{"ok":true, ...}`、終了コード 0。検索結果は `data`（配列）と `count`。
- 失敗: 標準エラーに `{"ok":false,"error":{"type","code","message","hint","retryable"}}`。`ok` で判定します。

| 終了コード | 意味 | 対処 |
| --- | --- | --- |
| 1 | API エラー | `error.hint` と `ne-cli schema errors <code>` を見る。`retryable: yes` なら時間を置いて再実行 |
| 2 | 使い方・検証エラー、`--yes` が必要 | `error.details` に項目ごとの原因と候補がある |
| 3 | 未ログイン・再ログインが必要 | 上の手順でログインし直す |
| 4 | 通信エラー | 再実行 |

- 数値も文字列で返ります（例: `"10000.00"`）。計算する前に数値に変換します。
- 値の無い項目は `null`、日時は `"0000-00-00 00:00:00"` のことがあります。
- 大量に取るときは `--fields` を必要な項目だけにし、`--format ndjson` か `csv` を使います。

## 4. 検索条件のルール

条件は `-p <項目名>-<演算子>=<値>` で渡します。`fields` は必須です。

```bash
ne-cli master goods search --fields goods_id,goods_name -p 'goods_id-like=A%' --limit 50
```

| 演算子 | 使える型 |
| --- | --- |
| `-eq` `-neq` | すべて |
| `-lt` `-lte` `-gt` `-gte` | 数値型・日時型 |
| `-in` `-nin`（カンマ区切り、最大 1000 個） | 文字列型・数値型 |
| `-like` `-nlike` `-nlikeornull` | 文字列型 |
| `-blank` `-nblank` `-null` `-nnull`（値は不要） | すべて |

1. **条件はすべて AND です。OR は書けません。** 同じ項目と演算子の組は 1 回しか送れないので、複数の値は `-in` に並べます。
2. **区分コードは数字に見えても文字列型です。** 範囲演算子は使えません（`004002`）。`receive_order_order_status_id-in=0,1,2,20` のように並べます。
3. **否定の演算子（`-neq` `-nin` `-nlike`）は、値が NULL のレコードを落とします。** 未設定も残すなら `-nlikeornull` を使うか、条件を付けずに取ってから除きます。
4. **存在しないコードで絞っても、エラーにならず 0 件が返ります。** コードは区分情報（`ne-cli system <種類> info`）で確かめます。支払区分・発送方法区分などは企業ごとに値が違います。
5. **項目同士の比較はできません。** 2 つの項目を取得してから比べます。
6. 通知（webhook）はありません。変化は検索を定期的に呼んで見つけます。

件数だけ欲しいときは `search` ではなく `count` を使います。全件が必要なときは `--page-all` を付けます。出力の `has_more` が `true` なら、`--page-limit` を上げるか `next_offset` から続きを取ります。

## 5. 更新するときの決まり

- 更新・登録・アップロードは、まず `--dry-run` を付けて送信内容を確かめます。
- `risk` が `high-risk-write` のコマンド（出荷確定 `shipped`、同梱 `bundle`、分割 `divide`、一括更新 `bulkupdate` / `bulkupdatereceipted`）は `--yes` が無いと実行されません。**`--yes` を付ける前に、対象と内容をユーザーに示して了承を得ます。** 出荷確定は API で取り消せません。
- 更新 API は対象の最終更新日（楽観ロック）を要求します。直前に検索して得た値を使います。
- アップロード API は `que_id` を返すだけで、処理は非同期です。`ne-cli system +que-wait --que-id <id>` で結果を確かめます。**同じ内容を送り直すと二重に反映されます。**
- 混雑時のエラー `003002` は、`--wait` を付けるとサーバー側で待ってから実行されます。

## 6. カタログに無い API

```bash
ne-cli api /api_v1_xxx_yyy/search -p fields=... -p ...
```

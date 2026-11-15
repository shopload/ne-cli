---
name: ne-master
description: ネクストエンジンの商品マスタ・在庫・拠点在庫・店舗・仕入先・発注/仕入伝票を ne-cli で検索し、CSV アップロードで商品や在庫を更新する。「在庫数を調べる」「商品を検索」「在庫を更新」「店舗一覧」「アップロードの結果確認」などの依頼で使う。先に ne-shared を読むこと。
---

# マスタ・在庫 (master / warehouse / system)

前提: `ne-shared` の共通ルール。

## 主なコマンド

| やりたいこと | コマンド |
| --- | --- |
| 在庫数を商品コードで引く | `ne-cli master +stock --goods-ids A001,A002` |
| 商品マスタを検索 | `ne-cli master goods search` |
| 在庫マスタを検索 | `ne-cli master stock search` |
| 拠点ごとの在庫 | `ne-cli warehouse stock search` |
| セット商品・セット在庫 | `ne-cli master setgoods search` / `ne-cli master setstock search` |
| 在庫の入出庫履歴 | `ne-cli master stockiohistory search` |
| 店舗・モール | `ne-cli master shop search` / `ne-cli system mall search` |
| 仕入先・卸先 | `ne-cli master supplier search` / `ne-cli master wholesale search` |
| 発注伝票・仕入伝票 | `ne-cli placeorder base search` / `ne-cli supplyorder base search` |
| 区分コード（商品区分など） | `ne-cli system goodstype info` ほか（`ne-cli schema --service system`） |
| アップロードの結果を待つ | `ne-cli system +que-wait --que-id <id>` |

項目名はリソースごとに接頭辞が違います（商品は `goods_`、在庫は `stock_`、店舗は `shop_`）。`ne-cli schema <id> --grep <語>` で確かめます。

## 検索の例

```bash
# 商品名で探す
ne-cli master goods search --fields goods_id,goods_name,goods_selling_price -p 'goods_name-like=%りんご%' --limit 50

# フリー在庫が 5 以下の商品
ne-cli master stock search --fields stock_goods_id,stock_quantity,stock_allocation_quantity,stock_free_quantity \
  -p stock_free_quantity-lte=5 --page-all --format csv

# 全商品を取り出す
ne-cli master goods search --fields goods_id,goods_name,goods_jan_code --page-all --format ndjson
```

- 在庫の数量: `stock_quantity` 在庫数、`stock_allocation_quantity` 引当数、`stock_free_quantity` フリー在庫数（在庫数 − 引当数）、`stock_defective_quantity` 不良在庫数、`stock_remaining_order_quantity` 発注残数。
- 商品区分 `goods_type_id` などの区分コードは文字列型です。存在しないコードで絞ると 0 件が返るだけなので、`ne-cli system goodstype info` で値を確かめます。
- 商品分類タグ `goods_tag` は `[タグA][タグB]` の形の文字列で、空のこともあります。

## アップロード（商品・在庫の登録と更新）

商品マスタ・セット商品・拠点在庫・受注は、CSV をアップロードして登録・更新します。処理は非同期です。

```bash
ne-cli master goods upload --data-type csv --data @goods.csv --dry-run   # 内容を確認
ne-cli master goods upload --data-type csv --data @goods.csv             # que_id が返る
ne-cli system +que-wait --que-id 189                                     # 成功 / 失敗を待つ
```

- `--data @ファイル` でファイルの内容を送ります。CSV の列はメイン機能の仕様と同じです（`ne-cli schema master.goods.upload` と公式ドキュメントを参照）。
- `+que-wait` は、成功（`que_status_id` が `2`）で終了コード 0、失敗（`-1`）で終了コード 1 と `que_message` を返します。
- **結果を確かめずに同じ内容を送り直さない。** 二重に反映されます。

### 在庫を更新するとき

- **在庫数は差分でしか送れません。** 「N 個にする」ときは、`ne-cli master +stock` で現在の在庫数を読み、差分を計算して送ります。
- 減らす量はフリー在庫数 `stock_free_quantity` 以下にします。引当の無い商品で在庫数を超えて減らすと 0 で止まり、フリー在庫数が 0 の商品は減らしても出庫されません。
- 送る前に、対象の商品と増減の数をユーザーに示して了承を得ます。

## 店舗

```bash
ne-cli master shop search --fields shop_id,shop_name,shop_mall_id,shop_deleted_flag
ne-cli master shop checkconnect --shop-id 3
```

店舗の作成・更新（`master shop create` / `update`）は、`data` に XML を渡します。書ける項目は `ne-cli schema master.shop.create` の `data_fields` にあります。

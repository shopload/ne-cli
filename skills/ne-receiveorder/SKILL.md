---
name: ne-receiveorder
description: ネクストエンジンの受注（受注伝票・受注明細）を ne-cli で検索・集計・更新・出荷確定する。「未出荷の受注」「今日の売上」「受注にタグを付ける」「キャンセル以外の受注」「店舗別・モール別の集計」などの依頼で使う。先に ne-shared を読むこと。
---

# 受注 (receiveorder)

前提: `ne-shared` の共通ルール。

## 主なコマンド

| やりたいこと | コマンド |
| --- | --- |
| 受注を一覧する（キャンセル除外、明細つき） | `ne-cli receiveorder +list` |
| 受注伝票を条件で検索 / 件数 | `ne-cli receiveorder base search` / `count` |
| 受注明細を検索 | `ne-cli receiveorder row search` |
| 受注を 1 件更新 | `ne-cli receiveorder +update` |
| 納品書印刷済みにする | `ne-cli receiveorder base receipted` |
| 出荷確定 | `ne-cli receiveorder base shipped --yes` |
| 受注状態・キャンセル区分のコード | `ne-cli system orderstatus info` / `ne-cli system canceltype info` |

## 一覧と検索

```bash
# 納品書印刷待ち (20) の有効な受注
ne-cli receiveorder +list --status 20 --format table

# 期間と店舗で絞り、明細を各受注の rows に付ける
ne-cli receiveorder +list --from 2026-11-01 --to 2026-11-15 --shop-ids 3,7 --with-rows

# 条件を足す / 項目を選ぶ
ne-cli receiveorder +list -p 'receive_order_gruoping_tag-nlikeornull=%[処理済]%' \
  --fields receive_order_id,receive_order_date,receive_order_total_amount
```

受注状態のコード: `0` 取込情報不足、`1` 受注メール取込済、`2` 起票済(CSV/手入力)、`20` 納品書印刷待ち、`30` 納品書印刷中、`40` 納品書印刷済、`50` 出荷確定済（完了）。

### 間違えやすい点

1. **キャンセルは受注状態とは別の項目です。** 有効な受注は `receive_order_cancel_type_id-eq=0` だけです。キャンセルしても受注状態は変わらないので、状態だけで「未出荷」を絞るとキャンセル済みが混ざります。`+list` は既定でこの条件を付けます。「キャンセルだけ」は `-neq=0` です。
2. **受注伝票の条件は、受注明細の検索には掛かりません。** 伝票を先に検索して `receive_order_id` を集め、明細を `receive_order_row_receive_order_id-in` で絞ります（`+list --with-rows` がこれを行います）。商品コードなど商品の情報は明細にしかありません。
3. **モールを直接指定する条件はありません。** 店舗マスタで対象モールの店舗を集めてから、`receive_order_shop_id-in` で絞ります。

   ```bash
   ne-cli master shop search --fields shop_id,shop_name,shop_mall_id
   ne-cli system mall search --fields mall_id,mall_name
   ```

   店舗名 `shop_name` は個々の店舗、モール名 `mall_name` は販売プラットフォームの名前です。1 つのモールに複数の店舗があります。
4. **商品分類タグ `goods_tag` は商品マスタの項目です。** 受注分類タグ `receive_order_gruoping_tag`（綴りはこのとおり）とは別物です。商品分類タグで絞るときは、明細の `receive_order_row_goods_id` と商品マスタの `goods_id` を突き合わせます。
5. **「印刷待ち」と「印刷日待ち」はどちらも状態 `20` です。** 納品書印刷指示日 `receive_order_statement_delivery_instruct_printing_date` が今日以前（`-lte`）なら印刷待ち、未来（`-gt`）なら印刷日待ちです。未設定は `"0000-00-00 00:00:00"` で返り、`-lte` に当たります。

## 集計

- **売上**: 単価 `receive_order_row_unit_price` は 1 個あたりです。明細単位なら小計 `receive_order_row_sub_total_price`、伝票単位なら商品計 `receive_order_goods_amount` か総合計 `receive_order_total_amount` を合計します。
- **明細と結合した後に、伝票の金額を合計しない。** 明細の行数だけ重複します。商品の条件が入る集計は明細の小計を合計します。
- **受注件数**は明細の行数ではありません。`receive_order_row_receive_order_id` の重複を除くか、条件が伝票だけで書けるなら `ne-cli receiveorder base count` を使います。
- 金額は文字列で返るので、数値に変換してから合計します。

## 更新

```bash
# 送信内容を確認
ne-cli receiveorder +update --id 123 --set 'receive_order_gruoping_tag=[処理済]' --dry-run
# 実行（最終更新日は自動で取得）
ne-cli receiveorder +update --id 123 --set 'receive_order_gruoping_tag=[処理済]'
# 明細を更新
ne-cli receiveorder +update --id 123 --row-set 1:receive_order_row_cancel_flag=1 --recalculate
```

更新できる項目は `ne-cli schema receiveorder.base.update` の `data_fields` にあります。受注状態によって更新できない項目があります。

- **作業用欄 `receive_order_worker_text`・備考 `receive_order_note` などは、送った値で丸ごと置き換わります。** 追記したいときは、直前に現在の値を取得し、後ろに足した値を送ります。
- **確認チェック `receive_order_confirm_check_id` は、読む値と書く値の体系が違います。** 検索では `2` が確認済みですが、書くときは `1` でチェックを付け、`0` で外します。
- **`--allow-shipped`（`receive_order_shipped_update_flag=1`）は出荷確定の操作ではありません。** 出荷確定済みの受注でも更新を許すだけです。
- 最終更新日が一致しないエラーは、他で受注が更新されたためです。検索し直してから再実行します。

複数の受注をまとめて更新するときは `ne-cli receiveorder base bulkupdate`（`--yes` が必要）を使います。

## 状態を進める操作

| 操作 | コマンド | 実行後の値 |
| --- | --- | --- |
| 納品書印刷済み | `receiveorder base receipted` | 受注状態 `40` |
| 配送情報出力済み | `receiveorder base labelprinted` | ラベル発行フラグ `2` |
| 出荷確定 | `receiveorder base shipped --yes` | 受注状態 `50`（取り消せない） |
| 同梱 | `receiveorder base bundle --yes` | キャンセル区分 `3` |
| 分割 | `receiveorder base divide --yes` | 状態は変わらない |

```bash
ne-cli receiveorder base search --fields receive_order_id,receive_order_last_modified_date \
  -p receive_order_order_status_id-eq=40 -p receive_order_cancel_type_id-eq=0
ne-cli receiveorder base shipped --receive-order-id 123 \
  --receive-order-last-modified-date '2026-11-15 10:00:00' --dry-run
```

- 出荷確定の対象は、有効な納品書印刷済み（状態 `40`）の受注です。
- **定期実行するときは、取得条件から更新後の値を外します。** そうしないと、処理済みの受注を毎回拾って失敗します。
- 実行前に対象の受注と件数をユーザーに示し、了承を得てから `--yes` を付けます。

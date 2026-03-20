from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from akshare_docgen import parse_source_text, preprocess_source_text


SAMPLE_SOURCE = """## [AKShare](https://github.com/akfamily/akshare) 股票数据

### A股

#### 股票市场总貌

##### 上海证券交易所

接口: stock_sse_summary

目标地址: http://www.sse.com.cn/market/stockdata/statistic/

描述: 上海证券交易所-股票数据总貌

限量: 单次返回最近交易日的股票数据总貌

输入参数

| 名称  | 类型  | 描述  |
|-----|-----|-----|
| -   | -   | -   |

输出参数-实时行情数据

| 名称  | 类型     | 描述  |
|-----|--------|-----|
| 项目  | object | -   |
| 股票  | object | -   |

接口示例

```python
import akshare as ak

stock_sse_summary_df = ak.stock_sse_summary()
print(stock_sse_summary_df)
```

数据示例

```
      项目     股票
0   流通股本   40403.47
```
"""


class ParseSourceTextTest(unittest.TestCase):
    def test_extracts_interface_metadata(self) -> None:
        records = parse_source_text(
            SAMPLE_SOURCE,
            page_title="AKShare 股票数据",
            category_slug="stock",
            page_url="https://akshare.akfamily.xyz/data/stock/stock.html",
            source_url="https://akshare.akfamily.xyz/_sources/data/stock/stock.md.txt",
        )

        self.assertEqual(len(records), 1)

        record = records[0]
        self.assertEqual(record["interface"], "stock_sse_summary")
        self.assertEqual(record["description"], "上海证券交易所-股票数据总貌")
        self.assertEqual(record["target_url"], "http://www.sse.com.cn/market/stockdata/statistic/")
        self.assertEqual(record["limit"], "单次返回最近交易日的股票数据总貌")
        self.assertEqual(record["page_title"], "AKShare 股票数据")
        self.assertEqual(record["category_slug"], "stock")
        self.assertEqual(
            record["headings"],
            ["AKShare 股票数据", "A股", "股票市场总貌", "上海证券交易所"],
        )
        self.assertEqual(record["input_params"][0]["name"], "-")
        self.assertEqual(record["output_groups"][0]["section"], "输出参数-实时行情数据")
        self.assertEqual(record["output_groups"][0]["fields"][0]["name"], "项目")
        self.assertIn("stock_sse_summary_df", record["example_code"])
        self.assertIn("流通股本", record["sample_data"])

    def test_resets_heading_path_when_same_level_heading_changes(self) -> None:
        source = """## 总览

### A类

#### 子项一

接口: api_one

描述: first

### B类

#### 子项二

接口: api_two

描述: second
"""
        records = parse_source_text(
            source,
            page_title="总览",
            category_slug="demo",
            page_url="https://example.com/page.html",
            source_url="https://example.com/source.md.txt",
        )

        self.assertEqual(records[0]["headings"], ["总览", "A类", "子项一"])
        self.assertEqual(records[1]["headings"], ["总览", "B类", "子项二"])

    def test_supports_qhkc_heading_style(self) -> None:
        source = """# 商品

## 合约持仓数据

### 接口名称

variety_positions

### 接口描述

合约持仓数据接口

### 请求参数

| 参数名 | 说明 | 举例 |
|---|---|---|
| code | 合约代号 | rb1810 |

### 返回参数

| 参数名 | 类型 | 说明 |
|---|---|---|
| broker | string | 席位 |

### 示例代码

```python
print("demo")
```
"""
        records = parse_source_text(
            preprocess_source_text(source, "qhkc"),
            page_title="AKShare 奇货可查",
            category_slug="qhkc",
            page_url="https://example.com/qhkc/commodity.html",
            source_url="https://example.com/qhkc/commodity.md.txt",
        )

        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["interface"], "variety_positions")
        self.assertEqual(records[0]["description"], "合约持仓数据接口")
        self.assertEqual(records[0]["input_params"][0]["name"], "code")
        self.assertIn("Example: rb1810", records[0]["input_params"][0]["description"])
        self.assertEqual(records[0]["output_groups"][0]["fields"][0]["name"], "broker")


if __name__ == "__main__":
    unittest.main()

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from akshare_focus_codegen import (
    extract_doc_sample_examples,
    extract_example_params,
    go_struct_field_name,
    infer_value_kind,
    render_go_catalog,
)


class AkshareFocusCodegenTest(unittest.TestCase):
    def test_extract_example_params_from_example_code(self) -> None:
        record = {
            "interface": "spot_hist_sge",
            "input_params": [{"name": "symbol", "description": ""}],
            "example_code": 'import akshare as ak\nspot_hist_sge_df = ak.spot_hist_sge(symbol="Au99.99")\nprint(spot_hist_sge_df)',
        }

        self.assertEqual(extract_example_params(record), {"symbol": "Au99.99"})

    def test_go_struct_field_name_supports_chinese_and_dedupes(self) -> None:
        used: set[str] = set()

        self.assertEqual(go_struct_field_name("日期", used, 1), "X日期")
        self.assertEqual(go_struct_field_name("日期", used, 2), "X日期2")
        self.assertEqual(go_struct_field_name("trade_date", used, 3), "TradeDate")

    def test_extract_doc_sample_examples_from_dataframe_text(self) -> None:
        record = {
            "sample_data": "          日期    开盘    收盘\n0  2024-03-19  575.0  578.0\n1  2024-03-20  578.0  579.0\n",
            "output_groups": [
                {
                    "fields": [
                        {"name": "日期", "type": "string", "description": ""},
                        {"name": "开盘", "type": "number", "description": ""},
                        {"name": "收盘", "type": "number", "description": ""},
                    ]
                }
            ],
        }

        self.assertEqual(
            extract_doc_sample_examples(record),
            {"日期": "2024-03-19", "开盘": "575.0", "收盘": "578.0"},
        )

    def test_infer_value_kind_prefers_runtime_shape(self) -> None:
        self.assertEqual(infer_value_kind("number", "40.120"), "string")
        self.assertEqual(infer_value_kind("object", 262.45), "number")
        self.assertEqual(infer_value_kind("object", True), "boolean")

    def test_render_go_catalog_generates_typed_row_struct_without_alias_method(self) -> None:
        record = {
            "interface": "spot_hist_sge",
            "page_title": "AKShare 现货数据",
            "category_slug": "spot",
            "page_url": "https://akshare.akfamily.xyz/data/spot/spot.html",
            "source_url": "https://akshare.akfamily.xyz/_sources/data/spot/spot.md.txt",
            "headings": ["AKShare 现货数据", "历史行情数据"],
            "target_url": "https://www.sge.com.cn/sjzx/mrhq",
            "description": "上海黄金交易所-历史数据",
            "input_params": [{"name": "symbol", "type": "string", "description": "品种"}],
            "output_groups": [
                {
                    "fields": [
                        {"name": "date", "type": "object", "description": "-"},
                        {"name": "open", "type": "float64", "description": "-"},
                        {"name": "close", "type": "float64", "description": "-"},
                    ]
                }
            ],
            "sample_data": "         date   open  close\n0  2024-03-19  575.0  578.0\n",
            "focus_tags": ["gold", "info"],
        }

        rendered = render_go_catalog([record])

        self.assertIn("type SpotHistSgeRow struct {", rendered)
        self.assertIn('\tDate string `json:"date"`', rendered)
        self.assertIn('\tOpen float64 `json:"open"`', rendered)
        self.assertNotIn("func (c *Client) SpotHistSgeRows(", rendered)

    def test_render_go_catalog_generates_any_for_unknown_fields(self) -> None:
        record = {
            "interface": "stock_individual_info_em",
            "page_title": "AKShare 股票数据",
            "category_slug": "stock",
            "page_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
            "source_url": "https://akshare.akfamily.xyz/_sources/data/stock/stock.md.txt",
            "headings": ["AKShare 股票数据", "个股信息查询-东财"],
            "target_url": "http://quote.eastmoney.com/",
            "description": "东方财富-个股-股票信息",
            "input_params": [{"name": "symbol", "type": "string", "description": "股票代码"}],
            "output_groups": [
                {
                    "fields": [
                        {"name": "item", "type": "object", "description": "-"},
                        {"name": "value", "type": "object", "description": "-"},
                    ]
                }
            ],
            "sample_data": "",
            "focus_tags": ["stock"],
        }

        rendered = render_go_catalog([record])

        self.assertIn("type StockIndividualInfoEmRow struct {", rendered)
        self.assertIn('\tItem string `json:"item"`', rendered)
        self.assertIn('\tValue any `json:"value"`', rendered)

    def test_render_go_catalog_supports_chinese_row_field_names(self) -> None:
        record = {
            "interface": "spot_quotations_sge",
            "page_title": "AKShare 现货数据",
            "category_slug": "spot",
            "page_url": "https://akshare.akfamily.xyz/data/spot/spot.html",
            "source_url": "https://akshare.akfamily.xyz/_sources/data/spot/spot.md.txt",
            "headings": ["AKShare 现货数据", "实时行情数据"],
            "target_url": "https://www.sge.com.cn/",
            "description": "上海黄金交易所-实时数据",
            "input_params": [],
            "output_groups": [
                {
                    "fields": [
                        {"name": "品种", "type": "object", "description": "-"},
                        {"name": "时间", "type": "object", "description": "-"},
                        {"name": "现价", "type": "float64", "description": "-"},
                    ]
                }
            ],
            "sample_data": "   品种      时间     现价\n0  Au99.99  09:31:00  578.32\n",
            "focus_tags": ["gold", "info"],
        }

        rendered = render_go_catalog([record])

        self.assertIn("type SpotQuotationsSgeRow struct {", rendered)
        self.assertIn('\tX品种 string `json:"品种"`', rendered)
        self.assertIn('\tX时间 string `json:"时间"`', rendered)
        self.assertIn('\tX现价 float64 `json:"现价"`', rendered)

    def test_render_go_catalog_preserves_string_minute_fields_for_known_endpoint(self) -> None:
        record = {
            "interface": "stock_zh_a_minute",
            "page_title": "AKShare 股票数据",
            "category_slug": "stock",
            "page_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
            "source_url": "https://akshare.akfamily.xyz/_sources/data/stock/stock.md.txt",
            "headings": ["AKShare 股票数据", "分时数据-新浪"],
            "target_url": "http://finance.sina.com.cn/",
            "description": "新浪财经分时数据",
            "input_params": [{"name": "symbol", "type": "string", "description": "股票代码"}],
            "output_groups": [
                {
                    "fields": [
                        {"name": "day", "type": "object", "description": "-"},
                        {"name": "open", "type": "number", "description": "-"},
                        {"name": "close", "type": "number", "description": "-"},
                        {"name": "volume", "type": "number", "description": "-"},
                    ]
                }
            ],
            "sample_data": "                   day   open  close  volume\n0  2026-03-20 09:31:00  10.10  10.28  12345\n",
            "focus_tags": ["stock"],
        }

        rendered = render_go_catalog([record])

        self.assertIn('\tDay string `json:"day"`', rendered)
        self.assertIn('\tOpen string `json:"open"`', rendered)
        self.assertIn('\tClose string `json:"close"`', rendered)
        self.assertIn('\tVolume string `json:"volume"`', rendered)

    def test_render_go_catalog_uses_any_for_known_mixed_value_endpoint(self) -> None:
        record = {
            "interface": "stock_individual_info_em",
            "page_title": "AKShare 股票数据",
            "category_slug": "stock",
            "page_url": "https://akshare.akfamily.xyz/data/stock/stock.html",
            "source_url": "https://akshare.akfamily.xyz/_sources/data/stock/stock.md.txt",
            "headings": ["AKShare 股票数据", "个股信息查询-东财"],
            "target_url": "http://quote.eastmoney.com/",
            "description": "东方财富-个股-股票信息",
            "input_params": [{"name": "symbol", "type": "string", "description": "股票代码"}],
            "output_groups": [
                {
                    "fields": [
                        {"name": "item", "type": "object", "description": "-"},
                        {"name": "value", "type": "object", "description": "-"},
                    ]
                }
            ],
            "sample_data": "   item   value\n0  总市值  123.4\n",
            "focus_tags": ["stock"],
        }

        rendered = render_go_catalog([record])

        self.assertIn('\tValue any `json:"value"`', rendered)

    def test_render_go_catalog_dedupes_duplicate_interfaces(self) -> None:
        record = {
            "interface": "inventory",
            "page_title": "AKShare 示例",
            "category_slug": "qhkc",
            "page_url": "https://example.com/a",
            "source_url": "https://example.com/a.txt",
            "headings": ["AKShare 示例", "库存"],
            "target_url": "https://example.com/target",
            "description": "first",
            "input_params": [],
            "output_groups": [{"fields": [{"name": "日期", "type": "string", "description": "-"}]}],
            "sample_data": "",
            "focus_tags": ["futures"],
        }
        duplicate = dict(record)
        duplicate["description"] = "second"

        rendered = render_go_catalog([record, duplicate])

        self.assertEqual(rendered.count("var EndpointInventory = Endpoint{"), 1)
        self.assertEqual(rendered.count("type InventoryRow struct {"), 1)


if __name__ == "__main__":
    unittest.main()

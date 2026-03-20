# AkShare Focus Catalog

- Generated at: `2026-03-20T09:24:46.498163+00:00`
- Source catalog: `docs/akshare/generated/akshare_api_catalog.json`
- Selected endpoints: `620`

## Domain Summary

| Domain | Endpoint Count |
|---|---:|
| futures | 121 |
| gold | 13 |
| info | 163 |
| stock | 384 |

## Endpoints

| API | Tags | Category | Deepest Heading |
|---|---|---|---|
| `bond_corporate_issue_cninfo` | info | bond | 企业债发行 |
| `bond_cov_issue_cninfo` | info | bond | 可转债发行 |
| `bond_cov_stock_issue_cninfo` | info | bond | 可转债转股 |
| `bond_local_government_issue_cninfo` | info | bond | 地方债发行 |
| `bond_treasure_issue_cninfo` | info | bond | 国债发行 |
| `crypto_bitcoin_cme` | info | dc | CME-成交量报告 |
| `crypto_bitcoin_hold_report` | info | dc | 比特币持仓报告 |
| `amac_futures_info` | futures | fund | 期货公司集合资管产品公示 |
| `fund_announcement_dividend_em` | info | fund | 分红配送 |
| `fund_announcement_personnel_em` | info | fund | 人事公告 |
| `fund_announcement_report_em` | info | fund | 定期报告 |
| `fund_open_fund_info_em` | info | fund | 开放式基金-历史数据 |
| `fund_report_asset_allocation_cninfo` | info | fund | 基金资产配置 |
| `fund_report_industry_allocation_cninfo` | info | fund | 基金行业配置 |
| `fund_report_stock_cninfo` | info | fund | 基金重仓股 |
| `futures_comex_inventory` | futures | futures | COMEX 库存数据 |
| `futures_comm_info` | futures | futures | 期货手续费与保证金 |
| `futures_comm_js` | futures | futures | 金十数据 |
| `futures_contract_detail` | futures | futures | 期货合约详情-新浪 |
| `futures_contract_detail_em` | futures | futures | 期货合约详情-东财 |
| `futures_contract_info_cffex` | futures | futures | 中国金融期货交易所 |
| `futures_contract_info_czce` | futures | futures | 郑州商品交易所 |
| `futures_contract_info_dce` | futures | futures | 大连商品交易所 |
| `futures_contract_info_gfex` | futures | futures | 广州期货交易所 |
| `futures_contract_info_ine` | futures | futures | 上海国际能源交易中心 |
| `futures_contract_info_shfe` | futures | futures | 上海期货交易所 |
| `futures_dce_position_rank` | futures | futures | 大连商品交易所 |
| `futures_delivery_czce` | futures | futures | 交割统计-郑商所 |
| `futures_delivery_dce` | futures | futures | 交割统计-大商所 |
| `futures_delivery_match_czce` | futures | futures | 交割配对-郑商所 |
| `futures_delivery_match_dce` | futures | futures | 交割配对-大商所 |
| `futures_delivery_shfe` | futures | futures | 交割统计-上期所 |
| `futures_fees_info` | futures | futures | 期货交易费用参照表 |
| `futures_foreign_commodity_realtime` | futures, gold | futures | 外盘-实时行情数据 |
| `futures_foreign_detail` | futures | futures | 外盘-合约详情 |
| `futures_foreign_hist` | futures | futures | 外盘-历史行情数据-新浪 |
| `futures_gfex_position_rank` | futures | futures | 广州期货交易所 |
| `futures_gfex_warehouse_receipt` | futures | futures | 仓单日报-广州期货交易所 |
| `futures_global_hist_em` | futures | futures | 外盘-历史行情数据-东财 |
| `futures_global_spot_em` | futures | futures | 外盘-实时行情数据-东财 |
| `futures_hist_em` | futures | futures | 内盘-历史行情数据-东财 |
| `futures_hog_core` | futures | futures | 核心数据 |
| `futures_hog_cost` | futures | futures | 成本维度 |
| `futures_hog_supply` | futures | futures | 供应维度 |
| `futures_hold_pos_sina` | futures | futures | 成交持仓 |
| `futures_hq_subscribe_exchange_symbol` | futures | futures | 外盘-品种代码表 |
| `futures_index_ccidx` | futures | futures | 中证商品指数 |
| `futures_inventory_99` | futures | futures | 库存数据-99期货网 |
| `futures_inventory_em` | futures | futures | 库存数据-东方财富 |
| `futures_main_sina` | futures | futures | 期货连续合约 |
| `futures_news_shmet` | futures, info | futures | 期货资讯 |
| `futures_rule` | futures | futures | 期货规则-交易日历表 |
| `futures_settle` | futures | futures | 内盘-结算参数数据 |
| `futures_settlement_price_sgx` | futures | futures | 新加坡交易所期货 |
| `futures_shfe_warehouse_receipt` | futures | futures | 仓单日报-上海期货交易所 |
| `futures_spot_stock` | futures | futures | 现货与股票 |
| `futures_spot_sys` | futures | futures | 现期图 |
| `futures_stock_shfe_js` | futures | futures | 上海期货交易所 |
| `futures_to_spot_czce` | futures | futures | 期转现-郑商所 |
| `futures_to_spot_dce` | futures | futures | 期转现-大商所 |
| `futures_to_spot_shfe` | futures | futures | 期转现-上期所 |
| `futures_warehouse_receipt_czce` | futures | futures | 仓单日报-郑州商品交易所 |
| `futures_warehouse_receipt_dce` | futures | futures | 仓单日报-大连商品交易所 |
| `futures_zh_daily_sina` | futures | futures | 内盘-历史行情数据-新浪 |
| `futures_zh_minute_sina` | futures | futures | 内盘-分时行情数据 |
| `futures_zh_realtime` | futures | futures | 内盘-实时行情数据(品种) |
| `futures_zh_spot` | futures, gold | futures | 内盘-实时行情数据 |
| `get_futures_daily` | futures | futures | 内盘-历史行情数据-交易所 |
| `index_hog_spot_price` | futures | futures | 生猪市场价格指数 |
| `macro_fx_sentiment` | info | fx | 货币对-投机情绪报告 |
| `index_news_sentiment_scope` | info | index | A 股新闻情绪指数 |
| `index_pmi_com_cx` | info | index | 综合 PMI |
| `index_pmi_man_cx` | info | index | 制造业 PMI |
| `index_pmi_ser_cx` | info | index | 服务业 PMI |
| `stock_hk_index_daily_em` | stock | index | 历史行情数据-东财 |
| `stock_hk_index_daily_sina` | stock | index | 历史行情数据-新浪 |
| `stock_hk_index_spot_em` | stock | index | 实时行情数据-东财 |
| `stock_hk_index_spot_sina` | stock | index | 实时行情数据-新浪 |
| `stock_zh_index_daily` | stock | index | 历史行情数据-新浪 |
| `stock_zh_index_daily_em` | stock | index | 历史行情数据-东方财富 |
| `stock_zh_index_daily_tx` | stock | index | 历史行情数据-腾讯 |
| `stock_zh_index_hist_csindex` | stock | index | 中证指数 |
| `stock_zh_index_spot_em` | stock | index | 实时行情数据-东财 |
| `stock_zh_index_spot_sina` | stock | index | 实时行情数据-新浪 |
| `stock_zh_index_value_csindex` | stock | index | 指数估值-中证 |
| `macro_bank_australia_interest_rate` | info | interest_rate | 澳洲联储决议报告 |
| `macro_bank_brazil_interest_rate` | info | interest_rate | 巴西利率决议报告 |
| `macro_bank_china_interest_rate` | info | interest_rate | 中国央行决议报告 |
| `macro_bank_english_interest_rate` | info | interest_rate | 英国央行决议报告 |
| `macro_bank_euro_interest_rate` | info | interest_rate | 欧洲央行决议报告 |
| `macro_bank_india_interest_rate` | info | interest_rate | 印度利率决议报告 |
| `macro_bank_japan_interest_rate` | info | interest_rate | 日本利率决议报告 |
| `macro_bank_newzealand_interest_rate` | info | interest_rate | 新西兰联储决议报告 |
| `macro_bank_russia_interest_rate` | info | interest_rate | 俄罗斯利率决议报告 |
| `macro_bank_switzerland_interest_rate` | info | interest_rate | 瑞士央行利率决议报告 |
| `macro_bank_usa_interest_rate` | info | interest_rate | 美联储利率决议报告 |
| `macro_china_au_report` | gold, info | macro | 上海黄金交易所报告 |
| `macro_china_bond_public` | info | macro | 新债发行 |
| `macro_china_cpi_monthly` | info | macro | 中国 CPI 月率报告 |
| `macro_china_cpi_yearly` | info | macro | 中国 CPI 年率报告 |
| `macro_china_cx_services_pmi_yearly` | info | macro | 财新服务业PMI |
| `macro_china_exports_yoy` | info | macro | 以美元计算出口年率 |
| `macro_china_foreign_exchange_gold` | gold | macro | 央行黄金和外汇储备 |
| `macro_china_fx_gold` | gold | macro | 外汇和黄金储备 |
| `macro_china_gdp_yearly` | info | macro | 中国 GDP 年率 |
| `macro_china_hk_market_info` | info | macro | 人民币香港银行同业拆息 |
| `macro_china_imports_yoy` | info | macro | 以美元计算进口年率 |
| `macro_china_industrial_production_yoy` | info | macro | 规模以上工业增加值年率 |
| `macro_china_market_margin_sh` | info | macro | 上海融资融券报告 |
| `macro_china_market_margin_sz` | info | macro | 深圳融资融券报告 |
| `macro_china_ppi_yearly` | info | macro | 中国 PPI 年率报告 |
| `macro_china_rmb` | info | macro | 人民币汇率中间价报告 |
| `macro_china_shibor_all` | info | macro | 上海银行业同业拆借报告 |
| `macro_china_trade_balance` | info | macro | 以美元计算贸易帐(亿美元) |
| `macro_cons_gold` | gold, info | macro | 全球最大黄金 ETF—SPDR Gold Trust 持仓报告 |
| `macro_cons_opec_month` | info | macro | 欧佩克报告 |
| `macro_cons_silver` | info | macro | 全球最大白银ETF--iShares Silver Trust持仓报告 |
| `macro_euro_cpi_mom` | info | macro | 欧元区CPI月率报告 |
| `macro_euro_cpi_yoy` | info | macro | 欧元区CPI年率报告 |
| `macro_euro_current_account_mom` | info | macro | 欧元区经常帐报告 |
| `macro_euro_employment_change_qoq` | info | macro | 欧元区季调后就业人数季率报告 |
| `macro_euro_gdp_yoy` | info | macro | 欧元区季度GDP年率报告 |
| `macro_euro_industrial_production_mom` | info | macro | 欧元区工业产出月率报告 |
| `macro_euro_lme_holding` | gold, info | macro | 持仓报告 |
| `macro_euro_lme_stock` | gold, info | macro | 库存报告 |
| `macro_euro_manufacturing_pmi` | info | macro | 欧元区制造业PMI初值报告 |
| `macro_euro_ppi_mom` | info | macro | 欧元区PPI月率报告 |
| `macro_euro_retail_sales_mom` | info | macro | 欧元区零售销售月率报告 |
| `macro_euro_sentix_investor_confidence` | info | macro | 欧元区Sentix投资者信心指数报告 |
| `macro_euro_services_pmi` | info | macro | 欧元区服务业PMI终值报告 |
| `macro_euro_trade_balance` | info | macro | 欧元区未季调贸易帐报告 |
| `macro_euro_unemployment_rate_mom` | info | macro | 欧元区失业率报告 |
| `macro_euro_zew_economic_sentiment` | info | macro | 欧元区ZEW经济景气指数报告 |
| `macro_usa_adp_employment` | info | macro | 美国ADP就业人数报告 |
| `macro_usa_api_crude_stock` | info | macro | 美国 API 原油库存报告 |
| `macro_usa_building_permits` | info | macro | 美国营建许可总数报告 |
| `macro_usa_business_inventories` | info | macro | 美国商业库存月率报告 |
| `macro_usa_cb_consumer_confidence` | info | macro | 美国谘商会消费者信心指数报告 |
| `macro_usa_cftc_c_holding` | futures, info | macro | 商品类非商业持仓报告 |
| `macro_usa_cftc_merchant_currency_holding` | futures, info | macro | 外汇类商业持仓报告 |
| `macro_usa_cftc_merchant_goods_holding` | futures, info | macro | 商品类商业持仓报告 |
| `macro_usa_cftc_nc_holding` | futures, info | macro | 外汇类非商业持仓报告 |
| `macro_usa_core_cpi_monthly` | info | macro | 美国核心CPI月率报告 |
| `macro_usa_core_pce_price` | info | macro | 美国核心PCE物价指数年率报告 |
| `macro_usa_core_ppi` | info | macro | 美国核心生产者物价指数(PPI)报告 |
| `macro_usa_cpi_monthly` | info | macro | 美国CPI月率报告 |
| `macro_usa_cpi_yoy` | info | macro | 美国CPI年率报告 |
| `macro_usa_crude_inner` | info | macro | 美国原油产量报告 |
| `macro_usa_current_account` | info | macro | 美国经常帐报告 |
| `macro_usa_durable_goods_orders` | info | macro | 美国耐用品订单月率报告 |
| `macro_usa_eia_crude_rate` | info | macro | 美国EIA原油库存报告 |
| `macro_usa_exist_home_sales` | info | macro | 美国成屋销售总数年化报告 |
| `macro_usa_export_price` | info | macro | 美国出口价格指数报告 |
| `macro_usa_factory_orders` | info | macro | 美国工厂订单月率报告 |
| `macro_usa_gdp_monthly` | info | macro | 美国GDP |
| `macro_usa_house_price_index` | info | macro | 美国FHFA房价指数月率报告 |
| `macro_usa_house_starts` | info | macro | 美国新屋开工总数年化报告 |
| `macro_usa_import_price` | info | macro | 美国进口物价指数报告 |
| `macro_usa_industrial_production` | info | macro | 美国工业产出月率报告 |
| `macro_usa_initial_jobless` | info | macro | 美国初请失业金人数报告 |
| `macro_usa_ism_non_pmi` | info | macro | 美国ISM非制造业PMI报告 |
| `macro_usa_ism_pmi` | info | macro | 美国ISM制造业PMI报告 |
| `macro_usa_job_cuts` | info | macro | 美国挑战者企业裁员人数报告 |
| `macro_usa_lmci` | info | macro | LMCI |
| `macro_usa_michigan_consumer_sentiment` | info | macro | 美国密歇根大学消费者信心指数初值报告 |
| `macro_usa_nahb_house_market_index` | info | macro | 美国NAHB房产市场指数报告 |
| `macro_usa_new_home_sales` | info | macro | 美国新屋销售总数年化报告 |
| `macro_usa_nfib_small_business` | info | macro | 美国NFIB小型企业信心指数报告 |
| `macro_usa_non_farm` | info | macro | 美国非农就业人数报告 |
| `macro_usa_pending_home_sales` | info | macro | 美国成屋签约销售指数月率报告 |
| `macro_usa_personal_spending` | info | macro | 美国个人支出月率报告 |
| `macro_usa_pmi` | info | macro | 美国Markit制造业PMI初值报告 |
| `macro_usa_ppi` | info | macro | 美国生产者物价指数(PPI)报告 |
| `macro_usa_real_consumer_spending` | info | macro | 美国实际个人消费支出季率初值报告 |
| `macro_usa_retail_sales` | info | macro | 美国零售销售月率报告 |
| `macro_usa_rig_count` | info | macro | 贝克休斯钻井报告 |
| `macro_usa_services_pmi` | info | macro | 美国Markit服务业PMI初值报告 |
| `macro_usa_spcs20` | info | macro | 美国S&P/CS20座大城市房价指数年率报告 |
| `macro_usa_trade_balance` | info | macro | 美国贸易帐报告 |
| `macro_usa_unemployment_rate` | info | macro | 美国失业率报告 |
| `option_current_day_sse` | info | option | 当日合约-上海证券交易所 |
| `option_finance_board` | futures | option | 行情数据 |
| `option_hist_gfex` | futures | option | 广州期货交易所 |
| `option_hist_shfe` | futures | option | 上海期货交易所 |
| `option_lhb_em` | futures | option | 期权龙虎榜-金融期权 |
| `option_margin` | futures | option | 商品期权保证金 |
| `option_vol_gfex` | futures | option | 广州期货交易所-隐含波动参考值 |
| `car_sale_rank_gasgoo` | info | others | 盖世研究院 |
| `news_cctv` | info | others | 新闻联播文字稿 |
| `stock_js_weibo_report` | stock, info | others | 微博舆情报告 |
| `basis` | futures | qhkc | 基差数据 |
| `broker_all` | futures | qhkc | 所有席位数据 |
| `broker_bbr` | futures | qhkc | 席位多空比数据 |
| `broker_calendar` | futures | qhkc | 席位盈亏数据 |
| `broker_flow` | futures | qhkc | 席位每日大资金流动数据 |
| `broker_in_loss_list` | futures | qhkc | 席位亏损排行 |
| `broker_in_profit_list` | futures | qhkc | 席位盈利排行 |
| `broker_net_money` | futures | qhkc | 席位净持仓保证金数据 |
| `broker_net_money_chge` | futures | qhkc | 席位净持仓保证金变化数据 |
| `broker_pk` | futures | qhkc | 席位对对碰 |
| `broker_positions` | futures | qhkc | 席位持仓数据 |
| `broker_positions_process` | futures | qhkc | 建仓过程 |
| `broker_profit` | futures | qhkc | 席位的商品盈亏数据 |
| `broker_total_money` | futures | qhkc | 席位总持仓保证金数据 |
| `commodity_flow_long` | futures | qhkc | 每日净流多列表(商品) |
| `commodity_flow_short` | futures | qhkc | 每日净流空列表(商品) |
| `free_ratio` | futures | qhkc | 自由价比数据 |
| `free_spread` | futures | qhkc | 自由价差数据 |
| `index_info` | futures | qhkc | 指数信息 |
| `index_mine` | futures | qhkc | 个人指数列表 |
| `index_money` | futures | qhkc | 指数沉淀资金数据 |
| `index_official` | futures | qhkc | 公共指数列表 |
| `index_profit` | futures | qhkc | 指数的席位盈亏数据 |
| `index_quotes` | futures | qhkc | 指数行情数据 |
| `index_trend` | futures | qhkc | 指数资金动向 |
| `index_weights` | futures | qhkc | 指数权重数据 |
| `intertemporal_arbitrage` | futures | qhkc | 跨期套利数据 |
| `inventory` | futures | qhkc | 参数类型一 |
| `long_pool` | futures | qhkc | 龙虎牛熊多头合约池 |
| `money_in_out` | futures | qhkc | 每日商品保证金沉淀变化 |
| `profit` | futures | qhkc | 利润数据 |
| `short_pool` | futures | qhkc | 龙虎牛熊空头合约池 |
| `stock_flow_long` | stock, futures | qhkc | 每日净流多列表(指数) |
| `stock_flow_short` | stock, futures | qhkc | 每日净流空列表(指数) |
| `term_structure` | futures | qhkc | 期限结构 |
| `trader_prices` | futures | qhkc | 现货贸易商报价 |
| `variety_all` | futures | qhkc | 商品列表数据 |
| `variety_all_positions` | futures | qhkc | 商品持仓数据 |
| `variety_bbr` | futures | qhkc | 合约多空比数据 |
| `variety_list` | futures | qhkc | 合约索引 |
| `variety_longhu_top` | futures | qhkc | 多头排行 |
| `variety_money` | futures | qhkc | 商品沉淀资金数据 |
| `variety_net_money` | futures | qhkc | 合约净持仓保证金数据 |
| `variety_net_money_chge` | futures | qhkc | 合约净持仓保证金变化数据 |
| `variety_net_positions` | futures | qhkc | 商品净持仓数据 |
| `variety_niuxiong_top` | futures | qhkc | 多头排行 |
| `variety_no_futures` | futures | qhkc | 非期货公司净持仓 |
| `variety_positions` | futures | qhkc | 合约持仓数据 |
| `variety_profit` | futures | qhkc | 商品的席位盈亏数据 |
| `variety_quotes` | futures | qhkc | 合约行情数据 |
| `variety_reports` | futures, info | qhkc | 商品相关研报数据 |
| `variety_strategies` | futures | qhkc | 自研指标数据 |
| `variety_total_money` | futures | qhkc | 合约总持仓保证金数据 |
| `virtual_real` | futures | qhkc | 虚实盘比数据 |
| `warehouse_receipt` | futures | qhkc | 仓单数据 |
| `spot_golden_benchmark_sge` | gold, info | spot | 上海金基准价 |
| `spot_hist_sge` | gold, info | spot | 历史行情数据 |
| `spot_price_qh` | futures | spot | 99 现货走势 |
| `spot_quotations_sge` | gold, info | spot | 实时行情数据 |
| `spot_silver_benchmark_sge` | gold, info | spot | 上海银基准价 |
| `news_report_time_baidu` | stock, info | stock | 财报发行 |
| `news_trade_notify_dividend_baidu` | stock | stock | 分红派息 |
| `news_trade_notify_suspend_baidu` | stock | stock | 停复牌 |
| `stock_a_all_pb` | stock | stock | A 股等权重与中位数市净率 |
| `stock_a_below_net_asset_statistics` | stock | stock | 破净股统计 |
| `stock_a_congestion_lg` | stock | stock | 大盘拥挤度 |
| `stock_a_gxl_lg` | stock | stock | A 股股息率 |
| `stock_a_high_low_statistics` | stock | stock | 创新高和新低的股票数量 |
| `stock_a_ttm_lyr` | stock | stock | A 股等权重与中位数市盈率 |
| `stock_account_statistics_em` | stock | stock | 股票账户统计月度 |
| `stock_add_stock` | stock | stock | 股票增发 |
| `stock_allotment_cninfo` | stock, info | stock | 配股实施方案-巨潮资讯 |
| `stock_analyst_detail_em` | stock, info | stock | 分析师详情 |
| `stock_analyst_rank_em` | stock, info | stock | 分析师指数排行 |
| `stock_balance_sheet_by_report_delisted_em` | stock, info | stock | 资产负债表-按报告期 |
| `stock_balance_sheet_by_report_em` | stock, info | stock | 资产负债表-按报告期 |
| `stock_balance_sheet_by_yearly_em` | stock | stock | 资产负债表-按年度 |
| `stock_bid_ask_em` | stock | stock | 行情报价 |
| `stock_bj_a_spot_em` | stock | stock | 京 A 股 |
| `stock_board_change_em` | stock | stock | 板块异动详情 |
| `stock_board_concept_cons_em` | stock | stock | 东方财富-成份股 |
| `stock_board_concept_hist_em` | stock | stock | 东方财富-指数 |
| `stock_board_concept_hist_min_em` | stock | stock | 东方财富-指数-分时 |
| `stock_board_concept_index_ths` | stock | stock | 同花顺-概念板块指数 |
| `stock_board_concept_info_ths` | stock | stock | 同花顺-概念板块简介 |
| `stock_board_concept_name_em` | stock | stock | 东方财富-概念板块 |
| `stock_board_concept_spot_em` | stock | stock | 东方财富-概念板块-实时行情 |
| `stock_board_industry_cons_em` | stock | stock | 东方财富-成份股 |
| `stock_board_industry_hist_em` | stock | stock | 东方财富-指数-日频 |
| `stock_board_industry_hist_min_em` | stock | stock | 东方财富-指数-分时 |
| `stock_board_industry_index_ths` | stock | stock | 同花顺-指数 |
| `stock_board_industry_name_em` | stock | stock | 东方财富-行业板块 |
| `stock_board_industry_spot_em` | stock | stock | 东方财富-行业板块-实时行情 |
| `stock_board_industry_summary_ths` | stock | stock | 同花顺-同花顺行业一览表 |
| `stock_buffett_index_lg` | stock | stock | 巴菲特指标 |
| `stock_cash_flow_sheet_by_quarterly_em` | stock | stock | 现金流量表-按单季度 |
| `stock_cash_flow_sheet_by_report_delisted_em` | stock, info | stock | 现金流量表-按报告期 |
| `stock_cash_flow_sheet_by_report_em` | stock, info | stock | 现金流量表-按报告期 |
| `stock_cash_flow_sheet_by_yearly_em` | stock | stock | 现金流量表-按年度 |
| `stock_cg_equity_mortgage_cninfo` | stock, info | stock | 股权质押 |
| `stock_cg_guarantee_cninfo` | stock, info | stock | 对外担保 |
| `stock_cg_lawsuit_cninfo` | stock, info | stock | 公司诉讼 |
| `stock_changes_em` | stock | stock | 盘口异动 |
| `stock_circulate_stock_holder` | stock | stock | 流通股东 |
| `stock_comment_detail_scrd_desire_em` | stock | stock | 市场参与意愿 |
| `stock_comment_detail_scrd_focus_em` | stock | stock | 用户关注指数 |
| `stock_comment_detail_zhpj_lspf_em` | stock | stock | 历史评分 |
| `stock_comment_detail_zlkp_jgcyd_em` | stock | stock | 机构参与度 |
| `stock_comment_em` | stock | stock | 千股千评 |
| `stock_concept_cons_futu` | stock | stock | 富途牛牛-美股概念-成分股 |
| `stock_concept_fund_flow_hist` | stock | stock | 概念历史资金流 |
| `stock_cy_a_spot_em` | stock | stock | 创业板 |
| `stock_cyq_em` | stock | stock | 筹码分布 |
| `stock_dividend_cninfo` | stock, info | stock | 历史分红 |
| `stock_dxsyl_em` | stock | stock | 打新收益率 |
| `stock_dzjy_hygtj` | stock | stock | 活跃 A 股统计 |
| `stock_dzjy_hyyybtj` | stock | stock | 活跃营业部统计 |
| `stock_dzjy_mrmx` | stock | stock | 每日明细 |
| `stock_dzjy_mrtj` | stock | stock | 每日统计 |
| `stock_dzjy_sctj` | stock | stock | 市场统计 |
| `stock_dzjy_yybph` | stock | stock | 营业部排行 |
| `stock_ebs_lg` | stock | stock | 股债利差 |
| `stock_esg_hz_sina` | stock | stock | 华证指数 |
| `stock_esg_msci_sina` | stock | stock | MSCI |
| `stock_esg_rate_sina` | stock | stock | ESG 评级数据 |
| `stock_esg_rft_sina` | stock | stock | 路孚特 |
| `stock_esg_zd_sina` | stock | stock | 秩鼎 |
| `stock_fhps_detail_em` | stock | stock | 分红配送详情-东财 |
| `stock_fhps_detail_ths` | stock | stock | 分红情况-同花顺 |
| `stock_fhps_em` | stock | stock | 分红配送-东财 |
| `stock_financial_abstract` | stock | stock | 关键指标-新浪 |
| `stock_financial_abstract_new_ths` | stock | stock | 关键指标-同花顺 |
| `stock_financial_analysis_indicator` | stock | stock | 财务指标 |
| `stock_financial_analysis_indicator_em` | stock | stock | 主要指标-东方财富 |
| `stock_financial_benefit_new_ths` | stock | stock | 利润表 |
| `stock_financial_cash_new_ths` | stock | stock | 现金流量表 |
| `stock_financial_debt_new_ths` | stock | stock | 资产负债表 |
| `stock_financial_hk_analysis_indicator_em` | stock | stock | 港股财务指标 |
| `stock_financial_hk_report_em` | stock | stock | 港股财务报表 |
| `stock_financial_report_sina` | stock | stock | 财务报表-新浪 |
| `stock_financial_us_analysis_indicator_em` | stock | stock | 美股财务指标 |
| `stock_financial_us_report_em` | stock | stock | 美股财务报表 |
| `stock_fund_flow_big_deal` | stock | stock | 大单追踪 |
| `stock_fund_flow_concept` | stock | stock | 概念资金流 |
| `stock_fund_flow_individual` | stock | stock | 个股资金流 |
| `stock_fund_flow_industry` | stock, gold | stock | 行业资金流 |
| `stock_fund_stock_holder` | stock | stock | 基金持股 |
| `stock_gddh_em` | stock | stock | 股东大会 |
| `stock_gdfx_free_holding_analyse_em` | stock | stock | 股东持股分析-十大流通股东 |
| `stock_gdfx_free_holding_change_em` | stock | stock | 股东持股变动统计-十大流通股东 |
| `stock_gdfx_free_holding_detail_em` | stock | stock | 股东持股明细-十大流通股东 |
| `stock_gdfx_free_holding_statistics_em` | stock | stock | 股东持股统计-十大流通股东 |
| `stock_gdfx_free_holding_teamwork_em` | stock | stock | 股东协同-十大流通股东 |
| `stock_gdfx_free_top_10_em` | stock | stock | 十大流通股东(个股) |
| `stock_gdfx_holding_analyse_em` | stock | stock | 股东持股分析-十大股东 |
| `stock_gdfx_holding_change_em` | stock | stock | 股东持股变动统计-十大股东 |
| `stock_gdfx_holding_detail_em` | stock | stock | 股东持股明细-十大股东 |
| `stock_gdfx_holding_statistics_em` | stock | stock | 股东持股统计-十大股东 |
| `stock_gdfx_holding_teamwork_em` | stock | stock | 股东协同-十大股东 |
| `stock_gdfx_top_10_em` | stock | stock | 十大股东(个股) |
| `stock_ggcg_em` | stock | stock | 股东增减持 |
| `stock_gpzy_distribute_statistics_bank_em` | stock | stock | 质押机构分布统计-银行 |
| `stock_gpzy_distribute_statistics_company_em` | stock | stock | 质押机构分布统计-证券公司 |
| `stock_gpzy_industry_data_em` | stock | stock | 上市公司质押比例 |
| `stock_gpzy_pledge_ratio_detail_em` | stock | stock | 重要股东股权质押明细 |
| `stock_gpzy_pledge_ratio_em` | stock | stock | 上市公司质押比例 |
| `stock_gpzy_profile_em` | stock | stock | 股权质押市场概况 |
| `stock_gsrl_gsdt_em` | stock | stock | 公司动态 |
| `stock_history_dividend` | stock | stock | 历史分红 |
| `stock_history_dividend_detail` | stock, info | stock | 分红配股 |
| `stock_hk_company_profile_em` | stock | stock | 公司资料 |
| `stock_hk_daily` | stock | stock | 历史行情数据-新浪 |
| `stock_hk_dividend_payout_em` | stock | stock | 分红派息 |
| `stock_hk_famous_spot_em` | stock | stock | 知名港股 |
| `stock_hk_fhpx_detail_ths` | stock | stock | 分红配送详情-港股-同花顺 |
| `stock_hk_financial_indicator_em` | stock | stock | 财务指标 |
| `stock_hk_ggt_components_em` | stock | stock | 港股通成份股 |
| `stock_hk_growth_comparison_em` | stock | stock | 成长性对比 |
| `stock_hk_gxl_lg` | stock | stock | 恒生指数股息率 |
| `stock_hk_hist` | stock | stock | 历史行情数据-东财 |
| `stock_hk_hist_min_em` | stock | stock | 分时数据-东财 |
| `stock_hk_hot_rank_detail_em` | stock | stock | 港股 |
| `stock_hk_hot_rank_detail_realtime_em` | stock | stock | 港股 |
| `stock_hk_hot_rank_em` | stock | stock | 人气榜-港股 |
| `stock_hk_hot_rank_latest_em` | stock | stock | 港股 |
| `stock_hk_indicator_eniu` | stock | stock | 港股个股指标 |
| `stock_hk_main_board_spot_em` | stock | stock | 港股主板实时行情数据-东财 |
| `stock_hk_profit_forecast_et` | stock | stock | 港股盈利预测-经济通 |
| `stock_hk_scale_comparison_em` | stock | stock | 规模对比 |
| `stock_hk_security_profile_em` | stock | stock | 证券资料 |
| `stock_hk_spot` | stock | stock | 实时行情数据-新浪 |
| `stock_hk_spot_em` | stock | stock | 实时行情数据-东财 |
| `stock_hk_valuation_baidu` | stock | stock | 港股估值指标 |
| `stock_hk_valuation_comparison_em` | stock | stock | 估值对比 |
| `stock_hold_change_cninfo` | stock, info | stock | 股本变动 |
| `stock_hold_control_cninfo` | stock, info | stock | 实际控制人持股变动 |
| `stock_hold_management_detail_cninfo` | stock, info | stock | 高管持股变动明细 |
| `stock_hold_management_detail_em` | stock | stock | 董监高及相关人员持股变动明细 |
| `stock_hold_management_person_em` | stock | stock | 人员增减持股变动明细 |
| `stock_hold_num_cninfo` | stock, info | stock | 股东人数及持股集中度 |
| `stock_hot_deal_xq` | stock | stock | 交易排行榜 |
| `stock_hot_follow_xq` | stock | stock | 关注排行榜 |
| `stock_hot_keyword_em` | stock | stock | 热门关键词 |
| `stock_hot_rank_detail_em` | stock | stock | A股 |
| `stock_hot_rank_detail_realtime_em` | stock | stock | A股 |
| `stock_hot_rank_em` | stock | stock | 人气榜-A股 |
| `stock_hot_rank_latest_em` | stock | stock | A股 |
| `stock_hot_rank_relate_em` | stock | stock | 相关股票 |
| `stock_hot_search_baidu` | stock | stock | 热搜股票 |
| `stock_hot_tweet_xq` | stock | stock | 讨论排行榜 |
| `stock_hot_up_em` | stock | stock | 飙升榜-A股 |
| `stock_hsgt_board_rank_em` | stock | stock | 板块排行 |
| `stock_hsgt_fund_flow_summary_em` | stock | stock | 沪深港通资金流向 |
| `stock_hsgt_fund_min_em` | stock | stock | 沪深港通分时数据 |
| `stock_hsgt_hist_em` | stock | stock | 沪深港通历史数据 |
| `stock_hsgt_hold_stock_em` | stock | stock | 个股排行 |
| `stock_hsgt_individual_detail_em` | stock | stock | 沪深港通持股-个股详情 |
| `stock_hsgt_individual_em` | stock | stock | 沪深港通持股-个股 |
| `stock_hsgt_institution_statistics_em` | stock | stock | 机构排行 |
| `stock_hsgt_sh_hk_spot_em` | stock | stock | 沪深港通-港股通(沪>港)实时行情 |
| `stock_hsgt_stock_statistics_em` | stock | stock | 每日个股统计 |
| `stock_index_pb_lg` | stock | stock | 指数市净率 |
| `stock_index_pe_lg` | stock | stock | 指数市盈率 |
| `stock_individual_basic_info_hk_xq` | stock | stock | 个股信息查询-雪球 |
| `stock_individual_basic_info_us_xq` | stock | stock | 个股信息查询-雪球 |
| `stock_individual_basic_info_xq` | stock | stock | 个股信息查询-雪球 |
| `stock_individual_fund_flow` | stock | stock | 个股资金流 |
| `stock_individual_fund_flow_rank` | stock | stock | 个股资金流排名 |
| `stock_individual_info_em` | stock | stock | 个股信息查询-东财 |
| `stock_individual_spot_xq` | stock | stock | 实时行情数据-雪球 |
| `stock_industry_category_cninfo` | stock, info | stock | 行业分类数据-巨潮资讯 |
| `stock_industry_change_cninfo` | stock, info | stock | 上市公司行业归属的变动情况-巨潮资讯 |
| `stock_industry_clf_hist_sw` | stock | stock | 申万个股行业分类变动历史 |
| `stock_industry_pe_ratio_cninfo` | stock, info | stock | 行业市盈率 |
| `stock_info_a_code_name` | stock | stock | 股票列表-A股 |
| `stock_info_bj_name_code` | stock | stock | 股票列表-北证 |
| `stock_info_change_name` | stock | stock | 股票更名 |
| `stock_info_sh_delist` | stock | stock | 暂停/终止上市-上证 |
| `stock_info_sh_name_code` | stock | stock | 股票列表-上证 |
| `stock_info_sz_change_name` | stock | stock | 名称变更-深证 |
| `stock_info_sz_delist` | stock | stock | 终止/暂停上市-深证 |
| `stock_info_sz_name_code` | stock | stock | 股票列表-深证 |
| `stock_inner_trade_xq` | stock | stock | 内部交易 |
| `stock_institute_hold` | stock | stock | 机构持股一览表 |
| `stock_institute_hold_detail` | stock | stock | 机构持股详情 |
| `stock_institute_recommend` | stock | stock | 机构推荐池 |
| `stock_institute_recommend_detail` | stock | stock | 股票评级记录 |
| `stock_intraday_em` | stock | stock | 日内分时数据-东财 |
| `stock_intraday_sina` | stock | stock | 日内分时数据-新浪 |
| `stock_ipo_benefit_ths` | stock | stock | IPO 受益股 |
| `stock_ipo_declare_em` | stock | stock | 首发申报信息 |
| `stock_ipo_hk_ths` | stock | stock | 新股申购与中签-港股-同花顺 |
| `stock_ipo_info` | stock | stock | 新股发行 |
| `stock_ipo_review_em` | stock | stock | 新股上会信息 |
| `stock_ipo_summary_cninfo` | stock, info | stock | 上市相关-巨潮资讯 |
| `stock_ipo_ths` | stock | stock | 新股申购与中签-同花顺 |
| `stock_ipo_tutor_em` | stock | stock | IPO辅导信息 |
| `stock_irm_ans_cninfo` | stock | stock | 互动易-回答 |
| `stock_irm_cninfo` | stock | stock | 互动易-提问 |
| `stock_jgdy_detail_em` | stock | stock | 机构调研-详细 |
| `stock_jgdy_tj_em` | stock | stock | 机构调研-统计 |
| `stock_kc_a_spot_em` | stock | stock | 科创板 |
| `stock_lh_yyb_capital` | stock | stock | 龙虎榜-营业部排行-资金实力最强 |
| `stock_lh_yyb_control` | stock | stock | 龙虎榜-营业部排行-抱团操作实力 |
| `stock_lh_yyb_most` | stock | stock | 龙虎榜-营业部排行-上榜次数最多 |
| `stock_lhb_detail_daily_sina` | stock | stock | 龙虎榜-每日详情 |
| `stock_lhb_detail_em` | stock | stock | 龙虎榜详情 |
| `stock_lhb_ggtj_sina` | stock | stock | 龙虎榜-个股上榜统计 |
| `stock_lhb_hyyyb_em` | stock | stock | 每日活跃营业部 |
| `stock_lhb_jgmmtj_em` | stock | stock | 机构买卖每日统计 |
| `stock_lhb_jgmx_sina` | stock | stock | 龙虎榜-机构席位成交明细 |
| `stock_lhb_jgstatistic_em` | stock | stock | 机构席位追踪 |
| `stock_lhb_jgzz_sina` | stock | stock | 龙虎榜-机构席位追踪 |
| `stock_lhb_stock_detail_em` | stock | stock | 个股龙虎榜详情 |
| `stock_lhb_stock_statistic_em` | stock | stock | 个股上榜统计 |
| `stock_lhb_traderstatistic_em` | stock | stock | 营业部统计 |
| `stock_lhb_yyb_detail_em` | stock | stock | 营业部详情数据-东财 |
| `stock_lhb_yybph_em` | stock | stock | 营业部排行 |
| `stock_lhb_yytj_sina` | stock | stock | 龙虎榜-营业上榜统计 |
| `stock_lrb_em` | stock | stock | 利润表 |
| `stock_main_fund_flow` | stock | stock | 主力净流入排名 |
| `stock_main_stock_holder` | stock | stock | 主要股东 |
| `stock_management_change_ths` | stock | stock | 高管持股变动统计 |
| `stock_margin_account_info` | stock | stock | 两融账户信息 |
| `stock_margin_detail_sse` | stock | stock | 融资融券明细 |
| `stock_margin_detail_szse` | stock | stock | 融资融券明细 |
| `stock_margin_ratio_pa` | stock | stock | 标的证券名单及保证金比例查询 |
| `stock_margin_sse` | stock | stock | 融资融券汇总 |
| `stock_margin_szse` | stock | stock | 融资融券汇总 |
| `stock_margin_underlying_info_szse` | stock | stock | 标的证券信息 |
| `stock_market_activity_legu` | stock | stock | 赚钱效应分析 |
| `stock_market_fund_flow` | stock | stock | 大盘资金流 |
| `stock_market_pb_lg` | stock | stock | 主板市净率 |
| `stock_market_pe_lg` | stock | stock | 主板市盈率 |
| `stock_new_a_spot_em` | stock | stock | 新股 |
| `stock_new_gh_cninfo` | stock, info | stock | 新股过会 |
| `stock_new_ipo_cninfo` | stock, info | stock | 新股发行 |
| `stock_news_em` | stock, info | stock | 个股新闻 |
| `stock_news_main_cx` | stock | stock | 财经内容精选 |
| `stock_notice_report` | stock, info | stock | 沪深京 A 股公告 |
| `stock_pg_em` | stock | stock | 配股 |
| `stock_price_js` | stock | stock | 美港目标价 |
| `stock_profile_cninfo` | stock, info | stock | 公司概况-巨潮资讯 |
| `stock_profit_forecast_em` | stock, info | stock | 盈利预测-东方财富 |
| `stock_profit_forecast_ths` | stock, info | stock | 盈利预测-同花顺 |
| `stock_profit_sheet_by_quarterly_em` | stock | stock | 利润表-按单季度 |
| `stock_profit_sheet_by_report_delisted_em` | stock, info | stock | 利润表-按报告期 |
| `stock_profit_sheet_by_report_em` | stock, info | stock | 利润表-按报告期 |
| `stock_profit_sheet_by_yearly_em` | stock | stock | 利润表-按年度 |
| `stock_qbzf_em` | stock | stock | 增发 |
| `stock_qsjy_em` | stock | stock | 券商业绩月报 |
| `stock_rank_cxfl_ths` | stock | stock | 持续放量 |
| `stock_rank_cxsl_ths` | stock | stock | 持续缩量 |
| `stock_rank_forecast_cninfo` | stock, info | stock | 投资评级 |
| `stock_rank_ljqd_ths` | stock | stock | 量价齐跌 |
| `stock_rank_ljqs_ths` | stock | stock | 量价齐升 |
| `stock_rank_xstp_ths` | stock | stock | 向上突破 |
| `stock_rank_xxtp_ths` | stock | stock | 向下突破 |
| `stock_rank_xzjp_ths` | stock | stock | 险资举牌 |
| `stock_register_all_em` | stock | stock | 全部 |
| `stock_register_bj` | stock | stock | 北交所 |
| `stock_register_cyb` | stock | stock | 创业板 |
| `stock_register_db` | stock | stock | 达标企业 |
| `stock_register_kcb` | stock | stock | 科创板 |
| `stock_register_sh` | stock | stock | 上海主板 |
| `stock_register_sz` | stock | stock | 深圳主板 |
| `stock_report_disclosure` | stock, info | stock | 预约披露时间-巨潮资讯 |
| `stock_report_fund_hold` | stock | stock | 基金持股 |
| `stock_report_fund_hold_detail` | stock | stock | 基金持股明细 |
| `stock_repurchase_em` | stock | stock | 股票回购数据 |
| `stock_research_report_em` | stock, info | stock | 个股研报 |
| `stock_restricted_release_detail_em` | stock | stock | 限售股解禁详情 |
| `stock_restricted_release_queue_em` | stock | stock | 解禁批次 |
| `stock_restricted_release_queue_sina` | stock | stock | 个股限售解禁-新浪 |
| `stock_restricted_release_stockholder_em` | stock | stock | 解禁股东 |
| `stock_restricted_release_summary_em` | stock | stock | 限售股解禁 |
| `stock_sector_detail` | stock | stock | 板块详情 |
| `stock_sector_fund_flow_hist` | stock | stock | 行业历史资金流 |
| `stock_sector_fund_flow_rank` | stock | stock | 板块资金流排名 |
| `stock_sector_fund_flow_summary` | stock | stock | 行业个股资金流 |
| `stock_sector_spot` | stock | stock | 板块行情 |
| `stock_sgt_reference_exchange_rate_sse` | stock, info | stock | 参考汇率-沪港通 |
| `stock_sgt_reference_exchange_rate_szse` | stock | stock | 参考汇率-深港通 |
| `stock_sgt_settlement_exchange_rate_sse` | stock, info | stock | 结算汇率-沪港通 |
| `stock_sgt_settlement_exchange_rate_szse` | stock | stock | 结算汇率-深港通 |
| `stock_sh_a_spot_em` | stock | stock | 沪 A 股 |
| `stock_share_change_cninfo` | stock, info | stock | 公司股本变动-巨潮资讯 |
| `stock_share_hold_change_bse` | stock, info | stock | 董监高及相关人员持股变动-北证 |
| `stock_share_hold_change_sse` | stock | stock | 董监高及相关人员持股变动-上证 |
| `stock_share_hold_change_szse` | stock, info | stock | 董监高及相关人员持股变动-深证 |
| `stock_shareholder_change_ths` | stock | stock | 股东持股变动统计 |
| `stock_sns_sseinfo` | stock | stock | 上证e互动 |
| `stock_sse_deal_daily` | stock | stock | 上海证券交易所-每日概况 |
| `stock_sse_summary` | stock | stock | 上海证券交易所 |
| `stock_staq_net_stop` | stock | stock | 两网及退市 |
| `stock_sy_em` | stock | stock | 个股商誉明细 |
| `stock_sy_hy_em` | stock | stock | 行业商誉 |
| `stock_sy_jz_em` | stock | stock | 个股商誉减值明细 |
| `stock_sy_profile_em` | stock | stock | A股商誉市场概况 |
| `stock_sy_yq_em` | stock | stock | 商誉减值预期明细 |
| `stock_sz_a_spot_em` | stock | stock | 深 A 股 |
| `stock_szse_area_summary` | stock | stock | 地区交易排序 |
| `stock_szse_sector_summary` | stock | stock | 股票行业成交 |
| `stock_szse_summary` | stock | stock | 证券类别统计 |
| `stock_tfp_em` | stock | stock | 停复牌信息 |
| `stock_us_daily` | stock | stock | 历史行情数据-新浪 |
| `stock_us_famous_spot_em` | stock | stock | 知名美股 |
| `stock_us_hist` | stock | stock | 历史行情数据-东财 |
| `stock_us_hist_min_em` | stock | stock | 分时数据-东财 |
| `stock_us_pink_spot_em` | stock | stock | 粉单市场 |
| `stock_us_spot` | stock | stock | 实时行情数据-新浪 |
| `stock_us_spot_em` | stock | stock | 实时行情数据-东财 |
| `stock_us_valuation_baidu` | stock | stock | 美股估值指标 |
| `stock_value_em` | stock | stock | 个股估值 |
| `stock_xgsglb_em` | stock | stock | 新股申购与中签 |
| `stock_xgsr_ths` | stock | stock | 新股上市首日 |
| `stock_xjll_em` | stock | stock | 现金流量表 |
| `stock_yjbb_em` | stock | stock | 业绩报表 |
| `stock_yjkb_em` | stock | stock | 业绩快报 |
| `stock_yjyg_em` | stock | stock | 业绩预告 |
| `stock_yysj_em` | stock | stock | 预约披露时间-东方财富 |
| `stock_yzxdr_em` | stock | stock | 一致行动人 |
| `stock_zcfz_bj_em` | stock | stock | 资产负债表-北交所 |
| `stock_zcfz_em` | stock | stock | 资产负债表-沪深 |
| `stock_zdhtmx_em` | stock | stock | 重大合同 |
| `stock_zh_a_cdr_daily` | stock | stock | 历史行情数据 |
| `stock_zh_a_daily` | stock | stock | 历史行情数据-新浪 |
| `stock_zh_a_disclosure_relation_cninfo` | stock, info | stock | 信息披露调研-巨潮资讯 |
| `stock_zh_a_disclosure_report_cninfo` | stock, info | stock | 信息披露公告-巨潮资讯 |
| `stock_zh_a_gbjg_em` | stock | stock | 股本结构 |
| `stock_zh_a_gdhs` | stock | stock | 股东户数 |
| `stock_zh_a_gdhs_detail_em` | stock | stock | 股东户数详情 |
| `stock_zh_a_hist` | stock | stock | 历史行情数据-东财 |
| `stock_zh_a_hist_min_em` | stock | stock | 分时数据-东财 |
| `stock_zh_a_hist_pre_min_em` | stock | stock | 盘前数据 |
| `stock_zh_a_hist_tx` | stock | stock | 历史行情数据-腾讯 |
| `stock_zh_a_minute` | stock | stock | 分时数据-新浪 |
| `stock_zh_a_new` | stock | stock | 次新股 |
| `stock_zh_a_new_em` | stock | stock | 新股 |
| `stock_zh_a_spot` | stock | stock | 实时行情数据-新浪 |
| `stock_zh_a_spot_em` | stock | stock | 沪深京 A 股 |
| `stock_zh_a_st_em` | stock | stock | 风险警示板 |
| `stock_zh_a_stop_em` | stock | stock | 两网及退市 |
| `stock_zh_a_tick_tx` | stock | stock | 腾讯财经 |
| `stock_zh_ab_comparison_em` | stock | stock | AB 股比价 |
| `stock_zh_ah_daily` | stock | stock | 历史行情数据 |
| `stock_zh_ah_name` | stock | stock | A+H股票字典 |
| `stock_zh_ah_spot` | stock | stock | 实时行情数据-腾讯 |
| `stock_zh_ah_spot_em` | stock | stock | 实时行情数据-东财 |
| `stock_zh_b_daily` | stock | stock | 历史行情数据 |
| `stock_zh_b_minute` | stock | stock | 分时数据 |
| `stock_zh_b_spot` | stock | stock | 实时行情数据-新浪 |
| `stock_zh_b_spot_em` | stock | stock | 实时行情数据-东财 |
| `stock_zh_dupont_comparison_em` | stock | stock | 杜邦分析比较 |
| `stock_zh_growth_comparison_em` | stock | stock | 成长性比较 |
| `stock_zh_kcb_daily` | stock | stock | 历史行情数据 |
| `stock_zh_kcb_report_em` | stock, info | stock | 科创板公告 |
| `stock_zh_kcb_spot` | stock | stock | 实时行情数据 |
| `stock_zh_scale_comparison_em` | stock | stock | 公司规模 |
| `stock_zh_valuation_baidu` | stock | stock | A 股估值指标 |
| `stock_zh_valuation_comparison_em` | stock | stock | 估值比较 |
| `stock_zh_vote_baidu` | stock | stock | 涨跌投票 |
| `stock_zt_pool_dtgc_em` | stock | stock | 跌停股池 |
| `stock_zt_pool_em` | stock | stock | 涨停股池 |
| `stock_zt_pool_previous_em` | stock | stock | 昨日涨停股池 |
| `stock_zt_pool_strong_em` | stock | stock | 强势股池 |
| `stock_zt_pool_sub_new_em` | stock | stock | 次新股池 |
| `stock_zt_pool_zbgc_em` | stock | stock | 炸板股池 |
| `stock_zygc_em` | stock | stock | 主营构成-东财 |
| `stock_zyjs_ths` | stock | stock | 主营介绍-同花顺 |

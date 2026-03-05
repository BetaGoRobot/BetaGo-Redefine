// Package config 提供动态配置管理功能
//
// 该包实现了一个灵活的动态配置系统，支持：
//   - 多级配置优先级：user > chat > global > toml > default
//   - 按 chat_id 和 user_id 维度筛选配置
//   - 功能开关管理（支持按 chat_id 屏蔽功能）
//   - 配置增删改查接口
//   - 飞书命令集成
//
// # 配置优先级
//
// 配置读取时按以下优先级从高到低查找：
//  1. chat_user: 特定聊天中的特定用户配置
//  2. user: 特定用户配置
//  3. chat: 特定聊天配置
//  4. global: 全局配置
//  5. toml: 配置文件中的配置
//  6. default: 代码中的默认值
//
// # 使用示例
//
// ## 直接使用 Manager
//
//	mgr := config.GetManager()
//
//	// 读取配置
//	rate := mgr.GetInt(ctx, config.KeyReactionDefaultRate, chatID, userID)
//	enabled := mgr.GetBool(ctx, config.KeyIntentRecognitionEnabled, chatID, userID)
//
//	// 设置配置
//	err := mgr.SetInt(ctx, config.KeyReactionDefaultRate, config.ScopeChat, chatID, "", 50)
//	err := mgr.SetBool(ctx, config.KeyIntentRecognitionEnabled, config.ScopeGlobal, "", "", true)
//
//	// 删除配置
//	err := mgr.DeleteConfig(ctx, config.KeyReactionDefaultRate, config.ScopeChat, chatID, "")
//
// ## 使用 Accessor（推荐）
//
//	// 创建访问器（绑定上下文和用户/聊天ID）
//	accessor := config.NewAccessor(ctx, chatID, userID)
//
//	// 或从 meta data 创建
//	accessor := config.NewAccessorFromMeta(ctx, metaData)
//
//	// 读取配置
//	rate := accessor.ReactionDefaultRate()
//	enabled := accessor.IntentRecognitionEnabled()
//
//	// 检查功能是否启用
//	if accessor.IsFeatureEnabled("chat") {
//	    // 功能已启用
//	}
//
// ## 使用全局便捷函数
//
//	rate := config.GetReactionDefaultRate(ctx, chatID, userID)
//	enabled := config.IsIntentRecognitionEnabled(ctx, chatID, userID)
//
// # 飞书命令
//
// ## 配置管理命令
//
//	/config list [scope=global/chat/user]  - 列出配置
//	/config set key=xxx value=xxx [scope=...] - 设置配置
//	/config delete key=xxx [scope=...]        - 删除配置
//
// ## 功能开关命令
//
//	/feature list                    - 列出被禁用的功能
//	/feature disable feature=xxx     - 禁用功能
//	/feature enable feature=xxx      - 启用功能
//
// # 支持的配置项
//
// ## 概率配置 (0-100)
//   - reaction_default_rate: 默认反应概率
//   - reaction_follow_default_rate: 跟随反应概率
//   - repeat_default_rate: 默认重复消息概率
//   - imitate_default_rate: 默认模仿概率
//   - intent_fallback_rate: 意图识别失败回退概率
//   - intent_reply_threshold: 意图回复阈值
//
// ## 开关配置
//   - intent_recognition_enabled: 是否启用意图识别
//
// # 数据库存储
//
// 配置存储在 dynamic_configs 表中，键格式为：
//   - global:key
//   - chat:chat_id:key
//   - user::user_id:key (注意两个冒号)
//   - user:chat_id:user_id:key
//
// 功能开关存储在 function_enablings 表中。
package config

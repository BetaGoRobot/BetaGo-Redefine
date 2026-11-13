# BetaGo 飞书背景音频原型

本目录是 **BetaGoRobot/BetaGo-Redefine** 的薄小程序实验工程。只负责客户端背景音频与自有音乐 API 适配，不实现生产歌单后端，不修改旧 BetaGo、YesPlayMusic 或共享 Bot。

开发者从这里开始：[导入、登录和手机扫码步骤](docs/developer-quickstart.md)。合入主仓库时目录是 `examples/feishu-music-mini`，专用 CI 会上传三个 ZIP 开发产物。

已实现：官方 `tt.getBackgroundAudioManager` 适配、连续队列、播放/暂停/切歌/seek、一次取源刷新并恢复位置、自有 bearer 会话与恢复码、CAS 进度保存、状态观察和脱敏诊断、固定 HTTPS 网页回退。Android/iOS 真机行为全部待测；Node 模拟管理器和 HTTP 测试不是飞书真机证明。

## 本地复现

需要 Node >=18、Python 3 和 zip；没有 npm 运行时依赖。

```sh
npm ci --ignore-scripts
npm run fixtures:generate
npm run verify
node scripts/config-check.mjs
```

`npm run verify` 校验 JS 语法、JSON、TTML 层级/事件处理器，运行关键状态机与 HTTP 夹具测试，再生成可导入开发包。此命令不调用飞书官方编译器。

开发包位于 `dist/betago-music-mini-dev.zip`。解压后在飞书开发者工具导入包含 `project.config.json` 的目录；源码根目录由 `miniprogramRoot: ./gadget/` 指定。开发工具根据 `setting.es6: true` 处理源码。工程使用原生 `.js/.json/.ttml/.ttss`，没有微信/抖音 SDK。

## 环境配置与导入

```sh
cp config.example.json config.local.json
# 编辑非敏感的 App ID、测试服务 HTTPS 地址与允许域名
npm run build
```

`config.local.json` 已忽略，不接受密钥、身份或任意新增字段。例子里的 `.invalid` 地址有意不可访问；构建会列出未配置项，不会把它当作可用的手机预览。

| 配置 | 含义 |
| --- | --- |
| `appId` | 现有飞书测试应用的非敏感 ID；无 App Secret |
| `mode: fixture` | 使用本目录 HTTP 夹具，路径增加 `/fixture`，页面与诊断均显示夹具标记 |
| `mode: service` | 使用共享契约 `/music-api/v1`；生产歌单/身份全部交给自有服务 |
| `apiBaseUrl` | 服务源地址，手机测试必须使用受信任 HTTPS；不含 token 或查询参数 |
| `audioOrigins` | 精确音源 origin 允许列表；需要与实际 resolver/CDN 返回值一致，不接受任意源 |
| `webPlayerUrl / webOrigins` | 固定 HTTPS 网页回退；禁止 query/hash、任意 schema 或重定向参数 |
| `authMode: anonymous` | 新自有访客会话，或使用本机已有 bearer；不自动继承网页访客身份 |
| `authMode: feishu` | 用户连接时调用 `requestAccess({scopeList:[]})`，旧端回退 `login`；发送短期 code 到自有交换接口 |
| `feishuExchangePath` | 默认 `/music-api/v1/auth/feishu/exchange`；后端必须用同一 App 验证实际租户/用户 |
| `allowLocalhost` | 只为 Node/本机夹具联调；`service` 构建禁止开启，手机不能访问服务器的 localhost |

App 实例持有唯一音频管理器，Page 的隐藏/卸载不会主动 stop。暂停或其他音频打断后不自动抢回声音；点击播放会重新取源并恢复本播放器记录的位置。锁屏原生 Play 事件也会更新状态，过期时重新取源。后台是否还能执行取源/自动换曲、长时间定时器及进度保存，以真机矩阵为准。

自有 bearer 存在飞书本地缓存中，只发送到配置的自有服务。该缓存不是安全硬件存储保证，清缓存会丢失；不存飞书 user_access_token、App Secret、网易 Cookie。退出会话会吊销服务会话并清除本地 bearer。恢复码只在显式输入时发送至自有服务，不写入缓存/日志/跳转 URL。

飞书登录交换和实际取源仍需用目标应用联调；本仓库不声称匿名/飞书登录/网页会话可以直接互换。用已部署自有服务的恢复码，可在小程序显式连接同一资料，再选择歌单。队列 deviceID 只定位设备队列，不是凭证。

## 确定性音源

```sh
npm run fixtures:generate
npm run fixtures:serve
```

默认监听 `127.0.0.1:8787`，`GET /fixture/health` 会返回 `fixture: true` 和 `nativeDeviceProof: false`。两条原创低音量三角波各 **16 分钟**，PCM WAV、8 kHz、16 bit、mono，CC0；每分钟首秒静音作为观察标记。完整哈希见生成后的 `fixtures/audio/manifest.json`。用于验证播放行为，不是第三方音乐内容；WAV 在目标客户端的实际解码也属于真机待测项。

如需手机连接，在**现有获准的测试 HTTPS 入口**反向代理本服务，保留 `/fixture/*`、Range、Content-Range 和 Content-Length，并通过 `FIXTURE_PUBLIC_ORIGIN` 指定该入口：

```sh
FIXTURE_PUBLIC_ORIGIN=https://your-existing-test-origin.example \
  FIXTURE_PORT=8787 npm run fixtures:serve
```

此地址是占位示例，不会创建 DNS、云资源或修改应用权限。本服务默认只绑定 loopback；可通过既有代理访问，不建议向公网直接开放 HTTP。

音源租约默认 60 秒（`FIXTURE_LEASE_MS` 可调）。点击“使已签发 URL 过期”后，旧 URL 的后续请求必定返回 403；新 resolve 返回新租约。已经缓冲的音频不会因 URL 字符串过期必然停止，真机可通过 seek 到未缓冲处触发新请求。页面“刷新续播”只是显式调用恢复路径，不能冒充原生 `onError` 自动恢复成功。

## 接口和验证范围

共享契约来自相邻网页/后端任务，本目录保存只读的 [契约快照](docs/music-api-contract.md)。精确映射和边界见 [docs/api-integration.md](docs/api-integration.md)。

真实设备步骤、通过标准和结果表见 [docs/device-validation.md](docs/device-validation.md) 与 [docs/device-results.csv](docs/device-results.csv)。诊断最多保留 240 条，仅包含相对时间、设备型号/OS/飞书/SDK 版本、状态、队列下标、位置、固定错误码；没有曲目名称/ID、URL、sessionToken、授权码、Cookie 或原始 SDK 错误。复制后可保存为本地 JSON，勿把开发工具 Network 面板原始请求导出作为公开诊断。

没有官方可靠 API 读取聊天卡片 native audio 的进度。本原型只恢复自有播放器的队列记录，网页回退不传递凭证或签名音源。外部播放器注册表暂为空，没有集成未知 schema。

## 官方依据（2026-11-13 核对）

- [BackgroundAudioManager](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/getbackgroundaudiomanager.md)：小程序 Android/iOS >=5.20；PC/H5 不支持。
- [背景音频概述与已知问题](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/background-audio.md)：网络 URL、音轨切换与其他音频打断均需验证。
- [seek 秒单位](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/backgroundaudiomanager/seek.md)、[本地缓存签名](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/cache/setstoragesync.md)。
- [小程序登录](https://open.feishu.cn/document/client-docs/gadget/open-capabilities/gadget-login.md)、[requestAccess](https://open.feishu.cn/document/web-app/gadget-api/open-ability/login/requestaccess.md)。
- [项目配置](https://open.feishu.cn/document/tools-and-resources/development-tools/development-of-gadget-in-tools/gadget-project-configuration.md)、[官方 CLI 与预览前提](https://open.feishu.cn/document/tools-and-resources/development-tools/ide-with-commands.md)。
- [openSchema](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/navigation/openschema.md)：固定网页回退仍受平台、应用白名单和实际支持限制。

官方目录把小程序开发列为不推荐新开发；本工程是能力验证原型，应用维护者仍需确认现有应用能否启用该能力。不会发布线上应用或修改应用权限。

# BetaGo 飞书音乐小程序原型

本目录是 **BetaGoRobot/BetaGo-Redefine** 的薄小程序实验工程，负责飞书后台音频与自有音乐 API 适配。歌曲搜索、歌单、身份和音源由现有网页/API 服务管理，小程序不另建后端。

普通手机测试复用真实 API 和现有 MinIO 歌曲。维护者在部署并启用已有歌曲缓存支持后交付已配置的 ZIP；应用开发者只需登录飞书开发者工具、导入、预览并用手机扫码。操作见 [开发者快速预览](docs/developer-quickstart.md)，无需在自己的电脑启动音频服务或修改服务器网络。

## 当前进度

已实现官方 `tt.getBackgroundAudioManager` 适配、搜索歌曲并加入本设备播放列表、载入已有歌单、播放/暂停/切歌/拖动进度、过期后刷新并恢复位置、自有登录和恢复码、带版本检查的进度保存、排查信息和固定 HTTPS 网页入口。

2026-11-13 已验证的服务入口是 `https://music-mini.kmhomelab.cn:2443`：

| 路径 | 现有服务 | 已验证范围 |
| --- | --- | --- |
| `/music-api/v1/*` | 真实网页/API 后端 `172.18.0.1:37834` | HTTPS 登录、身份检查、搜索、读取/保存设备播放列表均返回 200 |
| GET/HEAD `/cloudmusic/*` | 现有 MinIO | TLS 1.3；两首已存 MP3 的 Range 206；短链接 206 → 过期 403 → 重新签发后 206 |

**手机播放尚未就绪。** 网页/API 源码已有 MinIO 缓存支持，但真实部署仍需更新并启用该能力；抽样歌曲仍返回 `preview`，本原型会停止并提示只能试听。对象文件能下载与 API 能搜索，不能证明小程序已能取得完整歌曲。当前只确认该域名有 AAAA 记录，手机所用网络的 IPv6 连通性也需验证。飞书官方编译、手机扫码、锁屏和后台播放结果均待测。

现有《小王日记》（`netease:1417862065`）与 `netease:109616` 均已通过 HTTPS Range 206 检查，签名链接也通过 206 → 过期 403 → 重新签发后 206。两份完整 HTTPS 下载文件经服务器工具识别为 MP3、双声道、44.1 kHz，实际文件时长分别为 **43:35.592** 和 **4:49.573**，合计 **48:25.165**。手机解码与连续播放超过 10 分钟仍待验证。

## 源码构建（可选）

需要 Node >=18 和 zip；没有 npm 运行时依赖。合入主仓库后的路径是 `examples/feishu-music-mini`。普通测试使用维护者提供的已配置开发包即可；CI 的通用包仍可能含占位 App ID/地址，不能当作可直接播放的手机包。

```sh
npm ci --ignore-scripts
npm run configure -- --app-id cli_你的应用ID --api-origin https://music-mini.kmhomelab.cn:2443
npm run build
```

`configure` 默认 `mode: service`，生成已忽略的 `config.local.json`，仅写入非敏感 App ID 和地址。网页入口可另加 `--web-url https://你的自有网页播放器地址/`。构建产物是 `dist/betago-music-mini-dev.zip`；导入解压后包含 `project.config.json` 的目录，源码根目录为 `./gadget/`。本地构建不调用飞书官方编译器。

| 配置 | 含义 |
| --- | --- |
| `appId` | 现有飞书应用的非敏感 ID，不含 App Secret |
| `mode: service` | 默认模式，连接真实 `/music-api/v1` API |
| `apiBaseUrl` | 不带路径的 HTTPS origin；客户端自行追加 `/music-api/v1` |
| `audioOrigins` | 允许的精确音频 origin，须与解析接口实际返回值一致 |
| `webPlayerUrl / webOrigins` | 固定 HTTPS 网页入口，不传递凭证或当前播放链接 |
| `authMode: anonymous` | 新建自有访客登录，或使用本机已有登录；不自动继承网页访客资料 |
| `authMode: feishu` | 连接时向自有后端发送飞书短期授权码，实际登录仍需目标应用联调 |
| `feishuExchangePath` | 默认 `/music-api/v1/auth/feishu/exchange` |
| `allowLocalhost` | 仅限本机开发回归；`service` 构建禁止开启 |

连接后保持暂停。搜索并选择歌曲时，会保留本设备已有播放列表：已有歌曲直接选中，新歌曲追加后播放。选择已有歌单则把该歌单载入本设备播放列表。播放地址只在使用时获取，不作为歌曲身份或长期保存内容。

## 本机开发回归

```sh
npm ci --ignore-scripts
npm run fixtures:generate
npm run verify
```

此路径需要 Python 3。生成的两条 16 分钟 CC0 合成音只供本机开发回归，用于验证 Range、链接过期和播放器状态；不上传为普通手机测试歌曲，也不部署公网测试音频服务。需要专门构建测试模式时，开发者须显式添加 `--mode fixture`，它不再是默认配置。测试服务默认仅监听 `127.0.0.1:8787`，普通用户不需要运行 `fixtures:serve`。

当前完整 `npm run verify` 已通过 33 项测试，包含 JS/JSON/TTML 校验、状态机和 HTTP 测试及开发包检查；它不能证明飞书官方编译或手机播放成功。

## 身份、进度和验证边界

自有登录凭证保存在飞书本地缓存中，仅发送给配置的自有服务；清缓存会丢失本机登录。工程不保存飞书 user_access_token、App Secret 或网易个人 Cookie。恢复码只在用户输入后发送给自有服务，不写入缓存、日志或跳转地址。退出会吊销当前登录并清理本机凭证。

App 持有唯一音频管理器，页面隐藏不会主动停止。暂停或其他音频打断后不自动抢回声音；显式播放会重新获取地址并恢复已记录位置。后台换曲、链接刷新、长时间锁屏和进度保存以真机结果为准。没有可靠官方 API 读取聊天卡片音频的精确进度，网页入口也不会自动接收当前音源或进度。

接口边界见 [API 对齐记录](docs/api-integration.md) 与只读 [契约快照](docs/music-api-contract.md)。设备步骤见 [真机执行表](docs/device-validation.md)，结果表为 [device-results.csv](docs/device-results.csv)，当前均待测。排查信息最多保留 240 条，不包含歌曲 ID/名称、播放 URL、登录凭证、授权码或 Cookie；公开前仍需检查截图和文件中的个人资料。

## 官方依据（2026-11-13 核对）

- [BackgroundAudioManager](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/getbackgroundaudiomanager.md)：小程序 Android/iOS >=5.20；PC/H5 不支持。
- [背景音频概述与已知问题](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/background-audio.md)：音轨切换与其他音频打断需要验证。
- [seek 秒单位](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/media/backgroundaudio/backgroundaudiomanager/seek.md)、[本地缓存签名](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/cache/setstoragesync.md)。
- [小程序登录](https://open.feishu.cn/document/client-docs/gadget/open-capabilities/gadget-login.md)、[requestAccess](https://open.feishu.cn/document/web-app/gadget-api/open-ability/login/requestaccess.md)。
- [项目配置](https://open.feishu.cn/document/tools-and-resources/development-tools/development-of-gadget-in-tools/gadget-project-configuration.md)、[官方 CLI 与预览前提](https://open.feishu.cn/document/tools-and-resources/development-tools/ide-with-commands.md)。
- [openSchema](https://open.feishu.cn/document/client-docs/gadget/-web-app-api/navigation/openschema.md)：网页入口仍受应用允许范围和平台实际支持限制。

官方目录把小程序开发列为不推荐新开发；本工程用于能力验证，应用维护者需确认现有应用能启用小程序。本任务不发布线上版本或修改应用权限。

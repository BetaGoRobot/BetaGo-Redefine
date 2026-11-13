# 开发者从电脑到手机预览

你是应用开发者，可直接在自己的电脑登录和扫码，无需把飞书密码、App Secret、开发者 Token 或个人 Cookie 发给代理。

## 1. 获取工程与工具

从本 PR 的 `Feishu music mini` 工作流下载 `feishu-music-mini-packages` 产物，解压后包含开发包、源码包、音频夹具包。也可以检出 PR 分支并进入 `examples/feishu-music-mini`。

使用[飞书开发者工具](https://open.feishu.cn/document/tools-and-sdks/developer-tools-portal.md)桌面版，登录你拥有该应用开发权限的飞书账号。后续手机也用同一账号。登录的是飞书开发工具；与 Codex 启动 Hook 无关。

在[开发者后台](https://open.feishu.cn/app)打开现有应用，复制 App ID，并确认已有小程序能力可用。若后台没有小程序入口，先记录这一实际限制，不要通过发布线上版本或增加无关权限来绕过。

## 2. 准备手机可访问的 HTTPS 音频

示例配置中的 `.invalid` 地址不可访问；仅导入示例包不能完成声音验证。手机也无法访问这台服务器的 `127.0.0.1`。

在运行夹具服务的机器执行：

```sh
npm ci --ignore-scripts
npm run fixtures:generate
FIXTURE_PUBLIC_ORIGIN=https://你的已有测试域名 npm run fixtures:serve
```

让该**已有、获准使用**的测试 HTTPS 入口把 `/fixture/*` 代理到服务的 `127.0.0.1:8787`，保留 Range/Content-Range，不缓存音源。手机浏览器访问 `https://你的测试域名/fixture/health`，应返回 `fixture:true`。HTTP 连通性不代表飞书播放成功。

该 HTTPS 源需要符合现有应用的请求/音源域名配置。若没有测试域名或现有允许列表不包含它，先明确记录缺项；不能声称已准备好真机播放。代理可在获准入口确定后补齐服务器配置，但不会擅改应用权限或发布。

## 3. 生成你的配置与开发包

在源码目录运行，将占位值换为实际值：

```sh
npm run configure -- --app-id cli_你的应用ID --api-origin https://你的测试域名
npm run verify
```

这会生成已忽略的 `config.local.json` 和 `dist/project.config.json`。仅含非敏感 App ID/地址；不需要任何应用密钥。默认 `fixture` 模式会提供两条各16分钟的合法合成音源。网页回退可加 `--web-url https://你的自有网页播放器地址/`；不加时保留未配置提示，先测试小程序音频。

若直接下载开发包，建议同时解压源码包，用以上命令生成配置后重建；不要只在 `dist` 改完又运行 build 覆盖配置。

## 4. 导入与扫码

在飞书开发者工具选择导入已有小程序项目，选择 **`dist` 目录**（里面有 `project.config.json`，其源码目录是 `./gadget/`）。确认 App ID，点击编译，保存真实编译结果。

编译成功后点击工具里的“预览”，用手机飞书扫一扫生成的二维码，进入后点“连接自有服务”，看见两曲队列，再点“播放”。不要把模拟器按钮成功当成手机成功。官方文档明确预览依赖开发者登录和正确 App ID。

若使用官方 CLI，可在你自己的电脑登录后预览：

```sh
opdev login
opdev preview ./dist --platform=mobile
```

CLI 的安装和登录说明见[官方文档](https://open.feishu.cn/document/tools-and-resources/development-tools/ide-with-commands.md)。不需要执行 `opdev upload`，本次不发布线上应用。

## 5. 先跑最短验证，再跑长测

先前台播放30秒，暂停/播放，seek，再切A/B。然后将A拖到15:40，锁屏70秒，确认是否自动进入B。短测通过后再做锁屏15分钟与30分钟、URL过期续播、来电/其他音频打断。

每项记录设备型号、OS、飞书版本和实际结果，点击“复制脱敏诊断”。完整矩阵在 [device-validation.md](device-validation.md)，可填写表在 [device-results.csv](device-results.csv)。真机结果目前均待测，不能用CI的28项单测代替。

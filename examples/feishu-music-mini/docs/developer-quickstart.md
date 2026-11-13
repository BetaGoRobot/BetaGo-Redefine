# 手机预览：拿到 ZIP 后再开始

**现在先等维护者把准备好的 ZIP 发给你。** 歌曲服务还需完成部署，当前测试歌曲仍可能提示只能试听。服务器和歌曲由维护者处理，你不用搭服务或改网络。

拿到 `betago-music-mini-dev.zip` 后，按下面操作：

1. 解压 ZIP。
2. 打开[飞书开发者工具](https://open.feishu.cn/document/tools-and-sdks/developer-tools-portal.md)，用有该应用开发权限的飞书账号登录。
3. 点击“导入项目”，选择解压后包含 `project.config.json` 的文件夹。
4. 编译成功后点击“预览”。
5. 用手机飞书扫描二维码，打开小程序。

进入后点“连接音乐服务”，搜索维护者提供的歌曲，再点歌曲播放。新选的歌曲会加入当前播放列表，已有歌曲会保留；也可以选择已有歌单后点击“播放”。

如果编译、扫码或播放失败，保留实际提示交给维护者。你不需要提供密码、App Secret、开发者 Token 或个人 Cookie，也不需要发布线上版本。

## 手机上先试这些

先播放 30 秒，暂停再继续，拖动进度，再切换歌曲。测试锁屏换曲时，按手机当前显示的时长，拖到歌曲**结束前 20 秒**，锁屏 70 秒，再检查是否进入下一首。

长测步骤见 [真机执行表](device-validation.md)：播放列表总时长大于 30 分钟；单曲连续播放超过 10 分钟单独记录。现有长曲已在服务器检查了文件时长和读取，手机能否解码、锁屏继续播放仍待测。

点击“复制排查信息”可保存测试记录。当前所有手机结果仍待测，本地 33 项自动测试通过也不代表手机通过。

## 仅开发者需要：从源码构建

使用维护者提供的 ZIP 可以跳过本节。自行构建需要 Node >=18 和 zip，在源码目录运行：

```sh
npm ci --ignore-scripts
npm run configure -- --app-id cli_你的应用ID --api-origin https://music-mini.kmhomelab.cn:2443
npm run build
```

默认是 `service` 模式。配置和构建只生成本机开发包，不会替维护者部署服务。导入生成的 `dist/` 或解压后的开发包。源码在主仓库的位置是 `examples/feishu-music-mini`。

开发回归另需 Python 3，执行 `npm run fixtures:generate`、`npm run verify`。两条 16 分钟合成音与显式 `--mode fixture` 仅供本机回归，不用于普通手机测试，也不部署公网测试音频服务。

已有官方 CLI 登录态的开发者也可使用 `opdev preview ./dist --platform=mobile`，见[官方说明](https://open.feishu.cn/document/tools-and-resources/development-tools/ide-with-commands.md)。本任务尚未执行官方预览，不需要执行 `opdev upload`。

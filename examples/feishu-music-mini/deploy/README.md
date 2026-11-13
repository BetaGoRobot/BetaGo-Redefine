# 原型 HTTPS 路由与维护者检查单

普通用户只需拿到开发包、登录开发工具、导入、预览和手机扫码；以下由维护者核对，不要求用户搭服务或改网络。

## 两个实际来源

| 用途 | HTTPS origin | 路径和当前结果 |
| --- | --- | --- |
| 小程序 API 请求 | `https://music-mini.kmhomelab.cn:2443` | `/music-api/v1/*`；真实自有访客、搜索、队列与完整歌曲解析已通过 |
| API 返回的音频 | `https://music.kmhomelab.cn:2443` | GET/HEAD `/cloudmusic/*`；两首指定歌曲的实际地址均取得 Range 206 |

真实 API 已为 `netease:109616`《100种生活》和 `netease:1417862065`《小王日记》返回 `available`、`reason: owned_minio_cache`。即使 API 请求发给 `music-mini`，返回的音频来源也是 **`music`**。`audioOrigins` 必须允许 `https://music.kmhomelab.cn:2443`；配置命令使用可重复的 `--audio-origin` 指定音频来源，不把 API 域名当作唯一音频来源。

当前 `/music-api/v1/auth/options` 返回 `feishu.enabled=false`。本次使用自有访客登录；飞书开发工具账号登录不代表音乐服务已开通飞书单点登录。无需为此次测试启用单点登录。

## 本目录原有路由文件

本目录的两条原型路由复用现有 Traefik、证书和 MinIO，没有启动新的测试音频服务，也不要求改动共享 Bot：

- `traefik-minio-cache.yaml`：`music-mini.*` 下 GET/HEAD `/cloudmusic/*` 转到现有 `minio:9000`。该路由可读取已有对象，但当前真实 API 返回的是上面的 `music` 来源。
- `traefik-music-api.yaml`：`music-mini.*` 下 `/music-api/v1/*` 转到真实音乐 API。本目录模板配置的上游是 `http://172.18.0.1:37834`；服务变化后须核对实际目标。

本机安装文件名为 `betago-music-mini-cache.yaml` 和 `betago-music-mini-api.yaml`，位于既有 Traefik 监视的 `data/dynamic_conf/`。只回退本任务新增的这两个文件；若原有同名文件则恢复对应备份。不执行整个 Traefik 栈的 down/up，也不操作共享数据库。已有 `music` 域名路由不属于这两个文件的回退范围。

## 维护者交付前检查单

1. 使用维护者已经核对的目标应用 App ID，以本次已配置 ZIP 的 `project.config.json` 为准；仅在本机生成配置，不硬编码进源码或公共默认配置。
2. 只读核对该应用的小程序能力、开发者/测试账号可见范围、请求域名 `music-mini.kmhomelab.cn:2443` 与音频域名 `music.kmhomelab.cn:2443`。AppID已由官方只读接口核实；已发布0.0.7以Bot为默认能力，未返回小程序能力，不等于当前开发配置一定没有小程序。**未版本化开发配置、域名许可和:2443端口接受规则仍未证实**；开发工具预览时保持正常校验，不修改应用设置。
3. 开发包使用 `mode: service`、`authMode: anonymous`，分别配置上述 API 和音频来源；如启用网页入口，再独立核对其配置。
4. 已知入口有公网 IPv6/AAAA 记录，实际手机网络连通性仍待确认。服务器探测不代表独立手机已可达。
5. 交付已配置 ZIP 和包摘要。开发者登录、官方编译、预览及手机扫码尚未执行，不能把本地静态校验当作这些步骤已通过。

## 已测范围与只读探针

真实 API 的登录、搜索、队列与两首完整歌曲解析已通过；API 返回的实际音频地址均为 HTTPS 并取得 Range 206。已有短期签名链接也通过 `206 → 过期 403 → 重新签名 206`。完整下载后，服务器识别两份文件为 MP3、双声道、44.1 kHz；长曲实际时长 43:35.592，短曲 4:49.573。

手机解码、锁屏、切聊天、自动换曲和声音打断仍待测，26 条既有设备记录保持 PENDING。普通测试优先播放长曲进行 15/30 分钟锁屏，结尾前 20 秒的自动换曲另测。

维护者的只读对象探针（运行机器需已有 boto3；不把私有配置复制到工程）：

```sh
python3 scripts/probe-owned-audio.py --config /你的私有配置.toml --public-origin https://music.kmhomelab.cn:2443
```

可选 `--ffprobe /实际路径/ffprobe` 会临时完整下载两首歌曲、检查服务器上的格式和时长，再移除临时文件；仍不是手机播放验证。输出不含存储密钥或带签名的完整播放地址；私有配置、原始请求日志和临时下载链接不进入公开工程。

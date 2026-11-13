# 原型自己的 HTTPS 路由

这两条路由复用现有 Traefik、证书和 MinIO，没有启动新的测试音频服务，也没有修改网页任务的文件、共享播放器路由或线上 Bot。

- `traefik-minio-cache.yaml`：`music-mini.*` 下的 GET/HEAD `/cloudmusic/*` 转到现有 `minio:9000`，读取已有音频；仍须 MinIO 自身的有效签名。
- `traefik-music-api.yaml`：同一域名下 `/music-api/v1/*` 转到网页任务部署的真实音乐 API。本机当前目标是 `http://172.18.0.1:37834`；服务名称/端口变化后须与该任务对齐，不能盲目复用此地址。

本机实际入口为 `https://music-mini.kmhomelab.cn:2443`，DNS 目前有公网 IPv6；没有确认独立 IPv4 客户端可达性。现有通配证书已验证，没有新建 DNS、证书解析器或宿主机端口。

## 安装和回退

本机将这两个文件分别安装到已有 Traefik 监视的 `data/dynamic_conf/` 目录，名称为 `betago-music-mini-cache.yaml` 和 `betago-music-mini-api.yaml`。安装前先比对同名文件，备份已有版本；采用临时文件后原子替换，权限644。其他文件不改，Traefik不重启。

回退只移除本任务新增的这两个文件；已有同名文件时先恢复该文件备份。不要执行整个 Traefik 栈的 down/up，不操作共享数据库。

## 当前已测和待测

已有两首 MinIO MP3 的 HTTPS Range 请求成功，短期播放链接实测为 `206 → 过期403 → 重新签名206`。新音乐 API 的自有会话、搜索和队列保存通过 HTTPS 返回 JSON。

机器网络检查不代表飞书已能播放。真实服务的网易来源仍可能只返回试听；读取已有 MinIO 音频由网页/API 任务在真实服务启用现有缓存支持后经 resolve 返回，客户端不会在服务明确返回试听/不可播后自行绕过。用户手机上的编译、登录、锁屏、切聊天、自动换曲和声音打断仍待测。

只读音频探针（运行机器需已有 boto3；不把私有配置复制到工程）：

```sh
python3 scripts/probe-owned-audio.py --config /你的私有配置.toml --public-origin https://你的原型地址
```

可选添加 `--ffprobe /实际路径/ffprobe`，会临时完整下载这两首歌曲并检查服务器上的格式和时长，随后移除临时文件；仍不是手机播放验证。

输出只保留 HTTP/TLS、歌曲标识和长度；不输出存储密钥或带签名的播放地址。不要把私有配置、原始请求日志或临时下载链接推到 GitHub。

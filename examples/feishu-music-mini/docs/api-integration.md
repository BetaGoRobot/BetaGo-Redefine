# 自有 API 对齐记录

只读来源：相邻网页/后端任务的 `music-api-contract.md`，标题 `BetaGo Music API v1 — implemented contract (2026-11-13)`。可移植快照见 [music-api-contract.md](music-api-contract.md)，不依赖开发者机器上的相邻目录。后端发布新版契约后再同步。

交付前已复读追加的 `tracks/register` 与旧 descriptor 503 回退说明。本小程序只消费自有稳定 ID 队列/已有歌单，不接收旧网页 `target`、任意 descriptor URL 或内存音源回退；这些网页能力不影响下面已实测的请求映射。

| 小程序行为 | 自有服务请求 |
| --- | --- |
| 新访客 | `POST /music-api/v1/auth/guest {transport:"bearer"}` |
| 已有会话检查 | `GET /music-api/v1/me` |
| 飞书登录接入点 | `tt.requestAccess`（旧端 `tt.login`）→ `POST /music-api/v1/auth/feishu/exchange {code,transport:"bearer"}` |
| 显式恢复已有资料 | `POST /music-api/v1/auth/recover {recoveryCode,transport:"bearer"}` |
| 当前设备队列 | `GET /music-api/v1/queue/{deviceID}` |
| 按稳定 ID 获取元数据 | `POST /music-api/v1/tracks/details {ids:[]}`，按队列 ID 重新排序，保留重复位置 |
| 选择已有歌单 | `GET /music-api/v1/playlists` 与 `GET /playlists/{id}`；只把已有曲目载入当前设备队列 |
| 播放/恢复/过期刷新 | `POST /music-api/v1/tracks/{encodedID}/resolve {}`；每次返回新 URL |
| 保存进度 | `PUT /music-api/v1/queue/{deviceID} {revision,trackIDs,currentIndex,positionMs}` |
| 退出 | `POST /music-api/v1/auth/logout {}`，清除本机凭证与当前音源 |

所有普通请求用 `Authorization: Bearer`，不携带网页 Cookie。路径里的 `sessionId`、open_id、音源 URL 等不作为身份。配置只接受固定自有服务 base URL；曲目 ID、歌单 ID 用 encodeURIComponent 编码，不允许远端重定向选择服务。

进度网络写入最多每 5 秒一次，重要切换/隐藏/暂停立即入串行保存链。SDK 后台回调被系统暂停时不保证写入频率。409 后锁住本设备写入，页面显示冲突，必须“重新连接 / 读取队列”加载新 revision；不会盲目重试覆盖。页面重建保留 App 实例，冷启动从自有队列读取并保持暂停，显式播放再取源。飞书本地 checkpoint 只记录自有队列 ID/位置，当前默认以服务端 CAS 记录恢复，避免覆盖另一客户端的新队列。

`available` 才完整播放；`preview` 和 `unavailable` 显示独立错误并停止。解析响应的 trackID 必须匹配请求，URL 必须属于允许的精确 origin。过期/媒体错误保留当前位置，同一选择最多自动刷新一次；再次失败停止并允许显式重试。不跳过受限曲目、不回退网易账号、不代换来源。

飞书 SDK 回调没有经过文档确认的音源 generation 参数；原生事件跨 src 交错的真实行为仍需设备验证。实现丢弃异步 resolver 的旧结果、过滤已停止前一音源的延迟 pause，并在加载中忽略前一源的 ended。不能由这些本地防护推导官方已知音轨切换问题已经解决。

飞书身份接入已做协议和请求测试，但未进行真实授权。相邻后端当前实现 OAuth token + user_info 校验；小程序预授权码在目标应用和部署配置下是否接受，仍需联调，尤其老客户端 `tt.login` 回退不能只凭请求单测宣称成功。默认采用自有访客/恢复码，飞书身份失败不会静默创建不同身份。

`scripts/probe-service.mjs` 可以对相邻任务的隔离服务执行自有 bearer、空队列、CAS 和退出的实际 HTTP 探针。它会创建独立测试访客，不操作共享 Bot、生产歌单或网易账号：

```sh
MUSIC_TEST_ORIGIN=http://127.0.0.1:YOUR_ISOLATED_PORT node scripts/probe-service.mjs
```

若未执行，交付报告必须写未执行；夹具与后端联调不能互相替代。

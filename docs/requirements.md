# 需求文档

## 目标

- 为多个 Epic 账号自动领取每周免费游戏
- Go 单二进制负责调度、存储、通知与管理；浏览器自动化交给外部 Worker
- 部署简单、资源占用低

## 非目标

- 不在 Go 内实现浏览器自动化或验证码破解
- 不提供多租户 / 公共服务
- 不规避 Epic 的风控或付费内容

## 功能需求

1. **账号管理**：增删改查账号；密码、Cookie/Refresh Token 使用 AES-GCM 加密存储（主密钥来自环境变量）；支持 TOTP 密钥，生成二步验证码交给 Worker。
2. **免费游戏发现**：调用 `https://store-site-backend-static.epicgames.com/freeGamesPromotions?locale=zh-CN&country=CN`，解析当前免费（折扣价为 0 且在促销期内）的游戏。
3. **调度**：默认每小时检查一次；发现新游戏后按账号逐个排队领取，账号间加入可配置随机延迟。
4. **代理**：每个账号可配置独立 HTTP/SOCKS5 代理，传递给 Worker。
5. **重试**：失败自动重试，指数退避，限制最大次数。
6. **状态跟踪**：记录每账号每游戏的状态：已领取 / 已拥有 / 失败（含错误信息）/ 待处理。
7. **通知**：Telegram、微信推送（Server酱 / PushPlus 等）、邮件（SMTP）；领取成功、失败、需人工介入时推送。
8. **Web 管理 + REST API**：查看账号、游戏、领取记录，手动触发领取；API 鉴权。

## 非功能需求

- 单二进制、低内存（目标 < 50MB 常驻）
- SQLite 存储（纯 Go 驱动优先，免 CGO）
- 提供 Docker 镜像与 docker compose（含 Worker 占位服务）
- 日志结构化；凭据不得出现在日志中

## 里程碑

- **M1**：项目脚手架、配置加载、免费游戏 API、/healthz 与 /api/freegames
- **M2**：SQLite 存储、账号管理与加密、TOTP、Claimer（exec/HTTP）对接一种 Worker
- **M3**：调度队列、延迟/重试/退避、状态跟踪、Telegram/微信/邮件通知
- **M4**：Web 管理面板、REST API 鉴权、Docker 镜像发布与文档完善

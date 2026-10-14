# epic-claim

自动领取 Epic Games Store 每周免费游戏的多账号服务（Go 实现）。

> ⚠️ 项目处于早期脚手架阶段（M1），功能尚未完成。

## 简介

epic-claim 采用**混合架构**：Go 负责 Web 管理面板、定时调度、多账号队列、通知与存储；真正的浏览器登录 / hCaptcha / 下单流程交给外部的 **Claimer Worker**（如 [Ronchy2000/epic-freebies-helper](https://github.com/Ronchy2000/epic-freebies-helper)、[claabs/epicgames-freegames-node](https://github.com/claabs/epicgames-freegames-node)，或未来自研的 Playwright/Camoufox + hcaptcha-challenger Python Worker），以独立容器运行，Go 通过可插拔的 `Claimer` 接口（CLI 或 HTTP）驱动。

## 功能

- 多账号管理，凭据加密存储，支持 TOTP 二步验证
- 通过 Epic 公开 `freeGamesPromotions` 接口发现当前免费游戏
- 定时检查（如每小时）+ 按账号分批领取、带随机延迟
- 每账号独立代理
- 失败重试与指数退避
- 状态跟踪：已领取 / 已拥有 / 失败
- 通知：Telegram、微信推送、邮件
- Web 管理面板 + REST API

## 架构

```mermaid
flowchart LR
    subgraph Go[epic-claim (Go 单二进制)]
        W[Web 面板 / REST API]
        S[调度器]
        Q[多账号队列]
        E[Epic 免费游戏 API 客户端]
        N[通知]
        DB[(SQLite)]
    end
    Epic[(Epic 公开 API)]
    subgraph Worker[Claimer Worker 容器]
        P[Playwright / Camoufox<br/>hCaptcha 求解]
    end
    S --> E --> Epic
    S --> Q -->|Claimer 接口: CLI / HTTP| P
    P -->|结果| Q
    Q --> DB
    Q --> N --> TG[Telegram / 微信 / 邮件]
    W --> DB
```

## 目录结构

```
cmd/epic-claim/        程序入口
internal/config/       YAML 配置加载
internal/store/        存储接口与 SQLite 实现（桩）
internal/epic/         Epic 免费游戏 API 客户端
internal/claimer/      Claimer 接口与 exec 实现（桩）
internal/scheduler/    定时调度
internal/notify/       通知接口与 Telegram 实现（桩）
internal/web/          HTTP 服务（/healthz, /api/freegames）
docs/requirements.md   需求文档
```

## 快速开始

本地运行：

```bash
cp config.example.yaml config.yaml
make run
curl http://localhost:8080/api/freegames
```

Docker Compose（规划中）：

```bash
docker compose up -d
```

## 配置示例

```yaml
listen: ":8080"
database: "data/epic-claim.db"
locale: "zh-CN"
country: "CN"
schedule:
  check_interval: "1h"
  account_delay: "2m"
claimer:
  type: "exec"            # exec | http
  command: ["python", "worker.py"]
  http_endpoint: "http://worker:8000"
notify:
  telegram:
    bot_token: ""
    chat_id: ""
```

## 路线图

- [x] M1：脚手架、免费游戏发现、基础 Web
- [ ] M2：账号管理、加密存储、Claimer 对接
- [ ] M3：调度/重试/通知完整实现
- [ ] M4：Web 管理面板、Docker 发布

## 免责声明

- 本项目仅供个人学习与使用。
- 请遵守 Epic Games 服务条款；自动化操作可能导致账号受限或封禁，风险自负。
- 切勿分享你的账号凭据，也不要使用他人账号。

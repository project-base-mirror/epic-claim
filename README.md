# epic-claim

Go 管理服务 + Node/Playwright 领取 Worker，保留原仓库的轻量混合架构。支持多账号标签、免费游戏发现、持久化队列、自动检查和中文管理后台。

**验证边界：管理后台和队列已通过离线测试；浏览器适配器仍是实验实现：2026-10-14 已只读验证一个公开商品页的身份提取，但登录后的结账结构尚未验证。未知页面会返回 unsupported 等待适配验证，可能无法自动领取任何真实游戏，不能当作已验证可用的自动领取服务。没有使用真实 Epic 账号验证登录或领取，也没有实际 Docker 部署。**

## 已实现

- 中文响应式后台：账号启停、会话状态、免费游戏、任务历史、手动触发、重试、明确恢复、定时间隔
- 所有管理 API 使用 Bearer 管理令牌；页面不收集 Epic 密码、验证码、TOTP 或 cookies
- SQLite 持久化账号、任务和调度设置；账号 + namespace + offer 唯一去重
- 定时和手动共用队列；单进程/单数据库锁；单 Worker 执行，避免同账号并发
- 真实 Playwright 浏览器适配器：检查当前活动、当前拥有状态、订单身份、最终数值总价与税额，提交后重新验证拥有状态
- CAPTCHA、2FA、登录、条款和价格检查失败均停下等待操作；不会自动解题、规避检测或接受协议
- 意外重启、超时或下单结果不明记为 unconfirmed，明确恢复前不再次下单
- 明确的下单前失败最多尝试 3 次，1/2 分钟退避；人工阻塞状态不会自动重试

通知、HTTP Worker、密码/TOTP 加密托管、代理和多副本部署尚未实现。原来的通知文件是未接入的占位接口，不会发送通知。

## 本地运行（Linux）

需要 Go 1.24+、Node.js 22+ 和 Playwright Chromium 运行依赖。

```bash
cp config.example.yaml config.yaml
npm --prefix workers/epic ci --ignore-scripts
cd workers/epic && npx playwright install --with-deps chromium && cd ../..
export EPIC_ADMIN_TOKEN='替换成至少32字符的随机私密管理令牌'
export EPIC_PROFILE_ROOT="$PWD/data/profiles"
go run ./cmd/epic-claim -config config.yaml
```

打开 http://127.0.0.1:8080，输入管理令牌。令牌只保存在当前浏览器标签页的 sessionStorage，退出后台会清除。后台默认仅绑定本机；不要在明文公网 HTTP 上传输令牌。远程访问应使用自己的 HTTPS 反向代理或安全隧道。

1. 在配置中设置账号实际商店地区；目录 locale 必须为 en-US，Worker 固定英文以匹配明确按钮；后台仍为中文。
2. 后台添加账号标签，记下编号。每个账号对应隔离的浏览器配置文件。标签不是 Epic 身份验证：系统尚未绑定或核对 Epic account ID，用户必须在每个配置文件内确认登录的是预期账号。
3. 在同一台 Linux 机器的图形桌面，以相同系统用户和 EPIC_PROFILE_ROOT 运行下面的人工会话入口。
4. 用户自己登录和处理挑战或协议，完成后关闭窗口，再在后台触发检查/恢复。
5. 首次运行定时检查关闭。确认账号状态后，在后台明确启用；保存后的调度配置位于 SQLite，重启不会丢失。

```bash
node workers/epic/login.mjs 1
```

该命令只打开浏览器，登录和挑战由用户完成。不要把密码、验证码、cookies、浏览器配置或令牌发给 AI。配置文件包含登录会话，应按凭据保护。

## Docker

```bash
# 编辑 config.docker.yaml，将 country 改为实际地区
export EPIC_ADMIN_TOKEN='替换成至少32字符的随机私密管理令牌'
docker compose build
docker compose up -d
```

仅发布 127.0.0.1:8080；数据库和会话位于 epic-data 私有卷，容器使用非 root 用户。Playwright 镜像与 npm 包必须保持相同版本。使用 Linux 单实例运行，不支持共享数据库多副本。生产部署前自行审查镜像更新、安全策略和 Epic 服务条款。

**无头容器本身没有人工登录图形界面。** 在与卷相同的 Linux 主机上，通过受保护的本地图形会话执行 Worker 登录工具，或先在同一可信环境准备对应配置目录后挂载。不要公开 VNC/调试端口，不要把 profile 打包进镜像或提交到 Git。挂载的私有卷需要 pwuser 可读写。未提供自动远程登录界面；如果没有可信图形会话，请先使用本地 Linux 模式。

## 状态与恢复

| 状态 | 含义/操作 |
| --- | --- |
| pending / running | 排队 / 正在执行 |
| claimed / owned | 已重新验证拥有 / 执行前已拥有，不会重复领取 |
| failed | 明确失败，自动重试耗尽后可点“重试” |
| login_required / two_factor_required / captcha_required | 打开人工会话入口完成对应操作，再点“恢复” |
| terms_required | 用户自行审阅并决定是否接受；程序不代为接受，再明确恢复 |
| unsupported | 页面未提供已验证的精确商品/订单身份结构；需要开发者更新并验证适配器，不能靠重复恢复解决 |
| price_changed | 未能确认最终零金额或活动到期；核查活动，不要强行继续 |
| unconfirmed | 订单可能已提交或服务中断；先查 Epic 库/订单，再明确恢复，Worker 会先查拥有状态 |

“立即检查”发现当前活动并插入新任务。已过期活动在新的不重叠促销周期再次出现时，会更新未完成任务的活动信息；只有明确失败任务可重新排队。阻塞/结果不明任务保留暂停状态，已成功或正在执行的任务保持不变。停用账号阻止后续排队/执行，但不能撤回已发出的订单。关闭定时仅停止新的自动发现，已排队任务继续执行。历史 API 返回最近 500 条任务，数据库仍保留更早记录。

## 接口

`GET /healthz` 无需令牌，仅用于存活探测。以下均需要 `Authorization: Bearer <token>`：

- GET /api/status, /api/accounts, /api/history, /api/freegames
- POST /api/accounts；PUT /api/accounts/{id}，JSON `{ "name": "账号", "disabled": false }`
- POST /api/trigger
- POST /api/jobs/{id}/retry 或 /resume
- PUT /api/schedule，JSON `{ "enabled": true, "interval_seconds": 3600 }`

API 不返回任何登录会话或令牌。请求严格校验 JSON；后台使用同源 CSP 和转义文本。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix workers/epic test
```

测试不登录 Epic、不领取真实游戏、不使用真实凭据。Worker 具体安全设计、页面适配边界和人工登录见 [workers/epic/README.md](workers/epic/README.md)。

## 运维与免责声明

停服后备份整个私有 data/ 卷（SQLite 可能使用 WAL），加密保存，不要只在线复制主 db。恢复时保持数据库与对应 profile 编号一致。被强制终止的 Worker 可能残留 profile 锁；确认所有相关浏览器进程已经退出后按 Worker 文档处理，不要在进程运行时删除锁。

仅用于自己的账号。自动化可能违反平台条款并带来账号限制风险，请自行评估。没有从参考项目复制 GPL 代码；本实现仍采用 MIT 许可。

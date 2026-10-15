# epic-claim

Go 管理服务 + Node/Playwright 领取 Worker，保留原仓库的轻量混合架构。支持多账号标签、免费游戏发现、持久化队列、自动检查和中文管理后台。

**验证边界（2026-10-15）：控制台账号体系、凭据加密、登录 Worker 的状态机/验证码转交已通过离线测试和容器内 Chromium 对本地假登录页的端到端测试；登录 Worker 尚未用真实 Epic 账号验证。实测部署主机上无头 Chromium 打开 Epic 登录页会被 Cloudflare 安全检查拦截（报告为“需要验证码”），这种情况只能按下文“手动登录”处理。领取适配器仍是实验实现：登录后的结账结构尚未验证，未知页面返回 unsupported。**

## 已实现

- 控制台用户：用户名 + 密码注册/登录（bcrypt），HttpOnly + SameSite=Strict 会话 Cookie（经 HTTPS 反代访问时带 Secure），写操作需 CSRF 令牌，登录失败限速
- 第一个注册的用户自动成为管理员；之后注册默认关闭（配置 `allow_registration`，管理员可在“系统设置”中开启）
- 在网页中添加 Epic 账户：Epic 邮箱 + 密码 + 可选 TOTP 密钥，使用 AES-256-GCM 加密保存（密钥来自环境变量 `EPIC_SECRET_KEY`，绝不入库/提交），页面只显示脱敏邮箱
- 点击“登录 Epic”：容器内 Playwright 无头登录，每个账户独立持久化浏览器配置；有 TOTP 密钥时自动填写 2FA；Epic 要求邮箱验证码时状态变为“需要输入验证码”，在网页输入后由 Worker 提交
- 可选的 `EPIC_ADMIN_TOKEN` 继续作为脚本用 API 令牌（`Authorization: Bearer`）
- 免费游戏发现、持久化队列、手动/定时检查、重试、明确恢复；结果不明不重复下单
- CAPTCHA、条款、价格检查失败均停下等待处理；不会自动解题、规避检测或接受协议

通知、HTTP Worker、多副本部署尚未实现。

## 使用流程

1. 打开控制台（例如 https://epic.lan.policoil.top）。系统还没有用户时显示注册页：**第一个注册的账号就是管理员**。注册后自动登录。
2. “账户管理” → “添加账户”：填写名称、Epic 邮箱、Epic 密码，若账号开启了“身份验证器应用”2FA，可填写其 Base32 密钥（TOTP）。保存后密码不会再显示；修改凭据需重新输入密码；可勾选“删除已保存的 Epic 凭据”。
3. 点击该账户的“登录 Epic”。状态依次为：登录中 → 会话就绪 / 需要输入验证码 / 需要验证码（captcha）/ 邮箱或密码错误 / 登录超时。
4. 状态为“需要输入验证码”时，点击“输入验证码”，填入 Epic 发到邮箱的验证码（10 分钟内）。
5. 状态为“会话就绪”后，点击“立即检查”或在“计划设置”中启用定时检查。
6. 管理员可在“系统设置”中开启/关闭新用户注册。

### 遇到 captcha 时：手动登录（后备方案）

无头浏览器无法也不会自动处理人机验证。**2026-10-15 在 31.200 的容器内实测：直接打开 Epic 登录页即被 Cloudflare “Just a moment / One more step” 安全检查拦截（直连和经 7890 代理都会出现），因此网页登录目前大概率显示“需要验证码”。** 此时需在有图形界面的电脑上手动登录一次，再把浏览器配置拷进容器卷，会话之后会被复用：

```bash
# 1) 在有桌面的 Linux 电脑上（本仓库、Node 22、npx playwright install chromium 已就绪）
EPIC_PROFILE_ROOT=$PWD/profiles node workers/epic/login.mjs <账户编号>   # 手动登录，完成后关闭窗口
tar -C profiles -czf account.tgz account-<账户编号>
# 2) 拷到 31.200，导入卷（先停容器释放配置锁）
scp account.tgz root@31.200:/tmp/ && ssh root@31.200
cd /opt/epic-claim && docker compose stop
V=/var/lib/docker/volumes/epic-claim_epic-data/_data/profiles
rm -rf $V/account-<账户编号> && tar -C $V -xzf /tmp/account.tgz && chown -R 1001:1001 $V/account-<账户编号> && rm /tmp/account.tgz
docker compose start
```

注意浏览器版本需与容器内 Playwright（1.55.1）一致或更旧，否则 Chromium 可能拒绝打开较新版本写入的配置。不要把密码、验证码、cookies 或配置目录发给他人。

## 本地运行（Linux）

需要 Go 1.24+、Node.js 22+ 和 Playwright Chromium 运行依赖。

```bash
cp config.example.yaml config.yaml
npm --prefix workers/epic ci --ignore-scripts
cd workers/epic && npx playwright install --with-deps chromium && cd ../..
export EPIC_SECRET_KEY="$(openssl rand -base64 32)"   # 请自行持久保存；丢失后需重新填写所有 Epic 密码
export EPIC_PROFILE_ROOT="$PWD/data/profiles"
go run ./cmd/epic-claim -config config.yaml
```

打开 http://127.0.0.1:8080 注册第一个（管理员）账号。后台默认仅绑定本机；远程访问请经 HTTPS 反向代理（会话 Cookie 会自动带 Secure，需代理设置 `X-Forwarded-Proto: https`）。

## Docker

```bash
# 编辑 config.docker.yaml，将 country 改为实际地区
cat >> .env <<EOT
EPIC_SECRET_KEY=$(openssl rand -base64 32)
EOT
chmod 600 .env
docker compose up -d --build
```

`.env` 已在 .gitignore 中，切勿提交。`EPIC_ADMIN_TOKEN`（可选，≥32 字符）只用于脚本调用 API。仅发布 127.0.0.1:8080；数据库、加密凭据和浏览器配置位于 epic-data 私有卷，容器使用非 root 用户。更换 `EPIC_SECRET_KEY` 会导致已保存凭据无法解密，需要在网页重新填写。

### Nginx 反代示例

```nginx
location / {
    allow 192.168.31.0/24; allow 127.0.0.1; allow 100.64.0.0/10; deny all;
    proxy_pass http://127.0.0.1:18080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
}
```

## 状态与恢复

| 状态 | 含义/操作 |
| --- | --- |
| pending / running | 排队 / 正在执行 |
| claimed / owned | 已重新验证拥有 / 执行前已拥有，不会重复领取 |
| failed | 明确失败，自动重试耗尽后可点“重试” |
| 账户：logging_in / code_required / code_submitted | 登录中 / 等待在网页输入验证码 / 已提交等待确认 |
| 账户：login_failed / timeout / error | 检查邮箱密码后重试“登录 Epic”；页面无法识别时改用手动登录 |
| login_required / two_factor_required / captcha_required | 在账户管理中重新“登录 Epic”；captcha 需手动登录，再点“恢复” |
| terms_required | 用户自行审阅并决定是否接受；程序不代为接受，再明确恢复 |
| unsupported | 页面未提供已验证的精确商品/订单身份结构；需要开发者更新并验证适配器，不能靠重复恢复解决 |
| price_changed | 未能确认最终零金额或活动到期；核查活动，不要强行继续 |
| unconfirmed | 订单可能已提交或服务中断；先查 Epic 库/订单，再明确恢复，Worker 会先查拥有状态 |

“立即检查”发现当前活动并插入新任务。已过期活动在新的不重叠促销周期再次出现时，会更新未完成任务的活动信息；只有明确失败任务可重新排队。阻塞/结果不明任务保留暂停状态，已成功或正在执行的任务保持不变。停用账号阻止后续排队/执行，但不能撤回已发出的订单。关闭定时仅停止新的自动发现，已排队任务继续执行。历史 API 返回最近 500 条任务，数据库仍保留更早记录。

## 接口

`GET /healthz` 无需登录。网页使用 `/auth/state`、`POST /auth/register`、`/auth/login`、`/auth/logout`。`/api/*` 接受会话 Cookie（写操作需 `X-CSRF-Token`，值来自 `/auth/state`）或 `Authorization: Bearer <EPIC_ADMIN_TOKEN>`：

- GET /api/status, /api/accounts, /api/history, /api/freegames, /api/settings
- POST /api/accounts；PUT /api/accounts/{id}，JSON `{ "name": "账号", "disabled": false, "email": "", "password": "", "totp_secret": "", "clear_credentials": false }`
- POST /api/accounts/{id}/login（启动无头登录）；POST /api/accounts/{id}/code，JSON `{ "code": "123456" }`
- PUT /api/settings，JSON `{ "allow_registration": false }`（仅管理员）
- POST /api/trigger；POST /api/jobs/{id}/retry 或 /resume
- PUT /api/schedule，JSON `{ "enabled": true, "interval_seconds": 3600 }`

API 不返回密码、TOTP、会话或 cookies。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix workers/epic test
```

测试不登录 Epic、不领取真实游戏、不使用真实凭据；`login-headless.e2e.test.mjs` 针对本地假登录页（需要 Playwright Chromium，否则跳过）。Worker 具体安全设计、页面适配边界和人工登录见 [workers/epic/README.md](workers/epic/README.md)。

## 运维与免责声明

停服后备份整个私有 data/ 卷（SQLite 可能使用 WAL），加密保存，不要只在线复制主 db。恢复时保持数据库与对应 profile 编号一致。被强制终止的 Worker 可能残留 profile 锁；确认所有相关浏览器进程已经退出后按 Worker 文档处理，不要在进程运行时删除锁。

仅用于自己的账号。自动化可能违反平台条款并带来账号限制风险，请自行评估。没有从参考项目复制 GPL 代码；本实现仍采用 MIT 许可。

# ADR 0003 落地运维笔记（Stage 首次配置）

**日期**：2026-07-24
**范围**：ADR 0003（direct migration URL + 有界 advisory lock）从 stage 首次落地到生产复用的经验
**对应**：issue #35、ADR `docs/adr/0003-migration-direct-url-and-bounded-advisory-lock.md`

> 本笔记记录在 stage 环境首次配置 ADR 0003 时遇到的**四个非显而易见的问题**及解法，供**生产环境**首次配置时直接复用，避免重复踩坑。

---

## 一、配置清单（生产复用）

生产环境（production EC2）首次启用 ADR 0003，需要完成以下步骤。**除第一步外，代码侧已就绪**（stage 与 dev 已合并一致）。

### 1. 服务器 `.env` 配置（必做，人工）

SSH 到生产服务器，在 `/opt/monera-digital/.env` **新增**（不是替换）：

```
# Migration connection (ADR 0003): direct/unpooled endpoint
MIGRATION_DATABASE_URL=postgresql://<user>:<password>@<direct-host>/neondb?sslmode=require&channel_binding=require
```

**关键点**：
- **先备份**：`sudo cp -p /opt/monera-digital/.env /opt/monera-digital/.env.bak.$(date +%Y%m%d%H%M%S)`
- **保留现有 `DATABASE_URL`**（业务用的 pooler 连接串，不动）。ADR 0003 让业务继续用 pooler，迁移单独用 direct。
- `MIGRATION_DATABASE_URL` 的 host **必须不含 `-pooler`**（direct endpoint）。
- `APP_ENV=production`（生产服务器应已配置；若没有需补上，否则 `ResolveMigrationDSN` 会把空 `APP_ENV` 当 local，绕过 fail-closed）。

### 2. 代码侧（已完成，无需重复）

- `deploy-remote.sh::run_migration()` 已能在 migrate 前加载 `.env`（commit `aef93bf`）。
- migrate 二进制已支持 `MIGRATION_DATABASE_URL` 解析、pooler 拒绝、有界锁（commit `a7e6f34` 等）。

---

## 二、踩过的四个坑（生产避坑指南）

### 坑 1：migrate 进程读不到 `.env`（集成缺口）

**现象**：ADR 0003 代码合并后，stage 部署在 migrate 步骤失败：`migration database URL is required`。

**根因**：`deploy-remote.sh` 直接在 SSH shell 里跑 `./monera-migrate`，而 systemd 的 `EnvironmentFile=` 指令**只对 server 服务进程生效**，对 deploy 脚本 spawn 的 migrate 进程无效。导致 `.env` 里的 `MIGRATION_DATABASE_URL` / `APP_ENV` 根本没进 migrate 的环境。

**解法**：`run_migration()` 在跑 migrate 前，逐行加载 `$ENV_FILE` 到当前 shell（commit `657df4d`）。

**生产启示**：任何「在 deploy 脚本里直接跑的二进制」都不会自动继承 systemd EnvironmentFile。必须显式加载。

### 坑 2：`&` 连接串被 shell source 吞掉（解析缺陷）

**现象**：`.env` 里 `MIGRATION_DATABASE_URL=...?sslmode=require&channel_binding=require` 加载后值为空。

**根因**：最初用 shell `source`（`. .env`）加载。但 `&` 是 shell 的后台执行操作符，`source` 遇到 `URL?...&...` 会 `parse error near '&'`，变量读不到。Neon direct URL 的查询串**天然含 `&`**（`channel_binding=require`）。

**解法**：放弃 shell `source`，改为**逐行解析**（`while read` 拆 `KEY=VALUE`，剥离可选引号，与 systemd `EnvironmentFile` 语义一致）（commit `aef93bf`）。

**生产启示**：**永远不要用 shell `source` 加载 `.env`**。systemd 的 `EnvironmentFile` 和 shell `source` 语义不等价。前者是严格的 `KEY=VALUE` 逐行解析（值可含任意字符），后者要经过 shell 词法分析（`&`、`$`、空格、引号都会出问题）。

### 坑 3：独立迁移 role 的权限死结（过度工程）

**现象**：为「零影响 neondb_owner」创建独立迁移 role `md_migrator`，但授权 `public` schema 的 CREATE 权限时卡死。

**根因**：
- `public` schema 的 owner 是 `neondb_owner`，只有 owner 能 GRANT。
- `md_migrator` 虽是 Neon 的 `neon_superuser` 成员，但 **Neon 禁止跨用户 `SET ROLE`**，且 `neon_superuser` **不能代行 schema owner 的 GRANT**。
- 授权必须用 `neondb_owner` 凭据，无法绕过。

**解法**：放弃独立 role。ADR 0003 **只要求 direct URL，不要求独立 role**。直接用 `neondb_owner` + direct host 作为 `MIGRATION_DATABASE_URL`。

**生产启示**：重读 ADR 决策项，别加 spec 没要求的复杂度。ADR 0003 全程未提 role，独立 role 是过度工程。

### 坑 4：squash merge 导致 dev/stage 分叉

**现象**：#60、#61 用 squash 合并进 stage 后，dev（原始 commit）与 stage（squash commit）分叉，内容等价但历史不同。

**根因**：squash 总是生成新 commit hash。即使内容一致，git 视角下 dev 和 stage 是两条线。

**解法**：
1. stage `reset --hard` 到共同祖先（merge-base）
2. 把 stage 直接 `reset` 到 dev 的 HEAD
3. `force-with-lease` 推送
4. 结果：stage 与 dev 指向**同一 commit**，hash 完全一致

**生产启示**：若要 dev 和 stage **完全一致**（hash 相同），不要用 GitHub 的 squash/rebase/merge 按钮（都会改 hash）。用 `git reset + force-with-lease` 让目标分支直接指向源分支的 commit。代价是丢失"PR 合并记录"，但换来零分叉。

---

## 三、验证方法（生产复用）

配置完成后，在生产服务器跑以下验证（只读，不写数据）：

```bash
# SSH 到生产服务器后
cd /opt/monera-digital

# 1. dry-run：确认 direct URL 解析成功 + 读到迁移历史
./monera-migrate -dry-run
# 期望：列出所有 applied 迁移，退出码 0

# 2. 反向验证 pooler 拒绝（构造 pooler host，不实际连接）
MIGRATION_DATABASE_URL="$(grep '^MIGRATION_DATABASE_URL=' .env | cut -d= -f2- | sed 's/@ep-/@ep-test-pooler-/')" \
  ./monera-migrate -dry-run
# 期望：报错 "looks like a connection pooler"，退出码 1
```

---

## 四、Neon 账号与 endpoint 说明

- **测试账号**：`tech@moneradigital.com`（neonctl 已登录，org `org-sweet-unit-30505331`）
- **生产账号**：另一独立账号（首次配置时由运维切换）
- Neon branch 的 endpoint 在控制台可一键切换 pooled/direct。**关闭 connection pooling 开关**即得到 direct host（不含 `-pooler`）。
- 业务 `DATABASE_URL` 保持 pooler（Neon Scale to Zero / 连接复用），迁移 `MIGRATION_DATABASE_URL` 用 direct。

---

## 五、生产发布前检查清单

- [ ] 生产服务器 `/opt/monera-digital/.env` 已备份
- [ ] 已新增 `MIGRATION_DATABASE_URL`（direct，host 不含 `-pooler`）
- [ ] 保留 `DATABASE_URL`（pooler，业务用）
- [ ] `APP_ENV=production` 已配置
- [ ] dry-run 验证通过（读到迁移历史，退出码 0）
- [ ] pooler 拒绝反向验证通过（退出码 1）
- [ ] 下次发布部署 migrate 步骤成功（不再是 `migration database URL is required`）

---

## 六、ADR 0003 的持续维护成本

**配置完成后，ADR 0003 的持续维护成本为零**：

- `MIGRATION_DATABASE_URL` 配一次长期有效，除非 Neon direct endpoint 变更或密码轮换。
- 发版时**不需要**为 ADR 0003 做任何额外操作。
- 唯一的既有维护项是 `.github/workflows/deploy-backend-{stage,prod}.yml` 的 `EXPECTED_MIGRATION_CEILING`（每加新迁移版本时同步），这与 ADR 0003 无关。

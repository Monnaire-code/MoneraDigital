# Safeheron 钱包基础设施接入 — Phase 1 SPEC

> Status: **Approved**
> Last updated: 2026-05-11
> Owner: 待定
> Target ship date: 本周内
> 相关需求核对笔记: Obsidian / 项目 / 结构化产品 / Safeheron钱包基础设施接入需求核对.md

---

## 1. 目标 (Objective)

把 Monera Digital 现有依赖 Monnaire Core API（HTTP）的钱包能力，切换为**直接通过 Safeheron Go SDK** 调用 Safeheron 钱包基础设施，完成「充值地址分配 → 用户充币 → 入账闭环」的最小可用闭环。

### 业务定位

```text
Monera Digital   = 业务系统（用户、产品、订单、资产账户、风控、审计）
Safeheron        = 钱包基础设施（地址生成、充币监听、提币执行、归集、热冷互转）
Monnaire Core API= 上层业务 API（与 Monera Digital 同层）— 钱包业务不再依赖
```

---

## 2. 范围 (Scope)

### 2.1 In Scope（Phase 1 必须完成）

- ✅ Safeheron Go SDK 接入与 adapter 封装
- ✅ 链币配置表（`chains` + `coin_chains`）+ 内存 Registry + 启动加载 + 60s 后台刷新
- ✅ 地址池表（`address_pool`，EVM/TRON 合表）+ 预生成 + 定时补水
- ✅ 用户首次请求时分配 EVM/TRON 充值地址
- ✅ Safeheron Webhook 接收（同步验签落库 + 异步 worker 入账）
- ✅ 入账以 Safeheron `transactionStatus = COMPLETED` 为唯一依据
- ✅ 充值入账更新 `account.balance` + 写 `journal` 流水
- ✅ 异常进入 `MANUAL_REVIEW` + 飞书/邮件告警
- ✅ **入金 KYT 合规筛查（MistTrack via Safeheron）**：以 `AML_KYT_ALERT` webhook 为主路径，初始 + 超时各调 1 次 `/v1/compliance/kyt/report` 兜底；命中 HIGH/SEVERE/MEDIUM/FAILED/SKIPPED/UNKNOWN/UNTRIGGERED 均进 `MANUAL_REVIEW`，仅 LOW 放行入账
- ✅ 切换前端 `/api/wallet/*` 调用链到 Safeheron 路径（Monnaire 路径下线）

### 2.2 Out of Scope（明确不在 Phase 1）

- ❌ 链上二次校验（Etherscan/TronGrid RPC）— **二期或三期**
- ❌ 提现 / Safeheron 提币 API — **二期**
- ❌ 提现地址白名单（前提依赖提现）— **二期**
- ❌ Auto Sweep 归集策略配置 — **二期**
- ❌ API Co-Signer 部署 — **二期归集时必需**
- ❌ Gas Station / 自动加油 — **二期**
- ❌ 老用户数据迁移（生产无真实用户，置换即可）
- ❌ UTXO 链（BTC 等）
- ❌ 完整运营后台 — **三期**
- ❌ 充值金额阈值动态调整（DB 字段已留，运营后台后置）
- ❌ 自定义确认数（Safeheron `COMPLETED` 自带确认数语义）

### 2.3 支持的链与币种

> safeheron_coin_key / decimals 来自 2026-05-11 sandbox 实测（`/v1/coin/list` + V3/V4 实测）。

#### 2.3.1 生产环境（mainnet）coinKey

| Chain | Network Family | Coin | Native | safeheron_coin_key | decimals |
|-------|----------------|------|--------|--------------------|----------|
| ETHEREUM | EVM | ETH | ✓ | `ETH` | 18 |
| ETHEREUM | EVM | USDT | | `USDT_ERC20` | 6 |
| ETHEREUM | EVM | USDC | | `USDC_ERC20` | 6 |
| BSC | EVM | BNB | ✓ | `BNB_BSC` | 18 |
| BSC | EVM | USDT | | `USDT_BEP20` | 18 |
| BSC | EVM | USDC | | `USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET` | 18 |
| TRON | TRON | TRX | ✓ | `TRX` | 6 |
| TRON | TRON | USDT | | `USDT_TRC20` | 6 |

> 注意：BSC 系 USDT/USDC decimals 是 **18**，与 ETHEREUM/TRON 上的 USDT/USDC 不同。业务侧金额计算必须按 `coin_chains.decimals` 区分，禁止假设「USDT 永远 6 位」。

#### 2.3.2 测试环境（local / test）coinKey

> **环境分层原则**：Safeheron 每个 `coinKey` 对应独立的链扫块器（实测 V6/V7 确认）。`ETH` 扫 Ethereum mainnet；`ETH(SEPOLIA)_ETHEREUM_SEPOLIA` 扫 Sepolia testnet。两者是**完全独立的 coinKey**，同一 EVM 钱包可以同时持有但用途分离。
>
> 因此采用**「同 schema、按环境注入不同 coinKey 值」**的设计：
> - `coin_chains` 表 schema 不变，`safeheron_coin_key` 字段在不同环境的数据库里取不同值
> - prod 数据库注入 §2.3.1 表（mainnet）
> - local / test 数据库注入 §2.3.2 表（testnet）
> - 生产 Safeheron 团队（生产 API Key）后台**不会出现** testnet coinKey 选项 → 配置层 + Safeheron 层双重隔离，不可能 prod 误打 testnet 币

| Chain | Coin | safeheron_coin_key (testnet) | dev/test enabled | 备注 |
|-------|------|------------------------------|------------------|------|
| ETHEREUM (Sepolia) | ETH | `ETH(SEPOLIA)_ETHEREUM_SEPOLIA` | ✅ | V6 webhook 实测通 |
| ETHEREUM (Sepolia) | USDC | `USDCOIN_ERC20_ETHEREUM_SEPOLIA` | ✅ | Circle Sepolia USDC token `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238` |
| ETHEREUM (Sepolia) | USDT | — | ❌ | sandbox 测试 team 未配置 |
| BSC (Testnet) | BNB | — | ❌ | sandbox 测试 team 未配置 BSC 测试网 |
| BSC (Testnet) | USDT | — | ❌ | 同上 |
| BSC (Testnet) | USDC | — | ❌ | 同上 |
| TRON (Shasta) | TRX | `TRX(SHASTA)_TRON_TESTNET` | ✅ | list-accounts 反查到 |
| TRON (Shasta) | USDT | — | ❌ | sandbox 测试 team 未配置 |

> **Sandbox testnet 实际可用范围**：D1 通过 `/v1/coin/list`（返回 325 个 mainnet coin，**不含 testnet**）+ list-accounts 反查钱包 1 默认带的 27 个 coin 确认。当前测试 team 只支持 3 个 testnet coinKey（ETH/USDC Sepolia + TRX Shasta），BSC 测试网整条链不通，USDT 测试网整套不通。
>
> **不支持的 5 个 testnet 行不进 dev/test 数据库**（§4.7 testnet seed 只 INSERT 3 行）。生产数据库照旧 8 行不受影响。代码侧不需要任何 `if env` 分支，照样 `SELECT * FROM coin_chains WHERE deposit_enabled=true` 即可。
>
> **环境覆盖差距**：生产支持的 5 个 mainnet 币种（USDT_ERC20 / BNB_BSC / USDT_BEP20 / USDC_BEP20 / USDT_TRC20）在 dev/test 无 testnet 等价物，上生产前不会在 sandbox 跑过 E2E。已记入 §13 风险。
>
> **decimals 一致性**：testnet 的 decimals 与对应 mainnet **相同**（如 BSC testnet USDT 仍是 18），因此 §2.3.1 的 decimals 列对两个环境都生效。

---

## 3. 技术架构

### 3.1 模块组织

```
internal/
├── safeheron/                  # 新增: Safeheron SDK adapter
│   ├── client.go               # SDK 初始化 + 接口封装
│   ├── types.go                # 请求/响应类型
│   ├── signing.go              # RSA 签名 + 验签
│   └── client_test.go          # mock 测试
├── wallet/                     # 新增: 钱包模块根
│   ├── config/                 # 链币配置 Registry
│   │   ├── registry.go         # 内存索引 + 后台刷新
│   │   ├── chain.go            # Chain 模型
│   │   ├── coin_chain.go       # CoinChain 模型
│   │   └── repository.go       # DB 访问
│   ├── pool/                   # 地址池
│   │   ├── manager.go          # 分配 / 补水
│   │   ├── replenisher.go      # 定时补水任务
│   │   └── repository.go
│   └── deposit/                # 充值入账
│       ├── service.go          # 入账状态机
│       ├── webhook.go          # Webhook 处理
│       ├── worker.go           # 异步入账 worker
│       └── repository.go
├── handlers/
│   ├── wallet_handler.go       # 改造: /api/wallet/deposit-address
│   └── safeheron_webhook_handler.go  # 新增: /api/webhooks/safeheron
├── coreapi/                    # ⚠️ DEPRECATED: 标记不删, 二期评估清理
└── services/
    └── wallet.go               # 钱包业务调用从 coreapi 切换到 safeheron
```

### 3.2 与现有系统的关系

| 现有模块 | Phase 1 处理 |
|----------|-------------|
| `internal/coreapi/` | 标记 DEPRECATED，停止 service 层调用，包代码保留（避免破坏测试编译） |
| `internal/services/wallet.go` | 重构: `coreAPIClient` 依赖替换为 `safeheronClient` |
| `internal/handlers/core/` | 不动（独立的 core_account handler，与钱包无关） |
| `user_wallets` / `wallet_creation_requests` | 表保留，Phase 1 后不再写入。前端展示老地址兼容由二期处理 |
| `deposits` 表 | 扩展字段（见 §4.5） |
| `account` 表 | 不动，入账走 `balance += amount` + journal |
| `journal` 表 | 不动，新增 `biz_type = 10`（DEPOSIT） |

---

## 4. 数据模型

### 4.1 `chains` — 链字典

```sql
CREATE TABLE chains (
    code            VARCHAR(32)  PRIMARY KEY,        -- 'ETHEREUM' | 'BSC' | 'TRON'
    name            VARCHAR(64)  NOT NULL,
    description     TEXT,
    network_family  VARCHAR(16)  NOT NULL,           -- 'EVM' | 'TRON'
    chain_id        VARCHAR(32),                     -- EVM '1'/'56'; TRON NULL
    native_symbol   VARCHAR(16)  NOT NULL,
    short_name      VARCHAR(16),                     -- T9 新增: 下拉框主标题 (ETH / BSC / TRON)
    explorer_url    VARCHAR(255),
    icon_url        VARCHAR(255),
    enabled         BOOLEAN      NOT NULL DEFAULT true,
    display_order   INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP    NOT NULL DEFAULT NOW()
);
```

### 4.2 `coins` — 币字典

```sql
CREATE TABLE coins (
    id              SERIAL       PRIMARY KEY,
    symbol          VARCHAR(32)  NOT NULL UNIQUE,         -- 全局唯一: 'USDT' / 'USDC' / 'ETH' / 'BNB' / 'TRX'
    name            VARCHAR(64)  NOT NULL,
    description     TEXT,
    icon_url        VARCHAR(255),
    is_stable       BOOLEAN      NOT NULL DEFAULT false,
    enabled         BOOLEAN      NOT NULL DEFAULT true,
    display_order   INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP    NOT NULL DEFAULT NOW()
);
```

币种元数据（name / icon / is_stable）只在这里维护一份。USDT 改名、换 icon 只改一行。

### 4.3 `coin_chains` — 币链关系

```sql
CREATE TABLE coin_chains (
    id                      SERIAL       PRIMARY KEY,
    chain_code              VARCHAR(32)  NOT NULL REFERENCES chains(code),
    coin_id                 INT          NOT NULL REFERENCES coins(id),
    is_native               BOOLEAN      NOT NULL DEFAULT false,
    token_contract          VARCHAR(128),                 -- native 为 NULL
    decimals                INT          NOT NULL,
    safeheron_coin_key      VARCHAR(64)  NOT NULL UNIQUE,
    min_deposit_amount      VARCHAR(64)  NOT NULL,        -- '1' / '0.001' 字符串
    deposit_enabled         BOOLEAN      NOT NULL DEFAULT true,
    withdraw_enabled        BOOLEAN      NOT NULL DEFAULT false,  -- 二期开启
    required_confirmations  INT          NOT NULL DEFAULT 0,      -- 二期/三期
    token_standard          VARCHAR(16),                          -- T9 新增: 代币标准 (Native/ERC20/BEP20/TRC20)
    estimated_arrival_minutes INT,                                -- T9 新增: 预估到账分钟数 (UI 展示用静态值)
    display_order           INT          NOT NULL DEFAULT 0,
    created_at              TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMP    NOT NULL DEFAULT NOW(),
    UNIQUE(chain_code, coin_id)
);

CREATE INDEX idx_coin_chains_chain_enabled ON coin_chains(chain_code, deposit_enabled);
CREATE INDEX idx_coin_chains_safeheron_key ON coin_chains(safeheron_coin_key);
CREATE INDEX idx_coin_chains_coin ON coin_chains(coin_id);
```

每行 = 「某条链上的某个币」的具体配置（合约、decimals、Safeheron coinKey、最小充值等）。`USDT_ERC20` / `USDT_BEP20` / `USDT_TRC20` 各一行，但都 `coin_id` 指向 `coins` 表里的同一行 USDT。

### 4.4 `address_pool` — 地址池（EVM + TRON 合表）

```sql
CREATE TABLE address_pool (
    id                      SERIAL       PRIMARY KEY,
    network_family          VARCHAR(16)  NOT NULL,        -- 'EVM' | 'TRON'
    address                 VARCHAR(128) NOT NULL,
    safeheron_account_key   VARCHAR(64)  NOT NULL,
    customer_ref_id         VARCHAR(64)  NOT NULL UNIQUE, -- 预生成幂等键
    address_group_key       VARCHAR(64),
    derive_path             VARCHAR(64),

    -- Safeheron 钱包参数（创建时固定: DEPOSIT + hidden + autoFuel=false）
    account_tag             VARCHAR(32),                  -- 'DEPOSIT'
    hidden_on_ui            BOOLEAN      NOT NULL DEFAULT true,
    auto_fuel               BOOLEAN      NOT NULL DEFAULT false,

    -- 分配状态
    status                  VARCHAR(16)  NOT NULL DEFAULT 'AVAILABLE',
                                                          -- AVAILABLE / ASSIGNED / DISABLED / ERROR
    assigned_user_id        INT,
    assigned_at             TIMESTAMP,

    created_at              TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMP    NOT NULL DEFAULT NOW(),

    UNIQUE(network_family, address)
);

CREATE INDEX idx_pool_status_family ON address_pool(network_family, status);
CREATE INDEX idx_pool_user ON address_pool(assigned_user_id);
```

> **AddCoin 规则（系统级，sandbox V3/V4/V6 实测确认）**：
>
> - 地址按 `network_family` 预生成。EVM 钱包一次性 AddCoin **当前环境 `coin_chains` 表里所有 enabled EVM `safeheron_coin_key`**（prod 是 mainnet 系，local/test 是 testnet 系 — 见 §2.3.2）。TRON 钱包同理。
> - 实测确认：同一 EVM accountKey 下 mainnet 系（`ETH/USDT_ERC20/USDC_ERC20/BNB_BSC/USDT_BEP20/USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET`）和 testnet 系（`ETH(SEPOLIA)_ETHEREUM_SEPOLIA` 等）的 AddCoin 全部返回**同一 `0x...` 地址**。所以 100 个 EVM 地址即可同时收所有支持币种，不需要按链或网络分池。
> - **不在 `address_pool` 表里冗余存储每个地址的 AddCoin 列表** — `coin_chains` 是唯一来源。AddCoin V2 (`/v2/account/coin/create`) 幂等（已存在的 coinKey 直接返回原地址）。
> - **环境隔离**：生产部署只会执行 prod 数据库的 mainnet coinKey 列表；local/test 部署只会执行 testnet coinKey 列表。代码逻辑不需要任何 `if env == "prod"` 分支 — 单一来源就是当前环境的 `coin_chains` 表。
>
> 未来加新币（如 DAI）：运营在 `coin_chains` 加一行 → 跑 `cmd/pool_recoin/main.go` 对相应 `network_family` 的所有地址执行该新 coinKey 的 AddCoin。

### 4.5 `safeheron_webhook_events` — Webhook 原始事件

```sql
CREATE TABLE safeheron_webhook_events (
    id              SERIAL       PRIMARY KEY,
    event_id        VARCHAR(128) NOT NULL UNIQUE,        -- Safeheron 事件 ID, 幂等键
    event_type      VARCHAR(64)  NOT NULL,
    safeheron_tx_key VARCHAR(128),
    customer_ref_id VARCHAR(128),
    raw_payload     JSONB        NOT NULL,                -- 全量原始 payload
    process_status  VARCHAR(16)  NOT NULL DEFAULT 'PENDING',
                                                          -- PENDING / PROCESSING / DONE / FAILED
    process_attempts INT         NOT NULL DEFAULT 0,
    error_message   TEXT,
    received_at     TIMESTAMP    NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMP
);

CREATE INDEX idx_webhook_status ON safeheron_webhook_events(process_status);
CREATE INDEX idx_webhook_tx_key ON safeheron_webhook_events(safeheron_tx_key);
```

> 保留策略：90 天后归档（Phase 1 不实现归档脚本，DB 容量评估二期）

### 4.6 `deposits` 表扩展

```sql
ALTER TABLE deposits
    ADD COLUMN safeheron_tx_key      VARCHAR(128),
    ADD COLUMN safeheron_coin_key    VARCHAR(64),
    ADD COLUMN chain_code            VARCHAR(32) REFERENCES chains(code),
    ADD COLUMN coin_chain_id         INT         REFERENCES coin_chains(id),
    ADD COLUMN block_height          BIGINT,
    ADD COLUMN block_hash            VARCHAR(128),

    -- Safeheron 最新状态 (来自 webhook eventDetail.transactionStatus)
    ADD COLUMN safeheron_status      VARCHAR(32),   -- SUBMITTED/SIGNING/BROADCASTING/CONFIRMING/COMPLETED/FAILED/CANCELLED/REJECTED
    ADD COLUMN safeheron_sub_status  VARCHAR(64),   -- transactionSubStatus (CONFIRMED 等 41 种, 详见 §6.4)
    -- 单调状态序号, 用于 webhook 乱序保护 (实测确认 Safeheron 不保证顺序, COMPLETED 之后会重发 CONFIRMING)
    -- 更新时仅当新事件的 status_rank >= 当前值才覆盖, 防止状态回退
    ADD COLUMN status_rank           SMALLINT NOT NULL DEFAULT 0,

    ADD COLUMN credited_at           TIMESTAMP,
    ADD COLUMN failed_reason         TEXT,

    -- ============ KYT / AML 合规筛查字段 (Phase 1 接入 MistTrack) ============
    -- amlScreeningTriggeredState: IN_PROGRESS / TRIGGERED / UNTRIGGERED / NULL(尚未发起初查)
    ADD COLUMN aml_screening_state   VARCHAR(16),
    -- 业务汇总风险等级 (amlList 中取最高): LOW / MEDIUM / HIGH / SEVERE / UNKNOWN / FAILED / SKIPPED
    -- LOW → 放行入账; 其余 → MANUAL_REVIEW (含 UNTRIGGERED 这种"该币种不支持 KYT"场景)
    ADD COLUMN aml_risk_level        VARCHAR(8),
    -- 最后一次 KYT 评估时间 (来自 amlList[].lastUpdateTime 最大值)
    ADD COLUMN aml_evaluated_at      TIMESTAMP,
    -- 完整 amlList 数组原样存档 (含各 provider 的 status / riskLevel / payload), 用于审计 + 未来扩展三家服务商
    ADD COLUMN aml_list              JSONB;

-- 防重复入账幂等键 (V6/V7 实测确认: 一笔链上交易对应一个 Safeheron txKey, 无 logIndex 概念)
CREATE UNIQUE INDEX idx_deposits_safeheron_tx_key
    ON deposits(safeheron_tx_key)
    WHERE safeheron_tx_key IS NOT NULL;

-- KYT_PENDING 状态下 worker 需周期扫描判超时, 走 updated_at 索引
CREATE INDEX idx_deposits_kyt_pending
    ON deposits(updated_at)
    WHERE status = 'KYT_PENDING';

-- 状态枚举 (内部业务状态, 区别于 safeheron_status)
ALTER TABLE deposits ADD CONSTRAINT ck_deposits_status
    CHECK (status IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
                      'KYT_PENDING',                          -- Phase 1 新增: 链上 COMPLETED 但 KYT 评估未结束
                      'CREDITED', 'FAILED', 'MANUAL_REVIEW'));
```

> **为什么删除 `log_index`**：SPEC v1.1/v1.2 假设 webhook payload 携带 `logIndex` 用于区分同一 tx 内多笔代币转账。V7 实测 + 官方文档确认 **Safeheron webhook eventDetail 不包含 logIndex 字段**：每笔被 Safeheron 识别的入账（不管 ERC-20 转账还是原生币）都用唯一 `txKey` 标识。一笔链上 tx 触发多个 Safeheron 入账事件时，会有多个独立 txKey，天然不冲突。
>
> **`status_rank` 单调字段语义**（V6 实测必须做的乱序保护）：
>
> | safeheron_status | rank | 含义 |
> |---|---|---|
> | SUBMITTED / SIGNING / BROADCASTING | 10 / 20 / 30 | 出账早期阶段（入账场景不会出现） |
> | CONFIRMING | 50 | 链上已上链待确认数 |
> | COMPLETED | 100 | 已完成 |
> | FAILED / CANCELLED / REJECTED | 90 | 失败终态（在 COMPLETED 之前到达） |
>
> 更新 deposits 时执行：`UPDATE deposits SET safeheron_status=?, safeheron_sub_status=?, status_rank=? WHERE safeheron_tx_key=? AND status_rank <= ?`。如果新事件 rank 小于当前 rank（如 COMPLETED 之后又来 CONFIRMING），WHERE 条件不匹配，UPDATE 静默成功 0 行，状态不回退。

> `account` 表也需要补一个唯一约束以支持入账时的 ON CONFLICT upsert：
>
> ```sql
> CREATE UNIQUE INDEX IF NOT EXISTS idx_account_user_currency
>     ON account(user_id, currency);
> ```
>
> （Phase 1 迁移脚本里加，不改 account 表结构）

### 4.7 初始数据（Seed）

Seed 通过两个迁移脚本组合，**`coin_chains` 的 `safeheron_coin_key` 字段按 `APP_ENV` 注入不同值**：

- `0XX_seed_safeheron_phase1_base.go` — 基础数据（`chains` + `coins`，所有环境一致）
- `0YY_seed_safeheron_phase1_coinchains.go` — `coin_chains` seed，**读 `APP_ENV` 选择 mainnet 或 testnet coinKey 集合**

```sql
-- ============ 基础数据（所有环境一致）============
-- chains
INSERT INTO chains (code, name, network_family, chain_id, native_symbol, explorer_url, display_order) VALUES
('ETHEREUM', 'Ethereum',        'EVM',  '1',  'ETH', 'https://etherscan.io',  10),
('BSC',      'BNB Smart Chain', 'EVM',  '56', 'BNB', 'https://bscscan.com',   20),
('TRON',     'TRON',            'TRON', NULL, 'TRX', 'https://tronscan.org',  30);

-- coins
INSERT INTO coins (symbol, name, is_stable, display_order) VALUES
('ETH',  'Ether',      false, 10),
('BNB',  'BNB',        false, 20),
('TRX',  'TRON',       false, 30),
('USDT', 'Tether USD', true,  40),
('USDC', 'USD Coin',   true,  50);
```

```sql
-- ============ coin_chains: 生产 (APP_ENV=production) ============
INSERT INTO coin_chains (chain_code, coin_id, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, display_order)
    SELECT 'ETHEREUM', id, true,  NULL,                                          18, 'ETH',        '0.001', 10 FROM coins WHERE symbol='ETH'
UNION ALL SELECT 'ETHEREUM', id, false, '0xdAC17F958D2ee523a2206206994597C13D831ec7', 6,  'USDT_ERC20', '1',     20 FROM coins WHERE symbol='USDT'
UNION ALL SELECT 'ETHEREUM', id, false, '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48', 6,  'USDC_ERC20', '1',     30 FROM coins WHERE symbol='USDC'
UNION ALL SELECT 'BSC',      id, true,  NULL,                                          18, 'BNB_BSC',    '0.005', 40 FROM coins WHERE symbol='BNB'
UNION ALL SELECT 'BSC',      id, false, '0x55d398326f99059fF775485246999027B3197955',  18, 'USDT_BEP20', '1',     50 FROM coins WHERE symbol='USDT'
UNION ALL SELECT 'BSC',      id, false, '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d',  18, 'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', '1', 60 FROM coins WHERE symbol='USDC'
UNION ALL SELECT 'TRON',     id, true,  NULL,                                          6,  'TRX',        '1',     70 FROM coins WHERE symbol='TRX'
UNION ALL SELECT 'TRON',     id, false, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',          6,  'USDT_TRC20', '1',     80 FROM coins WHERE symbol='USDT';
```

```sql
-- ============ coin_chains: 测试 (APP_ENV in (local, test)) ============
-- 只插入 sandbox 当前支持的 3 个 testnet 行。不支持的 5 个币种不进 dev/test 数据库。
INSERT INTO coin_chains (chain_code, coin_id, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, display_order)
    SELECT 'ETHEREUM', id, true,  NULL,                                          18, 'ETH(SEPOLIA)_ETHEREUM_SEPOLIA',  '0.0001', 10 FROM coins WHERE symbol='ETH'
UNION ALL SELECT 'ETHEREUM', id, false, '0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238', 6,  'USDCOIN_ERC20_ETHEREUM_SEPOLIA', '0.1',    30 FROM coins WHERE symbol='USDC'
UNION ALL SELECT 'TRON',     id, true,  NULL,                                          6,  'TRX(SHASTA)_TRON_TESTNET',       '0.1',    70 FROM coins WHERE symbol='TRX';
```

> ✅ 生产 8 行 `safeheron_coin_key` / `token_contract` / `decimals` 已通过 2026-05-11 sandbox `/v1/coin/list` 实测确认（产出: `~/scratch/safeheron-sandbox-test/results/v2-list-coins.md`）。
>
> ✅ Testnet 3 行通过 V6 webhook 实测 + `list-accounts` 钱包 1 默认币种反查确认。Sepolia USDC 合约 `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238` 是 Circle 官方测试网部署地址。
>
> ❌ Sandbox 测试 team 不支持 BSC 测试网、Sepolia USDT、Shasta USDT 共 5 个币种。对应 `coin_chains` 行不进 dev/test 数据库，等同于 dev/test 环境永远 `SELECT WHERE deposit_enabled=true` 拿不到这些 chain+coin 组合 → 代码无需任何 `if env` 分支。
>
> ⚠️ **环境覆盖差距**：生产 8 个币种中有 5 个（USDT_ERC20 / BNB_BSC / USDT_BEP20 / USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET / USDT_TRC20）**无法在 dev/test 跑 E2E**。上生产前的 staging 环境需用 prod Safeheron team 的真小额做最终验证。已记入 §13。
>
> BSC 系 USDT/USDC decimals=18 与 ETHEREUM/TRON 上的 USDT/USDC（=6）不同，业务侧金额计算必须读 `coin_chains.decimals`，testnet 同此 decimals。

#### 4.7.1 T9 充值展示字段 Seed（Migration 022）

T9 引入选币→选链 UX 后，新增三个展示字段。Migration `022_add_deposit_display_fields` 同时 `ADD COLUMN IF NOT EXISTS` 并对存量行回填默认值（所有 UPDATE 用 `WHERE col IS NULL` 保持幂等）：

```sql
-- chains.short_name
UPDATE chains SET short_name = 'ETH'  WHERE code = 'ETHEREUM' AND short_name IS NULL;
UPDATE chains SET short_name = 'BSC'  WHERE code = 'BSC'      AND short_name IS NULL;
UPDATE chains SET short_name = 'TRON' WHERE code = 'TRON'     AND short_name IS NULL;

-- coin_chains.{token_standard, estimated_arrival_minutes}
UPDATE coin_chains SET token_standard='Native', estimated_arrival_minutes=2
    WHERE chain_code='ETHEREUM' AND is_native=true  AND token_standard IS NULL;
UPDATE coin_chains SET token_standard='ERC20',  estimated_arrival_minutes=2
    WHERE chain_code='ETHEREUM' AND is_native=false AND token_standard IS NULL;
UPDATE coin_chains SET token_standard='Native', estimated_arrival_minutes=1
    WHERE chain_code='BSC'      AND is_native=true  AND token_standard IS NULL;
UPDATE coin_chains SET token_standard='BEP20',  estimated_arrival_minutes=1
    WHERE chain_code='BSC'      AND is_native=false AND token_standard IS NULL;
UPDATE coin_chains SET token_standard='Native', estimated_arrival_minutes=1
    WHERE chain_code='TRON'     AND is_native=true  AND token_standard IS NULL;
UPDATE coin_chains SET token_standard='TRC20',  estimated_arrival_minutes=1
    WHERE chain_code='TRON'     AND is_native=false AND token_standard IS NULL;
```

> 这些值是 UI 静态展示用（D-21）。新增链/币时在新 migration 里补行即可，不进入业务逻辑判断。

---

## 5. 配置加载与缓存（Registry）

### 5.1 设计

```go
// internal/wallet/config/registry.go
type Registry struct {
    mu              sync.RWMutex
    chains          map[string]*Chain        // chain_code → Chain
    coins           map[string]*Coin         // symbol → Coin
    coinsByID       map[int]*Coin            // coin_id → Coin
    coinChains      map[string]*CoinChain    // "ETHEREUM|USDT" → CoinChain
    bySHKey         map[string]*CoinChain    // safeheron_coin_key → CoinChain
    byChain         map[string][]*CoinChain  // chain_code → []*CoinChain (按链列出)
    repo            Repository
    refreshInterval time.Duration
    log             Logger
}

// CoinChain 加载时解引用 Chain 和 Coin, 业务代码可一步拿到完整信息
type CoinChain struct {
    ID                 int
    ChainCode          string
    CoinID             int
    Chain              *Chain   // 加载时引用
    Coin               *Coin    // 加载时引用
    IsNative           bool
    TokenContract      string
    Decimals           int
    SafeheronCoinKey   string
    MinDepositAmount   string
    DepositEnabled     bool
    WithdrawEnabled    bool
    // ...
}

func (r *Registry) Load(ctx context.Context) error
func (r *Registry) StartBackgroundRefresh(ctx context.Context)
func (r *Registry) GetChain(code string) (*Chain, bool)
func (r *Registry) GetCoin(symbol string) (*Coin, bool)
func (r *Registry) GetCoinChain(chainCode, symbol string) (*CoinChain, bool)
func (r *Registry) GetCoinChainBySafeheronKey(key string) (*CoinChain, bool)
func (r *Registry) ListEnabledCoinChainsByChain(chainCode string) []*CoinChain
```

### 5.2 行为规则

| 时机 | 行为 | 失败处理 |
|------|------|---------|
| 启动 | `container.NewContainer()` 调用 `Registry.Load()` | **panic 启动失败**（前置必需依赖） |
| 后台 | 每 60s 调用 `Registry.Load()` 原子替换内存 | **保留旧值** + 日志 WARN + 告警，**不清空、不 panic** |
| 业务读取 | 走内存 map（RLock，亚微秒） | N/A |

刷新策略：构建**新的** map → 整体替换旧 map，避免读到半成品。

### 5.3 配置项

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `WALLET_CONFIG_REFRESH_INTERVAL` | `60s` | 后台刷新间隔 |

---

## 6. 业务流程

### 6.1 地址池预生成（运维 / 部署阶段）

```
人工触发部署脚本 / cmd/pool_init/main.go
  → 对每个 enabled network_family 循环 N 次（默认 EVM=100, TRON=100）:
      1. 生成 customer_ref_id (UUID)
      2. Safeheron 创建 Asset Wallet (POST /v1/account/create)
         - accountTag   = "DEPOSIT"
         - hiddenOnUI   = true
         - autoFuel     = false
         - coinKeyList  = SELECT safeheron_coin_key FROM coin_chains
                          WHERE chain_code IN (SELECT code FROM chains WHERE network_family=?)
                            AND deposit_enabled=true
                          (prod 取 mainnet 集合, local/test 取 testnet 集合 — §2.3.2)
      3. 写入 address_pool (status=AVAILABLE, network_family, address, safeheron_account_key, customer_ref_id)
         (AddCoin 列表来自 coin_chains 表, 不在 address_pool 持久化)
  → 失败重试: 指数退避 5s/30s/120s, 3 次后落 ERROR 状态供人工排查
```

> 不再按 chain 分池。V4 sandbox 实测证实：单个 EVM accountKey 同时 AddCoin ETH+BSC 全部 coinKey 共享同一 `0x...` 地址。100 个 EVM 钱包即可同时承载 ETH 链和 BSC 链所有币种的入金，钱包总数比"按 chain 各 100"方案减半。

### 6.2 地址池补水（定时任务，Phase 1 范围）

```
internal/wallet/pool/replenisher.go
  → 复用现有 internal/scheduler 框架
  → 每 10 分钟检查一次:
      SELECT network_family, COUNT(*) FILTER (WHERE status='AVAILABLE')
        FROM address_pool GROUP BY network_family;
  → 若 AVAILABLE < 50（EVM）或 < 50（TRON）, 补到 100
  → 调用 §6.1 同样的预生成逻辑
  → 单次补水失败不阻塞下次执行
```

### 6.3 用户充值地址分配

```
前端 → GET /api/wallet/deposit-address?network_family=EVM (或 TRON)
  ↓
Handler:
  1. 校验 JWT
  2. 查询用户在该 network_family 下是否已有分配:
     SELECT * FROM address_pool WHERE assigned_user_id=? AND network_family=?
  3. 已有 → 返回（幂等）
  4. 无 → 加事务 + SELECT ... FOR UPDATE SKIP LOCKED 取一个 AVAILABLE:
     UPDATE address_pool
       SET status='ASSIGNED', assigned_user_id=?, assigned_at=NOW()
       WHERE id=? AND status='AVAILABLE'
  5. 返回 { address, network_family, supported_coins: [...] }
```

并发保护：DB 事务 + `FOR UPDATE SKIP LOCKED`，防止两个请求拿到同一地址。

### 6.4 充值入账闭环

> **Webhook payload 真实结构（V7 实测 + 官方文档确认）**：
>
> 信封层（HTTP body 顶层）：
> ```json
> {
>   "timestamp":  "1778491846329",
>   "rsaType":    "ECB_OAEP",
>   "aesType":    "GCM_NOPADDING",
>   "key":        "<RSA-OAEP 加密的 AES key+IV, base64>",
>   "bizContent": "<AES-GCM 加密的业务内容, base64>",
>   "sig":        "<SHA256WithRSA 签名, base64>"
> }
> ```
>
> 解密后业务层：
> ```json
> {
>   "eventType": "TRANSACTION_CREATED" | "TRANSACTION_STATUS_CHANGED" | ... (14 种, 见下),
>   "eventDetail": {
>     "txKey": "txstgyq358c7214a18o79f711d936b5001",
>     "txHash": "0x...",
>     "blockHeight": 10832108,
>     "coinKey": "ETH(SEPOLIA)_ETHEREUM_SEPOLIA",
>     "transactionStatus": "CONFIRMING" | "COMPLETED" | ... (8 种),
>     "transactionSubStatus": "CONFIRMED" | null | ... (41 种),
>     "transactionDirection": "INFLOW" | "OUTFLOW" | "INTERNAL_TRANSFER",
>     "transactionType": "NORMAL",
>     "txAmount": "0.0002",
>     "txFee": "0.000000021000483",
>     "feeCoinKey": "ETH(SEPOLIA)_ETHEREUM_SEPOLIA",
>     "destinationAccountKey": "accountswkny358...",
>     "destinationAccountType": "VAULT_ACCOUNT",
>     "destinationAddress": "0xB2355506...",
>     "sourceAddress": "0x77A50402...",
>     "sourceAccountType": "UNKNOWN",
>     "destinationAddressList": [{ "address": ..., "amount": ..., "memo": null, ... }],
>     "sourceAddressList":      [{ "address": ..., "isSourcePhishing": false }],
>     "customerRefId": null,
>     "nonce": "93",
>     "createTime": 1778491846223,
>     "completedTime": 1778491997748 | null,
>     "amlLock": "NO",
>     "isDestinationPhishing": false,
>     "isSourcePhishing": false,
>     "replaceTxHash": null,             // RBF/加速交易引用的原 hash
>     "replacedTxKey": null,             // RBF 替换后新 txKey 关联的旧 txKey
>     "replacedCustomerRefId": null
>     // ... 其他字段见官方文档 https://docs.safeheron.com/api/zh.html#Webhook
>   }
> }
> ```
>
> **关键字段确认**（V7 实测样本）：
> - 真实字段位于 `eventDetail` **嵌套对象**下，不在顶层
> - `eventType` 不是 `transactionStatusChange`（v1.1 猜测），是 `TRANSACTION_CREATED` / `TRANSACTION_STATUS_CHANGED`
> - `transactionDirection` 取值是 `INFLOW`（不是 `incoming`）
> - **没有 `logIndex` 字段**
> - 入账以 `transactionStatus='COMPLETED' AND transactionSubStatus='CONFIRMED'` 为唯一安全条件
>
> **Phase 1 关心的 eventType**（共 14 种，其他 12 种统一标记 DONE 不入账）：
> - `TRANSACTION_CREATED` — 首次创建（含首次扫到入账）
> - `TRANSACTION_STATUS_CHANGED` — 状态变更（COMPLETED 通常在此事件）
> - （2025-11-11 起 Safeheron 不再在 CREATED 时同步发同样内容的 STATUS_CHANGED；STATUS_CHANGED 仅在状态真变才发）

```
Safeheron → POST /api/webhooks/safeheron  (JSON body 含 sig/key/bizContent/timestamp 三件套)
  ↓
[同步阶段] (HTTP handler 内, 必须 < 5s)
  1. 读取 body 反序列化为 webhook.WebHook 结构（sig/key/bizContent/timestamp/rsaType/aesType）
     ⚠️ 签名不在 HTTP Header, 在 body 的 sig 字段（v1.1 假设错误已修正）
  2. 调 SDK webhook.WebhookConverter.Convert(env) 一步完成验签 + AES 解密
     - 验签: SHA256WithRSA, 串构造 = "bizContent=...&key=...&timestamp=..." (按字典序, 不含 rsaType/aesType)
     - 解密: RSA/ECB/OAEPWithSHA-256AndMGF1Padding 解 key, AES/GCM/NoPadding 解 bizContent
     失败 → 401, 不落库, 触发告警
  3. 反序列化得到 { eventType, eventDetail }
  4. event_id 幂等键 = eventDetail.txKey + ':' + eventDetail.transactionStatus
     (文档明确说会有重复推送, 同一 (txKey, status) 是同一逻辑事件)
     INSERT INTO safeheron_webhook_events (event_id ON CONFLICT DO NOTHING)
     已存在 → 仍然返回成功 ack (Safeheron 仍会按其重试机制继续发, 直到收到 SUCCESS)
  5. ⚠️ 必须返回 HTTP 200 且 body = {"code":"200","message":"SUCCESS"}
     任何偏离 (200+不对的 message / 非 200) 都会触发 Safeheron 重试 30s→1m→5m→1h→12h→24h
     共 6 次. (V6 实测: 我们写 "ok" 收到了 6 倍重复)

[异步阶段] (worker goroutine, 轮询 PENDING 事件, 整个流程单事务)

  BEGIN;

  -- 并发防御 #1: SELECT FOR UPDATE SKIP LOCKED 锁住事件, 多 worker 各取各的
  SELECT * FROM safeheron_webhook_events
   WHERE process_status='PENDING'
   ORDER BY received_at
   LIMIT 1
   FOR UPDATE SKIP LOCKED;

  -- 解析 eventDetail
  d := raw_payload.eventDetail
  eventType := raw_payload.eventType

  -- 早退条件 (任一不满足都标 DONE 跳过, 不入账)
  IF eventType NOT IN ('TRANSACTION_CREATED','TRANSACTION_STATUS_CHANGED') → DONE 跳过
  IF d.transactionDirection != 'INFLOW' → DONE 跳过 (Phase 1 只关心入金)

  -- 路由判定
  pool := SELECT * FROM address_pool WHERE address=d.destinationAddress
  IF pool IS NULL OR pool.assigned_user_id IS NULL → MANUAL_REVIEW (reason=ADDRESS_UNASSIGNED)

  coinChain := Registry.GetCoinChainBySafeheronKey(d.coinKey)
  IF coinChain IS NULL → MANUAL_REVIEW (reason=COIN_UNSUPPORTED)

  IF parse(d.txAmount) < coinChain.MinDepositAmount → MANUAL_REVIEW (reason=BELOW_MIN_AMOUNT)

  -- UPSERT deposits (V6 实测: 同一 txKey 会推多条 status 不同的事件)
  -- 并发防御 #2: deposits.safeheron_tx_key UNIQUE
  -- 并发防御 #3: status_rank 单调递增防回退 (见 §4.6)
  newRank := rankOf(d.transactionStatus)  -- COMPLETED=100, CONFIRMING=50, FAILED/CANCELLED/REJECTED=90, ...

  INSERT INTO deposits (safeheron_tx_key, user_id, amount, asset, chain_code, coin_chain_id,
                        safeheron_status, safeheron_sub_status, status_rank,
                        block_height, status)
  VALUES (d.txKey, pool.assigned_user_id, d.txAmount, coinChain.Coin.Symbol,
          coinChain.ChainCode, coinChain.ID,
          d.transactionStatus, d.transactionSubStatus, newRank,
          d.blockHeight, 'PENDING')
  ON CONFLICT (safeheron_tx_key) DO UPDATE
    SET safeheron_status      = EXCLUDED.safeheron_status,
        safeheron_sub_status  = EXCLUDED.safeheron_sub_status,
        status_rank           = EXCLUDED.status_rank,
        block_height          = EXCLUDED.block_height,
        updated_at            = NOW()
    WHERE deposits.status_rank <= EXCLUDED.status_rank  -- 单调保护
  RETURNING id, status, status_rank;

  -- 入账触发条件: 链上 COMPLETED + CONFIRMED 且业务 status 仍是 PENDING
  -- ⚠️ Phase 1 在 CREDITED 之前必须插入 KYT 检查, 详见 §6.5
  IF d.transactionStatus = 'COMPLETED'
     AND d.transactionSubStatus = 'CONFIRMED'
     AND deposits.status = 'PENDING'
  THEN
      -- ============ KYT 初始查询 (能力 A 辅路径第一次) ============
      -- 若 KYT_ENABLED=false (仅非 prod 生效, 见 §9.6) → 跳过此分支直接走原入账逻辑, 用于本地 / sandbox 端到端不依赖 Safeheron AML
      IF kytEnabled THEN
          report := SafeheronClient.KytReport(d.txKey)   -- /v1/compliance/kyt/report
          UPDATE deposits SET aml_screening_state=report.amlScreeningTriggeredState,
                              aml_list=report.amlList,
                              aml_evaluated_at=maxLastUpdateTime(report.amlList)
                        WHERE id=deposits.id;

          CASE report.amlScreeningTriggeredState
            WHEN 'UNTRIGGERED' THEN
              -- 该币种未启用 KYT 筛查 → 保守策略, 进人工审核 (K-8)
              MANUAL_REVIEW (reason='KYT_UNTRIGGERED', alertLevel=WARN)
              GOTO 事件标 DONE
            WHEN 'IN_PROGRESS' THEN
              -- 评估进行中 → 进 KYT_PENDING 等 AML_KYT_ALERT webhook (主路径)
              UPDATE deposits SET status='KYT_PENDING', updated_at=NOW() WHERE id=deposits.id;
              GOTO 事件标 DONE
            WHEN 'TRIGGERED' THEN
              risk := summarizeRiskLevel(report.amlList)  -- 见 §6.5 处置矩阵
              IF risk = 'PENDING_PROVIDER' THEN  -- amlList 内任一 provider status=PENDING
                UPDATE deposits SET status='KYT_PENDING' WHERE id=deposits.id;
                GOTO 事件标 DONE
              END IF
              UPDATE deposits SET aml_risk_level=risk WHERE id=deposits.id;
              IF risk != 'LOW' THEN
                MANUAL_REVIEW (reason='KYT_RISK_'||risk, alertLevel=alertLevelOf(risk))
                GOTO 事件标 DONE
              END IF
              -- risk = LOW → 落到下方入账分支
          END CASE
      END IF

      -- ============ 入账动作 (KYT 通过或 KYT_ENABLED=false) ============
      -- 并发防御 #4: account upsert (PG 原子)
      INSERT INTO account (user_id, currency, balance)
      VALUES (pool.assigned_user_id, coinChain.Coin.Symbol, d.txAmount)
      ON CONFLICT (user_id, currency) DO UPDATE
        SET balance    = account.balance + EXCLUDED.balance,
            updated_at = NOW(),
            version    = account.version + 1;

      INSERT INTO journal (serial_no, user_id, account_id, amount,
                           balance_snapshot, biz_type, ref_id, created_at)
      VALUES (gen_serial(), pool.assigned_user_id, account_id, d.txAmount,
              new_balance, 10 /* DEPOSIT */, deposits.id, NOW());

      UPDATE deposits SET status='CREDITED', credited_at=NOW() WHERE id=deposits.id;
  END IF;

  -- 失败终态处理
  IF d.transactionStatus IN ('FAILED','CANCELLED','REJECTED')
     AND deposits.status NOT IN ('CREDITED','FAILED')
  THEN
      UPDATE deposits SET status='FAILED', failed_reason=d.transactionSubStatus WHERE id=deposits.id;
      -- 不写 journal, 不调 account
      触发告警
  END IF;

  -- [MANUAL_REVIEW 分支] 早退场景
  -- INSERT INTO deposits status='MANUAL_REVIEW', failed_reason=?
  -- 不更新 account, 不写 journal
  -- 触发飞书/邮件告警 (异步, 失败不阻塞事务)

  UPDATE safeheron_webhook_events SET process_status='DONE', processed_at=NOW() WHERE id=?;
  COMMIT;
```

**并发防御层级**（实测后 5 → 6 层）：

| 场景 | 防御 |
|------|------|
| Safeheron 重推同一 (txKey, status) 事件 | `safeheron_webhook_events.event_id` UNIQUE（事件键 = txKey:status） |
| 多 worker 同时拉同一行 | `FOR UPDATE SKIP LOCKED` |
| 同 txKey 不同 status 多事件 (CONFIRMING / COMPLETED) | `deposits.safeheron_tx_key` UNIQUE + `ON CONFLICT DO UPDATE` |
| **Webhook 乱序到达 (COMPLETED → CONFIRMING 倒退)** | `WHERE deposits.status_rank <= EXCLUDED.status_rank`，回退事件 0 行影响 |
| 同用户同币种并发入账 | `account(user_id, currency)` UNIQUE + `ON CONFLICT DO UPDATE` 原子操作 |
| worker 崩溃中途 | 单事务包裹，崩溃自动 ROLLBACK，事件保持 PENDING，下次重试 |

**承诺**：同一笔充值（`safeheron_tx_key` 唯一）永远只能让 `account.balance` 增加一次。COMPLETED 之后再收到 CONFIRMING 重发也不会回退状态、不会重复入账。

**Webhook 容错补救**：Safeheron 提供两个补救接口（Phase 1 不主动调用，监控告警时人工触发）：
- `POST /v1/webhook/resend` — 按 txKey 重发最后一条状态
- `POST /v1/webhook/resend/failed` — 重发某 1 小时区间内全部失败事件（7 天内可调，每 10 分钟限 1 次）

### 6.5 KYT 合规筛查（Phase 1 新增）

> 设计目标：**主路径靠 `AML_KYT_ALERT` webhook（被动），辅路径靠 KYT Report API 兜底（主动）**。理由：Safeheron API 有次数限制，webhook 推送在 90%+ 场景能覆盖；API 调用收敛到「初查 1 次 + 超时兜底 1 次」共 2 次/笔。

#### 6.5.1 处置矩阵

| `amlScreeningTriggeredState` | `amlList` 汇总 (任一 provider 取最高严重度) | 处置 | 告警级别 |
|------|------|------|------|
| `UNTRIGGERED` | — | MANUAL_REVIEW (`KYT_UNTRIGGERED`) | WARN |
| `IN_PROGRESS` | — | 进 `KYT_PENDING` 等 webhook / 等超时 | — |
| `TRIGGERED` | 任一 `status=PENDING` | 进 `KYT_PENDING` 等 webhook / 等超时 | — |
| `TRIGGERED` | 全部 `status` ∈ {COMPLETED} 且 `riskLevel=LOW` | **CREDITED**（放行入账） | — |
| `TRIGGERED` | 任一 `riskLevel=MEDIUM` | MANUAL_REVIEW (`KYT_RISK_MEDIUM`) | WARN |
| `TRIGGERED` | 任一 `riskLevel=HIGH` | MANUAL_REVIEW (`KYT_RISK_HIGH`) | **ERROR** |
| `TRIGGERED` | 任一 `riskLevel=SEVERE` | MANUAL_REVIEW (`KYT_RISK_SEVERE`) | **ERROR** |
| `TRIGGERED` | 任一 `riskLevel=UNKNOWN` | MANUAL_REVIEW (`KYT_RISK_UNKNOWN`) | WARN |
| `TRIGGERED` | 任一 `status=FAILED` | MANUAL_REVIEW (`KYT_PROVIDER_FAILED`) | WARN |
| `TRIGGERED` | 任一 `status=SKIPPED` | MANUAL_REVIEW (`KYT_SKIPPED`) | WARN |

风险等级汇总规则：取 amlList 中所有 provider `riskLevel` 的**最高严重度**（SEVERE > HIGH > MEDIUM > UNKNOWN > LOW）；任一 `status=PENDING` 视为整体未完成，需继续等待。

> 注：Phase 1 只接 MistTrack 一家，所以 amlList 中只有 1 条 provider 记录。规则按"任一"写是为了未来三家服务商扩展时无需改逻辑。

#### 6.5.2 主路径：`AML_KYT_ALERT` Webhook

Safeheron 在 KYT 筛查完成且**触发告警**时推送 `AML_KYT_ALERT` 事件。Webhook handler 复用现有 `/api/webhooks/safeheron`，新增事件类型分支：

```text
异步阶段 (worker 处理):
  IF eventType = 'AML_KYT_ALERT' THEN
      d := raw_payload.eventDetail   -- 见 AMLKYTAlertParam 字段定义
      dep := SELECT * FROM deposits WHERE safeheron_tx_key = d.txKey
      IF dep IS NULL THEN
          -- 乱序场景: AML_KYT_ALERT 比 TRANSACTION_STATUS_CHANGED 先到 (K-13)
          -- 事件保持 PENDING, 不标 DONE, 等下次 worker 轮询时 deposits 行已创建
          ROLLBACK; CONTINUE  -- 不更新 process_status, 让事件留在队列里
      END IF

      risk := summarizeRiskLevel(d.amlList)
      UPDATE deposits SET aml_screening_state='TRIGGERED',
                          aml_risk_level=risk,
                          aml_list=d.amlList,
                          aml_evaluated_at=NOW()
                    WHERE id=dep.id;

      -- 仅当当前是 KYT_PENDING / PENDING 才推进 (CREDITED / MANUAL_REVIEW 是终态不回退)
      IF dep.status IN ('PENDING','KYT_PENDING') THEN
          IF risk = 'LOW' AND no_pending_provider(d.amlList) THEN
              -- 放行入账 (复用 §6.4 入账事务: account + journal + status=CREDITED)
              creditAccount(dep)
          ELSIF risk = 'PENDING_PROVIDER' THEN
              -- 还有 provider 评估中, 继续 KYT_PENDING 等下一条 AML_KYT_ALERT
              -- Phase 1 仅 MistTrack 一家, 此分支理论上不会进入
              NO_OP
          ELSE
              MANUAL_REVIEW (reason='KYT_RISK_'||risk, alertLevel=alertLevelOf(risk))
          END IF
      END IF

      UPDATE safeheron_webhook_events SET process_status='DONE' WHERE id=?;
  END IF
```

**乱序保护**（K-13）：如果 `AML_KYT_ALERT` 在对应 `TRANSACTION_STATUS_CHANGED COMPLETED` 之前到达，`deposits` 行还不存在。事件**保留为 PENDING**（不标 DONE），下次 worker 轮询时 deposits 行已经创建（由后到的 status 事件创建），可正确关联。复用 `safeheron_webhook_events` 队列的天然能力，无新表。

> 实现细节：在 worker.go 内 `eventType=AML_KYT_ALERT` 且 `deposits` 未找到时，不调 `UPDATE process_status='DONE'`，事务 ROLLBACK 即可。下次轮询 `FOR UPDATE SKIP LOCKED` 重新拉取。需限制最大重试次数（如 100 次）避免死循环，超过后转 MANUAL_REVIEW (`KYT_ORPHAN_ALERT`)。

#### 6.5.3 辅路径：API 主动查询

**调用时机仅 2 处**（最大化节省 API 配额）：

1. **入账分支首次进入**（§6.4）— 收到 `TRANSACTION_STATUS_CHANGED COMPLETED+CONFIRMED` 时调一次，判断 `amlScreeningTriggeredState`，决定直接放行 / 直接 MANUAL_REVIEW / 进 KYT_PENDING 等 webhook
2. **超时兜底扫描**（见 §6.5.4） — `KYT_PENDING` 超过 20 分钟未收到 `AML_KYT_ALERT` 时再调一次

#### 6.5.4 超时兜底扫描

独立 goroutine（建议复用 `DepositWorker` 主 ticker，间隔 1 分钟检查一次），SQL：

```sql
SELECT id, safeheron_tx_key, user_id FROM deposits
 WHERE status = 'KYT_PENDING'
   AND updated_at < NOW() - INTERVAL '20 minutes'
 FOR UPDATE SKIP LOCKED
 LIMIT 50;
```

对每行执行：
```
report := SafeheronClient.KytReport(dep.safeheron_tx_key)
UPDATE deposits SET aml_list=report.amlList, aml_evaluated_at=NOW(), updated_at=NOW() WHERE id=dep.id;

CASE report.amlScreeningTriggeredState
  WHEN 'UNTRIGGERED' THEN
    MANUAL_REVIEW (reason='KYT_UNTRIGGERED_AFTER_TIMEOUT', alertLevel=WARN)
  WHEN 'IN_PROGRESS' THEN
    -- K-19: 仍未完成, 不再延长, 直接 MANUAL_REVIEW
    MANUAL_REVIEW (reason='KYT_TIMEOUT_STILL_PENDING', alertLevel=ERROR)
  WHEN 'TRIGGERED' THEN
    risk := summarizeRiskLevel(report.amlList)
    IF risk = 'LOW' AND no_pending_provider(report.amlList) THEN
      creditAccount(dep)
    ELSE
      MANUAL_REVIEW (reason='KYT_RISK_'||risk||'_AFTER_TIMEOUT', alertLevel=alertLevelOf(risk))
    END IF
END CASE
```

#### 6.5.5 状态转移完整图

```
PENDING ─(COMPLETED+CONFIRMED, KYT_ENABLED=true)─> 调 KYT Report API
   │                                                  │
   │                                                  ├──UNTRIGGERED────────────────────────> MANUAL_REVIEW (WARN)
   │                                                  ├──TRIGGERED+LOW──────────────────────> CREDITED
   │                                                  ├──TRIGGERED+MEDIUM───────────────────> MANUAL_REVIEW (WARN)
   │                                                  ├──TRIGGERED+HIGH/SEVERE──────────────> MANUAL_REVIEW (ERROR)
   │                                                  ├──TRIGGERED+FAILED/SKIPPED/UNKNOWN───> MANUAL_REVIEW (WARN)
   │                                                  └──IN_PROGRESS / PENDING_PROVIDER ──> KYT_PENDING
   │                                                                                            │
   │                                                                       ┌────AML_KYT_ALERT───┤
   │                                                                       │                    │
   │                                                                       ▼                    │
   │                                                              [评估同上, 含 LOW→CREDITED]    │
   │                                                                                            │
   │                                                              ┌─────20 min 未到 webhook─────┘
   │                                                              │
   │                                                              ▼
   │                                                  超时再调 1 次 KYT Report API
   │                                                              │
   │                                                              ├──TRIGGERED+LOW────> CREDITED
   │                                                              ├──IN_PROGRESS──────> MANUAL_REVIEW (ERROR, 不再延长)
   │                                                              └──其余─────────────> MANUAL_REVIEW
   │
   └─(COMPLETED+CONFIRMED, KYT_ENABLED=false)──────────────────> CREDITED  (仅非 prod 生效, 见 §9.6)
```

#### 6.5.6 KYT 不变量

- 任何 `CREDITED` 的 deposit 必须满足以下之一：
  1. `aml_risk_level='LOW'` 且 `aml_screening_state='TRIGGERED'` 且 amlList 内无 PENDING provider
  2. `KYT_ENABLED=false`（仅 local / test，prod 启动校验会阻止）
- `MANUAL_REVIEW` 是终态：KYT 后续即使变化也不自动改回 CREDITED（Phase 1 不做运维放行接口，K-18 列入 Phase 2 TODO）
- `aml_list` 字段一旦写入永不删除（审计要求）

### 6.6 异常处理

所有进入 `MANUAL_REVIEW` 的事件都满足以下规则：

- `deposits.status = 'MANUAL_REVIEW'`
- `deposits.failed_reason` 填写明确原因码（`ADDRESS_UNASSIGNED` / `COIN_UNSUPPORTED` / `BELOW_MIN_AMOUNT` / `AMOUNT_MISMATCH` / `KYT_UNTRIGGERED` / `KYT_RISK_{LEVEL}` / `KYT_PROVIDER_FAILED` / `KYT_SKIPPED` / `KYT_TIMEOUT_STILL_PENDING` / `KYT_ORPHAN_ALERT` 等）
- `safeheron_webhook_events.process_status = 'DONE'`（事件已消费，不再重试）
- 触发**飞书/邮件告警**：告警级别按 §6.5.1 处置矩阵分级（HIGH/SEVERE/超时类 = ERROR，其余 = WARN）
- `account.balance` **不变更**，`journal` **不写**
- 运营人工评估后通过 DB 直改恢复（Phase 1 不提供 admin 接口，K-18 列入 Phase 2 TODO）

---

## 7. 充值状态机

### 7.1 状态枚举（DB 一次定义清楚）

```text
PENDING               -- 已收到 webhook, 但 Safeheron transactionStatus 未到 COMPLETED
CHAIN_VERIFYING       -- [二期] 已 COMPLETED, 提交链上二次校验
CHAIN_VERIFIED        -- [二期] 链上校验通过, 等待入账
KYT_PENDING           -- [Phase 1 新增] 链上 COMPLETED, KYT 评估中等待 AML_KYT_ALERT 或 20min 超时兜底
CREDITED              -- 已入账, account.balance 已增加
FAILED                -- Safeheron 标记交易失败
MANUAL_REVIEW         -- 异常事件, 人工介入 (终态, Phase 1 不自动恢复)
```

### 7.2 Phase 1 实际流转

```text
PENDING ──(COMPLETED + KYT_ENABLED=false)──→ CREDITED                               (本地/sandbox)
   │
   ├──(COMPLETED + KYT 初查 UNTRIGGERED)──→ MANUAL_REVIEW                            (K-8)
   │
   ├──(COMPLETED + KYT 初查 TRIGGERED+LOW)──→ CREDITED
   │
   ├──(COMPLETED + KYT 初查 TRIGGERED+MEDIUM/HIGH/SEVERE/UNKNOWN/FAILED/SKIPPED)──→ MANUAL_REVIEW
   │
   ├──(COMPLETED + KYT 初查 IN_PROGRESS / PENDING_PROVIDER)──→ KYT_PENDING
   │                                                              │
   │                                                              ├──(AML_KYT_ALERT, LOW)──→ CREDITED
   │                                                              │
   │                                                              ├──(AML_KYT_ALERT, 非 LOW)──→ MANUAL_REVIEW
   │                                                              │
   │                                                              └──(20min 超时兜底)──→ CREDITED / MANUAL_REVIEW
   │
   ├──(COMPLETED + 地址无主/币种不支持/金额异常)──→ MANUAL_REVIEW
   │
   └──(Safeheron FAILED)──→ FAILED
```

`CHAIN_VERIFYING` / `CHAIN_VERIFIED` 在 Phase 1 代码中**不会被设置**。

### 7.3 二期流转（预留）

```text
PENDING ──(COMPLETED)──→ CHAIN_VERIFYING
                              │
                              ├──(校验通过)──→ CHAIN_VERIFIED ──(KYT 同 Phase 1)──→ CREDITED
                              │
                              └──(校验失败)──→ MANUAL_REVIEW
```

二期接入时只需在 worker 路径中**插入链上校验节点**（KYT 节点保持不变），无 schema 变更。

---

## 8. API 端点

### 8.1 用户侧（受保护，需 JWT）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/wallet/deposit-address?networkFamily=EVM` | 获取/分配 EVM 充值地址（实际实现 camelCase，R2-C-1 修复） |
| GET | `/api/wallet/deposit-address?networkFamily=TRON` | 获取/分配 TRON 充值地址 |
| GET | `/api/wallet/deposit-coins` | **T9 新增**：按币种分组返回可充值币种 + 每币种可用网络列表，供选币→选链 UI 使用 |
| GET | `/api/wallet/supported-chains` | 按链分组返回支持币种（T8 老接口，T9 UX 不再调用，保留向后兼容） |
| GET | `/api/deposits` | （已存在，T9 充值页底部「Recent deposits」消费 `?limit=5`） |

**响应示例**：
```json
GET /api/wallet/deposit-address?networkFamily=EVM
{
  "address": "0xabc...123",
  "networkFamily": "EVM",
  "supportedCoins": [
    {"chainCode": "ETHEREUM", "symbol": "ETH",  "coinKey": "ETH",        "minDeposit": "0.001", "decimals": 18},
    {"chainCode": "ETHEREUM", "symbol": "USDT", "coinKey": "USDT_ERC20", "minDeposit": "1",     "decimals": 6},
    {"chainCode": "ETHEREUM", "symbol": "USDC", "coinKey": "USDC_ERC20", "minDeposit": "1",     "decimals": 6},
    {"chainCode": "BSC",      "symbol": "BNB",  "coinKey": "BNB_BSC",    "minDeposit": "0.005", "decimals": 18},
    {"chainCode": "BSC",      "symbol": "USDT", "coinKey": "USDT_BEP20", "minDeposit": "1",     "decimals": 18},
    {"chainCode": "BSC",      "symbol": "USDC", "coinKey": "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET", "minDeposit": "1", "decimals": 18}
  ]
}
```

```json
GET /api/wallet/deposit-coins
{
  "coins": [
    {
      "symbol": "ETH",
      "name": "Ether",
      "isStable": false,
      "networks": [
        {
          "chainCode": "ETHEREUM",
          "chainName": "Ethereum",
          "networkFamily": "EVM",
          "shortName": "ETH",
          "tokenStandard": "Native",
          "isNative": true,
          "tokenContract": null,
          "decimals": 18,
          "minDeposit": "0.001",
          "requiredConfirmations": 0,
          "estimatedArrivalMinutes": 2,
          "explorerUrl": "https://etherscan.io"
        }
      ]
    },
    {
      "symbol": "USDC",
      "name": "USD Coin",
      "isStable": true,
      "networks": [
        {
          "chainCode": "ETHEREUM",
          "chainName": "Ethereum",
          "networkFamily": "EVM",
          "shortName": "ETH",
          "tokenStandard": "ERC20",
          "isNative": false,
          "tokenContract": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
          "decimals": 6,
          "minDeposit": "1",
          "requiredConfirmations": 0,
          "estimatedArrivalMinutes": 2,
          "explorerUrl": "https://etherscan.io"
        },
        {
          "chainCode": "BSC",
          "chainName": "BNB Smart Chain",
          "networkFamily": "EVM",
          "shortName": "BSC",
          "tokenStandard": "BEP20",
          "isNative": false,
          "tokenContract": "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d",
          "decimals": 18,
          "minDeposit": "1",
          "requiredConfirmations": 0,
          "estimatedArrivalMinutes": 1,
          "explorerUrl": "https://bscscan.com"
        }
      ]
    }
  ]
}
```

> **`deposit-coins` 数据组装逻辑**：遍历 Registry 中 `deposit_enabled=true` 的 coin_chains，按 `coin.symbol` 分组；每个 coin 下 `networks` 列表来自该 symbol 在不同链上的 coin_chain 行；输出顺序按 coin 首次出现的 `display_order` 升序。`tokenContract` 空字符串序列化为 `null`；`shortName` 为空时 fallback 到 `chain.code`。

### 8.2 Webhook（公开，验签保护）

| Method | Path | 说明 |
|--------|------|------|
| POST | `/api/webhooks/safeheron` | Safeheron 推送事件，统一入口；按 `eventType` 路由：`TRANSACTION_CREATED` / `TRANSACTION_STATUS_CHANGED` 走充值入账状态机（§6.4），`AML_KYT_ALERT` 走 KYT 决策（§6.5.2），其余 12 种事件类型标 DONE 跳过 |

> **Phase 1 关心的 webhook eventType（2 类共 3 种）**：
> - 充值事件：`TRANSACTION_CREATED` / `TRANSACTION_STATUS_CHANGED`，`eventDetail` 结构详见 §6.4（TransactionEventDetail）
> - KYT 事件：`AML_KYT_ALERT`，`eventDetail` 是**独立 struct** `AMLKYTAlertDetail`（**不与 TransactionEventDetail 共用**），关键字段：
>   ```text
>   txKey                  string        // 关联到 deposits.safeheron_tx_key
>   customerRefId          string
>   txHash                 string
>   coinKey                string
>   txAmount               string
>   sourceAccountKey       string
>   sourceAddress          string
>   destinationAccountKey  string
>   destinationAddress     string
>   transactionDirection   string        // INFLOW / OUTFLOW / INTERNAL_TRANSFER
>   alertTime              string        // UNIX 毫秒
>   amlList                []AmlReport   // 含 provider/status/riskLevel/timestamp/lastUpdateTime/payload
>   ```
> - 每个服务商完成筛查且触发告警后立即推送；同一 txKey 可能收到多条 `AML_KYT_ALERT`（Phase 1 只接 MistTrack 一家，理论上每笔 1 条），以 `alertTime` 最新为准
> - **代码实现建议**：webhook handler 先解析顶层 `eventType` 字段判断分支，再分别 unmarshal 到 `TransactionEventDetail` 或 `AMLKYTAlertDetail`；不要尝试用单一 struct + omitempty 共用，避免字段歧义
>
> Safeheron Console 必须开启「AML 功能」+「告警方式启用 Webhook 通知」才会推 `AML_KYT_ALERT`，详见 §9.1。

### 8.3 Vercel 路由配置

需要在 `api/[...route].ts` 的 `ROUTE_CONFIG` 中追加：

```ts
'GET /api/wallet/deposit-address':  { requiresAuth: true,  backendPath: '/api/wallet/deposit-address' },
'GET /api/wallet/deposit-coins':    { requiresAuth: true,  backendPath: '/api/wallet/deposit-coins' },   // T9 新增
'GET /api/wallet/supported-chains': { requiresAuth: true,  backendPath: '/api/wallet/supported-chains' },
'POST /api/webhooks/safeheron':     { requiresAuth: false, backendPath: '/api/webhooks/safeheron' },
```

### 8.4 充值页面 UX 流程（前端，T9 重构后）

T8 落地的 EVM/TRON tab 切换 UI 已被 T9 重构为对齐币安/欧易的「选币→选链→展示地址」三步流程。本节描述新的前端契约，作为后续维护者的对齐基线。

**整体布局**（lg 断点起 1fr | 320px 双栏；sm 端单栏 RecentDeposits 折到底部）：

```
┌──────────────────────────────────────┐ ┌─────────────────┐
│  ◇ 1. Select coin       (高亮)        │ │ Recent deposits │
│     [ETH] [USDC] [USDT] [BNB] [TRX]  │ │   ─────────     │
│                                       │ │   (空态 / 列表) │
│  ◇ 2. Select network    (灰显待激活)  │ │                 │
│     [▼ Select network]                │ │                 │
│                                       │ │                 │
│  ◇ 3. Deposit address                 │ │                 │
│     [ QR ] 0xF2...1058 [Copy]         │ │                 │
│     Contract ends in 6eB48 ⤴          │ │                 │
│     Details: min, confirmations, ...  │ │                 │
└──────────────────────────────────────┘ └─────────────────┘
```

**数据流**：

1. **页面 mount** → 调 `GET /api/wallet/deposit-coins` 加载币种 + 网络元数据（React Query `staleTime: 5min`）
2. **用户点币种 chip**：本地 state `selectedCoin = coin`；清空 `selectedNetwork`；步骤指示器推进到 ②
3. **若该 coin 只有 1 个 network** → useEffect 自动 `setSelectedNetwork(networks[0])`；推进到 ③
4. **用户在下拉框选 network** → `selectedNetwork = network`；推进到 ③
5. **步骤 ③ 渲染时** → 调 `GET /api/wallet/deposit-address?networkFamily=${network.networkFamily}` 拿地址（React Query 按 `networkFamily` 缓存；同 family 下切币不重新请求）
6. **底部 RecentDeposits** → 独立调 `GET /api/deposits?limit=5`

**安全文案（必含）**：

- 网络下拉打开时顶部警告：「Only the networks shown here are supported. Sending via an unsupported network will result in permanent loss of funds.」
- 非原生币地址卡显示「Contract address ends in {last4}」，外链按钮跳 `${chain.explorerUrl}/token/${tokenContract}`
- 详情区固定展开，展示：Minimum deposit、Credited after N network confirmations、Contract address（非原生币才显示）

**跳转规则**：

| 用户动作 | 步骤指示器 | selectedCoin | selectedNetwork | 地址区 |
|---------|----------|-------------|----------------|--------|
| 初次加载 | ① 高亮 | null | null | placeholder |
| 选币种（多网络） | ② 激活 | 已设 | null | placeholder |
| 选币种（单网络，自动推进） | ③ 激活 | 已设 | 已自动设 | 显示地址 |
| 选网络 | ③ 激活 | 已设 | 已设 | 显示地址 |
| 切币种 | 退回 ① / ② / ③ | 新值 | null | placeholder（或同 family 复用） |

**地址复用规则（D-15）**：当 `selectedNetwork.networkFamily` 不变（如从 USDC@ETHEREUM 切到 ETH@ETHEREUM），后端返回同一 `address_pool` 行（同一 user × 同一 networkFamily 唯一地址，这是 Safeheron AddCoin 设计：一个 EVM 钱包接收所有 EVM coinKey 都到同一地址）。前端 React Query 在 `networkFamily` 维度缓存，无需重新请求。

**i18n key 命名空间**（T9 重写 `deposit.*` 子树，旧 `deposit.tabs.*` / `deposit.supportedCoins.*` / `deposit.comingSoon.*` 全部删除）：
- `deposit.steps.{selectCoin, selectNetwork, depositAddress}`
- `deposit.coinSelector.{placeholder, empty}`
- `deposit.networkSelector.{placeholder, disabledHint, warning, arrivalEstimate, confirmations}`
- `deposit.addressCard.{title, copy, copied, copyFailed, qrAlt, contractEnd, viewOnExplorer, errorTitle, placeholder}`
- `deposit.details.{title, minDeposit, requiredConfirmations, confirmationsValue, contract}`
- `deposit.recent.{title, empty, viewAll, columns.{amount, network, address, txid, time}}`
- `deposit.status.{PENDING, CHAIN_VERIFYING, CHAIN_VERIFIED, CREDITED, FAILED, MANUAL_REVIEW}`（已存在，保留）

---

## 9. Safeheron SDK 集成

### 9.1 API Key 与权限

Phase 1 仅需 **访问 API** 类型的 API Key，最小权限：
- 读取（钱包账户、币种、交易）
- 管理钱包账户（创建、AddCoin）
- Webhook 配置（如需 API 配置）
- **合规筛查（Compliance / KYT Report）** — 调用 `/v1/compliance/kyt/report`

**不**授予「发起/取消交易」权限（二期提现再开）。

**Web Console 还需手动开启**（详见 §6.5）：
1. 管理 → API → 开通 AML 功能
2. 配置风险等级映射（MistTrack 评分 → LOW/MEDIUM/HIGH/SEVERE）
3. 风险等级配置中**启用 Webhook 通知**（否则不会推 `AML_KYT_ALERT`）

### 9.2 出口 IP 白名单

Safeheron API Key 要求白名单。生产部署需要：
- Docker host 固定公网出口 IP
- 提交 IP 到 Safeheron 控制台白名单
- 预留**至少 2 个 IP**（主备 / 灰度）

> **风险点**：当前 docker-compose 部署无固定出口 IP。**部署前必须确认**。

### 9.3 私钥管理

- 商户 RSA 私钥用于 API 请求签名
- 存放方式：**环境变量**（Phase 1）或云 Secret Manager（推荐二期改）
- **绝不**入仓
- `.env.example` 仅放占位符

### 9.4 钱包创建参数（Phase 1 固定）

```go
safeheronClient.CreateWallet(CreateWalletRequest{
    AccountTag:    "DEPOSIT",     // 为二期 Auto Sweep 铺路
    HiddenOnUI:    true,           // 100+ 钱包不污染控制台
    AutoFuel:      false,          // 二期开启
    CoinKeyList:   []string{...},  // 该 chain 下所有 enabled coin_chains
    CustomerRefId: uuid.New(),
})
```

### 9.5 环境隔离

通过 `APP_ENV`（`local` | `test` | `production`）控制环境，**代码只一套，配置随环境切换**：

- 不同环境用不同 Safeheron API Key / RSA 私钥 / 平台公钥 / Webhook 公钥
- 不同环境连接不同数据库（避免 sandbox 钱包污染生产）
- 不同环境用不同 `BACKEND_URL` 和 `BASE_URL`
- 启动时打印当前 `APP_ENV` 到日志（运维确认）
- 仓库只 commit `.env.example`，**绝不** commit 任何环境的真实密钥
- 配置加载顺序：环境变量 > `.env.<APP_ENV>` 文件 > 默认值
- 启动校验：如果 `APP_ENV=production`，必须提供 Safeheron 全套密钥，否则 panic

### 9.6 环境变量

```bash
APP_ENV=production                                 # local | test | production
SAFEHERON_API_BASE_URL=                            # production: https://api.safeheron.com; test: sandbox URL
SAFEHERON_API_KEY=                                 # 必需
SAFEHERON_PRIVATE_KEY_PEM=                         # 商户 RSA 私钥 (PEM, 单行 base64 或文件路径)
SAFEHERON_PLATFORM_PUBLIC_KEY_PEM=                 # Safeheron 平台公钥, 用于 API 响应验签
SAFEHERON_WEBHOOK_PUBLIC_KEY_PEM=                  # Safeheron Webhook 签名公钥
WALLET_CONFIG_REFRESH_INTERVAL=60s
POOL_REPLENISH_INTERVAL=10m
POOL_LOW_WATERMARK_EVM=50
POOL_TARGET_CAPACITY_EVM=100
POOL_LOW_WATERMARK_TRON=50
POOL_TARGET_CAPACITY_TRON=100
ALERT_WEBHOOK_URL=                                 # 飞书机器人 webhook URL
ALERT_EMAIL_RECIPIENTS=ops@moneradigital.com

# ============ KYT / AML 合规筛查 (Phase 1) ============
KYT_ENABLED=true                                   # 是否启用 KYT 检查; 默认 true. 仅 APP_ENV != production 时允许设为 false (启动校验, K-16)
KYT_TIMEOUT=20m                                    # KYT_PENDING 状态超时阈值, 触发兜底 API 调用
KYT_SCAN_INTERVAL=1m                               # 超时扫描 worker 轮询间隔
KYT_ORPHAN_ALERT_MAX_RETRY=100                     # AML_KYT_ALERT 找不到 deposits 时最大重试次数, 超过转 MANUAL_REVIEW
```

> **K-16 启动校验逻辑**：
> ```go
> if appEnv == "production" && !viper.GetBool("KYT_ENABLED") {
>     panic("KYT_ENABLED=false is not allowed in production")
> }
> ```
> 生产环境强制启用，本地 / sandbox 可关闭以便端到端不依赖真实 Safeheron AML 配置。

---

## 10. 安全要求

| 要求 | 实现 |
|------|------|
| Webhook 验签 | SDK `webhook.WebhookConverter.Convert()` 一步完成 SHA256WithRSA 验签 + RSA-OAEP + AES-GCM 解密；签名在 body 内 `sig` 字段（非 HTTP Header）；失败 → 401 + 告警，不落库 |
| Webhook ack body | 必须返回 200 + `{"code":"200","message":"SUCCESS"}` 严格匹配；偏离即触发 6 次重试风暴（30s→1m→5m→1h→12h→24h） |
| Webhook 幂等 | `safeheron_webhook_events.event_id` 唯一约束（事件键 = `txKey:transactionStatus`，覆盖 Safeheron "inevitable duplicate" 推送语义） |
| Webhook 乱序保护 | `deposits.status_rank` 单调递增，UPDATE WHERE `status_rank <= newRank`，防 COMPLETED → CONFIRMING 状态回退（V6 实测确认会出现） |
| 防重复入账 | `deposits(safeheron_tx_key)` 唯一约束 + 入账触发用 `WHERE status='PENDING'` 条件守卫 |
| 并发入账原子 | `account(user_id, currency)` 唯一约束 + `ON CONFLICT DO UPDATE` |
| 数据库事务 | 入账整个流程单事务（webhook + deposits + account + journal） |
| 并发地址分配 | `SELECT FOR UPDATE SKIP LOCKED` |
| 多 worker 竞争 | `safeheron_webhook_events` 拉取也用 `FOR UPDATE SKIP LOCKED` |
| 6 层并发防御 | 详见 §6.4 并发防御层级表 |
| 私钥不入仓 | `.gitignore` 排除 `secrets/` 与 `.env`；4 个 RSA PEM 经 `SAFEHERON_*_KEY_PATH` env 指向 `secrets/` 下的真实文件（v1.6 起），代码不再处理 PEM 内容，详见 §10.1 |
| 日志脱敏 | 私钥、API Key、签名头、`sig` / `key` / `bizContent` 字段不写日志 |
| 环境隔离 | `APP_ENV` 区分 local / test / production，密钥 + coinKey 集合 + Safeheron API Key 全套独立 |
| KYT 强制启用 (prod) | `APP_ENV=production` 启动时若 `KYT_ENABLED=false` 直接 panic（K-16）|
| KYT 不变量 | `CREDITED` 必须满足 `aml_risk_level='LOW'` 或 `KYT_ENABLED=false`；`aml_list` 写入后永不删除（审计要求） |
| KYT 终态保护 | `MANUAL_REVIEW` 是终态，KYT 后续变化不自动改回 CREDITED（K-18） |
| AML_KYT_ALERT 乱序保护 | webhook 找不到 deposit 时事件保留为 PENDING 待下次轮询；超过 `KYT_ORPHAN_ALERT_MAX_RETRY` 转 `MANUAL_REVIEW(KYT_ORPHAN_ALERT)` |

### 10.1 RSA 密钥文件化（v1.6 — 原 Phase 2 提前）

**目标**：消除 `writeTempPEM` / `tempDir` 整套机制，让 Safeheron SDK 直接读取运维放置在 `secrets/` 目录下的真实 PEM 文件。

**Why**：commit `fc44c99` 的 SEC-2 修复把 PEM 文本从 env 写到进程级 0700 临时目录，绕一圈就是为了把 env 字符串塞进文件供 SDK 读取——SDK 本就要求文件路径。env 中存大段 PEM 文本不优雅（多行换行需 `\n` 转义），临时文件归属/权限/清理都得自己管，进程被 OOM kill 不走 SIGTERM 时 tempDir 会残留。

#### 决策对齐（已明确，不可重开）

| 问题 | 决定 |
|------|------|
| 旧 `*_PEM` env 兼容 | **硬切换** — 完全删除旧读取代码，运维同步部署 |
| 启动时文件权限校验 | **WARN 不阻塞** — perm 宽于推荐时 log 警告，不影响启动 |
| CI / 测试服务器密钥分发 | **服务器手工放置一次** — 不改 `.github/workflows/`，运维 scp 后 chmod |

#### 目录布局（运维职责，仓库不跟踪）

```
<repo-root>/
├── secrets/                          # 0700, 在 .gitignore 中
│   ├── safeheron-private.pem         # 0600 — 客户端 RSA 私钥
│   ├── safeheron-platform-pub.pem    # 0644 — Safeheron 平台公钥（验签 API 响应）
│   ├── safeheron-webhook-pub.pem     # 0644 — Webhook 验签公钥
│   └── safeheron-webhook-priv.pem    # 0600 — Webhook 解密私钥
```

#### 影响文件清单

| 文件 | 改动 |
|------|------|
| `internal/safeheron/client.go` | 删 `writeTempPEM` / `tempDir` / `tempFiles` / `cleanupFiles`；`Config.*PEM` → `Config.*Path`；`NewClient` 改为校验文件存在 + 权限 warn；`Close()` 简化为 no-op |
| `internal/safeheron/client_test.go` | 删 tempDir 相关测试；新增 7 个文件路径测试用例 |
| `internal/container/container.go` | viper 读 `SAFEHERON_*_KEY_PATH` |
| `cmd/pool_init/main.go` | 同上 |
| `.env.example` | 4 个 key 重命名 `*_PEM` → `*_PATH` + 部署注释 |
| `.gitignore` | 追加 `/secrets/` |
| `CLAUDE.md` | "Environment Variables → Safeheron" 段更新 |

#### Config 结构（重命名）

```go
type Config struct {
    BaseURL               string
    APIKey                string
    PrivateKeyPath        string // 必填，0600 推荐
    PlatformPublicKeyPath string // 必填，0644 推荐
    WebhookPublicKeyPath  string // 选填（与 WebhookPrivateKeyPath 同时设置）
    WebhookPrivateKeyPath string // 选填
    RequestTimeoutMS      int64
}
```

#### 校验帮助函数

```go
// validateKeyFile 检查路径存在 + 可读，权限位过宽时打 warn log。
// 失败时返回 wrapped error 让 NewClient 直接返回；权限超出时仅 log 不阻塞。
func validateKeyFile(path, label string, recommendedPerm os.FileMode) error {
    if path == "" {
        return fmt.Errorf("safeheron: %s path is required", label)
    }
    info, err := os.Stat(path)
    if err != nil {
        return fmt.Errorf("safeheron: %s stat %q: %w", label, path, err)
    }
    if info.IsDir() {
        return fmt.Errorf("safeheron: %s path %q is a directory", label, path)
    }
    if actual := info.Mode().Perm(); actual&^recommendedPerm != 0 {
        log.Printf("safeheron: %s file %q permission %#o is wider than recommended %#o; tighten via chmod",
            label, path, actual, recommendedPerm)
    }
    return nil
}
```

#### 验收标准

| # | 条件 | 验证方式 |
|---|------|----------|
| K-PATH-1 | `client.go` 不再含 `writeTempPEM` / `tempDir` / `MkdirTemp` 任何引用 | `grep -n 'writeTempPEM\|tempDir\|MkdirTemp' internal/safeheron/client.go` 无输出 |
| K-PATH-2 | `Config` 字段为 `*Path` | grep + 编译通过 |
| K-PATH-3 | env 4 项更名为 `*_PATH`，旧 `*_PEM` 完全不存在于业务代码 | `grep -rn 'SAFEHERON.*PEM' --include="*.go"` 无业务命中 |
| K-PATH-4 | 文件不存在 / 不可读 → `NewClient` 返回错误（非 panic）；权限位宽于推荐 → WARN log 不阻塞 | 单元测试覆盖 |
| K-PATH-5 | `secrets/` 在 `.gitignore`，仓库无任何 `*.pem` | `git check-ignore secrets/foo.pem` 返回 0；`git ls-files \| grep '\.pem$'` 空 |
| K-PATH-6 | 本地 dev 通过：`secrets/` 放 4 个文件 → `./bin/server` 启动成功，pool 初始化正常 | 手动验证 |
| K-PATH-7 | `client_test.go` 全绿；`go vet` 无新警告；覆盖率不低于现状 | `go test -race -cover ./internal/safeheron/...` |

#### 单元测试用例（替换原 tempDir 测试）

| 用例 | 断言 |
|------|------|
| `TestNewClient_FilePathsHappyPath` | 4 个 PEM 文件齐备 → NewClient 成功 |
| `TestNewClient_PrivateKeyPathMissing` | 空 PrivateKeyPath → "path is required" |
| `TestNewClient_PrivateKeyFileNotFound` | 路径指向不存在文件 → wrapped `ErrNotExist` |
| `TestNewClient_PrivateKeyPathIsDir` | 路径指向目录 → "is a directory" |
| `TestNewClient_WebhookKeysMustBeBothOrNeither` | 仅设 pub 不设 priv → 错误（保持原语义） |
| `TestNewClient_PermissionWarningEmittedButNotBlocking` | 私钥 perm=0644 → log 含 warn，但 NewClient 成功 |
| `TestClient_CloseIsIdempotent` | 多次 Close 不 panic（保留以便未来注入资源） |

#### 非目标

- ❌ Vault / sops / AWS Secrets Manager 集成（Phase 3 议题）
- ❌ 任何 backwards-compatibility shim 兼容旧 `*_PEM` env
- ❌ Go 代码创建 `secrets/` 或写入示例文件（运维职责）
- ❌ 修改 `.github/workflows/deploy-backend-test-env.yml`
- ❌ 强制权限校验导致启动失败

---

## 11. 验收标准

### 11.1 功能验收

- [ ] 链币配置表 + Registry 启动加载 + 60s 后台刷新 + 失败保留旧值
- [ ] 地址池预生成 EVM 100 个 + TRON 100 个，状态 AVAILABLE
- [ ] 定时任务每 10 分钟检查，低于水位自动补水
- [ ] 用户首次请求获得 EVM 地址，重复请求返回同一地址
- [ ] 同一地址不会被分配给两个用户
- [ ] 用户在 EVM 链上 6 个币种（ETH/USDT/USDC × ETH/BSC）任意一种充值，Safeheron 推送 webhook
- [ ] Webhook 验签失败返回 401，不落库
- [ ] Webhook 验签成功落库 `safeheron_webhook_events`
- [ ] 重复 event_id 不重复处理
- [ ] `transactionStatus = COMPLETED` 触发入账：`account.balance` 增加 + `journal` 写入 + `deposits.status = CREDITED`
- [ ] 地址无主、币种不支持、金额低于阈值 → `MANUAL_REVIEW` + 飞书告警
- [ ] 同一 tx + log_index 不会重复入账
- [ ] TRON 链充值闭环同 EVM

**KYT 合规筛查追加项**（Phase 1）：

- [ ] **F-KYT-1** `KYT_ENABLED=true` 下，收到 `COMPLETED+CONFIRMED` webhook 时主动调一次 `/v1/compliance/kyt/report`，结果写入 `deposits.aml_screening_state` / `aml_list` / `aml_evaluated_at`
- [ ] **F-KYT-2** KYT 初查返回 `UNTRIGGERED` → `MANUAL_REVIEW(KYT_UNTRIGGERED)` + WARN 告警，**不入账**
- [ ] **F-KYT-3** KYT 初查返回 `TRIGGERED + LOW` → 直接 `CREDITED`，account.balance 增加
- [ ] **F-KYT-4** KYT 初查返回 `TRIGGERED + HIGH/SEVERE` → `MANUAL_REVIEW(KYT_RISK_HIGH/SEVERE)` + **ERROR 告警**，不入账
- [ ] **F-KYT-5** KYT 初查返回 `TRIGGERED + MEDIUM/UNKNOWN/FAILED/SKIPPED` → `MANUAL_REVIEW(KYT_RISK_*)` + WARN 告警
- [ ] **F-KYT-6** KYT 初查返回 `IN_PROGRESS` 或 任一 provider status=PENDING → `deposits.status = KYT_PENDING`
- [ ] **F-KYT-7** `KYT_PENDING` 状态收到 `AML_KYT_ALERT` webhook，按 §6.5.1 矩阵推进到 CREDITED 或 MANUAL_REVIEW
- [ ] **F-KYT-8** `AML_KYT_ALERT` 先于 `TRANSACTION_STATUS_CHANGED` 到达时，事件保留 PENDING 等下次 worker 轮询关联（乱序保护）
- [ ] **F-KYT-9** `KYT_PENDING` 状态超过 20 分钟未推进，超时扫描调一次 KYT Report API 兜底
- [ ] **F-KYT-10** 超时兜底仍返回 `IN_PROGRESS` → `MANUAL_REVIEW(KYT_TIMEOUT_STILL_PENDING)` + **ERROR 告警**（不再延长）
- [ ] **F-KYT-11** `KYT_ENABLED=false` 在 `APP_ENV=production` 启动直接 panic（启动校验）
- [ ] **F-KYT-12** `KYT_ENABLED=false` 在非 prod 环境，COMPLETED 直接 CREDITED 不走 KYT 流程（本地端到端不依赖 Safeheron AML）
- [ ] **F-KYT-13** 前端 `Recent deposits` 列表对 `KYT_PENDING` 状态展示「Under compliance review」文案（K-15）
- [ ] **F-KYT-14** `MANUAL_REVIEW` 终态：即使后续收到 AML_KYT_ALERT 推送 LOW 也不自动改回 CREDITED（K-18）
- [ ] **F-KYT-15** `aml_list` 字段一旦写入永不删除/重置（审计要求）

**T9 UX 重构追加项**（选币→选链→展示地址三步流程）：

- [ ] **F-T9-1** 页面初次加载显示 StepIndicator，步骤 ① 高亮、② ③ 灰显
- [ ] **F-T9-2** 步骤 ① 展示当前环境支持充值的所有币种 chip（local：ETH/USDC/TRX；prod：ETH/USDT/USDC/BNB/TRX）
- [ ] **F-T9-3** 点击币种 → 步骤 ② 激活，下拉框列出该币种可用网络；单网络 coin 自动选中并跳到 ③
- [ ] **F-T9-4** 步骤 ③ 显示充值地址 + QR 码，地址内容与 `address_pool.address` 一致
- [ ] **F-T9-5** 非原生币（如 USDC）显示「Contract address ends in {last4}」+ 可点击跳转区块链浏览器
- [ ] **F-T9-6** 详情区显示「Minimum deposit: {min} {symbol}」、「Credited after: {N} network confirmations」；合约地址行只在非原生币显示
- [ ] **F-T9-7** 切换币种 → 网络选择重置；同 networkFamily 内换币地址不变（地址按 family 缓存）
- [ ] **F-T9-8** 页面右侧（或 sm 端底部）显示「Recent deposits」区，无数据时显示空态文案
- [ ] **F-T9-9** 响应式：375px / 768px / 1440px 三档下布局不破，无水平滚动

### 11.2 非功能验收

- [ ] 前端 `/dashboard/Deposit` 页面展示充值地址、最小金额、链/币列表
- [ ] Webhook 同步处理 P99 < 2s
- [ ] 异步入账延迟 P99 < 30s（从 webhook 落库到 CREDITED）
- [ ] 失败告警在 5 分钟内推送飞书
- [ ] 单元测试覆盖率：safeheron adapter / Registry / pool manager / deposit service ≥ 80%
- [ ] Sandbox 端到端通跑：3 条链 × 2-3 个币种 至少各成功 1 次

---

## 12. 边界 (Boundaries)

### 12.1 Always Do（永远要做）

- ✅ 所有 Safeheron 调用走 Go 后端 `internal/safeheron/`，前端**绝不**直连
- ✅ 入账动作必须在**数据库事务**中完成（account + journal + deposits）
- ✅ 所有 webhook 入口先验签再处理
- ✅ 金额传输与存储用 `string`，禁止 `float`
- ✅ 表名、字段名严格用 snake_case；JSON 输出 camelCase（项目约定）
- ✅ 日志结构化（`internal/logger`），脱敏私钥/API Key/签名头
- ✅ 入账失败 / 异常事件必须告警，不允许"静默失败"
- ✅ 新增表必须有 `created_at` / `updated_at` 并自动维护
- ✅ Migration 必须可回滚（写 Up 也写 Down）
- ✅ Sandbox 实测确认 `safeheron_coin_key` / `token_contract` 后再写入迁移脚本

### 12.2 Ask First（动手前先问）

- ❓ 修改 `account` / `journal` 表结构 — 影响理财模块
- ❓ 修改任何状态枚举的取值 — 会触发 schema 迁移
- ❓ 引入新的第三方依赖（除 `safeheron-api-sdk-go` 外）
- ❓ 部署相关的环境变量调整 / 出口 IP 变更
- ❓ 给用户分配地址的链/币组合超出 §2.3 范围
- ❓ 修改 `internal/coreapi/` 包代码（标记 DEPRECATED 但不动）

### 12.3 Never Do（绝不要做）

- ❌ **不**做链上二次校验（二期工作）
- ❌ **不**做提现 / Safeheron 提币 API 调用
- ❌ **不**做 Auto Sweep 策略配置 / API Co-Signer 部署
- ❌ **不**让前端直连 Safeheron
- ❌ **不**复用别的用户的已分配地址
- ❌ **不**回收已分配的地址到 AVAILABLE
- ❌ **不**在没拿到 `transactionStatus = COMPLETED` 时入账
- ❌ **不**绕过验签、不在测试环境跳过 RSA 校验
- ❌ **不**把私钥 / API Key 写进任何 commit
- ❌ **不**用 `console.log` / `fmt.Println` 输出敏感信息
- ❌ **不**修改 `users` / `account` / `journal` / `wealth_*` 这些非钱包模块的表
- ❌ **不**在 Phase 1 删除 `internal/coreapi/` 包代码（保留以兼容 test 编译）

---

## 13. 已知风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| 仅依赖 Safeheron `COMPLETED`，无独立链上校验兜底 | Safeheron 状态错误会直接错账 | 一期接受（无真实用户），二期补 |
| 出口 IP 白名单依赖部署环境 | 切环境/扩容失败 | 部署前 ops 确认固定 IP，预留 2 个 |
| AddCoin 失败导致地址不可用 | 用户充值无人监听 | 任一 coinKey AddCoin 失败 → 整个地址进 ERROR 状态，运维通过 `cmd/pool_recoin/main.go` 重试或废弃 |
| 100+ 钱包预生成耗时长 | 部署窗口长 | 异步生成，Phase 1 用 `cmd/pool_init/main.go` 一次性灌池 |
| Webhook 私钥 / 公钥配置错误 | 全部充值无法入账 | 启动健康检查接口验证 keypair, 失败拒绝启动 |
| 历史 USDT 合约切换风险（生僻链） | 写错 token_contract 导致币种识别错误 | Phase 1 只支持 ETH/BSC/TRON，三条链 USDT 合约稳定 |
| sandbox 团队默认 API Key 缺「钱包账户管理」权限 | 创建钱包返回 code:1005 | 控制台 Setting→API→Edit 勾选该权限 |
| **Webhook ack body 格式错误** | Safeheron 判失败，每条事件按 30s/1m/5m/1h/12h/24h 重发 6 次，DB + 监控压力激增 | 字符串严格匹配 `{"code":"200","message":"SUCCESS"}`；单元测试断言 handler 返回值；V6 已踩过坑 |
| **Webhook 状态乱序到达** | COMPLETED 之后再收 CONFIRMING，朴素 UPSERT 会让状态回退，导致入账重算 / 状态显示错误 | `deposits.status_rank` 单调字段 + UPDATE WHERE 守卫，详见 §4.6 / §6.4 |
| **回溯扫描的存量入账不推 webhook** | AddCoin 之前已上链的链上余额，添加 coinKey 后只更新 balance 不补推 webhook | 部署时新增 coinKey 后必须在控制台核对历史余额，必要时调 `/v1/webhook/resend` 主动重发 |
| **生产 / 测试 coinKey 误用** | dev seed 写入 prod 数据库会让钱包扫错链 | 严格按 `APP_ENV` 选择 seed 文件（§4.7）；CI 检查 production migration 不含 testnet coinKey 字符串 |
| **Dev/test 环境覆盖不全** | Sandbox 测试 team 不支持 BSC 测试网 + Sepolia USDT + Shasta USDT 共 5 个币种，上生产前未在 sandbox 跑过 E2E（详见 §2.3.2） | staging 环境用生产 Safeheron team API Key + 真小额做最终验证；监控告警优先级提到 5 分钟内推送 |
| **MistTrack 单服务商不可用** | Phase 1 只接 MistTrack，服务商不可用时 amlList[].status='FAILED'（最长 8h 标记），所有充值全部进 MANUAL_REVIEW，运营压力大 | Phase 1 接受（接两家成本高且大部分能在 webhook 兜底）；告警 ERROR 级别 + 监控 KYT_PROVIDER_FAILED 比例；Phase 2 可加 Chainalysis / Elliptic |
| **`AML_KYT_ALERT` webhook 永不到达** | Console Webhook 未启用 / 网络异常 → KYT_PENDING 堆积 | 20min 超时兜底 API 主动查询；ops 监控 `status=KYT_PENDING` 的最长停留时间，超阈值告警 |
| **AML_KYT_ALERT 在 deposit 创建前到达** | webhook 乱序，事件找不到对应 deposit | 事件保留 PENDING 待下次 worker 轮询关联；超过 `KYT_ORPHAN_ALERT_MAX_RETRY` 默认 100 次后转 MANUAL_REVIEW(KYT_ORPHAN_ALERT) + ERROR 告警 |
| **MistTrack 把 MEDIUM 风险打得过松/严** | Phase 1 配置 MEDIUM → MANUAL_REVIEW（K-1）；如果 MistTrack 默认把"经过中心化交易所"标 MEDIUM，会让大量正常充值进人工 | 上线初期监控 MEDIUM 占比，必要时在 Safeheron Console 调整风险等级映射（不改代码） |
| **生产环境误关 KYT** | 配置错误绕过合规检查 | `APP_ENV=production` 启动时 `KYT_ENABLED=false` 直接 panic（K-16）；CI / 部署脚本 grep 检查 |

---

## 14. Phase 2 / 3 预览（不在本期实现）

### Phase 2

- 提现：调用 Safeheron 创建提币交易（需要新 API Key 权限）
- 提现地址白名单 + 24h 冻结
- Auto Sweep 归集配置 + API Co-Signer 部署
- Gas Station 配置
- 链上二次校验（接入 Etherscan / TronGrid）
- 状态机走完 6 状态
- **KYT 扩展**（Phase 1 已落地基础，Phase 2 加增强）：
  - 接入 Chainalysis / Elliptic 第二、第三家服务商（三家投票降低单点风险）
  - 运维放行接口 `POST /api/admin/deposits/:id/release` — MANUAL_REVIEW 误判时人工触发完整入账流程（含审计字段：reviewer / reviewed_at / review_reason）（K-18）
  - 提现 KYT：调用提币前对目标地址做 `/v1/tools/aml-checker/*` 风险评估
  - KYT 风险等级动态阈值（运营后台可配置 MEDIUM → CREDITED 或 MANUAL_REVIEW）

### Phase 3

- 运营后台（链/币动态配置、地址池监控、人工 review 处理）
- 自定义确认数配置
- 多 EVM 链扩展（Polygon / Arbitrum / Base / Optimism）
- UTXO 链支持（BTC）
- 老 Monnaire 数据下线评估
- 对账 / 报表

---

## 15. 实施里程碑（粗粒度，一周内）

| Day | 内容 |
|-----|------|
| D1 | Safeheron sandbox 申请 + API Key 创建 + 拉取币种列表确认 coinKey 值。`chains` / `coin_chains` migration + seed |
| D2 | Safeheron SDK 接入 + adapter 封装 + 签名验签单元测试。Registry 实现 + 启动加载 + 后台刷新 |
| D3 | `address_pool` migration + `cmd/pool_init/main.go` 预生成脚本。Sandbox 实测分配 1 个钱包通 |
| D4 | `address_pool` 补水 scheduler。`/api/wallet/deposit-address` handler + 并发分配测试 |
| D5 | `safeheron_webhook_events` migration + Webhook handler 同步验签落库 + 异步 worker 入账。`deposits` 表扩展 |
| D6 | 端到端 sandbox 验证（3 链 × 2-3 币） + 异常路径（MANUAL_REVIEW + 告警） |
| D7 | 文档 / 回归测试 / 灰度上线 |

> 实际任务拆解由后续 `/agent-skills:plan` 产出。

---

## 16. 变更记录

| 日期 | 变更 |
|------|------|
| 2026-05-11 | v1.0 初稿 |
| 2026-05-11 | v1.1 链币配置规范化：新增 `coins` 字典表，`coin_chains` 改为纯关系表（外键 `coin_id` 引用 `coins.id`）。`address_pool` 去除 `added_coin_keys` 字段，AddCoin 规则改为「以 `coin_chains` 为系统级单一来源」。`§6.4` 充值入账伪代码强化为单事务 5 层并发防御。新增 `§9.5 环境隔离`，引入 `APP_ENV`。`account(user_id, currency)` 加 UNIQUE 索引以支持 ON CONFLICT upsert。 |
| 2026-05-11 | v1.2 sandbox 实测产出落地（V2/V3/V4/V5 通过）：(a) §2.3 / §4.7 coinKey 实测修正 — `USDT_BSC`→`USDT_BEP20`、`USDC_BSC`→`USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET`、`USDT_TRX`→`USDT_TRC20`，全部 8 行 token_contract / decimals 实测确认。(b) §4.4 / §6.1 地址池预生成改为按 `network_family` 而非按 chain — 实测证实单 EVM 钱包同时 AddCoin ETH+BSC 全部 coinKey 共享同一 `0x...` 地址，钱包数减半。(c) §13 删除「coinKey 训练数据可能过时」（已验证），新增「API Key 钱包账户管理权限缺失」风险。(d) §6.4 webhook 字段映射 / §10 签名 Header 部分待 V6 实测后修订。 |
| 2026-05-11 | v1.3 sandbox V6/V7 webhook 实测 + 官方文档对齐：(a) **环境分层** — §2.3 拆 2.3.1 生产 (mainnet) + 2.3.2 测试 (testnet) coinKey，schema 不变靠 `APP_ENV` 注入；§2.3.2 已实测 `ETH(SEPOLIA)_..._SEPOLIA` / `USDCOIN_ERC20_..._SEPOLIA` / `TRX(SHASTA)_..._TESTNET` 三个，其他 D1 阶段补；§4.7 seed 拆 mainnet/testnet 两套 SQL；§4.4 / §6.1 AddCoin coinKey 集合从环境内 `coin_chains` 表动态注入。(b) **§4.6 deposits 表重设计** — 删除 `log_index` 字段（V7 + 官方文档确认 webhook 无此字段）；UNIQUE 改为 `(safeheron_tx_key)` 单字段；新增 `safeheron_status` + `safeheron_sub_status` + `status_rank` 单调字段防 webhook 状态乱序回退。(c) **§6.4 webhook 完全重写** — 真实结构是嵌套 `{eventType, eventDetail}`；`eventType` 14 种 Phase 1 只关心 `TRANSACTION_CREATED` + `TRANSACTION_STATUS_CHANGED`；入账条件改为 `transactionStatus='COMPLETED' AND transactionSubStatus='CONFIRMED' AND transactionDirection='INFLOW'`；幂等键 = `(txKey, transactionStatus)`；UPSERT + status_rank 守卫；ack body 必须严格匹配 `{"code":"200","message":"SUCCESS"}` 否则 30s/1m/5m/1h/12h/24h 重试 6 次（V6 实测踩过）；记录 Safeheron 提供的 `/v1/webhook/resend*` 补救接口。(d) **§10** — 签名从 X-Sign Header 假设改为信封内 `sig` 字段 + SDK `WebhookConverter` 一步处理；并发防御 5→6 层（新增乱序保护）。(e) **§13** — 新增 4 个风险：ack body 错误、状态乱序、回溯扫描不推 webhook、生产/测试 coinKey 误用。**纠正 v1.2 错误判断**：原以为 sandbox = mainnet 视图、testnet 走后台路由；实际 sandbox 有独立 testnet coinKey 集合，不存在自动路由。 |
| 2026-05-11 | v1.4 D1 收尾：(a) §2.3.2 testnet 表收紧 — sandbox `/v1/coin/list` 实测返回 325 个 coin **全是 mainnet**（不含 testnet），结合 list-accounts 钱包 1 默认带的 27 个 coin 反查，确认测试 team 只支持 3 个 testnet coinKey（ETH/USDC Sepolia + TRX Shasta），不支持 BSC 测试网整条 + USDT 测试系列共 5 项。(b) §4.7 testnet seed 收紧为只插 3 行（不支持的 5 个不进 dev/test 数据库），Sepolia USDC token_contract = `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238`（Circle 官方）。(c) §13 新增「Dev/test 环境覆盖不全」风险条目，staging 环境用 prod team API Key 真小额做最终验证。 |
| 2026-05-14 | v1.6 RSA 密钥文件化（**T12 任务**，原 Phase 2 提前到本期合并前）：(a) §10 表格「私钥不入仓」行更新，指向新 §10.1；(b) **新增 §10.1** — 4 个 RSA PEM 从 env 文本（`SAFEHERON_*_KEY_PEM`）改为文件路径（`SAFEHERON_*_KEY_PATH`），运维放置在仓库根 `secrets/` 目录（已加 `.gitignore`），SDK 直接读取真实文件；删除 `internal/safeheron/client.go` 的 `writeTempPEM` / `tempDir` / `cleanupFiles` 整套机制及 `Close()` 中的清理逻辑（commit `fc44c99` SEC-2 引入）；新增 `validateKeyFile` 帮助函数做存在/类型/权限校验；**顺带补 `.env.example` 历史遗留**（缺 `SAFEHERON_WEBHOOK_PRIVATE_KEY_PEM` env 占位，container.go 实际读取此 env）。**决策对齐**：硬切换不兼容旧 env、权限 warn 不阻塞、CI 不动（测试服务器手工放置一次）。验收标准 K-PATH-1 ~ K-PATH-7（详见 §10.1）。原 T12 / T13 编号交换：新 T12 = RSA 密钥文件化、新 T13 = Sandbox 端到端 + 灰度上线。 |
| 2026-05-12 | v1.5 KYT 合规筛查并入 Phase 1（19 项决策 K-1 ~ K-19）：(a) §2.1 In Scope 新增 KYT 项；(b) §4.6 deposits 表扩展 4 个 AML 字段（`aml_screening_state` / `aml_risk_level` / `aml_evaluated_at` / `aml_list JSONB`）+ 新增 `idx_deposits_kyt_pending` 部分索引；status 枚举新增 `KYT_PENDING`；(c) §6.4 入账伪代码插入 KYT 初查节点，TRIGGERED+LOW 才放行入账；(d) **新增 §6.5 KYT 合规筛查** — 处置矩阵（K-1/K-5/K-6/K-7/K-8）、主路径 AML_KYT_ALERT webhook（含 K-13 乱序保护：找不到 deposit 则事件保留 PENDING 待轮询）、辅路径 API 初查 + 超时兜底（K-9 20min）、K-19 超时仍 IN_PROGRESS 直接 MANUAL_REVIEW；原 §6.5 异常处理重命名为 §6.6；(e) §7.1 / §7.2 状态机加 KYT_PENDING 流转；(f) §8.2 webhook 注明 AML_KYT_ALERT 由统一入口分发；(g) §9.1 API Key 权限加 Compliance/KytReport + Console 配置清单（开启 AML + Webhook 通知）；(h) §9.6 加 4 个环境变量（`KYT_ENABLED` / `KYT_TIMEOUT=20m` / `KYT_SCAN_INTERVAL=1m` / `KYT_ORPHAN_ALERT_MAX_RETRY=100`）+ K-16 启动校验（prod 强制 enable）；(i) §10 加 4 条 KYT 安全要求；(j) §11.1 加 15 条 KYT 功能验收项 F-KYT-1 ~ F-KYT-15；(k) §13 加 5 条 KYT 风险；(l) §14 Phase 2 删除「KYT 接入」（已上移到 Phase 1），新增「KYT 扩展」TODO（三家服务商 / 运维放行接口 K-18 / 提现 KYT / 动态阈值）。**决策对齐**：仅接 MistTrack 一家（K-4），前端 KYT_PENDING 文案「Under compliance review」（K-15），不回填历史数据（K-14）。 |

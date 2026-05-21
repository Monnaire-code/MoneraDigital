# Approval Callback Service — 技术规格

> **版本**: v1.0
> **日期**: 2026-05-21
> **状态**: DRAFT — 待用户确认后施工
> **分支**: dev

---

## 1. 目标

开发 Approval Callback Service，接收 Safeheron API Co-Signer 的审批回调，执行白名单校验后返回 APPROVE/REJECT 决定。本期聚焦**归集业务**（Auto Sweep / Auto Fuel / UTXO Collection），架构设计预留提币（NORMAL）扩展口子。

### 1.1 核心原则

- **Don't Trust, Verify** — 逐项校验交易合法性
- **白名单机制** — 仅允许符合规则的交易通过，其余一律 REJECT
- **审计可追溯** — 所有审批记录入库，REJECT 推飞书告警

### 1.2 范围

| 包含 | 不包含 |
|------|--------|
| AUTO_SWEEP 审批 | 提币（NORMAL）审批逻辑（预留接口） |
| AUTO_FUEL 审批 | MPC_SIGN 详细审批逻辑（预留，默认 REJECT） |
| UTXO_COLLECTION 审批 | WEB3_SIGN 详细审批逻辑（预留，默认 REJECT） |
| CALLBACK_TEST 连通性测试 | Safeheron Console 配置 |
| 审批记录入库 + 飞书告警 | 前端 UI |

---

## 2. 架构

### 2.1 请求流程

```
API Co-Signer
    ↓ POST /api/cosigner/callback (V3 协议)
Gin Handler
    ↓ IP 白名单校验（可选，复用 SAFEHERON_WEBHOOK_ALLOWED_IPS 或独立配置）
    ↓ SDK CoSignerConverter.RequestV3Convert() — RSA-PSS 验签 + base64 解码
    ↓ JSON 反序列化 → CoSignerBizContentV3{approvalId, type, detail}
Approval Service
    ↓ 路由到具体 Approver（按 type 分发）
    ↓ 校验逻辑（白名单、目标账户、金额策略）
    ↓ 写审批记录到 DB
    ↓ REJECT → 飞书告警
    ↓ 构造 CoSignerResponseV3{approvalId, action}
SDK CoSignerConverter.ResponseV3Converter()
    ↓ base64 编码 + RSA-PSS 签名
    ↓ 返回 JSON map → HTTP 200 响应
```

### 2.2 模块划分

```
internal/
├── safeheron/
│   └── cosigner.go            # 新增：SDK cosigner 包的封装
│                               # CoSignerBizContentV3 / TransactionApproval / ...
│                               # CosignerClient 包装 CoSignerConverter
├── approval/
│   ├── service.go             # ApprovalService — 入口分发 + 审批记录持久化
│   ├── approver.go            # Approver 接口 + 各 type 实现
│   ├── transaction_approver.go # TRANSACTION 类型审批（含 transactionType 子分发）
│   ├── models.go              # ApprovalRecord / 配置模型
│   └── repository.go          # DB 操作（approval_records 表）
├── handlers/
│   └── cosigner_callback_handler.go  # Gin HTTP handler
├── container/
│   └── container.go           # 新增 WithCosignerCallback option
└── routes/
    └── routes.go              # 新增路由注册
```

### 2.3 与现有模块的关系

| 现有模块 | 关系 |
|---------|------|
| `internal/safeheron/client.go` | cosigner.go 与其并列，共享 safeheron 包 |
| `internal/alert/alert.go` | REJECT 时调用 AlertService.Send() |
| `internal/handlers/safeheron_webhook_handler.go` | 参考其 IP 白名单、body 限制模式 |
| `internal/container/container.go` | 新增 WithCosignerCallback() option |

---

## 3. SDK 封装（`internal/safeheron/cosigner.go`）

### 3.1 SDK 已有能力（不造轮子）

| SDK 方法 | 用途 |
|---------|------|
| `cosigner.CoSignerConverter.RequestV3Convert(CoSignerCallBackV3)` | 验签 + 解码请求 |
| `cosigner.CoSignerConverter.ResponseV3Converter(CoSignerResponseV3)` | 编码 + 签名响应 |
| `cosigner.CoSignerCallBackV3` | V3 请求结构体 |
| `cosigner.CoSignerResponseV3` | V3 响应结构体（action + approvalId） |
| `cosigner.CoSignerConfig` | 配置（CoSignerPubKey + PrivateKey） |

### 3.2 需要补充的类型定义

SDK **不含**以下业务数据结构，需要在 `internal/safeheron/cosigner.go` 中定义：

```go
// CoSignerBizContentV3 是 API Co-Signer 回调的 bizContent 解码后的顶层结构。
type CoSignerBizContentV3 struct {
    ApprovalId string          `json:"approvalId"`
    Type       string          `json:"type"`    // TRANSACTION / MPC_SIGN / WEB3_SIGN / CALLBACK_TEST
    Detail     json.RawMessage `json:"detail"`  // 按 type 延迟解析
}

// TransactionApproval 是 type=TRANSACTION 时 detail 的结构。
type TransactionApproval struct {
    TxKey                      string                   `json:"txKey"`
    TxHash                     string                   `json:"txHash"`
    CoinKey                    string                   `json:"coinKey"`
    TxAmount                   string                   `json:"txAmount"`
    SourceAccountKey           string                   `json:"sourceAccountKey"`
    SourceAccountType          string                   `json:"sourceAccountType"`
    SourceAddress              string                   `json:"sourceAddress"`
    SourceAddressList          []AddressEntry            `json:"sourceAddressList"`
    DestinationAccountKey      string                   `json:"destinationAccountKey"`
    DestinationAccountType     string                   `json:"destinationAccountType"`
    DestinationAddress         string                   `json:"destinationAddress"`
    Memo                       string                   `json:"memo"`
    DestinationAddressList     []DestinationAddressEntry `json:"destinationAddressList"`
    DestinationProfile         *DestinationProfile       `json:"destinationProfile"`
    TransactionType            string                   `json:"transactionType"`
    TransactionStatus          string                   `json:"transactionStatus"`
    TransactionSubStatus       string                   `json:"transactionSubStatus"`
    CreateTime                 int64                    `json:"createTime"`
    Note                       string                   `json:"note"`
    AuditUserKey               string                   `json:"auditUserKey"`
    CreatedByUserKey           string                   `json:"createdByUserKey"`
    EstimateFee                string                   `json:"estimateFee"`
    FeeCoinKey                 string                   `json:"feeCoinKey"`
    ReplaceTxHash              string                   `json:"replaceTxHash"`
    CustomerRefId              string                   `json:"customerRefId"`
    ReplacedTxKey              string                   `json:"replacedTxKey"`
    ReplacedCustomerRefId      string                   `json:"replacedCustomerRefId"`
    CustomerExt1               string                   `json:"customerExt1"`
    CustomerExt2               string                   `json:"customerExt2"`
    TransactionDirection       string                   `json:"transactionDirection"`
    AmlScreeningTriggeredState string                   `json:"amlScreeningTriggeredState"`
    AmlList                    json.RawMessage          `json:"amlList"`
}

type AddressEntry struct {
    Address string `json:"address"`
}

type DestinationAddressEntry struct {
    Address string `json:"address"`
    Memo    string `json:"memo"`
    Amount  string `json:"amount"`
}

type DestinationProfile struct {
    ConnectId string `json:"connectId"`
    Name      string `json:"name"`
}

// MPCSignApproval 是 type=MPC_SIGN 时 detail 的结构。
type MPCSignApproval struct {
    TxKey                string          `json:"txKey"`
    TransactionStatus    string          `json:"transactionStatus"`
    TransactionSubStatus string          `json:"transactionSubStatus"`
    CreateTime           int64           `json:"createTime"`
    SourceAccountKey     string          `json:"sourceAccountKey"`
    CreatedByUserKey     string          `json:"createdByUserKey"`
    CustomerRefId        string          `json:"customerRefId"`
    CustomerExt1         string          `json:"customerExt1"`
    CustomerExt2         string          `json:"customerExt2"`
    SignAlg              string          `json:"signAlg"`
    DataList             json.RawMessage `json:"dataList"`
}

// Web3Approval 是 type=WEB3_SIGN 时 detail 的结构。
type Web3Approval struct {
    TxKey                string          `json:"txKey"`
    SubjectType          string          `json:"subjectType"`
    AccountKey           string          `json:"accountKey"`
    SourceAddress        string          `json:"sourceAddress"`
    TransactionStatus    string          `json:"transactionStatus"`
    TransactionSubStatus string          `json:"transactionSubStatus"`
    CreatedByUserKey     string          `json:"createdByUserKey"`
    CreateTime           int64           `json:"createTime"`
    AuditUserKey         string          `json:"auditUserKey"`
    CustomerRefId        string          `json:"customerRefId"`
    CustomerExt1         string          `json:"customerExt1"`
    CustomerExt2         string          `json:"customerExt2"`
    Note                 string          `json:"note"`
    Transaction          json.RawMessage `json:"transaction"`
    Message              json.RawMessage `json:"message"`
    MessageHash          json.RawMessage `json:"messageHash"`
}
```

### 3.3 CosignerClient 封装

> **注意**：CosignerClient 与现有 SafeheronClient 是**独立类型**，使用不同密钥对、不同 SDK 子模块，不实现同一接口。

```go
// CosignerConfig 配置 Co-Signer 回调所需的密钥文件路径。
// 注意：SDK CoSignerConfig 的字段名 CoSignerPubKey / ApprovalCallbackServicePrivateKey
// 有误导性，实际接受的是文件路径（SDK 内部调用 loadPrivateKeyFromPath 读取）。
type CosignerConfig struct {
    CoSignerPubKeyPath       string // Co-Signer 公钥 PEM 文件路径（0644）
    CallbackPrivateKeyPath   string // Callback Service 私钥 PEM 文件路径（0600）
}

// CosignerClient 封装 SDK 的 CoSignerConverter，提供类型安全的请求解析和响应构造。
type CosignerClient struct {
    converter cosigner.CoSignerConverter
}

// NewCosignerClient 构造 CosignerClient。
// 复用现有 validateKeyFile() 校验文件存在性和权限。
// 路径为空或文件不存在时返回 error（调用方决定是 panic 还是降级）。
func NewCosignerClient(cfg CosignerConfig) (*CosignerClient, error) {
    // validateKeyFile(cfg.CoSignerPubKeyPath, "CoSignerPubKey", 0o644)
    // validateKeyFile(cfg.CallbackPrivateKeyPath, "CallbackPrivateKey", 0o600)
    // converter = cosigner.CoSignerConverter{Config: cosigner.CoSignerConfig{
    //     CoSignerPubKey:                    cfg.CoSignerPubKeyPath,
    //     ApprovalCallbackServicePrivateKey: cfg.CallbackPrivateKeyPath,
    // }}
}

// ParseRequest 验签 + 解码回调请求，返回类型化的 BizContent。
func (c *CosignerClient) ParseRequest(req cosigner.CoSignerCallBackV3) (*CoSignerBizContentV3, error) {
    // plaintext, err := c.converter.RequestV3Convert(req)
    // json.Unmarshal(plaintext) → CoSignerBizContentV3
}

// BuildResponse 构造签名响应。
func (c *CosignerClient) BuildResponse(approvalId, action string) (map[string]string, error) {
    // resp := cosigner.CoSignerResponseV3{ApprovalId: approvalId, Action: action}
    // return c.converter.ResponseV3Converter(resp)
}
```

---

## 4. 审批逻辑

### 4.1 Approver 接口

```go
type ApprovalDecision struct {
    Action string // "APPROVE" / "REJECT"
    Reason string // 审批理由（记录到 DB + 告警）
}

type Approver interface {
    Evaluate(ctx context.Context, bizContent *CoSignerBizContentV3) (*ApprovalDecision, error)
}
```

### 4.2 类型分发

| bizContent.type | Approver | 本期状态 |
|----------------|----------|---------|
| TRANSACTION | TransactionApprover | ✅ 完整实现 |
| MPC_SIGN | DefaultRejectApprover | 预留，默认 REJECT |
| WEB3_SIGN | DefaultRejectApprover | 预留，默认 REJECT |
| CALLBACK_TEST | CallbackTestApprover | ✅ 固定 APPROVE |
| 未知类型 | — | 固定 REJECT |

### 4.3 TransactionApprover — 按 tx_type 校验

> **命名区分**：Safeheron API 返回的 JSON 字段名为 `transactionType`（Go struct tag 不可改），我们的 DB 字段和 env 变量统一使用 `tx_type`。

#### AUTO_SWEEP

| 校验维度 | 规则 | REJECT 条件 |
|---------|------|------------|
| 目标账户类型 | `destinationAccountType == "VAULT_ACCOUNT"` | 非 VAULT_ACCOUNT |
| 目标账户 | `destinationAccountKey` 在配置的归集目标账户白名单中 | 不在白名单 |
| 金额合理性 | ⚠️ **待验证** — 见下方说明 | — |

#### AUTO_FUEL

| 校验维度 | 规则 | REJECT 条件 |
|---------|------|------------|
| 目标账户类型 | `destinationAccountType == "VAULT_ACCOUNT"` | 非 VAULT_ACCOUNT |
| 金额合理性 | ⚠️ **待验证** — 见下方说明 | — |

#### UTXO_COLLECTION

| 校验维度 | 规则 | REJECT 条件 |
|---------|------|------------|
| 目标账户类型 | `destinationAccountType == "VAULT_ACCOUNT"` | 非 VAULT_ACCOUNT |

#### 金额校验策略（待测试环境验证后定稿）

本期 **先只校验账户类型 + 白名单**（这两项是硬性要求）。金额校验涉及以下未确定因素：

- 归集金额与充值记录的关联方式（是否可按 sourceAddress 匹配 deposits 表做交叉验证）
- 非 stablecoin 的 USD 汇率来源
- Safeheron Auto Sweep 策略的实际触发金额

代码中预留金额校验的扩展点并注释说明，在测试环境接入真实归集数据后，根据实际字段值决定校验细节。

#### NORMAL（预留，本期不实现）

本期收到 `tx_type=NORMAL` 时默认 **REJECT**，预留未来提币审批扩展。

#### 其他 tx_type

一律 REJECT + 告警。

### 4.4 chain_symbol 推导

`chain_symbol` 不在 Safeheron 回调字段中，需从 `coinKey` 通过 WalletRegistry 内存缓存反查：

```
coinKey (e.g. "USDT_ERC20") → WalletRegistry.GetCoinChainBySafeheronKey(coinKey) → CoinChain.Chain.ShortName → chain_symbol
```

- **直接读内存缓存**，不查数据库。Registry 已在上版本实现了内存缓存 + 后台定时刷新（币链配置变更频率极低）。
- Registry 缓存中查不到时：`chain_symbol` 记为 `'UNKNOWN'`，日志 warning（不影响审批决定）。
- CALLBACK_TEST 等无 coinKey 的类型：`chain_symbol` 为 `'UNKNOWN'`。
- 审批校验不依赖 `chain_symbol`，它仅用于记录和查询。

### 4.5 幂等处理

Co-Signer 可能对同一 `approvalId` 重试回调。处理策略：

1. INSERT `approval_records` 遇 UNIQUE 冲突
2. 按 `approvalId` 查回首次记录的 `action`
3. 用首次结果重新构造签名响应返回

确保相同输入始终返回相同决定，不重复写 DB 和告警。

### 4.6 校验配置

通过环境变量配置（后续可迁移到 DB）：

```env
# 归集目标账户白名单（逗号分隔的 accountKey）
# 为空时一律 REJECT + 启动 warning 日志（安全默认）
COSIGNER_SWEEP_TARGET_ACCOUNTS=account-key-1,account-key-2

# 允许的 tx_type 白名单（逗号分隔）
COSIGNER_ALLOWED_TX_TYPES=AUTO_SWEEP,AUTO_FUEL,UTXO_COLLECTION
```

> **注**：金额校验相关的 env（如 `COSIGNER_SWEEP_MAX_AMOUNT_USD`）待测试环境验证后补充，本期不使用。

---

## 5. 数据库

### 5.1 approval_records 表

```sql
CREATE TABLE IF NOT EXISTS approval_records (
    id              BIGSERIAL    PRIMARY KEY,
    approval_id     VARCHAR(128) NOT NULL UNIQUE,
    callback_type   VARCHAR(32)  NOT NULL,  -- TRANSACTION / MPC_SIGN / WEB3_SIGN / CALLBACK_TEST
    tx_type         VARCHAR(32),            -- NORMAL / AUTO_SWEEP / AUTO_FUEL / UTXO_COLLECTION（仅 TRANSACTION 时）
    action          VARCHAR(16)  NOT NULL,  -- APPROVE / REJECT
    reason          TEXT,
    tx_key          VARCHAR(128),
    chain_symbol    VARCHAR(32)  DEFAULT 'UNKNOWN',  -- 区块链标识：ETH / BSC / TRON / BTC（coinKey 查不到时为 UNKNOWN）
    coin_key        VARCHAR(64),
    tx_amount       VARCHAR(64),
    source_account_key      VARCHAR(128),
    destination_account_key VARCHAR(128),
    destination_account_type VARCHAR(32),
    destination_address     VARCHAR(256),
    customer_ref_id VARCHAR(128),
    raw_request     JSONB        NOT NULL,  -- 完整 bizContent 原文，审计留痕
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_approval_records_tx_key ON approval_records(tx_key);
CREATE INDEX idx_approval_records_created_at ON approval_records(created_at);
CREATE INDEX idx_approval_records_action ON approval_records(action);
```

### 5.2 sweep_transactions 表（归集流水）

```sql
CREATE TABLE IF NOT EXISTS sweep_transactions (
    id                       BIGSERIAL    PRIMARY KEY,

    -- Safeheron 交易标识
    tx_key                   VARCHAR(128) NOT NULL UNIQUE,
    tx_hash                  VARCHAR(256),
    customer_ref_id          VARCHAR(128),

    -- 交易分类
    tx_type                  VARCHAR(32)  NOT NULL,
    -- AUTO_SWEEP / AUTO_FUEL / UTXO_COLLECTION

    -- 区块链 & 币种 & 金额
    chain_symbol             VARCHAR(32)  NOT NULL DEFAULT 'UNKNOWN',  -- 区块链标识：ETH / BSC / TRON / BTC
    coin_key                 VARCHAR(64)  NOT NULL,
    fee_coin_key             VARCHAR(64),
    tx_amount                VARCHAR(64)  NOT NULL,
    estimate_fee             VARCHAR(64),

    -- 来源
    source_account_key       VARCHAR(128),
    source_address           VARCHAR(256),

    -- 目标
    destination_account_key  VARCHAR(128),
    destination_address      VARCHAR(256),

    -- 状态跟踪
    tx_status                VARCHAR(32)  NOT NULL DEFAULT 'PENDING',
    tx_sub_status            VARCHAR(64),

    -- 审批关联
    approval_id              VARCHAR(128),
    approval_action          VARCHAR(16),  -- APPROVE / REJECT

    -- 时间线
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    completed_at             TIMESTAMPTZ
);

CREATE INDEX idx_sweep_tx_type ON sweep_transactions(tx_type);
CREATE INDEX idx_sweep_tx_status ON sweep_transactions(tx_status);
CREATE INDEX idx_sweep_created_at ON sweep_transactions(created_at);
CREATE INDEX idx_sweep_coin_key ON sweep_transactions(coin_key);
CREATE INDEX idx_sweep_chain ON sweep_transactions(chain_symbol);
```

| 字段 | 说明 |
|------|------|
| `tx_key` | Safeheron 交易唯一标识（UNIQUE） |
| `tx_hash` | 链上交易 hash，上链后由 webhook 更新 |
| `tx_type` | AUTO_SWEEP / AUTO_FUEL / UTXO_COLLECTION |
| `chain_symbol` | 区块链标识（ETH / BSC / TRON / BTC） |
| `coin_key` | 归集币种（ETH、USDT_ERC20 等） |
| `fee_coin_key` | 手续费币种（ERC20 归集时为 ETH） |
| `source_address` | 来源地址（地址池中的充值地址） |
| `destination_address` | 目标地址（汇总钱包） |
| `tx_status` | Safeheron 交易状态：PENDING → SIGNING → BROADCASTING → CONFIRMING → COMPLETED / FAILED |
| `approval_id` | 关联 approval_records.approval_id |
| `approval_action` | 审批结果快照 |

**数据写入规则**：
- **仅 APPROVE 的归集交易写入此表**，REJECT 的只记录在 `approval_records` 中。
- sweep_transactions 是归集流水台账，REJECT 的交易不会上链，无需追踪。

**写入时机**：
1. **审批回调 APPROVE 时**（INSERT）— 从 TransactionApproval 提取字段创建记录，`tx_status` 取回调携带的状态
2. **Webhook 推送时**（UPDATE）— Safeheron webhook 推送归集交易的状态变更（出向/SEND 方向），按 `tx_key` 匹配更新 `tx_status` / `tx_hash` / `completed_at`

**Webhook 更新路径**：
- **主路径**：在现有 `safeheron_webhook_handler` 中增加出向交易（SEND）分支，识别 `tx_key` 存在于 `sweep_transactions` 时更新状态
- **兜底**：定时轮询 Safeheron `GetTransaction` API，补偿 webhook 丢失或延迟的场景（复用现有兜底模式）

> ⚠️ **竞态待验证**：Webhook 可能先于审批回调到达（Safeheron 回调时序未在文档中明确保证）。如果 webhook UPDATE 找不到 sweep_transactions 记录，当前策略是忽略（由定时轮询兜底补全）。此行为需在测试环境接入真实归集数据后验证，确认实际时序后定稿。代码中预留注释标记此处理。

### 5.3 Migration

新增 migration 文件 `023_create_approval_and_sweep_tables.go`（编号跟现有最大编号递增），一个 migration 包含两张表，遵循现有 migration 模式。

---

## 6. Handler

### 6.1 路由

```
POST /api/cosigner/callback   # 公开路由（无 JWT），与 webhooks 同级
```

注册位置：`routes.go` 的 public 路由组内，紧邻 webhooks。

### 6.2 Handler 逻辑

```go
func (h *CosignerCallbackHandler) Handle(c *gin.Context) {
    // 0. context timeout 5s（Co-Signer 有回调超时，必须快速响应）
    // 1. IP 白名单校验（可选）
    // 2. 读取 body（MaxBytesReader 1MB 限制）
    // 3. JSON 解码为 CoSignerCallBackV3
    // 4. CosignerClient.ParseRequest() — 验签 + 解码
    // 5. ApprovalService.Evaluate() — 分发 + 校验 + 入库（含幂等处理）
    // 6. CosignerClient.BuildResponse() — 构造签名响应
    // 7. c.JSON(200, response)
}
```

> **超时说明**：Handler 设置 5s context timeout。Co-Signer 有回调超时限制，超时后可能重试。如果 DB 写入成功但响应未送达，重试会命中 §4.5 幂等逻辑，查回首次结果返回。本期不引入新 goroutine。

### 6.3 响应格式

成功时返回 SDK 构造的签名 map：

```json
{
  "code": "200",
  "message": "SUCCESS",
  "timestamp": "1716300000000",
  "version": "v3",
  "bizContent": "base64(...)",
  "sig": "base64(...)"
}
```

错误时（验签失败/内部错误）返回 HTTP 4xx/5xx，Co-Signer 会重试。

---

## 7. 告警

| 场景 | 告警级别 | 飞书通知 |
|------|---------|---------|
| REJECT（任何原因） | ERROR | ✅ 含 approvalId / txKey / type / reason / coinKey / txAmount / sourceAddress / destinationAddress |
| 验签失败 | ERROR | ✅ 含 IP / 错误信息 |
| 未知 type | ERROR | ✅ 含原始 type 值 / approvalId |
| APPROVE | — | 不告警，仅入库 |

---

## 8. 环境变量

### 8.1 新增

```env
# Cosigner 密钥（已有密钥文件，读文件路径）
COSIGNER_PUBLIC_KEY_PATH=secrets/cosigner_public.pem
COSIGNER_CALLBACK_PRIVATE_KEY_PATH=secrets/callback_private.pem

# 审批策略配置
COSIGNER_SWEEP_TARGET_ACCOUNTS=           # 归集目标 accountKey 白名单（为空一律 REJECT）
COSIGNER_ALLOWED_TX_TYPES=AUTO_SWEEP,AUTO_FUEL,UTXO_COLLECTION

# IP 白名单（生产环境建议配置，为空时记 warning 日志）
COSIGNER_ALLOWED_IPS=
```

### 8.2 密钥文件

| 文件 | 权限 | 来源 |
|------|------|------|
| `secrets/cosigner_public.pem` | 0644 | Co-Signer CLI 导出：`./cosigner export-public-key` |
| `secrets/callback_private.pem` | 0600 | 自行生成 RSA-4096 |

---

## 9. 安全

| 维度 | 措施 |
|------|------|
| 传输加密 | V3 协议：RSA-PSS 签名 + base64 编码（非 AES 加密） |
| 验签 | 使用 Co-Signer 公钥验证请求签名，拒绝伪造请求 |
| 响应签名 | 使用 Callback Service 私钥签名响应，防止中间人篡改 |
| IP 白名单 | 可选，限制回调来源 IP |
| Body 限制 | MaxBytesReader 1MB 上限 |
| 密钥管理 | PEM 文件 secrets/ 目录，.gitignore 已覆盖 |
| 审计日志 | 所有请求（含 APPROVE/REJECT）入库，raw_request 保留完整原文 |

---

## 10. 测试策略

### 10.1 单元测试

| 模块 | 测试重点 |
|------|---------|
| `cosigner.go` | ParseRequest 正确解码各 type / BuildResponse 正确构造 |
| `transaction_approver.go` | 各 transactionType 的 APPROVE/REJECT 条件 |
| `service.go` | 未知 type → REJECT / CALLBACK_TEST → APPROVE / 入库验证 |
| `repository.go` | CRUD + approvalId 唯一约束 |
| `handler.go` | IP 白名单 / 验签失败 / 正常流程 |

### 10.2 集成测试

- 使用 mock CoSignerConverter 模拟完整回调流程
- 验证审批记录正确入库
- 验证 REJECT 触发告警调用

### 10.3 覆盖率

目标 ≥ 90%（力争 100%）。钱包业务零容忍，所有正常路径、异常路径、边界条件都要有测试。

---

## 11. 实现步骤

| # | 任务 | 依赖 |
|---|------|------|
| 1 | `internal/safeheron/cosigner.go` — 类型定义 + CosignerClient 封装 | 无 |
| 2 | `internal/approval/models.go` — ApprovalRecord + SweepTransaction + 配置模型 | 无 |
| 3 | `internal/approval/repository.go` — DB 操作（两张表） | #2 |
| 4 | Migration `023_create_approval_and_sweep_tables.go` | #3 |
| 5 | `internal/approval/approver.go` — 接口 + DefaultRejectApprover + CallbackTestApprover | #1 |
| 6 | `internal/approval/transaction_approver.go` — TRANSACTION 审批逻辑 | #1, #5 |
| 7 | `internal/approval/service.go` — 分发 + 入库 + 告警 + 幂等 | #3, #5, #6 |
| 8 | `internal/handlers/cosigner_callback_handler.go` — Gin handler | #1, #7 |
| 9 | Webhook handler 扩展 — 出向交易更新 sweep_transactions | #3 |
| 10 | Container + 路由注册 | #7, #8, #9 |
| 11 | 测试（TDD，各模块并行） | 各模块 |

---

## 12. 开放问题

| # | 问题 | 当前方案 | 后续迭代 |
|---|------|---------|---------|
| 1 | 非 stablecoin 的 USD 汇率转换 | 本期不做，stablecoin 取面值 | 接入价格 oracle |
| 2 | NORMAL 类型提币审批 | 默认 REJECT | 提币功能上线时实现 |
| 3 | MPC_SIGN / WEB3_SIGN 审批 | 默认 REJECT | 有业务需求时实现 |
| 4 | 审批策略从 env 迁移到 DB | env 配置 | 需要动态调整时迁移 |
| 5 | Co-Signer 回调 IP 是否与 webhook IP 相同 | 可选独立配置 | 部署时确认 |

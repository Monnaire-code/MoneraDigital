# Safeheron Sandbox 实测 Checklist

> Phase 1 编码前置工作 — 必须先在 Safeheron sandbox 完成所有验证项
> 关联: [safeheron-phase1-spec.md](./safeheron-phase1-spec.md)
> 预计耗时: 半天到一天

---

## 为什么必须先做 sandbox 实测

Phase 1 SPEC 里 8 行 `coin_chains` 初始数据的 `safeheron_coin_key` 和 `token_contract` 是**示例值**，来自训练数据和公开文档，**不能直接进 production 迁移脚本**。Safeheron 实际 coinKey 命名可能与示例不同（比如 BSC 系到底是 `USDT_BSC` 还是 `BSC_USDT` 还是 `USDT_BEP20`）。

如果用错值，地址池预生成 AddCoin 会全部失败，Webhook 反查 `safeheron_coin_key → coin_chain` 也会全部失败。**所有后续代码白搭**。

---

## 前置准备

- [ ] Safeheron sandbox 账号（联系 BD 申请，或自助注册）
- [ ] 商户 RSA 密钥对（用 `openssl` 本地生成）
  ```bash
  openssl genrsa -out merchant_private.pem 3072
  openssl rsa -in merchant_private.pem -pubout -out merchant_public.pem
  ```
- [ ] 本地开发机或测试机出口公网 IP（用于 sandbox API Key 白名单）
- [ ] 安装 Safeheron Go SDK 到本地实测项目（不一定要在 monera-digital 主仓里，可以单独建一个 `~/scratch/safeheron-sandbox-test/`）
  ```bash
  go get github.com/Safeheron/safeheron-api-sdk-go
  ```

---

## 实测项目清单

### V1. 申请 sandbox API Key

- [ ] 在 Safeheron 控制台创建 API Key（类型：**访问 API**，不是 API Co-Signer）
- [ ] 上传 `merchant_public.pem`
- [ ] 配置出口 IP 白名单
- [ ] 勾选最小权限：**读取** + **管理钱包账户**
- [ ] 不要勾选「发起/取消交易」（Phase 1 不做提现）
- [ ] 下载 Safeheron 平台公钥（用于 API 响应验签）
- [ ] 下载 Safeheron Webhook 签名公钥（如果与平台公钥不同）

**记录**：
```
sandbox_api_base_url:     ___
api_key:                  ___
merchant_private_key:     存放在 ___
platform_public_key:      存放在 ___
webhook_public_key:       存放在 ___
```

---

### V2. 拉取 Safeheron 支持的币种列表 ⭐ 关键

调用 Safeheron API 拉取所有 sandbox 支持的 `coinKey`，从中筛选出 8 个我们需要的组合。

参考 Safeheron API 文档「查询币种列表」接口（可能叫 `/v1/coin/list` 或类似）。

**目标**：找到下表里 8 个组合的真实 `coinKey`：

| 期望组合 | 训练数据示例值（不一定对） | sandbox 实测值 |
|----------|---------------------------|---------------|
| ETHEREUM × ETH | `ETH` | ? |
| ETHEREUM × USDT | `USDT_ERC20` | ? |
| ETHEREUM × USDC | `USDC_ERC20` | ? |
| BSC × BNB | `BNB_BSC` | ? |
| BSC × USDT | `USDT_BSC` | ? |
| BSC × USDC | `USDC_BSC` | ? |
| TRON × TRX | `TRX` | ? |
| TRON × USDT | `USDT_TRX` | ? |

**同时记录**：
- 每个 coinKey 的 `decimals`（Safeheron 返回应该有）
- 每个 token 的 `token_contract`（Safeheron 返回的合约地址，比公开文档可靠）
- 是否有 `chainId` / `network` 字段

---

### V3. 创建 Asset Wallet 验证参数

- [ ] 调用「创建钱包账户」接口，参数：
  ```json
  {
    "customerRefId": "monera-test-001",
    "accountTag": "DEPOSIT",
    "hiddenOnUI": true,
    "autoFuel": false,
    "coinKeyList": ["ETH", "USDT_ERC20", "USDC_ERC20"]  // 用 V2 实测的值
  }
  ```
- [ ] 记录返回的 `accountKey`
- [ ] 检查返回的 `coinAddressList`：每个 coinKey 是否返回了 `address`
- [ ] **关键验证**：3 个 EVM coinKey 返回的 `address` 是否**完全相同**？
  - 如果相同 → SPEC §4.4 地址池设计成立
  - 如果不同 → 需要调整地址池策略（EVM 不能单地址多 coin）

**记录**：
```
account_key: ___
ETH 地址:        ___
USDT_ERC20 地址: ___
USDC_ERC20 地址: ___
地址是否一致:     ___ (Y/N)
```

---

### V4. AddCoin 幂等性验证

- [ ] 对 V3 创建的 accountKey 再次调用 AddCoin，传相同的 `coinKeyList`
- [ ] 期望：返回相同地址，不报错（文档说幂等）
- [ ] 然后增加一个新 coinKey 比如 `BNB_BSC`（或 V2 实测的 BSC 系列）
- [ ] 验证：旧 coinKey 地址不变，新 coinKey 是否复用同一地址？

**这一步直接决定 SPEC §6.1 的 AddCoin 策略是否成立。**

---

### V5. TRON Asset Wallet 单独验证

- [ ] 创建一个新的 Asset Wallet，`coinKeyList=["TRX", "USDT_TRX"]`
- [ ] 验证返回的 TRON 地址（`T` 开头）
- [ ] TRX 和 USDT_TRX 两个 coinKey 返回的地址是否相同？
  - 期望相同（TRON 同 EVM 一样是 account-based）

---

### V6. Webhook 配置与触发

- [ ] 在 Safeheron 控制台配置 Webhook URL（可以用 webhook.site 或 ngrok 本地接收）
- [ ] 给某个 sandbox 地址打一笔测试币（Safeheron 应该提供 sandbox 水龙头或 testnet 转账）
- [ ] 观察 Webhook 推送：
  - HTTP Header 里签名字段名是什么（`X-Sign`? `X-Signature`?）
  - HTTP Header 里时间戳字段名是什么
  - payload 是否加密
  - payload 字段结构（event_id, event_type, txKey, customerRefId, txStatus, amount, address, coinKey...）
  - 同一笔交易会推送几次（`CREATED` → `BROADCASTING` → `CONFIRMED` → `COMPLETED`？）
- [ ] **特别关注**：
  - 哪个字段是「事件唯一 ID」（对应 SPEC `safeheron_webhook_events.event_id`）
  - 哪个字段是「交易唯一 ID」（对应 SPEC `deposits.safeheron_tx_key`）
  - 同一笔交易多笔 Transfer 日志怎么区分（`logIndex` 字段名？）
  - `transactionStatus` 取值枚举（确认 `COMPLETED` 是终态）
  - `direction` 字段是否存在（区分 incoming / outgoing）

**保存样本**：保留 1-2 个完整 webhook payload 样本到本地（脱敏后）。

---

### V7. 验签实测

- [ ] 用 Safeheron 提供的公钥 + Go SDK 验证 webhook 签名
- [ ] 故意改一个字符制造无效签名，确认验签 fail
- [ ] 记录签名算法细节（RSA-SHA256? PKCS1v15 vs PSS？签名内容是 raw body 还是 timestamp+body？）

---

### V8. 错误码与异常场景

- [ ] 故意传错 `coinKey` 调用 AddCoin → 记录错误码
- [ ] 故意传重复 `customerRefId` → 记录返回行为（应该幂等返回相同 accountKey）
- [ ] 模拟网络错误（断开 SDK 连接）→ 观察 SDK 重试行为

---

## 实测产出（必须交付）

完成后请把以下信息填到 SPEC §4.7 初始数据 SQL 里，**替换示例值**：

```sql
-- 把训练数据示例值替换为 sandbox 实测值
INSERT INTO coin_chains (chain_code, coin_id, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, display_order)
SELECT 'ETHEREUM', id, true,  NULL,                                              <实测 decimals>, '<实测 coinKey>', '0.001', 10 FROM coins WHERE symbol='ETH'
...
```

同时把以下信息记到团队内部 wiki（不要进仓库）：
- sandbox API Key + 私钥存放位置
- Webhook payload 样本（脱敏）
- Webhook 签名验证细节
- Safeheron 错误码映射表

---

## 验证完成的判断标准

- [ ] 8 个 `safeheron_coin_key` 全部实测确认，可以填到 migration
- [ ] EVM 多 coinKey 地址一致性确认（Y）→ §4.4 地址池设计成立
- [ ] AddCoin 幂等性确认 → §4.4 AddCoin 规则可执行
- [ ] Webhook 字段名 + 签名格式 + 状态枚举确认 → §6.4 入账逻辑可编码
- [ ] 端到端跑通：创建钱包 → 收到测试币 → Webhook COMPLETED → 验签通过

**全部通过后才能开 D2 写代码。**

---

## 风险

| 风险 | 处理 |
|------|------|
| EVM 多 coinKey 地址**不**一致 | SPEC §4.4 地址池策略需要重新设计 → 紧急调整 |
| Safeheron sandbox 申请耗时长 | 提前 24h 联系 BD |
| 出口 IP 申请白名单耗时 | 测试机用静态 IP 或公网代理 |
| coinKey 命名规则与文档差异大 | 直接采用 sandbox 返回值，不质疑命名 |

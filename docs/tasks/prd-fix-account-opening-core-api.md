# PRD: 修复账户开设页面使用 Core API 获取测试网络地址

## Introduction

修复账户开设页面添加 TRON Testnet 地址的问题。之前使用本地生成的测试地址（TTest...），现在需要改用 Core API 获取真实地址，与存款页面行为保持一致。

## Goals

- 账户开设页面添加地址时统一调用 Core API
- 测试网络地址从 Core API 获取，与主网行为一致
- Core API 调用失败时返回错误（无降级方案）
- 不影响主网货币的现有逻辑

## User Stories

### US-001: 统一使用 Core API 获取地址
**Description:** 作为系统，账户开设页面添加地址时应统一调用 Core API，不管主网还是测试网络。

**Acceptance Criteria:**
- [ ] 移除 isTestnetCurrency 检测和本地地址生成逻辑
- [ ] 所有货币都调用 Core API GetAddress
- [ ] Core API 返回错误时直接返回给调用方

### US-002: 测试网络地址验证
**Description:** 作为用户，我想通过账户开设页面添加测试网络地址，与存款页面行为一致。

**Acceptance Criteria:**
- [ ] USDT + TRX(SHASTA) 通过 Core API 获取地址
- [ ] USDC + TRX(SHASTA) 通过 Core API 获取地址
- [ ] 地址格式正确（非 TTest... 格式）

## Functional Requirements

- FR-1: 移除 `AddAddress` 函数中的 `isTestnetCurrency` 检测逻辑
- FR-2: 移除 `generateTestnetAddress` 函数调用
- FR-3: 所有货币统一调用 `s.coreAPIClient.GetAddress`
- FR-4: Core API 错误直接返回，不做降级处理

## Non-Goals

- 不修改存款页面的逻辑
- 不添加新的测试网络支持
- 不修改其他 API 端点

## Technical Considerations

- 复用现有的 `GetWalletAddress` 逻辑模式
- 与存款页面保持一致的行为

## Success账户开设页面可以 Metrics

- 成功添加 TRX(SHASTA) 测试网络地址
- 地址格式与 Core API 返回一致

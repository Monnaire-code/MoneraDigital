# PRD: 修复 TRON Testnet 钱包地址添加失败问题

## Introduction

修复用户在使用 TRON Testnet (SHASTA) 添加钱包地址时失败的问题。当用户在账户开设页面选择 USDT + TRX(SHASTA) - TRON Testnet 时，系统报错 "failed to get address from Core API: get address failed"。根本原因是 Core API 不支持测试网络货币，解决方案是为测试网络货币生成本地测试地址。

## Goals

- 用户选择 USDT + TRX(SHASTA) 时能够成功添加地址
- 用户选择 USDC + TRX(SHASTA) 时能够成功添加地址
- 主网货币仍然通过 Core API 获取地址（保持原有逻辑）
- 测试地址格式正确（TRON 地址以 T 开头，共 34 位）

## User Stories

### US-001: Testnet 地址本地生成
**Description:** 作为系统，当用户请求添加 TRON Testnet 钱包地址时，应生成本地测试地址而不是调用 Core API。

**Acceptance Criteria:**
- [x] 添加 `isTestnetCurrency` 函数检测测试网络货币
- [x] 添加 `generateTestnetAddress` 函数生成测试地址
- [x] `AddAddress` 函数对测试网络使用本地生成，主网使用 Core API
- [x] 测试覆盖：USDT_TRON_TESTNET 和 USDC_TRON_TESTNET

### US-002: 测试地址格式正确
**Description:** 生成的测试地址应符合对应网络的地址格式规范。

**Acceptance Criteria:**
- [x] TRON Testnet 地址以 T 开头，共 34 位字母数字
- [x] 地址基于 userID 确定性生成，相同用户相同货币生成相同地址

### US-003: 保持主网逻辑不变
**Description:** 主网货币（USDT_TRC20、USDT_ERC20 等）仍然通过 Core API 获取地址。

**Acceptance Criteria:**
- [x] 主网货币调用 Core API GetAddress 接口
- [x] Core API 返回失败时返回错误（原有行为）
- [x] 现有测试全部通过

## Functional Requirements

- FR-1: 添加 `isTestnetCurrency(currency string) bool` 函数，检测货币是否包含 TESTNET/SHASTA/NILE/GOERLI/SEPOLIA/MUMBAI
- FR-2: 添加 `generateTestnetAddress(currency string, userID int) string` 函数，根据网络类型生成对应格式的测试地址
- FR-3: 修改 `AddAddress` 函数逻辑：先检查是否为测试网络，是则本地生成地址，否则调用 Core API
- FR-4: 测试地址的 AddressType 设置为 "TESTNET"，DerivePath 设置为测试网路径

## Non-Goals

- 不添加新的测试网络支持（Sepolia、Goerli 等）
- 不修改 currency 包
- 不添加 EVM 测试网络的本地地址生成
- 不支持测试网络的充值/提现功能

## Technical Considerations

- 测试地址仅用于显示目的，不能用于实际交易
- 确定性生成保证同一用户同一货币获得相同地址
- 使用用户 ID 作为地址生成种子，保证可重现性

## Success Metrics

- 用户添加 USDT + TRX(SHASTA) 地址成功率 100%
- 用户添加 USDC + TRX(SHASTA) 地址成功率 100%
- 主网地址添加不受影响

## Open Questions

- 是否需要为其他测试网络（如 NILE）添加支持？

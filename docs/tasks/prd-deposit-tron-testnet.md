# PRD: 存款页面添加 TRON Testnet 网络选项

## Introduction

在存款页面（/dashboard/deposit）添加 TRX (SHASTA) - TRON Testnet 网络选项，允许用户使用测试网络进行存款操作。后端已支持 TRX(SHASTA)_TRON_TESTNET，前端需要添加 UI 选项和处理逻辑。

## Goals

- 用户可以选择 TRX (SHASTA) - TRON Testnet 网络进行存款
- 支持 USDT 和 USDC 两种代币的测试网络存款
- 显示测试网络警告提示，防止用户误操作
- 保持主网存款功能不受影响

## User Stories

### US-001: 添加测试网络选项到下拉菜单
**Description:** 作为用户，我想在存款页面看到 TRX (SHASTA) - TRON Testnet 选项。

**Acceptance Criteria:**
- [ ] networkOptions 数组添加 TRON_TESTNET 选项
- [ ] 下拉菜单显示 "TRX (SHASTA) - TRON Testnet"
- [ ] 选择后触发正确的 currencyKey 构建逻辑

### US-002: 构建正确的测试网络 currencyKey
**Description:** 作为系统，我需要为测试网络构建正确的 currencyKey 以便后端处理。

**Acceptance Criteria:**
- [ ] 添加 getNetworkCode 函数处理网络到 currency 后缀的映射
- [ ] TRON_TESTNET -> TRX(SHASTA)_TRON_TESTNET
- [ ] 测试网络 currencyKey 格式: USDT_TRX(SHASTA)_TRON_TESTNET

### US-003: 显示测试网络警告
**Description:** 作为用户，我想在使用测试网络时看到警告提示，防止误操作。

**Acceptance Criteria:**
- [ ] 选择测试网络时显示警告提示
- [ ] 警告内容：这是测试网络，仅用于测试
- [ ] 警告使用醒目的样式（黄色/橙色）

### US-004: 测试存款地址获取
**Description:** 作为用户，我想在选择测试网络后能获取到存款地址。

**Acceptance Criteria:**
- [ ] 调用 /api/wallet/address/get 获取地址
- [ ] 后端返回本地生成的测试地址
- [ ] 地址正确显示在页面上

## Functional Requirements

- FR-1: 在 Deposit.tsx 的 networkOptions 添加 `{ value: "TRON_TESTNET", label: "TRX (SHASTA) - TRON Testnet", name: "TRON Testnet" }`
- FR-2: 添加 getNetworkCode(network: string) 函数，映射网络值到 currency 后缀
- FR-3: 使用 getNetworkCode 构建 currencyKey，替换原来的三元表达式
- FR-4: 添加测试网络警告组件，选择测试网络时显示
- FR-5: 后端已支持，无需修改

## Non-Goals

- 不添加其他测试网络（Sepolia、Goerli 等）
- 不修改提现页面
- 不支持测试网络充值到账（仅显示地址）
- 不添加测试网络充值历史

## Technical Considerations

- 重用现有的警告组件样式
- 测试网络地址由后端 generateTestnetAddress 生成
- 前端只需正确传递 currency 参数

## Success Metrics

- 用户可以选择 TRX (SHASTA) - TRON Testnet 并获取地址
- USDT 和 USDC 两种代币都能正常工作
- 测试网络警告正确显示

## Open Questions

- 是否需要在账户开设页面也添加相同逻辑？（已单独处理）

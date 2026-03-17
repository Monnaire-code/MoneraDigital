import { pgTable, index, unique, bigserial, bigint, numeric, varchar, timestamp, integer, boolean, uniqueIndex, text, foreignKey, jsonb, serial, smallint, date, check, pgView, pgEnum } from "drizzle-orm/pg-core"
import { sql } from "drizzle-orm"

export const addressType = pgEnum("address_type", ['BTC', 'ETH', 'USDC', 'USDT'])
export const depositStatus = pgEnum("deposit_status", ['PENDING', 'CONFIRMED', 'FAILED'])
export const lendingStatus = pgEnum("lending_status", ['ACTIVE', 'COMPLETED', 'TERMINATED'])
export const walletCreationStatus = pgEnum("wallet_creation_status", ['CREATING', 'SUCCESS', 'FAILED'])
export const withdrawalStatus = pgEnum("withdrawal_status", ['PENDING', 'PROCESSING', 'COMPLETED', 'FAILED'])


export const withdrawalOrder = pgTable("withdrawal_order", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale:  30 }).notNull(),
	networkFee: numeric("network_fee", { precision: 65, scale:  30 }),
	platformFee: numeric("platform_fee", { precision: 65, scale:  30 }),
	actualAmount: numeric("actual_amount", { precision: 65, scale:  30 }),
	chainType: varchar("chain_type", { length: 32 }).notNull(),
	coinType: varchar("coin_type", { length: 32 }).notNull(),
	toAddress: varchar("to_address", { length: 255 }).notNull(),
	safeheronOrderId: varchar("safeheron_order_id", { length: 64 }),
	transactionHash: varchar("transaction_hash", { length: 255 }),
	status: varchar({ length: 32 }).default('PENDING'),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	sentAt: timestamp("sent_at", { withTimezone: true, mode: 'string' }),
	confirmedAt: timestamp("confirmed_at", { withTimezone: true, mode: 'string' }),
	completedAt: timestamp("completed_at", { withTimezone: true, mode: 'string' }),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_withdrawal_tx_hash").using("btree", table.transactionHash.asc().nullsLast().op("text_ops")),
	index("idx_withdrawal_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	unique("uk_safeheron_order").on(table.safeheronOrderId),
]);

export const walletCreationRequest = pgTable("wallet_creation_request", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	requestId: varchar("request_id", { length: 128 }).notNull(),
	status: varchar({ length: 32 }).default('PENDING').notNull(),
	safeheronWalletId: varchar("safeheron_wallet_id", { length: 128 }),
	coinAddress: varchar("coin_address", { length: 256 }),
	errorMessage: varchar("error_message", { length: 255 }),
	retryCount: integer("retry_count").default(0).notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	unique("wallet_creation_request_user_id_key").on(table.userId),
	unique("wallet_creation_request_request_id_key").on(table.requestId),
]);

export const transferRecord = pgTable("transfer_record", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	transferId: varchar("transfer_id", { length: 64 }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	fromAccountId: bigint("from_account_id", { mode: "number" }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	toAccountId: bigint("to_account_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale:  30 }).notNull(),
	status: varchar({ length: 32 }).default('PENDING').notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	completedAt: timestamp("completed_at", { withTimezone: true, mode: 'string' }),
}, (table) => [
	index("idx_transfer_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	unique("transfer_record_transfer_id_key").on(table.transferId),
]);

export const withdrawalAddressWhitelist = pgTable("withdrawal_address_whitelist", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	addressAlias: varchar("address_alias", { length: 255 }).notNull(),
	chainType: varchar("chain_type", { length: 32 }).notNull(),
	walletAddress: varchar("wallet_address", { length: 255 }).notNull(),
	verified: boolean().default(false).notNull(),
	verifiedAt: timestamp("verified_at", { withTimezone: true, mode: 'string' }),
	verificationMethod: varchar("verification_method", { length: 32 }),
	isDeleted: boolean("is_deleted").default(false).notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_whitelist_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	unique("uk_user_address").on(table.userId, table.walletAddress),
]);

export const withdrawalRequest = pgTable("withdrawal_request", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	requestId: varchar("request_id", { length: 64 }).notNull(),
	status: varchar({ length: 32 }).default('PROCESSING').notNull(),
	errorCode: varchar("error_code", { length: 64 }),
	errorMessage: varchar("error_message", { length: 255 }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_request_request_id").using("btree", table.requestId.asc().nullsLast().op("text_ops")),
	unique("withdrawal_request_request_id_key").on(table.requestId),
]);

export const withdrawalFreezeLog = pgTable("withdrawal_freeze_log", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale:  30 }).notNull(),
	frozenAt: timestamp("frozen_at", { withTimezone: true, mode: 'string' }).notNull(),
	releasedAt: timestamp("released_at", { withTimezone: true, mode: 'string' }),
	reason: varchar({ length: 64 }).notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_freeze_log_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	index("idx_log_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
]);

export const userBalance = pgTable("UserBalance", {
	id: text().primaryKey().notNull(),
	userId: text().notNull(),
	totalAmount: numeric({ precision: 18, scale:  2 }).notNull(),
	frozenAmount: numeric({ precision: 18, scale:  2 }).default('0').notNull(),
	updatedAt: timestamp({ precision: 3, mode: 'string' }).notNull(),
}, (table) => [
	uniqueIndex("UserBalance_userId_key").using("btree", table.userId.asc().nullsLast().op("text_ops")),
]);

export const transactionLog = pgTable("TransactionLog", {
	id: text().primaryKey().notNull(),
	userId: text().notNull(),
	type: text().notNull(),
	amount: numeric({ precision: 18, scale:  2 }).notNull(),
	createdAt: timestamp({ precision: 3, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const fixedIncomeProduct = pgTable("FixedIncomeProduct", {
	id: text().primaryKey().notNull(),
	name: text().notNull(),
	yieldRate: numeric({ precision: 10, scale:  4 }).notNull(),
	termDays: integer().notNull(),
	minAmount: numeric({ precision: 18, scale:  2 }).notNull(),
	status: text().default('ACTIVE').notNull(),
	createdAt: timestamp({ precision: 3, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp({ precision: 3, mode: 'string' }).notNull(),
});

export const userProfitStats = pgTable("UserProfitStats", {
	id: text().primaryKey().notNull(),
	userId: text().notNull(),
	totalProfit: numeric({ precision: 18, scale:  2 }).notNull(),
	lastUpdate: timestamp({ precision: 3, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	uniqueIndex("UserProfitStats_userId_key").using("btree", table.userId.asc().nullsLast().op("text_ops")),
]);

export const subscriptionOrder = pgTable("SubscriptionOrder", {
	id: text().primaryKey().notNull(),
	userId: text().notNull(),
	productId: text().notNull(),
	amount: numeric({ precision: 18, scale:  2 }).notNull(),
	status: text().default('PENDING').notNull(),
	createdAt: timestamp({ precision: 3, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp({ precision: 3, mode: 'string' }).notNull(),
}, (table) => [
	foreignKey({
			columns: [table.productId],
			foreignColumns: [fixedIncomeProduct.id],
			name: "SubscriptionOrder_productId_fkey"
		}).onUpdate("cascade").onDelete("restrict"),
]);

export const businessStats = pgTable("BusinessStats", {
	id: text().primaryKey().notNull(),
	date: timestamp({ precision: 3, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	totalAmount: numeric({ precision: 18, scale:  2 }).notNull(),
	totalOrders: integer().notNull(),
	activeUsers: integer().notNull(),
});

export const wealthProductApproval = pgTable("wealth_product_approval", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	productId: bigint("product_id", { mode: "number" }).notNull(),
	currentStep: varchar("current_step", { length: 32 }).default('CREATED'),
	financeReviewedBy: varchar("finance_reviewed_by", { length: 64 }),
	financeReviewAt: timestamp("finance_review_at", { withTimezone: true, mode: 'string' }),
	financeApproved: boolean("finance_approved"),
	financeComment: text("finance_comment"),
	riskReviewedBy: varchar("risk_reviewed_by", { length: 64 }),
	riskReviewAt: timestamp("risk_review_at", { withTimezone: true, mode: 'string' }),
	riskApproved: boolean("risk_approved"),
	riskComment: text("risk_comment"),
	adminApprovedBy: varchar("admin_approved_by", { length: 64 }),
	adminApproveAt: timestamp("admin_approve_at", { withTimezone: true, mode: 'string' }),
	adminApproved: boolean("admin_approved"),
	adminComment: text("admin_comment"),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	unique("wealth_product_approval_product_id_key").on(table.productId),
]);

export const accountAdjustment = pgTable("account_adjustment", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	accountId: bigint("account_id", { mode: "number" }).notNull(),
	adjustmentAmount: numeric("adjustment_amount", { precision: 65, scale:  30 }).notNull(),
	reason: varchar({ length: 255 }).notNull(),
	requestedBy: varchar("requested_by", { length: 64 }),
	requestedAt: timestamp("requested_at", { withTimezone: true, mode: 'string' }),
	reviewedBy: varchar("reviewed_by", { length: 64 }),
	reviewedAt: timestamp("reviewed_at", { withTimezone: true, mode: 'string' }),
	approvedBy: varchar("approved_by", { length: 64 }),
	approvedAt: timestamp("approved_at", { withTimezone: true, mode: 'string' }),
	status: varchar({ length: 32 }).default('PENDING'),
	executionBy: varchar("execution_by", { length: 64 }),
	executedAt: timestamp("executed_at", { withTimezone: true, mode: 'string' }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const auditTrail = pgTable("audit_trail", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	operatorId: varchar("operator_id", { length: 64 }).notNull(),
	operatorRole: varchar("operator_role", { length: 32 }).notNull(),
	action: varchar({ length: 64 }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	targetId: bigint("target_id", { mode: "number" }),
	targetType: varchar("target_type", { length: 32 }),
	oldValue: jsonb("old_value"),
	newValue: jsonb("new_value"),
	reason: text(),
	ipAddress: varchar("ip_address", { length: 45 }),
	status: varchar({ length: 32 }),
	errorMessage: varchar("error_message", { length: 255 }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_audit_action").using("btree", table.action.asc().nullsLast().op("text_ops"), table.createdAt.asc().nullsLast().op("timestamptz_ops")),
	index("idx_audit_operator").using("btree", table.operatorId.asc().nullsLast().op("timestamptz_ops"), table.createdAt.asc().nullsLast().op("text_ops")),
]);

export const reconciliationLog = pgTable("reconciliation_log", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	checkTime: timestamp("check_time", { withTimezone: true, mode: 'string' }).notNull(),
	type: varchar({ length: 32 }).notNull(),
	userTotal: numeric("user_total", { precision: 65, scale:  30 }),
	systemBalance: numeric("system_balance", { precision: 65, scale:  30 }),
	difference: numeric({ precision: 65, scale:  30 }),
	status: varchar({ length: 32 }).default('SUCCESS'),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const reconciliationAlertLog = pgTable("reconciliation_alert_log", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	alertTime: timestamp("alert_time", { withTimezone: true, mode: 'string' }).notNull(),
	type: varchar({ length: 32 }).notNull(),
	description: text().notNull(),
	userTotal: numeric("user_total", { precision: 65, scale:  30 }),
	systemBalance: numeric("system_balance", { precision: 65, scale:  30 }),
	difference: numeric({ precision: 65, scale:  30 }),
	status: varchar({ length: 32 }).default('CRITICAL'),
	resolvedAt: timestamp("resolved_at", { withTimezone: true, mode: 'string' }),
	resolutionNotes: text("resolution_notes"),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const adminUsers = pgTable("admin_users", {
	id: serial().primaryKey().notNull(),
	username: varchar({ length: 50 }).notNull(),
	passwordHash: text("password_hash").notNull(),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow(),
}, (table) => [
	unique("admin_users_username_key").on(table.username),
]);

export const account = pgTable("account", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	type: varchar({ length: 16 }).notNull(),
	currency: varchar({ length: 8 }).notNull(),
	balance: numeric({ precision: 65, scale: 8 }).default('0').notNull(),      // 65,7 → 65,8
	frozenBalance: numeric("frozen_balance", { precision: 65, scale: 8 }).default('0').notNull(), // 65,7 → 65,8
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	version: bigint({ mode: "number" }).default(1).notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_account_frozen_balance").using("btree", table.userId.asc().nullsLast().op("int8_ops"), table.frozenBalance.asc().nullsLast().op("numeric_ops")),
	index("idx_account_type").using("btree", table.type.asc().nullsLast().op("text_ops")),
	index("idx_account_updated_at").using("btree", table.updatedAt.asc().nullsLast().op("timestamptz_ops")),
	index("idx_account_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	unique("uk_user_type_currency").on(table.userId, table.type, table.currency),
]);

export const accountJournal = pgTable("account_journal", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	serialNo: varchar("serial_no", { length: 64 }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	accountId: bigint("account_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale: 8 }).notNull(),      // 65,7 → 65,8
	balanceSnapshot: numeric("balance_snapshot", { precision: 65, scale: 8 }).notNull(), // 65,7 → 65,8
	bizType: varchar("biz_type", { length: 32 }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	refId: bigint("ref_id", { mode: "number" }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_journal_account_id").using("btree", table.accountId.asc().nullsLast().op("int8_ops")),
	index("idx_journal_biz_type").using("btree", table.bizType.asc().nullsLast().op("text_ops")),
	index("idx_journal_created_at").using("btree", table.createdAt.asc().nullsLast().op("timestamptz_ops")),
	index("idx_journal_ref_id").using("btree", table.refId.asc().nullsLast().op("int8_ops")),
	index("idx_journal_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
	unique("account_journal_serial_no_key").on(table.serialNo),
]);

export const wealthProduct = pgTable("wealth_product", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	title: varchar({ length: 128 }).notNull(),
	currency: varchar({ length: 8 }).notNull(),
	apy: numeric({ precision: 10, scale:  4 }).notNull(),
	duration: integer().notNull(),
	minAmount: numeric("min_amount", { precision: 65, scale:  30 }).notNull(),
	maxAmount: numeric("max_amount", { precision: 65, scale:  30 }).notNull(),
	totalQuota: numeric("total_quota", { precision: 65, scale:  30 }).notNull(),
	soldQuota: numeric("sold_quota", { precision: 65, scale:  30 }).default('0').notNull(),
	status: smallint().default(1).notNull(),
	autoRenewAllowed: boolean("auto_renew_allowed").default(true).notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_product_currency").using("btree", table.currency.asc().nullsLast().op("text_ops")),
	index("idx_product_status").using("btree", table.status.asc().nullsLast().op("int2_ops")),
]);

export const wealthOrder = pgTable("wealth_order", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	productId: bigint("product_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale:  30 }).notNull(),
	principalRedeemed: numeric("principal_redeemed", { precision: 65, scale:  30 }).default('0').notNull(),
	interestExpected: numeric("interest_expected", { precision: 65, scale:  30 }).default('0').notNull(),
	interestPaid: numeric("interest_paid", { precision: 65, scale:  30 }).default('0').notNull(),
	interestAccrued: numeric("interest_accrued", { precision: 65, scale:  30 }).default('0').notNull(),
	startDate: date("start_date").notNull(),
	endDate: date("end_date").notNull(),
	lastInterestDate: date("last_interest_date"),
	autoRenew: boolean("auto_renew").default(false).notNull(),
	status: smallint().default(0).notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	renewedFromOrderId: bigint("renewed_from_order_id", { mode: "number" }),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	renewedToOrderId: bigint("renewed_to_order_id", { mode: "number" }),
	redeemedAt: timestamp("redeemed_at", { withTimezone: true, mode: 'string' }),
	redemptionAmount: numeric("redemption_amount", { precision: 65, scale:  30 }),
	redemptionType: varchar("redemption_type", { length: 16 }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_order_end_date").using("btree", table.endDate.asc().nullsLast().op("date_ops")),
	index("idx_order_product_id").using("btree", table.productId.asc().nullsLast().op("int8_ops")),
	index("idx_order_renewed_from").using("btree", table.renewedFromOrderId.asc().nullsLast().op("int8_ops")),
	index("idx_order_status").using("btree", table.status.asc().nullsLast().op("int2_ops")),
	index("idx_order_user_id").using("btree", table.userId.asc().nullsLast().op("int8_ops")),
]);

export const wealthInterestRecord = pgTable("wealth_interest_record", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	orderId: bigint("order_id", { mode: "number" }).notNull(),
	amount: numeric({ precision: 65, scale:  30 }).notNull(),
	type: smallint().notNull(),
	date: date().notNull(),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
}, (table) => [
	index("idx_interest_date").using("btree", table.date.asc().nullsLast().op("date_ops")),
	index("idx_interest_order_id").using("btree", table.orderId.asc().nullsLast().op("int8_ops")),
]);

export const idempotencyRecord = pgTable("idempotency_record", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }).notNull(),
	requestId: varchar("request_id", { length: 128 }).notNull(),
	bizType: varchar("biz_type", { length: 32 }).notNull(),
	status: varchar({ length: 32 }).default('PROCESSING').notNull(),
	resultData: jsonb("result_data"),
	errorMessage: varchar("error_message", { length: 255 }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
	completedAt: timestamp("completed_at", { withTimezone: true, mode: 'string' }),
	ttlExpireAt: timestamp("ttl_expire_at", { withTimezone: true, mode: 'string' }).notNull(),
}, (table) => [
	index("idx_idempotency_request_id").using("btree", table.requestId.asc().nullsLast().op("text_ops")),
	index("idx_idempotency_ttl").using("btree", table.ttlExpireAt.asc().nullsLast().op("timestamptz_ops")),
	unique("uk_idempotency").on(table.userId, table.requestId, table.bizType),
]);

export const reconciliationErrorLog = pgTable("reconciliation_error_log", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	accountId: bigint("account_id", { mode: "number" }).notNull(),
	expectedBalance: numeric("expected_balance", { precision: 65, scale:  30 }),
	actualBalance: numeric("actual_balance", { precision: 65, scale:  30 }),
	errorType: varchar("error_type", { length: 32 }).notNull(),
	description: text(),
	resolved: boolean().default(false),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const manualReviewQueue = pgTable("manual_review_queue", {
	id: bigserial({ mode: "bigint" }).primaryKey().notNull(),
	type: varchar({ length: 32 }).notNull(),
	description: text().notNull(),
	severity: varchar({ length: 32 }).default('WARNING'),
	reviewedBy: varchar("reviewed_by", { length: 64 }),
	reviewResult: text("review_result"),
	reviewedAt: timestamp("reviewed_at", { withTimezone: true, mode: 'string' }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }).default(sql`CURRENT_TIMESTAMP`).notNull(),
});

export const businessFreezeStatus = pgTable("business_freeze_status", {
	id: integer().default(1).primaryKey().notNull(),
	isFrozen: boolean("is_frozen").default(false).notNull(),
	freezeReason: text("freeze_reason"),
	frozenAt: timestamp("frozen_at", { withTimezone: true, mode: 'string' }),
	unfrozenAt: timestamp("unfrozen_at", { withTimezone: true, mode: 'string' }),
}, (table) => [
	check("chk_id", sql`id = 1`),
]);

export const users = pgTable("users", {
	id: serial().primaryKey().notNull(),
	email: text().notNull(),
	password: text().notNull(),
	twoFactorSecret: text("two_factor_secret"),
	twoFactorEnabled: boolean("two_factor_enabled").default(false).notNull(),
	twoFactorBackupCodes: text("two_factor_backup_codes"),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow().notNull(),
}, (table) => [
	index("idx_users_email").using("btree", table.email.asc().nullsLast().op("text_ops")),
	unique("users_email_unique").on(table.email),
]);

export const lendingPositions = pgTable("lending_positions", {
	id: serial().primaryKey().notNull(),
	userId: integer("user_id").notNull(),
	asset: text().notNull(),
	amount: numeric({ precision: 20, scale:  8 }).notNull(),
	durationDays: integer("duration_days").notNull(),
	apy: numeric({ precision: 5, scale:  2 }).notNull(),
	status: lendingStatus().default('ACTIVE').notNull(),
	accruedYield: numeric("accrued_yield", { precision: 20, scale:  8 }).default('0').notNull(),
	startDate: timestamp("start_date", { mode: 'string' }).defaultNow().notNull(),
	endDate: timestamp("end_date", { mode: 'string' }).notNull(),
}, (table) => [
	index("idx_lending_positions_asset").using("btree", table.asset.asc().nullsLast().op("text_ops")),
	index("idx_lending_positions_status").using("btree", table.status.asc().nullsLast().op("enum_ops")),
	index("idx_lending_positions_user_id").using("btree", table.userId.asc().nullsLast().op("int4_ops")),
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "lending_positions_user_id_users_id_fk"
		}),
]);

export const withdrawalAddresses = pgTable("withdrawal_addresses", {
	id: serial().primaryKey().notNull(),
	userId: integer("user_id").notNull(),
	address: text().notNull(),
	addressType: addressType("address_type").notNull(),
	label: text().notNull(),
	isVerified: boolean("is_verified").default(false).notNull(),
	isPrimary: boolean("is_primary").default(false).notNull(),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow().notNull(),
	verifiedAt: timestamp("verified_at", { mode: 'string' }),
	deactivatedAt: timestamp("deactivated_at", { mode: 'string' }),
}, (table) => [
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "withdrawal_addresses_user_id_users_id_fk"
		}),
]);

export const addressVerifications = pgTable("address_verifications", {
	id: serial().primaryKey().notNull(),
	addressId: integer("address_id").notNull(),
	token: text().notNull(),
	expiresAt: timestamp("expires_at", { mode: 'string' }).notNull(),
	verifiedAt: timestamp("verified_at", { mode: 'string' }),
}, (table) => [
	foreignKey({
			columns: [table.addressId],
			foreignColumns: [withdrawalAddresses.id],
			name: "address_verifications_address_id_withdrawal_addresses_id_fk"
		}),
	unique("address_verifications_token_unique").on(table.token),
]);

export const withdrawals = pgTable("withdrawals", {
	id: serial().primaryKey().notNull(),
	userId: integer("user_id").notNull(),
	fromAddressId: integer("from_address_id").notNull(),
	amount: numeric({ precision: 65, scale: 8 }).notNull(),     // 65,7 → 65,8
	asset: text().notNull(),
	toAddress: text("to_address").notNull(),
	status: withdrawalStatus().default('PENDING').notNull(),
	txHash: text("tx_hash"),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow().notNull(),
	completedAt: timestamp("completed_at", { mode: 'string' }),
	failureReason: text("failure_reason"),
	feeAmount: numeric("fee_amount", { precision: 65, scale: 8 }),      // 65,7 → 65,8
	receivedAmount: numeric("received_amount", { precision: 65, scale: 8 }), // 65,7 → 65,8
	safeheronTxId: text("safeheron_tx_id"),
	chain: text(),
}, (table) => [
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "withdrawals_user_id_users_id_fk"
		}),
	foreignKey({
			columns: [table.fromAddressId],
			foreignColumns: [withdrawalAddresses.id],
			name: "withdrawals_from_address_id_withdrawal_addresses_id_fk"
		}),
]);

export const deposits = pgTable("deposits", {
	id: serial().primaryKey().notNull(),
	userId: integer("user_id").notNull(),
	txHash: text("tx_hash").notNull(),
	amount: numeric({ precision: 65, scale: 8 }).notNull(),  // 20,8 → 65,8
	asset: text().notNull(),
	chain: text().notNull(),
	status: depositStatus().default('PENDING').notNull(),
	fromAddress: text("from_address"),
	toAddress: text("to_address"),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow().notNull(),
	confirmedAt: timestamp("confirmed_at", { mode: 'string' }),
}, (table) => [
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "deposits_user_id_users_id_fk"
		}),
	unique("deposits_tx_hash_unique").on(table.txHash),
]);

export const walletCreationRequests = pgTable("wallet_creation_requests", {
	id: serial().primaryKey().notNull(),
	requestId: text("request_id").notNull(),
	userId: integer("user_id").notNull(),
	status: walletCreationStatus().default('CREATING').notNull(),
	walletId: text("wallet_id"),
	address: text(),
	addresses: text(),
	errorMessage: text("error_message"),
	createdAt: timestamp("created_at", { mode: 'string' }).defaultNow().notNull(),
	updatedAt: timestamp("updated_at", { mode: 'string' }).defaultNow().notNull(),
}, (table) => [
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "wallet_creation_requests_user_id_users_id_fk"
		}),
	unique("wallet_creation_requests_request_id_unique").on(table.requestId),
]);

export const migrations = pgTable("migrations", {
	id: serial().primaryKey().notNull(),
	version: varchar({ length: 50 }).notNull(),
	name: varchar({ length: 255 }).notNull(),
	executedAt: timestamp("executed_at", { mode: 'string' }).default(sql`CURRENT_TIMESTAMP`),
}, (table) => [
	unique("migrations_version_key").on(table.version),
]);

export const withdrawalVerification = pgTable("withdrawal_verification", {
	id: serial().primaryKey().notNull(),
	userId: integer("user_id").notNull(),
	withdrawalOrderId: integer("withdrawal_order_id").notNull(),
	verificationMethod: varchar("verification_method", { length: 32 }).notNull(),
	verificationCode: varchar("verification_code", { length: 255 }),
	attempts: integer().default(0),
	maxAttempts: integer("max_attempts").default(3),
	verified: boolean().default(false),
	verifiedAt: timestamp("verified_at", { mode: 'string' }),
	expiresAt: timestamp("expires_at", { mode: 'string' }),
	createdAt: timestamp("created_at", { mode: 'string' }).default(sql`CURRENT_TIMESTAMP`),
}, (table) => [
	index("idx_verification_user_id").using("btree", table.userId.asc().nullsLast().op("int4_ops")),
	foreignKey({
			columns: [table.userId],
			foreignColumns: [users.id],
			name: "withdrawal_verification_user_id_fkey"
		}),
]);
export const vAccountAvailable = pgView("v_account_available", {	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	id: bigint({ mode: "number" }),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	userId: bigint("user_id", { mode: "number" }),
	type: varchar({ length: 16 }),
	currency: varchar({ length: 8 }),
	balance: numeric({ precision: 65, scale:  7 }),
	frozenBalance: numeric("frozen_balance", { precision: 65, scale:  7 }),
	availableBalance: numeric("available_balance"),
	// You can use { mode: "bigint" } if numbers are exceeding js number limitations
	version: bigint({ mode: "number" }),
	createdAt: timestamp("created_at", { withTimezone: true, mode: 'string' }),
	updatedAt: timestamp("updated_at", { withTimezone: true, mode: 'string' }),
}).as(sql`SELECT id, user_id, type, currency, balance, frozen_balance, balance - frozen_balance AS available_balance, version, created_at, updated_at FROM account WHERE user_id > 0`);
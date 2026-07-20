package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"monera-digital/internal/db"
	"monera-digital/internal/repository"
	"monera-digital/internal/repository/postgres"

	"github.com/joho/godotenv"
)

type TestScenario struct {
	Name           string
	UserID         int64
	ProductID      int64
	Amount         string
	AutoRenew      bool
	ExpectedResult string
}

type TestReport struct {
	Scenarios   []ScenarioResult
	TotalTests  int
	PassedTests int
	FailedTests int
	StartTime   time.Time
	EndTime     time.Time
}

type ScenarioResult struct {
	ScenarioName string
	UserID       int64
	ProductID    int64
	Amount       string
	AutoRenew    bool
	Steps        []StepResult
	Status       string
	ErrorMessage string
	Duration     time.Duration
}

type StepResult struct {
	StepName string
	Status   string
	Details  string
	Duration time.Duration
}

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	report := &TestReport{
		StartTime: time.Now(),
		Scenarios: []ScenarioResult{},
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required (e.g., postgresql://user:pass@host:5432/db?sslmode=require)")
	}

	database, err := db.InitDB(databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	wealthRepo := postgres.NewWealthRepository(database)
	accountRepo := postgres.NewAccountRepository(database)

	log.Println("========================================")
	log.Println("MoneraDigital 定期理财产品 - 完整流程测试")
	log.Println("========================================")
	log.Println()

	scenarios := []TestScenario{
		{
			Name:           "场景1: 正常申购(自动续期开启)",
			UserID:         1001,
			ProductID:      1,
			Amount:         "5000",
			AutoRenew:      true,
			ExpectedResult: "成功",
		},
		{
			Name:           "场景2: 正常申购(自动续期关闭)",
			UserID:         1002,
			ProductID:      1,
			Amount:         "3000",
			AutoRenew:      false,
			ExpectedResult: "成功",
		},
		{
			Name:           "场景3: 最小金额申购",
			UserID:         1003,
			ProductID:      1,
			Amount:         "100",
			AutoRenew:      false,
			ExpectedResult: "成功",
		},
		{
			Name:           "场景4: 最大金额申购",
			UserID:         1004,
			ProductID:      1,
			Amount:         "10000",
			AutoRenew:      true,
			ExpectedResult: "成功",
		},
		{
			Name:           "场景5: 重复申购",
			UserID:         1001,
			ProductID:      1,
			Amount:         "2000",
			AutoRenew:      false,
			ExpectedResult: "成功",
		},
		{
			Name:           "场景6: 余额不足",
			UserID:         1005,
			ProductID:      1,
			Amount:         "99999999",
			AutoRenew:      false,
			ExpectedResult: "余额不足",
		},
		{
			Name:           "场景7: 不存在的产品",
			UserID:         1002,
			ProductID:      9999,
			Amount:         "1000",
			AutoRenew:      false,
			ExpectedResult: "产品不存在",
		},
	}

	for _, scenario := range scenarios {
		result := runScenario(wealthRepo, accountRepo, database, scenario)
		report.Scenarios = append(report.Scenarios, result)
		report.TotalTests++

		if result.Status == "通过" {
			report.PassedTests++
		} else {
			report.FailedTests++
		}

		printScenarioResult(result)
		log.Println()
	}

	report.EndTime = time.Now()

	printFinalReport(report)
}

func runScenario(wealthRepo *postgres.WealthRepository, accountRepo repository.AccountV2, database *sql.DB, scenario TestScenario) ScenarioResult {
	result := ScenarioResult{
		ScenarioName: scenario.Name,
		UserID:       scenario.UserID,
		ProductID:    scenario.ProductID,
		Amount:       scenario.Amount,
		AutoRenew:    scenario.AutoRenew,
		Steps:        []StepResult{},
		Status:       "通过",
	}

	ctx := context.Background()
	startTime := time.Now()

	step1Start := time.Now()
	account, err := accountRepo.GetAccountByUserIDAndCurrency(ctx, scenario.UserID, "USDT")
	step1Duration := time.Since(step1Start)

	if err == sql.ErrNoRows {
		accountID, _ := createTestAccount(ctx, database, scenario.UserID)
		account = &repository.AccountModel{
			ID:       accountID,
			UserID:   scenario.UserID,
			Currency: "USDT",
			Balance:  "100000",
		}
		accountRepo.AddBalance(ctx, account.ID, "100000")
		account, _ = accountRepo.GetAccountByUserIDAndCurrency(ctx, scenario.UserID, "USDT")
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "检查用户账户",
		Status:   "通过",
		Details:  fmt.Sprintf("用户ID: %d, 账户余额: %s USDT", scenario.UserID, account.Balance),
		Duration: step1Duration,
	})

	step2Start := time.Now()
	product, err := wealthRepo.GetProductByID(ctx, scenario.ProductID)
	step2Duration := time.Since(step2Start)

	if err != nil {
		result.Steps = append(result.Steps, StepResult{
			StepName: "获取产品信息",
			Status:   "失败",
			Details:  fmt.Sprintf("产品ID: %d, 错误: %v", scenario.ProductID, err),
			Duration: step2Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = "产品不存在"
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "获取产品信息",
		Status:   "通过",
		Details:  fmt.Sprintf("产品: %s, APY: %s%%, 期限: %d天", product.Title, product.APY, product.Duration),
		Duration: step2Duration,
	})

	step3Start := time.Now()
	amount, _ := strconv.ParseFloat(scenario.Amount, 64)
	minAmount, _ := strconv.ParseFloat(product.MinAmount, 64)
	maxAmount, _ := strconv.ParseFloat(product.MaxAmount, 64)
	step3Duration := time.Since(step3Start)

	if amount < minAmount {
		result.Steps = append(result.Steps, StepResult{
			StepName: "验证金额",
			Status:   "失败",
			Details:  fmt.Sprintf("申购金额: %s, 最低要求: %s", scenario.Amount, product.MinAmount),
			Duration: step3Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = "金额低于最小限额"
		result.Duration = time.Since(startTime)
		return result
	}

	if amount > maxAmount {
		result.Steps = append(result.Steps, StepResult{
			StepName: "验证金额",
			Status:   "失败",
			Details:  fmt.Sprintf("申购金额: %s, 最高限额: %s", scenario.Amount, product.MaxAmount),
			Duration: step3Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = "金额超过最大限额"
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "验证金额",
		Status:   "通过",
		Details:  fmt.Sprintf("金额 %s USDT 在允许范围内 [%s, %s]", scenario.Amount, product.MinAmount, product.MaxAmount),
		Duration: step3Duration,
	})

	step4Start := time.Now()
	balance, _ := strconv.ParseFloat(account.Balance, 64)
	step4Duration := time.Since(step4Start)

	if balance < amount {
		result.Steps = append(result.Steps, StepResult{
			StepName: "检查余额",
			Status:   "失败",
			Details:  fmt.Sprintf("可用余额: %s, 申购金额: %s", account.Balance, scenario.Amount),
			Duration: step4Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = "余额不足"
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "检查余额",
		Status:   "通过",
		Details:  fmt.Sprintf("余额充足: %s >= %s", account.Balance, scenario.Amount),
		Duration: step4Duration,
	})

	step5Start := time.Now()
	now := time.Now()
	startDate := now.Format("2006-01-02")
	endDate := now.AddDate(0, 0, product.Duration).Format("2006-01-02")

	order := &repository.WealthOrderModel{
		UserID:          scenario.UserID,
		ProductID:       scenario.ProductID,
		ProductTitle:    product.Title,
		Currency:        product.Currency,
		Amount:          scenario.Amount,
		InterestAccrued: "0",
		StartDate:       startDate,
		EndDate:         endDate,
		AutoRenew:       scenario.AutoRenew,
		Status:          1,
	}

	err = wealthRepo.CreateOrder(ctx, order)
	step5Duration := time.Since(step5Start)

	if err != nil {
		result.Steps = append(result.Steps, StepResult{
			StepName: "创建订单",
			Status:   "失败",
			Details:  fmt.Sprintf("错误: %v", err),
			Duration: step5Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = fmt.Sprintf("创建订单失败: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "创建订单",
		Status:   "通过",
		Details:  fmt.Sprintf("订单ID: %d, 期限: %s 至 %s, 自动续期: %v", order.ID, startDate, endDate, scenario.AutoRenew),
		Duration: step5Duration,
	})

	step6Start := time.Now()
	err = accountRepo.FreezeBalance(ctx, account.ID, scenario.Amount)
	step6Duration := time.Since(step6Start)

	if err != nil {
		result.Steps = append(result.Steps, StepResult{
			StepName: "冻结金额",
			Status:   "失败",
			Details:  fmt.Sprintf("错误: %v", err),
			Duration: step6Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = fmt.Sprintf("冻结金额失败: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "冻结金额",
		Status:   "通过",
		Details:  fmt.Sprintf("冻结 %s USDT", scenario.Amount),
		Duration: step6Duration,
	})

	step7Start := time.Now()
	err = wealthRepo.UpdateProductSoldQuota(ctx, scenario.ProductID, scenario.Amount)
	step7Duration := time.Since(step7Start)

	if err != nil {
		result.Steps = append(result.Steps, StepResult{
			StepName: "更新产品销量",
			Status:   "失败",
			Details:  fmt.Sprintf("错误: %v", err),
			Duration: step7Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = fmt.Sprintf("更新销量失败: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "更新产品销量",
		Status:   "通过",
		Details:  fmt.Sprintf("销量增加 %s", scenario.Amount),
		Duration: step7Duration,
	})

	step8Start := time.Now()
	createdOrder, err := wealthRepo.GetOrderByID(ctx, order.ID)
	step8Duration := time.Since(step8Start)

	if err != nil {
		result.Steps = append(result.Steps, StepResult{
			StepName: "验证订单数据",
			Status:   "失败",
			Details:  fmt.Sprintf("错误: %v", err),
			Duration: step8Duration,
		})
		result.Status = "失败"
		result.ErrorMessage = fmt.Sprintf("验证订单失败: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	if createdOrder.UserID != scenario.UserID ||
		createdOrder.Amount != scenario.Amount ||
		createdOrder.AutoRenew != scenario.AutoRenew {
		result.Status = "失败"
		result.ErrorMessage = "数据验证不通过"
	}

	result.Steps = append(result.Steps, StepResult{
		StepName: "验证订单数据",
		Status:   result.Status,
		Details: fmt.Sprintf("订单ID: %d, 用户: %d, 金额: %s, 自动续期: %v",
			createdOrder.ID, createdOrder.UserID, createdOrder.Amount, createdOrder.AutoRenew),
		Duration: step8Duration,
	})

	result.Duration = time.Since(startTime)
	return result
}

func createTestAccount(ctx context.Context, database *sql.DB, userID int64) (int64, error) {
	var accountID int64
	query := `INSERT INTO account (user_id, type, currency, balance, frozen_balance, version, created_at, updated_at) 
	          VALUES ($1, 'spot', 'USDT', '0', '0', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id`
	err := database.QueryRowContext(ctx, query, userID).Scan(&accountID)
	return accountID, err
}

func printScenarioResult(result ScenarioResult) {
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("场景: %s", result.ScenarioName)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("用户ID: %d | 产品ID: %d | 金额: %s USDT | 自动续期: %v",
		result.UserID, result.ProductID, result.Amount, result.AutoRenew)
	log.Println()

	for i, step := range result.Steps {
		statusIcon := "✅"
		if step.Status != "通过" {
			statusIcon = "❌"
		}
		log.Printf("  %s 步骤%d: %s", statusIcon, i+1, step.StepName)
		log.Printf("       状态: %s | 耗时: %v", step.Status, step.Duration)
		log.Printf("       详情: %s", step.Details)
		log.Println()
	}

	if result.Status == "通过" {
		log.Printf("✅ 结果: 通过 | 耗时: %v", result.Duration)
	} else {
		log.Printf("❌ 结果: 失败 | 错误: %s", result.ErrorMessage)
	}
}

func printFinalReport(report *TestReport) {
	log.Println()
	log.Println("╔══════════════════════════════════════════════════════════════╗")
	log.Println("║                    完 整 测 试 报 告                          ║")
	log.Println("╚══════════════════════════════════════════════════════════════╝")
	log.Println()
	log.Printf("测试时间: %s 至 %s", report.StartTime.Format("2006-01-02 15:04:05"), report.EndTime.Format("2006-01-02 15:04:05"))
	log.Printf("总耗时:   %v", report.EndTime.Sub(report.StartTime))
	log.Println()
	log.Println("──────────────────────────────────────────────────────────────")
	log.Println()
	log.Printf("总测试数:  %d", report.TotalTests)
	log.Printf("通过数量:  %d ✅", report.PassedTests)
	log.Printf("失败数量:  %d ❌", report.FailedTests)
	log.Printf("通过率:    %.2f%%", float64(report.PassedTests)/float64(report.TotalTests)*100)
	log.Println()

	log.Println("──────────────────────────────────────────────────────────────")
	log.Println("场景通过情况:")
	log.Println()

	for _, scenario := range report.Scenarios {
		statusIcon := "✅"
		if scenario.Status != "通过" {
			statusIcon = "❌"
		}
		log.Printf("  %s %s", statusIcon, scenario.ScenarioName)
	}
	log.Println()

	log.Println("──────────────────────────────────────────────────────────────")
	log.Println("详细结果:")
	log.Println()

	log.Printf("%-40s | %-8s | %-10s | %-8s", "场景名称", "状态", "金额", "自动续期")
	log.Println(strings.Repeat("-", 80))

	for _, scenario := range report.Scenarios {
		status := "通过"
		if scenario.Status != "通过" {
			status = "失败"
		}
		autoRenew := "是"
		if !scenario.AutoRenew {
			autoRenew = "否"
		}
		log.Printf("%-40s | %-8s | %-10s | %-8s",
			truncateString(scenario.ScenarioName, 40),
			status,
			scenario.Amount+" USDT",
			autoRenew)
	}
	log.Println()

	totalDuration := time.Duration(0)
	for _, scenario := range report.Scenarios {
		totalDuration += scenario.Duration
	}
	avgDuration := totalDuration / time.Duration(report.TotalTests)

	log.Println("──────────────────────────────────────────────────────────────")
	log.Println("性能统计:")
	log.Println()
	log.Printf("平均每个场景耗时: %v", avgDuration)
	log.Printf("总执行时间:      %v", totalDuration)
	log.Println()

	writeReportToFile(report)
}

func writeReportToFile(report *TestReport) {
	filename := fmt.Sprintf("test_report_%s.txt", time.Now().Format("20060102150405"))

	content := fmt.Sprintf(`================================================================================
                    MoneraDigital 定期理财产品 - 完整测试报告
================================================================================

测试时间: %s 至 %s
总耗时:   %v

--------------------------------------------------------------------------------
测试结果汇总
--------------------------------------------------------------------------------
总测试数:  %d
通过数量:  %d
失败数量:  %d
通过率:    %.2f%%

--------------------------------------------------------------------------------
场景详情
--------------------------------------------------------------------------------
`, report.StartTime.Format("2006-01-02 15:04:05"),
		report.EndTime.Format("2006-01-02 15:04:05"),
		report.EndTime.Sub(report.StartTime),
		report.TotalTests,
		report.PassedTests,
		report.FailedTests,
		float64(report.PassedTests)/float64(report.TotalTests)*100)

	for i, scenario := range report.Scenarios {
		content += fmt.Sprintf("\n[%d] %s\n", i+1, scenario.ScenarioName)
		content += fmt.Sprintf("    用户ID:    %d\n", scenario.UserID)
		content += fmt.Sprintf("    产品ID:    %d\n", scenario.ProductID)
		content += fmt.Sprintf("    申购金额:  %s USDT\n", scenario.Amount)
		content += fmt.Sprintf("    自动续期:  %v\n", scenario.AutoRenew)
		content += fmt.Sprintf("    状态:      %s\n", scenario.Status)
		if scenario.Status != "通过" {
			content += fmt.Sprintf("    错误信息:  %s\n", scenario.ErrorMessage)
		}
		content += fmt.Sprintf("    耗时:      %v\n", scenario.Duration)

		content += "\n    执行步骤:\n"
		for j, step := range scenario.Steps {
			content += fmt.Sprintf("      %d. %s [%s]\n", j+1, step.StepName, step.Status)
			content += fmt.Sprintf("         详情: %s\n", step.Details)
			content += fmt.Sprintf("         耗时: %v\n", step.Duration)
		}
		content += "\n"
	}

	content += fmt.Sprintf(`
--------------------------------------------------------------------------------
结论
--------------------------------------------------------------------------------
本次测试覆盖了 %d 个不同场景，包括:
  - 正常申购流程
  - 不同金额边界测试
  - 自动续期开关测试
  - 异常场景测试(余额不足、产品不存在)

================================================================================
                              报告生成时间: %s
================================================================================
`, report.TotalTests, time.Now().Format("2006-01-02 15:04:05"))

	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		log.Printf("警告: 无法写入报告文件: %v", err)
	} else {
		log.Printf("报告已保存至: %s", filename)
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

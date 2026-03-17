package utils

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestFromString(t *testing.T) {
	tests := []struct {
		input    string
		expected decimal.Decimal
		hasError bool
	}{
		{"100", decimal.NewFromFloat(100), false},
		{"100.12345678", decimal.NewFromFloat(100.12345678), false},
		{"", decimal.Zero, false},
		{"invalid", decimal.Zero, true},
	}

	for _, tt := range tests {
		result, err := FromString(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("FromString(%v) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("FromString(%v) unexpected error: %v", tt.input, err)
			}
			if !result.Equal(tt.expected) {
				t.Errorf("FromString(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasError bool
	}{
		{"100", "100", false},
		{"100.12345678", "100.12345678", false},
		{"100.123456789", "100.12345679", false},  // 四舍五入
		{"100.1234567891", "100.12345679", false}, // 超过8位截断
		{"", "0", false},
	}

	for _, tt := range tests {
		result, err := Normalize(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("Normalize(%v) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("Normalize(%v) unexpected error: %v", tt.input, err)
			}
			if result.String() != tt.expected {
				t.Errorf("Normalize(%v) = %v, want %v", tt.input, result.String(), tt.expected)
			}
		}
	}
}

func TestAdd(t *testing.T) {
	result, err := Add("100.00000000", "50.00000000")
	if err != nil {
		t.Errorf("Add error: %v", err)
	}
	if result != "150" && result != "150.00000000" {
		t.Errorf("Add = %v, want 150", result)
	}

	// 测试精度
	result2, _ := Add("0.00000001", "0.00000002")
	if result2 != "0.00000003" {
		t.Errorf("Add precision = %v, want 0.00000003", result2)
	}
}

func TestSub(t *testing.T) {
	result, err := Sub("100.00000000", "30.00000000")
	if err != nil {
		t.Errorf("Sub error: %v", err)
	}
	if result != "70" && result != "70.00000000" {
		t.Errorf("Sub = %v, want 70", result)
	}
}

func TestMul(t *testing.T) {
	result, err := Mul("100.00000000", 0.05)
	if err != nil {
		t.Errorf("Mul error: %v", err)
	}
	if result != "5" && result != "5.00000000" {
		t.Errorf("Mul = %v, want 5", result)
	}

	// 测试字符串乘数
	result2, _ := Mul("100", "0.05")
	if result2 != "5" {
		t.Errorf("Mul string = %v, want 5", result2)
	}
}

func TestDiv(t *testing.T) {
	result, err := Div("100", "4")
	if err != nil {
		t.Errorf("Div error: %v", err)
	}
	if result != "25" {
		t.Errorf("Div = %v, want 25", result)
	}

	// 测试除零
	_, err = Div("100", "0")
	if err == nil {
		t.Errorf("Div by zero should return error")
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected int
	}{
		{"100", "100", 0},
		{"100.00000001", "100", 1},
		{"99.99999999", "100", -1},
	}

	for _, tt := range tests {
		result, err := Compare(tt.a, tt.b)
		if err != nil {
			t.Errorf("Compare error: %v", err)
		}
		if result != tt.expected {
			t.Errorf("Compare(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestGT(t *testing.T) {
	result, _ := GT("100", "99")
	if !result {
		t.Error("GT(100, 99) should be true")
	}
	result, _ = GT("100", "100")
	if result {
		t.Error("GT(100, 100) should be false")
	}
}

func TestLT(t *testing.T) {
	result, _ := LT("99", "100")
	if !result {
		t.Error("LT(99, 100) should be true")
	}
	result, _ = LT("100", "100")
	if result {
		t.Error("LT(100, 100) should be false")
	}
}

func TestEqual(t *testing.T) {
	result, _ := Equal("100.00000000", "100")
	if !result {
		t.Error("Equal(100.00000000, 100) should be true")
	}
	result, _ = Equal("100", "99")
	if result {
		t.Error("Equal(100, 99) should be false")
	}
}

func TestCalculateInterest(t *testing.T) {
	// 测试: 10000 * 12% * 30天 / 365 = 98.63
	result, err := CalculateInterest("10000", "12", 30)
	if err != nil {
		t.Errorf("CalculateInterest error: %v", err)
	}
	// 10000 * 0.12 / 365 * 30 = 98.63013698... -> 98.63013698
	t.Log("Interest for 10000 at 12% for 30 days:", result)
}

func TestCalculateDailyInterest(t *testing.T) {
	// 测试: 10000 * 12% / 365 = 3.28
	result, err := CalculateDailyInterest("10000", "12")
	if err != nil {
		t.Errorf("CalculateDailyInterest error: %v", err)
	}
	t.Log("Daily interest for 10000 at 12%:", result)
}

func TestIsValidAmount(t *testing.T) {
	if !IsValidAmount("100") {
		t.Error("IsValidAmount(100) should be true")
	}
	if !IsValidAmount("0") {
		t.Error("IsValidAmount(0) should be true")
	}
	if IsValidAmount("-100") {
		t.Error("IsValidAmount(-100) should be false")
	}
	if IsValidAmount("invalid") {
		t.Error("IsValidAmount(invalid) should be false")
	}
}

func TestNeg(t *testing.T) {
	result, err := Neg("100")
	if err != nil {
		t.Errorf("Neg error: %v", err)
	}
	if result != "-100" {
		t.Errorf("Neg(100) = %v, want -100", result)
	}
}

func TestAbs(t *testing.T) {
	result, err := Abs("-100.5")
	if err != nil {
		t.Errorf("Abs error: %v", err)
	}
	if result != "100.5" {
		t.Errorf("Abs(-100.5) = %v, want 100.5", result)
	}
}

func TestSum(t *testing.T) {
	result, err := Sum("100", "200", "300")
	if err != nil {
		t.Errorf("Sum error: %v", err)
	}
	if result != "600" {
		t.Errorf("Sum = %v, want 600", result)
	}
}

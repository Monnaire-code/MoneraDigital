package utils

import (
	"errors"

	"github.com/shopspring/decimal"
)

const BalancePrecision = 8

var (
	ErrDivByZero     = errors.New("division by zero")
	ErrInvalidNumber = errors.New("invalid number")
)

// ============================================================
// 基础转换函数
// ============================================================

// FromString 将字符串转换为 Decimal
func FromString(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// FromFloat 将 float64 转换为 Decimal (注意: float 有精度损失)
func FromFloat(f float64) decimal.Decimal {
	return decimal.NewFromFloat(f)
}

// FromInt 将 int64 转换为 Decimal
func FromInt(i int64) decimal.Decimal {
	return decimal.NewFromInt(i)
}

// ToString 将 Decimal 转换为字符串
func ToString(d decimal.Decimal) string {
	return d.String()
}

// ToStringPrec 将 Decimal 转换为指定精度的字符串 (四舍五入)
func ToStringPrec(d decimal.Decimal, scale int32) string {
	return d.Round(scale).String()
}

// ToFloat 将 Decimal 转换为 float64 (注意: 有精度损失)
func ToFloat(d decimal.Decimal) float64 {
	return d.InexactFloat64()
}

// Normalize 将金额规范化为8位小数 (四舍五入)
func Normalize(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	return d.Round(BalancePrecision), nil
}

// NormalizeString 将金额规范化为8位小数的字符串
func NormalizeString(s string) (string, error) {
	d, err := Normalize(s)
	if err != nil {
		return "", err
	}
	return d.Round(BalancePrecision).String(), nil
}

// MustNormalize 规范化金额,失败时 panic
func MustNormalize(s string) decimal.Decimal {
	d, err := Normalize(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ============================================================
// 算术运算
// ============================================================

// Add 加法: a + b
func Add(a, b string) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return "", err
	}
	return aDec.Add(bDec).Round(BalancePrecision).String(), nil
}

// Sub 减法: a - b
func Sub(a, b string) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return "", err
	}
	return aDec.Sub(bDec).Round(BalancePrecision).String(), nil
}

// Mul 乘法: a * b
func Mul(a string, multiplier interface{}) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}

	switch m := multiplier.(type) {
	case float64:
		return aDec.Mul(decimal.NewFromFloat(m)).Round(BalancePrecision).String(), nil
	case int64:
		return aDec.Mul(decimal.NewFromInt(m)).Round(BalancePrecision).String(), nil
	case int:
		return aDec.Mul(decimal.NewFromInt32(int32(m))).Round(BalancePrecision).String(), nil
	case string:
		mDec, err := decimal.NewFromString(m)
		if err != nil {
			return "", err
		}
		return aDec.Mul(mDec).Round(BalancePrecision).String(), nil
	default:
		return "", ErrInvalidNumber
	}
}

// Div 除法: a / b (b 不能为0)
func Div(a, b string) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return "", err
	}
	if bDec.IsZero() {
		return "", ErrDivByZero
	}
	return aDec.Div(bDec).Round(BalancePrecision).String(), nil
}

// ============================================================
// 金额计算专用 (适用于利率、利息计算)
// ============================================================

// CalculateInterest 计算利息: 本金 * 年化利率 * (天数/365)
// interest = principal * apy * (days/365)
func CalculateInterest(principal, apy string, days int) (string, error) {
	p, err := decimal.NewFromString(principal)
	if err != nil {
		return "", err
	}
	r, err := decimal.NewFromString(apy)
	if err != nil {
		return "", err
	}

	// 年化利率转为日利率: apy / 100 / 365
	dailyRate := r.Div(decimal.NewFromInt(100)).Div(decimal.NewFromInt(365))
	// 利息 = 本金 * 日利率 * 天数
	interest := p.Mul(dailyRate).Mul(decimal.NewFromInt(int64(days)))

	return interest.Round(BalancePrecision).String(), nil
}

// CalculateDailyInterest 计算日利息: 本金 * 年化利率 / 365
func CalculateDailyInterest(principal, apy string) (string, error) {
	return CalculateInterest(principal, apy, 1)
}

// CalculateYearInterest 计算年利息: 本金 * 年化利率
func CalculateYearInterest(principal, apy string) (string, error) {
	p, err := decimal.NewFromString(principal)
	if err != nil {
		return "", err
	}
	r, err := decimal.NewFromString(apy)
	if err != nil {
		return "", err
	}
	interest := p.Mul(r.Div(decimal.NewFromInt(100)))
	return interest.Round(BalancePrecision).String(), nil
}

// ============================================================
// 比较运算
// ============================================================

// Compare 比较: a > b 返回 1, a < b 返回 -1, a == b 返回 0
func Compare(a, b string) (int, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return 0, err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return 0, err
	}
	return aDec.Cmp(bDec), nil
}

// GT 大于: a > b
func GT(a, b string) (bool, error) {
	cmp, err := Compare(a, b)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// GTE 大于等于: a >= b
func GTE(a, b string) (bool, error) {
	cmp, err := Compare(a, b)
	if err != nil {
		return false, err
	}
	return cmp >= 0, nil
}

// LT 小于: a < b
func LT(a, b string) (bool, error) {
	cmp, err := Compare(a, b)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// LTE 小于等于: a <= b
func LTE(a, b string) (bool, error) {
	cmp, err := Compare(a, b)
	if err != nil {
		return false, err
	}
	return cmp <= 0, nil
}

// Equal 等于: a == b
func Equal(a, b string) (bool, error) {
	cmp, err := Compare(a, b)
	if err != nil {
		return false, err
	}
	return cmp == 0, nil
}

// IsZero 是否为零
func IsZero(s string) (bool, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return false, err
	}
	return d.IsZero(), nil
}

// IsPositive 是否为正数
func IsPositive(s string) (bool, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return false, err
	}
	return d.GreaterThan(decimal.Zero), nil
}

// IsNegative 是否为负数
func IsNegative(s string) (bool, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return false, err
	}
	return d.LessThan(decimal.Zero), nil
}

// ============================================================
// 验证函数
// ============================================================

// IsValidAmount 验证是否为有效金额 (>= 0)
func IsValidAmount(s string) bool {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return false
	}
	return d.GreaterThanOrEqual(decimal.Zero)
}

// MustParse 解析金额,失败时 panic
func MustParse(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ============================================================
// 特殊金额处理
// ============================================================

// Neg 取负数
func Neg(s string) (string, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return "", err
	}
	return d.Neg().Round(BalancePrecision).String(), nil
}

// Abs 取绝对值
func Abs(s string) (string, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return "", err
	}
	return d.Abs().Round(BalancePrecision).String(), nil
}

// Min 最小值
func Min(a, b string) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return "", err
	}
	if aDec.LessThan(bDec) {
		return aDec.Round(BalancePrecision).String(), nil
	}
	return bDec.Round(BalancePrecision).String(), nil
}

// Max 最大值
func Max(a, b string) (string, error) {
	aDec, err := decimal.NewFromString(a)
	if err != nil {
		return "", err
	}
	bDec, err := decimal.NewFromString(b)
	if err != nil {
		return "", err
	}
	if aDec.GreaterThan(bDec) {
		return aDec.Round(BalancePrecision).String(), nil
	}
	return bDec.Round(BalancePrecision).String(), nil
}

// Sum 求和
func Sum(amounts ...string) (string, error) {
	var total decimal.Decimal
	for _, amount := range amounts {
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return "", err
		}
		total = total.Add(d)
	}
	return total.Round(BalancePrecision).String(), nil
}

// ============================================================
// 金额格式化
// ============================================================

// FormatWithThousandSep 格式化千分位
func FormatWithThousandSep(d decimal.Decimal) string {
	return d.Round(BalancePrecision).String()
}

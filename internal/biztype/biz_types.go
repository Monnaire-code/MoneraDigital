package biztype

type Category string

const (
	CategoryWealth     Category = "WEALTH"
	CategoryLending    Category = "LENDING"
	CategoryWithdrawal Category = "WITHDRAWAL"
	CategoryDeposit    Category = "DEPOSIT"
	CategorySystem     Category = "SYSTEM"
)

type Direction int

const (
	DirectionIncome   Direction = 1 // 收入
	DirectionExpense  Direction = 2 // 支出
	DirectionFreeze   Direction = 3 // 冻结
	DirectionUnfreeze Direction = 4 // 解冻
)

type BizType struct {
	ID        int       `json:"id"`
	Code      string    `json:"code"`
	NameKey   string    `json:"nameKey"`
	Category  Category  `json:"category"`
	Direction Direction `json:"direction"`
}

var AllBizTypes = map[int]BizType{
	1:  {ID: 1, Code: "WEALTH_SUBSCRIBE", NameKey: "biz.wealth_subscribe", Category: CategoryWealth, Direction: DirectionFreeze},
	2:  {ID: 2, Code: "WEALTH_REDEEM", NameKey: "biz.wealth_redeem", Category: CategoryWealth, Direction: DirectionUnfreeze},
	3:  {ID: 3, Code: "INTEREST_PAYOUT", NameKey: "biz.interest_payout", Category: CategoryWealth, Direction: DirectionIncome},
	4:  {ID: 4, Code: "LENDING_DEPOSIT", NameKey: "biz.lending_deposit", Category: CategoryLending, Direction: DirectionFreeze},
	5:  {ID: 5, Code: "LENDING_WITHDRAW", NameKey: "biz.lending_withdraw", Category: CategoryLending, Direction: DirectionUnfreeze},
	6:  {ID: 6, Code: "LENDING_INTEREST", NameKey: "biz.lending_interest", Category: CategoryLending, Direction: DirectionIncome},
	7:  {ID: 7, Code: "WITHDRAWAL_FEE", NameKey: "biz.withdrawal_fee", Category: CategoryWithdrawal, Direction: DirectionExpense},
	8:  {ID: 8, Code: "WITHDRAWAL", NameKey: "biz.withdrawal", Category: CategoryWithdrawal, Direction: DirectionExpense},
	9:  {ID: 9, Code: "DEPOSIT", NameKey: "biz.deposit", Category: CategoryDeposit, Direction: DirectionIncome},
	10: {ID: 10, Code: "DEPOSIT_CONFIRM", NameKey: "biz.deposit_confirm", Category: CategoryDeposit, Direction: DirectionIncome},
	11: {ID: 11, Code: "REFERRAL_BONUS", NameKey: "biz.referral_bonus", Category: CategorySystem, Direction: DirectionIncome},
	12: {ID: 12, Code: "ADMIN_FREEZE", NameKey: "biz.admin_freeze", Category: CategorySystem, Direction: DirectionFreeze},
	13: {ID: 13, Code: "ADMIN_UNFREEZE", NameKey: "biz.admin_unfreeze", Category: CategorySystem, Direction: DirectionUnfreeze},
	14: {ID: 14, Code: "ADMIN_DEPOSIT", NameKey: "biz.admin_deposit", Category: CategorySystem, Direction: DirectionIncome},
	15: {ID: 15, Code: "ADMIN_DEDUCTION", NameKey: "biz.admin_deduction", Category: CategorySystem, Direction: DirectionExpense},
}

func GetByID(id int) (BizType, bool) {
	bt, ok := AllBizTypes[id]
	return bt, ok
}

func GetAll() []BizType {
	result := make([]BizType, 0, len(AllBizTypes))
	for _, bt := range AllBizTypes {
		result = append(result, bt)
	}
	return result
}

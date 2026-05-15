package config

type Chain struct {
	Code          string
	Name          string
	NetworkFamily string
	ChainID       string
	NativeSymbol  string
	ExplorerURL   string
	ShortName     string
	Enabled       bool
	DisplayOrder  int
}

type Coin struct {
	ID           int
	Symbol       string
	Name         string
	IsStable     bool
	Enabled      bool
	DisplayOrder int
}

type CoinChain struct {
	ID                      int
	ChainCode               string
	CoinID                  int
	Chain                   *Chain
	Coin                    *Coin
	IsNative                bool
	TokenContract           string
	Decimals                int
	SafeheronCoinKey        string
	MinDepositAmount        string
	DepositEnabled          bool
	WithdrawEnabled         bool
	RequiredConfirmations   int
	TokenStandard           string
	EstimatedArrivalMinutes int
	DisplayOrder            int
}

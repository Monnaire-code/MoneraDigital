package safeheron

type Wallet struct {
	AccountKey      string
	CustomerRefID   string
	CoinAddressList []CoinAddress
}

type CoinAddress struct {
	CoinKey         string
	AddressGroupKey string
	Address         string
	DerivePath      string
}

type AccountCoin struct {
	CoinKey     string
	Symbol      string
	Balance     string
	AddressList []AddressInfo
}

type AddressInfo struct {
	Address     string
	AddressType string
	DerivePath  string
	Balance     string
}

type Account struct {
	AccountKey    string
	CustomerRefID string
	AccountName   string
	AccountTag    string
	HiddenOnUI    bool
	AutoFuel      bool
}

type WebhookEvent struct {
	EventType   string       `json:"eventType"`
	EventDetail EventDetail  `json:"eventDetail"`
}

type EventDetail struct {
	TxKey                string `json:"txKey"`
	CoinKey              string `json:"coinKey"`
	TxAmount             string `json:"txAmount"`
	TransactionStatus    string `json:"transactionStatus"`
	TransactionSubStatus string `json:"transactionSubStatus"`
	SourceAddress        string `json:"sourceAddress"`
	DestinationAddress   string `json:"destinationAddress"`
	CustomerRefID        string `json:"customerRefId"`
	BlockHeight          int64  `json:"blockHeight"`
	BlockHash            string `json:"blockHash"`
	TxHash               string `json:"txHash"`
	TransactionDirection string `json:"transactionDirection"`
}

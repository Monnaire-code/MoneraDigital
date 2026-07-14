package safeheron

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
)

type historyTransactionAPI struct {
	listFn func(api.ListTransactionsV2Request, *api.TransactionsResponseV2) error
	oneFn  func(api.OneTransactionsRequest, *api.OneTransactionsResponse) error
}

func (m *historyTransactionAPI) CreateTransactionsV3(
	api.CreateTransactionsRequest,
	*api.CreateTransactionV3Response,
) error {
	return nil
}

func (m *historyTransactionAPI) ListTransactionsV2(
	req api.ListTransactionsV2Request,
	resp *api.TransactionsResponseV2,
) error {
	return m.listFn(req, resp)
}

func (m *historyTransactionAPI) OneTransactions(
	req api.OneTransactionsRequest,
	resp *api.OneTransactionsResponse,
) error {
	return m.oneFn(req, resp)
}

func richHistoryResponse() api.TransactionsResponse {
	return api.TransactionsResponse{
		TxKey:                      "tx-001",
		TxHash:                     "0xabc",
		CoinKey:                    "ETHEREUM_ETH",
		TxAmount:                   "1.234567890123456789",
		SourceAccountKey:           "vault-source",
		SourceAccountType:          "VAULT_ACCOUNT",
		SourceAddress:              "0xsource",
		IsSourcePhishing:           true,
		SourceAddressList:          []api.SourceAddress{{Address: "0xsource", IsSourcePhishing: true, AddressGroupKey: "source-group"}},
		DestinationAccountKey:      "vault-destination",
		DestinationAccountType:     "VAULT_ACCOUNT",
		DestinationAddress:         "0xdestination",
		IsDestinationPhishing:      true,
		DestinationAddressList:     []api.DestinationAddress{{Address: "0xdestination", IsDestinationPhishing: true, Amount: "1.234567890123456789", Memo: "memo", AddressGroupKey: "destination-group"}},
		TransactionType:            "NORMAL",
		TransactionDirection:       "INTERNAL_TRANSFER",
		TransactionStatus:          "COMPLETED",
		TransactionSubStatus:       "CONFIRMED",
		CreateTime:                 1722470400000,
		CompletedTime:              1722470460000,
		TxFee:                      "0.00021",
		FeeCoinKey:                 "ETHEREUM_ETH",
		GasFee:                     []api.GasFee{{Symbol: "ETH", Amount: "0.00021"}},
		BlockHeight:                123456,
		TxAmountToUsd:              "4321.987654321",
		CustomerRefId:              "finance-ref-1",
		AmlLock:                    "YES",
		AmlScreeningTriggeredState: "TRIGGERED",
		AmlList:                    []api.Aml{{Provider: "MistTrack", Timestamp: "1722470460000", Status: "COMPLETED", RiskLevel: "LOW", LastUpdateTime: "1722470461000"}},
	}
}

func TestListTransactions_MapsCursorAndCanonicalSnapshot(t *testing.T) {
	sdkResponse := richHistoryResponse()
	client := &Client{transaction: &historyTransactionAPI{
		listFn: func(req api.ListTransactionsV2Request, resp *api.TransactionsResponseV2) error {
			if req.Direct != "NEXT" || req.Limit != 50 || req.FromId != "tx-before" {
				t.Fatalf("unexpected cursor request: %#v", req)
			}
			if req.AccountKey != "vault-source" || req.CreateTimeMin != 1722470400000 || req.CreateTimeMax != 1722556800000 {
				t.Fatalf("unexpected list filters: %#v", req)
			}
			if req.HideSmallAmountUsd != "" {
				t.Fatalf("history list must not filter small amounts: %#v", req)
			}
			*resp = api.TransactionsResponseV2{sdkResponse}
			return nil
		},
	}}

	snapshots, err := client.ListTransactions(context.Background(), TransactionHistoryRequest{
		Limit:         50,
		Cursor:        "tx-before",
		AccountKey:    "vault-source",
		CreateTimeMin: 1722470400000,
		CreateTimeMax: 1722556800000,
	})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}

	got := snapshots[0]
	if got.TxKey != sdkResponse.TxKey || got.TxHash != sdkResponse.TxHash || got.CoinKey != sdkResponse.CoinKey || got.TxAmount != sdkResponse.TxAmount {
		t.Fatalf("basic transaction fields not preserved: %#v", got)
	}
	if got.SourceAddress != sdkResponse.SourceAddress || got.DestinationAddress != sdkResponse.DestinationAddress || !got.IsSourcePhishing || !got.IsDestinationPhishing {
		t.Fatalf("address/phishing fields not preserved: %#v", got)
	}
	if len(got.SourceAddressList) != 1 || got.SourceAddressList[0].AddressGroupKey != "source-group" || len(got.DestinationAddressList) != 1 || got.DestinationAddressList[0].Amount != sdkResponse.TxAmount {
		t.Fatalf("batch address fields not preserved: %#v", got)
	}
	if got.TxFee != sdkResponse.TxFee || got.FeeCoinKey != sdkResponse.FeeCoinKey || len(got.GasFee) != 1 || got.GasFee[0].Amount != "0.00021" {
		t.Fatalf("fee fields not preserved: %#v", got)
	}
	if got.CreateTime != sdkResponse.CreateTime || got.CompletedTime != sdkResponse.CompletedTime || got.TransactionStatus != "COMPLETED" || got.TxAmountToUSD != sdkResponse.TxAmountToUsd {
		t.Fatalf("time/status/USD fields not preserved: %#v", got)
	}
	if got.AmlLock != "YES" || got.AMLScreeningTriggeredState != "TRIGGERED" || len(got.AMLList) != 1 || got.AMLList[0].RiskLevel != "LOW" {
		t.Fatalf("AML fields not preserved: %#v", got)
	}

	wantRaw, err := json.Marshal(sdkResponse)
	if err != nil {
		t.Fatalf("marshal expected raw payload: %v", err)
	}
	if !json.Valid(got.RawPayload) || string(got.RawPayload) != string(wantRaw) {
		t.Fatalf("raw payload must be canonical SDK JSON\n got: %s\nwant: %s", got.RawPayload, wantRaw)
	}
}

func TestListTransactions_RejectsInvalidLimitBeforeSDKCall(t *testing.T) {
	for _, limit := range []int32{0, -1, 501} {
		t.Run("limit", func(t *testing.T) {
			called := false
			client := &Client{transaction: &historyTransactionAPI{
				listFn: func(api.ListTransactionsV2Request, *api.TransactionsResponseV2) error {
					called = true
					return nil
				},
			}}
			_, err := client.ListTransactions(context.Background(), TransactionHistoryRequest{Limit: limit})
			if err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("ListTransactions(limit=%d) error = %v, want limit validation", limit, err)
			}
			if called {
				t.Fatalf("SDK called for invalid limit %d", limit)
			}
		})
	}
}

func TestListTransactions_WrapsSDKError(t *testing.T) {
	client := &Client{transaction: &historyTransactionAPI{
		listFn: func(api.ListTransactionsV2Request, *api.TransactionsResponseV2) error {
			return errors.New("sdk unavailable")
		},
	}}
	_, err := client.ListTransactions(context.Background(), TransactionHistoryRequest{Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "ListTransactionsV2") {
		t.Fatalf("ListTransactions error = %v, want wrapped SDK error", err)
	}
}

func TestListTransactions_ReturnsPromptlyWhenContextCancelsDuringSDKCall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	client := &Client{transaction: &historyTransactionAPI{
		listFn: func(_ api.ListTransactionsV2Request, _ *api.TransactionsResponseV2) error {
			close(started)
			<-release
			close(finished)
			return nil
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := client.ListTransactions(ctx, TransactionHistoryRequest{Limit: 1})
		returned <- err
	}()

	<-started
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListTransactions cancellation error = %v, want context canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("ListTransactions waited for the blocking SDK call after context cancellation")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("blocking SDK test call did not finish after release")
	}
}

func TestLookupTransaction_ByCustomerReferencePreservesCanonicalSnapshot(t *testing.T) {
	listResponse := richHistoryResponse()
	client := &Client{transaction: &historyTransactionAPI{
		oneFn: func(req api.OneTransactionsRequest, resp *api.OneTransactionsResponse) error {
			if req.TxKey != "" || req.CustomerRefId != "finance-ref-1" {
				t.Fatalf("unexpected one request: %#v", req)
			}
			*resp = api.OneTransactionsResponse{
				TxKey:                      listResponse.TxKey,
				TxHash:                     listResponse.TxHash,
				CoinKey:                    listResponse.CoinKey,
				TxAmount:                   listResponse.TxAmount,
				SourceAccountKey:           listResponse.SourceAccountKey,
				SourceAccountType:          listResponse.SourceAccountType,
				SourceAddress:              listResponse.SourceAddress,
				IsSourcePhishing:           listResponse.IsSourcePhishing,
				SourceAddressList:          listResponse.SourceAddressList,
				DestinationAccountKey:      listResponse.DestinationAccountKey,
				DestinationAccountType:     listResponse.DestinationAccountType,
				DestinationAddress:         listResponse.DestinationAddress,
				IsDestinationPhishing:      listResponse.IsDestinationPhishing,
				DestinationAddressList:     listResponse.DestinationAddressList,
				TransactionType:            listResponse.TransactionType,
				TransactionDirection:       listResponse.TransactionDirection,
				TransactionStatus:          listResponse.TransactionStatus,
				TransactionSubStatus:       listResponse.TransactionSubStatus,
				CreateTime:                 listResponse.CreateTime,
				CompletedTime:              listResponse.CompletedTime,
				TxFee:                      listResponse.TxFee,
				FeeCoinKey:                 listResponse.FeeCoinKey,
				GasFee:                     listResponse.GasFee,
				BlockHeight:                listResponse.BlockHeight,
				TxAmountToUsd:              listResponse.TxAmountToUsd,
				CustomerRefId:              listResponse.CustomerRefId,
				AmlLock:                    listResponse.AmlLock,
				AmlScreeningTriggeredState: listResponse.AmlScreeningTriggeredState,
				AmlList:                    listResponse.AmlList,
			}
			return nil
		},
	}}

	got, err := client.LookupTransaction(context.Background(), TransactionLookup{CustomerRefID: "finance-ref-1"})
	if err != nil {
		t.Fatalf("LookupTransaction: %v", err)
	}
	if got.TxKey != "tx-001" || got.CustomerRefID != "finance-ref-1" || got.TxFee != "0.00021" || len(got.DestinationAddressList) != 1 {
		t.Fatalf("lookup fields not preserved: %#v", got)
	}
	if !json.Valid(got.RawPayload) || !strings.Contains(string(got.RawPayload), `"txKey":"tx-001"`) {
		t.Fatalf("lookup raw payload is missing canonical transaction JSON: %s", got.RawPayload)
	}
}

func TestLookupTransaction_RequiresExactlyOneKnownIdentifier(t *testing.T) {
	called := false
	client := &Client{transaction: &historyTransactionAPI{
		oneFn: func(api.OneTransactionsRequest, *api.OneTransactionsResponse) error {
			called = true
			return nil
		},
	}}

	for _, lookup := range []TransactionLookup{{}, {TxKey: "tx-1", CustomerRefID: "ref-1"}} {
		_, err := client.LookupTransaction(context.Background(), lookup)
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("LookupTransaction(%#v) error = %v, want identifier validation", lookup, err)
		}
	}
	if called {
		t.Fatal("SDK called for invalid lookup")
	}
}

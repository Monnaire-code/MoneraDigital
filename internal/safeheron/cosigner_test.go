package safeheron

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/cosigner"
)

// ---------------------------------------------------------------------------
// Mock converter
// ---------------------------------------------------------------------------

type mockCosignerConverter struct {
	requestResult  string
	requestErr     error
	responseResult map[string]string
	responseErr    error
}

func (m *mockCosignerConverter) RequestV3Convert(_ cosigner.CoSignerCallBackV3) (string, error) {
	return m.requestResult, m.requestErr
}

func (m *mockCosignerConverter) ResponseV3Converter(_ any) (map[string]string, error) {
	return m.responseResult, m.responseErr
}

// ---------------------------------------------------------------------------
// ParseRequest tests
// ---------------------------------------------------------------------------

func TestParseRequest_Transaction(t *testing.T) {
	detail := `{"txKey":"tx-123","coinKey":"ETH","txAmount":"1.5","transactionType":"AUTO_SWEEP"}`
	bizJSON := `{"approvalId":"ap-1","type":"TRANSACTION","detail":` + detail + `}`

	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: bizJSON}}

	biz, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if biz.ApprovalId != "ap-1" {
		t.Errorf("approvalId = %q, want ap-1", biz.ApprovalId)
	}
	if biz.Type != "TRANSACTION" {
		t.Errorf("type = %q, want TRANSACTION", biz.Type)
	}

	var tx TransactionApproval
	if err := json.Unmarshal(biz.Detail, &tx); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if tx.TxKey != "tx-123" {
		t.Errorf("txKey = %q, want tx-123", tx.TxKey)
	}
	if tx.TransactionType != "AUTO_SWEEP" {
		t.Errorf("transactionType = %q, want AUTO_SWEEP", tx.TransactionType)
	}
}

func TestParseRequest_CallbackTest(t *testing.T) {
	bizJSON := `{"approvalId":"test-1","type":"CALLBACK_TEST","detail":{}}`
	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: bizJSON}}

	biz, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if biz.Type != "CALLBACK_TEST" {
		t.Errorf("type = %q, want CALLBACK_TEST", biz.Type)
	}
}

func TestParseRequest_MPCSign(t *testing.T) {
	bizJSON := `{"approvalId":"mpc-1","type":"MPC_SIGN","detail":{"txKey":"mpc-tx","signAlg":"secp256k1"}}`
	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: bizJSON}}

	biz, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if biz.Type != "MPC_SIGN" {
		t.Errorf("type = %q, want MPC_SIGN", biz.Type)
	}
	var mpc MPCSignApproval
	if err := json.Unmarshal(biz.Detail, &mpc); err != nil {
		t.Fatalf("unmarshal MPC detail: %v", err)
	}
	if mpc.SignAlg != "secp256k1" {
		t.Errorf("signAlg = %q, want secp256k1", mpc.SignAlg)
	}
}

func TestParseRequest_Web3Sign(t *testing.T) {
	bizJSON := `{"approvalId":"w3-1","type":"WEB3_SIGN","detail":{"txKey":"w3-tx","subjectType":"eth_signTransaction"}}`
	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: bizJSON}}

	biz, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var w3 Web3Approval
	if err := json.Unmarshal(biz.Detail, &w3); err != nil {
		t.Fatalf("unmarshal Web3 detail: %v", err)
	}
	if w3.SubjectType != "eth_signTransaction" {
		t.Errorf("subjectType = %q, want eth_signTransaction", w3.SubjectType)
	}
}

func TestParseRequest_VerifyFails(t *testing.T) {
	client := &CosignerClient{converter: &mockCosignerConverter{
		requestErr: errors.New("signature verification failed"),
	}}

	_, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "cosigner verify/decode: signature verification failed" {
		t.Errorf("error = %q", got)
	}
}

func TestParseRequest_InvalidJSON(t *testing.T) {
	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: "not-json"}}

	_, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRequest_EmptyBizContent(t *testing.T) {
	client := &CosignerClient{converter: &mockCosignerConverter{requestResult: "{}"}}

	biz, err := client.ParseRequest(cosigner.CoSignerCallBackV3{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if biz.ApprovalId != "" {
		t.Errorf("approvalId = %q, want empty", biz.ApprovalId)
	}
}

// ---------------------------------------------------------------------------
// BuildResponse tests
// ---------------------------------------------------------------------------

func TestBuildResponse_Success(t *testing.T) {
	expected := map[string]string{
		"code": "200", "message": "SUCCESS",
		"bizContent": "encoded", "sig": "signed",
	}
	client := &CosignerClient{converter: &mockCosignerConverter{responseResult: expected}}

	result, err := client.BuildResponse("ap-1", "APPROVE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["code"] != "200" {
		t.Errorf("code = %q, want 200", result["code"])
	}
	if result["sig"] != "signed" {
		t.Errorf("sig = %q, want signed", result["sig"])
	}
}

func TestBuildResponse_SignError(t *testing.T) {
	client := &CosignerClient{converter: &mockCosignerConverter{
		responseErr: errors.New("signing failed"),
	}}

	_, err := client.BuildResponse("ap-1", "REJECT")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "cosigner sign response: signing failed" {
		t.Errorf("error = %q", got)
	}
}

func TestBuildResponse_Reject(t *testing.T) {
	expected := map[string]string{"code": "200", "bizContent": "rej"}
	client := &CosignerClient{converter: &mockCosignerConverter{responseResult: expected}}

	result, err := client.BuildResponse("ap-2", "REJECT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["code"] != "200" {
		t.Errorf("code = %q, want 200", result["code"])
	}
}

// ---------------------------------------------------------------------------
// Type serialization round-trip tests
// ---------------------------------------------------------------------------

func TestTransactionApproval_JSONRoundTrip(t *testing.T) {
	orig := TransactionApproval{
		TxKey:                  "tx-1",
		CoinKey:                "USDT_ERC20",
		TxAmount:               "100.50",
		TransactionType:        "AUTO_SWEEP",
		DestinationAccountKey:  "acct-main",
		DestinationAccountType: "VAULT_ACCOUNT",
		DestinationAddress:     "0xabc",
		SourceAddress:          "0xdef",
		FeeCoinKey:             "ETH",
		EstimateFee:            "0.001",
		CustomerRefId:          "ref-1",
		DestinationAddressList: []DestinationAddressEntry{
			{Address: "0xabc", Amount: "100.50"},
		},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded TransactionApproval
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TxKey != orig.TxKey {
		t.Errorf("txKey = %q, want %q", decoded.TxKey, orig.TxKey)
	}
	if decoded.TransactionType != orig.TransactionType {
		t.Errorf("transactionType = %q, want %q", decoded.TransactionType, orig.TransactionType)
	}
	if len(decoded.DestinationAddressList) != 1 {
		t.Fatalf("destinationAddressList len = %d, want 1", len(decoded.DestinationAddressList))
	}
	if decoded.DestinationAddressList[0].Address != "0xabc" {
		t.Errorf("dest address = %q, want 0xabc", decoded.DestinationAddressList[0].Address)
	}
}

func TestCoSignerBizContentV3_DelayedDetailParsing(t *testing.T) {
	raw := `{"approvalId":"ap-1","type":"TRANSACTION","detail":{"txKey":"k1","transactionType":"AUTO_FUEL"}}`
	var biz CoSignerBizContentV3
	if err := json.Unmarshal([]byte(raw), &biz); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if biz.Detail == nil {
		t.Fatal("detail should not be nil")
	}
	var tx TransactionApproval
	if err := json.Unmarshal(biz.Detail, &tx); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if tx.TransactionType != "AUTO_FUEL" {
		t.Errorf("transactionType = %q, want AUTO_FUEL", tx.TransactionType)
	}
}

func TestMPCSignApproval_JSONRoundTrip(t *testing.T) {
	orig := MPCSignApproval{
		TxKey:   "mpc-1",
		SignAlg: "secp256k1",
	}
	data, _ := json.Marshal(orig)
	var decoded MPCSignApproval
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SignAlg != "secp256k1" {
		t.Errorf("signAlg = %q", decoded.SignAlg)
	}
}

func TestWeb3Approval_JSONRoundTrip(t *testing.T) {
	orig := Web3Approval{
		TxKey:       "w3-1",
		SubjectType: "personal_sign",
	}
	data, _ := json.Marshal(orig)
	var decoded Web3Approval
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SubjectType != "personal_sign" {
		t.Errorf("subjectType = %q", decoded.SubjectType)
	}
}

// ---------------------------------------------------------------------------
// NewCosignerClient tests
// ---------------------------------------------------------------------------

func TestNewCosignerClient_Success(t *testing.T) {
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "pub.pem")
	privPath := filepath.Join(dir, "priv.pem")
	os.WriteFile(pubPath, []byte("fake-pub"), 0o644)
	os.WriteFile(privPath, []byte("fake-priv"), 0o600)

	client, err := NewCosignerClient(CosignerConfig{
		CoSignerPubKeyPath:     pubPath,
		CallbackPrivateKeyPath: privPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("client should not be nil")
	}
}

func TestNewCosignerClient_MissingPubKey(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "priv.pem")
	os.WriteFile(privPath, []byte("fake-priv"), 0o600)

	_, err := NewCosignerClient(CosignerConfig{
		CoSignerPubKeyPath:     filepath.Join(dir, "nonexistent.pem"),
		CallbackPrivateKeyPath: privPath,
	})
	if err == nil {
		t.Fatal("expected error for missing pub key")
	}
}

func TestNewCosignerClient_MissingPrivKey(t *testing.T) {
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "pub.pem")
	os.WriteFile(pubPath, []byte("fake-pub"), 0o644)

	_, err := NewCosignerClient(CosignerConfig{
		CoSignerPubKeyPath:     pubPath,
		CallbackPrivateKeyPath: filepath.Join(dir, "nonexistent.pem"),
	})
	if err == nil {
		t.Fatal("expected error for missing priv key")
	}
}

func TestNewCosignerClient_EmptyPaths(t *testing.T) {
	_, err := NewCosignerClient(CosignerConfig{})
	if err == nil {
		t.Fatal("expected error for empty paths")
	}
}

func TestNewCosignerClient_DirectoryAsPubKey(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "priv.pem")
	os.WriteFile(privPath, []byte("fake-priv"), 0o600)

	_, err := NewCosignerClient(CosignerConfig{
		CoSignerPubKeyPath:     dir,
		CallbackPrivateKeyPath: privPath,
	})
	if err == nil {
		t.Fatal("expected error for directory as pub key")
	}
}

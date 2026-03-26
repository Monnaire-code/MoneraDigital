import { useTranslation } from "react-i18next";
import { Shield, Copy, Check, AlertCircle, Loader2, Wallet, Globe, Plus } from "lucide-react";
import { useState, useEffect, useMemo } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useToast } from "@/components/ui/use-toast";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiRequest, ApiError } from "@/lib/api-client";

// Mock mode - set to true to generate test wallet addresses without backend calls
const MOCK_MODE = true;

// SQL NullString format from backend (sql.NullString serializes to {String, Valid})
interface NullString {
  String: string;
  Valid: boolean;
}

// Wallet info response type - matches backend WalletCreationRequest structure
interface WalletInfoResponse {
  status: string;
  currency?: string;
  walletId?: NullString;
  address?: NullString;
  addresses?: NullString | string; // Can be NullString or plain string
}

// Network option type
interface NetworkOption {
  value: string;
  label: string;
}

// Parse addresses JSON and extract available networks
export const parseAvailableNetworks = (addressesJson: string): NetworkOption[] => {
  if (!addressesJson) return [];
  try {
    const addrMap = JSON.parse(addressesJson);
    return Object.keys(addrMap).map((network) => ({
      value: network,
      label: formatNetworkLabel(network),
    }));
  } catch {
    return [];
  }
};

// Get display address for selected network
export const getDisplayAddress = (addressesJson: string, selectedNetwork: string): string => {
  if (!addressesJson) return "";
  try {
    const addrMap = JSON.parse(addressesJson);
    return addrMap[selectedNetwork] || "";
  } catch {
    return "";
  }
};

// Format currency for display (e.g., "USDT_TRC20" -> "USDT", "USDC_BEP20" -> "USDC")
export const formatCurrency = (currency: string): string => {
  if (!currency) return "";
  // Handle compound currencies like USDT_TRC20, USDT_ERC20, USDT_BEP20, USDC_BEP20
  if (currency.startsWith("USDT_") || currency.startsWith("USDC_")) {
    return currency.split("_")[0];
  }
  return currency;
};

// Format network label for display (e.g., "USDT_TRON" -> "USDT (TRON)", "ETH" -> "ETH")
export const formatNetworkLabel = (networkKey: string): string => {
  if (!networkKey) return "";
  // Handle compound keys like USDT_TRON, USDC_TRON, USDT_ERC20, USDT_BSC
  if (networkKey.includes("_")) {
    const parts = networkKey.split("_");
    const token = parts[0];
    const chain = parts[1];
    // Map chain abbreviations to full names
    const chainDisplayName = getChainDisplayName(chain);
    return `${token} (${chainDisplayName})`;
  }
  return networkKey;
};

// Get display name for chain abbreviation
const getChainDisplayName = (chain: string): string => {
  const chainMap: Record<string, string> = {
    TRC20: "TRC20",
    ERC20: "ERC20",
    BEP20: "BEP20",
  };
  return chainMap[chain] || chain;
};

// Currency options (DB存储格式: USDC_BEP20使用长格式)
const CURRENCY_OPTIONS = [
  { value: "USDT_ERC20", label: "USDT (ERC20)" },
  { value: "USDT_TRC20", label: "USDT (TRC20)" },
  { value: "USDT_BEP20", label: "USDT (BEP20)" },
  { value: "USDC_ERC20", label: "USDC (ERC20)" },
  { value: "USDC_TRC20", label: "USDC (TRC20)" },
  { value: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET", label: "USDC (BEP20)" }, // 特例：长格式
];

// Token options for adding new address
const TOKEN_OPTIONS = [
  { value: "USDT", label: "USDT" },
  { value: "USDC", label: "USDC" },
];

// Network options for adding new address
const NETWORK_OPTIONS = [
  { value: "TRC20", label: "TRON (TRC20)", name: "TRON" },
  { value: "TRX(SHASTA)_TRON_TESTNET", label: "TRX (SHASTA) - TRON Testnet", name: "TRON Testnet" },
  { value: "ERC20", label: "Ethereum (ERC20)", name: "Ethereum" },
  { value: "BEP20", label: "BNB Smart Chain (BEP20)", name: "BNB Smart Chain" },
];

// Add address request type
interface AddAddressRequest {
  chain: string;
  token: string;
}

// Chain to backend value mapping (统一短格式)
const CHAIN_TO_BACKEND_MAP: Record<string, string> = {
  "TRC20": "TRC20",
  "TRON_TESTNET": "TRON_TESTNET",
  "ERC20": "ERC20",
  "BEP20": "BEP20",
};

// Get backend chain value for API call
const getBackendChain = (chain: string): string => {
  const mappedValue = CHAIN_TO_BACKEND_MAP[chain];
  const result = mappedValue || chain;
  console.log(`[DEBUG-ADDRESS] getBackendChain called with: "${chain}"`);
  console.log(`[DEBUG-ADDRESS] CHAIN_TO_BACKEND_MAP lookup: "${mappedValue}"`);
  console.log(`[DEBUG-ADDRESS] Final result: "${result}"`);
  return result;
};

// Get wallet address request type
interface GetWalletAddressRequest {
  userId: string;
  productCode: string;
  currency: string;
}

// Wallet address data from API
interface WalletAddressData {
  address: string;
  addressType?: string;
  derivePath?: string;
}

const AccountOpening = () => {
  const { t } = useTranslation();
  const { toast } = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const [copied, setCopied] = useState(false);
  const [selectedCurrency, setSelectedCurrency] = useState("TRON");
  const queryClient = useQueryClient();

  // Get token for API calls
  const token = typeof window !== "undefined" ? localStorage.getItem("token") : null;

  // Auth guard: redirect to login if not authenticated
  useEffect(() => {
    if (!token) {
      toast({
        variant: "destructive",
        title: t("auth.required"),
        description: t("auth.pleaseLoginFirst"),
      });
      navigate("/login", { state: { returnTo: location.pathname } });
    }
  }, [token, navigate, location.pathname, toast, t]);

  const { data: walletInfo, isLoading, error } = useQuery<WalletInfoResponse>({
    queryKey: ["walletInfo"],
    enabled: !!token,
    queryFn: async () => {
      if (MOCK_MODE) {
        // Return mock wallet data for testing
        console.log("[MOCK] AccountOpening: Returning mock wallet data");
        return {
          status: "SUCCESS",
          currency: "USDT",
          walletId: { String: "wallet-123", Valid: true },
          address: { String: "TJR7VfPVgBGGd7Df7fCpWeYEjGVPgrr", Valid: true },
          addresses: JSON.stringify({
            "USDT_TRC20": "TJR7VfPVgBGGd7Df7fCpWeYEjGVPgrr",
            "USDT_ERC20": "0x742d35Cc6634C0532925a3b844Bc9e7595f8bE21",
            "USDT_BEP20": "0x1234567890abcdef1234567890abcdef12345678"
          })
        };
      }
      if (!token) {
        console.log("[DEBUG] AccountOpening: No token, returning NONE");
        return { status: "NONE" };
      }
      if (!token) {
        console.log("[DEBUG] AccountOpening: No token, returning NONE");
        return { status: "NONE" };
      }
      console.log("[DEBUG] AccountOpening: Fetching wallet info...");
      const response = await apiRequest("/api/wallet/info", {
        headers: { Authorization: `Bearer ${token}` },
      }) as WalletInfoResponse;
      console.log("[DEBUG] AccountOpening: Wallet info response:", JSON.stringify(response, null, 2));
      return response;
    },
    retry: (failureCount, err) => {
      console.log(`[DEBUG] AccountOpening: Retry attempt ${failureCount}, error:`, err);
      if (err instanceof ApiError && err.status === 401) {
        return false;
      }
      return failureCount < 3;
    },
    refetchInterval: (query) => {
      const data = query.state.data as WalletInfoResponse | undefined;
      console.log(`[DEBUG] AccountOpening: RefetchInterval check - status: ${data?.status}, shouldPoll: ${data?.status === "CREATING"}`);
      return data?.status === "CREATING" ? 2000 : false;
    },
  });

  useEffect(() => {
    console.log("[DEBUG] AccountOpening: walletInfo updated:", {
      status: walletInfo?.status,
      isLoading,
      hasError: !!error,
      errorMessage: error?.message,
    });
  }, [walletInfo, isLoading, error]);

  // Handle query errors
  useEffect(() => {
    if (error) {
      // Check if it's a 401 error
      if (error instanceof ApiError && error.status === 401) {
        // Clear token and redirect to login
        localStorage.removeItem('token');
        toast({
          variant: 'destructive',
          title: t("auth.sessionExpired"),
          description: t("auth.pleaseLoginAgain"),
        });
        navigate('/login', { state: { returnTo: location.pathname } });
        return;
      }

      // Handle other errors
      toast({
        variant: 'destructive',
        title: t("wallet.opening.errorTitle"),
        description: error instanceof Error ? error.message : t("common.error"),
      });
    }
  }, [error, navigate, location.pathname, toast, t]);

  // Parse addresses and extract available networks
  const addressesJson = useMemo(() => {
    if (!walletInfo?.addresses) return "";
    return typeof walletInfo.addresses === "string"
      ? walletInfo.addresses
      : walletInfo.addresses.String || "";
  }, [walletInfo?.addresses]);

  const availableNetworks = useMemo(
    () => parseAvailableNetworks(addressesJson),
    [addressesJson]
  );

  const [selectedNetwork, setSelectedNetwork] = useState<string>("");
  const [showAddAddressDialog, setShowAddAddressDialog] = useState(false);
  const [selectedToken, setSelectedToken] = useState<string>("USDT");
  const [selectedChain, setSelectedChain] = useState<string>("TRON");

  useEffect(() => {
    if (availableNetworks.length > 0 && !selectedNetwork) {
      setSelectedNetwork(availableNetworks[0].value);
    }
  }, [availableNetworks, selectedNetwork]);

  const createMutation = useMutation({
    mutationFn: async () => {
      if (MOCK_MODE) {
        // Mock mode - just return success without calling backend
        console.log("[MOCK] createMutation: Skipping backend call, returning mock success");
        return { status: "SUCCESS", message: "Mock wallet created" };
      }
      if (!token) {
        throw new Error("Not authenticated");
      }
      return apiRequest("/api/wallet/create", {
        method: "POST",
        headers: {
            Authorization: `Bearer ${token}`
        },
        body: JSON.stringify({
          productCode: "X_FINANCE",
          currency: selectedCurrency
        })
      });
    },
    mutationFn: async () => {
      if (!token) {
        throw new Error("Not authenticated");
      }
      return apiRequest("/api/wallet/create", {
        method: "POST",
        headers: {
            Authorization: `Bearer ${token}`
        },
        body: JSON.stringify({
          productCode: "X_FINANCE",
          currency: selectedCurrency
        })
      });
    },
    onSuccess: () => {
      // Invalidate walletInfo cache - data will be refreshed from wallet_creation_requests
      queryClient.invalidateQueries({ queryKey: ["walletInfo"] });
      // Address will be available from walletInfo.addresses after cache refresh
    },
    onError: (err) => {
      toast({
        variant: "destructive",
        title: t("wallet.opening.errorTitle"),
        description: err.message,
      });
    }
  });

  const addAddressMutation = useMutation({
    mutationFn: async (data: AddAddressRequest) => {
      if (!token) {
        throw new Error("Not authenticated");
      }
      return apiRequest("/api/wallet/addresses", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify(data),
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["walletInfo"] });
      setShowAddAddressDialog(false);
      toast({
        variant: "default",
        title: t("wallet.opening.addressAdded"),
        description: t("wallet.opening.addressAddedDesc"),
      });
    },
    onError: (err) => {
      toast({
        variant: "destructive",
        title: t("wallet.opening.addAddressErrorTitle"),
        description: err.message,
      });
    },
  });

  const handleAddAddress = () => {
    // Debug log the full request
    const backendChain = getBackendChain(selectedChain);
    const requestData = { chain: backendChain, token: selectedToken };
    console.log(`[DEBUG-ADDRESS] handleAddAddress called`);
    console.log(`[DEBUG-ADDRESS] selectedToken: "${selectedToken}"`);
    console.log(`[DEBUG-ADDRESS] selectedChain: "${selectedChain}"`);
    console.log(`[DEBUG-ADDRESS] Final request data:`, JSON.stringify(requestData, null, 2));
    
    // Use mapped backend chain value for API call
    addAddressMutation.mutate(requestData);
  };

  const handleCreateWallet = () => {
    createMutation.mutate();
  };

  const copyToClipboard = async (text: string) => {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
  };

  const status = walletInfo?.status || "NONE";
  const isCreating = status === "CREATING" || createMutation.isPending;

  // Debug: log address parsing
  console.log("[DEBUG-ACCOUNT-OPENING] addressesJson:", addressesJson);
  console.log("[DEBUG-ACCOUNT-OPENING] selectedNetwork:", selectedNetwork);
  console.log("[DEBUG-ACCOUNT-OPENING] availableNetworks:", availableNetworks);
  
  const displayAddress = useMemo(
    () => getDisplayAddress(addressesJson, selectedNetwork),
    [addressesJson, selectedNetwork]
  );

  // Address comes from walletInfo.addresses after cache refresh
  // Fallback to direct address field if displayAddress is empty
  const directAddress = walletInfo?.address?.String || "";
  const displayAddressValue = displayAddress || directAddress;
  
  console.log("[DEBUG-ACCOUNT-OPENING] displayAddress:", displayAddress);
  console.log("[DEBUG-ACCOUNT-OPENING] directAddress:", directAddress);
  console.log("[DEBUG-ACCOUNT-OPENING] displayAddressValue:", displayAddressValue);

  const walletId = walletInfo?.walletId?.String || walletInfo?.address?.String || "";

  return (
    <div className="space-y-6 animate-fade-in">
      {/* Header */}
      <div>
        <h1 className="text-3xl font-bold tracking-tight">{t("wallet.opening.title")}</h1>
        <p className="text-muted-foreground mt-2">
          {t("wallet.opening.description")}
        </p>
      </div>

      {/* Add Address Dialog */}
      <Dialog open={showAddAddressDialog} onOpenChange={setShowAddAddressDialog}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("wallet.opening.addAddressDialogTitle")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {/* Token Selection */}
            <div className="space-y-2">
              <label className="text-sm font-medium">{t("wallet.opening.tokenLabel")}</label>
              <Select value={selectedToken} onValueChange={setSelectedToken}>
                <SelectTrigger>
                  <SelectValue placeholder="Select token" />
                </SelectTrigger>
                <SelectContent>
                  {TOKEN_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Network Selection */}
            <div className="space-y-2">
              <label className="text-sm font-medium">{t("wallet.opening.networkLabel")}</label>
              <Select value={selectedChain} onValueChange={setSelectedChain}>
                <SelectTrigger>
                  <SelectValue placeholder="Select network" />
                </SelectTrigger>
                <SelectContent>
                  {NETWORK_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="flex justify-end gap-3">
            <Button
              variant="outline"
              onClick={() => setShowAddAddressDialog(false)}
              disabled={addAddressMutation.isPending}
            >
              {t("common.cancel")}
            </Button>
            <Button
              onClick={handleAddAddress}
              disabled={addAddressMutation.isPending}
            >
              {addAddressMutation.isPending ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  {t("wallet.opening.addingAddress")}
                </>
              ) : (
                t("wallet.opening.confirmAddAddress")
              )}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Main Card */}
      <Card className="bg-card/50 backdrop-blur-sm border-border/50">
        <CardHeader className="text-center pb-2">
          <div className="mx-auto mb-4 p-4 rounded-full bg-primary/10">
            <Shield className="w-12 h-12 text-primary" />
          </div>
          <CardTitle className="text-xl">{t("wallet.opening.cardTitle")}</CardTitle>
          <CardDescription className="max-w-md mx-auto">
            {t("wallet.opening.cardDescription")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* Security Info */}
          <div className="flex items-center gap-3 p-4 rounded-lg bg-secondary/50 text-sm">
            <AlertCircle className="w-5 h-5 text-primary shrink-0" />
            <p>{t("wallet.opening.securityInfo")}</p>
          </div>

          {/* Currency Selection */}
          {(status === "NONE" || status === "FAILED") && (
            <div className="space-y-2">
              <label className="text-sm font-medium">{t("wallet.opening.currencyLabel") || "Select Currency"}</label>
              <Select value={selectedCurrency} onValueChange={setSelectedCurrency}>
                <SelectTrigger>
                  <SelectValue placeholder="Select currency" />
                </SelectTrigger>
                <SelectContent>
                  {CURRENCY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}

          {/* Action Button */}
          {(status === "NONE" || status === "FAILED") && (
            <Button
              onClick={handleCreateWallet}
              className="w-full h-12 text-base font-medium"
              size="lg"
              disabled={isCreating}
            >
              {isCreating ? (
                  <Loader2 className="w-5 h-5 mr-2 animate-spin" />
              ) : (
                  <Wallet className="w-5 h-5 mr-2" />
              )}
              {isCreating ? t("wallet.opening.creating") : t("wallet.opening.activateButton")}
            </Button>
          )}

          {/* Loading State Display */}
          {status === "CREATING" && !createMutation.isPending && (
             <div className="flex flex-col items-center justify-center py-8 space-y-4">
               <Loader2 className="w-12 h-12 text-primary animate-spin" />
               <p className="text-muted-foreground">{t("wallet.opening.creating")}</p>
             </div>
          )}

              {/* Success State */}
          {status === "SUCCESS" && (
            <div className="space-y-4">
              <div className="p-4 rounded-lg bg-green-500/10 border border-green-500/20">
                <div className="flex items-center gap-2 text-green-500 mb-2">
                  <Check className="w-5 h-5" />
                  <span className="font-medium">{t("wallet.opening.activated")}</span>
                </div>
                <p className="text-sm text-muted-foreground">
                  {t("wallet.opening.addressLabel")}
                </p>
              </div>

              {/* Network Display */}
              {availableNetworks.length > 0 && (
                <>
                  {availableNetworks.length > 1 ? (
                    <Tabs value={selectedNetwork} onValueChange={setSelectedNetwork}>
                      <TabsList
                        className="grid w-full"
                        style={{
                          gridTemplateColumns: `repeat(${availableNetworks.length}, minmax(0, 1fr))`,
                        }}
                      >
                        {availableNetworks.map((network) => (
                          <TabsTrigger
                            key={network.value}
                            value={network.value}
                            className="flex items-center gap-1"
                          >
                            <Globe className="w-3 h-3" />
                            {network.label}
                          </TabsTrigger>
                        ))}
                      </TabsList>
                    </Tabs>
                  ) : (
                    <div className="flex items-center gap-2 px-3 py-2 rounded-md bg-secondary/50 border border-border">
                      <Globe className="w-4 h-4 text-muted-foreground" />
                      <span className="text-sm font-medium">
                        {availableNetworks[0].label}
                      </span>
                    </div>
                  )}
                </>
              )}

              {/* Address Display */}
              <div className="p-4 rounded-lg bg-secondary/50 border border-border">
                <p className="text-xs text-muted-foreground mb-1">
                  {t("wallet.opening.walletId")}: {walletId}
                </p>
                <p className="text-xs text-muted-foreground mb-2">
                  {t("wallet.opening.currency")}: {formatCurrency(walletInfo?.currency || "")}
                </p>
                <div className="flex items-center gap-2">
                  <code className="flex-1 text-sm font-mono break-all bg-background p-3 rounded-md">
                    {displayAddressValue}
                  </code>
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => copyToClipboard(displayAddressValue)}
                    className="shrink-0"
                  >
                    {copied ? (
                      <Check className="w-4 h-4 text-green-500" />
                    ) : (
                      <Copy className="w-4 h-4" />
                    )}
                  </Button>
                </div>
              </div>

              <p className="text-xs text-muted-foreground text-center">
                {t("wallet.opening.depositHint")}
              </p>

              {/* Add New Address Button */}
              <Button
                variant="outline"
                className="w-full"
                onClick={() => setShowAddAddressDialog(true)}
              >
                <Plus className="w-4 h-4 mr-2" />
                {t("wallet.opening.addNewAddress")}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Info Cards */}
      <div className="grid gap-4 md:grid-cols-3">
        <Card className="bg-card/30">
          <CardContent className="pt-6">
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-4">
              <Shield className="w-5 h-5 text-primary" />
            </div>
            <h3 className="font-medium mb-2">{t("wallet.opening.feature1Title")}</h3>
            <p className="text-sm text-muted-foreground">
              {t("wallet.opening.feature1Description")}
            </p>
          </CardContent>
        </Card>

        <Card className="bg-card/30">
          <CardContent className="pt-6">
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-4">
              <Wallet className="w-5 h-5 text-primary" />
            </div>
            <h3 className="font-medium mb-2">{t("wallet.opening.feature2Title")}</h3>
            <p className="text-sm text-muted-foreground">
              {t("wallet.opening.feature2Description")}
            </p>
          </CardContent>
        </Card>

        <Card className="bg-card/30">
          <CardContent className="pt-6">
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-4">
              <Check className="w-5 h-5 text-primary" />
            </div>
            <h3 className="font-medium mb-2">{t("wallet.opening.feature3Title")}</h3>
            <p className="text-sm text-muted-foreground">
              {t("wallet.opening.feature3Description")}
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default AccountOpening;

import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Copy, AlertTriangle, ExternalLink, RefreshCw } from "lucide-react";
import QRCode from "qrcode";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { CryptoIcon } from "@/components/ui/crypto-icon";
import {
  WalletService,
  type DepositCoin,
  type DepositCoinNetwork,
  type DepositRecord,
} from "@/lib/wallet-service";

const ALLOWED_EXPLORER_ORIGINS = new Set([
  "https://etherscan.io",
  "https://bscscan.com",
  "https://tronscan.org",
]);

function safeExplorerUrl(
  base: string,
  ...segments: string[]
): string | null {
  try {
    const url = new URL(base);
    if (!ALLOWED_EXPLORER_ORIGINS.has(url.origin)) return null;
    const path = segments.map((s) => encodeURIComponent(s)).join("/");
    return `${url.origin}${url.pathname.replace(/\/$/, "")}/${path}`;
  } catch {
    return null;
  }
}

const STEPS = ["selectCoin", "selectNetwork", "depositAddress"] as const;

function StepIndicator({ current }: { current: number }) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 mb-6">
      {STEPS.map((key, i) => {
        const step = i + 1;
        const active = step <= current;
        return (
          <div key={key} className="flex items-center gap-2">
            {i > 0 && (
              <div
                className={`h-px w-6 ${active ? "bg-primary" : "bg-muted-foreground/30"}`}
              />
            )}
            <div
              data-testid={`step-${step}`}
              className={`flex h-7 w-7 items-center justify-center rounded-md text-xs font-semibold transition-colors ${
                active
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground"
              }`}
            >
              {step}
            </div>
            <span
              className={`text-sm ${active ? "text-foreground font-medium" : "text-muted-foreground"}`}
            >
              {t(`deposit.steps.${key}`)}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function CoinSelector({
  coins,
  selected,
  onSelect,
}: {
  coins: DepositCoin[];
  selected: DepositCoin | null;
  onSelect: (coin: DepositCoin) => void;
}) {
  const { t } = useTranslation();

  if (coins.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("deposit.coinSelector.empty")}
      </p>
    );
  }

  return (
    <div>
      <p className="text-sm text-muted-foreground mb-3">
        {t("deposit.coinSelector.title")}
      </p>
      <div className="flex flex-wrap gap-2">
        {coins.map((coin) => (
          <button
            key={coin.symbol}
            data-testid={`coin-chip-${coin.symbol}`}
            onClick={() => onSelect(coin)}
            className={`flex items-center gap-2 rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
              selected?.symbol === coin.symbol
                ? "border-primary bg-primary/10 text-primary"
                : "border-border bg-background hover:bg-muted"
            }`}
          >
            <CryptoIcon currency={coin.symbol} size={20} />
            {coin.symbol}
          </button>
        ))}
      </div>
    </div>
  );
}

function NetworkSelector({
  coin,
  selected,
  onSelect,
}: {
  coin: DepositCoin;
  selected: DepositCoinNetwork | null;
  onSelect: (net: DepositCoinNetwork) => void;
}) {
  const { t } = useTranslation();

  if (coin.networks.length === 1) {
    const net = coin.networks[0];
    return (
      <div>
        <p className="text-sm text-muted-foreground mb-3">
          {t("deposit.networkSelector.title")}
        </p>
        <div
          data-testid="network-badge"
          className="inline-flex items-center gap-2 rounded-md border bg-muted/40 px-4 py-2 text-sm font-medium"
        >
          {net.shortName} ({net.tokenStandard})
        </div>
      </div>
    );
  }

  return (
    <div>
      <p className="text-sm text-muted-foreground mb-3">
        {t("deposit.networkSelector.title")}
      </p>
      <Select
        value={selected?.chainCode ?? ""}
        onValueChange={(val) => {
          const net = coin.networks.find((n) => n.chainCode === val);
          if (net) onSelect(net);
        }}
      >
        <SelectTrigger className="w-full" data-testid="network-select">
          <SelectValue placeholder={t("deposit.networkSelector.placeholder")} />
        </SelectTrigger>
        <SelectContent>
          {coin.networks.map((net) => (
            <SelectItem key={net.chainCode} value={net.chainCode}>
              {net.shortName} ({net.tokenStandard})
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selected && (
        <Alert variant="destructive" className="mt-3">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>
            {t("deposit.networkSelector.warning", {
              symbol: coin.symbol,
              network: selected.shortName,
            })}
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}

function AddressDisplay({
  network,
  coin,
}: {
  network: DepositCoinNetwork;
  coin: DepositCoin;
}) {
  const { t } = useTranslation();

  const {
    data,
    isLoading,
    error,
    refetch,
  } = useQuery({
    queryKey: ["deposit-address", network.networkFamily],
    queryFn: () =>
      WalletService.getDepositAddress(network.networkFamily),
    staleTime: 5 * 60_000,
    retry: false,
  });

  const [qrDataUrl, setQrDataUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!data?.address) {
      setQrDataUrl(null);
      return;
    }
    let cancelled = false;
    QRCode.toDataURL(data.address, { width: 160, margin: 1 })
      .then((url) => {
        if (!cancelled) setQrDataUrl(url);
      })
      .catch(() => {
        if (!cancelled) setQrDataUrl(null);
      });
    return () => {
      cancelled = true;
    };
  }, [data?.address]);

  const handleCopy = async () => {
    if (!data?.address) return;
    try {
      await navigator.clipboard.writeText(data.address);
      toast.success(t("deposit.addressCard.copied"));
    } catch {
      toast.error(t("deposit.addressCard.copyFailed"));
    }
  };

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <Card className="border-destructive/30">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-destructive text-base">
            <AlertTriangle className="h-5 w-5" />
            {t("deposit.addressCard.errorTitle")}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Button
            variant="outline"
            size="sm"
            onClick={() => refetch()}
            data-testid="retry-address"
          >
            <RefreshCw className="h-4 w-4 mr-1" />
            {t("deposit.addressCard.retry")}
          </Button>
        </CardContent>
      </Card>
    );
  }

  if (!data) return null;

  const contractLast4 = network.tokenContract
    ? network.tokenContract.slice(-4)
    : null;

  const contractUrl = contractLast4
    ? safeExplorerUrl(network.explorerUrl, "token", network.tokenContract!)
    : null;

  return (
    <div className="space-y-4">
      <div className="flex flex-col sm:flex-row items-start gap-4">
        {qrDataUrl && (
          <img
            data-testid="deposit-qr"
            src={qrDataUrl}
            alt={t("deposit.addressCard.qrAlt")}
            width={160}
            height={160}
            className="rounded-md border bg-white p-2 shrink-0"
          />
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-3 font-mono text-sm break-all">
            <span data-testid="deposit-address" className="flex-1">
              {data.address}
            </span>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleCopy}
              aria-label={t("deposit.addressCard.copy")}
            >
              <Copy className="h-4 w-4" />
            </Button>
          </div>

          <div className="mt-4 space-y-2 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">
                {t("deposit.details.minDeposit")}
              </span>
              <span className="font-medium">
                {network.minDeposit} {coin.symbol}
              </span>
            </div>
            {network.requiredConfirmations > 0 && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">
                  {t("deposit.details.confirmations")}
                </span>
                <span className="font-medium">
                  {t("deposit.details.confirmationsValue", {
                    count: network.requiredConfirmations,
                  })}
                </span>
              </div>
            )}
            {network.estimatedArrivalMinutes > 0 && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">
                  {t("deposit.details.arrivalTime")}
                </span>
                <span className="font-medium">
                  {t("deposit.details.arrivalTimeValue", {
                    minutes: network.estimatedArrivalMinutes,
                  })}
                </span>
              </div>
            )}
            {contractLast4 && (
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">
                  {t("deposit.details.contractAddress")}
                </span>
                <span className="flex items-center gap-1 font-mono font-medium">
                  {contractLast4}
                  {contractUrl && (
                    <a
                      href={contractUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-primary hover:underline inline-flex items-center gap-0.5"
                      data-testid="contract-link"
                    >
                      <ExternalLink className="h-3 w-3" />
                    </a>
                  )}
                </span>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function RecentDeposits({
  explorerUrlMap,
}: {
  explorerUrlMap: Map<string, string>;
}) {
  const { t } = useTranslation();

  const { data } = useQuery({
    queryKey: ["recent-deposits"],
    queryFn: () => WalletService.getRecentDeposits().catch(() => []),
    staleTime: 60_000,
  });

  const deposits: DepositRecord[] = data ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("deposit.recent.title")}</CardTitle>
      </CardHeader>
      <CardContent>
        {deposits.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {t("deposit.recent.empty")}
          </p>
        ) : (
          <div className="space-y-3">
            {deposits.map((d, i) => {
              const txUrl =
                d.txHash && d.chainCode && explorerUrlMap.has(d.chainCode)
                  ? safeExplorerUrl(explorerUrlMap.get(d.chainCode)!, "tx", d.txHash)
                  : null;
              return (
                <div
                  key={d.id ?? i}
                  className="flex items-center justify-between text-sm"
                >
                  <div className="flex items-center gap-2">
                    <CryptoIcon currency={d.currency} size={16} />
                    <span className="font-medium">
                      {d.amount} {d.currency}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-muted-foreground">
                      {t(`deposit.status.${d.status}`, d.status)}
                    </span>
                    {txUrl && (
                      <a
                        href={txUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-primary hover:underline"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </a>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default function Deposit() {
  const { t } = useTranslation();

  const {
    data: coinsData,
    isLoading,
    error,
  } = useQuery({
    queryKey: ["deposit-coins"],
    queryFn: () => WalletService.getDepositCoins(),
    staleTime: 5 * 60_000,
  });

  const [selectedCoin, setSelectedCoin] = useState<DepositCoin | null>(null);
  const [selectedNetwork, setSelectedNetwork] =
    useState<DepositCoinNetwork | null>(null);

  const currentStep = selectedNetwork ? 3 : selectedCoin ? 2 : 1;

  const handleSelectCoin = (coin: DepositCoin) => {
    setSelectedCoin(coin);
    setSelectedNetwork(coin.networks.length === 1 ? coin.networks[0] : null);
  };

  const explorerUrlMap = useMemo(() => {
    const m = new Map<string, string>();
    if (coinsData) {
      for (const coin of coinsData.coins) {
        for (const net of coin.networks) {
          if (net.explorerUrl) {
            m.set(net.chainCode, net.explorerUrl);
          }
        }
      }
    }
    return m;
  }, [coinsData]);

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">
          {t("deposit.title")}
        </h1>
        <p className="text-muted-foreground mt-2">{t("deposit.description")}</p>
      </div>

      <div className="flex flex-col lg:flex-row gap-6">
        <div className="flex-1 min-w-0">
          <StepIndicator current={currentStep} />

          {isLoading && (
            <div className="space-y-3">
              <Skeleton className="h-10 w-full" />
              <div className="flex gap-2">
                {[1, 2, 3].map((i) => (
                  <Skeleton key={i} className="h-10 w-20 rounded-full" />
                ))}
              </div>
            </div>
          )}

          {error && (
            <Card className="border-destructive/30">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-destructive text-base">
                  <AlertTriangle className="h-5 w-5" />
                  {t("deposit.coinsLoadError")}
                </CardTitle>
              </CardHeader>
            </Card>
          )}

          {coinsData && (
            <div className="space-y-6">
              <CoinSelector
                coins={coinsData.coins}
                selected={selectedCoin}
                onSelect={handleSelectCoin}
              />

              {selectedCoin && (
                <NetworkSelector
                  coin={selectedCoin}
                  selected={selectedNetwork}
                  onSelect={setSelectedNetwork}
                />
              )}

              {selectedCoin && selectedNetwork && (
                <AddressDisplay
                  network={selectedNetwork}
                  coin={selectedCoin}
                />
              )}
            </div>
          )}
        </div>

        <div className="w-full lg:w-80 shrink-0">
          <RecentDeposits explorerUrlMap={explorerUrlMap} />
        </div>
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Copy, AlertTriangle } from "lucide-react";
import QRCode from "qrcode";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  WalletService,
  type NetworkFamily,
  type SupportedCoin,
} from "@/lib/wallet-service";

const NETWORK_FAMILIES: NetworkFamily[] = ["EVM", "TRON"];

function DepositAddressCard({ networkFamily }: { networkFamily: NetworkFamily }) {
  const { t } = useTranslation();

  const { data, isLoading, error } = useQuery({
    queryKey: ["deposit-address", networkFamily],
    queryFn: () => WalletService.getDepositAddress(networkFamily),
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
          <CardTitle className="flex items-center gap-2 text-destructive">
            <AlertTriangle className="h-5 w-5" />
            {t("deposit.addressCard.errorTitle")}
          </CardTitle>
          <CardDescription>
            {error instanceof Error ? error.message : t("common.error")}
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  if (!data) return null;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{t("deposit.addressCard.label")}</CardTitle>
          <CardDescription>{t("deposit.addressCard.hint")}</CardDescription>
        </CardHeader>
        <CardContent>
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
          {qrDataUrl && (
            <div className="mt-4 flex justify-center">
              <img
                data-testid="deposit-qr"
                src={qrDataUrl}
                alt={t("deposit.addressCard.qrAlt")}
                width={160}
                height={160}
                className="rounded-md border bg-white p-2"
              />
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("deposit.supportedCoins.title")}</CardTitle>
        </CardHeader>
        <CardContent>
          <SupportedCoinsTable coins={data.supportedCoins} />
        </CardContent>
      </Card>
    </div>
  );
}

function SupportedCoinsTable({ coins }: { coins: SupportedCoin[] }) {
  const { t } = useTranslation();

  if (coins.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("deposit.supportedCoins.empty")}
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t("deposit.supportedCoins.chain")}</TableHead>
          <TableHead>{t("deposit.supportedCoins.coin")}</TableHead>
          <TableHead className="text-right">
            {t("deposit.supportedCoins.minDeposit")}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {coins.map((coin) => (
          <TableRow key={`${coin.chainCode}-${coin.coinKey}`}>
            <TableCell>{coin.chainCode}</TableCell>
            <TableCell>{coin.symbol}</TableCell>
            <TableCell className="text-right font-mono">{coin.minDeposit}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export default function Deposit() {
  const { t } = useTranslation();
  const [active, setActive] = useState<NetworkFamily>("EVM");

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">{t("deposit.title")}</h1>
        <p className="text-muted-foreground mt-2">{t("deposit.description")}</p>
      </div>

      <Tabs
        value={active}
        onValueChange={(v) => setActive(v as NetworkFamily)}
        className="w-full"
      >
        <TabsList>
          {NETWORK_FAMILIES.map((family) => (
            <TabsTrigger key={family} value={family}>
              {t(`deposit.tabs.${family.toLowerCase()}`)}
            </TabsTrigger>
          ))}
        </TabsList>
        {NETWORK_FAMILIES.map((family) => (
          <TabsContent key={family} value={family} className="mt-6">
            <DepositAddressCard networkFamily={family} />
          </TabsContent>
        ))}
      </Tabs>
    </div>
  );
}


import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { PieChart, Pie, Cell, ResponsiveContainer, Tooltip, Legend } from 'recharts';
import { ArrowRight, Wallet, Info, RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

interface Asset {
  currency: string;
  total: string;
  available: string;
  frozenBalance: string;
  investedBalance: string;
  usdValue: number;
}

interface ChartData {
  name: string;
  value: number;
  color: string;
}

const CURRENCY_COLORS: Record<string, string> = {
  BTC: '#F7931A',
  ETH: '#627EEA',
  USDT: '#26A17B',
  USDC: '#2775CA',
  SOL: '#14F195',
  ADA: '#0033AD',
  XRP: '#23292F',
  DOGE: '#C2A633',
};

const Assets = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [assets, setAssets] = useState<Asset[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [chartData, setChartData] = useState<ChartData[]>([]);

  const fetchAssets = async () => {
    try {
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }

      const res = await fetch("/api/assets", {
        headers: { Authorization: `Bearer ${token}` }
      });

      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }

      if (res.ok) {
        const data = await res.json();
        const assetList = Array.isArray(data.assets) ? data.assets : [];
        setAssets(assetList);

        // Generate chart data based on USD value
        const chartItems: ChartData[] = assetList
          .filter((a: Asset) => (a.usdValue || 0) > 0)
          .map((a: Asset) => ({
            name: a.currency,
            value: a.usdValue || 0,
            color: CURRENCY_COLORS[a.currency] || '#888888',
          }));
        setChartData(chartItems);
      }
    } catch (error) {
      console.error("Failed to fetch assets", error);
      toast.error(t("auth.errors.networkError"));
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    fetchAssets();
  }, []);

  const formatNumber = (num: string | number): string => {
    if (typeof num === 'string') {
      const parsed = parseFloat(num);
      if (isNaN(parsed)) return num;
      num = parsed;
    }
    const formatted = new Intl.NumberFormat("en-US", {
      minimumFractionDigits: 0,
      maximumFractionDigits: 7,
    }).format(num);
    
    return formatted.replace(/(\.\d*?[1-9])0+$/g, '$1').replace(/\.$/, '');
  };

  const totalValue = assets.reduce((sum, a) => sum + (a.usdValue || 0), 0);

  if (isLoading) {
    return (
      <div className="space-y-8 animate-fade-in">
        <div className="flex items-center justify-between">
          <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.nav.assets")}</h1>
        </div>
        <div className="flex items-center justify-center h-64">
          <RefreshCw size={32} className="animate-spin text-muted-foreground" />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-8 animate-fade-in">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.nav.assets")}</h1>
          <p className="text-muted-foreground">{t("dashboard.assets.pageDesc")}</p>
        </div>
        <Button variant="outline" size="sm" onClick={fetchAssets} className="gap-1">
          <RefreshCw size={14} />
          {t("common.refresh")}
        </Button>
      </div>

      {assets.length === 0 ? (
        <Card className="bg-card/50 border-border/50">
          <CardContent className="pt-6">
            <div className="flex flex-col items-center justify-center h-64 gap-4 text-muted-foreground">
              <Wallet size={48} className="text-muted-foreground/50" />
              <div className="text-center">
                <p className="text-lg font-medium">{t("dashboard.assets.noAssets")}</p>
                <p className="text-sm">{t("dashboard.assets.noAssetsDesc")}</p>
              </div>
              <Button asChild>
                <Link to="/dashboard/deposit">{t("dashboard.assets.goDeposit")}</Link>
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {/* Distribution Chart */}
          <Card className="lg:col-span-1 bg-card/50 border-border/50">
            <CardHeader>
              <CardTitle className="text-base font-semibold">{t("dashboard.overview.assetDistribution")}</CardTitle>
              <CardDescription>
                {t("dashboard.assets.totalValue")}: ${formatNumber(totalValue)}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="h-[300px] w-full">
                {chartData.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie
                        data={chartData}
                        cx="50%"
                        cy="50%"
                        innerRadius={60}
                        outerRadius={80}
                        paddingAngle={5}
                        dataKey="value"
                      >
                        {chartData.map((entry, index) => (
                          <Cell key={`cell-${index}`} fill={entry.color} />
                        ))}
                      </Pie>
                      <Tooltip
                        formatter={(value: number, name: string) => [`$${formatNumber(value)} ${t("dashboard.assets.currencyUnit")}`, name]}
                        contentStyle={{ backgroundColor: 'hsl(var(--card))', borderColor: 'hsl(var(--border))', color: 'white' }}
                        labelStyle={{ color: 'white' }}
                        itemStyle={{ color: 'white' }}
                      />
                      <Legend verticalAlign="bottom" height={36} />
                    </PieChart>
                  </ResponsiveContainer>
                ) : (
                  <div className="flex items-center justify-center h-full text-muted-foreground">
                    <Info size={24} />
                  </div>
                )}
              </div>
            </CardContent>
          </Card>

          {/* Detailed List */}
          <Card className="lg:col-span-2 bg-card/50 border-border/50">
            <CardHeader>
              <CardTitle className="text-base font-semibold">{t("dashboard.assets.assetBreakdown")}</CardTitle>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent border-border/50">
                    <TableHead className="text-muted-foreground">{t("dashboard.assets.currency")}</TableHead>
                    <TableHead className="text-muted-foreground">{t("dashboard.assets.total")}</TableHead>
                    <TableHead className="text-muted-foreground">{t("dashboard.assets.invested")}</TableHead>
                    <TableHead className="text-muted-foreground">{t("dashboard.assets.frozen")}</TableHead>
                    <TableHead className="text-muted-foreground">{t("dashboard.assets.available")}</TableHead>
                    <TableHead className="text-muted-foreground text-right">{t("dashboard.assets.valueUSD")}</TableHead>
                    <TableHead className="text-muted-foreground text-right">{t("dashboard.assets.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {assets.map((asset) => (
                    <TableRow key={asset.currency} className="border-border/50 hover:bg-secondary/30 transition-colors">
                      <TableCell className="font-medium">
                        <div className="flex items-center gap-2">
                          <div
                            className="w-3 h-3 rounded-full"
                            style={{ backgroundColor: CURRENCY_COLORS[asset.currency] || '#888888' }}
                          />
                          {asset.currency}
                        </div>
                      </TableCell>
                      <TableCell className="font-mono">{formatNumber(asset.total)}</TableCell>
                      <TableCell className="font-mono text-blue-400">{formatNumber(asset.investedBalance || '0')}</TableCell>
                      <TableCell className="font-mono text-orange-400">{formatNumber(asset.frozenBalance)}</TableCell>
                      <TableCell className="font-mono text-green-400">{formatNumber(asset.available)}</TableCell>
                      <TableCell className="text-right font-semibold font-mono">
                        ${formatNumber(asset.usdValue)}
                      </TableCell>
                      <TableCell className="text-right">
                        {parseFloat(asset.available.replace(/,/g, '')) > 0 && (
                          <Button asChild size="sm" variant="outline" className="gap-1">
                            <Link to={`/dashboard/withdraw?asset=${asset.currency}`}>
                              <ArrowRight size={14} />
                              {t("dashboard.assets.withdraw")}
                            </Link>
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
};

export default Assets;

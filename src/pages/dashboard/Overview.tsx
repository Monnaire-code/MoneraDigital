import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { 
  XAxis, 
  YAxis, 
  CartesianGrid, 
  Tooltip, 
  ResponsiveContainer,
  AreaChart,
  Area
} from 'recharts';
import { Wallet, DollarSign, Coins, RefreshCw } from "lucide-react";
import { cn } from "@/lib/utils";
import { CryptoIcon } from "@/components/ui/crypto-icon";

interface Asset {
  currency: string;
  total: string;
  available: string;
  frozenBalance: string;
  investedBalance: string;
  usdValue: number;
}

interface DailyInterest {
  date: string;
  amount: number;
  currency: string;
}

interface PriceData {
  symbol: string;
  price: number;
  change24h: number;
}

const Overview = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [assets, setAssets] = useState<Asset[]>([]);
  const [interestHistory, setInterestHistory] = useState<DailyInterest[]>([]);
  const [prices, setPrices] = useState<PriceData[]>([]);
  const [loading, setLoading] = useState(true);
  const [lastUpdate, setLastUpdate] = useState<string>("");

  const totalAssets = assets.reduce((sum, a) => sum + a.usdValue, 0);
  const totalInvested = assets.reduce((sum, a) => {
    const invested = parseFloat(a.investedBalance) || 0;
    if (a.currency === "USDT" || a.currency === "USDC" || a.currency === "DAI") {
      return sum + invested;
    }
    const price = prices.find(p => p.symbol === a.currency)?.price || 1;
    return sum + (invested * price);
  }, 0);
  const totalInterest = interestHistory.reduce((sum, i) => {
    const isUSD = i.currency === "USDT" || i.currency === "USDC" || i.currency === "DAI" || i.currency === "USD";
    const price = isUSD ? 1 : (prices.find(p => p.symbol === i.currency)?.price || 1);
    return sum + (i.amount * price);
  }, 0);

  useEffect(() => {
    const fetchData = async () => {
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }

      const headers = { Authorization: `Bearer ${token}` };

      setLoading(true);
      try {
        const [assetsRes, historyRes, pricesRes] = await Promise.all([
          fetch("/api/assets", { headers }),
          fetch("/api/wealth/interest-history?days=7", { headers }),
          fetch("/api/assets/prices", { headers }),
        ]);

        if (assetsRes.status === 401 || assetsRes.status === 403) {
          localStorage.removeItem("token");
          localStorage.removeItem("user");
          navigate("/login");
          return;
        }

        if (assetsRes.ok) {
          const assetsData = await assetsRes.json();
          setAssets(assetsData.assets || []);
        }

        if (historyRes.ok) {
          const historyData = await historyRes.json();
          setInterestHistory(historyData.history || []);
        }

        if (pricesRes.ok) {
          const pricesData = await pricesRes.json();
          setPrices(pricesData.prices || []);
          if (pricesData.updatedAt) {
            const date = new Date(pricesData.updatedAt);
            date.setHours(date.getHours() + 8);
            setLastUpdate(date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }));
          }
        }
      } catch (error) {
        console.error("Failed to fetch overview data:", error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
    const interval = setInterval(fetchData, 60000);
    return () => clearInterval(interval);
  }, [navigate]);

  const getChartData = () => {
    const today = new Date();
    const dates: string[] = [];
    
    for (let i = -6; i <= 0; i++) {
      const date = new Date(today);
      date.setDate(date.getDate() + i);
      const year = date.getFullYear();
      const month = String(date.getMonth() + 1).padStart(2, '0');
      const day = String(date.getDate()).padStart(2, '0');
      dates.push(`${year}-${month}-${day}`);
    }
    
    const dateAmounts = new Map<string, number>();
    dates.forEach(d => dateAmounts.set(d, 0));
    
    interestHistory.forEach(item => {
      const existing = dateAmounts.get(item.date) || 0;
      dateAmounts.set(item.date, existing + item.amount);
    });
    
    return dates.map(date => ({
      name: date.slice(5),
      interest: dateAmounts.get(date) || 0,
    }));
  };
  
  const chartData = getChartData();

  const stats = [
    { 
      label: t("dashboard.overview.totalAssets"), 
      value: `$${totalAssets.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
      icon: Wallet,
      color: "text-blue-500"
    },
    { 
      label: t("dashboard.overview.productValue"), 
      value: `$${totalInvested.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
      icon: Coins,
      color: "text-purple-500"
    },
    { 
      label: t("dashboard.overview.totalInterest"), 
      value: `$${totalInterest.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
      icon: DollarSign,
      color: "text-green-500"
    },
  ];

  const cryptoDisplay = ["BTC", "ETH", "USDT", "USDC"].map(symbol => {
    const priceData = prices.find(p => p.symbol === symbol);
    const asset = assets.find(a => a.currency === symbol);
    return {
      symbol,
      price: priceData?.price || (symbol === "USDT" || symbol === "USDC" ? 1 : 0),
      change: priceData?.change24h || 0,
      holdings: asset ? parseFloat(asset.total) || 0 : 0,
    };
  }).filter(c => c.price > 0);

  if (loading && assets.length === 0) {
    return (
      <div className="space-y-8 animate-fade-in">
        <div className="flex items-center justify-between">
          <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.nav.overview")}</h1>
        </div>
        <div className="flex items-center justify-center h-64">
          <RefreshCw className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-8 animate-fade-in">
      <div className="flex items-center justify-between">
        <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.nav.overview")}</h1>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {stats.map((stat) => (
          <Card key={stat.label} className="bg-card/50 border-border/50">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium text-muted-foreground">
                {stat.label}
              </CardTitle>
              <stat.icon size={16} className={stat.color} />
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-bold">{stat.value}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-7">
        <Card className="col-span-4 bg-card/50 border-border/50">
          <CardHeader>
            <CardTitle className="text-base font-semibold">
              {t("dashboard.overview.dailyInterest")} (USD)
            </CardTitle>
          </CardHeader>
          <CardContent className="pl-2">
            <div className="h-[300px] w-full">
              {chartData.length > 0 ? (
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={chartData}>
                    <defs>
                      <linearGradient id="colorInterest" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="#10b981" stopOpacity={0.4}/>
                        <stop offset="95%" stopColor="#10b981" stopOpacity={0.05}/>
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="hsl(var(--border))" />
                    <XAxis 
                      dataKey="name" 
                      stroke="hsl(var(--muted-foreground))" 
                      fontSize={12}
                      tickLine={false}
                      axisLine={false}
                    />
                    <YAxis 
                      stroke="hsl(var(--muted-foreground))" 
                      fontSize={12}
                      tickLine={false}
                      axisLine={false}
                      tickFormatter={(value) => `$${value.toFixed(2)}`}
                    />
                    <Tooltip 
                      contentStyle={{ 
                        backgroundColor: 'hsl(var(--card))', 
                        borderColor: 'hsl(var(--border))',
                        color: 'hsl(var(--foreground))'
                      }}
                      itemStyle={{ color: '#10b981' }}
                      formatter={(value: number) => [`$${value.toFixed(2)}`, 'Interest (USD)']}
                    />
                    <Area 
                      type="monotone" 
                      dataKey="interest" 
                      stroke="#10b981" 
                      strokeWidth={2}
                      fillOpacity={1} 
                      fill="url(#colorInterest)" 
                      dot={{ r: 5, fill: "#10b981", strokeWidth: 2, stroke: "hsl(var(--card))" }}
                      activeDot={{ r: 7, fill: "#10b981", strokeWidth: 2, stroke: "hsl(var(--card))" }}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              ) : (
                <div className="flex items-center justify-center h-full text-muted-foreground">
                  No interest data available yet
                </div>
              )}
            </div>
          </CardContent>
        </Card>

        <Card className="col-span-3 bg-card/50 border-border/50">
          <CardHeader>
            <CardTitle className="text-base font-semibold">
              {t("dashboard.overview.cryptoPrices")}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-4">
              {cryptoDisplay.map((crypto) => (
                <div key={crypto.symbol} className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <CryptoIcon currency={crypto.symbol} size={24} />
                    <p className="text-sm font-medium">{crypto.symbol}</p>
                  </div>
                  <div className="text-right">
                    <p className="text-sm font-medium">
                      {crypto.symbol === "USDT" || crypto.symbol === "USDC"
                        ? `$${crypto.price.toFixed(2)}`
                        : `$${crypto.price.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`}
                    </p>
                    <p className={cn(
                      "text-xs",
                      crypto.change >= 0 ? "text-green-500" : "text-red-500"
                    )}>
                      {crypto.change >= 0 ? "+" : ""}{crypto.change.toFixed(2)}%
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default Overview;

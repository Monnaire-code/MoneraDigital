import { useTranslation } from "react-i18next";
import { Bar, BarChart, Cell, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useFundStats } from "@/hooks/use-fund-stats";
import { ChartContainer, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart";
import { formatUsdShort, formatUsdFull, formatMonthShort } from "@/lib/locale-format";

const PIE_COLORS = [
  "hsl(var(--primary))",
  "hsl(var(--primary) / 0.7)",
  "hsl(var(--primary) / 0.45)",
  "hsl(var(--primary) / 0.25)",
];

const FundPerformance = () => {
  const { t, i18n } = useTranslation();
  const { status, data, error } = useFundStats();
  const lang = i18n.language;

  if (status === "loading") {
    return (
      <section className="py-20 relative">
        <div className="container mx-auto px-6">
          <div className="text-center max-w-2xl mx-auto mb-12">
            <span className="text-primary text-sm font-medium tracking-wider uppercase mb-4 block">
              {t("fund.sectionLabel")}
            </span>
            <h2 className="text-3xl sm:text-4xl font-bold text-foreground mb-4">
              {t("fund.sectionTitle")}
            </h2>
          </div>
          <div className="h-64 rounded-2xl glass animate-pulse" role="status" aria-busy="true" />
        </div>
      </section>
    );
  }

  if (status === "error" || !data) {
    return (
      <section className="py-20 relative">
        <div className="container mx-auto px-6 text-center text-muted-foreground" role="status">
          {t("fund.errorPrefix")}
          {error ? ` — ${error}` : ""}
        </div>
      </section>
    );
  }

  const trendData = data.trend.map((p) => ({
    month: formatMonthShort(p.month, lang),
    aum: p.aum,
    aumLabel: formatUsdShort(p.aum, lang),
  }));

  const allocationData = data.allocations.map((a) => ({
    name: a.category,
    value: a.amount,
    pct: a.pct,
  }));

  const trendConfig: ChartConfig = {
    aum: { label: "AUM", color: "hsl(var(--primary))" },
  };

  return (
    <section className="py-20 relative">
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-primary/[0.02] to-transparent" />

      <div className="container mx-auto px-6 relative z-10">
        <div className="text-center max-w-2xl mx-auto mb-12">
          <span className="text-primary text-sm font-medium tracking-wider uppercase mb-4 block">
            {t("fund.sectionLabel")}
          </span>
          <h2 className="text-3xl sm:text-4xl font-bold text-foreground mb-4">
            {t("fund.sectionTitle")}
          </h2>
          <p className="text-muted-foreground text-lg">{t("fund.sectionDesc")}</p>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
          <MetricCard
            label={t("fund.currentAum")}
            value={formatUsdShort(data.current.totalAum, lang)}
            sublabel={`${t("fund.heroAsOf", { date: data.current.reportDate })}`}
          />
          <MetricCard
            label={t("fund.actualApy")}
            value={`${(data.current.actualApy * 100).toFixed(2)}%`}
            sublabel={`${t("fund.weightedApy")} ${(data.current.weightedApy * 100).toFixed(2)}%`}
          />
          <MetricCard
            label={t("fund.monthGrowth")}
            value={`+${(data.current.monthGrowth * 100).toFixed(2)}%`}
            sublabel={`${t("fund.newCapital")} ${formatUsdShort(data.current.newCapital, lang)}`}
          />
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
          <div className="lg:col-span-3 glass rounded-2xl p-6">
            <h3 id="fund-trend-title" className="text-lg font-semibold text-foreground mb-4">
              {t("fund.trendTitle")}
            </h3>
            <ChartContainer config={trendConfig} className="h-64 w-full" aria-labelledby="fund-trend-title">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={trendData} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
                  <XAxis
                    dataKey="month"
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
                    tickFormatter={(v: number) => formatUsdShort(v, lang)}
                    width={64}
                  />
                  <Tooltip
                    cursor={{ fill: "hsl(var(--muted) / 0.3)" }}
                    content={
                      <ChartTooltipContent
                        formatter={(value) => formatUsdFull(Number(value), lang)}
                      />
                    }
                  />
                  <Bar dataKey="aum" fill="var(--color-aum)" radius={[6, 6, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </ChartContainer>
            <table className="sr-only" aria-label={t("fund.trendTitle")}>
              <thead>
                <tr>
                  <th>Month</th>
                  <th>AUM</th>
                </tr>
              </thead>
              <tbody>
                {trendData.map((p) => (
                  <tr key={p.month}>
                    <td>{p.month}</td>
                    <td>{formatUsdFull(p.aum, lang)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="lg:col-span-2 glass rounded-2xl p-6">
            <h3 id="fund-alloc-title" className="text-lg font-semibold text-foreground mb-4">
              {t("fund.allocationTitle")}
            </h3>
            <ResponsiveContainer width="100%" height={192} aria-labelledby="fund-alloc-title">
              <PieChart>
                <Pie
                  data={allocationData}
                  dataKey="value"
                  nameKey="name"
                  innerRadius={50}
                  outerRadius={80}
                  paddingAngle={2}
                >
                  {allocationData.map((_, idx) => (
                    <Cell key={idx} fill={PIE_COLORS[idx % PIE_COLORS.length]} />
                  ))}
                </Pie>
                <Tooltip
                  formatter={(value: number, _name: string, item) =>
                    `${formatUsdShort(value, lang)} (${((item.payload as { pct: number }).pct * 100).toFixed(1)}%)`
                  }
                />
              </PieChart>
            </ResponsiveContainer>
            <ul className="mt-4 space-y-1.5 text-sm">
              {allocationData.map((a, idx) => (
                <li key={a.name} className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-2 text-muted-foreground min-w-0">
                    <span
                      className="w-2.5 h-2.5 rounded-full shrink-0"
                      style={{ backgroundColor: PIE_COLORS[idx % PIE_COLORS.length] }}
                      aria-hidden="true"
                    />
                    <span className="truncate">{a.name}</span>
                  </span>
                  <span className="text-foreground font-medium tabular-nums shrink-0">
                    {(a.pct * 100).toFixed(1)}%
                  </span>
                </li>
              ))}
            </ul>
            <table className="sr-only" aria-label={t("fund.allocationTitle")}>
              <thead>
                <tr>
                  <th>Strategy</th>
                  <th>Amount</th>
                  <th>Share</th>
                </tr>
              </thead>
              <tbody>
                {allocationData.map((a) => (
                  <tr key={a.name}>
                    <td>{a.name}</td>
                    <td>{formatUsdFull(a.value, lang)}</td>
                    <td>{(a.pct * 100).toFixed(1)}%</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <p className="text-center text-xs text-muted-foreground mt-6">
          {t("fund.poweredBy")}
        </p>
      </div>
    </section>
  );
};

interface MetricCardProps {
  label: string;
  value: string;
  sublabel: string;
}

const MetricCard = ({ label, value, sublabel }: MetricCardProps) => (
  <div className="text-center p-6 rounded-2xl glass">
    <div className="text-3xl sm:text-4xl font-bold text-primary mb-1 tabular-nums">{value}</div>
    <div className="text-foreground font-medium mb-1">{label}</div>
    <div className="text-xs text-muted-foreground">{sublabel}</div>
  </div>
);

export default FundPerformance;

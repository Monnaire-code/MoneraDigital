import { useTranslation } from "react-i18next";
import { TrendingUp } from "lucide-react";
import { useFundStats } from "@/hooks/use-fund-stats";
import { formatUsdShort, formatMonthYear } from "@/lib/locale-format";

const TARGET_AUM_USD = 100_000_000;
const AUM_TARGET_LABEL = "$100M";

const HeroAumCard = () => {
  const { t, i18n } = useTranslation();
  const { status, data, error } = useFundStats();
  const lang = i18n.language;

  if (status === "error") {
    return (
      <div className="mt-12 max-w-md mx-auto glass rounded-2xl px-6 py-4 animate-fade-in-delay-3" role="status">
        <p className="text-sm text-muted-foreground">
          {t("fund.errorPrefix")}
          {error ? ` — ${error}` : ""}
        </p>
      </div>
    );
  }

  if (status === "loading" || !data) {
    return (
      <div className="mt-12 max-w-md mx-auto glass rounded-2xl px-6 py-4 animate-fade-in-delay-3" role="status" aria-busy="true">
        <div className="h-3 w-32 mx-auto rounded bg-muted animate-pulse mb-2" />
        <div className="h-7 w-44 mx-auto rounded bg-muted animate-pulse" />
      </div>
    );
  }

  const current = data.current.totalAum;
  const progressPct = Math.min(100, Math.max(0, (current / TARGET_AUM_USD) * 100));
  const progressDeg = (progressPct / 100) * 360;

  return (
    <div className="mt-12 max-w-md mx-auto glass rounded-2xl px-6 py-5 animate-fade-in-delay-3" role="group" aria-label={t("fund.heroLabel")}>
      <p className="text-xs uppercase tracking-wider text-muted-foreground mb-2 text-center">
        {t("fund.heroLabel")}
      </p>
      <div className="flex items-center justify-center gap-4">
        <div
          className="relative w-16 h-16 shrink-0"
          aria-hidden="true"
          style={{
            background: `conic-gradient(hsl(var(--primary)) ${progressDeg}deg, hsl(var(--muted)) ${progressDeg}deg 360deg)`,
            borderRadius: "9999px",
          }}
        >
          <div className="absolute inset-1.5 bg-background rounded-full flex items-center justify-center">
            <span className="text-xs font-semibold text-primary">{progressPct.toFixed(1)}%</span>
          </div>
        </div>
        <div className="text-left">
          <p className="text-2xl sm:text-3xl font-bold text-foreground leading-tight" data-testid="hero-aum-value">
            {formatUsdShort(current, lang)}
          </p>
          <p className="text-xs text-muted-foreground mt-0.5">
            {t("fund.heroProgressLabel", { target: AUM_TARGET_LABEL })}
          </p>
        </div>
      </div>
      <div className="mt-3 flex items-center justify-center gap-2 text-sm">
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-primary/10 text-primary font-medium">
          <TrendingUp className="w-3.5 h-3.5" aria-hidden="true" />
          +{(data.current.monthGrowth * 100).toFixed(2)}%
          <span className="text-xs text-muted-foreground ml-1">
            {lang?.startsWith("zh") ? "本月" : "MoM"}
          </span>
        </span>
        <span className="text-xs text-muted-foreground">
          {t("fund.heroAsOf", { date: formatMonthYear(data.current.reportDate, lang) })}
        </span>
      </div>
    </div>
  );
};

export default HeroAumCard;

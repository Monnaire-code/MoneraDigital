// src/lib/locale-format.ts
// Locale-aware formatters for the public AUM widget.
//
// Currency: always USD ($) on every locale because the public figures
// are USD-denominated; only digit grouping follows locale.
//
// Date: short month + year, formatted by Intl.DateTimeFormat with the
// page's i18n language.

type SupportedLocale = "en" | "zh";

const MONTH_RE = /^(\d{4})-(0[1-9]|1[0-2])$/;

function resolveLocale(lang: string | undefined): SupportedLocale {
  return lang?.startsWith("zh") ? "zh" : "en";
}

export function formatUsdShort(value: number, lang: string | undefined): string {
  if (value >= 1_000_000) {
    return `$${(value / 1_000_000).toFixed(2)}M`;
  }
  if (value >= 1_000) {
    return `$${(value / 1_000).toFixed(0)}K`;
  }
  return `$${value.toFixed(0)}`;
}

export function formatUsdFull(value: number, lang: string | undefined): string {
  // Intl.NumberFormat with style:"currency" appends a locale-specific
  // prefix (e.g. "US$" for zh) to disambiguate from local currencies.
  // We hard-code "$" because all public figures are USD; only the digit
  // grouping separator comes from Intl.
  const locale = resolveLocale(lang);
  const grouping = new Intl.NumberFormat(locale === "zh" ? "en" : locale, {
    maximumFractionDigits: 0,
  }).format(value);
  return `$${grouping}`;
}

export function formatMonthYear(iso: string, lang: string | undefined): string {
  const match = iso.match(MONTH_RE);
  if (!match) return iso;
  const [, year, month] = match;
  const date = new Date(Number(year), Number(month) - 1, 1);
  if (resolveLocale(lang) === "zh") {
    return `${year}年${Number(month)}月`;
  }
  return new Intl.DateTimeFormat("en", { month: "short", year: "numeric" }).format(date);
}

export function formatMonthShort(iso: string, lang: string | undefined): string {
  const match = iso.match(MONTH_RE);
  if (!match) return iso;
  const [, , month] = match;
  const num = Number(month);
  if (resolveLocale(lang) === "zh") {
    return `${num}`;
  }
  return `${num.toString().padStart(2, "0")}`;
}

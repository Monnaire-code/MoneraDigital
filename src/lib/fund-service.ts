import logger from "./logger.js";

export interface FundCurrentMetrics {
  reportDate: string;
  totalAum: number;
  actualApy: number;
  weightedApy: number;
  monthGrowth: number;
  newCapital: number;
}

export interface FundTrendPoint {
  month: string;
  aum: number;
}

export interface FundAllocationItem {
  category: string;
  amount: number;
  pct: number;
}

export interface FundStatsData {
  current: FundCurrentMetrics;
  trend: FundTrendPoint[];
  allocations: FundAllocationItem[];
}

const MONTH_RE = /^\d{4}-(0[1-9]|1[0-2])$/;

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function isNonNegativeFiniteNumber(value: unknown): value is number {
  return isFiniteNumber(value) && value >= 0;
}

function isFundCurrentMetrics(value: unknown): value is FundCurrentMetrics {
  if (value === null || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.reportDate === "string" &&
    MONTH_RE.test(v.reportDate) &&
    isNonNegativeFiniteNumber(v.totalAum) &&
    isFiniteNumber(v.actualApy) &&
    isFiniteNumber(v.weightedApy) &&
    isFiniteNumber(v.monthGrowth) &&
    isNonNegativeFiniteNumber(v.newCapital)
  );
}

function isFundTrendPoint(value: unknown): value is FundTrendPoint {
  if (value === null || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return typeof v.month === "string" && MONTH_RE.test(v.month) && isNonNegativeFiniteNumber(v.aum);
}

function isFundAllocationItem(value: unknown): value is FundAllocationItem {
  if (value === null || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.category === "string" &&
    isNonNegativeFiniteNumber(v.amount) &&
    isFiniteNumber(v.pct) &&
    v.pct >= 0 &&
    v.pct <= 1
  );
}

export function parseFundStats(payload: unknown): FundStatsData {
  if (payload === null || typeof payload !== "object") {
    throw new Error("Invalid fund stats payload: expected an object");
  }
  const root = payload as Record<string, unknown>;

  if (root.success !== true) {
    throw new Error(typeof root.error === "string" ? root.error : "Fund stats request failed");
  }

  const data = root.data as Record<string, unknown> | undefined;
  if (!data) {
    throw new Error("Fund stats payload missing `data`");
  }

  if (!isFundCurrentMetrics(data.current)) {
    throw new Error("Fund stats payload `current` is missing or malformed (check reportDate, totalAum, actualApy, weightedApy, monthGrowth, newCapital)");
  }

  if (!Array.isArray(data.trend) || data.trend.length === 0) {
    throw new Error("Fund stats payload `trend` must be a non-empty array");
  }
  if (!data.trend.every(isFundTrendPoint)) {
    throw new Error("Fund stats payload `trend` entries are malformed (check month format YYYY-MM and aum >= 0)");
  }

  if (!Array.isArray(data.allocations) || data.allocations.length === 0) {
    throw new Error("Fund stats payload `allocations` must be a non-empty array");
  }
  if (!data.allocations.every(isFundAllocationItem)) {
    throw new Error("Fund stats payload `allocations` entries are malformed (check category, amount >= 0, pct in [0,1])");
  }

  return {
    current: data.current,
    trend: data.trend,
    allocations: data.allocations,
  };
}

let inFlight: Promise<FundStatsData> | null = null;

export class FundService {
  static async getStats(): Promise<FundStatsData> {
    // C1: dedup concurrent callers onto a single in-flight request.
    // Rejected promises are not cached so subsequent calls retry.
    if (inFlight) {
      return inFlight;
    }
    const promise = (async () => {
      try {
        const response = await fetch("/api/fund/stats", {
          method: "GET",
          headers: { Accept: "application/json" },
        });

        let payload: unknown = null;
        try {
          payload = await response.json();
        } catch (err) {
          logger.error({ err, status: response.status }, "Failed to parse fund stats response");
          throw new Error("Fund stats: invalid server response");
        }

        if (!response.ok) {
          const root = payload as { error?: string } | null;
          const msg = root?.error ?? `Fund stats request failed (${response.status})`;
          logger.warn({ status: response.status, msg }, "Fund stats request non-OK");
          throw new Error(msg);
        }

        return parseFundStats(payload);
      } finally {
        inFlight = null;
      }
    })();
    inFlight = promise;
    return promise;
  }
}

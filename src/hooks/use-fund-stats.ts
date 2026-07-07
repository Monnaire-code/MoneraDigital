import { useEffect, useState } from "react";
import { FundService, type FundStatsData } from "@/lib/fund-service";

export type FundStatsStatus = "loading" | "ready" | "error";

export interface FundStatsState {
  status: FundStatsStatus;
  data: FundStatsData | null;
  error: string | null;
}

export function useFundStats(): FundStatsState {
  const [state, setState] = useState<FundStatsState>({
    status: "loading",
    data: null,
    error: null,
  });

  useEffect(() => {
    let cancelled = false;

    FundService.getStats()
      .then((data) => {
        if (cancelled) return;
        setState({ status: "ready", data, error: null });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const message = err instanceof Error ? err.message : "Unknown error";
        setState({ status: "error", data: null, error: message });
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}

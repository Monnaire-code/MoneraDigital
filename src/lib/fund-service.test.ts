import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { FundService, parseFundStats } from "./fund-service";

function buildValidPayload(overrides: { data?: Record<string, unknown> } & Record<string, unknown> = {}) {
  const { data: dataOverrides, ...topLevel } = overrides;
  return {
    success: true,
    data: {
      current: {
        reportDate: "2026-05",
        totalAum: 14820125.94,
        actualApy: 0.1623,
        weightedApy: 0.5871,
        monthGrowth: 0.0461,
        newCapital: 2130800,
      },
      trend: [
        { month: "2026-01", aum: 1000000 },
        { month: "2026-05", aum: 14820125.94 },
      ],
      allocations: [
        { category: "DeFi Yield Farming", amount: 3857328.43, pct: 0.26028 },
      ],
      ...(dataOverrides ?? {}),
    },
    ...topLevel,
  };
}

describe("parseFundStats", () => {
  it("extracts a valid payload", () => {
    const data = parseFundStats(buildValidPayload());
    expect(data.current.totalAum).toBe(14820125.94);
    expect(data.trend).toHaveLength(2);
    expect(data.allocations[0].category).toBe("DeFi Yield Farming");
  });

  it("throws when success is false", () => {
    expect(() =>
      parseFundStats({ success: false, error: "No fund report" })
    ).toThrow("No fund report");
  });

  it("throws when current is missing required fields", () => {
    expect(() =>
      parseFundStats(
        buildValidPayload({ data: { current: { reportDate: "2026-05" } } })
      )
    ).toThrow(/current/);
  });

  it("throws when trend is not an array of points", () => {
    expect(() =>
      parseFundStats(
        buildValidPayload({ data: { trend: [{ month: "2026-01" }] } })
      )
    ).toThrow(/trend/);
  });

  it("throws when allocations are malformed", () => {
    expect(() =>
      parseFundStats(
        buildValidPayload({ data: { allocations: [{ category: "x" }] } })
      )
    ).toThrow(/allocations/);
  });

  it("throws on non-object input", () => {
    expect(() => parseFundStats(null)).toThrow();
    expect(() => parseFundStats("nope")).toThrow();
  });

  // E: domain validation
  describe("domain constraints (audit 2.5)", () => {
    it("rejects negative totalAum", () => {
      expect(() =>
        parseFundStats(buildValidPayload({ data: { current: { totalAum: -1 } } }))
      ).toThrow(/totalAum/);
    });

    it("rejects NaN totalAum", () => {
      const payload = buildValidPayload({ data: { current: { totalAum: Number.NaN } } });
      expect(() => parseFundStats(payload)).toThrow();
    });

    it("rejects Infinity totalAum", () => {
      const payload = buildValidPayload({ data: { current: { totalAum: Infinity } } });
      expect(() => parseFundStats(payload)).toThrow();
    });

    it("rejects negative trend aum", () => {
      expect(() =>
        parseFundStats(
          buildValidPayload({ data: { trend: [{ month: "2026-01", aum: -100 }] } })
        )
      ).toThrow(/trend/);
    });

    it("rejects empty trend", () => {
      expect(() =>
        parseFundStats(buildValidPayload({ data: { trend: [] } }))
      ).toThrow(/trend/);
    });

    it("rejects empty allocations", () => {
      expect(() =>
        parseFundStats(buildValidPayload({ data: { allocations: [] } }))
      ).toThrow(/allocations/);
    });

    it("rejects negative allocation amount", () => {
      expect(() =>
        parseFundStats(
          buildValidPayload({
            data: { allocations: [{ category: "x", amount: -1, pct: 0.1 }] },
          })
        )
      ).toThrow(/amount/);
    });

    it("rejects allocation pct outside [0, 1]", () => {
      expect(() =>
        parseFundStats(
          buildValidPayload({
            data: { allocations: [{ category: "x", amount: 100, pct: 1.5 }] },
          })
        )
      ).toThrow(/pct/);
    });

    it("rejects reportDate not matching YYYY-MM", () => {
      expect(() =>
        parseFundStats(buildValidPayload({ data: { current: { reportDate: "May 2026" } } }))
      ).toThrow(/reportDate/);
    });

    it("rejects month not matching YYYY-MM", () => {
      expect(() =>
        parseFundStats(
          buildValidPayload({ data: { trend: [{ month: "2026/05", aum: 1 }] } })
        )
      ).toThrow(/month/);
    });
  });
});

describe("FundService.getStats", () => {
  beforeEach(() => {
    vi.spyOn(global, "fetch").mockReset();
    // L3: clear sessionStorage so prior tests' cached payloads don't
    // short-circuit subsequent tests in this block.
    if (typeof sessionStorage !== "undefined") {
      sessionStorage.clear();
    }
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns parsed fund stats on 200", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(buildValidPayload()),
    });

    const data = await FundService.getStats();
    expect(data.current.totalAum).toBe(14820125.94);
    expect(global.fetch).toHaveBeenCalledWith(
      "/api/fund/stats",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("throws on 404 with server error message", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      json: () => Promise.resolve({ success: false, error: "No fund report available yet" }),
    });

    await expect(FundService.getStats()).rejects.toThrow(
      "No fund report available yet"
    );
  });

  it("throws on 500 with generic message when no error field", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: () => Promise.resolve({}),
    });

    await expect(FundService.getStats()).rejects.toThrow(/500/);
  });

  it("throws when response body is not JSON", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.reject(new SyntaxError("unexpected token")),
    });

    await expect(FundService.getStats()).rejects.toThrow(/invalid server response/);
  });

  // C1: request dedup
  describe("request dedup (audit 2.1)", () => {
    it("coalesces concurrent calls into a single fetch", async () => {
      let resolveJson: (v: unknown) => void = () => {};
      global.fetch = vi.fn().mockReturnValue(
        new Promise((resolve) => {
          resolveJson = (v) =>
            resolve({
              ok: true,
              status: 200,
              json: () => Promise.resolve(v),
            });
        })
      );

      const p1 = FundService.getStats();
      const p2 = FundService.getStats();
      const p3 = FundService.getStats();

      resolveJson(buildValidPayload());
      const [r1, r2, r3] = await Promise.all([p1, p2, p3]);

      expect(r1.current.totalAum).toBe(14820125.94);
      expect(r2.current.totalAum).toBe(14820125.94);
      expect(r3.current.totalAum).toBe(14820125.94);
      expect(global.fetch).toHaveBeenCalledTimes(1);
    });

    it("does not cache rejections (next call retries)", async () => {
      global.fetch = vi
        .fn()
        .mockRejectedValueOnce(new TypeError("network down"))
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () => Promise.resolve(buildValidPayload()),
        });

      await expect(FundService.getStats()).rejects.toThrow("network down");
      const data = await FundService.getStats();
      expect(data.current.totalAum).toBe(14820125.94);
      expect(global.fetch).toHaveBeenCalledTimes(2);
    });
  });

  // L3: sessionStorage cache + stale-while-revalidate.
  // Frontend defence in depth on top of the Go backend's in-memory
  // cache (L1) and rate-limit whitelist (L2). Keeps homepage navigation
  // snappy and removes the "too many requests" blast radius if a user
  // hammers refresh in the same tab.
  describe("sessionStorage cache (L3)", () => {
    beforeEach(() => {
      sessionStorage.clear();
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2026-06-01T12:00:00Z"));
    });

    afterEach(() => {
      vi.useRealTimers();
      sessionStorage.clear();
    });

    it("populates sessionStorage on first successful fetch", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(buildValidPayload()),
      });

      const data = await FundService.getStats();
      expect(data.current.totalAum).toBe(14820125.94);

      const raw = sessionStorage.getItem("fund:stats:v1");
      expect(raw).not.toBeNull();
      const entry = JSON.parse(raw!);
      expect(entry.data.current.totalAum).toBe(14820125.94);
      expect(typeof entry.ts).toBe("number");
    });

    it("serves from cache within 30s — no network call", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(buildValidPayload()),
      });

      await FundService.getStats(); // populates cache
      expect(global.fetch).toHaveBeenCalledTimes(1);

      vi.setSystemTime(new Date("2026-06-01T12:00:25Z")); // 25s later, still inside TTL

      const data = await FundService.getStats();
      expect(data.current.totalAum).toBe(14820125.94);
      expect(global.fetch).toHaveBeenCalledTimes(1);
    });

    it("fetches fresh from network after 30s TTL expires", async () => {
      global.fetch = vi
        .fn()
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () =>
            Promise.resolve(
              buildValidPayload({
                data: {
                  current: {
                    reportDate: "2026-05",
                    totalAum: 1000,
                    actualApy: 0.1,
                    weightedApy: 0.2,
                    monthGrowth: 0.05,
                    newCapital: 100,
                  },
                },
              })
            ),
        })
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () =>
            Promise.resolve(
              buildValidPayload({
                data: {
                  current: {
                    reportDate: "2026-05",
                    totalAum: 2000,
                    actualApy: 0.1,
                    weightedApy: 0.2,
                    monthGrowth: 0.05,
                    newCapital: 100,
                  },
                },
              })
            ),
        });

      const first = await FundService.getStats();
      expect(first.current.totalAum).toBe(1000);

      vi.setSystemTime(new Date("2026-06-01T12:00:31Z")); // 31s later, past TTL

      const second = await FundService.getStats();
      expect(second.current.totalAum).toBe(2000);
      expect(global.fetch).toHaveBeenCalledTimes(2);
    });

    it("does not pollute cache on error — next call retries", async () => {
      global.fetch = vi
        .fn()
        .mockRejectedValueOnce(new TypeError("network down"))
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () => Promise.resolve(buildValidPayload()),
        });

      await expect(FundService.getStats()).rejects.toThrow("network down");
      expect(sessionStorage.getItem("fund:stats:v1")).toBeNull();

      const data = await FundService.getStats();
      expect(data.current.totalAum).toBe(14820125.94);
      expect(global.fetch).toHaveBeenCalledTimes(2);
    });

    it("survives corrupted sessionStorage payload", async () => {
      sessionStorage.setItem("fund:stats:v1", "{not valid json");
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(buildValidPayload()),
      });

      const data = await FundService.getStats();
      expect(data.current.totalAum).toBe(14820125.94);
      expect(global.fetch).toHaveBeenCalledTimes(1);
    });
  });
});

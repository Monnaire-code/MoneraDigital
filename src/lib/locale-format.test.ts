import { describe, it, expect } from "vitest";
import { formatUsdShort, formatUsdFull, formatMonthYear, formatMonthShort } from "./locale-format";

describe("formatUsdShort", () => {
  it("formats millions with 2 decimals", () => {
    expect(formatUsdShort(14820125.94, "en")).toBe("$14.82M");
    expect(formatUsdShort(14820125.94, "zh")).toBe("$14.82M");
  });

  it("formats thousands as 2-decimal millions", () => {
    expect(formatUsdShort(2130800, "en")).toBe("$2.13M");
    expect(formatUsdShort(2130800, "zh")).toBe("$2.13M");
  });

  it("formats sub-thousand values as integer dollars", () => {
    expect(formatUsdShort(500, "en")).toBe("$500");
  });

  it("returns $0.00 for zero / non-positive", () => {
    expect(formatUsdShort(0, "en")).toBe("$0");
  });
});

describe("formatUsdFull", () => {
  it("en uses USD currency formatting", () => {
    expect(formatUsdFull(14820125.94, "en")).toBe("$14,820,126");
  });

  it("zh uses USD with comma grouping (no US$ prefix)", () => {
    const out = formatUsdFull(14820125.94, "zh");
    expect(out).toBe("$14,820,126");
  });
});

describe("formatMonthYear", () => {
  it("en renders short month name + year", () => {
    expect(formatMonthYear("2026-05", "en")).toBe("May 2026");
  });

  it("zh renders numeric year + 月 + month number", () => {
    expect(formatMonthYear("2026-05", "zh")).toBe("2026年5月");
  });

  it("returns input unchanged for malformed month", () => {
    expect(formatMonthYear("not-a-month", "en")).toBe("not-a-month");
  });
});

describe("formatMonthShort", () => {
  it("en renders 2-digit month", () => {
    expect(formatMonthShort("2026-05", "en")).toBe("05");
  });

  it("zh renders month number without leading zero", () => {
    expect(formatMonthShort("2026-05", "zh")).toBe("5");
  });
});

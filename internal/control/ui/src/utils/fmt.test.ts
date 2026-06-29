import { describe, test, expect } from "vitest";
import { fmtNum, fmtBytes, fmtDurMs } from "./fmt";

// These pin the EXACT outputs the per-view inline formatters produced before the
// DRY extraction (Overview/Health/Xray/Blueprint fmtNum; Health fmtHeap→fmtBytes,
// fmtMs→fmtDurMs). The extraction is behaviour-preserving — if any of these flip,
// a view's rendered numbers changed and the refactor regressed.

describe("fmtNum (compact magnitude — verbatim from Overview/Health/Xray/Blueprint)", () => {
  test("sub-thousand rounds to an integer string", () => {
    expect(fmtNum(0)).toBe("0");
    expect(fmtNum(7)).toBe("7");
    expect(fmtNum(840)).toBe("840");
    expect(fmtNum(840.6)).toBe("841");
    expect(fmtNum(999)).toBe("999");
  });
  test("thousands → k with one decimal below 10k, integer at/above 10k", () => {
    expect(fmtNum(1000)).toBe("1k");
    expect(fmtNum(1500)).toBe("1.5k");
    expect(fmtNum(9400)).toBe("9.4k");
    expect(fmtNum(9999)).toBe("10k"); // toFixed(1) rounds 9.999 → 10.0 → strips .0
    expect(fmtNum(10000)).toBe("10k");
    expect(fmtNum(12345)).toBe("12k");
  });
  test("millions → M with one decimal below 10M, integer at/above 10M", () => {
    expect(fmtNum(1000000)).toBe("1M");
    expect(fmtNum(1200000)).toBe("1.2M");
    expect(fmtNum(9400000)).toBe("9.4M");
    expect(fmtNum(10000000)).toBe("10M");
    expect(fmtNum(12345678)).toBe("12M");
  });
  test("non-finite / NaN coerces to 0", () => {
    expect(fmtNum(NaN)).toBe("0");
    expect(fmtNum(Number("nope") as unknown as number)).toBe("0");
  });
});

describe("fmtBytes (Health fmtHeap — bytes → 'N MB', verbatim)", () => {
  test("zero / falsy → em dash", () => {
    expect(fmtBytes(0)).toBe("—");
  });
  test("rounds to whole MB then applies fmtNum", () => {
    expect(fmtBytes(31457280)).toBe("30 MB"); // 31457280 / 1048576 = 30
    expect(fmtBytes(1048576)).toBe("1 MB"); // exactly 1 MiB
    expect(fmtBytes(1572864)).toBe("2 MB"); // 1.5 MiB rounds to 2
    expect(fmtBytes(10737418240)).toBe("10k MB"); // 10240 MiB → fmtNum(10240) = "10k"
  });
});

describe("fmtDurMs (Health fmtMs — ms → 'N.N ms', verbatim)", () => {
  test("null / undefined / zero → em dash", () => {
    expect(fmtDurMs(undefined)).toBe("—");
    expect(fmtDurMs(null)).toBe("—");
    expect(fmtDurMs(0)).toBe("—");
  });
  test("one-decimal millisecond rendering", () => {
    expect(fmtDurMs(1)).toBe("1.0 ms");
    expect(fmtDurMs(1.25)).toBe("1.3 ms");
    expect(fmtDurMs(42.7)).toBe("42.7 ms");
    expect(fmtDurMs(1000)).toBe("1000.0 ms");
  });
});

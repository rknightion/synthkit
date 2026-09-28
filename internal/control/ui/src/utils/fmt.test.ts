import { test, expect } from "vitest";
import { fmtNum, fmtBytes, fmtDurMs } from "./fmt";

test("fmtNum preserves magnitude thresholds and handles NaN", () => {
  expect(fmtNum(999)).toBe("999");
  expect(fmtNum(1000)).toBe("1k");
  expect(fmtNum(9999)).toBe("10k");
  expect(fmtNum(1_000_000)).toBe("1M");
  expect(fmtNum(10_000_000)).toBe("10M");
  expect(fmtNum(NaN)).toBe("0");
});

test("fmtBytes uses MiB conversion, rounds, and marks zero as absent", () => {
  expect(fmtBytes(0)).toBe("—");
  expect(fmtBytes(1_048_576)).toBe("1 MB");
  expect(fmtBytes(1_572_864)).toBe("2 MB");
});

test("fmtDurMs marks missing or zero values as absent and rounds to tenths", () => {
  expect(fmtDurMs(undefined)).toBe("—");
  expect(fmtDurMs(0)).toBe("—");
  expect(fmtDurMs(1.25)).toBe("1.3 ms");
});

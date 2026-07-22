import { describe, expect, it } from "vitest";
import { normalizeRects, selectionText } from "./pdfSelection";

const page = { left: 100, top: 200, width: 400, height: 800 };

describe("pdfSelection", () => {
  it("normalizes client rects to page-relative 0..1 boxes", () => {
    const rects = normalizeRects(page, [
      { left: 200, top: 400, width: 100, height: 40 }, // inside
    ]);
    expect(rects).toEqual([{ x: 0.25, y: 0.25, w: 0.25, h: 0.05 }]);
  });

  it("drops empty and out-of-page rectangles and clamps overflow", () => {
    const rects = normalizeRects(page, [
      { left: 200, top: 400, width: 0, height: 40 }, // empty width
      { left: -500, top: 0, width: 100, height: 10 }, // fully left of page
      { left: 450, top: 200, width: 200, height: 80 }, // overflows right edge
    ]);
    // Only the overflowing rect survives, clamped to the page width.
    expect(rects).toHaveLength(1);
    expect(rects[0].x).toBeCloseTo(0.875);
    expect(rects[0].x + rects[0].w).toBeLessThanOrEqual(1.0001);
  });

  it("returns nothing for a zero-size page", () => {
    expect(normalizeRects({ left: 0, top: 0, width: 0, height: 0 }, [{ left: 0, top: 0, width: 1, height: 1 }])).toEqual([]);
  });

  it("trims and bounds selected text to 2000 code points", () => {
    expect(selectionText("  hello  ")).toBe("hello");
    expect(selectionText("x".repeat(2500)).length).toBe(2000);
  });
});

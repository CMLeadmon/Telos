import { describe, expect, it } from "vitest";
import {
  formatKind,
  clampZoom,
  clampPage,
  epubProgressPayload,
  pdfProgressPayload,
  parseStoredLocator,
} from "./reader";

describe("reader helpers", () => {
  it("maps formats to reader kinds", () => {
    expect(formatKind("EPUB")).toBe("epub");
    expect(formatKind("pdf")).toBe("pdf");
    expect(formatKind("MOBI")).toBeNull();
  });

  it("clamps zoom and page to accepted ranges", () => {
    expect(clampZoom(99)).toBe(10);
    expect(clampZoom(0)).toBe(0.1);
    expect(clampZoom(Number.NaN)).toBe(1);
    expect(clampPage(0, 10)).toBe(1);
    expect(clampPage(50, 10)).toBe(10);
    expect(clampPage(2.9, 10)).toBe(2);
  });

  it("builds bounded epub and pdf progress payloads", () => {
    expect(epubProgressPayload("cfi(x)", 0.42)).toEqual({
      locator: { cfi: "cfi(x)", fraction: 0.42 },
      percent: 0.42,
    });
    expect(epubProgressPayload("cfi(x)", 5)).toEqual({
      locator: { cfi: "cfi(x)", fraction: 1 },
      percent: 1,
    });
    expect(pdfProgressPayload(5, 1.5, 10)).toEqual({
      locator: { page: 5, zoom: 1.5 },
      percent: 0.5,
    });
    // Out-of-range page/zoom are clamped.
    expect(pdfProgressPayload(99, 99, 10)).toEqual({
      locator: { page: 10, zoom: 10 },
      percent: 1,
    });
  });

  it("parses only coherent stored locators", () => {
    expect(parseStoredLocator("EPUB", { cfi: "x", fraction: 0.3 })).toEqual({
      kind: "epub",
      cfi: "x",
      fraction: 0.3,
    });
    expect(parseStoredLocator("EPUB", { cfi: "" })).toBeNull();
    expect(parseStoredLocator("PDF", { page: 3, zoom: 2 })).toEqual({
      kind: "pdf",
      page: 3,
      zoom: 2,
    });
    expect(parseStoredLocator("PDF", { page: 0 })).toBeNull();
    // A mixed/foreign locator is not accepted for the wrong format.
    expect(parseStoredLocator("PDF", { cfi: "x", fraction: 0.3 })).toBeNull();
    expect(parseStoredLocator("MOBI", { page: 1 })).toBeNull();
  });
});

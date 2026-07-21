import { describe, expect, it } from "vitest";
import { mergePage, pageQuery, type Page } from "./pagination";

describe("pageQuery", () => {
  it("omits empty params", () => {
    expect(pageQuery()).toBe("");
  });
  it("includes cursor and limit", () => {
    expect(pageQuery("abc", 25)).toBe("?cursor=abc&limit=25");
  });
});

describe("mergePage", () => {
  it("appends new items by id", () => {
    const existing = [{ id: "a" }, { id: "b" }];
    const page: Page<{ id: string }> = { items: [{ id: "c" }, { id: "d" }] };
    expect(mergePage(existing, page).map((x) => x.id)).toEqual(["a", "b", "c", "d"]);
  });
  it("de-duplicates an item that shifts between pages", () => {
    const existing = [{ id: "a" }, { id: "b" }];
    const page: Page<{ id: string }> = { items: [{ id: "b" }, { id: "c" }] };
    expect(mergePage(existing, page).map((x) => x.id)).toEqual(["a", "b", "c"]);
  });
});

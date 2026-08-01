import { describe, expect, it } from "vitest";

import { libraryContentUrl, libraryCoverUrl } from "./api";

describe("library URL helpers", () => {
  it("keeps a canonical catalog ID in the cover route", () => {
    expect(libraryCoverUrl("11111111-1111-4111-8111-111111111111")).toBe(
      "/api/v1/library/books/11111111-1111-4111-8111-111111111111/cover",
    );
  });

  it("encodes an opaque catalog ID as one content-route segment", () => {
    expect(libraryContentUrl("catalog/item ?edition=1")).toBe(
      "/api/v1/library/books/catalog%2Fitem%20%3Fedition%3D1/content",
    );
  });
});

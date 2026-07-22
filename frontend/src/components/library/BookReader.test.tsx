import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

// Mock the format-specific readers so this test exercises only dispatch (and the
// heavy epub.js / PDF.js libraries never load under jsdom).
vi.mock("./EpubReader", () => ({
  EpubReader: () => <div data-testid="epub-reader">epub</div>,
}));
vi.mock("./PdfReader", () => ({
  PdfReader: () => <div data-testid="pdf-reader">pdf</div>,
}));

import { BookReader } from "./BookReader";
import type { LibraryBook } from "@/stores/useLibraryStore";

function book(format: string): LibraryBook {
  return {
    id: 1,
    title: "A Book",
    subtitle: "",
    authors: [],
    categories: [],
    language: "en",
    description: "",
    seriesName: "",
    seriesNumber: null,
    publisher: "",
    publishedDate: "",
    isbn10: "",
    isbn13: "",
    format,
    fileSizeKb: 0,
    addedOn: "",
    library: "",
  };
}

describe("BookReader dispatch", () => {
  it("renders the EPUB reader for an EPUB book", () => {
    render(<BookReader book={book("EPUB")} onClose={() => {}} />);
    expect(screen.getByTestId("epub-reader")).toBeInTheDocument();
    expect(screen.queryByTestId("pdf-reader")).toBeNull();
  });

  it("renders the PDF reader for a PDF book", () => {
    render(<BookReader book={book("PDF")} onClose={() => {}} />);
    expect(screen.getByTestId("pdf-reader")).toBeInTheDocument();
    expect(screen.queryByTestId("epub-reader")).toBeNull();
  });

  it("explains an unreadable format instead of opening a reader", () => {
    render(<BookReader book={book("MOBI")} onClose={() => {}} />);
    expect(screen.getByTestId("reader-unsupported")).toBeInTheDocument();
    expect(screen.queryByTestId("epub-reader")).toBeNull();
    expect(screen.queryByTestId("pdf-reader")).toBeNull();
  });
});

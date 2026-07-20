// epubjs 0.4.x ships no TypeScript declarations (and no @types/epubjs
// exists). Declare the narrow API surface Telos uses in BookReader.tsx.
declare module "epubjs" {
  export interface Rendition {
    on(event: string, handler: (...args: never[]) => void): void;
    display(target?: string): Promise<void>;
    prev(): Promise<void>;
    next(): Promise<void>;
  }

  export interface Locations {
    generate(chars: number): Promise<unknown>;
    cfiFromPercentage(percentage: number): string;
  }

  export interface Book {
    ready: Promise<unknown>;
    locations: Locations;
    renderTo(
      element: Element,
      options?: { width?: string; height?: string },
    ): Rendition;
    destroy(): void;
  }

  export default function ePub(
    input: ArrayBuffer | string,
    options?: Record<string, unknown>,
  ): Book;
}

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { CreditsSection } from "@/components/settings/CreditsSection";

describe("component test harness", () => {
  it("renders a real component under jsdom", () => {
    render(<CreditsSection />);
    expect(screen.getByTestId("credits-section")).toBeInTheDocument();
    expect(screen.getByText("Jellyfin")).toBeInTheDocument();
  });
});

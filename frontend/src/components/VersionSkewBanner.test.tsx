import { describe, expect, it, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { VersionSkewBanner } from "./VersionSkewBanner";
import { useConnectionStore } from "@/stores/useConnectionStore";

describe("VersionSkewBanner component", () => {
  beforeEach(() => {
    useConnectionStore.getState().reset();
  });

  it("renders nothing when versionSkewSoft is false", () => {
    const { container } = render(<VersionSkewBanner />);
    expect(container.firstChild).toBeNull();
  });

  it("renders soft skew warning when versionSkewSoft is true", () => {
    useConnectionStore.setState({ versionSkewSoft: true, serverVersion: "0.9.5" });
    render(<VersionSkewBanner />);
    expect(screen.getByText(/server version 0\.9\.5 is available/i)).toBeInTheDocument();
  });

});

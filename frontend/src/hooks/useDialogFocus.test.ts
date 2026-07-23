import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useDialogFocus } from "./useDialogFocus";

describe("useDialogFocus", () => {
  it("attaches escape key listener when open", () => {
    const onClose = vi.fn();
    renderHook(() => useDialogFocus({ isOpen: true, onClose }));

    const event = new KeyboardEvent("keydown", { key: "Escape" });
    window.dispatchEvent(event);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not call onClose on escape if isOpen is false", () => {
    const onClose = vi.fn();
    renderHook(() => useDialogFocus({ isOpen: false, onClose }));

    const event = new KeyboardEvent("keydown", { key: "Escape" });
    window.dispatchEvent(event);

    expect(onClose).not.toHaveBeenCalled();
  });
});

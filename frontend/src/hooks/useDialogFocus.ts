import { useEffect, useRef } from "react";

export interface UseDialogFocusOptions {
  isOpen: boolean;
  onClose: () => void;
  preventCloseOnEscape?: boolean;
}

export function useDialogFocus<T extends HTMLElement = HTMLDivElement>({
  isOpen,
  onClose,
  preventCloseOnEscape = false,
}: UseDialogFocusOptions) {
  const ref = useRef<T | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!isOpen) return;

    // Record previously focused element
    previousFocusRef.current = document.activeElement as HTMLElement | null;

    // Focus the dialog container or first focusable child
    const el = ref.current;
    if (el) {
      const focusables = el.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (focusables.length > 0) {
        focusables[0].focus();
      } else {
        el.focus();
      }
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !preventCloseOnEscape) {
        e.preventDefault();
        onClose();
        return;
      }

      if (e.key === "Tab" && el) {
        const focusables = Array.from(
          el.querySelectorAll<HTMLElement>(
            'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
          ),
        ).filter((node) => node.offsetWidth > 0 || node.offsetHeight > 0);

        if (focusables.length === 0) return;

        const first = focusables[0];
        const last = focusables[focusables.length - 1];

        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      // Restore focus to opener element
      previousFocusRef.current?.focus();
    };
  }, [isOpen, onClose, preventCloseOnEscape]);

  return ref;
}

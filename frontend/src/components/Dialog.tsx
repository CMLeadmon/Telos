"use client";

import { useDialogFocus } from "@/hooks/useDialogFocus";
import { X } from "lucide-react";

export interface DialogProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
  testId?: string;
}

export function Dialog({ isOpen, onClose, title, children, testId }: DialogProps) {
  const ref = useDialogFocus<HTMLDivElement>({ isOpen, onClose });

  if (!isOpen) return null;

  return (
    <div
      className="dialog-backdrop"
      onClick={onClose}
      data-testid={testId ? `${testId}-backdrop` : "dialog-backdrop"}
    >
      <div
        ref={ref}
        className="dialog-card"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        data-testid={testId ?? "dialog"}
      >
        <div className="dialog-header">
          <h2>{title}</h2>
          <button className="iconbtn" onClick={onClose} aria-label="Close dialog">
            <X size={18} />
          </button>
        </div>
        <div className="dialog-body">{children}</div>
      </div>
    </div>
  );
}

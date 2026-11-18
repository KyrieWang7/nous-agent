"use client";

import type { ReactNode } from "react";

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";

// All execution surfaces share the same mobile focus, escape and close rules.
export function WorkspaceSideSheet({
  title,
  description,
  onClose,
  children,
  restoreFocus,
}: {
  title: string;
  description: string;
  onClose: () => void;
  children: ReactNode;
  restoreFocus?: string | (() => HTMLElement | null);
}) {
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent
        side="right"
        className="w-full max-w-none gap-0 p-0 sm:max-w-none [&>button]:hidden"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          requestAnimationFrame(() =>
            (
              (typeof restoreFocus === "function"
                ? restoreFocus()
                : document.querySelector<HTMLElement>(
                    restoreFocus ?? "[data-workspace-navigation]",
                  )) ??
              document.querySelector<HTMLButtonElement>(
                "[data-workspace-navigation]",
              )
            )?.focus(),
          );
        }}
      >
        <SheetTitle className="sr-only">{title}</SheetTitle>
        <SheetDescription className="sr-only">{description}</SheetDescription>
        {children}
      </SheetContent>
    </Sheet>
  );
}

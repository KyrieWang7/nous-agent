import Link from "next/link";

import { NousLogo } from "@/components/ui/nous-logo";

export function ProductHeader({ homeLabel }: { homeLabel: string }) {
  return (
    <header className="border-b border-border bg-background">
      <div className="mx-auto flex h-16 max-w-6xl items-center px-4 sm:px-8">
        <Link
          href="/"
          aria-label={homeLabel}
          className="flex items-center gap-2 rounded-md outline-offset-4 transition-opacity hover:opacity-75"
        >
          <NousLogo className="size-8" />
          <span className="text-lg font-semibold tracking-tight">Nous</span>
        </Link>
      </div>
    </header>
  );
}

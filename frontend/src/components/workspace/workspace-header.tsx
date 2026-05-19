"use client";

import Link from "next/link";

import { NousLogo } from "@/components/ui/nous-logo";
import {
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

export function WorkspaceHeader({ className }: { className?: string }) {
  const { state } = useSidebar();
  return (
    <>
      <div
        className={cn(
          "group/workspace-header flex h-16 flex-col justify-center border-b border-sidebar-border/70 px-2",
          className,
        )}
      >
        {state === "collapsed" ? (
          <div className="group-has-data-[collapsible=icon]/sidebar-wrapper:-translate-y flex w-full cursor-pointer items-center justify-center">
            <Link href="/" className="group-hover/workspace-header:hidden flex items-center justify-center">
              <NousLogo className="size-6 text-foreground hover:opacity-80 transition-opacity" />
            </Link>
            <SidebarTrigger className="hidden pl-2 group-hover/workspace-header:block" />
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2 pl-2 w-full">
            <Link href="/" className="flex items-center gap-2 group">
              <NousLogo className="size-7 text-foreground group-hover:opacity-80 transition-opacity" />
              <img
                src="/name-transparent.png"
                alt="Nous"
                className="h-4 w-auto max-w-[70px] object-contain transition-opacity group-hover:opacity-80"
              />
            </Link>
            <SidebarTrigger className="text-muted-foreground mr-1" />
          </div>
        )}
      </div>
    </>
  );
}

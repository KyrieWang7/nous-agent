"use client";

import { SettingsIcon } from "lucide-react";
import { useEffect, useState } from "react";

import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { useI18n } from "@/core/i18n/hooks";

import { SettingsDialog } from "./settings";

export function WorkspaceNavMenu() {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [mounted, setMounted] = useState(false);
  const { open: isSidebarOpen } = useSidebar();
  const { t } = useI18n();

  useEffect(() => {
    setMounted(true);
  }, []);

  const buttonContent = isSidebarOpen ? (
    <div className="text-sidebar-foreground/74 flex w-full items-center gap-2 text-left text-sm py-1">
      <SettingsIcon className="size-5 shrink-0" />
      <span className="font-medium truncate">{t.settings.title}</span>
    </div>
  ) : (
    <div className="flex size-full items-center justify-center">
      <SettingsIcon className="text-sidebar-foreground/74 size-5" />
    </div>
  );

  return (
    <>
      <SettingsDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        defaultSection="appearance"
      />
      <SidebarMenu className="w-full">
        <SidebarMenuItem>
          <SidebarMenuButton
            size="lg"
            className={mounted ? undefined : "pointer-events-none"}
            onClick={() => setSettingsOpen(true)}
          >
            {buttonContent}
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </>
  );
}

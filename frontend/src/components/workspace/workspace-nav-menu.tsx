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

export function WorkspaceNavMenu({
  onOpenSettings,
}: {
  onOpenSettings: () => void;
}) {
  const [mounted, setMounted] = useState(false);
  const { open: isSidebarOpen, isMobile, setOpenMobile } = useSidebar();
  const { t } = useI18n();

  useEffect(() => {
    setMounted(true);
  }, []);

  const buttonContent = isSidebarOpen ? (
    <div className="text-sidebar-foreground/74 flex w-full items-center gap-2 py-1 text-left text-sm">
      <SettingsIcon className="size-5 shrink-0" />
      <span className="truncate font-medium">{t.settings.title}</span>
    </div>
  ) : (
    <div className="flex size-full items-center justify-center">
      <SettingsIcon className="text-sidebar-foreground/74 size-5" />
    </div>
  );

  return (
    <>
      <SidebarMenu className="w-full">
        <SidebarMenuItem>
          <SidebarMenuButton
            size="lg"
            className={mounted ? undefined : "pointer-events-none"}
            aria-label={t.settings.title}
            tooltip={t.settings.title}
            onClick={() => {
              if (isMobile) setOpenMobile(false);
              onOpenSettings();
            }}
          >
            {buttonContent}
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </>
  );
}

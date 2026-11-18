"use client";

import { useState } from "react";

import {
  Sidebar,
  SidebarHeader,
  SidebarContent,
  SidebarFooter,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar";

import { RecentChatList } from "./recent-chat-list";
import { SettingsDialog } from "./settings";
import { WorkspaceHeader } from "./workspace-header";
import { WorkspaceNavChatList } from "./workspace-nav-chat-list";
import { WorkspaceNavMenu } from "./workspace-nav-menu";

export function WorkspaceSidebar({
  ...props
}: React.ComponentProps<typeof Sidebar>) {
  const { open: isSidebarOpen } = useSidebar();
  const [settingsOpen, setSettingsOpen] = useState(false);
  return (
    <>
      <SettingsDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        defaultSection="appearance"
      />
      <Sidebar variant="sidebar" collapsible="icon" {...props}>
        <SidebarHeader className="py-0">
          <WorkspaceHeader />
        </SidebarHeader>
        <SidebarContent>
          <WorkspaceNavChatList />
          {isSidebarOpen && <RecentChatList />}
        </SidebarContent>
        <SidebarFooter>
          <WorkspaceNavMenu onOpenSettings={() => setSettingsOpen(true)} />
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>
    </>
  );
}

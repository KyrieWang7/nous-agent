"use client";

import {
  Bot,
  Search,
  BookOpen,
  FolderPlus,
  PlusCircle,
  MessageSquarePlus,
} from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { useI18n } from "@/core/i18n/hooks";

export function WorkspaceNavChatList() {
  const { t } = useI18n();
  const pathname = usePathname();
  return (
    <>
      <SidebarGroup className="pt-2 pb-0">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/workspace/chats/new"}
              className="mb-3 h-10 bg-sidebar-accent text-sidebar-accent-foreground hover:bg-sidebar-accent"
              asChild
            >
              <Link href="/workspace/chats/new">
                <MessageSquarePlus />
                <span>{t.sidebar.newChat}</span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>

          {/* Core Navigation Group */}
          <SidebarMenuItem>
            <SidebarMenuButton isActive={pathname === "/workspace"} asChild>
              <Link href="/workspace">
                <Bot />
                <span>{t.sidebar.agents}</span>
                <span className="ml-auto text-[10px] font-semibold bg-[#3B82F6]/10 text-[#3B82F6] px-1.5 py-0.5 rounded">
                  新
                </span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton asChild>
              <button type="button" className="w-full text-left">
                <Search />
                <span>{t.sidebar.search}</span>
              </button>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton asChild>
              <button type="button" className="w-full text-left">
                <BookOpen />
                <span>{t.sidebar.library}</span>
              </button>
            </SidebarMenuButton>
          </SidebarMenuItem>

        </SidebarMenu>
      </SidebarGroup>

      {/* Projects Group */}
      <SidebarGroup className="pt-2">
        <div className="flex items-center justify-between px-2 mb-1">
          <SidebarGroupLabel className="h-8 px-0 truncate text-[13px] font-semibold text-sidebar-foreground/54">
            {t.sidebar.projects}
          </SidebarGroupLabel>
          <button className="text-muted-foreground opacity-50 hover:opacity-100 hover:text-foreground outline-none transition-opacity">
            <PlusCircle className="size-4" />
          </button>
        </div>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild>
              <button type="button" className="w-full text-left">
                <FolderPlus />
                <span>{t.sidebar.newProject}</span>
              </button>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroup>
    </>
  );
}

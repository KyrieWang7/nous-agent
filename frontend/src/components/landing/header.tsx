"use client";

import Link from "next/link";
import { LayoutDashboard, LogOut } from "lucide-react";

import { logout as logoutRequest } from "@/core/auth";
import { useAuthStore } from "@/core/auth/store";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { NousLogo } from "@/components/ui/nous-logo";

const navLinks = [
  { href: "/workspace", label: "Agent" },
  { href: "#", label: "素材生成" },
  { href: "/workspace/drama", label: "短剧生成" },
  { href: "#", label: "关于" },
];

export function Header() {
  const { isLoggedIn, user, logout } = useAuthStore();
  const displayName = user?.display_name || user?.username || "U";
  const avatarFallback = displayName.charAt(0).toUpperCase();

  async function handleLogout() {
    try {
      await logoutRequest();
    } finally {
      logout();
    }
  }

  return (
    <header className="fixed top-0 right-0 left-0 z-50 flex h-16 items-center justify-center backdrop-blur-md bg-[#0a0a0a]/70 border-b border-white/5 transition-all">
      <div className="container-md mx-auto flex w-full items-center justify-between px-4 sm:px-6">
        <div className="flex items-center gap-8 md:gap-12">
          <a href="/workspace" className="flex items-center gap-3 group">
            <NousLogo className="size-8 md:size-10 text-primary transition-transform duration-300 group-hover:scale-105" />
            <span className="text-3xl md:text-4xl font-bold tracking-tight text-white/90 group-hover:text-white transition-colors">Nous</span>
          </a>

          <nav className="hidden md:flex items-center gap-8">
            {navLinks.map((link) => (
              <Link
                key={link.label}
                href={link.href}
                className="text-base md:text-lg font-medium tracking-wide text-white/60 hover:text-white transition-colors duration-200 hover:text-shadow-sm"
              >
                {link.label}
              </Link>
            ))}
          </nav>
        </div>

        <div className="flex items-center gap-4">
          {isLoggedIn ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  className="rounded-full outline-none transition-transform hover:scale-105 active:scale-95 focus-visible:ring-2 focus-visible:ring-white/20"
                >
                  <Avatar className="size-10 md:size-11 border border-white/10 shadow-lg shadow-black/20">
                    <AvatarImage src={user?.avatar_url ?? undefined} alt={displayName} />
                    <AvatarFallback className="bg-primary/10 text-primary font-semibold">
                      {avatarFallback}
                    </AvatarFallback>
                  </Avatar>
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                sideOffset={12}
                className="w-56 rounded-2xl bg-[#0a0a0a]/80 backdrop-blur-xl border border-white/10 p-2 shadow-2xl"
              >
                <DropdownMenuLabel className="font-normal px-2.5 py-2">
                  <div className="flex flex-col space-y-1.5">
                    <p className="text-sm font-semibold leading-none text-white">{displayName}</p>
                    {user?.email && (
                      <p className="text-xs leading-none text-white/50">{user.email}</p>
                    )}
                  </div>
                </DropdownMenuLabel>
                <DropdownMenuSeparator className="bg-white/10 mx-1" />
                <DropdownMenuItem asChild className="cursor-pointer rounded-xl focus:bg-white/10 focus:text-white px-2.5 py-2 mt-1">
                  <Link href="/workspace" className="flex items-center gap-3">
                    <LayoutDashboard className="size-4 opacity-70" />
                    <span>前往工作台</span>
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuSeparator className="bg-white/10 mx-1" />
                <DropdownMenuItem
                  className="cursor-pointer rounded-xl focus:bg-red-500/20 text-red-400 focus:text-red-400 px-2.5 py-2 mb-1"
                  onClick={() => {
                    void handleLogout();
                  }}
                >
                  <div className="flex items-center gap-3 w-full">
                    <LogOut className="size-4 opacity-70" />
                    <span>退出登录</span>
                  </div>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <>
              <Link href="/login">
                <Button
                  variant="ghost"
                  className="text-white/70 hover:text-white hover:bg-white/10 hidden sm:inline-flex font-medium text-base tracking-wide rounded-full px-6 py-5"
                >
                  登录
                </Button>
              </Link>
              <Link href="/login">
                <Button
                  className="bg-white text-black hover:bg-gray-200 font-semibold text-base tracking-wide rounded-full px-6 py-5 shadow-lg shadow-white/10 transition-all hover:shadow-white/20 active:scale-95"
                >
                  注册
                </Button>
              </Link>
            </>
          )}
        </div>
      </div>
    </header>
  );
}

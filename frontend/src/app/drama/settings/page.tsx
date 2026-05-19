"use client";

import { useState } from "react";

import { About } from "@/components/drama/settings/about";
import { AIConfig } from "@/components/drama/settings/ai-config";
import { DBConfig } from "@/components/drama/settings/db-config";
import { LoginConfig } from "@/components/drama/settings/login-config";
import { LogoutConfig } from "@/components/drama/settings/logout-config";
import { OtherConfig } from "@/components/drama/settings/other-config";
import { PromptsEdit } from "@/components/drama/settings/prompts-edit";
import { RequestConfig } from "@/components/drama/settings/request-config";
import { ThemeConfig } from "@/components/drama/settings/theme-config";
import { VideoModelConfig } from "@/components/drama/settings/video-model-config";
import { cn } from "@/lib/utils";

type SettingsTab =
  | "theme"
  | "request"
  | "login"
  | "ai"
  | "videoModel"
  | "prompts"
  | "other"
  | "database"
  | "about"
  | "logout";

interface TabItem {
  id: SettingsTab;
  label: string;
  icon: React.ReactNode;
}

const tabs: TabItem[] = [
  { id: "theme", label: "主题设置", icon: <span className="text-lg">🎨</span> },
  { id: "request", label: "请求地址", icon: <span className="text-lg">🔗</span> },
  { id: "login", label: "登录配置", icon: <span className="text-lg">🔑</span> },
  { id: "ai", label: "语言模型", icon: <span className="text-lg">🤖</span> },
  { id: "videoModel", label: "视频模型", icon: <span className="text-lg">🎬</span> },
  { id: "prompts", label: "提示词配置", icon: <span className="text-lg">📝</span> },
  { id: "other", label: "其他配置", icon: <span className="text-lg">⚙️</span> },
  { id: "database", label: "数据库", icon: <span className="text-lg">💾</span> },
  { id: "about", label: "关于", icon: <span className="text-lg">ℹ️</span> },
  { id: "logout", label: "退出登录", icon: <span className="text-lg">🚪</span> },
];

function SettingsContent({ tab }: { tab: SettingsTab }) {
  switch (tab) {
    case "theme":
      return <ThemeConfig />;
    case "request":
      return <RequestConfig />;
    case "login":
      return <LoginConfig />;
    case "ai":
      return <AIConfig />;
    case "videoModel":
      return <VideoModelConfig />;
    case "prompts":
      return <PromptsEdit />;
    case "other":
      return <OtherConfig />;
    case "database":
      return <DBConfig />;
    case "about":
      return <About />;
    case "logout":
      return <LogoutConfig />;
    default:
      return <div>未知设置</div>;
  }
}

export default function DramaSettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>("theme");

  return (
    <div className="flex h-full">
      {/* Sidebar */}
      <div className="w-64 border-r bg-card overflow-y-auto">
        <div className="p-4">
          <h1 className="text-xl font-semibold mb-4">设置</h1>
          <nav className="space-y-1">
            {tabs.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  "w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors",
                  activeTab === tab.id
                    ? "bg-primary text-primary-foreground"
                    : "hover:bg-muted"
                )}
              >
                {tab.icon}
                <span>{tab.label}</span>
              </button>
            ))}
          </nav>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto">
        <div className="max-w-2xl mx-auto p-8">
          <SettingsContent tab={activeTab} />
        </div>
      </div>
    </div>
  );
}

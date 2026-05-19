"use client";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

const VERSION = "1.0.0";

function openLink(url: string) {
  window.open(url, "_blank");
}

export function About() {
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">关于</h2>
        <p className="text-muted-foreground">查看应用信息和相关链接</p>
      </div>

      <Card>
        <CardContent className="flex flex-col items-center py-8">
          <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-purple-500 to-purple-700 flex items-center justify-center mb-4">
            <span className="text-2xl font-bold text-white">NF</span>
          </div>
          <h3 className="text-xl font-semibold mb-2">ToonFlow</h3>
          <span className="px-2 py-1 bg-primary/10 text-primary text-sm rounded">v{VERSION}</span>
          <p className="text-muted-foreground text-center mt-4">
            开源的 AI 驱动漫画/分镜创作工具
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="p-0">
          <button
            onClick={() => openLink("https://github.com/HBAI-Ltd/Toonflow-app")}
            className="w-full flex items-center gap-3 px-4 py-3 hover:bg-muted transition-colors"
          >
            <span className="text-lg">📦</span>
            <span className="flex-1 text-left">GitHub 仓库</span>
            <span className="text-muted-foreground">→</span>
          </button>
          <div className="h-px bg-border mx-4" />
          <button
            onClick={() => openLink("https://gitee.com/HBAI-Ltd/Toonflow-app")}
            className="w-full flex items-center gap-3 px-4 py-3 hover:bg-muted transition-colors"
          >
            <span className="text-lg">💻</span>
            <span className="flex-1 text-left">Gitee 仓库</span>
            <span className="text-muted-foreground">→</span>
          </button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="p-0">
          <button
            onClick={() => openLink("https://github.com/HBAI-Ltd/Toonflow-app/releases")}
            className="w-full flex items-center gap-3 px-4 py-3 hover:bg-muted transition-colors"
          >
            <span className="text-lg">🔄</span>
            <span className="flex-1 text-left">检查更新 (GitHub)</span>
            <span className="text-muted-foreground">→</span>
          </button>
          <div className="h-px bg-border mx-4" />
          <button
            onClick={() => openLink("https://gitee.com/HBAI-Ltd/Toonflow-app/releases")}
            className="w-full flex items-center gap-3 px-4 py-3 hover:bg-muted transition-colors"
          >
            <span className="text-lg">🔄</span>
            <span className="flex-1 text-left">检查更新 (Gitee)</span>
            <span className="text-muted-foreground">→</span>
          </button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="py-3">
          <div className="flex items-center justify-center gap-2 text-muted-foreground text-sm">
            <span>📜</span>
            <span>AGPL-3.0 License</span>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

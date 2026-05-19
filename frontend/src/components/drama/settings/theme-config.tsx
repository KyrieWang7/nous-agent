"use client";

import { useState } from "react";
import { Sun, Moon, Monitor } from "lucide-react";
import { cn } from "@/lib/utils";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";

const THEME_COLORS = [
  { name: "香芋紫", value: "#9810fa" },
  { name: "活力橙", value: "#ED7B2F" },
  { name: "柠檬黄", value: "#F5BA18" },
  { name: "薄荷绿", value: "#00A870" },
  { name: "青翠绿", value: "#2BA471" },
  { name: "山茶红", value: "#D54941" },
  { name: "碧空蓝", value: "#029CD4" },
];

type ThemeMode = "light" | "dark" | "auto";

export function ThemeConfig() {
  const [mode, setMode] = useState<ThemeMode>(() => {
    return (localStorage.getItem("theme-mode") as ThemeMode) || "auto";
  });
  const [primaryColor, setPrimaryColor] = useState(() => {
    return localStorage.getItem("primary-color") || "#9810fa";
  });

  const handleModeChange = (newMode: ThemeMode) => {
    setMode(newMode);
    localStorage.setItem("theme-mode", newMode);

    // Apply theme mode
    if (newMode === "auto") {
      document.documentElement.classList.remove("dark");
    } else if (newMode === "dark") {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
  };

  const handleColorChange = (color: string) => {
    setPrimaryColor(color);
    localStorage.setItem("primary-color", color);
    document.documentElement.style.setProperty("--primary", color);
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">主题设置</h2>
        <p className="text-muted-foreground">自定义应用的外观和主题色</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>主题模式</CardTitle>
          <CardDescription>切换后建议刷新页面</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex gap-2">
            <button
              onClick={() => handleModeChange("light")}
              className={cn(
                "flex items-center gap-2 px-4 py-2 rounded-lg border transition-colors",
                mode === "light"
                  ? "bg-primary text-primary-foreground border-primary"
                  : "hover:bg-muted"
              )}
            >
              <Sun className="h-4 w-4" />
              <span>浅色</span>
            </button>
            <button
              onClick={() => handleModeChange("dark")}
              className={cn(
                "flex items-center gap-2 px-4 py-2 rounded-lg border transition-colors",
                mode === "dark"
                  ? "bg-primary text-primary-foreground border-primary"
                  : "hover:bg-muted"
              )}
            >
              <Moon className="h-4 w-4" />
              <span>深色</span>
            </button>
            <button
              onClick={() => handleModeChange("auto")}
              className={cn(
                "flex items-center gap-2 px-4 py-2 rounded-lg border transition-colors",
                mode === "auto"
                  ? "bg-primary text-primary-foreground border-primary"
                  : "hover:bg-muted"
              )}
            >
              <Monitor className="h-4 w-4" />
              <span>跟随系统</span>
            </button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>主题色</CardTitle>
          <CardDescription>选择应用的主色调</CardDescription>
        </CardHeader>
        <CardContent>
          <Label className="mb-3 block">预设颜色</Label>
          <div className="flex flex-wrap gap-3">
            {THEME_COLORS.map((color) => (
              <button
                key={color.value}
                onClick={() => handleColorChange(color.value)}
                className={cn(
                  "w-9 h-9 rounded-md flex items-center justify-center transition-transform hover:scale-110",
                  primaryColor.toLowerCase() === color.value.toLowerCase()
                    ? "ring-2 ring-primary ring-offset-2"
                    : ""
                )}
                style={{ backgroundColor: color.value }}
                title={color.name}
              >
                {primaryColor.toLowerCase() === color.value.toLowerCase() && (
                  <span className="text-white text-sm">✓</span>
                )}
              </button>
            ))}
            <button
              onClick={() => {
                const color = prompt("输入 HEX 颜色值 (如 #ff0000):");
                if (color) handleColorChange(color);
              }}
              className="w-9 h-9 rounded-md border-2 border-dashed border-muted-foreground/30 flex items-center justify-center hover:border-primary transition-colors"
              title="自定义颜色"
            >
              <span className="text-xs">🎨</span>
            </button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

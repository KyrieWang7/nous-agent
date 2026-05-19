"use client";

import { useState } from "react";
import { Settings } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function OtherConfig() {
  const [settings, setSettings] = useState(() => {
    const saved = localStorage.getItem("drama-other-settings");
    return saved
      ? JSON.parse(saved)
      : {
          autoSave: true,
          autoSaveInterval: 30,
          maxHistorySize: 100,
          assetsBatchGenerateSize: 5,
        };
  });
  const [loading, setLoading] = useState(false);

  const handleUpdateSetting = (key: string, value: unknown) => {
    setSettings({ ...settings, [key]: value });
  };

  const handleSave = async () => {
    setLoading(true);
    try {
      localStorage.setItem("drama-other-settings", JSON.stringify(settings));
      alert("其他配置保存成功");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">其他配置</h2>
        <p className="text-muted-foreground">其他系统设置选项</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Settings className="h-5 w-5" />
            通用设置
          </CardTitle>
          <CardDescription>配置系统通用行为</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="flex items-center justify-between">
            <div className="space-y-0.5">
              <Label>自动保存</Label>
              <p className="text-sm text-muted-foreground">
                自动保存编辑内容
              </p>
            </div>
            <Switch
              checked={settings.autoSave}
              onCheckedChange={(checked) => handleUpdateSetting("autoSave", checked)}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="autoSaveInterval">自动保存间隔 (秒)</Label>
            <Input
              id="autoSaveInterval"
              type="number"
              min={10}
              max={300}
              value={settings.autoSaveInterval}
              onChange={(e) =>
                handleUpdateSetting("autoSaveInterval", parseInt(e.target.value) || 30)
              }
              disabled={!settings.autoSave}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="maxHistorySize">最大历史记录数</Label>
            <Input
              id="maxHistorySize"
              type="number"
              min={10}
              max={1000}
              value={settings.maxHistorySize}
              onChange={(e) =>
                handleUpdateSetting("maxHistorySize", parseInt(e.target.value) || 100)
              }
            />
            <p className="text-sm text-muted-foreground">
              控制在对话历史中保留的最大消息数
            </p>
          </div>

          <div className="space-y-2">
            <Label htmlFor="assetsBatchGenerateSize">素材批量生成数量</Label>
            <Input
              id="assetsBatchGenerateSize"
              type="number"
              min={1}
              max={20}
              value={settings.assetsBatchGenerateSize}
              onChange={(e) =>
                handleUpdateSetting("assetsBatchGenerateSize", parseInt(e.target.value) || 5)
              }
            />
            <p className="text-sm text-muted-foreground">
              批量生成图片时的并发数量
            </p>
          </div>

          <div className="pt-4">
            <Button onClick={handleSave} disabled={loading}>
              {loading ? "保存中..." : "保存配置"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

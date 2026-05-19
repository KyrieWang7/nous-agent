"use client";

import { useState } from "react";
import { Video, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface VideoModel {
  id: string;
  name: string;
  apiKey: string;
  baseUrl: string;
  model: string;
}

export function VideoModelConfig() {
  const [models, setModels] = useState<VideoModel[]>(() => {
    const saved = localStorage.getItem("drama-video-models");
    return saved ? JSON.parse(saved) : [];
  });
  const [loading, setLoading] = useState(false);

  const handleAddModel = () => {
    const newModel: VideoModel = {
      id: Date.now().toString(),
      name: "",
      apiKey: "",
      baseUrl: "",
      model: "",
    };
    setModels([...models, newModel]);
  };

  const handleUpdateModel = (id: string, updates: Partial<VideoModel>) => {
    setModels(models.map((m) => (m.id === id ? { ...m, ...updates } : m)));
  };

  const handleDeleteModel = (id: string) => {
    setModels(models.filter((m) => m.id !== id));
  };

  const handleSave = async () => {
    setLoading(true);
    try {
      localStorage.setItem("drama-video-models", JSON.stringify(models));
      alert("视频模型配置保存成功");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">视频模型配置</h2>
        <p className="text-muted-foreground">配置视频生成模型的连接信息</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Video className="h-5 w-5" />
            视频模型列表
          </CardTitle>
          <CardDescription>添加和管理您的视频生成模型配置</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {models.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground">
              <Video className="h-12 w-12 mx-auto mb-4 opacity-50" />
              <p>暂无视频模型配置</p>
              <p className="text-sm">点击下方按钮添加</p>
            </div>
          ) : (
            models.map((model, index) => (
              <div key={model.id} className="border rounded-lg p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium">模型 {index + 1}</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleDeleteModel(model.id)}
                    className="text-destructive hover:text-destructive"
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1">
                    <Label htmlFor={`name-${model.id}`}>名称</Label>
                    <Input
                      id={`name-${model.id}`}
                      placeholder="如: Kling"
                      value={model.name}
                      onChange={(e) => handleUpdateModel(model.id, { name: e.target.value })}
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor={`model-${model.id}`}>模型</Label>
                    <Input
                      id={`model-${model.id}`}
                      placeholder="如: kling-v1"
                      value={model.model}
                      onChange={(e) => handleUpdateModel(model.id, { model: e.target.value })}
                    />
                  </div>
                  <div className="space-y-1 col-span-2">
                    <Label htmlFor={`baseUrl-${model.id}`}>API 地址</Label>
                    <Input
                      id={`baseUrl-${model.id}`}
                      placeholder="https://api.example.com"
                      value={model.baseUrl}
                      onChange={(e) => handleUpdateModel(model.id, { baseUrl: e.target.value })}
                    />
                  </div>
                  <div className="space-y-1 col-span-2">
                    <Label htmlFor={`apiKey-${model.id}`}>API Key</Label>
                    <Input
                      id={`apiKey-${model.id}`}
                      type="password"
                      placeholder="..."
                      value={model.apiKey}
                      onChange={(e) => handleUpdateModel(model.id, { apiKey: e.target.value })}
                    />
                  </div>
                </div>
              </div>
            ))
          )}

          <Button variant="outline" onClick={handleAddModel} className="gap-2">
            <Plus className="h-4 w-4" />
            添加模型
          </Button>

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

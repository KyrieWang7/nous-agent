"use client";

import { useState, useEffect } from "react";
import { Link, Unlink } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function RequestConfig() {
  const [formData, setFormData] = useState({
    baseUrl: "",
    wsBaseUrl: "",
  });
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    // Load from localStorage or defaults
    const baseUrl = localStorage.getItem("DRAMA_API_BASE_URL") || "http://localhost:8003";
    const wsBaseUrl = localStorage.getItem("DRAMA_WS_BASE_URL") || "ws://localhost:8003";
    setFormData({ baseUrl, wsBaseUrl });
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      localStorage.setItem("DRAMA_API_BASE_URL", formData.baseUrl);
      localStorage.setItem("DRAMA_WS_BASE_URL", formData.wsBaseUrl);
      alert("请求地址保存成功");
    } finally {
      setLoading(false);
    }
  };

  const handleReset = () => {
    const defaultData = {
      baseUrl: "http://localhost:8003",
      wsBaseUrl: "ws://localhost:8003",
    };
    setFormData(defaultData);
    localStorage.setItem("DRAMA_API_BASE_URL", defaultData.baseUrl);
    localStorage.setItem("DRAMA_WS_BASE_URL", defaultData.wsBaseUrl);
    alert("已重置为默认地址");
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">请求地址配置</h2>
        <p className="text-muted-foreground">配置后端 API 和 WebSocket 地址</p>
      </div>

      <form onSubmit={handleSubmit}>
        <Card>
          <CardHeader>
            <CardTitle>API 配置</CardTitle>
            <CardDescription>
              设置 ToonFlow 后端服务的地址
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="baseUrl">API 地址</Label>
              <div className="relative">
                <Link className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                <Input
                  id="baseUrl"
                  type="url"
                  placeholder="http://localhost:8003"
                  value={formData.baseUrl}
                  onChange={(e) =>
                    setFormData({ ...formData, baseUrl: e.target.value })
                  }
                  className="pl-10"
                  required
                />
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="wsBaseUrl">WebSocket 地址</Label>
              <div className="relative">
                <Unlink className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                <Input
                  id="wsBaseUrl"
                  type="url"
                  placeholder="ws://localhost:8003"
                  value={formData.wsBaseUrl}
                  onChange={(e) =>
                    setFormData({ ...formData, wsBaseUrl: e.target.value })
                  }
                  className="pl-10"
                  required
                />
              </div>
            </div>

            <div className="flex gap-2 pt-4">
              <Button type="submit" disabled={loading}>
                {loading ? "保存中..." : "保存"}
              </Button>
              <Button type="button" variant="outline" onClick={handleReset}>
                重置
              </Button>
            </div>
          </CardContent>
        </Card>
      </form>
    </div>
  );
}

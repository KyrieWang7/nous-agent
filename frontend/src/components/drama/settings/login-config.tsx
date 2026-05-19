"use client";

import { useState } from "react";
import { Key } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function LoginConfig() {
  const [username, setUsername] = useState(() => {
    return localStorage.getItem("drama-username") || "";
  });
  const [password, setPassword] = useState(() => {
    return localStorage.getItem("drama-password") || "";
  });
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      localStorage.setItem("drama-username", username);
      localStorage.setItem("drama-password", password);
      alert("登录配置保存成功");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">登录配置</h2>
        <p className="text-muted-foreground">配置后端服务的登录凭证</p>
      </div>

      <form onSubmit={handleSubmit}>
        <Card>
          <CardHeader>
            <CardTitle>认证信息</CardTitle>
            <CardDescription>
              输入您的用户名和密码以访问后端 API
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                placeholder="请输入用户名"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                placeholder="请输入密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>

            <Button type="submit" disabled={loading} className="gap-2">
              <Key className="h-4 w-4" />
              {loading ? "保存中..." : "保存配置"}
            </Button>
          </CardContent>
        </Card>
      </form>
    </div>
  );
}

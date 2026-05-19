"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { Send, Loader2, Trash2, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { dramaProjectApi } from "@/core/drama/api";
import { cn } from "@/lib/utils";

interface ChatMessage {
  id: string;
  identity: "user" | "assistant" | "notice";
  role: string;
  data: Array<{ type: "text" | "thinking"; text: string }>;
}

interface Outline {
  id?: number;
  episodeIndex: number;
  title: string;
  chapterRange: number[];
  scenes: Array<{ name: string; description: string }>;
  characters: Array<{ name: string; description: string }>;
  props: Array<{ name: string; description: string }>;
  coreConflict: string;
  openingHook: string;
  endingHook: string;
  outline: string;
  keyEvents: string[];
  emotionalCurve: string;
  visualHighlights: string[];
  classicQuotes: string[];
}

const defaultOutline = (): Outline => ({
  episodeIndex: 1,
  title: "",
  chapterRange: [],
  scenes: [],
  characters: [],
  props: [],
  coreConflict: "",
  openingHook: "",
  endingHook: "",
  outline: "",
  keyEvents: [],
  emotionalCurve: "",
  visualHighlights: [],
  classicQuotes: [],
});

export function OutlineManager({ projectId }: { projectId: number }) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [inputMessage, setInputMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [ws, setWs] = useState<WebSocket | null>(null);
  const [storyline, setStoryline] = useState("");
  const [activeTab, setActiveTab] = useState("storyline");
  const [outlines, setOutlines] = useState<Outline[]>([]);
  const [editModalOpen, setEditModalOpen] = useState(false);
  const [editingOutline, setEditingOutline] = useState<Outline | null>(null);
  const [isAddMode, setIsAddMode] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // Initialize with welcome message
  useEffect(() => {
    if (messages.length === 0) {
      setMessages([
        {
          id: crypto.randomUUID(),
          identity: "assistant",
          role: "助手",
          data: [
            {
              type: "text",
              text: "欢迎使用Toonflow!请选择小说后开始AI对话来生成小说故事线与大纲。如您需要我开始为您工作您可以跟我说开始",
            },
          ],
        },
      ]);
    }
  }, []);

  // Scroll to bottom when messages change
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // Fetch storyline
  const fetchStoryline = useCallback(async () => {
    try {
      const response = await dramaProjectApi.getStoryline(projectId);
      if (response.code === 200) {
        setStoryline(response.data?.content || "");
      }
    } catch (error) {
      console.error("获取故事线失败:", error);
    }
  }, [projectId]);

  // Fetch outlines
  const fetchOutlines = useCallback(async () => {
    try {
      const response = await dramaProjectApi.getOutline(projectId);
      if (response.code === 200) {
        const parsed = (response.data || []).map((item: { id: number; episode: number; data: string }) => {
          try {
            return { ...defaultOutline(), ...JSON.parse(item.data), id: item.id, episodeIndex: item.episode };
          } catch {
            return { ...defaultOutline(), id: item.id, episodeIndex: item.episode };
          }
        });
        setOutlines(parsed);
      }
    } catch (error) {
      console.error("获取大纲失败:", error);
    }
  }, [projectId]);

  useEffect(() => {
    fetchStoryline();
    fetchOutlines();
  }, [fetchStoryline, fetchOutlines]);

  // Save storyline
  const saveStoryline = async () => {
    try {
      await dramaProjectApi.updateStoryline(projectId, storyline);
      alert("故事线保存成功");
      fetchStoryline();
    } catch (error) {
      console.error("保存故事线失败:", error);
      alert("保存失败");
    }
  };

  // WebSocket connection
  const connectWebSocket = useCallback(() => {
    const wsUrl = `ws://localhost:8003/api/drama/outline/agentsOutline?projectId=${projectId}`;
    const newWs = new WebSocket(wsUrl);

    newWs.onopen = () => {
      console.log("WebSocket connected");
    };

    newWs.onmessage = (event) => {
      try {
        const msgData = JSON.parse(event.data);

        switch (msgData.type) {
          case "init":
            console.log("WebSocket initialized");
            break;
          case "stream":
          case "response":
            setMessages((prev) => {
              const lastMsg = prev[prev.length - 1];
              if (lastMsg?.identity === "assistant" && lastMsg.role === "助手") {
                const newData = [...lastMsg.data];
                const lastItem = newData[newData.length - 1];
                if (lastItem?.type === "thinking") {
                  newData[newData.length - 1] = { type: "text", text: msgData.data?.data || "" };
                } else if (lastItem) {
                  lastItem.text += msgData.data?.data || "";
                }
                return [...prev.slice(0, -1), { ...lastMsg, data: newData }];
              }
              return [
                ...prev,
                {
                  id: crypto.randomUUID(),
                  identity: "assistant" as const,
                  role: "助手",
                  data: [{ type: "text" as const, text: msgData.data?.data || "" }],
                },
              ];
            });
            break;
          case "response_end":
            setLoading(false);
            // Refresh data
            fetchStoryline();
            fetchOutlines();
            break;
          case "notice":
            setMessages((prev) => [
              ...prev,
              {
                id: crypto.randomUUID(),
                identity: "notice" as const,
                role: "",
                data: [{ type: "text" as const, text: msgData.data }],
              },
            ]);
            break;
          case "error":
            setMessages((prev) => [
              ...prev,
              {
                id: crypto.randomUUID(),
                identity: "notice" as const,
                role: "",
                data: [{ type: "text" as const, text: `错误: ${msgData.data}` }],
              },
            ]);
            setLoading(false);
            break;
        }
      } catch (error) {
        console.error("WebSocket message parse error:", error);
      }
    };

    newWs.onerror = () => {
      console.error("WebSocket error");
      setLoading(false);
    };

    newWs.onclose = () => {
      console.log("WebSocket closed");
      setLoading(false);
    };

    setWs(newWs);
    return newWs;
  }, [projectId, fetchStoryline, fetchOutlines]);

  // Send message
  const sendMessage = () => {
    if (!inputMessage.trim() || loading) return;

    const userMessage: ChatMessage = {
      id: crypto.randomUUID(),
      identity: "user",
      role: "用户",
      data: [{ type: "text", text: inputMessage }],
    };

    setMessages((prev) => [...prev, userMessage]);
    setInputMessage("");
    setLoading(true);

    // Add thinking indicator
    setMessages((prev) => [
      ...prev,
      {
        id: crypto.randomUUID(),
        identity: "assistant",
        role: "助手",
        data: [{ type: "thinking", text: "生成中..." }],
      },
    ]);

    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: "msg", data: { type: "user", data: inputMessage } }));
    } else {
      const newWs = connectWebSocket();
      newWs.onopen = () => {
        newWs.send(JSON.stringify({ type: "msg", data: { type: "user", data: inputMessage } }));
      };
    }
  };

  // Clean history
  const cleanHistory = () => {
    setMessages([
      {
        id: crypto.randomUUID(),
        identity: "assistant",
        role: "助手",
        data: [
          {
            type: "text",
            text: "欢迎使用Toonflow!请选择小说后开始AI对话来生成小说故事线与大纲。如您需要我开始为您工作您可以跟我说开始",
          },
        ],
      },
    ]);
    ws?.send(JSON.stringify({ type: "cleanHistory" }));
  };

  // Add outline
  const handleAddOutline = () => {
    setIsAddMode(true);
    setEditingOutline(defaultOutline());
    setEditModalOpen(true);
  };

  // Edit outline
  const handleEditOutline = (outline: Outline) => {
    setIsAddMode(false);
    setEditingOutline({ ...outline });
    setEditModalOpen(true);
  };

  // Save outline
  const handleSaveOutline = async () => {
    if (!editingOutline) return;
    try {
      const data = JSON.stringify(editingOutline);
      if (isAddMode) {
        await dramaProjectApi.addOutline(projectId, data);
      } else {
        await dramaProjectApi.updateOutline(editingOutline.id!, projectId, data);
      }
      fetchOutlines();
      setEditModalOpen(false);
    } catch (error) {
      console.error("保存大纲失败:", error);
    }
  };

  // Delete outline
  const handleDeleteOutline = async (outline: Outline) => {
    if (!confirm("删除大纲将会删除该大纲下的剧本和独有资产，确定吗？")) return;
    try {
      await dramaProjectApi.deleteOutline(outline.id!, projectId);
      fetchOutlines();
    } catch (error) {
      console.error("删除大纲失败:", error);
    }
  };

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      ws?.close();
    };
  }, [ws]);

  return (
    <div className="flex h-full gap-4">
      {/* Left: Chat Panel */}
      <div className="w-1/3 flex flex-col border rounded-lg overflow-hidden">
        <div className="p-3 border-b bg-muted/50">
          <div className="flex items-center justify-between">
            <h3 className="font-semibold">AI 对话</h3>
            <Button variant="ghost" size="sm" onClick={cleanHistory}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-y-auto p-4 space-y-4">
          {messages.map((msg) => (
            <div
              key={msg.id}
              className={cn(
                "flex",
                msg.identity === "user" ? "justify-end" : "justify-start"
              )}
            >
              <div
                className={cn(
                  "max-w-[85%] rounded-lg px-4 py-2",
                  msg.identity === "user"
                    ? "bg-primary text-primary-foreground"
                    : msg.identity === "notice"
                      ? "bg-yellow-100 dark:bg-yellow-900/30 text-yellow-800 dark:text-yellow-200 text-sm"
                      : "bg-muted"
                )}
              >
                <div className="text-xs opacity-70 mb-1">{msg.role}</div>
                {msg.data[0]?.type === "thinking" ? (
                  <div className="flex items-center gap-2">
                    <Loader2 className="h-4 w-4 animate-spin" />
                    <span className="text-sm opacity-70">{msg.data[0].text}</span>
                  </div>
                ) : (
                  <p className="text-sm whitespace-pre-wrap">{msg.data[0]?.text}</p>
                )}
              </div>
            </div>
          ))}
          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        <div className="p-3 border-t">
          <div className="flex gap-2">
            <Input
              value={inputMessage}
              onChange={(e) => setInputMessage(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && sendMessage()}
              placeholder="输入消息..."
              disabled={loading}
              className="flex-1"
            />
            <Button onClick={sendMessage} disabled={loading || !inputMessage.trim()}>
              <Send className="h-4 w-4" />
            </Button>
          </div>
        </div>
      </div>

      {/* Right: Tabs */}
      <div className="flex-1 flex flex-col">
        <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col">
          <TabsList>
            <TabsTrigger value="storyline">故事线</TabsTrigger>
            <TabsTrigger value="outline">大纲</TabsTrigger>
          </TabsList>

          <TabsContent value="storyline" className="flex-1 flex flex-col">
            <Card className="flex-1 flex flex-col">
              <CardHeader>
                <div className="flex items-center justify-between">
                  <CardTitle>故事线</CardTitle>
                  <Button size="sm" onClick={saveStoryline}>
                    保存
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="flex-1">
                <Textarea
                  value={storyline}
                  onChange={(e) => setStoryline(e.target.value)}
                  placeholder="输入故事线内容..."
                  className="h-full min-h-[400px] resize-none"
                />
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="outline" className="flex-1">
            <Card>
              <CardHeader>
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle>大纲管理</CardTitle>
                    <p className="text-sm text-muted-foreground">每一集的详细内容</p>
                  </div>
                  <Button onClick={handleAddOutline}>
                    <Plus className="h-4 w-4 mr-2" />
                    新增大纲
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                {outlines.length === 0 ? (
                  <div className="text-center py-12 text-muted-foreground">
                    <p>暂无大纲数据</p>
                    <Button variant="outline" className="mt-4" onClick={handleAddOutline}>
                      创建第一个大纲
                    </Button>
                  </div>
                ) : (
                  <div className="space-y-4">
                    {outlines.map((item) => (
                      <div key={item.id} className="border rounded-lg p-4 space-y-3">
                        <div className="flex items-center justify-between">
                          <div className="flex items-center gap-3">
                            <span className="px-3 py-1 bg-primary/10 text-primary rounded-full text-sm font-medium">
                              第 {item.episodeIndex} 集
                            </span>
                            <span className="font-semibold">{item.title || "未命名"}</span>
                          </div>
                          <div className="flex gap-2">
                            <Button variant="ghost" size="sm" onClick={() => handleEditOutline(item)}>
                              编辑
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => handleDeleteOutline(item)}
                              className="text-destructive hover:text-destructive"
                            >
                              删除
                            </Button>
                          </div>
                        </div>
                        <div className="grid grid-cols-3 gap-3 text-sm">
                          <div className="bg-muted rounded p-2">
                            <span className="opacity-70">章节范围：</span>
                            {item.chapterRange.length > 0
                              ? item.chapterRange.map((i) => `第${i}章`).join("、")
                              : "—"}
                          </div>
                          <div className="bg-muted rounded p-2">
                            <span className="opacity-70">核心冲突：</span>
                            {item.coreConflict || "—"}
                          </div>
                          <div className="bg-muted rounded p-2">
                            <span className="opacity-70">黄金3秒：</span>
                            {item.openingHook || "—"}
                          </div>
                        </div>
                        {item.outline && (
                          <div className="bg-muted rounded p-3 text-sm">
                            <span className="opacity-70">剧情主干：</span>
                            <p className="mt-1 line-clamp-2">{item.outline}</p>
                          </div>
                        )}
                        {item.keyEvents.length > 0 && (
                          <div className="flex flex-wrap gap-2">
                            <span className="text-sm opacity-70">关键节点：</span>
                            {item.keyEvents.slice(0, 3).map((event, i) => (
                              <span key={i} className="px-2 py-1 bg-primary/10 text-primary rounded text-xs">
                                {event}
                              </span>
                            ))}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>

      {/* Edit Dialog */}
      <Dialog open={editModalOpen} onOpenChange={setEditModalOpen}>
        <DialogContent className="max-w-3xl max-h-[80vh] overflow-auto">
          <DialogHeader>
            <DialogTitle>{isAddMode ? "新增大纲" : "编辑大纲"}</DialogTitle>
            <DialogDescription>填写大纲详细信息</DialogDescription>
          </DialogHeader>
          {editingOutline && (
            <div className="space-y-6 py-4">
              {/* Basic Info */}
              <div className="space-y-3">
                <h4 className="font-medium">基础信息</h4>
                <div className="grid grid-cols-3 gap-3">
                  <div className="space-y-1">
                    <label className="text-sm">集数</label>
                    <Input
                      type="number"
                      min={1}
                      value={editingOutline.episodeIndex}
                      onChange={(e) =>
                        setEditingOutline({
                          ...editingOutline,
                          episodeIndex: parseInt(e.target.value) || 1,
                        })
                      }
                    />
                  </div>
                  <div className="col-span-2 space-y-1">
                    <label className="text-sm">标题</label>
                    <Input
                      value={editingOutline.title}
                      onChange={(e) =>
                        setEditingOutline({ ...editingOutline, title: e.target.value })
                      }
                      placeholder="请输入本集标题"
                    />
                  </div>
                </div>
              </div>

              {/* Plot Design */}
              <div className="space-y-3">
                <h4 className="font-medium">剧情设计</h4>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1">
                    <label className="text-sm">黄金3秒</label>
                    <Input
                      value={editingOutline.openingHook}
                      onChange={(e) =>
                        setEditingOutline({ ...editingOutline, openingHook: e.target.value })
                      }
                      placeholder="开头吸引观众的亮点"
                    />
                  </div>
                  <div className="space-y-1">
                    <label className="text-sm">结尾悬念</label>
                    <Input
                      value={editingOutline.endingHook}
                      onChange={(e) =>
                        setEditingOutline({ ...editingOutline, endingHook: e.target.value })
                      }
                      placeholder="结尾留下的悬念"
                    />
                  </div>
                  <div className="col-span-2 space-y-1">
                    <label className="text-sm">核心冲突</label>
                    <Input
                      value={editingOutline.coreConflict}
                      onChange={(e) =>
                        setEditingOutline({ ...editingOutline, coreConflict: e.target.value })
                      }
                      placeholder="本集的核心矛盾点"
                    />
                  </div>
                  <div className="col-span-2 space-y-1">
                    <label className="text-sm">剧情主干</label>
                    <Textarea
                      value={editingOutline.outline}
                      onChange={(e) =>
                        setEditingOutline({ ...editingOutline, outline: e.target.value })
                      }
                      placeholder="详细描述本集剧情走向"
                      rows={4}
                    />
                  </div>
                </div>
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditModalOpen(false)}>
              取消
            </Button>
            <Button onClick={handleSaveOutline}>{isAddMode ? "新增" : "保存"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

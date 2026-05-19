"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { Send, Trash2, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { dramaOutlineApi } from "@/core/drama/api";
import { StorylineView } from "./components/storyline-view";
import { OutlineView } from "./components/outline-view";

interface ChatMessage {
  id: string;
  identity: "user" | "assistant" | "notice";
  role: string;
  data: { type: "text" | "thinking"; text: string }[];
}

const WELCOME_MESSAGE: ChatMessage = {
  id: "welcome",
  identity: "assistant",
  role: "助手",
  data: [{ type: "text", text: "欢迎使用Toonflow!请选择小说后开始AI对话来生成小说故事线与大纲。如您需要我开始为您工作您可以跟我说开始" }],
};

export function OutlineManager({ projectId }: { projectId: number }) {
  const [messageList, setMessageList] = useState<ChatMessage[]>([WELCOME_MESSAGE]);
  const [inputMessage, setInputMessage] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamMessageId, setStreamMessageId] = useState<string | null>(null);
  const chatContainerRef = useRef<HTMLDivElement>(null);

  // Fetch storyline on mount
  useEffect(() => {
    fetchStoryline();
  }, [projectId]);

  const scrollToBottom = useCallback(() => {
    if (chatContainerRef.current) {
      chatContainerRef.current.scrollTop = chatContainerRef.current.scrollHeight;
    }
  }, []);

  useEffect(() => {
    scrollToBottom();
  }, [messageList, scrollToBottom]);

  const fetchStoryline = async () => {
    // Storyline is fetched in the StorylineView component
  };

  const sendMessage = async () => {
    if (!inputMessage.trim() || isStreaming) return;

    const userMessage: ChatMessage = {
      id: `user-${Date.now()}`,
      identity: "user",
      role: "用户",
      data: [{ type: "text", text: inputMessage.trim() }],
    };

    setMessageList((prev) => [...prev, userMessage]);
    setInputMessage("");
    setIsStreaming(true);

    // Add thinking message
    const thinkingId = `thinking-${Date.now()}`;
    setMessageList((prev) => [
      ...prev,
      {
        id: thinkingId,
        identity: "assistant",
        role: "助手",
        data: [{ type: "thinking", text: "生成中..." }],
      },
    ]);

    try {
      await new Promise<void>((resolve, reject) => {
        let fullResponse = "";

        dramaOutlineApi.streamOutline(
          projectId,
          inputMessage.trim(),
          (data) => {
            fullResponse += data;

            // Update or create streaming message
            setMessageList((prev) => {
              const existing = prev.find((m) => m.id === streamMessageId);
              if (existing) {
                return prev.map((m) =>
                  m.id === streamMessageId
                    ? { ...m, data: [{ type: "text", text: fullResponse }] }
                    : m
                );
              } else {
                // Create new streaming message
                const newMsg: ChatMessage = {
                  id: `assistant-${Date.now()}`,
                  identity: "assistant",
                  role: "助手",
                  data: [{ type: "text", text: fullResponse }],
                };
                setStreamMessageId(newMsg.id);
                return [...prev.filter((m) => m.id !== thinkingId), newMsg];
              }
            });
          },
          (error) => {
            reject(new Error(error));
          }
        );

        // Mark as complete after stream ends
        setTimeout(() => {
          resolve();
        }, 100);
      });
    } catch (error) {
      console.error("Stream error:", error);
      toast.error("消息发送失败");
    } finally {
      setIsStreaming(false);
      setStreamMessageId(null);
      // Remove thinking message if still present
      setMessageList((prev) => prev.filter((m) => m.id !== thinkingId));
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  };

  const clearHistory = () => {
    setMessageList([WELCOME_MESSAGE]);
    toast.success("对话已清空");
  };

  return (
    <div className="flex h-full gap-4">
      {/* Chat Panel */}
      <div className="w-1/3 flex flex-col border rounded-lg overflow-hidden">
        <div className="p-4 border-b bg-muted/50">
          <h3 className="font-semibold">AI 对话</h3>
        </div>

        {/* Messages */}
        <div ref={chatContainerRef} className="flex-1 overflow-y-auto p-4 space-y-4">
          {messageList.map((msg) => (
            <div
              key={msg.id}
              className={`flex ${
                msg.identity === "user" ? "justify-end" : "justify-start"
              }`}
            >
              <div
                className={`max-w-[85%] rounded-lg px-4 py-2 ${
                  msg.identity === "user"
                    ? "bg-primary text-primary-foreground"
                    : msg.identity === "notice"
                    ? "bg-yellow-100 text-yellow-800 text-sm"
                    : msg.data[0]?.type === "thinking"
                    ? "bg-muted text-muted-foreground"
                    : "bg-muted"
                }`}
              >
                <div className="text-xs font-medium mb-1 opacity-70">
                  {msg.role}
                </div>
                <div className="text-sm whitespace-pre-wrap">
                  {msg.data[0]?.text}
                </div>
              </div>
            </div>
          ))}
          {isStreaming && (
            <div className="flex justify-start">
              <div className="bg-muted rounded-lg px-4 py-2">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            </div>
          )}
        </div>

        {/* Input */}
        <div className="p-4 border-t bg-muted/50">
          <div className="flex gap-2">
            <Input
              value={inputMessage}
              onChange={(e) => setInputMessage(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="输入消息..."
              disabled={isStreaming}
              className="flex-1"
            />
            <Button onClick={sendMessage} disabled={isStreaming || !inputMessage.trim()}>
              <Send className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" onClick={clearHistory}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        </div>
      </div>

      {/* Workspace Panel */}
      <div className="flex-1 border rounded-lg overflow-hidden">
        <Tabs defaultValue="storyline" className="h-full flex flex-col">
          <div className="border-b bg-muted/50 px-4">
            <TabsList>
              <TabsTrigger value="storyline">故事线</TabsTrigger>
              <TabsTrigger value="outline">大纲</TabsTrigger>
            </TabsList>
          </div>
          <div className="flex-1 overflow-auto">
            <TabsContent value="storyline" className="h-full m-0">
              <StorylineView projectId={projectId} />
            </TabsContent>
            <TabsContent value="outline" className="h-full m-0">
              <OutlineView projectId={projectId} />
            </TabsContent>
          </div>
        </Tabs>
      </div>
    </div>
  );
}

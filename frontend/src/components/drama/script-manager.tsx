"use client";

import { useState, useEffect, useCallback } from "react";
import { FileText, Save, Loader2, Download, Image, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import { dramaProjectApi } from "@/core/drama/api";
import { useDramaStore } from "@/core/drama/store";
import { cn } from "@/lib/utils";

interface Element {
  id: number;
  filePath: string;
  intro: string;
  name: string;
  prompt: string;
  remark: string;
  duration: number;
  type: string;
  videoPrompt?: string;
}

interface Script {
  id: number;
  name: string;
  content: string;
  outlineId: number;
  element: Element[];
}

export function ScriptManager({ projectId }: { projectId: number }) {
  const setCurrentScriptId = useDramaStore((s) => s.setCurrentScriptId);
  const [scripts, setScripts] = useState<Script[]>([]);
  const [activeScriptId, setActiveScriptId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [generating, setGenerating] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const [localContent, setLocalContent] = useState<Record<number, string>>({});

  const activeScript = scripts.find((s) => s.id === activeScriptId);

  const fetchScripts = useCallback(async () => {
    setLoading(true);
    try {
      const response = await dramaProjectApi.getScripts(projectId);
      if (response.code === 200) {
        setScripts(response.data || []);
        // Set initial active script
        if (response.data?.length && !activeScriptId) {
          setActiveScriptId(response.data[0].id);
        }
      }
    } catch (error) {
      console.error("获取剧本列表失败:", error);
    } finally {
      setLoading(false);
    }
  }, [projectId, activeScriptId]);

  useEffect(() => {
    fetchScripts();
  }, [fetchScripts]);

  // Sync local content with script content
  useEffect(() => {
    if (activeScript) {
      setLocalContent((prev) => ({
        ...prev,
        [activeScript.id]: activeScript.content || "",
      }));
    }
  }, [activeScript?.id]);

  const handleTabChange = (scriptId: number) => {
    setActiveScriptId(scriptId);
    setCurrentScriptId(scriptId);
  };

  const generateScript = async () => {
    if (!activeScript) return;
    setGenerating(true);
    setElapsed(0);

    const timer = setInterval(() => {
      setElapsed((e) => e + 1);
    }, 1000);

    try {
      await dramaProjectApi.generateScript(activeScript.outlineId, activeScript.id);
      alert("生成剧本成功");
      fetchScripts();
    } catch (error) {
      console.error("生成剧本失败:", error);
      alert("生成剧本失败");
    } finally {
      setGenerating(false);
      clearInterval(timer);
    }
  };

  const saveScript = async () => {
    if (!activeScript) return;
    try {
      const content = localContent[activeScript.id] || "";
      await dramaProjectApi.saveScript(activeScript.outlineId, activeScript.id, content);
      alert("保存成功");
      fetchScripts();
    } catch (error) {
      console.error("保存剧本失败:", error);
      alert("保存失败");
    }
  };

  const exportScripts = () => {
    if (scripts.length === 0) {
      alert("暂无剧本可导出");
      return;
    }

    const content = scripts
      .map((s) => `${s.name}\n\n${s.content || ""}\n\n`)
      .join("\n---\n\n");

    const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `剧本_${new Date().toISOString().slice(0, 10)}.txt`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
  };

  const lineCount = (content: string): number => {
    return Math.max(content.split("\n").length, 20);
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="flex items-center gap-2">
              <FileText className="h-5 w-5" />
              剧本管理
              {scripts.length > 0 && (
                <span className="text-sm font-normal text-muted-foreground">
                  {scripts.length} 集
                </span>
              )}
            </CardTitle>
            <Button onClick={exportScripts} variant="outline" className="gap-2">
              <Download className="h-4 w-4" />
              一键导出剧本
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
          ) : scripts.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              <FileText className="h-12 w-12 mx-auto mb-4 opacity-50" />
              <p>暂无剧本</p>
              <p className="text-sm mt-2">请先生成故事大纲后再生成剧本</p>
            </div>
          ) : (
            <Tabs
              value={activeScriptId?.toString()}
              onValueChange={(v) => handleTabChange(Number(v))}
            >
              <TabsList>
                {scripts.map((script) => (
                  <TabsTrigger key={script.id} value={script.id.toString()}>
                    {script.name}
                  </TabsTrigger>
                ))}
              </TabsList>

              {scripts.map((script) => (
                <TabsContent key={script.id} value={script.id.toString()} className="space-y-4">
                  {/* Elements */}
                  <div className="border rounded-lg">
                    <div className="flex items-center justify-between p-3 border-b bg-muted/50">
                      <h4 className="font-medium flex items-center gap-2">
                        <Image className="h-4 w-4" />
                        关联资产
                      </h4>
                      {script.element?.length > 0 && (
                        <Button size="sm" variant="outline" className="gap-1">
                          <Sparkles className="h-3 w-3" />
                          批量生成
                        </Button>
                      )}
                    </div>
                    <div className="p-4">
                      {script.element?.length > 0 ? (
                        <div className="grid grid-cols-4 gap-4">
                          {script.element.map((el) => (
                            <div
                              key={el.id}
                              className="border rounded-lg overflow-hidden cursor-pointer hover:shadow-md transition-shadow"
                            >
                              <div className="aspect-video bg-muted flex items-center justify-center">
                                {el.filePath ? (
                                  <img
                                    src={el.filePath}
                                    alt={el.name}
                                    className="w-full h-full object-cover"
                                  />
                                ) : (
                                  <Image className="h-8 w-8 text-muted-foreground" />
                                )}
                              </div>
                              <div className="p-2">
                                <p className="font-medium text-sm truncate">{el.name}</p>
                                <span className="text-xs text-muted-foreground">{el.type}</span>
                              </div>
                            </div>
                          ))}
                        </div>
                      ) : (
                        <div className="text-center py-8 text-muted-foreground">
                          <p>该集暂无使用的资产</p>
                        </div>
                      )}
                    </div>
                  </div>

                  {/* Script Content */}
                  <div className="border rounded-lg">
                    <div className="flex items-center justify-between p-3 border-b bg-muted/50">
                      <h4 className="font-medium flex items-center gap-2">
                        <FileText className="h-4 w-4" />
                        剧本内容
                      </h4>
                      <div className="flex items-center gap-3">
                        {script.content && (
                          <span className="text-sm text-muted-foreground">
                            {script.content.length} 字
                          </span>
                        )}
                        {script.element?.length > 0 && (
                          <Button
                            size="sm"
                            onClick={generateScript}
                            disabled={generating}
                            className="gap-1"
                          >
                            {generating ? (
                              <>
                                <Loader2 className="h-3 w-3 animate-spin" />
                                生成中... {elapsed}s
                              </>
                            ) : (
                              <>
                                <Sparkles className="h-3 w-3" />
                                生成剧本
                              </>
                            )}
                          </Button>
                        )}
                      </div>
                    </div>
                    <div className="p-4">
                      {generating ? (
                        <div className="flex flex-col items-center justify-center py-12">
                          <Loader2 className="h-8 w-8 animate-spin mb-4" />
                          <p className="font-medium">剧本生成中...</p>
                          <p className="text-sm text-muted-foreground">
                            已用时 {elapsed} 秒
                          </p>
                        </div>
                      ) : script.content ? (
                        <div className="space-y-4">
                          {/* Notebook style editor */}
                          <div className="flex border rounded-lg overflow-hidden">
                            <div className="w-12 bg-muted border-r flex-shrink-0">
                              {Array.from({ length: lineCount(localContent[script.id] || "") }).map(
                                (_, i) => (
                                  <div
                                    key={i}
                                    className="h-7 flex items-center justify-end pr-2 text-xs text-muted-foreground"
                                  >
                                    {i + 1}
                                  </div>
                                )
                              )}
                            </div>
                            <Textarea
                              value={localContent[script.id] || ""}
                              onChange={(e) =>
                                setLocalContent((prev) => ({
                                  ...prev,
                                  [script.id]: e.target.value,
                                }))
                              }
                              className="flex-1 border-0 rounded-none min-h-[560px] resize-none font-serif"
                              style={{
                                backgroundImage:
                                  "repeating-linear-gradient(transparent, transparent 27px, #e5e7eb 27px, #e5e7eb 28px)",
                                backgroundPosition: "0 12px",
                              }}
                              placeholder="请输入剧本内容..."
                            />
                          </div>
                          <div className="flex items-center justify-between">
                            <span className="text-sm text-muted-foreground">
                              编辑后请记得保存
                            </span>
                            <Button onClick={saveScript} className="gap-2">
                              <Save className="h-4 w-4" />
                              保存剧本
                            </Button>
                          </div>
                        </div>
                      ) : (
                        <div className="text-center py-12 text-muted-foreground">
                          <FileText className="h-8 w-8 mx-auto mb-4 opacity-50" />
                          <p>该集暂无剧本内容</p>
                          <p className="text-sm mt-2">点击上方按钮生成剧本</p>
                        </div>
                      )}
                    </div>
                  </div>
                </TabsContent>
              ))}
            </Tabs>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

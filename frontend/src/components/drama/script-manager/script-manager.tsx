"use client";

import { useEffect, useState } from "react";
import { FileText, Download, Loader2, Image, Zap } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { dramaScriptApi, dramaStoryboardApi } from "@/core/drama/api";

interface ScriptElement {
  id: number;
  name: string;
  type: string;
  intro: string;
  prompt: string;
  filePath: string;
  remark: string;
  duration: number;
}

interface Script {
  id: number;
  name: string;
  content: string;
  outline_id: number;
  element: ScriptElement[];
}

export function ScriptManager({ projectId }: { projectId: number }) {
  const [scripts, setScripts] = useState<Script[]>([]);
  const [selectedScriptId, setSelectedScriptId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [generating, setGenerating] = useState(false);
  const [generatingScriptId, setGeneratingScriptId] = useState<number | null>(null);

  useEffect(() => {
    fetchScripts();
  }, [projectId]);

  const fetchScripts = async () => {
    try {
      setLoading(true);
      const response = await dramaScriptApi.getScripts(projectId);
      if (response.code === 200 && response.data) {
        setScripts(response.data as unknown as Script[]);
        if (response.data.length > 0 && !selectedScriptId) {
          setSelectedScriptId((response.data[0] as Script).id);
        }
      }
    } catch (error) {
      console.error("获取剧本列表失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const selectedScript = scripts.find((s) => s.id === selectedScriptId);

  const handleGenerateScript = async () => {
    if (!selectedScript) return;

    setGenerating(true);
    setGeneratingScriptId(selectedScript.id);

    try {
      // Call the generate API (simplified - actual implementation would use SSE)
      const response = await fetch(`/drama-api/script/${projectId}/generate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          outlineId: selectedScript.outline_id,
          scriptId: selectedScript.id,
        }),
      });

      if (response.ok) {
        toast.success("剧本生成成功");
        fetchScripts();
      }
    } catch (error) {
      console.error("生成剧本失败:", error);
      toast.error("生成剧本失败");
    } finally {
      setGenerating(false);
      setGeneratingScriptId(null);
    }
  };

  const handleSaveScript = async () => {
    if (!selectedScript) return;

    try {
      const response = await dramaScriptApi.updateScript(selectedScript.id, {
        content: selectedScript.content,
      });
      if (response.code === 200) {
        toast.success("保存成功");
      }
    } catch (error) {
      console.error("保存失败:", error);
      toast.error("保存失败");
    }
  };

  const exportScripts = () => {
    if (!scripts.length) {
      toast.warning("暂无剧本可导出");
      return;
    }

    const content = scripts
      .map((s) => `${s.name}\n\n${s.content || "（暂无内容）"}\n\n`)
      .join("");

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

  const handleScriptChange = (scriptId: number) => {
    setSelectedScriptId(scriptId);
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-muted-foreground">加载中...</div>
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-xl font-semibold mb-1">剧本管理</h2>
          <p className="text-sm text-muted-foreground">管理和编辑分集剧本内容</p>
        </div>
        <Button onClick={exportScripts} variant="outline">
          <Download className="h-4 w-4 mr-2" />
          一键导出剧本
        </Button>
      </div>

      {/* Script List */}
      {scripts.length === 0 ? (
        <div className="text-center py-12 bg-card rounded-xl border">
          <div className="text-5xl mb-4">
            <FileText className="h-12 w-12 mx-auto text-muted-foreground" />
          </div>
          <h3 className="text-lg font-medium mb-2">暂无剧本</h3>
          <p className="text-muted-foreground">请先在大纲管理中创建大纲</p>
        </div>
      ) : (
        <div className="space-y-4">
          <Tabs value={String(selectedScriptId)} onValueChange={(v) => handleScriptChange(Number(v))}>
            <TabsList className="mb-4">
              {scripts.map((script) => (
                <TabsTrigger key={script.id} value={String(script.id)}>
                  {script.name}
                </TabsTrigger>
              ))}
            </TabsList>

            {scripts.map((script) => (
              <TabsContent key={script.id} value={String(script.id)} className="space-y-4">
                {/* Asset Elements */}
                <div className="bg-card rounded-xl border shadow-sm overflow-hidden">
                  <div className="px-4 py-3 bg-muted/50 border-b flex items-center gap-2">
                    <Image className="h-4 w-4" />
                    <span className="font-medium">关联资产</span>
                    {script.element?.length > 0 && (
                      <span className="text-xs text-muted-foreground ml-2">
                        ({script.element.length} 个)
                      </span>
                    )}
                  </div>
                  <div className="p-4">
                    {script.element?.length ? (
                      <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-4">
                        {script.element.map((el) => (
                          <div
                            key={el.id}
                            className="border rounded-lg overflow-hidden hover:border-primary transition-colors cursor-pointer"
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
                              <p className="text-sm font-medium truncate">{el.name}</p>
                              <p className="text-xs text-muted-foreground">{el.type}</p>
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
                <div className="bg-card rounded-xl border shadow-sm overflow-hidden">
                  <div className="px-4 py-3 bg-muted/50 border-b flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <FileText className="h-4 w-4" />
                      <span className="font-medium">剧本内容</span>
                      {script.content && (
                        <span className="text-xs text-muted-foreground">
                          {script.content.length} 字
                        </span>
                      )}
                    </div>
                    <div className="flex gap-2">
                      {script.element?.length > 0 && (
                        <Button
                          size="sm"
                          onClick={handleGenerateScript}
                          disabled={generating || generatingScriptId === script.id}
                        >
                          {generating && generatingScriptId === script.id ? (
                            <>
                              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                              生成中...
                            </>
                          ) : (
                            <>
                              <Zap className="h-4 w-4 mr-2" />
                              生成剧本
                            </>
                          )}
                        </Button>
                      )}
                    </div>
                  </div>
                  <div className="p-4">
                    {script.content ? (
                      <div>
                        <textarea
                          value={script.content}
                          onChange={(e) => {
                            const newScripts = scripts.map((s) =>
                              s.id === script.id ? { ...s, content: e.target.value } : s
                            );
                            setScripts(newScripts);
                          }}
                          className="w-full min-h-[400px] p-4 border rounded-lg font-mono text-sm leading-relaxed resize-none focus:outline-none focus:ring-2 focus:ring-primary"
                          placeholder="请输入剧本内容..."
                        />
                        <div className="flex justify-between items-center mt-4">
                          <p className="text-xs text-muted-foreground">编辑后请记得保存</p>
                          <Button onClick={handleSaveScript} size="sm">
                            保存剧本
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <div className="text-center py-12">
                        <FileText className="h-12 w-12 mx-auto text-muted-foreground mb-4" />
                        <p className="text-muted-foreground mb-2">该集暂无剧本内容</p>
                        <p className="text-sm text-muted-foreground">点击上方按钮生成剧本</p>
                      </div>
                    )}
                  </div>
                </div>
              </TabsContent>
            ))}
          </Tabs>
        </div>
      )}
    </div>
  );
}

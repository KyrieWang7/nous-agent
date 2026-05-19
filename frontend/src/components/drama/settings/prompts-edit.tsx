"use client";

import { useState } from "react";
import { FileText, Save } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

interface PromptTemplate {
  id: string;
  name: string;
  content: string;
}

const DEFAULT_PROMPTS: PromptTemplate[] = [
  {
    id: "storyline",
    name: "故事线生成提示词",
    content: "你是一个专业的故事创作者，请根据用户提供的小说内容生成故事线。",
  },
  {
    id: "outline",
    name: "大纲生成提示词",
    content: "你是一个专业的编剧，请根据故事线生成详细的剧本大纲。",
  },
  {
    id: "script",
    name: "剧本生成提示词",
    content: "你是一个专业的编剧，请根据大纲生成详细的分镜剧本。",
  },
];

export function PromptsEdit() {
  const [prompts, setPrompts] = useState<PromptTemplate[]>(() => {
    const saved = localStorage.getItem("drama-prompt-templates");
    return saved ? JSON.parse(saved) : DEFAULT_PROMPTS;
  });
  const [loading, setLoading] = useState(false);

  const handleUpdatePrompt = (id: string, content: string) => {
    setPrompts(prompts.map((p) => (p.id === id ? { ...p, content } : p)));
  };

  const handleResetToDefault = () => {
    setPrompts(DEFAULT_PROMPTS);
    alert("已重置为默认提示词");
  };

  const handleSave = async () => {
    setLoading(true);
    try {
      localStorage.setItem("drama-prompt-templates", JSON.stringify(prompts));
      alert("提示词配置保存成功");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">提示词配置</h2>
        <p className="text-muted-foreground">自定义 AI 生成的提示词模板</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileText className="h-5 w-5" />
            提示词模板
          </CardTitle>
          <CardDescription>
            修改提示词以定制 AI 的生成风格和内容
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {prompts.map((prompt) => (
            <div key={prompt.id} className="space-y-2">
              <Label htmlFor={`prompt-${prompt.id}`}>{prompt.name}</Label>
              <Textarea
                id={`prompt-${prompt.id}`}
                value={prompt.content}
                onChange={(e) => handleUpdatePrompt(prompt.id, e.target.value)}
                rows={4}
                className="font-mono text-sm"
              />
            </div>
          ))}

          <div className="flex gap-2 pt-4">
            <Button onClick={handleSave} disabled={loading} className="gap-2">
              <Save className="h-4 w-4" />
              {loading ? "保存中..." : "保存配置"}
            </Button>
            <Button variant="outline" onClick={handleResetToDefault}>
              重置为默认
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

"use client";

import { useState, useEffect, useCallback } from "react";
import { Image, Plus, Pencil, Trash2, Sparkles, Layers, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { dramaProjectApi } from "@/core/drama/api";
import { cn } from "@/lib/utils";

interface Asset {
  id: number;
  name: string;
  intro: string;
  prompt: string;
  remark: string;
  filePath: string;
  type: string;
  duration: number;
  videoPrompt?: string;
}

interface Script {
  id: number;
  name: string;
}

type AssetType = "role" | "scene" | "props" | "storyboard";

const TYPE_LABELS: Record<AssetType, string> = {
  role: "角色",
  scene: "场景",
  props: "道具",
  storyboard: "分镜",
};

const TYPE_PARAMS: Record<string, string> = {
  角色: "role",
  场景: "scene",
  分镜: "storyboard",
  道具: "props",
};

const ASSET_TYPE = ["role", "scene", "props", "storyboard"] as const;

export function AssetsManager({ projectId }: { projectId: number }) {
  const [assets, setAssets] = useState<Asset[]>([]);
  const [scripts, setScripts] = useState<Script[]>([]);
  const [loading, setLoading] = useState(true);
  const [activeType, setActiveType] = useState<AssetType>("role");
  const [selectedScriptId, setSelectedScriptId] = useState<number | null>(null);

  const [addDialogOpen, setAddDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [generateDialogOpen, setGenerateDialogOpen] = useState(false);
  const [editingAsset, setEditingAsset] = useState<Asset | null>(null);
  const [generating, setGenerating] = useState(false);

  const [formData, setFormData] = useState<Partial<Asset>>({
    name: "",
    intro: "",
    prompt: "",
    remark: "",
    type: "role",
  });

  // Fetch scripts for storyboard
  const fetchScripts = useCallback(async () => {
    try {
      const response = await dramaProjectApi.getScriptList(projectId);
      if (response.code === 200) {
        setScripts(response.data || []);
        if (response.data?.length) {
          setSelectedScriptId(response.data[0].id);
        }
      }
    } catch (error) {
      console.error("获取剧本列表失败:", error);
    }
  }, [projectId]);

  // Fetch assets
  const fetchAssets = useCallback(async () => {
    setLoading(true);
    try {
      if (activeType === "storyboard" && selectedScriptId) {
        const response = await dramaProjectApi.getStoryboard(projectId, selectedScriptId);
        if (response.code === 200) {
          setAssets(response.data || []);
        }
      } else {
        const typeParam = TYPE_PARAMS[Object.keys(TYPE_PARAMS).find(k => TYPE_PARAMS[k as keyof typeof TYPE_PARAMS] === activeType) || "角色"];
        const typeName = Object.entries(TYPE_PARAMS).find(([k, v]) => v === activeType)?.[0] || "角色";
        const response = await dramaProjectApi.getAssets(projectId, typeName);
        if (response.code === 200) {
          setAssets(response.data || []);
        }
      }
    } catch (error) {
      console.error("获取素材失败:", error);
    } finally {
      setLoading(false);
    }
  }, [projectId, activeType, selectedScriptId]);

  useEffect(() => {
    fetchAssets();
  }, [fetchAssets]);

  useEffect(() => {
    if (activeType === "storyboard") {
      fetchScripts();
    }
  }, [activeType, fetchScripts]);

  const handleAddAsset = async () => {
    try {
      await dramaProjectApi.addAsset(projectId, {
        ...formData,
        type: activeType,
      } as Partial<Asset> & { projectId: number });
      fetchAssets();
      setAddDialogOpen(false);
      setFormData({ name: "", intro: "", prompt: "", remark: "", type: activeType });
    } catch (error) {
      console.error("添加素材失败:", error);
    }
  };

  const handleEditAsset = (asset: Asset) => {
    setEditingAsset(asset);
    setFormData({ ...asset });
    setEditDialogOpen(true);
  };

  const handleUpdateAsset = async () => {
    if (!editingAsset) return;
    try {
      await dramaProjectApi.updateAsset(editingAsset.id, {
        name: formData.name,
        intro: formData.intro,
        prompt: formData.prompt,
        remark: formData.remark,
      });
      fetchAssets();
      setEditDialogOpen(false);
    } catch (error) {
      console.error("更新素材失败:", error);
    }
  };

  const handleDeleteAsset = async (asset: Asset) => {
    if (!confirm(`确定要删除「${asset.name}」吗？此操作不可恢复。`)) return;
    try {
      await dramaProjectApi.deleteAsset(asset.id);
      fetchAssets();
    } catch (error) {
      console.error("删除素材失败:", error);
    }
  };

  const handleGenerateImage = async () => {
    if (!editingAsset) return;
    setGenerating(true);
    try {
      const typeName = Object.entries(TYPE_PARAMS).find(([k, v]) => v === activeType)?.[0] || "角色";
      await dramaProjectApi.generateAsset(projectId, typeName, editingAsset.name, editingAsset.prompt || editingAsset.intro || "", editingAsset.id);
      alert("图片生成中...");
      setGenerateDialogOpen(false);
      fetchAssets();
    } catch (error) {
      console.error("生成图片失败:", error);
      alert("生成图片失败");
    } finally {
      setGenerating(false);
    }
  };

  const canAddElement = !(scripts.length === 0 && activeType === "storyboard");

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Image className="h-5 w-5" />
                资产管理
              </CardTitle>
              <CardDescription>管理场景、角色、道具、分镜资源库</CardDescription>
            </div>
            {assets.length > 0 && (
              <div className="text-sm text-muted-foreground">
                {TYPE_LABELS[activeType]}数量: {assets.length}
              </div>
            )}
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Toolbar */}
          <div className="flex items-center justify-between">
            <Tabs value={activeType} onValueChange={(v) => setActiveType(v as AssetType)}>
              <TabsList>
                {ASSET_TYPE.map((type) => (
                  <TabsTrigger key={type} value={type}>
                    {TYPE_LABELS[type]}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>

            <div className="flex gap-2">
              <Button variant="outline" className="gap-2">
                <Layers className="h-4 w-4" />
                批量生成
              </Button>
              <Button onClick={() => setAddDialogOpen(true)} disabled={!canAddElement} className="gap-2">
                <Plus className="h-4 w-4" />
                新增{TYPE_LABELS[activeType]}
              </Button>
            </div>
          </div>

          {/* Script selector for storyboard */}
          {activeType === "storyboard" && scripts.length > 0 && (
            <div className="flex items-center gap-2 p-3 bg-muted/50 rounded-lg">
              <Label className="text-sm">选择剧本：</Label>
              <div className="flex gap-2">
                {scripts.map((script) => (
                  <Button
                    key={script.id}
                    variant={selectedScriptId === script.id ? "default" : "outline"}
                    size="sm"
                    onClick={() => setSelectedScriptId(script.id)}
                  >
                    {script.name}
                  </Button>
                ))}
              </div>
            </div>
          )}

          {/* Assets Table */}
          {loading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
          ) : assets.length === 0 ? (
            <div className="text-center py-12 border-2 border-dashed rounded-lg">
              <Image className="h-12 w-12 mx-auto mb-4 text-muted-foreground opacity-50" />
              <p className="text-muted-foreground mb-2">暂无{TYPE_LABELS[activeType]}元素</p>
              <p className="text-sm text-muted-foreground mb-4">
                点击右上角"新增{TYPE_LABELS[activeType]}"按钮添加
              </p>
              <Button onClick={() => setAddDialogOpen(true)} disabled={!canAddElement}>
                <Plus className="h-4 w-4 mr-2" />
                新增{TYPE_LABELS[activeType]}
              </Button>
            </div>
          ) : (
            <div className="border rounded-lg overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-[200px]">名称</TableHead>
                    <TableHead className="w-[140px]">预览图</TableHead>
                    <TableHead className="w-[200px]">描述</TableHead>
                    <TableHead>提示词</TableHead>
                    <TableHead className="w-[100px]">时长</TableHead>
                    <TableHead className="w-[140px]">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {assets.map((asset) => (
                    <TableRow key={asset.id}>
                      <TableCell className="font-medium">{asset.name || "未命名"}</TableCell>
                      <TableCell>
                        <div className="w-20 h-14 bg-muted rounded flex items-center justify-center overflow-hidden">
                          {asset.filePath ? (
                            <img src={asset.filePath} alt={asset.name} className="w-full h-full object-cover" />
                          ) : (
                            <Image className="h-6 w-6 text-muted-foreground" />
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="max-w-[200px] truncate">{asset.intro || "暂无描述"}</TableCell>
                      <TableCell className="max-w-[200px] truncate">{asset.prompt || "暂无提示词"}</TableCell>
                      <TableCell>{asset.duration ? `${asset.duration}s` : "-"}</TableCell>
                      <TableCell>
                        <div className="flex gap-1">
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => handleEditAsset(asset)}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => {
                              setEditingAsset(asset);
                              setGenerateDialogOpen(true);
                            }}
                          >
                            <Sparkles className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => handleDeleteAsset(asset)}
                            className="text-destructive hover:text-destructive"
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Add Dialog */}
      <Dialog open={addDialogOpen} onOpenChange={setAddDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新增{TYPE_LABELS[activeType]}</DialogTitle>
            <DialogDescription>添加新的{TYPE_LABELS[activeType]}素材</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="name">名称</Label>
              <Input
                id="name"
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                placeholder="输入名称"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="intro">描述</Label>
              <Textarea
                id="intro"
                value={formData.intro}
                onChange={(e) => setFormData({ ...formData, intro: e.target.value })}
                placeholder="输入描述"
                rows={2}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="prompt">提示词</Label>
              <Textarea
                id="prompt"
                value={formData.prompt}
                onChange={(e) => setFormData({ ...formData, prompt: e.target.value })}
                placeholder="输入AI生成提示词"
                rows={3}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="remark">备注</Label>
              <Input
                id="remark"
                value={formData.remark}
                onChange={(e) => setFormData({ ...formData, remark: e.target.value })}
                placeholder="输入备注"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleAddAsset}>添加</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Dialog */}
      <Dialog open={editDialogOpen} onOpenChange={setEditDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>编辑{TYPE_LABELS[activeType]}</DialogTitle>
            <DialogDescription>修改素材信息</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="edit-name">名称</Label>
              <Input
                id="edit-name"
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-intro">描述</Label>
              <Textarea
                id="edit-intro"
                value={formData.intro}
                onChange={(e) => setFormData({ ...formData, intro: e.target.value })}
                rows={2}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-prompt">提示词</Label>
              <Textarea
                id="edit-prompt"
                value={formData.prompt}
                onChange={(e) => setFormData({ ...formData, prompt: e.target.value })}
                rows={3}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-remark">备注</Label>
              <Input
                id="edit-remark"
                value={formData.remark}
                onChange={(e) => setFormData({ ...formData, remark: e.target.value })}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleUpdateAsset}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Generate Image Dialog */}
      <Dialog open={generateDialogOpen} onOpenChange={setGenerateDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>AI 生成图片</DialogTitle>
            <DialogDescription>为「{editingAsset?.name}」生成图片</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {editingAsset?.filePath && (
              <div className="aspect-video bg-muted rounded-lg overflow-hidden">
                <img
                  src={editingAsset.filePath}
                  alt={editingAsset.name}
                  className="w-full h-full object-cover"
                />
              </div>
            )}
            <div className="space-y-2">
              <Label>提示词</Label>
              <p className="text-sm bg-muted p-3 rounded">
                {editingAsset?.prompt || editingAsset?.intro || "暂无提示词"}
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setGenerateDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleGenerateImage} disabled={generating} className="gap-2">
              {generating ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Sparkles className="h-4 w-4" />
              )}
              {generating ? "生成中..." : "生成图片"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

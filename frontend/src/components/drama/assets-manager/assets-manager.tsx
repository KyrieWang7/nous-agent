"use client";

import { useEffect, useState } from "react";
import { Plus, Image, Trash2, Edit, Wand2, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { dramaAssetsApi } from "@/core/drama/api";

type AssetType = "role" | "scene" | "props" | "storyboard";

interface Asset {
  id: number;
  name: string;
  intro: string | null;
  prompt: string | null;
  image_url: string | null;
  type: string;
  project_id: number;
}

const ASSET_TYPES = [
  { label: "角色", value: "role" as AssetType },
  { label: "场景", value: "scene" as AssetType },
  { label: "道具", value: "props" as AssetType },
];

export function AssetsManager({ projectId }: { projectId: number }) {
  const [assets, setAssets] = useState<Asset[]>([]);
  const [loading, setLoading] = useState(true);
  const [currentFilter, setCurrentFilter] = useState<AssetType>("role");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingAsset, setEditingAsset] = useState<Asset | null>(null);
  const [formData, setFormData] = useState({
    name: "",
    intro: "",
    prompt: "",
    type: "role" as AssetType,
  });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchAssets();
  }, [projectId, currentFilter]);

  const fetchAssets = async () => {
    try {
      setLoading(true);
      const response = await dramaAssetsApi.getAssets(projectId, currentFilter);
      if (response.code === 200) {
        setAssets(response.data as unknown as Asset[]);
      }
    } catch (error) {
      console.error("获取资产列表失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const handleFilterChange = (type: AssetType) => {
    setCurrentFilter(type);
  };

  const handleAdd = () => {
    setEditingAsset(null);
    setFormData({
      name: "",
      intro: "",
      prompt: "",
      type: currentFilter,
    });
    setDialogOpen(true);
  };

  const handleEdit = (asset: Asset) => {
    setEditingAsset(asset);
    setFormData({
      name: asset.name,
      intro: asset.intro || "",
      prompt: asset.prompt || "",
      type: asset.type as AssetType,
    });
    setDialogOpen(true);
  };

  const handleDelete = async (asset: Asset) => {
    if (!confirm(`确定要删除 "${asset.name}" 吗？`)) return;

    try {
      const response = await dramaAssetsApi.deleteAsset(asset.id);
      if (response.code === 200) {
        toast.success("删除成功");
        fetchAssets();
      }
    } catch (error) {
      console.error("删除失败:", error);
      toast.error("删除失败");
    }
  };

  const handleSave = async () => {
    if (!formData.name.trim()) {
      toast.warning("请输入名称");
      return;
    }

    setSaving(true);
    try {
      if (editingAsset) {
        const response = await dramaAssetsApi.updateAsset(editingAsset.id, {
          name: formData.name,
          intro: formData.intro,
          prompt: formData.prompt,
        });
        if (response.code === 200) {
          toast.success("更新成功");
        }
      } else {
        const response = await dramaAssetsApi.createAsset(projectId, {
          name: formData.name,
          intro: formData.intro,
          prompt: formData.prompt,
          type: formData.type,
        });
        if (response.code === 200) {
          toast.success("创建成功");
        }
      }
      setDialogOpen(false);
      fetchAssets();
    } catch (error) {
      console.error("保存失败:", error);
      toast.error("保存失败");
    } finally {
      setSaving(false);
    }
  };

  const handlePolishPrompt = async (asset: Asset) => {
    if (!asset.prompt) {
      toast.warning("该资产没有提示词可润色");
      return;
    }

    try {
      const response = await dramaAssetsApi.polishPrompt(asset.prompt);
      if (response.code === 200) {
        toast.success("提示词已润色");
        // Update the asset with polished prompt
        await dramaAssetsApi.updateAsset(asset.id, {
          prompt: response.data,
        });
        fetchAssets();
      }
    } catch (error) {
      console.error("润色失败:", error);
      toast.error("润色失败");
    }
  };

  return (
    <div className="p-6">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-xl font-semibold mb-1">资产管理</h2>
          <p className="text-sm text-muted-foreground">管理场景、角色、道具资源库</p>
        </div>
        <Button onClick={handleAdd}>
          <Plus className="h-4 w-4 mr-2" />
          新增{ASSET_TYPES.find((t) => t.value === currentFilter)?.label}
        </Button>
      </div>

      {/* Filter Tabs */}
      <div className="flex gap-2 mb-6">
        {ASSET_TYPES.map((type) => (
          <Button
            key={type.value}
            variant={currentFilter === type.value ? "default" : "outline"}
            onClick={() => handleFilterChange(type.value)}
          >
            {type.label}
          </Button>
        ))}
      </div>

      {/* Asset Grid */}
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : assets.length === 0 ? (
        <div className="text-center py-12 bg-card rounded-xl border">
          <Image className="h-12 w-12 mx-auto text-muted-foreground mb-4" />
          <h3 className="text-lg font-medium mb-2">暂无{ASSET_TYPES.find((t) => t.value === currentFilter)?.label}元素</h3>
          <p className="text-muted-foreground mb-6">点击右上角按钮添加资产</p>
          <Button onClick={handleAdd}>
            <Plus className="h-4 w-4 mr-2" />
            新增{ASSET_TYPES.find((t) => t.value === currentFilter)?.label}
          </Button>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
          {assets.map((asset) => (
            <div
              key={asset.id}
              className="bg-card rounded-xl border shadow-sm overflow-hidden hover:border-primary transition-colors"
            >
              {/* Preview Image */}
              <div className="aspect-video bg-muted flex items-center justify-center">
                {asset.image_url ? (
                  <img
                    src={asset.image_url}
                    alt={asset.name}
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <Image className="h-8 w-8 text-muted-foreground" />
                )}
              </div>

              {/* Info */}
              <div className="p-4">
                <h4 className="font-medium mb-1 truncate">{asset.name}</h4>
                <p className="text-xs text-muted-foreground mb-2 line-clamp-2">
                  {asset.intro || "暂无描述"}
                </p>
                {asset.prompt && (
                  <p className="text-xs text-muted-foreground mb-3 line-clamp-2 italic">
                    Prompt: {asset.prompt}
                  </p>
                )}

                {/* Actions */}
                <div className="flex gap-2">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => handleEdit(asset)}
                  >
                    <Edit className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => handlePolishPrompt(asset)}
                  >
                    <Wand2 className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 text-destructive hover:text-destructive"
                    onClick={() => handleDelete(asset)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Add/Edit Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingAsset ? "编辑资产" : "新增资产"}
            </DialogTitle>
          </DialogHeader>

          <div className="space-y-4">
            {!editingAsset && (
              <div>
                <label className="text-sm font-medium mb-2 block">资产类型</label>
                <Select
                  value={formData.type}
                  onValueChange={(v) => setFormData({ ...formData, type: v as AssetType })}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ASSET_TYPES.map((type) => (
                      <SelectItem key={type.value} value={type.value}>
                        {type.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            <div>
              <label className="text-sm font-medium mb-2 block">名称</label>
              <Input
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                placeholder="请输入资产名称"
              />
            </div>

            <div>
              <label className="text-sm font-medium mb-2 block">描述</label>
              <Textarea
                value={formData.intro}
                onChange={(e) => setFormData({ ...formData, intro: e.target.value })}
                placeholder="请输入资产描述..."
                className="min-h-[80px]"
              />
            </div>

            <div>
              <label className="text-sm font-medium mb-2 block">生图提示词</label>
              <Textarea
                value={formData.prompt}
                onChange={(e) => setFormData({ ...formData, prompt: e.target.value })}
                placeholder="请输入提示词..."
                className="min-h-[100px]"
              />
            </div>

            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDialogOpen(false)}>
                取消
              </Button>
              <Button onClick={handleSave} disabled={saving}>
                {saving ? "保存中..." : "保存"}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

"use client";

import { ArrowLeftIcon, EyeIcon, SparklesIcon } from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useMemo, useState } from "react";
import { Streamdown } from "streamdown";

import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import {
  Item,
  ItemActions,
  ItemTitle,
  ItemContent,
  ItemDescription,
} from "@/components/ui/item";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/core/i18n/hooks";
import { useEnableSkill, useSkillDetail, useSkills } from "@/core/skills/hooks";
import type { Skill } from "@/core/skills/type";
import { env } from "@/env";

import { SettingsSection } from "./settings-section";

export function SkillSettingsPage({ onClose }: { onClose?: () => void } = {}) {
  const { t } = useI18n();
  const { skills, isLoading, error } = useSkills();
  const [viewingSkill, setViewingSkill] = useState<string | null>(null);

  if (viewingSkill) {
    return (
      <SkillDetailView
        skillName={viewingSkill}
        onBack={() => setViewingSkill(null)}
      />
    );
  }

  return (
    <SettingsSection
      title={t.settings.skills.title}
      description={t.settings.skills.description}
    >
      {isLoading ? (
        <div className="text-muted-foreground text-sm">{t.common.loading}</div>
      ) : error ? (
        <div>Error: {error.message}</div>
      ) : (
        <SkillSettingsList
          skills={skills}
          onClose={onClose}
          onViewSkill={setViewingSkill}
        />
      )}
    </SettingsSection>
  );
}

function SkillDetailView({
  skillName,
  onBack,
}: {
  skillName: string;
  onBack: () => void;
}) {
  const { skill, isLoading } = useSkillDetail(skillName);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack} className="gap-1.5">
          <ArrowLeftIcon className="size-4" />
          返回
        </Button>
      </div>
      {isLoading ? (
        <div className="text-muted-foreground text-sm py-8 text-center">
          加载中...
        </div>
      ) : skill ? (
        <div className="flex flex-col gap-4">
          <div className="flex items-center gap-3">
            <div className="flex size-10 items-center justify-center rounded-lg bg-primary/10">
              <SparklesIcon className="size-5 text-primary" />
            </div>
            <div>
              <h3 className="text-lg font-semibold">{skill.name}</h3>
              <p className="text-muted-foreground text-sm">
                {skill.description}
              </p>
            </div>
          </div>
          {skill.content ? (
            <div className="rounded-lg border bg-muted/30 p-5">
              <Streamdown>{skill.content}</Streamdown>
            </div>
          ) : (
            <div className="text-muted-foreground text-sm py-4 text-center">
              该技能暂无详细文档。
            </div>
          )}
        </div>
      ) : (
        <div className="text-muted-foreground text-sm py-8 text-center">
          未找到技能信息。
        </div>
      )}
    </div>
  );
}

function SkillSettingsList({
  skills,
  onClose,
  onViewSkill,
}: {
  skills: Skill[];
  onClose?: () => void;
  onViewSkill?: (name: string) => void;
}) {
  const { t } = useI18n();
  const router = useRouter();
  const [filter, setFilter] = useState<string>("public");
  const { mutate: enableSkill } = useEnableSkill();
  const filteredSkills = useMemo(
    () => skills.filter((skill) => skill.category === filter),
    [skills, filter],
  );
  const handleCreateSkill = () => {
    onClose?.();
    router.push("/workspace/chats/new?mode=skill");
  };
  return (
    <div className="flex w-full flex-col gap-4">
      <header className="flex justify-between">
        <div className="flex gap-2">
          <Tabs defaultValue="public" onValueChange={setFilter}>
            <TabsList variant="line">
              <TabsTrigger value="public">{t.common.public}</TabsTrigger>
              <TabsTrigger value="custom">{t.common.custom}</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
        <div>
          <Button size="sm" onClick={handleCreateSkill}>
            <SparklesIcon className="size-4" />
            {t.settings.skills.createSkill}
          </Button>
        </div>
      </header>
      {filteredSkills.length === 0 && (
        <EmptySkill onCreateSkill={handleCreateSkill} />
      )}
      {filteredSkills.length > 0 &&
        filteredSkills.map((skill) => (
          <Item className="w-full" variant="outline" key={skill.name}>
            <ItemContent>
              <ItemTitle>
                <div className="flex items-center gap-2">{skill.name}</div>
              </ItemTitle>
              <ItemDescription className="line-clamp-4">
                {skill.description}
              </ItemDescription>
            </ItemContent>
            <ItemActions>
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onViewSkill?.(skill.name)}
              >
                <EyeIcon className="size-4" />
              </Button>
              <Switch
                checked={skill.enabled}
                disabled={env.NEXT_PUBLIC_STATIC_WEBSITE_ONLY === "true"}
                onCheckedChange={(checked) =>
                  enableSkill({ skillName: skill.name, enabled: checked })
                }
              />
            </ItemActions>
          </Item>
        ))}
    </div>
  );
}

function EmptySkill({ onCreateSkill }: { onCreateSkill: () => void }) {
  const { t } = useI18n();
  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <SparklesIcon />
        </EmptyMedia>
        <EmptyTitle>{t.settings.skills.emptyTitle}</EmptyTitle>
        <EmptyDescription>
          {t.settings.skills.emptyDescription}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button onClick={onCreateSkill}>{t.settings.skills.emptyButton}</Button>
      </EmptyContent>
    </Empty>
  );
}

"use client";

import { ArrowUpRight, Bot, PenLine, type LucideIcon } from "lucide-react";
import { useI18n } from "@/core/i18n/hooks";

import { ProductHeader } from "./product-header";

const labels = {
  "zh-CN": {
    title: "选择工作台",
    agent: "Agent 工作区",
    agentDescription: "发起对话和任务，使用技能与子 Agent 协作。",
    studio: "内容工作台",
    studioDescription: "用影策制作商品图、视频和分镜。",
  },
  "en-US": {
    title: "Choose a workspace",
    agent: "Agent workspace",
    agentDescription: "Start chats and tasks, use skills, and collaborate with subagents.",
    studio: "Content studio",
    studioDescription: "Create product images, videos, and storyboards with Yingce.",
  },
};

function ModuleLink({
  href, title, description, icon: Icon, creative = false,
}: {
  href: string;
  title: string;
  description: string;
  icon: LucideIcon;
  creative?: boolean;
}) {
  return (
    <a
      href={href}
      className="group flex min-h-48 flex-col items-start justify-between gap-8 rounded-2xl border border-current/15 bg-card p-5 transition-colors hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-offset-4 sm:p-7"
      aria-label={title}
    >
      <div className="flex w-full items-start justify-between gap-2">
        <span className={creative
          ? "grid size-10 place-items-center rounded-lg bg-emerald-900/10 text-emerald-900 dark:bg-emerald-100/10 dark:text-emerald-100"
          : "grid size-10 place-items-center rounded-lg bg-foreground/8 text-foreground"}
        >
          <Icon aria-hidden className="size-5" />
        </span>
        <ArrowUpRight aria-hidden className="size-5 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 motion-reduce:transition-none" />
      </div>
      <div>
        <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{description}</p>
      </div>
    </a>
  );
}

export function ModuleLauncher() {
  const { locale, t } = useI18n();
  const copy = labels[locale];

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <ProductHeader homeLabel={t.common.home} />
      <main className="mx-auto w-full max-w-6xl px-4 pt-14 pb-16 sm:px-8 sm:pt-24">
        <h1 className="text-4xl font-normal tracking-tight sm:text-6xl" style={{ fontFamily: '"Songti SC", Georgia, serif' }}>
          {copy.title}
        </h1>
        <nav aria-label={copy.title} className="mt-8 grid max-w-4xl gap-3 sm:mt-12 sm:grid-cols-2 sm:gap-4">
          <ModuleLink href="/workspace/chats/new" title={copy.agent} description={copy.agentDescription} icon={Bot} />
          <ModuleLink href="/creative/" title={copy.studio} description={copy.studioDescription} icon={PenLine} creative />
        </nav>
      </main>
    </div>
  );
}

"use client";

import { useSearchParams } from "next/navigation";
import { useEffect, useMemo } from "react";

import { useI18n } from "@/core/i18n/hooks";
import { cn } from "@/lib/utils";

import { AuroraText } from "../ui/aurora-text";

let waved = false;

export function Welcome({
  className,
  mode,
}: {
  className?: string;
  mode?: "ultra" | "pro" | "thinking" | "flash";
}) {
  const { t } = useI18n();
  const searchParams = useSearchParams();
  const creatingSkill = searchParams.get("mode") === "skill";
  const isUltra = useMemo(() => mode === "ultra", [mode]);
  const colors = useMemo(() => {
    if (isUltra) {
      return ["#262622", "#65635e", "#0b7cff"];
    }
    return ["var(--color-foreground)"];
  }, [isUltra]);

  useEffect(() => {
    waved = true;
  }, []);
  return (
    <div
      className={cn(
        "mx-auto flex w-full flex-col items-center gap-2 px-2 text-center sm:gap-3",
        className,
      )}
    >
      <h1 className="animate-slide-up-fade text-foreground text-2xl font-semibold tracking-tight sm:text-3xl">
        {creatingSkill ? (
          <>
            <span aria-hidden="true">✨ </span>
            {t.welcome.createYourOwnSkill}
          </>
        ) : (
          <>
            <span aria-hidden="true" className={cn("mr-2 inline-block", !waved && "animate-wave")}>
              {isUltra ? "🚀" : "👋"}
            </span>
            <AuroraText colors={colors}>{t.welcome.greeting}</AuroraText>
          </>
        )}
      </h1>
      <p className="animate-slide-up-fade text-muted-foreground max-w-lg text-sm leading-relaxed text-pretty opacity-0 [animation-delay:150ms] motion-reduce:animate-none motion-reduce:opacity-100">
        {creatingSkill ? t.welcome.createYourOwnSkillDescription : t.welcome.description}
      </p>
    </div>
  );
}

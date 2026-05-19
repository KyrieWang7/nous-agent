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
  const isUltra = useMemo(() => mode === "ultra", [mode]);
  const colors = useMemo(() => {
    if (isUltra) {
      return ["#262622", "#65635e", "#0b7cff"];
    }
    return ["var(--color-foreground)"];
  }, [isUltra]);

  const greeting = useMemo(() => {
    return t.welcome.greeting;
  }, [t.welcome.greeting]);

  useEffect(() => {
    waved = true;
  }, []);
  return (
    <div
      className={cn(
        "mx-auto flex w-full flex-col items-center justify-center gap-3 px-8 py-4 text-center",
        className,
      )}
    >
      <div className="animate-slide-up-fade text-2xl font-medium text-foreground">
        {searchParams.get("mode") === "skill" ? (
          `✨ ${t.welcome.createYourOwnSkill} ✨`
        ) : (
          <div className="flex items-center gap-3">
            <div className={cn("inline-block text-2xl", !waved ? "animate-wave" : "")}>
              {isUltra ? "🚀" : "👋"}
            </div>
            <AuroraText colors={colors}>{greeting}</AuroraText>
          </div>
        )}
      </div>
      {searchParams.get("mode") === "skill" ? (
        <div className="animate-slide-up-fade text-muted-foreground max-w-lg text-sm leading-relaxed opacity-0 [animation-delay:150ms]">
          {t.welcome.createYourOwnSkillDescription.includes("\n") ? (
            <pre className="font-sans whitespace-pre">
              {t.welcome.createYourOwnSkillDescription}
            </pre>
          ) : (
            <p>{t.welcome.createYourOwnSkillDescription}</p>
          )}
        </div>
      ) : (
        <div className="animate-slide-up-fade text-muted-foreground max-w-lg text-sm leading-relaxed opacity-0 [animation-delay:150ms]">
          {t.welcome.description.includes("\n") ? (
            <pre className="whitespace-pre">{t.welcome.description}</pre>
          ) : (
            <p>{t.welcome.description}</p>
          )}
        </div>
      )}
    </div>
  );
}

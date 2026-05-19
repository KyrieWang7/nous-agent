"use client";

import { useMemo, useState } from "react";

import type { TokenUsage } from "@/core/threads/types";
import { cn } from "@/lib/utils";

function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

interface TokenUsageIndicatorProps {
  tokenUsage?: TokenUsage | null;
  maxContextTokens?: number;
  className?: string;
  elapsedMs?: number;
}

export function TokenUsageIndicator({
  tokenUsage,
  maxContextTokens = 200_000,
  className,
  elapsedMs,
}: TokenUsageIndicatorProps) {
  const [hovered, setHovered] = useState(false);

  const used = tokenUsage
    ? tokenUsage.inputTokens + tokenUsage.outputTokens
    : 0;

  const { ratio, color, label } = useMemo(() => {
    if (used === 0) {
      return { ratio: 0, color: "text-muted-foreground/50", label: "0" };
    }
    const r = Math.min(used / maxContextTokens, 1);
    let c: string;
    if (r < 0.6) c = "text-muted-foreground";
    else if (r < 0.85) c = "text-amber-500";
    else c = "text-red-500";
    return { ratio: r, color: c, label: formatTokenCount(used) };
  }, [used, maxContextTokens]);

  const size = 18;
  const stroke = 2;
  const radius = (size - stroke) / 2;
  const circumference = 2 * Math.PI * radius;
  const dashOffset = circumference * (1 - ratio);
  const elapsedSec = elapsedMs ? (elapsedMs / 1000).toFixed(1) : null;

  return (
    <div
      className={cn("relative inline-flex items-center gap-1 cursor-default", className)}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        className={cn("-rotate-90", color)}
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeWidth={stroke}
          opacity={0.15}
        />
        {ratio > 0 && (
          <circle
            cx={size / 2}
            cy={size / 2}
            r={radius}
            fill="none"
            stroke="currentColor"
            strokeWidth={stroke}
            strokeDasharray={circumference}
            strokeDashoffset={dashOffset}
            strokeLinecap="round"
            className="transition-all duration-500 ease-out"
          />
        )}
      </svg>
      <span className={cn("text-sm font-normal tabular-nums", color)}>{label}</span>

      {hovered && tokenUsage && used > 0 && (
        <div className="absolute bottom-full left-1/2 z-50 mb-2 -translate-x-1/2 whitespace-nowrap rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">↓ 输入</span>
              <span className="font-medium tabular-nums">
                {formatTokenCount(tokenUsage.inputTokens)}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">↑ 输出</span>
              <span className="font-medium tabular-nums">
                {formatTokenCount(tokenUsage.outputTokens)}
              </span>
            </div>
            {tokenUsage.cacheReadTokens > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">缓存</span>
                <span className="font-medium tabular-nums">
                  {formatTokenCount(tokenUsage.cacheReadTokens)}
                </span>
              </div>
            )}
            {elapsedSec && (
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">耗时</span>
                <span className="font-medium tabular-nums">{elapsedSec}s</span>
              </div>
            )}
            <div className="mt-1 border-t pt-1 text-[10px] text-muted-foreground">
              {formatTokenCount(used)} / {formatTokenCount(maxContextTokens)} 上下文
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

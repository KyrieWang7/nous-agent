"use client";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";
import { Children, type ComponentProps } from "react";

const STAGGER_DELAY_MS = 60;
const STAGGER_DELAY_MS_OFFSET = 250;

export type SuggestionsProps = ComponentProps<"div">;

export const Suggestions = ({
  className,
  children,
  ...props
}: SuggestionsProps) => (
  <div className={cn("flex flex-wrap items-center gap-2", className)} {...props}>
    {Children.map(children, (child, index) =>
      child != null ? (
        <span
          className="animate-fade-in-up inline-block opacity-0"
          style={{
            animationDelay: `${STAGGER_DELAY_MS_OFFSET + index * STAGGER_DELAY_MS}ms`,
          }}
        >
          {child}
        </span>
      ) : (
        child
      ),
    )}
  </div>
);

export type SuggestionProps = Omit<ComponentProps<typeof Button>, "onClick"> & {
  suggestion: React.ReactNode;
  icon?: LucideIcon;
  onClick?: () => void;
};

export const Suggestion = ({
  suggestion,
  onClick,
  className,
  icon: Icon,
  variant = "outline",
  size = "sm",
  children,
  ...props
}: SuggestionProps) => {
  const handleClick = () => {
    onClick?.();
  };

  return (
    <Button
      className={cn(
        "h-9 cursor-pointer rounded-full border-border/85 bg-white/78 px-4 text-[14px] font-medium leading-none text-[var(--manus-text-soft)] shadow-[0_1px_2px_rgba(38,38,34,0.04)] transition-all duration-200 hover:-translate-y-0.5 hover:border-[var(--manus-line-strong)] hover:bg-white hover:text-foreground hover:shadow-[0_8px_20px_rgba(38,38,34,0.08)] [&>svg]:size-4 [&>svg]:stroke-[1.8]",
        className,
      )}
      onClick={handleClick}
      size={size}
      type="button"
      variant={variant}
      {...props}
    >
      {Icon && <Icon />}
      {children || suggestion}
    </Button>
  );
};

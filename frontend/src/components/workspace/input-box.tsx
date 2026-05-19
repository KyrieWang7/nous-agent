"use client";

import type { ChatStatus } from "ai";
import {
  CheckIcon,
  GraduationCapIcon,
  LightbulbIcon,
  Network,
  PaperclipIcon,
  PlusIcon,
  SparklesIcon,
  RocketIcon,
  XIcon,
  ZapIcon,
} from "lucide-react";
import { useSearchParams } from "next/navigation";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
} from "react";

import {
  PromptInput,
  PromptInputActionMenu,
  PromptInputActionMenuContent,
  PromptInputActionMenuItem,
  PromptInputActionMenuTrigger,
  PromptInputAttachment,
  PromptInputAttachments,
  PromptInputBody,
  PromptInputButton,
  PromptInputFooter,
  PromptInputSubmit,
  PromptInputTextarea,
  PromptInputTools,
  usePromptInputAttachments,
  usePromptInputController,
  type PromptInputMessage,
} from "@/components/ai-elements/prompt-input";
import { ConfettiButton } from "@/components/ui/confetti-button";
import {
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { useI18n } from "@/core/i18n/hooks";
import { useModels } from "@/core/models/hooks";
import { useSkills } from "@/core/skills/hooks";
import type { Skill } from "@/core/skills/type";
import type { AgentThreadContext } from "@/core/threads";
import type { TokenUsage } from "@/core/types/thread";
import { cn } from "@/lib/utils";

import {
  ModelSelector,
  ModelSelectorContent,
  ModelSelectorInput,
  ModelSelectorItem,
  ModelSelectorList,
  ModelSelectorName,
  ModelSelectorTrigger,
} from "../ai-elements/model-selector";
import { Suggestion, Suggestions } from "../ai-elements/suggestion";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

import { ModeHoverGuide } from "./mode-hover-guide";
import { TokenUsageIndicator } from "./token-usage-indicator";
import { Tooltip } from "./tooltip";

type InputMode = "flash" | "thinking" | "pro" | "ultra";

function getResolvedMode(
  mode: InputMode | undefined,
  supportsThinking: boolean,
): InputMode {
  if (!supportsThinking && mode !== "flash") {
    return "flash";
  }
  if (mode) {
    return mode;
  }
  return supportsThinking ? "pro" : "flash";
}

export function InputBox({
  className,
  disabled,
  autoFocus,
  status = "ready",
  context,
  extraHeader,
  isNewThread,
  initialValue,
  tokenUsage,
  onContextChange,
  onSubmit,
  onStop,
  ...props
}: Omit<ComponentProps<typeof PromptInput>, "onSubmit"> & {
  assistantId?: string | null;
  status?: ChatStatus;
  disabled?: boolean;
  context: Omit<
    AgentThreadContext,
    "thread_id" | "is_plan_mode" | "thinking_enabled" | "subagent_enabled"
  > & {
    mode: "flash" | "thinking" | "pro" | "ultra" | undefined;
  };
  extraHeader?: React.ReactNode;
  isNewThread?: boolean;
  initialValue?: string;
  onContextChange?: (
    context: Omit<
      AgentThreadContext,
      "thread_id" | "is_plan_mode" | "thinking_enabled" | "subagent_enabled"
    > & {
      mode: "flash" | "thinking" | "pro" | "ultra" | undefined;
    },
  ) => void;
  tokenUsage?: TokenUsage | null;
  onSubmit?: (message: PromptInputMessage) => void;
  onStop?: () => void;
}) {
  const { t } = useI18n();
  const searchParams = useSearchParams();

  const [selectedSkills, setSelectedSkills] = useState<Skill[]>([]);
  const [showSkillPicker, setShowSkillPicker] = useState(false);
  const [skillFilter, setSkillFilter] = useState("");
  const [skillPickerIndex, setSkillPickerIndex] = useState(0);
  const skillPickerRef = useRef<HTMLDivElement>(null);
  const skillListRef = useRef<HTMLDivElement>(null);
  const { models } = useModels();
  const { skills } = useSkills();

  const enabledSkills = useMemo(
    () => skills.filter((s) => s.enabled),
    [skills],
  );

  const filteredSkills = useMemo(() => {
    if (!skillFilter) return enabledSkills;
    const lower = skillFilter.toLowerCase();
    return enabledSkills.filter(
      (s) =>
        s.name.toLowerCase().includes(lower) ||
        s.description.toLowerCase().includes(lower),
    );
  }, [enabledSkills, skillFilter]);

  useEffect(() => {
    setSkillPickerIndex(0);
  }, [filteredSkills.length]);

  useEffect(() => {
    if (models.length === 0) {
      return;
    }
    const currentModel = models.find((m) => m.name === context.model_name);
    const fallbackModel = currentModel ?? models[0]!;
    const supportsThinking = fallbackModel.supports_thinking ?? false;
    const nextModelName = fallbackModel.name;
    const nextMode = getResolvedMode(context.mode, supportsThinking);

    if (context.model_name === nextModelName && context.mode === nextMode) {
      return;
    }

    onContextChange?.({
      ...context,
      model_name: nextModelName,
      mode: nextMode,
    });
  }, [context, models, onContextChange]);

  const selectedModel = useMemo(() => {
    if (models.length === 0) {
      return undefined;
    }
    return models.find((m) => m.name === context.model_name) ?? models[0];
  }, [context.model_name, models]);

  const supportThinking = useMemo(
    () => selectedModel?.supports_thinking ?? false,
    [selectedModel],
  );

  const swarmEnabled = Boolean(context.swarm_enabled);

  const handleModelSelect = useCallback(
    (model_name: string) => {
      const model = models.find((m) => m.name === model_name);
      if (!model) {
        return;
      }
      onContextChange?.({
        ...context,
        model_name,
        mode: getResolvedMode(context.mode, model.supports_thinking ?? false),
      });
    },
    [onContextChange, context, models],
  );

  const handleModeSelect = useCallback(
    (mode: InputMode) => {
      onContextChange?.({
        ...context,
        mode: getResolvedMode(mode, supportThinking),
      });
    },
    [onContextChange, context, supportThinking],
  );

  const handleAddSkill = useCallback(
    (skill: Skill) => {
      setSelectedSkills((prev) => {
        if (prev.some((s) => s.name === skill.name)) return prev;
        return [...prev, skill];
      });
    },
    [],
  );

  const handleRemoveSkill = useCallback(
    (skillName: string) => {
      setSelectedSkills((prev) => prev.filter((s) => s.name !== skillName));
    },
    [],
  );

  const handleSelectSkillFromPicker = useCallback(
    (skill: Skill) => {
      handleAddSkill(skill);
      setShowSkillPicker(false);
      setSkillFilter("");

      const textarea = document.querySelector<HTMLTextAreaElement>(
        "textarea[name='message']",
      );
      if (textarea) {
        const val = textarea.value;
        const slashIdx = val.lastIndexOf("/");
        const before = slashIdx >= 0 ? val.substring(0, slashIdx) : val;
        const textareaValueDescriptor = Object.getOwnPropertyDescriptor(
          window.HTMLTextAreaElement.prototype,
          "value",
        );
        textareaValueDescriptor?.set?.call(textarea, before);
        textarea.dispatchEvent(new Event("input", { bubbles: true }));
        textarea.focus();
      }
    },
    [handleAddSkill],
  );

  const handleSubmit = useCallback(
    async (message: PromptInputMessage) => {
      if (status === "streaming") {
        onStop?.();
        return;
      }
      if (!message.text) {
        return;
      }

      let finalText = message.text;
      if (selectedSkills.length > 0) {
        const skillNames = selectedSkills.map((s) => s.name).join(", ");
        finalText = `[请使用以下技能完成此任务: ${skillNames}]\n\n${finalText}`;
        setSelectedSkills([]);
      }

      onSubmit?.({ ...message, text: finalText });
    },
    [onSubmit, onStop, status, selectedSkills],
  );

  const handleTextareaChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const val = e.currentTarget.value;
      const cursorPos = e.currentTarget.selectionStart ?? val.length;
      const textBeforeCursor = val.substring(0, cursorPos);

      const slashMatch = /(?:^|\s)\/(\S*)$/.exec(textBeforeCursor);
      if (slashMatch && enabledSkills.length > 0) {
        setShowSkillPicker(true);
        setSkillFilter(slashMatch[1] ?? "");
        setSkillPickerIndex(0); // 重置选择索引
      } else {
        setShowSkillPicker(false);
        setSkillFilter("");
      }
    },
    [enabledSkills.length],
  );

  // 选中索引变化时，自动滚动到可见区域
  useEffect(() => {
    if (skillListRef.current && showSkillPicker) {
      const selectedElement = skillListRef.current.children[skillPickerIndex] as HTMLElement;
      if (selectedElement) {
        selectedElement.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    }
  }, [skillPickerIndex, showSkillPicker]);

  const handleTextareaKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (!showSkillPicker) return;

      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSkillPickerIndex((i) =>
          i < filteredSkills.length - 1 ? i + 1 : 0,
        );
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSkillPickerIndex((i) =>
          i > 0 ? i - 1 : filteredSkills.length - 1,
        );
      } else if (e.key === "Enter" && !e.shiftKey) {
        if (filteredSkills[skillPickerIndex]) {
          e.preventDefault();
          e.stopPropagation();
          handleSelectSkillFromPicker(filteredSkills[skillPickerIndex]);
        }
      } else if (e.key === "Escape") {
        e.preventDefault();
        setShowSkillPicker(false);
        setSkillFilter("");
      }
    },
    [showSkillPicker, filteredSkills, skillPickerIndex, handleSelectSkillFromPicker],
  );

  return (
    <PromptInput
      className={cn(
        "rounded-[22px] bg-white shadow-[0_16px_48px_rgba(38,38,34,0.08),0_1px_2px_rgba(38,38,34,0.08)] backdrop-blur-md transition-all duration-300 ease-out hover:shadow-[0_18px_54px_rgba(38,38,34,0.1),0_1px_2px_rgba(38,38,34,0.08)] focus-within:shadow-[0_18px_54px_rgba(38,38,34,0.11),0_0_0_4px_rgba(38,38,34,0.04)] *:data-[slot='input-group']:rounded-[22px]",
        status === "streaming"
          ? "streaming-border"
          : "border border-[var(--manus-line)] hover:border-[var(--manus-line-strong)] focus-within:border-[var(--manus-line-strong)]",
        className,
      )}
      disabled={disabled}
      globalDrop
      multiple
      onSubmit={handleSubmit}
      {...props}
    >
      {extraHeader && (
        <div className="absolute top-0 right-0 left-0 z-10">
          <div className="absolute right-0 bottom-0 left-0 flex items-center justify-center">
            {extraHeader}
          </div>
        </div>
      )}
      <PromptInputAttachments>
        {(attachment) => <PromptInputAttachment data={attachment} />}
      </PromptInputAttachments>
      {selectedSkills.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-3 pt-2">
          {selectedSkills.map((skill) => (
            <span
              key={skill.name}
              className="inline-flex items-center gap-1 rounded-full bg-muted px-2.5 py-0.5 text-sm font-medium text-foreground"
            >
              <SparklesIcon className="size-3" />
              {skill.name}
              <button
                type="button"
                onClick={() => handleRemoveSkill(skill.name)}
                className="ml-0.5 rounded-full p-0.5 hover:bg-accent transition-colors"
              >
                <XIcon className="size-2.5" />
              </button>
            </span>
          ))}
        </div>
      )}
      <PromptInputBody className="absolute top-0 right-0 left-0 z-3">
        <div className="relative w-full">
          <PromptInputTextarea
            className={cn("size-full")}
            disabled={disabled}
            placeholder={t.inputBox.placeholder}
            autoFocus={autoFocus}
            defaultValue={initialValue}
            onChange={handleTextareaChange}
            onKeyDown={handleTextareaKeyDown}
          />
          {showSkillPicker && filteredSkills.length > 0 && (
            <div
              ref={skillPickerRef}
              className="absolute bottom-full left-0 z-50 mb-2 w-72 rounded-xl border bg-white shadow-[0_18px_46px_rgba(38,38,34,0.12)]"
            >
              <div className="px-3 py-2 text-xs font-medium text-muted-foreground border-b">
                选择技能（输入 <kbd className="rounded bg-muted px-1 font-mono">/</kbd> 触发）
              </div>
              <div className="max-h-52 overflow-y-auto p-1" ref={skillListRef}>
                {filteredSkills.map((skill, idx) => {
                  const isSelected = selectedSkills.some(
                    (s) => s.name === skill.name,
                  );
                  return (
                    <button
                      key={skill.name}
                      type="button"
                      disabled={isSelected}
                      onMouseDown={(e) => {
                        e.preventDefault();
                        if (!isSelected) handleSelectSkillFromPicker(skill);
                      }}
                      className={cn(
                        "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
                        idx === skillPickerIndex && "bg-accent",
                        isSelected
                          ? "opacity-40 cursor-default"
                          : "hover:bg-accent cursor-pointer",
                      )}
                    >
                      <SparklesIcon className="size-4 shrink-0 text-muted-foreground" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-medium truncate">
                          {skill.name}
                        </div>
                        <div className="text-muted-foreground text-xs truncate">
                          {skill.description}
                        </div>
                      </div>
                      {isSelected && (
                        <CheckIcon className="size-4 shrink-0 text-foreground" />
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </PromptInputBody>
      <PromptInputFooter className="flex">
        <PromptInputTools>
          <AddAttachmentsButton className="px-2!" />
          <PromptInputActionMenu>
            <ModeHoverGuide
              mode={
                context.mode === "flash" ||
                  context.mode === "thinking" ||
                  context.mode === "pro" ||
                  context.mode === "ultra"
                  ? context.mode
                  : "flash"
              }
            >
              <PromptInputActionMenuTrigger className="gap-1! px-2!">
                <div>
                  {context.mode === "flash" && <ZapIcon className="size-4" />}
                  {context.mode === "thinking" && (
                    <LightbulbIcon className="size-4" />
                  )}
                  {context.mode === "pro" && (
                    <GraduationCapIcon className="size-4" />
                  )}
                  {context.mode === "ultra" && (
                    <RocketIcon className="size-4 text-[var(--manus-blue)]" />
                  )}
                </div>
                <div
                  className={cn(
                    "text-sm font-normal",
                    context.mode === "ultra" ? "golden-text" : "",
                  )}
                >
                  {(context.mode === "flash" && t.inputBox.flashMode) ||
                    (context.mode === "thinking" && t.inputBox.reasoningMode) ||
                    (context.mode === "pro" && t.inputBox.proMode) ||
                    (context.mode === "ultra" && t.inputBox.ultraMode)}
                </div>
              </PromptInputActionMenuTrigger>
            </ModeHoverGuide>
            <PromptInputActionMenuContent className="w-80">
              <DropdownMenuGroup>
                <DropdownMenuLabel className="text-muted-foreground text-xs">
                  {t.inputBox.mode}
                </DropdownMenuLabel>
                <PromptInputActionMenu>
                  <PromptInputActionMenuItem
                    className={cn(
                      context.mode === "flash"
                        ? "text-accent-foreground"
                        : "text-muted-foreground/65",
                    )}
                    onSelect={() => handleModeSelect("flash")}
                  >
                    <div className="flex flex-col gap-2">
                      <div className="flex items-center gap-1 font-bold">
                        <ZapIcon
                          className={cn(
                            "mr-2 size-4",
                            context.mode === "flash" &&
                            "text-accent-foreground",
                          )}
                        />
                        {t.inputBox.flashMode}
                      </div>
                      <div className="pl-7 text-xs">
                        {t.inputBox.flashModeDescription}
                      </div>
                    </div>
                    {context.mode === "flash" ? (
                      <CheckIcon className="ml-auto size-4" />
                    ) : (
                      <div className="ml-auto size-4" />
                    )}
                  </PromptInputActionMenuItem>
                  {supportThinking && (
                    <PromptInputActionMenuItem
                      className={cn(
                        context.mode === "thinking"
                          ? "text-accent-foreground"
                          : "text-muted-foreground/65",
                      )}
                      onSelect={() => handleModeSelect("thinking")}
                    >
                      <div className="flex flex-col gap-2">
                        <div className="flex items-center gap-1 font-bold">
                          <LightbulbIcon
                            className={cn(
                              "mr-2 size-4",
                              context.mode === "thinking" &&
                              "text-accent-foreground",
                            )}
                          />
                          {t.inputBox.reasoningMode}
                        </div>
                        <div className="pl-7 text-xs">
                          {t.inputBox.reasoningModeDescription}
                        </div>
                      </div>
                      {context.mode === "thinking" ? (
                        <CheckIcon className="ml-auto size-4" />
                      ) : (
                        <div className="ml-auto size-4" />
                      )}
                    </PromptInputActionMenuItem>
                  )}
                  <PromptInputActionMenuItem
                    className={cn(
                      context.mode === "pro"
                        ? "text-accent-foreground"
                        : "text-muted-foreground/65",
                    )}
                    onSelect={() => handleModeSelect("pro")}
                  >
                    <div className="flex flex-col gap-2">
                      <div className="flex items-center gap-1 font-bold">
                        <GraduationCapIcon
                          className={cn(
                            "mr-2 size-4",
                            context.mode === "pro" && "text-accent-foreground",
                          )}
                        />
                        {t.inputBox.proMode}
                      </div>
                      <div className="pl-7 text-xs">
                        {t.inputBox.proModeDescription}
                      </div>
                    </div>
                    {context.mode === "pro" ? (
                      <CheckIcon className="ml-auto size-4" />
                    ) : (
                      <div className="ml-auto size-4" />
                    )}
                  </PromptInputActionMenuItem>
                  <PromptInputActionMenuItem
                    className={cn(
                      context.mode === "ultra"
                        ? "text-accent-foreground"
                        : "text-muted-foreground/65",
                    )}
                    onSelect={() => handleModeSelect("ultra")}
                  >
                    <div className="flex flex-col gap-2">
                      <div className="flex items-center gap-1 font-bold">
                        <RocketIcon
                          className={cn(
                            "mr-2 size-4",
                            context.mode === "ultra" && "text-[var(--manus-blue)]",
                          )}
                        />
                        <div
                          className={cn(
                            context.mode === "ultra" && "golden-text",
                          )}
                        >
                          {t.inputBox.ultraMode}
                        </div>
                      </div>
                      <div className="pl-7 text-xs">
                        {t.inputBox.ultraModeDescription}
                      </div>
                    </div>
                    {context.mode === "ultra" ? (
                      <CheckIcon className="ml-auto size-4" />
                    ) : (
                      <div className="ml-auto size-4" />
                    )}
                  </PromptInputActionMenuItem>
                </PromptInputActionMenu>
              </DropdownMenuGroup>
            </PromptInputActionMenuContent>
          </PromptInputActionMenu>
        </PromptInputTools>
        <PromptInputTools>
          <TokenUsageIndicator tokenUsage={tokenUsage} />
          <Tooltip
            content={swarmEnabled ? "Swarm Mode ON" : "Swarm Mode OFF"}
          >
            <PromptInputButton
              aria-label={swarmEnabled ? "Swarm Mode ON" : "Swarm Mode OFF"}
              aria-pressed={swarmEnabled}
              className={cn(
                "min-w-9 gap-1 rounded-full px-2! transition-colors",
                swarmEnabled
                  ? "border border-[#BCD7FF] bg-[#EAF3FF] text-[#1473E6] hover:bg-[#DDEBFF]"
                  : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() =>
                onContextChange?.({
                  ...context,
                  swarm_enabled: !swarmEnabled,
                })
              }
            >
              <Network className="size-4" />
              {swarmEnabled && (
                <span className="text-[10px] font-semibold leading-none">
                  ON
                </span>
              )}
            </PromptInputButton>
          </Tooltip>
          <ModelSelector>
            <ModelSelectorTrigger asChild>
              <PromptInputButton>
                <ModelSelectorName className="text-sm font-normal">
                  {selectedModel?.display_name}
                </ModelSelectorName>
              </PromptInputButton>
            </ModelSelectorTrigger>
            <ModelSelectorContent>
              <ModelSelectorInput placeholder={t.inputBox.searchModels} />
              <ModelSelectorList>
                {models.map((m) => (
                  <ModelSelectorItem
                    key={m.name}
                    value={m.name}
                    onSelect={() => handleModelSelect(m.name)}
                  >
                    <ModelSelectorName>{m.display_name}</ModelSelectorName>
                    {m.name === context.model_name ? (
                      <CheckIcon className="ml-auto size-4" />
                    ) : (
                      <div className="ml-auto size-4" />
                    )}
                  </ModelSelectorItem>
                ))}
              </ModelSelectorList>
            </ModelSelectorContent>
          </ModelSelector>
          <PromptInputSubmit
            className="rounded-full bg-muted text-muted-foreground hover:bg-primary hover:text-primary-foreground"
            disabled={disabled}
            variant="outline"
            status={status}
          />
        </PromptInputTools>
      </PromptInputFooter>
      {isNewThread && searchParams.get("mode") !== "skill" && (
        <div className="absolute right-0 -bottom-18 left-0 z-0 flex items-center justify-center">
          <SuggestionList />
        </div>
      )}
    </PromptInput>
  );
}

function SuggestionList() {
  const { t } = useI18n();
  const { textInput } = usePromptInputController();
  const handleSuggestionClick = useCallback(
    (prompt: string | undefined) => {
      if (!prompt) return;
      textInput.setInput(prompt);
      setTimeout(() => {
        const textarea = document.querySelector<HTMLTextAreaElement>(
          "textarea[name='message']",
        );
        if (textarea) {
          const selStart = prompt.indexOf("[");
          const selEnd = prompt.indexOf("]");
          if (selStart !== -1 && selEnd !== -1) {
            textarea.setSelectionRange(selStart, selEnd + 1);
            textarea.focus();
          }
        }
      }, 500);
    },
    [textInput],
  );
  return (
    <Suggestions className="min-h-12 w-fit items-start gap-2.5">
      <ConfettiButton
        className="h-9 cursor-pointer rounded-full border-border/85 bg-white/78 px-4 text-[14px] font-medium leading-none text-[var(--manus-text-soft)] shadow-[0_1px_2px_rgba(38,38,34,0.04)] transition-all duration-200 hover:-translate-y-0.5 hover:border-[var(--manus-line-strong)] hover:bg-white hover:text-foreground hover:shadow-[0_8px_20px_rgba(38,38,34,0.08)] [&>svg]:size-4 [&>svg]:stroke-[1.8]"
        variant="outline"
        size="sm"
        onClick={() => handleSuggestionClick(t.inputBox.surpriseMePrompt)}
      >
        <SparklesIcon className="size-4" /> {t.inputBox.surpriseMe}
      </ConfettiButton>
      {t.inputBox.suggestions.map((suggestion) => (
        <Suggestion
          key={suggestion.suggestion}
          icon={suggestion.icon}
          suggestion={suggestion.suggestion}
          onClick={() => handleSuggestionClick(suggestion.prompt)}
        />
      ))}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Suggestion icon={PlusIcon} suggestion={t.common.create} />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuGroup>
            {t.inputBox.suggestionsCreate.map((suggestion, index) =>
              "type" in suggestion && suggestion.type === "separator" ? (
                <DropdownMenuSeparator key={index} />
              ) : (
                !("type" in suggestion) && (
                  <DropdownMenuItem
                    key={suggestion.suggestion}
                    onClick={() => handleSuggestionClick(suggestion.prompt)}
                  >
                    {suggestion.icon && <suggestion.icon className="size-4" />}
                    {suggestion.suggestion}
                  </DropdownMenuItem>
                )
              ),
            )}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </Suggestions>
  );
}

function AddAttachmentsButton({ className }: { className?: string }) {
  const { t } = useI18n();
  const attachments = usePromptInputAttachments();
  return (
    <Tooltip content={t.inputBox.addAttachments}>
      <PromptInputButton
        className={cn("px-2!", className)}
        onClick={() => attachments.openFileDialog()}
      >
        <PaperclipIcon className="size-4" />
      </PromptInputButton>
    </Tooltip>
  );
}

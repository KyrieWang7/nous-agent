"use client";

import {
  ArrowRightIcon,
  CheckIcon,
  SquareIcon,
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
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type FormEvent,
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
import type { AgentRunIntent } from "@/core/threads";
import type { InputDelivery } from "@/core/threads/inbox";
import type { TokenUsage } from "@/core/types/thread";
import { cn } from "@/lib/utils";

import {
  ModelSelector,
  ModelSelectorContent,
  ModelSelectorEmpty,
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
  running = false,
  canSteer = false,
  stopping = false,
  submitting = false,
  context,
  initialValue,
  tokenUsage,
  onContextChange,
  onSubmit,
  onStop,
  ...props
}: Omit<ComponentProps<typeof PromptInput>, "onSubmit"> & {
  assistantId?: string | null;
  running?: boolean;
  canSteer?: boolean;
  stopping?: boolean;
  submitting?: boolean;
  disabled?: boolean;
  context: AgentRunIntent;
  initialValue?: string;
  onContextChange?: (context: AgentRunIntent) => void;
  tokenUsage?: TokenUsage | null;
  onSubmit?: (
    message: PromptInputMessage,
    delivery: InputDelivery,
  ) => void | Promise<void>;
  onStop?: () => void;
}) {
  const { t } = useI18n();
  const { textInput, attachments } = usePromptInputController();
  const hasDraft = Boolean(textInput.value.trim() || attachments.files.length);
  const canSend = hasDraft && !disabled && !submitting;
  const showSteer = canSteer && hasDraft;
  const primaryStops = running && (!hasDraft || disabled) && !submitting;
  const sendLabel = running ? t.sessionInbox.queueSend : t.subagentWorkspace.mainSend;
  const stopLabel = t.subagentWorkspace.mainStop;
  const stopHint = stopLabel + " (Esc Esc)";
  const steerButton = useRef<HTMLButtonElement>(null);

  const [selectedSkills, setSelectedSkills] = useState<Skill[]>([]);
  const [showSkillPicker, setShowSkillPicker] = useState(false);
  const [skillFilter, setSkillFilter] = useState("");
  const [skillPickerIndex, setSkillPickerIndex] = useState(0);
  const skillPickerRef = useRef<HTMLDivElement>(null);
  const skillListRef = useRef<HTMLDivElement>(null);
  const { models, isLoading: modelsLoading, error: modelsError } = useModels();
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
      const resolvedMode = getResolvedMode(mode, supportThinking);
      onContextChange?.({
        ...context,
        mode: resolvedMode,
        swarm_enabled: resolvedMode === "ultra" && context.swarm_enabled,
      });
    },
    [onContextChange, context, supportThinking],
  );

  const handleAddSkill = useCallback((skill: Skill) => {
    setSelectedSkills((prev) => {
      if (prev.some((s) => s.name === skill.name)) return prev;
      return [...prev, skill];
    });
  }, []);

  const handleRemoveSkill = useCallback((skillName: string) => {
    setSelectedSkills((prev) => prev.filter((s) => s.name !== skillName));
  }, []);

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
    async (message: PromptInputMessage, event: FormEvent<HTMLFormElement>) => {
      if (!message.text.trim() && !message.files?.length) return;
      const delivery = (event.nativeEvent as SubmitEvent).submitter?.getAttribute(
        "data-delivery",
      );

      let finalText = message.text;
      if (selectedSkills.length > 0) {
        const skillNames = selectedSkills.map((s) => s.name).join(", ");
        finalText = `[请使用以下技能完成此任务: ${skillNames}]\n\n${finalText}`;
      }

      await onSubmit?.(
        { ...message, text: finalText },
        canSteer && delivery === "next_step" ? "next_step" : "next_turn",
      );
      setSelectedSkills([]);
    },
    [onSubmit, canSteer, selectedSkills],
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
      const selectedElement = skillListRef.current.children[
        skillPickerIndex
      ] as HTMLElement;
      if (selectedElement) {
        selectedElement.scrollIntoView({
          block: "nearest",
          behavior: "smooth",
        });
      }
    }
  }, [skillPickerIndex, showSkillPicker]);

  const handleTextareaKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey || e.altKey)) {
        if (e.shiftKey || e.altKey || e.getModifierState("AltGraph") || (e.ctrlKey && e.metaKey)) {
          return;
        }
        if (canSend && showSteer && steerButton.current) {
          e.preventDefault();
          e.currentTarget.form?.requestSubmit(steerButton.current);
        }
        return;
      }
      if (!showSkillPicker) return;

      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSkillPickerIndex((i) => (i < filteredSkills.length - 1 ? i + 1 : 0));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSkillPickerIndex((i) => (i > 0 ? i - 1 : filteredSkills.length - 1));
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
    [
      showSkillPicker,
      filteredSkills,
      skillPickerIndex,
      handleSelectSkillFromPicker,
      canSend,
      showSteer,
    ],
  );

  return (
    <PromptInput
      className={cn(
        "bg-card rounded-2xl shadow-sm transition-shadow focus-within:ring-2 focus-within:ring-ring/25 *:data-[slot=input-group]:rounded-2xl *:data-[slot=input-group]:border-0 *:data-[slot=input-group]:shadow-none *:data-[slot=input-group]:ring-0",
        running
          ? "streaming-border"
          : "border border-border focus-within:border-ring/45",
        className,
      )}
      disabled={disabled}
      globalDrop
      multiple
      onSubmit={handleSubmit}
      {...props}
    >
      <PromptInputAttachments>
        {(attachment) => <PromptInputAttachment data={attachment} />}
      </PromptInputAttachments>
      {selectedSkills.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-3 pt-2">
          {selectedSkills.map((skill) => (
            <span
              key={skill.name}
              className="bg-muted text-foreground inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-sm font-medium"
            >
              <SparklesIcon className="size-3" />
              {skill.name}
              <button
                type="button"
                onClick={() => handleRemoveSkill(skill.name)}
                className="hover:bg-accent ml-0.5 rounded-full p-0.5 transition-colors"
              >
                <XIcon className="size-2.5" />
              </button>
            </span>
          ))}
        </div>
      )}
      <PromptInputBody>
        <div className="relative min-w-0 w-full">
          <PromptInputTextarea
            className="block w-full px-4"
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
              className="bg-popover absolute bottom-full left-0 z-50 mb-2 w-full max-w-72 rounded-xl border shadow-lg"
            >
              <div className="text-muted-foreground border-b px-3 py-2 text-xs font-medium">
                选择技能（输入{" "}
                <kbd className="bg-muted rounded px-1 font-mono">/</kbd> 触发）
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
                          ? "cursor-default opacity-40"
                          : "hover:bg-accent cursor-pointer",
                      )}
                    >
                      <SparklesIcon className="text-muted-foreground size-4 shrink-0" />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">
                          {skill.name}
                        </div>
                        <div className="text-muted-foreground truncate text-xs">
                          {skill.description}
                        </div>
                      </div>
                      {isSelected && (
                        <CheckIcon className="text-foreground size-4 shrink-0" />
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </PromptInputBody>
      <PromptInputFooter className="flex-wrap">
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
                            context.mode === "ultra" &&
                              "text-[var(--manus-blue)]",
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
        <PromptInputTools className="ml-auto min-w-0 justify-end">
          {tokenUsage && tokenUsage.inputTokens + tokenUsage.outputTokens > 0 && (
            <TokenUsageIndicator tokenUsage={tokenUsage} />
          )}
          <Tooltip content={swarmEnabled ? "Swarm Mode ON" : "Swarm Mode OFF"}>
            <PromptInputButton
              aria-label={swarmEnabled ? "Swarm Mode ON" : "Swarm Mode OFF"}
              aria-pressed={swarmEnabled}
              className={cn(
                "min-w-9 shrink-0 gap-1 rounded-full px-2! transition-colors",
                swarmEnabled
                  ? "border border-[#BCD7FF] bg-[#EAF3FF] text-[#1473E6] hover:bg-[#DDEBFF]"
                  : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() =>
                onContextChange?.({
                  ...context,
                  swarm_enabled: !swarmEnabled,
                  mode: !swarmEnabled ? "ultra" : context.mode,
                })
              }
            >
              <Network className="size-4" />
              {swarmEnabled && (
                <span className="text-[10px] leading-none font-semibold">
                  ON
                </span>
              )}
            </PromptInputButton>
          </Tooltip>
          <ModelSelector>
            <ModelSelectorTrigger asChild>
              <PromptInputButton
                className="min-w-0 max-w-28 sm:max-w-56"
                title={modelsError ? t.inputBox.modelUnavailable : undefined}
              >
                <ModelSelectorName className="truncate text-sm font-normal">
                  {selectedModel?.display_name ??
                    selectedModel?.name ??
                    (modelsLoading
                      ? t.inputBox.modelLoading
                      : t.inputBox.modelUnavailable)}
                </ModelSelectorName>
              </PromptInputButton>
            </ModelSelectorTrigger>
            <ModelSelectorContent>
              <ModelSelectorInput placeholder={t.inputBox.searchModels} />
              <ModelSelectorList>
                <ModelSelectorEmpty>
                  {modelsLoading
                    ? t.inputBox.modelLoading
                    : t.inputBox.modelUnavailable}
                </ModelSelectorEmpty>
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
          {running && !primaryStops && (
            <PromptInputButton
              type="button"
              aria-label={stopLabel}
              title={stopHint}
              disabled={stopping}
              onClick={onStop}
            >
              <SquareIcon className="size-4" />
            </PromptInputButton>
          )}
          {showSteer && (
            <PromptInputButton
              ref={steerButton}
              type="submit"
              data-delivery="next_step"
              disabled={!canSend}
              aria-label={t.sessionInbox.nextStep}
              title={t.sessionInbox.nextStep + " (Ctrl/⌘ + Enter)"}
            >
              <ArrowRightIcon className="size-4" />
            </PromptInputButton>
          )}
          <PromptInputSubmit
            className="bg-muted text-muted-foreground hover:bg-primary hover:text-primary-foreground shrink-0 rounded-full"
            disabled={primaryStops ? stopping : !canSend}
            variant="outline"
            status={primaryStops ? "streaming" : submitting ? "submitted" : "ready"}
            type={primaryStops ? "button" : "submit"}
            aria-busy={submitting}
            aria-label={submitting ? t.subagentWorkspace.sending : primaryStops ? stopLabel : sendLabel}
            title={primaryStops ? stopHint : running && !submitting ? sendLabel : undefined}
            onClick={primaryStops ? onStop : undefined}
          />
        </PromptInputTools>
      </PromptInputFooter>
    </PromptInput>
  );
}

export function SuggestionList() {
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
    <Suggestions className="justify-center gap-2">
      <ConfettiButton
        className="border-border/85 hover:text-foreground h-9 cursor-pointer rounded-full bg-white/78 px-4 text-[14px] leading-none font-medium text-[var(--manus-text-soft)] shadow-[0_1px_2px_rgba(38,38,34,0.04)] transition-all duration-200 hover:-translate-y-0.5 hover:border-[var(--manus-line-strong)] hover:bg-white hover:shadow-[0_8px_20px_rgba(38,38,34,0.08)] [&>svg]:size-4 [&>svg]:stroke-[1.8]"
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

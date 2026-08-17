/**
 * Message types for the chat thread.
 *
 * These plain TypeScript interfaces define the Agent API message projection.
 */

export type ImageDetail = "auto" | "low" | "high";

export interface MessageContentImageUrl {
  type: "image_url";
  image_url: string | { url: string; detail?: ImageDetail };
}

export interface MessageContentText {
  type: "text";
  text: string;
}

export type MessageContentComplex = MessageContentText | MessageContentImageUrl;

export type MessageContent = string | MessageContentComplex[];

export type MessageAdditionalKwargs = Record<string, unknown>;

export interface BaseMessage {
  additional_kwargs?: MessageAdditionalKwargs;
  content: MessageContent;
  id?: string;
  name?: string;
  response_metadata?: Record<string, unknown>;
}

export interface HumanMessage extends BaseMessage {
  type: "human";
  example?: boolean;
}

export interface ToolCall {
  name: string;
  args: Record<string, any>;
  id?: string;
  type?: "tool_call";
}

export interface InvalidToolCall {
  name?: string;
  args?: string;
  id?: string;
  error?: string;
  type?: "invalid_tool_call";
}

export interface UsageMetadata {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  input_token_details?: {
    audio?: number;
    cache_read?: number;
    cache_creation?: number;
  };
  output_token_details?: {
    audio?: number;
    reasoning?: number;
  };
}

export interface AIMessage extends BaseMessage {
  type: "ai";
  example?: boolean;
  tool_calls?: ToolCall[];
  invalid_tool_calls?: InvalidToolCall[];
  usage_metadata?: UsageMetadata;
}

export interface ToolMessage extends BaseMessage {
  type: "tool";
  status?: "error" | "success";
  tool_call_id: string;
  artifact?: any;
}

export interface SystemMessage extends BaseMessage {
  type: "system";
}

export interface FunctionMessage extends BaseMessage {
  type: "function";
}

export interface RemoveMessage extends BaseMessage {
  type: "remove";
}

export type Message =
  | HumanMessage
  | AIMessage
  | ToolMessage
  | SystemMessage
  | FunctionMessage
  | RemoveMessage;

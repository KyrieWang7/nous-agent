import {
  CompassIcon,
  GraduationCapIcon,
  ImageIcon,
  MicroscopeIcon,
  PenLineIcon,
  ShapesIcon,
  SparklesIcon,
  VideoIcon,
} from "lucide-react";

import type { Translations } from "./types";

export const enUS: Translations = {
  workbenchPanel: {
    title: "Workspace",
    searchFiles: "Search workspace files",
    emptyDirectory: "This folder is empty.",
    noMatches: "No matching files.",
    searchLimit: "Results were limited. Enter a more specific search.",
    backToFiles: "Back to files",
    ready: "Ready",
    waiting: "Waiting",
    files: "Files",
    filesHint:
      "View, preview and download actual files from this conversation.",
    noFile: "No file selected",
    selectFile: "Select a file to view its contents",
    fileError: "Could not load file:",
    swarm: "Swarm",
    swarmHint: "View the actual team, task status and collaboration messages.",
    connecting: "Connecting…",
    disconnected: "Connection interrupted",
    closed: "Team ended",
    all: "All",
    noMessages: "No collaboration messages yet.",
    resize: "Resize collaboration panel",
  },
  pluginSettings: {
    installed: "Installed",
    discover: "Discover",
    install: "Install",
    switchVersion: "Switch version",
    uninstall: "Uninstall",
    confirmRemove: "Uninstall this plugin? Existing runs can finish using it.",
    noCatalog: "No packages are available in the server catalog.",
    title: "Plugins",
    description:
      "Manage installed server plugins and their tools. Validated changes apply to new tasks; existing runs retain their plugins until completion.",
    host: "Plugin host",
    hostHint:
      "Turning this off excludes plugins from new tasks and preserves individual enablement choices.",
    empty:
      "No plugins found in the configured directories. Install a plugin.json package and refresh to discover it.",
    reload: "Reload runtime",
    applying: "Validating and applying…",
    loaded: "Loaded",
    disabled: "Disabled",
    draining: "Waiting for old runs",
    waiting: "Waiting for plugin host",
    closeFailed: "Old runtime cleanup failed",
    toggle: "Enable plugin",
    retained:
      "{count} runs still retain an older configuration of this plugin. It is released when they finish.",
    loadFailed:
      "Could not load the new configuration. The existing runtime was preserved:",
  },
  questionReply: {
    summary: "Clarification",
    skipped: "Skipped",
    closed: "Closed",
    options: "Answer options",
    answer: "Write an answer",
    skip: "Skip",
    submit: "Send answer",
  },
  sessionInbox: {
    queueSend: "Queue message",
    queuedCount: "{count} messages queued",
    fork: "Fork from this run",
    edit: "Edit queued message",
    save: "Save changes",
    steer: "Send to current turn",
    changed:
      "This message has changed. You can copy your edits before closing.",
    title: "Session inbox",
    nextStep: "Guide current task",
    unconfirmed:
      "The last delivery is unconfirmed. Check acceptance before retrying with the same message ID.",
    recover: "Check delivery",
    withdraw: "Withdraw",
    attachment: "Attachment message",
    loadError: "Could not load session inbox",
    statuses: {
      pending: "Pending",
      claimed: "Preparing",
      admitted: "Admitted",
      cancelled: "Withdrawn",
      rejected: "Not admitted",
    },
  },
  subagentWorkspace: {
    noTerminal: "No terminal record",
    waitEnded: "Wait ended",
    mainSend: "Send message",
    mainStop: "Stop main conversation",
    mainBusy:
      "The main conversation is running. You can edit a draft and send it when the run ends.",
    planMode: "Plan mode",

    title: "Subagents",
    plan: "Plan",
    main: "Main conversation",
    empty: "No child sessions yet",
    emptyHint: "Delegated sessions and their execution state will appear here.",
    select: "Select a child session",
    selectHint:
      "Inspect its conversation, send guidance, or continue earlier work.",
    refresh: "Refresh sessions",
    close: "Close execution panel",
    open: "Open child session",
    cancel: "Stop this subagent",
    canceling: "Stopping; waiting for resources to be released…",
    wait: "Wait for result",
    waiting: "Waiting…",
    waitTimeout: "Still running. This wait ended; the child continues working.",
    send: "Send guidance",
    continue: "Continue session",
    sending: "Submitting…",
    activeHint:
      "Guidance is admitted at the next model step. Unconsumed messages remain pending.",
    idleHint: "Continue with the saved child history in a new run.",
    busyHint:
      "The main conversation is running. Continue this child when it finishes.",
    nestedHint:
      "This child is managed by its parent. Inspect its history or send guidance while it runs.",
    placeholder: "Add guidance or the next task…",
    inbox: "Guidance inbox",
    pending: "Pending admission",
    consumed: "Admitted",
    provider: "Context",
    fork: "Forked history",
    spawn: "Fresh context",
    stopReason: "Stop reason",
    partial: "Partial result",
    coordination: "Coordination error",
    history: "Conversation",
    user: "Task input",
    assistant: "Agent response",
    tool: "Tool",
    request: "Arguments",
    noHistory: "No persisted conversation yet.",
    loadError: "Could not load sessions",
    retry: "Retry",
    conflict:
      "Session state changed. Refresh and try again. Your input is preserved.",
    notFound: "This child session is unavailable in this conversation.",
    noPlan: "No execution plan yet",
    noPlanHint: "Plans and tasks created in Pro / Ultra mode appear here.",
    reviewPlan: "This plan is awaiting review in the main conversation.",
    tasks: "Task progress",
    planHistory: "Plan history",
    stoppingMain:
      "Stopping the main conversation; waiting for execution to settle…",
    resize: "Resize execution panel",
    tokens: "tokens",
    statuses: {
      pending: "Queued",
      running: "Running",
      completed: "Completed",
      failed: "Failed",
      cancelled: "Cancelled",
      timed_out: "Timed out",
      interrupted: "Interrupted",
    },
  },
  // Locale meta
  locale: {
    localName: "English",
  },

  // Common
  common: {
    home: "Home",
    settings: "Settings",
    delete: "Delete",
    rename: "Rename",
    share: "Share",
    openInNewWindow: "Open in new window",
    close: "Close",
    more: "More",
    search: "Search",
    download: "Download",
    thinking: "Thinking",
    artifacts: "Artifacts",
    public: "Public",
    custom: "Custom",
    notAvailableInDemoMode: "Not available in demo mode",
    loading: "Loading...",
    version: "Version",
    lastUpdated: "Last updated",
    code: "Code",
    preview: "Preview",
    cancel: "Cancel",
    save: "Save",
    install: "Install",
    create: "Create",
  },

  // Welcome
  welcome: {
    greeting: "Hello, again!",
    description:
      "Search the web, analyze data, or create slides, images, videos, and web pages with Nous.",

    createYourOwnSkill: "Create Your Own Skill",
    createYourOwnSkillDescription:
      "Describe a task you want Nous to handle and turn your workflow into a reusable skill.",
  },

  // Clipboard
  clipboard: {
    copyToClipboard: "Copy to clipboard",
    copiedToClipboard: "Copied to clipboard",
    failedToCopyToClipboard: "Failed to copy to clipboard",
    linkCopied: "Link copied to clipboard",
  },

  // Input Box
  inputBox: {
    placeholder: "How can I assist you today?",
    createSkillPrompt:
      "We're going to build a new skill step by step with `skill-creator`. To start, what do you want this skill to do?",
    addAttachments: "Add attachments",
    mode: "Mode",
    flashMode: "Flash",
    flashModeDescription: "Fast and efficient, but may not be accurate",
    reasoningMode: "Reasoning",
    reasoningModeDescription:
      "Reasoning before action, balance between time and accuracy",
    proMode: "Pro",
    proModeDescription:
      "Reasoning, planning and executing, get more accurate results, may take more time",
    ultraMode: "Ultra",
    ultraModeDescription:
      "Pro mode with subagents to divide work; best for complex multi-step tasks",
    searchModels: "Search models...",
    modelLoading: "Loading models...",
    modelUnavailable: "Models unavailable",
    surpriseMe: "Surprise",
    surpriseMePrompt: "Surprise me",
    suggestions: [
      {
        suggestion: "Write",
        prompt: "Write a blog post about the latest trends on [topic]",
        icon: PenLineIcon,
      },
      {
        suggestion: "Research",
        prompt:
          "Conduct a deep dive research on [topic], and summarize the findings.",
        icon: MicroscopeIcon,
      },
      {
        suggestion: "Collect",
        prompt: "Collect data from [source] and create a report.",
        icon: ShapesIcon,
      },
      {
        suggestion: "Learn",
        prompt: "Learn about [topic] and create a tutorial.",
        icon: GraduationCapIcon,
      },
    ],
    suggestionsCreate: [
      {
        suggestion: "Webpage",
        prompt: "Create a webpage about [topic]",
        icon: CompassIcon,
      },
      {
        suggestion: "Image",
        prompt: "Create an image about [topic]",
        icon: ImageIcon,
      },
      {
        suggestion: "Video",
        prompt: "Create a video about [topic]",
        icon: VideoIcon,
      },
      {
        type: "separator",
      },
      {
        suggestion: "Skill",
        prompt:
          "We're going to build a new skill step by step with `skill-creator`. To start, what do you want this skill to do?",
        icon: SparklesIcon,
      },
    ],
  },

  // Sidebar
  sidebar: {
    newChat: "New task",
    chats: "Chats",
    recentChats: "Recent chats",
    demoChats: "Demo chats",
    agents: "Agents",
    creative: "Creative workspace",
    search: "Search",
    library: "Library",
    projects: "Projects",
    newProject: "New project",
    allTasks: "All tasks",
    logout: "Log out",
  },

  // Breadcrumb
  breadcrumb: {
    workspace: "Workspace",
    chats: "Chats",
  },

  // Workspace
  workspace: {
    officialWebsite: "Nous's official website",
    githubTooltip: "Nous on Github",
    settingsAndMore: "Settings and more",
    visitGithub: "Nous on GitHub",
    reportIssue: "Report a issue",
    contactUs: "Contact us",
    about: "About Nous",
  },

  // Conversation
  conversation: {
    noMessages: "No messages yet",
    startConversation: "Start a conversation to see messages here",
  },

  planReview: {
    title: "Review the complete plan",
    description: "Approve once to execute every todo in this plan.",
    onceHint:
      "This approval applies to the entire plan, not an individual todo.",
    feedbackPlaceholder: "Feedback for another planning pass (optional)",
    keepPlanning: "Keep planning",
    approveEntirePlan: "Approve entire plan and start",
    dismiss: "Dismiss plan review",
    submitError: "Could not submit plan review",
  },

  // Chats
  chats: {
    searchChats: "Search chats",
  },

  // Page titles (document title)
  pages: {
    appName: "Nous",
    chats: "Chats",
    newChat: "New chat",
    untitled: "Untitled",
  },

  // Tool calls
  toolCalls: {
    preparing: (name: string) => `Preparing ${name}`,
    notExecuted: (name: string) => `${name} was not executed`,
    argumentProgress: (length: number) =>
      `${length.toLocaleString()} argument characters received`,
    moreSteps: (count: number) => `${count} more step${count === 1 ? "" : "s"}`,
    lessSteps: "Less steps",
    executeCommand: "Execute command",
    presentFiles: "Present files",
    needYourHelp: "Need your help",
    useTool: (toolName: string) => `Use "${toolName}" tool`,
    searchFor: (query: string) => `Search for "${query}"`,
    searchForRelatedInfo: "Search for related information",
    searchForRelatedImages: "Search for related images",
    searchForRelatedImagesFor: (query: string) =>
      `Search for related images for "${query}"`,
    searchOnWebFor: (query: string) => `Search on the web for "${query}"`,
    viewWebPage: "View web page",
    listFolder: "List folder",
    readFile: "Read file",
    writeFile: "Write file",
    clickToViewContent: "Click to view file content",
    writeTodos: "Update to-do list",
    skillInstallTooltip: "Install skill and make it available to Nous",
  },

  // Subtasks
  inspector: {
    title: "Run events",
    run: "Run",
    eventType: "Event type",
    sequence: "Sequence",
    time: "Time",
    allTypes: "All types",
    empty: "No events",
    noRuns: "No runs yet",
    loadMore: "Load more",
    refresh: "Refresh",
    failed: "Could not load run events",
  },

  subtasks: {
    subtask: "Subtask",
    executing: (count: number) =>
      `Executing ${count === 1 ? "" : count + " "}subtask${count === 1 ? "" : "s in parallel"}`,
    in_progress: "Running subtask",
    completed: "Subtask completed",
    failed: "Subtask failed",
  },

  // Settings
  settings: {
    title: "Settings",
    description: "Adjust how Nous looks and behaves for you.",
    sections: {
      appearance: "Appearance",
      models: "Models",
      memory: "Memory",
      tools: "Tools",
      skills: "Skills",
      notification: "Notification",
      about: "About",
    },
    models: {
      title: "Model configuration",
      add: "Add model",
      edit: "Edit",
      name: "Model name",
      displayName: "Display name",
      provider: "API protocol",
      modelId: "Provider model ID",
      baseUrl: "API base URL",
      apiKey: "API key",
      keepApiKey: "Leave blank to keep the current key",
      setDefault: "Set as default model",
      defaultBadge: "Default",
      empty: "No models configured",
      duplicate: "A model with this name already exists.",
      missing: "This model was removed. Select another model.",
      required: "Enter a model name and provider model ID.",
      saved: "Model configuration saved",
      failed: "Could not save model configuration.",
    },
    memory: {
      title: "Memory",
      description:
        "Nous automatically learns from your conversations in the background. These memories help Nous understand you better and deliver a more personalized experience.",
      empty: "No memory data to display.",
      rawJson: "Raw JSON",
      markdown: {
        overview: "Overview",
        userContext: "User context",
        work: "Work",
        personal: "Personal",
        topOfMind: "Top of mind",
        historyBackground: "History",
        recentMonths: "Recent months",
        earlierContext: "Earlier context",
        longTermBackground: "Long-term background",
        updatedAt: "Updated at",
        facts: "Facts",
        empty: "(empty)",
        table: {
          category: "Category",
          confidence: "Confidence",
          confidenceLevel: {
            veryHigh: "Very high",
            high: "High",
            normal: "Normal",
            unknown: "Unknown",
          },
          content: "Content",
          source: "Source",
          createdAt: "CreatedAt",
          view: "View",
        },
      },
    },
    appearance: {
      themeTitle: "Theme",
      themeDescription:
        "Choose how the interface follows your device or stays fixed.",
      system: "System",
      light: "Light",
      dark: "Dark",
      systemDescription: "Match the operating system preference automatically.",
      lightDescription: "Bright palette with higher contrast for daytime.",
      darkDescription: "Dim palette that reduces glare for focus.",
      languageTitle: "Language",
      languageDescription: "Switch between languages.",
    },
    tools: {
      title: "Tools",
      description: "Manage the configuration and enabled status of MCP tools.",
    },
    skills: {
      title: "Agent Skills",
      description:
        "Manage the configuration and enabled status of the agent skills.",
      createSkill: "Create skill",
      emptyTitle: "No agent skill yet",
      emptyDescription:
        "Put your agent skill folders under the `/skills/custom` folder under the root folder of Nous.",
      emptyButton: "Create Your First Skill",
    },
    notification: {
      title: "Notification",
      description:
        "Nous only sends a completion notification when the window is not active. This is especially useful for long-running tasks so you can switch to other work and get notified when done.",
      requestPermission: "Request notification permission",
      deniedHint:
        "Notification permission was denied. You can enable it in your browser's site settings to receive completion alerts.",
      testButton: "Send test notification",
      testTitle: "Nous",
      testBody: "This is a test notification.",
      notSupported: "Your browser does not support notifications.",
      disableNotification: "Disable notification",
    },
    acknowledge: {
      emptyTitle: "Acknowledge",
      emptyDescription: "Related acknowledgements will be shown here.",
    },
    logout: "Log out",
  },

  // Auth
  auth: {
    signIn: "Sign in to your account",
    signUp: "Create a new account",
    email: "Email",
    emailPlaceholder: "you@example.com",
    password: "Password",
    passwordPlaceholder: "••••••••",
    displayName: "Display name (optional)",
    displayNamePlaceholder: "Your name",
    invalidEmail: "Please enter a valid email address",
    signingIn: "Signing in...",
    creatingAccount: "Creating account...",
    alreadyHaveAccount: "Already have an account? Sign in",
    noAccount: "Don't have an account? Sign up",
  },
};

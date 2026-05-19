/**
 * Drama Store - Zustand store for drama/short-drama state management
 */

import { create } from "zustand";
import type { DramaProject } from "./types";

interface DramaState {
  // Current project
  currentProject: DramaProject | null;
  currentProjectId: number | null;

  // Current script
  currentScriptId: number | null;

  // Sub navigation
  currentSubView: SubView;

  // Actions
  setCurrentProject: (project: DramaProject | null) => void;
  setCurrentProjectById: (projectId: number) => void;
  setCurrentScriptId: (scriptId: number | null) => void;
  setCurrentSubView: (view: SubView) => void;
  reset: () => void;
}

export type SubView = "overview" | "originalText" | "outline" | "script" | "assets";

const initialState = {
  currentProject: null,
  currentProjectId: null,
  currentScriptId: null,
  currentSubView: "overview" as SubView,
};

export const useDramaStore = create<DramaState>((set) => ({
  ...initialState,

  setCurrentProject: (project) =>
    set({
      currentProject: project,
      currentProjectId: project?.id ?? null,
    }),

  setCurrentProjectById: (projectId) =>
    set({
      currentProjectId: projectId,
    }),

  setCurrentScriptId: (scriptId) =>
    set({
      currentScriptId: scriptId,
    }),

  setCurrentSubView: (view) =>
    set({
      currentSubView: view,
    }),

  reset: () => set(initialState),
}));

// Selectors
export const selectCurrentProject = (state: DramaState) => state.currentProject;
export const selectCurrentProjectId = (state: DramaState) => state.currentProjectId;
export const selectCurrentScriptId = (state: DramaState) => state.currentScriptId;
export const selectCurrentSubView = (state: DramaState) => state.currentSubView;

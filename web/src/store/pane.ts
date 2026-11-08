import { create } from "zustand";
import type { ToolInvocation } from "@/store/chat";

/** 右 Pane store(P5-6):Spawn 详情/工具执行现场推入右栏并排阅读(§6.1 二排面) */
type PaneStore = {
  active: ToolInvocation | null;
  open: (t: ToolInvocation) => void;
  close: () => void;
};

export const usePane = create<PaneStore>((set) => ({
  active: null,
  open: (t) => set({ active: t }),
  close: () => set({ active: null }),
}));

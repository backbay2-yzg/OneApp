import { create } from "zustand";
import { Events } from "@wailsio/runtime";
import { AppService, SettingsService } from "../../bindings/oneapp/internal/service/index.js";
import { WindowService } from "../../bindings/oneapp/internal/app/index.js";
import type {
  AppOverview,
  Category,
  HotkeyStatus,
  IngestAnalysis,
  RunningAppState,
  Tag,
} from "../../bindings/oneapp/internal/domain/models.js";
import type { Settings } from "../../bindings/oneapp/internal/config/models.js";
import { applyTheme } from "./theme.js";

export interface ToastMessage {
  id: string;
  kind: "info" | "success" | "error";
  text: string;
}

interface AppStore {
  initialized: boolean;
  apps: AppOverview[];
  categories: Category[];
  tags: Tag[];
  settings: Settings | null;
  hotkeyStatus: HotkeyStatus;
  portableRoot: string;
  libraryRoot: string;
  selectedAppId: string | null;
  pendingIngests: IngestAnalysis[];
  commandModalOpen: boolean;
  settingsOpen: boolean;
  toasts: ToastMessage[];

  init: () => Promise<() => void>;
  refreshAll: () => Promise<void>;
  selectApp: (id: string | null) => void;
  setPendingIngests: (items: IngestAnalysis[]) => void;
  setCommandModalOpen: (open: boolean) => void;
  setSettingsOpen: (open: boolean) => void;
  saveSettings: (next: Settings) => Promise<void>;
  pushToast: (kind: ToastMessage["kind"], text: string) => void;
  dismissToast: (id: string) => void;
}

function mergeRunningIntoApps(apps: AppOverview[], runningList: RunningAppState[]): AppOverview[] {
  const map = new Map<string, RunningAppState>();
  for (const r of runningList) {
    map.set(r.appId, r);
  }
  return apps.map((a) => {
    const st = map.get(a.id);
    const pids = st?.pids ?? [];
    return {
      ...a,
      isRunning: pids.length > 0,
      runningPids: pids,
      isBuilding: Boolean(st?.isBuilding),
      buildMessage: st?.buildMessage ?? "",
    };
  });
}

function deriveTagsFromApps(apps: AppOverview[]): Tag[] {
  const seen = new Set<string>();
  const out: Tag[] = [];
  for (const a of apps) {
    for (const t of a.tags ?? []) {
      const k = t.toLowerCase();
      if (!seen.has(k)) {
        seen.add(k);
        out.push({ id: t, name: t });
      }
    }
  }
  out.sort((a, b) => a.name.localeCompare(b.name));
  return out;
}

export const useAppStore = create<AppStore>((set, get) => ({
  initialized: false,
  apps: [],
  categories: [],
  tags: [],
  settings: null,
  hotkeyStatus: { accelerator: "Alt+Space", registered: false, error: "" },
  portableRoot: "",
  libraryRoot: "",
  selectedAppId: null,
  pendingIngests: [],
  commandModalOpen: false,
  settingsOpen: false,
  toasts: [],

  init: async () => {
    const [boot, cfg] = await Promise.all([
      AppService.GetBootstrapData(),
      SettingsService.GetSettings(),
    ]);
    applyTheme(cfg.theme);

    const apps = mergeRunningIntoApps(boot.apps ?? [], boot.running ?? []);
    set((state) => ({
      initialized: true,
      apps,
      categories: boot.categories ?? [],
      tags: boot.tags ?? deriveTagsFromApps(apps),
      settings: cfg,
      hotkeyStatus: boot.hotkeyStatus,
      portableRoot: boot.portableRoot,
      libraryRoot: boot.libraryRoot,
      selectedAppId:
        state.selectedAppId && apps.some((a) => a.id === state.selectedAppId)
          ? state.selectedAppId
          : apps[0]?.id ?? null,
    }));

    const unsubs = [
      Events.On("apps:changed", async (ev) => {
        const nextApps = (ev.data as AppOverview[] | null) ?? [];
        const cats = await AppService.ListCategories().catch(() => get().categories);
        set((state) => {
          const curSel = state.selectedAppId;
          const stillExists = curSel && nextApps.some((a) => a.id === curSel);
          return {
            apps: nextApps,
            categories: cats ?? state.categories,
            tags: deriveTagsFromApps(nextApps),
            selectedAppId: stillExists ? curSel : nextApps[0]?.id ?? null,
          };
        });
      }),
      Events.On("process:status", (ev) => {
        const states = (ev.data as RunningAppState[] | null) ?? [];
        set((state) => ({
          apps: mergeRunningIntoApps(state.apps, states),
        }));
      }),
      Events.On("ingest:dropped", (ev) => {
        const analyses = (ev.data as IngestAnalysis[] | null) ?? [];
        if (analyses.length > 0) {
          set({ pendingIngests: analyses });
        }
      }),
      Events.On("ingest:silent", (ev) => {
        const res = ev.data as { apps?: AppOverview[]; errors?: string[] } | null;
        const okCount = res?.apps?.length ?? 0;
        const errCount = res?.errors?.length ?? 0;
        if (okCount > 0) {
          get().pushToast("success", `已静默入库 ${okCount} 个软件`);
        }
        if (errCount > 0) {
          get().pushToast("error", res?.errors?.[0] ?? "部分项目入库失败");
        }
      }),
      Events.On("hotkey:status", (ev) => {
        if (ev.data) {
          set({ hotkeyStatus: ev.data as HotkeyStatus });
        }
      }),
      Events.On("settings:changed", (ev) => {
        const next = ev.data as Settings | null;
        if (next) {
          applyTheme(next.theme);
          set({ settings: next });
        }
      }),
      Events.On("open:app", (ev) => {
        const id = ev.data as string;
        if (id) {
          set({ selectedAppId: id });
        }
      }),
      Events.On("open:settings", () => {
        set({ settingsOpen: true });
      }),
      Events.On("app:error", (ev) => {
        if (ev.data) {
          get().pushToast("error", String(ev.data));
        }
      }),
    ];

    return () => {
      for (const unsub of unsubs) {
        unsub();
      }
    };
  },

  refreshAll: async () => {
    const boot = await AppService.GetBootstrapData();
    const apps = mergeRunningIntoApps(boot.apps ?? [], boot.running ?? []);
    set((state) => ({
      apps,
      categories: boot.categories ?? [],
      tags: boot.tags ?? deriveTagsFromApps(apps),
      hotkeyStatus: boot.hotkeyStatus,
      portableRoot: boot.portableRoot,
      libraryRoot: boot.libraryRoot,
      selectedAppId:
        state.selectedAppId && apps.some((a) => a.id === state.selectedAppId)
          ? state.selectedAppId
          : apps[0]?.id ?? null,
    }));
  },

  selectApp: (id) => set({ selectedAppId: id }),

  setPendingIngests: (items) => set({ pendingIngests: items }),

  setCommandModalOpen: (open) => set({ commandModalOpen: open }),

  setSettingsOpen: (open) => set({ settingsOpen: open }),

  saveSettings: async (next) => {
    const res = await SettingsService.SaveSettings(next);
    applyTheme(res.settings.theme);
    set({ settings: res.settings, hotkeyStatus: res.hotkeyStatus });
    if (!res.hotkeyStatus.registered && res.hotkeyStatus.error) {
      get().pushToast("error", res.hotkeyStatus.error);
    } else {
      get().pushToast("success", "设置已保存");
    }
  },

  pushToast: (kind, text) => {
    const id = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    set((state) => ({
      toasts: [...state.toasts.slice(-3), { id, kind, text }],
    }));
    setTimeout(() => {
      get().dismissToast(id);
    }, kind === "error" ? 6000 : 3600);
  },

  dismissToast: (id) => {
    set((state) => ({
      toasts: state.toasts.filter((t) => t.id !== id),
    }));
  },
}));

export { AppService, SettingsService, WindowService };

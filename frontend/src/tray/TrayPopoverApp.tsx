import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import {
  AppWindow,
  Edit3,
  FolderOpen,
  MoreHorizontal,
  Pin,
  Play,
  Power,
  RotateCw,
  Search,
  Settings,
  Shield,
  Square,
  Terminal,
  Upload,
  X,
  Loader2,
} from "lucide-react";
import { AppService, SettingsService } from "../../bindings/oneapp/internal/service/index.js";
import { WindowService } from "../../bindings/oneapp/internal/app/index.js";
import type {
  AppOverview,
  Category,
  RunningAppState,
  SearchHit,
} from "../../bindings/oneapp/internal/domain/models.js";
import type { Settings as AppSettings } from "../../bindings/oneapp/internal/config/models.js";
import { AppIcon } from "../shared/AppIcon.js";
import { applyTheme } from "../shared/theme.js";

type TrayTab = "quick" | "running" | "all";

interface PopoverSection {
  key: string;
  title: string;
  apps: AppOverview[];
}

interface PopoverAction {
  id: string;
  label: string;
  hint?: string;
  danger?: boolean;
  icon: typeof Play;
  run: () => Promise<void>;
}

interface PopoverToast {
  kind: "info" | "success" | "error";
  text: string;
}

function mergeRunningStates(apps: AppOverview[], states: RunningAppState[]): AppOverview[] {
  const map = new Map<string, RunningAppState>();
  for (const s of states) {
    map.set(s.appId, s);
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

function sortByRunningAndRecent(a: AppOverview, b: AppOverview): number {
  if (a.isPinned !== b.isPinned) return a.isPinned ? -1 : 1;
  if (a.lastLaunchedAt !== b.lastLaunchedAt) return b.lastLaunchedAt - a.lastLaunchedAt;
  if (a.launchCount !== b.launchCount) return b.launchCount - a.launchCount;
  return a.name.localeCompare(b.name, "zh-CN");
}

function sortByAllApps(a: AppOverview, b: AppOverview): number {
  if (a.isPinned !== b.isPinned) return a.isPinned ? -1 : 1;
  if (a.isRunning !== b.isRunning) return a.isRunning ? -1 : 1;
  if (a.lastLaunchedAt !== b.lastLaunchedAt) return b.lastLaunchedAt - a.lastLaunchedAt;
  if (a.launchCount !== b.launchCount) return b.launchCount - a.launchCount;
  return a.name.localeCompare(b.name, "zh-CN");
}

export function TrayPopoverApp() {
  const [apps, setApps] = useState<AppOverview[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [query, setQuery] = useState("");
  const [searchHits, setSearchHits] = useState<SearchHit[]>([]);
  const [tab, setTab] = useState<TrayTab>("quick");
  const [selectedCategoryId, setSelectedCategoryId] = useState("");
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [pinned, setPinned] = useState(false);
  const [menuAppId, setMenuAppId] = useState<string | null>(null);
  const [toast, setToast] = useState<PopoverToast | null>(null);

  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const rowRefs = useRef<(HTMLDivElement | null)[]>([]);
  const queryRef = useRef("");
  const pinnedRef = useRef(false);
  const searchSeqRef = useRef(0);
  const toastTimerRef = useRef<number | null>(null);

  queryRef.current = query;
  pinnedRef.current = pinned;

  const showToast = useCallback((kind: PopoverToast["kind"], text: string) => {
    if (toastTimerRef.current !== null) {
      window.clearTimeout(toastTimerRef.current);
    }
    setToast({ kind, text });
    toastTimerRef.current = window.setTimeout(() => {
      setToast(null);
      toastTimerRef.current = null;
    }, 3500);
  }, []);

  const loadBootstrap = useCallback(async () => {
    try {
      const [boot, isPinned] = await Promise.all([
        AppService.GetBootstrapData(),
        WindowService.GetTrayPopoverPinned(),
      ]);
      setApps(mergeRunningStates(boot.apps ?? [], boot.running ?? []));
      setCategories(boot.categories ?? []);
      setPinned(Boolean(isPinned));
    } catch (err) {
      showToast("error", String(err));
    }
  }, [showToast]);

  const runSearch = useCallback(
    async (q: string) => {
      const trimmed = q.trim();
      if (!trimmed) {
        setSearchHits([]);
        return;
      }
      const seq = ++searchSeqRef.current;
      try {
        const res = (await AppService.Search(trimmed, 40)) ?? [];
        if (seq === searchSeqRef.current) {
          setSearchHits(res);
        }
      } catch (err) {
        if (seq === searchSeqRef.current) {
          showToast("error", String(err));
        }
      }
    },
    [showToast]
  );

  useEffect(() => {
    SettingsService.GetSettings()
      .then((cfg: AppSettings) => applyTheme(cfg.theme))
      .catch(() => undefined);

    loadBootstrap();

    const unsubs = [
      Events.On("tray:opened", () => {
        setQuery("");
        setSearchHits([]);
        setMenuAppId(null);
        setSelectedIdx(0);
        loadBootstrap();
        setTimeout(() => {
          searchInputRef.current?.focus();
        }, 20);
      }),
      Events.On("apps:changed", async (ev) => {
        const nextApps = (ev.data as AppOverview[] | null) ?? [];
        setApps(nextApps);
        const cats = await AppService.ListCategories().catch(() => null);
        if (cats) {
          setCategories(cats);
        }
        if (queryRef.current.trim() !== "") {
          runSearch(queryRef.current);
        }
      }),
      Events.On("process:status", (ev) => {
        const states = (ev.data as RunningAppState[] | null) ?? [];
        setApps((prev) => mergeRunningStates(prev, states));
        if (queryRef.current.trim() !== "") {
          runSearch(queryRef.current);
        }
      }),
      Events.On("settings:changed", (ev) => {
        const cfg = ev.data as AppSettings | null;
        if (cfg) {
          applyTheme(cfg.theme);
        }
      }),
      Events.On("ingest:silent", (ev) => {
        const res = ev.data as { apps?: AppOverview[]; errors?: string[] } | null;
        const okCount = res?.apps?.length ?? 0;
        const errCount = res?.errors?.length ?? 0;
        if (okCount > 0) {
          showToast("success", `已静默入库 ${okCount} 个软件`);
        }
        if (errCount > 0) {
          showToast("error", res?.errors?.[0] ?? "部分项目入库失败");
        }
      }),
      Events.On("app:error", (ev) => {
        if (ev.data) {
          showToast("error", String(ev.data));
        }
      }),
    ];

    return () => {
      if (toastTimerRef.current !== null) {
        window.clearTimeout(toastTimerRef.current);
      }
      for (const u of unsubs) u();
    };
  }, [loadBootstrap, runSearch, showToast]);

  useEffect(() => {
    setSelectedIdx(0);
    runSearch(query);
  }, [query, runSearch]);

  useEffect(() => {
    if (selectedCategoryId && !categories.some((c) => c.id === selectedCategoryId)) {
      setSelectedCategoryId("");
    }
  }, [categories, selectedCategoryId]);

  const isSearching = query.trim() !== "";
  const runningCount = useMemo(() => apps.filter((a) => a.isRunning).length, [apps]);

  const sections = useMemo<PopoverSection[]>(() => {
    if (isSearching) {
      return [
        {
          key: "search",
          title: `搜索结果 (${searchHits.length})`,
          apps: searchHits.map((h) => h.app),
        },
      ];
    }

    if (tab === "running") {
      const list = apps.filter((a) => a.isRunning).sort(sortByRunningAndRecent);
      return list.length > 0 ? [{ key: "running", title: `正在运行 (${list.length})`, apps: list }] : [];
    }

    if (tab === "all") {
      const list = apps
        .filter((a) => !selectedCategoryId || a.categoryId === selectedCategoryId)
        .sort(sortByAllApps);
      return list.length > 0 ? [{ key: "all", title: "", apps: list }] : [];
    }

    // "quick" tab: 正在运行 -> 收藏置顶 -> 最近使用 (fallback to recently added)
    const running = apps.filter((a) => a.isRunning).sort(sortByRunningAndRecent);
    const pinnedOnly = apps
      .filter((a) => a.isPinned && !a.isRunning)
      .sort((a, b) => {
        if (a.lastLaunchedAt !== b.lastLaunchedAt) return b.lastLaunchedAt - a.lastLaunchedAt;
        if (a.launchCount !== b.launchCount) return b.launchCount - a.launchCount;
        return a.name.localeCompare(b.name, "zh-CN");
      });
    const recent = apps
      .filter((a) => !a.isRunning && !a.isPinned && a.lastLaunchedAt > 0)
      .sort((a, b) => b.lastLaunchedAt - a.lastLaunchedAt)
      .slice(0, 10);

    const out: PopoverSection[] = [];
    if (running.length > 0) {
      out.push({ key: "quick-running", title: "正在运行", apps: running });
    }
    if (pinnedOnly.length > 0) {
      out.push({ key: "quick-pinned", title: "收藏置顶", apps: pinnedOnly });
    }
    if (recent.length > 0) {
      out.push({ key: "quick-recent", title: "最近使用", apps: recent });
    }
    if (out.length === 0 && apps.length > 0) {
      const libraryFallback = [...apps]
        .sort((a, b) => b.createdAt - a.createdAt || a.name.localeCompare(b.name, "zh-CN"))
        .slice(0, 12);
      out.push({ key: "quick-library", title: "软件库", apps: libraryFallback });
    }
    return out;
  }, [apps, isSearching, searchHits, selectedCategoryId, tab]);

  const flatRows = useMemo(() => {
    const out: AppOverview[] = [];
    for (const sec of sections) {
      for (const a of sec.apps) {
        out.push(a);
      }
    }
    return out;
  }, [sections]);

  useEffect(() => {
    setSelectedIdx((idx) => (flatRows.length === 0 ? 0 : Math.min(idx, flatRows.length - 1)));
  }, [flatRows.length]);

  useEffect(() => {
    if (!menuAppId) {
      rowRefs.current[selectedIdx]?.scrollIntoView({ block: "nearest" });
    }
  }, [selectedIdx, menuAppId]);

  const menuApp = useMemo(() => {
    if (!menuAppId) return null;
    return (
      apps.find((a) => a.id === menuAppId) ??
      searchHits.find((h) => h.app.id === menuAppId)?.app ??
      null
    );
  }, [apps, menuAppId, searchHits]);

  const hideIfUnpinned = useCallback(async () => {
    if (!pinnedRef.current) {
      await WindowService.HideTrayPopover();
    }
  }, []);

  const launchApp = useCallback(
    async (
      app: AppOverview,
      opts?: { entryId?: string; forceAdmin?: boolean }
    ) => {
      try {
        await AppService.LaunchApp({
          appId: app.id,
          entryId: opts?.entryId ?? "",
          extraArgs: "",
          forceAdmin: opts?.forceAdmin ?? false,
        });
        setMenuAppId(null);
        await hideIfUnpinned();
      } catch (err) {
        showToast("error", `启动失败: ${String(err)}`);
      }
    },
    [hideIfUnpinned, showToast]
  );

  const stopApp = useCallback(
    async (app: AppOverview) => {
      try {
        await AppService.TerminateApp(app.id);
      } catch (err) {
        showToast("error", `结束进程失败: ${String(err)}`);
      }
    },
    [showToast]
  );

  const togglePinnedPopover = async () => {
    const next = !pinned;
    setPinned(next);
    try {
      await WindowService.SetTrayPopoverPinned(next);
    } catch (err) {
      setPinned(!next);
      showToast("error", String(err));
    }
  };

  const menuActions = useMemo<PopoverAction[]>(() => {
    if (!menuApp) return [];
    const entries = menuApp.entries ?? [];
    const primary = entries.find((e) => e.isPrimary) ?? entries[0];
    const subEntries = entries.filter((e) => !primary || e.id !== primary.id);

    const actions: PopoverAction[] = [
      {
        id: "launch-primary",
        label: primary ? `启动 ${primary.name}` : "启动默认入口",
        hint: "Enter",
        icon: Play,
        run: () => launchApp(menuApp),
      },
    ];

    for (const sub of subEntries) {
      actions.push({
        id: `entry-${sub.id}`,
        label: `启动子入口：${sub.name}`,
        hint: sub.relPath,
        icon: Play,
        run: () => launchApp(menuApp, { entryId: sub.id }),
      });
    }

    actions.push(
      {
        id: "launch-admin",
        label: "以管理员身份运行",
        hint: "Ctrl+Click",
        icon: Shield,
        run: () => launchApp(menuApp, { forceAdmin: true }),
      },
      {
        id: "reveal-dir",
        label: "打开所在目录",
        icon: FolderOpen,
        run: async () => {
          try {
            await AppService.RevealAppDir(menuApp.id);
            setMenuAppId(null);
            await hideIfUnpinned();
          } catch (err) {
            showToast("error", String(err));
          }
        },
      },
      {
        id: "open-terminal",
        label: "在终端中打开目录",
        icon: Terminal,
        run: async () => {
          try {
            await AppService.OpenAppTerminal(menuApp.id);
            setMenuAppId(null);
            await hideIfUnpinned();
          } catch (err) {
            showToast("error", String(err));
          }
        },
      },
      {
        id: "toggle-pin",
        label: menuApp.isPinned ? "取消收藏置顶" : "收藏置顶",
        icon: Pin,
        run: async () => {
          try {
            await AppService.TogglePin(menuApp.id, !menuApp.isPinned);
            setMenuAppId(null);
          } catch (err) {
            showToast("error", String(err));
          }
        },
      }
    );

    if (menuApp.isBuilding) {
      actions.push({
        id: "cancel-build",
        label: "取消当前构建",
        danger: true,
        icon: Square,
        run: async () => {
          await stopApp(menuApp);
          setMenuAppId(null);
        },
      });
    }

    if (menuApp.isRunning) {
      actions.push(
        {
          id: "restart-proc",
          label: "重启进程",
          icon: RotateCw,
          run: async () => {
            try {
              await AppService.RestartApp(menuApp.id);
              setMenuAppId(null);
            } catch (err) {
              showToast("error", String(err));
            }
          },
        },
        {
          id: "terminate-proc",
          label: `结束进程 (${(menuApp.runningPids ?? []).length})`,
          danger: true,
          icon: Square,
          run: async () => {
            await stopApp(menuApp);
            setMenuAppId(null);
          },
        }
      );
    }

    actions.push({
      id: "open-main",
      label: "在主窗口中编辑",
      icon: Edit3,
      run: async () => {
        setMenuAppId(null);
        await WindowService.OpenAppInMain(menuApp.id);
      },
    });

    return actions;
  }, [hideIfUnpinned, launchApp, menuApp, showToast, stopApp]);

  const handleKeyDown = async (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      if (menuAppId) {
        setMenuAppId(null);
        setTimeout(() => searchInputRef.current?.focus(), 10);
        return;
      }
      if (query.trim() !== "") {
        setQuery("");
        return;
      }
      await WindowService.HideTrayPopover();
      return;
    }

    if (menuAppId) {
      return;
    }

    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIdx((i) => (flatRows.length ? Math.min(i + 1, flatRows.length - 1) : 0));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIdx((i) => (flatRows.length ? Math.max(i - 1, 0) : 0));
      return;
    }
    if (e.key === "Enter" && flatRows[selectedIdx]) {
      e.preventDefault();
      await launchApp(flatRows[selectedIdx], { forceAdmin: e.ctrlKey || e.metaKey });
    }
  };

  let flatCursor = 0;

  return (
    <div
      id="tray-drop-zone"
      data-file-drop-target
      onKeyDown={handleKeyDown}
      className="drop-zone-surface relative h-screen w-screen flex flex-col bg-[var(--bg-surface)] text-[var(--text-primary)] border border-[var(--border-strong)] rounded-xl overflow-hidden shadow-2xl"
    >
      {/* Drag-over overlay for quick file/archive ingest */}
      <div className="drop-zone-overlay pointer-events-none absolute inset-2 z-40 rounded-lg border-2 border-dashed border-[var(--accent)] bg-[var(--bg-app)]/90 backdrop-blur-sm flex-col items-center justify-center gap-2 px-4 text-center">
        <div className="p-2.5 rounded-full bg-[var(--accent-soft)] text-[var(--accent)]">
          <Upload className="w-5 h-5" />
        </div>
        <div className="text-sm font-semibold text-[var(--text-primary)]">松开立即导入到 OneApp</div>
        <div className="text-xs text-[var(--text-muted)]">支持 .exe、目录、.zip / .7z、脚本</div>
      </div>

      {/* Top Search & Pin Bar */}
      <header className="px-3 pt-3 pb-2 border-b border-[var(--border)] bg-[var(--bg-surface)] space-y-2 shrink-0">
        <div className="flex items-center gap-2">
          <div className="flex-1 flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-[var(--bg-elevated)] border border-[var(--border)] focus-within:border-[var(--accent)]">
            <Search className="w-3.5 h-3.5 text-[var(--accent)] shrink-0" />
            <input
              ref={searchInputRef}
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索软件、拼音、别名、#标签..."
              className="flex-1 min-w-0 bg-transparent text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none"
            />
            {query && (
              <button
                type="button"
                title="清空搜索"
                onClick={() => {
                  setQuery("");
                  searchInputRef.current?.focus();
                }}
                className="p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={togglePinnedPopover}
            title={pinned ? "已固定浮窗（点击取消自动驻留）" : "固定浮窗（连续启动多个软件）"}
            className={`p-2 rounded-lg border transition-colors shrink-0 ${
              pinned
                ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]"
                : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
            }`}
          >
            <Pin className="w-3.5 h-3.5" />
          </button>
        </div>

        {/* Lightweight Tabs */}
        {!isSearching && (
          <div className="space-y-1.5">
            <div className="grid grid-cols-3 gap-1 p-0.5 rounded-lg bg-[var(--bg-elevated)] border border-[var(--border)]">
              <button
                type="button"
                onClick={() => {
                  setTab("quick");
                  setSelectedIdx(0);
                }}
                className={`py-1 rounded-md text-xs font-medium transition-colors ${
                  tab === "quick"
                    ? "bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                }`}
              >
                快捷
              </button>
              <button
                type="button"
                onClick={() => {
                  setTab("running");
                  setSelectedIdx(0);
                }}
                className={`py-1 rounded-md text-xs font-medium transition-colors flex items-center justify-center gap-1.5 ${
                  tab === "running"
                    ? "bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                }`}
              >
                <span>运行中</span>
                {runningCount > 0 && (
                  <span className="px-1.5 rounded-full text-[11px] font-mono bg-emerald-500/15 text-emerald-400">
                    {runningCount}
                  </span>
                )}
              </button>
              <button
                type="button"
                onClick={() => {
                  setTab("all");
                  setSelectedIdx(0);
                }}
                className={`py-1 rounded-md text-xs font-medium transition-colors ${
                  tab === "all"
                    ? "bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                }`}
              >
                全部 ({apps.length})
              </button>
            </div>

            {tab === "all" && categories.length > 0 && (
              <div className="flex items-center gap-1 overflow-x-auto pb-0.5">
                <button
                  type="button"
                  onClick={() => setSelectedCategoryId("")}
                  className={`px-2 py-0.5 rounded-full text-[11px] whitespace-nowrap border transition-colors ${
                    selectedCategoryId === ""
                      ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)] font-medium"
                      : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  全部分类
                </button>
                {categories.map((cat) => (
                  <button
                    key={cat.id}
                    type="button"
                    onClick={() => setSelectedCategoryId(cat.id)}
                    className={`px-2 py-0.5 rounded-full text-[11px] whitespace-nowrap border transition-colors ${
                      selectedCategoryId === cat.id
                        ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)] font-medium"
                        : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {cat.name}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </header>

      {/* Main List Body */}
      <div className="flex-1 min-h-0 overflow-y-auto px-2 py-2 space-y-2.5">
        {flatRows.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center text-center px-5 py-8">
            <div className="text-sm font-medium text-[var(--text-secondary)]">
              {isSearching
                ? "没有找到匹配的软件"
                : tab === "running"
                ? "暂无运行中的软件"
                : apps.length === 0
                ? "软件库暂为空"
                : "当前分类下暂无软件"}
            </div>
            <div className="text-xs text-[var(--text-muted)] mt-1">
              {isSearching
                ? "尝试输入软件名、拼音首字母或 #标签"
                : apps.length === 0
                ? "可直接将 .exe / 压缩包拖入此浮窗，或打开主窗口导入"
                : "切换到其他标签或清空筛选查看全部软件"}
            </div>
          </div>
        ) : (
          sections.map((sec) => (
            <div key={sec.key} className="space-y-0.5">
              {sec.title && (
                <div className="px-2 py-0.5 text-[11px] font-medium text-[var(--text-muted)] flex items-center justify-between">
                  <span>{sec.title}</span>
                  <span>{sec.apps.length}</span>
                </div>
              )}
              {sec.apps.map((app) => {
                const rowIdx = flatCursor++;
                const active = rowIdx === selectedIdx;
                const entryCount = (app.entries ?? []).length;

                return (
                  <div
                    key={`${sec.key}-${app.id}`}
                    ref={(el) => {
                      rowRefs.current[rowIdx] = el;
                    }}
                    onMouseEnter={() => {
                      if (!menuAppId) setSelectedIdx(rowIdx);
                    }}
                    onClick={(e) => launchApp(app, { forceAdmin: e.ctrlKey || e.metaKey })}
                    onContextMenu={(e) => {
                      e.preventDefault();
                      setSelectedIdx(rowIdx);
                      setMenuAppId(app.id);
                    }}
                    className={`group flex items-center justify-between gap-2 px-2 py-1.5 rounded-lg cursor-pointer transition-colors ${
                      active
                        ? "bg-[var(--bg-selected)] text-[var(--text-primary)]"
                        : "hover:bg-[var(--bg-hover)]"
                    }`}
                  >
                    <div className="flex items-center gap-2.5 min-w-0">
                      <AppIcon
                        name={app.name}
                        iconUrl={app.iconUrl}
                        size="sm"
                        isRunning={app.isRunning}
                      />
                      <div className="min-w-0">
                        <div className="flex items-center gap-1.5">
                          <span className="text-[13px] font-medium truncate">{app.name}</span>
                          {app.isPinned && (
                            <Pin className="w-3 h-3 text-amber-400 shrink-0" />
                          )}
                          {app.currentVersion && (
                            <span className="text-[11px] font-mono text-[var(--text-muted)] truncate">
                              {app.currentVersion}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-1.5 text-[11px] text-[var(--text-muted)] truncate">
                          {app.categoryName && <span>{app.categoryName}</span>}
                          {(app.tags ?? []).slice(0, 2).map((t) => (
                            <span key={t}>#{t}</span>
                          ))}
                          {entryCount > 1 && (
                            <span className="font-mono">{entryCount}入口</span>
                          )}
                        </div>
                      </div>
                    </div>

                    <div className="flex items-center gap-1 shrink-0">
                      {app.isBuilding && (
                        <button
                          type="button"
                          title="构建中（点击取消）"
                          onClick={(e) => {
                            e.stopPropagation();
                            stopApp(app);
                          }}
                          className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-md text-[11px] text-[var(--accent)] bg-[var(--accent-soft)] hover:text-[var(--danger)] hover:bg-red-500/15 transition-colors"
                        >
                          <Loader2 className="w-3 h-3 animate-spin" />
                          构建中
                        </button>
                      )}
                      {app.isRunning && (
                        <button
                          type="button"
                          title="结束进程"
                          onClick={(e) => {
                            e.stopPropagation();
                            stopApp(app);
                          }}
                          className="p-1.5 rounded-md text-emerald-400 hover:text-[var(--danger)] hover:bg-red-500/15 transition-colors"
                        >
                          <Square className="w-3.5 h-3.5 fill-current" />
                        </button>
                      )}
                      <button
                        type="button"
                        title="更多操作"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSelectedIdx(rowIdx);
                          setMenuAppId((prev) => (prev === app.id ? null : app.id));
                        }}
                        className={`p-1.5 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] transition-opacity ${
                          active ? "opacity-100" : "opacity-0 group-hover:opacity-100"
                        }`}
                      >
                        <MoreHorizontal className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          ))
        )}
      </div>

      {/* Inline Toast Feedback */}
      {toast && (
        <div
          className={`px-3 py-1.5 border-t text-xs flex items-center justify-between gap-2 shrink-0 ${
            toast.kind === "error"
              ? "bg-red-500/10 border-red-500/30 text-[var(--danger)]"
              : toast.kind === "success"
              ? "bg-emerald-500/10 border-emerald-500/30 text-[var(--success)]"
              : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)]"
          }`}
        >
          <span className="truncate">{toast.text}</span>
          <button
            type="button"
            onClick={() => setToast(null)}
            className="shrink-0 opacity-75 hover:opacity-100"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      )}

      {/* Footer Global Controls */}
      <footer className="px-3 py-2 border-t border-[var(--border)] bg-[var(--bg-elevated)] flex items-center justify-between gap-2 shrink-0">
        <button
          type="button"
          onClick={() => WindowService.ShowMain()}
          className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] hover:border-[var(--accent)] hover:text-[var(--accent)] transition-colors"
        >
          <AppWindow className="w-3.5 h-3.5" />
          <span>主窗口</span>
        </button>

        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => WindowService.OpenSettingsInMain()}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
          >
            <Settings className="w-3.5 h-3.5" />
            <span>设置</span>
          </button>
          <button
            type="button"
            onClick={() => WindowService.QuitApp()}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs text-[var(--text-secondary)] hover:text-[var(--danger)] hover:bg-red-500/10 transition-colors"
          >
            <Power className="w-3.5 h-3.5" />
            <span>退出</span>
          </button>
        </div>
      </footer>

      {/* Contextual Action Sheet Overlay */}
      {menuApp && (
        <div
          onClick={() => setMenuAppId(null)}
          className="absolute inset-0 z-30 bg-black/45 backdrop-blur-[1px] flex items-end"
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="w-full max-h-[82%] flex flex-col bg-[var(--bg-surface)] border-t border-[var(--border-strong)] rounded-t-xl shadow-2xl overflow-hidden"
          >
            <div className="px-3 py-2.5 border-b border-[var(--border)] bg-[var(--bg-elevated)] flex items-center justify-between gap-2 shrink-0">
              <div className="flex items-center gap-2 min-w-0">
                <AppIcon
                  name={menuApp.name}
                  iconUrl={menuApp.iconUrl}
                  size="sm"
                  isRunning={menuApp.isRunning}
                />
                <div className="min-w-0">
                  <div className="text-[13px] font-semibold truncate">{menuApp.name}</div>
                  {menuApp.currentVersion && (
                    <div className="text-[11px] font-mono text-[var(--text-muted)]">
                      {menuApp.currentVersion}
                    </div>
                  )}
                </div>
              </div>
              <button
                type="button"
                onClick={() => setMenuAppId(null)}
                className="p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto p-1.5 space-y-0.5">
              {menuActions.map((act) => {
                const Icon = act.icon;
                return (
                  <button
                    key={act.id}
                    type="button"
                    onClick={() => act.run()}
                    className={`w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-md text-[13px] text-left transition-colors hover:bg-[var(--bg-hover)] ${
                      act.danger
                        ? "text-[var(--danger)] hover:bg-red-500/10"
                        : "text-[var(--text-primary)]"
                    }`}
                  >
                    <span className="flex items-center gap-2 min-w-0">
                      <Icon className="w-3.5 h-3.5 shrink-0 opacity-85" />
                      <span className="truncate">{act.label}</span>
                    </span>
                    {act.hint && (
                      <span className="text-[11px] font-mono text-[var(--text-muted)] truncate max-w-[110px]">
                        {act.hint}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

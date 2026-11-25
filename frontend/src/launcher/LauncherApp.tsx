import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Clipboard, Events } from "@wailsio/runtime";
import {
  CornerDownLeft,
  Edit3,
  FolderOpen,
  Play,
  RotateCw,
  Search,
  Shield,
  Square,
  Terminal,
  Copy,
  SlidersHorizontal,
  ArrowLeft,
  Loader2,
} from "lucide-react";
import { AppService, SettingsService } from "../../bindings/oneapp/internal/service/index.js";
import { WindowService } from "../../bindings/oneapp/internal/app/index.js";
import {
  EntryType,
  StorageMode,
  type AppOverview,
  type SearchHit,
} from "../../bindings/oneapp/internal/domain/models.js";
import type { Settings } from "../../bindings/oneapp/internal/config/models.js";
import { AppIcon } from "../shared/AppIcon.js";
import { applyTheme } from "../shared/theme.js";

interface LauncherAction {
  id: string;
  label: string;
  hint?: string;
  danger?: boolean;
  run: () => Promise<void>;
}

function joinWinPath(root: string, rel: string): string {
  if (!root) return rel;
  if (!rel) return root;
  if (/^[a-zA-Z]:/.test(rel) || rel.startsWith("\\\\")) return rel;
  const cleanRoot = root.replace(/[\\/]+$/, "");
  const cleanRel = rel.replace(/^[\\/]+/, "").replace(/\//g, "\\");
  return `${cleanRoot}\\${cleanRel}`;
}

export function LauncherApp() {
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [actionMode, setActionMode] = useState(false);
  const [actionIdx, setActionIdx] = useState(0);
  const [tempArgsMode, setTempArgsMode] = useState(false);
  const [tempArgs, setTempArgs] = useState("");
  const [showPreview, setShowPreview] = useState(true);
  const [errorMsg, setErrorMsg] = useState("");

  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const tempArgsInputRef = useRef<HTMLInputElement | null>(null);
  const itemRefs = useRef<(HTMLDivElement | null)[]>([]);
  const actionRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const runSearch = useCallback(async (q: string) => {
    try {
      const res = (await AppService.Search(q, 30)) ?? [];
      setHits(res);
      setSelectedIdx((idx) => (res.length === 0 ? 0 : Math.min(idx, res.length - 1)));
    } catch (err) {
      setErrorMsg(String(err));
    }
  }, []);

  useEffect(() => {
    SettingsService.GetSettings()
      .then((cfg: Settings) => {
        applyTheme(cfg.theme);
        setShowPreview(cfg.showLauncherPreview);
      })
      .catch(() => undefined);

    runSearch("");

    const unsubs = [
      Events.On("launcher:opened", () => {
        setActionMode(false);
        setTempArgsMode(false);
        setTempArgs("");
        setErrorMsg("");
        setSelectedIdx(0);
        runSearch(searchInputRef.current?.value ?? "");
        setTimeout(() => {
          searchInputRef.current?.focus();
          searchInputRef.current?.select();
        }, 20);
      }),
      Events.On("apps:changed", () => {
        runSearch(searchInputRef.current?.value ?? "");
      }),
      Events.On("process:status", () => {
        runSearch(searchInputRef.current?.value ?? "");
      }),
      Events.On("settings:changed", (ev) => {
        const cfg = ev.data as Settings | null;
        if (cfg) {
          applyTheme(cfg.theme);
          setShowPreview(cfg.showLauncherPreview);
        }
      }),
    ];

    return () => {
      for (const u of unsubs) u();
    };
  }, [runSearch]);

  useEffect(() => {
    setSelectedIdx(0);
    setActionMode(false);
    setTempArgsMode(false);
    runSearch(query);
  }, [query, runSearch]);

  useEffect(() => {
    if (!actionMode) {
      itemRefs.current[selectedIdx]?.scrollIntoView({ block: "nearest" });
    } else {
      actionRefs.current[actionIdx]?.scrollIntoView({ block: "nearest" });
    }
  }, [selectedIdx, actionIdx, actionMode]);

  useEffect(() => {
    if (tempArgsMode) {
      setTimeout(() => tempArgsInputRef.current?.focus(), 10);
    }
  }, [tempArgsMode]);

  const selectedHit = hits[selectedIdx] ?? null;
  const selectedApp: AppOverview | null = selectedHit?.app ?? null;

  const launchAndHide = useCallback(
    async (app: AppOverview, opts?: { entryId?: string; extraArgs?: string; forceAdmin?: boolean }) => {
      try {
        setErrorMsg("");
        await AppService.LaunchApp({
          appId: app.id,
          entryId: opts?.entryId ?? "",
          extraArgs: opts?.extraArgs ?? "",
          forceAdmin: opts?.forceAdmin ?? false,
        });
        await WindowService.HideLauncher();
      } catch (err) {
        setErrorMsg(String(err));
      }
    },
    []
  );

  const actions = useMemo<LauncherAction[]>(() => {
    if (!selectedApp) return [];
    const entries = selectedApp.entries ?? [];
    const primary = entries.find((e) => e.isPrimary) ?? entries[0];
    const subEntries = entries.filter((e) => !primary || e.id !== primary.id);

    const list: LauncherAction[] = [
      {
        id: "launch-default",
        label: primary ? `启动 ${primary.name}` : "启动默认入口",
        hint: "Enter",
        run: () => launchAndHide(selectedApp),
      },
    ];

    for (const sub of subEntries) {
      list.push({
        id: `entry-${sub.id}`,
        label: `启动子入口：${sub.name}`,
        hint: sub.relPath,
        run: () => launchAndHide(selectedApp, { entryId: sub.id }),
      });
    }

    list.push(
      {
        id: "launch-admin",
        label: "以管理员身份运行",
        hint: "Ctrl+Enter",
        run: () => launchAndHide(selectedApp, { forceAdmin: true }),
      },
      {
        id: "launch-args",
        label: "带临时参数运行...",
        hint: "Args",
        run: async () => {
          setTempArgsMode(true);
        },
      },
      {
        id: "reveal-dir",
        label: "打开所在目录",
        run: async () => {
          await AppService.RevealAppDir(selectedApp.id);
          await WindowService.HideLauncher();
        },
      },
      {
        id: "open-terminal",
        label: "在终端中打开目录",
        run: async () => {
          await AppService.OpenAppTerminal(selectedApp.id);
          await WindowService.HideLauncher();
        },
      },
      {
        id: "copy-path",
        label: primary?.entryType === EntryType.EntryCommand ? "复制启动命令" : "复制主程序路径",
        run: async () => {
          const text =
            primary?.entryType === EntryType.EntryCommand
              ? primary.relPath || selectedApp.primaryExe
              : joinWinPath(selectedApp.resolvedRoot, selectedApp.primaryExe);
          await Clipboard.SetText(text);
          await WindowService.HideLauncher();
        },
      }
    );

    if (selectedApp.isBuilding) {
      list.push({
        id: "cancel-build",
        label: "取消当前构建",
        danger: true,
        run: async () => {
          await AppService.TerminateApp(selectedApp.id);
          await runSearch(query);
          setActionMode(false);
        },
      });
    }

    if (selectedApp.isRunning) {
      list.push(
        {
          id: "restart-proc",
          label: "重启运行中进程",
          run: async () => {
            await AppService.RestartApp(selectedApp.id);
            await WindowService.HideLauncher();
          },
        },
        {
          id: "terminate-proc",
          label: `结束进程 (${(selectedApp.runningPids ?? []).length})`,
          danger: true,
          run: async () => {
            await AppService.TerminateApp(selectedApp.id);
            await runSearch(query);
            setActionMode(false);
          },
        }
      );
    }

    list.push({
      id: "open-main",
      label: "在主窗口中编辑 / 查看笔记",
      run: async () => {
        await WindowService.OpenAppInMain(selectedApp.id);
      },
    });

    return list;
  }, [selectedApp, launchAndHide, query, runSearch]);

  const handleKeyDown = async (e: React.KeyboardEvent) => {
    if (tempArgsMode) {
      if (e.key === "Escape") {
        e.preventDefault();
        setTempArgsMode(false);
        return;
      }
      if (e.key === "Enter" && selectedApp) {
        e.preventDefault();
        await launchAndHide(selectedApp, { extraArgs: tempArgs });
      }
      return;
    }

    if (e.key === "Tab") {
      e.preventDefault();
      if (!selectedApp) return;
      setActionIdx(0);
      setActionMode((v) => !v);
      return;
    }

    if (e.key === "Escape") {
      e.preventDefault();
      if (actionMode) {
        setActionMode(false);
        setTimeout(() => searchInputRef.current?.focus(), 10);
        return;
      }
      if (query.trim() !== "") {
        setQuery("");
        return;
      }
      await WindowService.HideLauncher();
      return;
    }

    if (actionMode) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActionIdx((i) => (actions.length ? (i + 1) % actions.length : 0));
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActionIdx((i) => (actions.length ? (i - 1 + actions.length) % actions.length : 0));
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        const act = actions[actionIdx];
        if (act) await act.run();
      }
      return;
    }

    // List mode
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIdx((i) => (hits.length ? Math.min(i + 1, hits.length - 1) : 0));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIdx((i) => (hits.length ? Math.max(i - 1, 0) : 0));
      return;
    }
    if (e.key === "Enter" && selectedApp) {
      e.preventDefault();
      await launchAndHide(selectedApp, { forceAdmin: e.ctrlKey || e.metaKey });
    }
  };

  return (
    <div
      onKeyDown={handleKeyDown}
      className="h-screen w-screen flex flex-col bg-[var(--bg-surface)] text-[var(--text-primary)] border border-[var(--border-strong)] rounded-xl overflow-hidden shadow-2xl"
    >
      {/* Top Search Bar */}
      <div className="px-4 py-3.5 border-b border-[var(--border)] flex items-center gap-3 bg-[var(--bg-surface)]">
        <Search className="w-5 h-5 text-[var(--accent)] shrink-0" />
        <input
          ref={searchInputRef}
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索软件、拼音首字母、别名、#标签、@分类 或 :running..."
          className="flex-1 bg-transparent text-base text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none"
        />
        <button
          type="button"
          onClick={() => WindowService.ShowMain()}
          className="px-2 py-1 rounded text-[13px] bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
        >
          主窗口
        </button>
      </div>

      {errorMsg && (
        <div className="px-4 py-1.5 bg-red-500/15 border-b border-red-500/30 text-[13px] text-[var(--danger)]">
          {errorMsg}
        </div>
      )}

      {/* Body */}
      <div className="flex-1 min-h-0 flex overflow-hidden">
        {/* Left Results List */}
        <div className="flex-1 min-w-0 overflow-y-auto p-2 space-y-0.5">
          {hits.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center text-center px-6">
              <div className="text-sm font-medium text-[var(--text-secondary)]">
                没有找到匹配的软件
              </div>
              <div className="text-[13px] text-[var(--text-muted)] mt-1">
                按主窗口按钮或拖入免安装工具来扩充你的软件库
              </div>
            </div>
          ) : (
            hits.map((hit, idx) => {
              const app = hit.app;
              const active = idx === selectedIdx;
              return (
                <div
                  key={app.id}
                  ref={(el) => {
                    itemRefs.current[idx] = el;
                  }}
                  onMouseEnter={() => {
                    if (!actionMode && !tempArgsMode) setSelectedIdx(idx);
                  }}
                  onClick={() => launchAndHide(app)}
                  className={`flex items-center justify-between gap-2.5 px-2.5 py-2 rounded-lg cursor-pointer transition-colors ${
                    active
                      ? "bg-[var(--bg-selected)] text-[var(--text-primary)]"
                      : "hover:bg-[var(--bg-hover)]"
                  }`}
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <AppIcon name={app.name} iconUrl={app.iconUrl} size="sm" isRunning={app.isRunning} />
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="text-sm font-medium truncate">{app.name}</span>
                        {app.currentVersion && (
                          <span className="text-xs font-mono text-[var(--text-muted)]">
                            {app.currentVersion}
                          </span>
                        )}
                      </div>
                      <div className="flex items-center gap-1.5 text-xs text-[var(--text-muted)] truncate">
                        {(app.aliases ?? []).length > 0 && (
                          <span className="font-mono">{(app.aliases ?? []).join("/")}</span>
                        )}
                        {(app.tags ?? []).slice(0, 2).map((t) => (
                          <span key={t}>#{t}</span>
                        ))}
                      </div>
                    </div>
                  </div>
                  {app.isBuilding ? (
                    <span className="inline-flex items-center gap-1 text-xs text-[var(--accent)] shrink-0">
                      <Loader2 className="w-3 h-3 animate-spin" />
                      构建中
                    </span>
                  ) : app.isRunning ? (
                    <span className="text-xs text-emerald-400 shrink-0">运行中</span>
                  ) : null}
                </div>
              );
            })
          )}
        </div>

        {/* Right Panel: TempArgs / Secondary Actions / Preview */}
        {selectedApp && (tempArgsMode || actionMode || showPreview) && (
          <div className="w-[340px] shrink-0 border-l border-[var(--border)] bg-[var(--bg-elevated)] flex flex-col overflow-hidden">
            {tempArgsMode ? (
              <div className="p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-semibold">带临时参数运行</span>
                  <button
                    type="button"
                    onClick={() => setTempArgsMode(false)}
                    className="inline-flex items-center gap-1 text-[13px] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  >
                    <ArrowLeft className="w-3 h-3" /> 返回
                  </button>
                </div>
                <div className="text-[13px] text-[var(--text-secondary)]">
                  为 <span className="font-medium text-[var(--text-primary)]">{selectedApp.name}</span>{" "}
                  追加本次启动参数：
                </div>
                <input
                  ref={tempArgsInputRef}
                  value={tempArgs}
                  onChange={(e) => setTempArgs(e.target.value)}
                  placeholder="例如 --port 9000 --verbose"
                  className="w-full px-2.5 py-2 rounded-md text-sm font-mono bg-[var(--bg-surface)] border border-[var(--accent)] text-[var(--text-primary)] focus:outline-none"
                />
                <button
                  type="button"
                  onClick={() => launchAndHide(selectedApp, { extraArgs: tempArgs })}
                  className="w-full py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] text-white hover:bg-[var(--accent-hover)]"
                >
                  立即启动 (Enter)
                </button>
              </div>
            ) : actionMode ? (
              <div className="flex-1 flex flex-col overflow-hidden">
                <div className="px-3 py-2 border-b border-[var(--border)] flex items-center justify-between">
                  <span className="text-[13px] font-semibold text-[var(--text-secondary)] truncate">
                    动作 · {selectedApp.name}
                  </span>
                  <button
                    type="button"
                    onClick={() => {
                      setActionMode(false);
                      searchInputRef.current?.focus();
                    }}
                    className="text-[13px] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  >
                    返回 (Tab)
                  </button>
                </div>
                <div className="flex-1 overflow-y-auto p-1.5 space-y-0.5">
                  {actions.map((act, idx) => {
                    const active = idx === actionIdx;
                    const Icon =
                      act.id === "launch-admin"
                        ? Shield
                        : act.id === "reveal-dir"
                        ? FolderOpen
                        : act.id === "open-terminal"
                        ? Terminal
                        : act.id === "copy-path"
                        ? Copy
                        : act.id === "launch-args"
                        ? SlidersHorizontal
                        : act.id === "restart-proc"
                        ? RotateCw
                        : act.id === "terminate-proc" || act.id === "cancel-build"
                        ? Square
                        : act.id === "open-main"
                        ? Edit3
                        : Play;

                    return (
                      <button
                        key={act.id}
                        ref={(el) => {
                          actionRefs.current[idx] = el;
                        }}
                        type="button"
                        onMouseEnter={() => setActionIdx(idx)}
                        onClick={() => act.run()}
                        className={`w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-md text-sm text-left transition-colors ${
                          active
                            ? "bg-[var(--bg-selected)] text-[var(--text-primary)]"
                            : "hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                        } ${act.danger ? "!text-[var(--danger)]" : ""}`}
                      >
                        <span className="flex items-center gap-2 min-w-0">
                          <Icon className="w-3.5 h-3.5 shrink-0" />
                          <span className="truncate">{act.label}</span>
                        </span>
                        {act.hint && (
                          <span className="text-xs font-mono text-[var(--text-muted)] truncate max-w-[90px]">
                            {act.hint}
                          </span>
                        )}
                      </button>
                    );
                  })}
                </div>
              </div>
            ) : (
              <div className="flex-1 overflow-y-auto p-3.5 space-y-3">
                <div className="flex items-center gap-2.5">
                  <AppIcon
                    name={selectedApp.name}
                    iconUrl={selectedApp.iconUrl}
                    size="md"
                    isRunning={selectedApp.isRunning}
                  />
                  <div className="min-w-0">
                    <div className="text-sm font-semibold truncate">{selectedApp.name}</div>
                    <div className="text-[13px] text-[var(--text-muted)] flex items-center gap-1.5 mt-0.5">
                      <span>
                        {selectedApp.storageMode === StorageMode.StorageManaged
                          ? "托管库"
                          : "仅链接"}
                      </span>
                      {selectedApp.currentVersion && (
                        <span className="font-mono">· {selectedApp.currentVersion}</span>
                      )}
                    </div>
                  </div>
                </div>

                {selectedApp.description && (
                  <p className="text-[13px] text-[var(--text-secondary)] leading-relaxed">
                    {selectedApp.description}
                  </p>
                )}

                <div className="pt-2 border-t border-[var(--border)] space-y-1">
                  <div className="text-xs font-medium text-[var(--text-muted)]">
                    个人笔记
                  </div>
                  {selectedApp.notesMd ? (
                    <pre className="whitespace-pre-wrap font-sans text-[13px] text-[var(--text-secondary)] leading-relaxed">
                      {selectedApp.notesMd}
                    </pre>
                  ) : (
                    <div className="text-[13px] text-[var(--text-muted)] italic">暂无笔记</div>
                  )}
                </div>

                {(selectedApp.entries ?? []).length > 1 && (
                  <div className="pt-2 border-t border-[var(--border)] space-y-1">
                    <div className="text-xs font-medium text-[var(--text-muted)]">
                      可用入口 ({(selectedApp.entries ?? []).length})
                    </div>
                    <div className="space-y-1">
                      {(selectedApp.entries ?? []).map((en) => (
                        <div
                          key={en.id}
                          className="text-[13px] flex items-center justify-between text-[var(--text-secondary)]"
                        >
                          <span className="truncate">{en.name}</span>
                          <span className="font-mono text-xs text-[var(--text-muted)]">
                            {en.entryType}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Bottom Keycap Bar */}
      <footer className="px-4 py-2 border-t border-[var(--border)] bg-[var(--bg-surface)] flex items-center justify-between text-[13px] text-[var(--text-muted)]">
        <div className="flex items-center gap-3">
          <span className="inline-flex items-center gap-1">
            <kbd>
              <CornerDownLeft className="w-3 h-3" />
            </kbd>
            启动
          </span>
          <span className="inline-flex items-center gap-1">
            <kbd>Ctrl+↵</kbd> 管理员
          </span>
          <span className="inline-flex items-center gap-1">
            <kbd>Tab</kbd> {actionMode ? "返回列表" : "更多动作"}
          </span>
          <span className="inline-flex items-center gap-1">
            <kbd>Esc</kbd> {actionMode || tempArgsMode ? "返回" : "隐藏"}
          </span>
        </div>
        {selectedHit && (
          <span className="font-mono text-xs opacity-75">
            {hits.length} 项匹配 · {selectedHit.matchReason}
          </span>
        )}
      </footer>
    </div>
  );
}

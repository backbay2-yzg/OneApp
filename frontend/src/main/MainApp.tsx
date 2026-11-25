import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  FilePlus2,
  FolderPlus,
  Hash,
  Layers,
  LayoutGrid,
  List,
  Loader2,
  Pin,
  Play,
  Plus,
  Search,
  Settings,
  Sparkles,
  Terminal,
  Trash2,
  Upload,
  Zap,
} from "lucide-react";
import { StorageMode, type AppOverview } from "../../bindings/oneapp/internal/domain/models.js";
import { AppIcon } from "../shared/AppIcon.js";
import { AppService, useAppStore, WindowService } from "../shared/store.js";
import { CreateCommandModal } from "./CreateCommandModal.js";
import { DetailDrawer } from "./DetailDrawer.js";
import { IngestModal } from "./IngestModal.js";
import { SettingsModal } from "./SettingsModal.js";

type NavFilter =
  | { kind: "all" }
  | { kind: "pinned" }
  | { kind: "running" }
  | { kind: "category"; id: string; name: string }
  | { kind: "tag"; name: string };

type SortMode = "frecency" | "name" | "launches" | "newest";
type ViewMode = "grid" | "list";

export function MainApp() {
  const init = useAppStore((s) => s.init);
  const apps = useAppStore((s) => s.apps);
  const categories = useAppStore((s) => s.categories);
  const tags = useAppStore((s) => s.tags);
  const settings = useAppStore((s) => s.settings);
  const hotkeyStatus = useAppStore((s) => s.hotkeyStatus);
  const selectedAppId = useAppStore((s) => s.selectedAppId);
  const selectApp = useAppStore((s) => s.selectApp);
  const setPendingIngests = useAppStore((s) => s.setPendingIngests);
  const setCommandModalOpen = useAppStore((s) => s.setCommandModalOpen);
  const setSettingsOpen = useAppStore((s) => s.setSettingsOpen);
  const toasts = useAppStore((s) => s.toasts);
  const dismissToast = useAppStore((s) => s.dismissToast);
  const pushToast = useAppStore((s) => s.pushToast);

  const [nav, setNav] = useState<NavFilter>({ kind: "all" });
  const [query, setQuery] = useState("");
  const [sortMode, setSortMode] = useState<SortMode>("frecency");
  const [viewMode, setViewMode] = useState<ViewMode>("grid");
  const [addingCat, setAddingCat] = useState(false);
  const [newCatName, setNewCatName] = useState("");

  useEffect(() => {
    let cleanup: (() => void) | undefined;
    init().then((fn) => {
      cleanup = fn;
    });
    return () => {
      if (cleanup) cleanup();
    };
  }, [init]);

  const runningCount = useMemo(() => apps.filter((a) => a.isRunning).length, [apps]);
  const pinnedCount = useMemo(() => apps.filter((a) => a.isPinned).length, [apps]);

  const filteredApps = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = apps.filter((a) => {
      if (nav.kind === "pinned" && !a.isPinned) return false;
      if (nav.kind === "running" && !a.isRunning) return false;
      if (nav.kind === "category" && a.categoryId !== nav.id) return false;
      if (nav.kind === "tag" && !(a.tags ?? []).some((t) => t.toLowerCase() === nav.name.toLowerCase())) {
        return false;
      }
      if (!q) return true;
      const inName = a.name.toLowerCase().includes(q);
      const inDesc = a.description.toLowerCase().includes(q);
      const inAlias = (a.aliases ?? []).some((al) => al.toLowerCase().includes(q));
      const inTags = (a.tags ?? []).some((t) => t.toLowerCase().includes(q));
      return inName || inDesc || inAlias || inTags;
    });

    return [...list].sort((a, b) => {
      switch (sortMode) {
        case "name":
          return a.name.localeCompare(b.name, "zh-CN");
        case "launches":
          return b.launchCount - a.launchCount || b.lastLaunchedAt - a.lastLaunchedAt;
        case "newest":
          return b.createdAt - a.createdAt;
        case "frecency":
        default:
          if (a.isPinned !== b.isPinned) return a.isPinned ? -1 : 1;
          if (a.isRunning !== b.isRunning) return a.isRunning ? -1 : 1;
          if (a.lastLaunchedAt !== b.lastLaunchedAt) return b.lastLaunchedAt - a.lastLaunchedAt;
          return b.launchCount - a.launchCount;
      }
    });
  }, [apps, nav, query, sortMode]);

  const handleCreateCategory = async () => {
    const trimmed = newCatName.trim();
    if (!trimmed) {
      setAddingCat(false);
      return;
    }
    try {
      await AppService.CreateCategory(trimmed);
      setNewCatName("");
      setAddingCat(false);
    } catch (err) {
      pushToast("error", `创建分类失败: ${String(err)}`);
    }
  };

  const handleDeleteCategory = async (id: string, name: string) => {
    try {
      await AppService.DeleteCategory(id);
      if (nav.kind === "category" && nav.id === id) {
        setNav({ kind: "all" });
      }
      pushToast("info", `已删除分类「${name}」`);
    } catch (err) {
      pushToast("error", String(err));
    }
  };

  const handlePickFiles = async () => {
    try {
      const analyses = await AppService.PickFilesForIngest();
      if (analyses && analyses.length > 0) {
        setPendingIngests(analyses);
      }
    } catch (err) {
      pushToast("error", String(err));
    }
  };

  const handlePickFolder = async () => {
    try {
      const analyses = await AppService.PickFolderForIngest();
      if (analyses && analyses.length > 0) {
        setPendingIngests(analyses);
      }
    } catch (err) {
      pushToast("error", String(err));
    }
  };

  const handleQuickLaunch = async (app: AppOverview) => {
    try {
      await AppService.LaunchApp({
        appId: app.id,
        entryId: "",
        extraArgs: "",
        forceAdmin: false,
      });
    } catch (err) {
      pushToast("error", `启动失败: ${String(err)}`);
    }
  };

  const navTitle =
    nav.kind === "all"
      ? "全部软件"
      : nav.kind === "pinned"
      ? "收藏置顶"
      : nav.kind === "running"
      ? "正在运行"
      : nav.kind === "category"
      ? nav.name
      : `#${nav.name}`;

  return (
    <div className="h-screen w-screen flex overflow-hidden bg-[var(--bg-app)] text-[var(--text-primary)]">
      {/* Left Sidebar */}
      <aside className="w-[244px] shrink-0 border-r border-[var(--border)] bg-[var(--bg-sidebar)] flex flex-col">
        <div className="px-4 py-3.5 flex items-center justify-between border-b border-[var(--border)]">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-md bg-gradient-to-br from-indigo-500 to-blue-600 flex items-center justify-center text-white text-sm font-bold shadow-sm">
              1
            </div>
            <span className="text-base font-semibold tracking-tight">OneApp</span>
          </div>
          <button
            type="button"
            title={`呼出极速启动条 (${settings?.launcherHotkey ?? "Alt+Space"})`}
            onClick={() => WindowService.ShowLauncher()}
            className="p-1.5 rounded-md text-[var(--text-muted)] hover:text-[var(--accent)] hover:bg-[var(--bg-hover)]"
          >
            <Zap className="w-4 h-4" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto px-2.5 py-3 space-y-5">
          {/* Smart Views */}
          <div className="space-y-0.5">
            <button
              type="button"
              onClick={() => setNav({ kind: "all" })}
              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-md text-sm transition-colors ${
                nav.kind === "all"
                  ? "bg-[var(--bg-selected)] text-[var(--accent)] font-medium"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <span className="flex items-center gap-2">
                <Layers className="w-4 h-4" /> 全部软件
              </span>
              <span className="text-[13px] font-mono opacity-75">{apps.length}</span>
            </button>

            <button
              type="button"
              onClick={() => setNav({ kind: "pinned" })}
              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-md text-sm transition-colors ${
                nav.kind === "pinned"
                  ? "bg-[var(--bg-selected)] text-[var(--accent)] font-medium"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <span className="flex items-center gap-2">
                <Pin className="w-4 h-4" /> 收藏置顶
              </span>
              <span className="text-[13px] font-mono opacity-75">{pinnedCount}</span>
            </button>

            <button
              type="button"
              onClick={() => setNav({ kind: "running" })}
              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-md text-sm transition-colors ${
                nav.kind === "running"
                  ? "bg-[var(--bg-selected)] text-[var(--accent)] font-medium"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <span className="flex items-center gap-2">
                <Activity className="w-4 h-4" /> 正在运行
              </span>
              {runningCount > 0 && (
                <span className="px-1.5 py-0.2 rounded-full text-xs font-mono bg-emerald-500/15 text-emerald-400">
                  {runningCount}
                </span>
              )}
            </button>
          </div>

          {/* Categories */}
          <div>
            <div className="px-2.5 mb-1.5 flex items-center justify-between text-[13px] font-medium text-[var(--text-muted)]">
              <span>分类</span>
              <button
                type="button"
                title="新建分类"
                onClick={() => setAddingCat(true)}
                className="p-0.5 rounded hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              >
                <Plus className="w-3.5 h-3.5" />
              </button>
            </div>

            {addingCat && (
              <div className="px-2 mb-1.5">
                <input
                  autoFocus
                  value={newCatName}
                  onChange={(e) => setNewCatName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") handleCreateCategory();
                    if (e.key === "Escape") {
                      setAddingCat(false);
                      setNewCatName("");
                    }
                  }}
                  onBlur={handleCreateCategory}
                  placeholder="分类名称，回车确认"
                  className="w-full px-2 py-1 rounded text-sm bg-[var(--bg-elevated)] border border-[var(--accent)] text-[var(--text-primary)] focus:outline-none"
                />
              </div>
            )}

            <div className="space-y-0.5">
              {categories.map((c) => {
                const count = apps.filter((a) => a.categoryId === c.id).length;
                const active = nav.kind === "category" && nav.id === c.id;
                return (
                  <div
                    key={c.id}
                    className={`group flex items-center justify-between px-2.5 py-1.5 rounded-md text-sm cursor-pointer transition-colors ${
                      active
                        ? "bg-[var(--bg-selected)] text-[var(--accent)] font-medium"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                    }`}
                    onClick={() => setNav({ kind: "category", id: c.id, name: c.name })}
                  >
                    <span className="truncate">{c.name}</span>
                    <div className="flex items-center gap-1">
                      <button
                        type="button"
                        title="删除分类"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleDeleteCategory(c.id, c.name);
                        }}
                        className="hidden group-hover:inline-flex p-0.5 rounded hover:text-[var(--danger)]"
                      >
                        <Trash2 className="w-3 h-3" />
                      </button>
                      <span className="text-[13px] font-mono opacity-70">{count}</span>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Tags */}
          {tags.length > 0 && (
            <div>
              <div className="px-2.5 mb-1.5 text-[13px] font-medium text-[var(--text-muted)]">
                标签
              </div>
              <div className="px-1.5 flex flex-wrap gap-1">
                {tags.map((t) => {
                  const active = nav.kind === "tag" && nav.name === t.name;
                  return (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() =>
                        setNav(active ? { kind: "all" } : { kind: "tag", name: t.name })
                      }
                      className={`inline-flex items-center gap-0.5 px-2 py-0.5 rounded-full text-[13px] border transition-colors ${
                        active
                          ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]"
                          : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                      }`}
                    >
                      <Hash className="w-2.5 h-2.5 opacity-70" />
                      {t.name}
                    </button>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        {/* Hotkey status & Settings footer */}
        <div className="p-2.5 border-t border-[var(--border)] space-y-2">
          {!hotkeyStatus.registered && hotkeyStatus.error && (
            <button
              type="button"
              onClick={() => setSettingsOpen(true)}
              className="w-full text-left p-2 rounded-md bg-amber-500/10 border border-amber-500/30 text-[13px] text-[var(--warning)] flex items-start gap-1.5"
            >
              <AlertTriangle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
              <span className="line-clamp-2">热键 {hotkeyStatus.accelerator} 冲突，点此修改</span>
            </button>
          )}
          <button
            type="button"
            onClick={() => setSettingsOpen(true)}
            className="w-full flex items-center justify-between px-2.5 py-1.5 rounded-md text-sm text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          >
            <span className="flex items-center gap-2">
              <Settings className="w-4 h-4" /> 设置与热键
            </span>
            <kbd>{settings?.launcherHotkey ?? "Alt+Space"}</kbd>
          </button>
        </div>
      </aside>

      {/* Center Workspace + Wails Native Drop Target */}
      <main
        id="workspace-drop-zone"
        data-file-drop-target
        className="drop-zone-surface flex-1 min-w-0 flex flex-col bg-[var(--bg-app)]"
      >
        {/* Drag-over overlay shown automatically when Wails adds .file-drop-target-active */}
        <div className="drop-zone-overlay pointer-events-none absolute inset-3 z-40 rounded-xl border-2 border-dashed border-[var(--accent)] bg-[var(--bg-app)]/85 backdrop-blur-sm flex-col items-center justify-center gap-2">
          <div className="p-3 rounded-full bg-[var(--accent-soft)] text-[var(--accent)]">
            <Upload className="w-6 h-6" />
          </div>
          <div className="text-base font-semibold text-[var(--text-primary)]">
            松开即可导入到 OneApp
          </div>
          <div className="text-sm text-[var(--text-muted)]">
            支持 .exe、绿色整包文件夹、Electron / Node 项目、.zip / .7z、.bat / .ps1 / .lnk
          </div>
        </div>

        {/* Top Toolbar */}
        <header className="px-4 py-3 border-b border-[var(--border)] bg-[var(--bg-surface)] flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0">
            <h1 className="text-base font-semibold whitespace-nowrap">{navTitle}</h1>
            <div className="relative w-72 max-w-full">
              <Search className="w-4 h-4 text-[var(--text-muted)] absolute left-2.5 top-1/2 -translate-y-1/2" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="筛选名称、别名、标签..."
                className="w-full pl-9 pr-3 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <select
              value={sortMode}
              onChange={(e) => setSortMode(e.target.value as SortMode)}
              className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)]"
            >
              <option value="frecency">智能排序</option>
              <option value="name">名称排序</option>
              <option value="launches">启动次数</option>
              <option value="newest">最近添加</option>
            </select>

            <div className="flex items-center bg-[var(--bg-elevated)] border border-[var(--border)] rounded-md p-0.5">
              <button
                type="button"
                title="紧凑卡片视图"
                onClick={() => setViewMode("grid")}
                className={`p-1.5 rounded ${
                  viewMode === "grid"
                    ? "bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm"
                    : "text-[var(--text-muted)]"
                }`}
              >
                <LayoutGrid className="w-4 h-4" />
              </button>
              <button
                type="button"
                title="高密度列表视图"
                onClick={() => setViewMode("list")}
                className={`p-1.5 rounded ${
                  viewMode === "list"
                    ? "bg-[var(--bg-surface)] text-[var(--text-primary)] shadow-sm"
                    : "text-[var(--text-muted)]"
                }`}
              >
                <List className="w-4 h-4" />
              </button>
            </div>

            <button
              type="button"
              onClick={handlePickFiles}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white"
            >
              <FilePlus2 className="w-4 h-4" />
              添加文件
            </button>
            <button
              type="button"
              onClick={handlePickFolder}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
            >
              <FolderPlus className="w-4 h-4" />
              添加目录
            </button>
            <button
              type="button"
              onClick={() => setCommandModalOpen(true)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
            >
              <Terminal className="w-4 h-4" />
              添加项目/命令
            </button>
          </div>
        </header>

        {/* Apps Grid / List */}
        <div className="flex-1 overflow-y-auto p-4">
          {filteredApps.length === 0 ? (
            <div className="h-full flex items-center justify-center">
              <div className="max-w-md w-full rounded-xl border border-dashed border-[var(--border-strong)] bg-[var(--bg-surface)] p-8 text-center space-y-3">
                <div className="mx-auto w-10 h-10 rounded-xl bg-[var(--accent-soft)] text-[var(--accent)] flex items-center justify-center">
                  <Sparkles className="w-5 h-5" />
                </div>
                <div className="text-base font-semibold text-[var(--text-primary)]">
                  {apps.length === 0 ? "拖入你的免安装软件或项目开始管理" : "当前筛选条件下没有匹配软件"}
                </div>
                <p className="text-sm text-[var(--text-muted)] leading-relaxed">
                  直接将 <code className="font-mono">.exe</code>、绿色软件文件夹、
                  Electron / Node 项目、<code className="font-mono">.zip / .7z</code> 拖到此处即可入库。
                </p>
                {apps.length === 0 && (
                  <div className="pt-2 flex flex-wrap items-center justify-center gap-2">
                    <button
                      type="button"
                      onClick={handlePickFiles}
                      className="px-3.5 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white"
                    >
                      选择文件 / 压缩包
                    </button>
                    <button
                      type="button"
                      onClick={handlePickFolder}
                      className="px-3.5 py-1.5 rounded-md text-sm border border-[var(--border-strong)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
                    >
                      选择文件夹 / 项目
                    </button>
                    <button
                      type="button"
                      onClick={() => setCommandModalOpen(true)}
                      className="px-3.5 py-1.5 rounded-md text-sm border border-[var(--border-strong)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
                    >
                      自定义命令
                    </button>
                  </div>
                )}
              </div>
            </div>
          ) : viewMode === "grid" ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2.5">
              {filteredApps.map((app) => {
                const selected = app.id === selectedAppId;
                return (
                  <div
                    key={app.id}
                    onClick={() => selectApp(app.id)}
                    onDoubleClick={() => handleQuickLaunch(app)}
                    className={`group rounded-lg border p-3 cursor-pointer transition-all ${
                      selected
                        ? "border-[var(--accent)] bg-[var(--bg-selected)]"
                        : "border-[var(--border)] bg-[var(--bg-surface)] hover:border-[var(--border-strong)] hover:bg-[var(--bg-elevated)]"
                    }`}
                  >
                    <div className="flex items-start gap-2.5">
                      <AppIcon name={app.name} iconUrl={app.iconUrl} isRunning={app.isRunning} />
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center justify-between gap-1">
                          <span className="text-sm font-semibold text-[var(--text-primary)] truncate">
                            {app.name}
                          </span>
                          <button
                            type="button"
                            title="立即启动"
                            onClick={(e) => {
                              e.stopPropagation();
                              handleQuickLaunch(app);
                            }}
                            className="opacity-0 group-hover:opacity-100 p-1 rounded bg-[var(--accent)] text-white hover:bg-[var(--accent-hover)] transition-opacity"
                          >
                            <Play className="w-3 h-3 fill-current" />
                          </button>
                        </div>
                        <div className="flex items-center gap-1.5 mt-1 text-xs">
                          {app.isBuilding && (
                            <span className="inline-flex items-center gap-1 text-[var(--accent)]">
                              <Loader2 className="w-3 h-3 animate-spin" />
                              构建中
                            </span>
                          )}
                          {app.currentVersion && (
                            <span className="font-mono text-[var(--text-muted)]">
                              {app.currentVersion}
                            </span>
                          )}
                          <span
                            className={
                              app.storageMode === StorageMode.StorageManaged
                                ? "text-[var(--accent)]"
                                : "text-amber-500"
                            }
                          >
                            {app.storageMode === StorageMode.StorageManaged ? "托管" : "链接"}
                          </span>
                          {app.categoryName && (
                            <span className="text-[var(--text-muted)] truncate">
                              · {app.categoryName}
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                    {(app.tags ?? []).length > 0 && (
                      <div className="mt-2.5 flex flex-wrap gap-1">
                        {(app.tags ?? []).slice(0, 3).map((t) => (
                          <span
                            key={t}
                            className="px-1.5 py-0.2 rounded text-xs bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)]"
                          >
                            #{t}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] divide-y divide-[var(--border)]">
              {filteredApps.map((app) => {
                const selected = app.id === selectedAppId;
                return (
                  <div
                    key={app.id}
                    onClick={() => selectApp(app.id)}
                    onDoubleClick={() => handleQuickLaunch(app)}
                    className={`group flex items-center justify-between gap-3 px-3 py-2 cursor-pointer transition-colors ${
                      selected ? "bg-[var(--bg-selected)]" : "hover:bg-[var(--bg-hover)]"
                    }`}
                  >
                    <div className="flex items-center gap-2.5 min-w-0 flex-1">
                      <AppIcon
                        name={app.name}
                        iconUrl={app.iconUrl}
                        size="sm"
                        isRunning={app.isRunning}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium text-[var(--text-primary)] truncate">
                            {app.name}
                          </span>
                          {app.isPinned && <Pin className="w-3 h-3 text-amber-400 shrink-0" />}
                          {app.currentVersion && (
                            <span className="text-xs font-mono text-[var(--text-muted)]">
                              {app.currentVersion}
                            </span>
                          )}
                        </div>
                        <div className="text-[13px] font-mono text-[var(--text-muted)] truncate">
                          {app.primaryExe || app.rootPath}
                        </div>
                      </div>
                    </div>

                    <div className="flex items-center gap-2 shrink-0">
                      {app.isBuilding && (
                        <span className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-[var(--accent-soft)] text-[var(--accent)]">
                          <Loader2 className="w-3 h-3 animate-spin" />
                          构建中
                        </span>
                      )}
                      <span className="text-xs px-1.5 py-0.5 rounded bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)]">
                        {app.storageMode === StorageMode.StorageManaged ? "托管" : "链接"}
                      </span>
                      <span className="text-[13px] font-mono text-[var(--text-muted)] w-14 text-right">
                        {app.launchCount} 次
                      </span>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleQuickLaunch(app);
                        }}
                        className="px-2.5 py-1 rounded text-[13px] font-medium bg-[var(--accent-soft)] text-[var(--accent)] hover:bg-[var(--accent)] hover:text-white transition-colors"
                      >
                        启动
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </main>

      {/* Right Detail Drawer */}
      <DetailDrawer />

      {/* Modals */}
      <IngestModal />
      <CreateCommandModal />
      <SettingsModal />

      {/* Toast Stack */}
      {toasts.length > 0 && (
        <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-sm">
          {toasts.map((t) => (
            <div
              key={t.id}
              onClick={() => dismissToast(t.id)}
              className={`px-3.5 py-2 rounded-lg border text-sm shadow-lg cursor-pointer backdrop-blur-md ${
                t.kind === "error"
                  ? "bg-red-950/90 border-red-500/40 text-red-200"
                  : t.kind === "success"
                  ? "bg-emerald-950/90 border-emerald-500/40 text-emerald-200"
                  : "bg-[var(--bg-surface)] border-[var(--border-strong)] text-[var(--text-primary)]"
              }`}
            >
              {t.text}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

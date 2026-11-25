import { useEffect, useRef, useState, type ReactNode } from "react";
import { Clipboard, Events } from "@wailsio/runtime";
import {
  FolderOpen,
  Terminal,
  Play,
  Shield,
  Pin,
  Trash2,
  Square,
  RotateCw,
  ImagePlus,
  RefreshCw,
  Plus,
  Save,
  Star,
  FileSearch,
  Eye,
  Edit3,
  Loader2,
  Copy,
  Eraser,
} from "lucide-react";
import {
  EntryType,
  StorageMode,
  type AppEntry,
  type AppLogSnapshot,
  type AppOverview,
  type EntryInput,
} from "../../bindings/oneapp/internal/domain/models.js";
import { AppIcon } from "../shared/AppIcon.js";
import { formatBytes, formatRelativeTime, splitCommaInput } from "../shared/format.js";
import { AppService, useAppStore } from "../shared/store.js";

type DetailTab = "basic" | "entries" | "notes" | "logs";

interface EntryDraft {
  id: string;
  name: string;
  entryType: EntryType;
  relPath: string;
  argsTemplate: string;
  workingDir: string;
  envPairs: { key: string; value: string }[];
  runAsAdmin: boolean;
  hideConsole: boolean;
  promptArgs: boolean;
  isPrimary: boolean;
  preLaunchCommand: string;
  preLaunchCondition: string;
  readyCheckPath: string;
}

function toEntryDraft(e: AppEntry): EntryDraft {
  const env = e.envVars ?? {};
  const envPairs = Object.entries(env).map(([key, value]) => ({
    key,
    value: value ?? "",
  }));
  return {
    id: e.id,
    name: e.name,
    entryType: e.entryType,
    relPath: e.relPath,
    argsTemplate: e.argsTemplate,
    workingDir: e.workingDir,
    envPairs,
    runAsAdmin: e.runAsAdmin,
    hideConsole: e.hideConsole,
    promptArgs: e.promptArgs,
    isPrimary: e.isPrimary,
    preLaunchCommand: e.preLaunchCommand ?? "",
    preLaunchCondition: e.preLaunchCondition ?? "",
    readyCheckPath: e.readyCheckPath ?? "",
  };
}

function renderInlineMarkdown(line: string): ReactNode[] {
  const parts = line.split(/(`[^`]+`)/g);
  return parts.map((part, i) => {
    if (part.startsWith("`") && part.endsWith("`") && part.length >= 2) {
      return (
        <code
          key={i}
          className="px-1 py-0.5 rounded bg-[var(--bg-elevated)] border border-[var(--border)] font-mono text-[13px] text-[var(--accent)]"
        >
          {part.slice(1, -1)}
        </code>
      );
    }
    return <span key={i}>{part}</span>;
  });
}

function SafeMarkdownView({ content }: { content: string }) {
  if (!content.trim()) {
    return <p className="text-sm text-[var(--text-muted)] italic">暂无使用笔记，点击上方「编辑」添加参数备忘或使用说明。</p>;
  }

  const lines = content.split(/\r?\n/);
  const nodes: ReactNode[] = [];
  let inCode = false;
  let codeLines: string[] = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.trim().startsWith("```")) {
      if (!inCode) {
        inCode = true;
        codeLines = [];
      } else {
        inCode = false;
        nodes.push(
          <pre
            key={`code-${i}`}
            className="p-2.5 rounded-md bg-[var(--bg-app)] border border-[var(--border)] font-mono text-[13px] text-[var(--text-primary)] overflow-x-auto my-2"
          >
            {codeLines.join("\n")}
          </pre>
        );
      }
      continue;
    }
    if (inCode) {
      codeLines.push(line);
      continue;
    }
    if (line.startsWith("### ")) {
      nodes.push(
        <h4 key={i} className="text-sm font-semibold text-[var(--text-primary)] mt-2.5 mb-1">
          {renderInlineMarkdown(line.slice(4))}
        </h4>
      );
    } else if (line.startsWith("## ")) {
      nodes.push(
        <h3 key={i} className="text-base font-semibold text-[var(--text-primary)] mt-3 mb-1">
          {renderInlineMarkdown(line.slice(3))}
        </h3>
      );
    } else if (line.startsWith("# ")) {
      nodes.push(
        <h2 key={i} className="text-lg font-semibold text-[var(--text-primary)] mt-3 mb-1.5">
          {renderInlineMarkdown(line.slice(2))}
        </h2>
      );
    } else if (line.trim().startsWith("- ") || line.trim().startsWith("* ")) {
      const txt = line.trim().slice(2);
      nodes.push(
        <div key={i} className="flex items-start gap-2 text-sm text-[var(--text-secondary)] pl-1 my-0.5">
          <span className="text-[var(--accent)] mt-0.5">•</span>
          <span>{renderInlineMarkdown(txt)}</span>
        </div>
      );
    } else if (!line.trim()) {
      nodes.push(<div key={i} className="h-2" />);
    } else {
      nodes.push(
        <p key={i} className="text-sm text-[var(--text-secondary)] leading-relaxed my-0.5">
          {renderInlineMarkdown(line)}
        </p>
      );
    }
  }

  return <div>{nodes}</div>;
}

export function DetailDrawer() {
  const apps = useAppStore((s) => s.apps);
  const selectedAppId = useAppStore((s) => s.selectedAppId);
  const categories = useAppStore((s) => s.categories);
  const pushToast = useAppStore((s) => s.pushToast);

  const summary = apps.find((a) => a.id === selectedAppId) ?? null;
  const [detail, setDetail] = useState<AppOverview | null>(null);
  const [tab, setTab] = useState<DetailTab>("basic");

  // Basic form state
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [aliasesText, setAliasesText] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [tagsText, setTagsText] = useState("");
  const [description, setDescription] = useState("");
  const [notesMd, setNotesMd] = useState("");
  const [notesPreview, setNotesPreview] = useState(false);

  // Entries state
  const [entryDrafts, setEntryDrafts] = useState<EntryDraft[]>([]);

  // Delete confirm state
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(true);
  const [saving, setSaving] = useState(false);

  // Logs state
  const [logSnap, setLogSnap] = useState<AppLogSnapshot | null>(null);
  const logEndRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setConfirmDelete(false);
    setDeleteFiles(true);
    if (!selectedAppId) {
      setDetail(null);
      setLogSnap(null);
      return;
    }
    let active = true;
    AppService.GetApp(selectedAppId)
      .then((res) => {
        if (active && res) {
          setDetail(res);
        }
      })
      .catch(() => {
        if (active && summary) {
          setDetail(summary);
        }
      });
    return () => {
      active = false;
    };
  }, [selectedAppId, summary?.updatedAt]);

  const app = detail ?? summary;
  const currentAppId = app?.id ?? "";
  const isBuilding = Boolean(summary?.isBuilding ?? detail?.isBuilding);
  const isRunning = Boolean(summary?.isRunning ?? detail?.isRunning);
  const runningPids = summary?.runningPids ?? detail?.runningPids ?? [];

  useEffect(() => {
    if (!currentAppId) return;
    let active = true;
    AppService.GetAppLogs(currentAppId)
      .then((snap) => {
        if (active) setLogSnap(snap);
      })
      .catch(() => undefined);

    const unsub = Events.On("app:log", (ev) => {
      const snap = ev.data as AppLogSnapshot | null;
      if (snap && snap.appId === currentAppId) {
        setLogSnap(snap);
      }
    });
    return () => {
      active = false;
      unsub();
    };
  }, [currentAppId]);

  useEffect(() => {
    if (tab === "logs") {
      logEndRef.current?.scrollIntoView({ block: "end" });
    }
  }, [tab, logSnap?.updatedAt, logSnap?.lines?.length]);

  useEffect(() => {
    if (!app) return;
    setName(app.name);
    setVersion(app.currentVersion ?? "");
    setAliasesText((app.aliases ?? []).join(", "));
    setCategoryId(app.categoryId ?? "");
    setTagsText((app.tags ?? []).join(", "));
    setDescription(app.description ?? "");
    setNotesMd(app.notesMd ?? "");
    setEntryDrafts((app.entries ?? []).map(toEntryDraft));
  }, [app?.id, app?.updatedAt]);

  if (!app) {
    return (
      <aside className="w-[384px] xl:w-[430px] shrink-0 border-l border-[var(--border)] bg-[var(--bg-surface)] flex items-center justify-center p-6 text-center">
        <div className="space-y-1.5">
          <div className="text-sm font-medium text-[var(--text-secondary)]">未选中任何软件</div>
          <p className="text-[13px] text-[var(--text-muted)]">
            从左侧列表选择一个软件查看入口配置、路径信息或个人笔记
          </p>
        </div>
      </aside>
    );
  }

  const handleLaunch = async (forceAdmin = false, entryId = "") => {
    try {
      await AppService.LaunchApp({
        appId: app.id,
        entryId,
        extraArgs: "",
        forceAdmin,
      });
    } catch (err) {
      pushToast("error", `启动失败: ${String(err)}`);
    }
  };

  const handleSaveBasicOrNotes = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const updated = await AppService.UpdateApp({
        id: app.id,
        name: name.trim() || app.name,
        aliases: splitCommaInput(aliasesText),
        description,
        notesMd,
        categoryId,
        tags: splitCommaInput(tagsText),
        isPinned: app.isPinned,
        hotkey: app.hotkey,
        currentVersion: version.trim(),
        persistRules: app.persistRules ?? [],
      });
      if (updated) setDetail(updated);
      pushToast("success", "已保存更改");
    } catch (err) {
      pushToast("error", `保存失败: ${String(err)}`);
    } finally {
      setSaving(false);
    }
  };

  const handleSaveEntries = async () => {
    if (saving || entryDrafts.length === 0) return;
    setSaving(true);
    try {
      const inputs: EntryInput[] = entryDrafts.map((d) => {
        const envVars: Record<string, string> = {};
        for (const pair of d.envPairs) {
          const k = pair.key.trim();
          if (k) envVars[k] = pair.value;
        }
        return {
          id: d.id,
          name: d.name.trim() || "入口",
          entryType: d.entryType,
          relPath: d.relPath.trim(),
          argsTemplate: d.argsTemplate,
          workingDir: d.workingDir,
          envVars,
          runAsAdmin: d.runAsAdmin,
          hideConsole: d.hideConsole,
          promptArgs: d.promptArgs,
          isPrimary: d.isPrimary,
          preLaunchCommand: d.preLaunchCondition ? d.preLaunchCommand.trim() : "",
          preLaunchCondition: d.preLaunchCondition,
          readyCheckPath: d.preLaunchCondition === "if_missing" ? d.readyCheckPath.trim() : "",
        };
      });
      const updated = await AppService.SaveEntries(app.id, inputs);
      if (updated) setDetail(updated);
      pushToast("success", "启动入口配置已保存");
    } catch (err) {
      pushToast("error", `保存入口失败: ${String(err)}`);
    } finally {
      setSaving(false);
    }
  };

  const handleAddEntry = async () => {
    let relPath = "";
    try {
      relPath = await AppService.PickEntryFile(app.id);
    } catch {
      relPath = "";
    }
    const ext = relPath.split(".").pop()?.toLowerCase();
    const detectedType =
      ext === "bat"
        ? EntryType.EntryBat
        : ext === "cmd"
        ? EntryType.EntryCmd
        : ext === "ps1"
        ? EntryType.EntryPs1
        : ext === "lnk"
        ? EntryType.EntryLnk
        : EntryType.EntryExe;

    setEntryDrafts((prev) => [
      ...prev,
      {
        id: "",
        name: relPath ? relPath.split("/").pop()?.replace(/\.[^.]+$/, "") || "新入口" : "新入口",
        entryType: detectedType,
        relPath,
        argsTemplate: "",
        workingDir: "",
        envPairs: [],
        runAsAdmin: false,
        hideConsole: detectedType === EntryType.EntryBat || detectedType === EntryType.EntryCmd || detectedType === EntryType.EntryPs1,
        promptArgs: false,
        isPrimary: prev.length === 0,
        preLaunchCommand: "",
        preLaunchCondition: "",
        readyCheckPath: "",
      },
    ]);
  };

  const handleAddCommandEntry = () => {
    setEntryDrafts((prev) => [
      ...prev,
      {
        id: "",
        name: "自定义命令",
        entryType: EntryType.EntryCommand,
        relPath: "",
        argsTemplate: "",
        workingDir: "{{APP_DIR}}",
        envPairs: [],
        runAsAdmin: false,
        hideConsole: true,
        promptArgs: false,
        isPrimary: prev.length === 0,
        preLaunchCommand: "",
        preLaunchCondition: "",
        readyCheckPath: "",
      },
    ]);
  };

  const updateEntry = (idx: number, patch: Partial<EntryDraft>) => {
    setEntryDrafts((prev) => prev.map((e, i) => (i === idx ? { ...e, ...patch } : e)));
  };

  const setPrimaryEntry = (idx: number) => {
    setEntryDrafts((prev) => prev.map((e, i) => ({ ...e, isPrimary: i === idx })));
  };

  const handleDelete = async () => {
    try {
      await AppService.DeleteApp(app.id, app.storageMode === StorageMode.StorageManaged && deleteFiles);
      setConfirmDelete(false);
      pushToast("info", `已移除 ${app.name}`);
    } catch (err) {
      pushToast("error", `删除失败: ${String(err)}`);
    }
  };

  return (
    <aside className="w-[384px] xl:w-[430px] shrink-0 border-l border-[var(--border)] bg-[var(--bg-surface)] flex flex-col overflow-hidden">
      {/* Header */}
      <div className="p-4 border-b border-[var(--border)] space-y-3">
        <div className="flex items-start gap-3">
          <div className="group relative">
            <AppIcon name={app.name} iconUrl={app.iconUrl} size="lg" isRunning={isRunning} />
            <div className="absolute -bottom-1 -right-1 hidden group-hover:flex items-center gap-0.5 bg-[var(--bg-surface)] border border-[var(--border-strong)] rounded-md p-0.5 shadow">
              <button
                type="button"
                title="自定义图标"
                onClick={() => AppService.PickCustomIcon(app.id).then((r) => r && setDetail(r))}
                className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              >
                <ImagePlus className="w-3 h-3" />
              </button>
              <button
                type="button"
                title="重新提取图标"
                onClick={() => AppService.ReextractIcon(app.id).then((r) => r && setDetail(r))}
                className="p-1 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              >
                <RefreshCw className="w-3 h-3" />
              </button>
            </div>
          </div>

          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1.5">
              <h2 className="text-base font-semibold text-[var(--text-primary)] truncate">
                {app.name}
              </h2>
              <button
                type="button"
                title={app.isPinned ? "取消收藏置顶" : "收藏置顶"}
                onClick={() => AppService.TogglePin(app.id, !app.isPinned)}
                className={`p-1 rounded hover:bg-[var(--bg-hover)] ${
                  app.isPinned ? "text-amber-400" : "text-[var(--text-muted)]"
                }`}
              >
                <Pin className="w-3.5 h-3.5" />
              </button>
            </div>

            <div className="flex flex-wrap items-center gap-1.5 mt-1">
              {app.currentVersion && (
                <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)]">
                  {app.currentVersion}
                </span>
              )}
              <span
                className={`px-1.5 py-0.5 rounded text-xs border ${
                  app.storageMode === StorageMode.StorageManaged
                    ? "bg-[var(--accent-soft)] border-[var(--accent)]/30 text-[var(--accent)]"
                    : "bg-amber-500/10 border-amber-500/30 text-amber-500"
                }`}
              >
                {app.storageMode === StorageMode.StorageManaged ? "托管库" : "仅链接"}
              </span>
              {isBuilding && (
                <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs bg-[var(--accent-soft)] border border-[var(--accent)]/30 text-[var(--accent)]">
                  <Loader2 className="w-3 h-3 animate-spin" />
                  构建中
                </span>
              )}
              {isRunning && (
                <span className="px-1.5 py-0.5 rounded text-xs bg-emerald-500/15 border border-emerald-500/30 text-emerald-400">
                  运行中 ({runningPids.length})
                </span>
              )}
            </div>
          </div>
        </div>

        {/* Action Bar */}
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            disabled={isBuilding}
            onClick={() => handleLaunch(false)}
            className="flex-1 inline-flex items-center justify-center gap-1.5 py-2 px-3 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white transition-colors disabled:opacity-55"
          >
            {isBuilding ? (
              <>
                <Loader2 className="w-4 h-4 animate-spin" />
                构建中...
              </>
            ) : (
              <>
                <Play className="w-4 h-4 fill-current" />
                启动
              </>
            )}
          </button>
          <button
            type="button"
            title="以管理员身份运行"
            disabled={isBuilding}
            onClick={() => handleLaunch(true)}
            className="p-2 rounded-md border border-[var(--border-strong)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40"
          >
            <Shield className="w-4 h-4" />
          </button>
          <button
            type="button"
            title="在资源管理器中打开目录"
            onClick={() => AppService.RevealAppDir(app.id).catch((e) => pushToast("error", String(e)))}
            className="p-2 rounded-md border border-[var(--border-strong)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          >
            <FolderOpen className="w-4 h-4" />
          </button>
          <button
            type="button"
            title="在终端中打开目录"
            onClick={() => AppService.OpenAppTerminal(app.id).catch((e) => pushToast("error", String(e)))}
            className="p-2 rounded-md border border-[var(--border-strong)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          >
            <Terminal className="w-4 h-4" />
          </button>
          {isBuilding && (
            <button
              type="button"
              title="取消构建"
              onClick={() => AppService.TerminateApp(app.id).catch((e) => pushToast("error", String(e)))}
              className="p-2 rounded-md border border-red-500/30 text-[var(--danger)] hover:bg-red-500/10"
            >
              <Square className="w-4 h-4 fill-current" />
            </button>
          )}
          {isRunning && (
            <>
              <button
                type="button"
                title="重启进程"
                onClick={() => AppService.RestartApp(app.id).catch((e) => pushToast("error", String(e)))}
                className="p-2 rounded-md border border-[var(--border-strong)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              >
                <RotateCw className="w-4 h-4" />
              </button>
              <button
                type="button"
                title="结束进程"
                onClick={() => AppService.TerminateApp(app.id).catch((e) => pushToast("error", String(e)))}
                className="p-2 rounded-md border border-red-500/30 text-[var(--danger)] hover:bg-red-500/10"
              >
                <Square className="w-4 h-4 fill-current" />
              </button>
            </>
          )}
          <button
            type="button"
            title="移除软件"
            onClick={() => setConfirmDelete((v) => !v)}
            className="p-2 rounded-md border border-[var(--border-strong)] text-[var(--text-muted)] hover:text-[var(--danger)] hover:border-red-500/30 hover:bg-red-500/10"
          >
            <Trash2 className="w-4 h-4" />
          </button>
        </div>

        {confirmDelete && (
          <div className="p-2.5 rounded-lg border border-red-500/30 bg-red-500/10 space-y-2">
            <div className="text-sm font-medium text-[var(--danger)]">
              确定移除「{app.name}」？
            </div>
            {app.storageMode === StorageMode.StorageManaged && (
              <label className="flex items-center gap-1.5 text-[13px] text-[var(--text-secondary)] cursor-pointer">
                <input
                  type="checkbox"
                  checked={deleteFiles}
                  onChange={(e) => setDeleteFiles(e.target.checked)}
                  className="accent-red-500"
                />
                同时删除托管库目录内的程序文件
              </label>
            )}
            <div className="flex items-center justify-end gap-1.5">
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="px-2.5 py-1 rounded text-[13px] border border-[var(--border-strong)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              >
                取消
              </button>
              <button
                type="button"
                onClick={handleDelete}
                className="px-2.5 py-1 rounded text-[13px] bg-red-500 hover:bg-red-600 text-white font-medium"
              >
                确认移除
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Tabs */}
      <div className="flex border-b border-[var(--border)] px-3">
        {(
          [
            ["basic", "基础信息"],
            ["entries", `入口 (${entryDrafts.length})`],
            ["notes", "个人笔记"],
            ["logs", "运行日志"],
          ] as [DetailTab, string][]
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            className={`px-2.5 py-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5 ${
              tab === key
                ? "border-[var(--accent)] text-[var(--text-primary)]"
                : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"
            }`}
          >
            <span>{label}</span>
            {key === "logs" && (logSnap?.status === "failed" || isBuilding) && (
              <span
                className={`w-1.5 h-1.5 rounded-full ${
                  logSnap?.status === "failed" ? "bg-[var(--danger)]" : "bg-[var(--accent)] animate-pulse"
                }`}
              />
            )}
          </button>
        ))}
      </div>

      {/* Body */}
      <div className="flex-1 overflow-y-auto p-4">
        {tab === "basic" && (
          <div className="space-y-3">
            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">软件名称</label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>

            <div className="grid grid-cols-2 gap-2.5">
              <div>
                <label className="block text-[13px] text-[var(--text-muted)] mb-1">分类</label>
                <select
                  value={categoryId}
                  onChange={(e) => setCategoryId(e.target.value)}
                  className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
                >
                  <option value="">未分类</option>
                  {categories.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-[13px] text-[var(--text-muted)] mb-1">版本号</label>
                <input
                  value={version}
                  onChange={(e) => setVersion(e.target.value)}
                  placeholder="例如 v1.0.0"
                  className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                />
              </div>
            </div>

            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                搜索别名（逗号分隔）
              </label>
              <input
                value={aliasesText}
                onChange={(e) => setAliasesText(e.target.value)}
                placeholder="例如 jt, snip, 截图"
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>

            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                标签（逗号分隔）
              </label>
              <input
                value={tagsText}
                onChange={(e) => setTagsText(e.target.value)}
                placeholder="例如 自研, CLI, 网络"
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>

            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">简介</label>
              <textarea
                rows={2}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="简短描述这个工具的用途..."
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)] resize-none"
              />
            </div>

            <button
              type="button"
              disabled={saving}
              onClick={handleSaveBasicOrNotes}
              className="w-full inline-flex items-center justify-center gap-1.5 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-50"
            >
              <Save className="w-3.5 h-3.5" />
              保存基础信息
            </button>

            {/* Metadata & Stats */}
            <div className="pt-3 border-t border-[var(--border)] space-y-1.5 text-[13px] text-[var(--text-secondary)]">
              <div className="flex justify-between">
                <span className="text-[var(--text-muted)]">启动次数</span>
                <span className="font-mono">{app.launchCount} 次</span>
              </div>
              <div className="flex justify-between">
                <span className="text-[var(--text-muted)]">最近启动</span>
                <span>{formatRelativeTime(app.lastLaunchedAt)}</span>
              </div>
              {app.storageMode === StorageMode.StorageManaged && (
                <div className="flex justify-between">
                  <span className="text-[var(--text-muted)]">占用空间</span>
                  <span className="font-mono">{formatBytes(app.diskSizeBytes)}</span>
                </div>
              )}
              <div className="pt-1">
                <div className="text-[var(--text-muted)] mb-0.5">实际路径</div>
                <div
                  className="font-mono text-xs px-2 py-1 rounded bg-[var(--bg-elevated)] border border-[var(--border)] break-all"
                  title={app.resolvedRoot}
                >
                  {app.resolvedRoot || app.rootPath}
                </div>
              </div>
            </div>
          </div>
        )}

        {tab === "entries" && (
          <div className="space-y-3">
            <div className="flex items-center justify-between gap-2">
              <span className="text-[13px] text-[var(--text-muted)]">
                支持 <code className="font-mono">{"{{APP_DIR}}"}</code>
              </span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={handleAddCommandEntry}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded text-[13px] border border-[var(--border-strong)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                >
                  <Terminal className="w-3 h-3" /> 添加命令
                </button>
                <button
                  type="button"
                  onClick={handleAddEntry}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded text-[13px] border border-[var(--border-strong)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                >
                  <Plus className="w-3 h-3" /> 添加文件
                </button>
              </div>
            </div>

            {entryDrafts.map((entry, idx) => (
              <div
                key={`${entry.id}-${idx}`}
                className="rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3 space-y-2.5"
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-1.5">
                    <button
                      type="button"
                      onClick={() => setPrimaryEntry(idx)}
                      title={entry.isPrimary ? "当前主入口" : "设为主入口"}
                      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs border ${
                        entry.isPrimary
                          ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]"
                          : "border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                      }`}
                    >
                      <Star className={`w-2.5 h-2.5 ${entry.isPrimary ? "fill-current" : ""}`} />
                      {entry.isPrimary ? "主入口" : "设为主入口"}
                    </button>
                    <button
                      type="button"
                      onClick={() => handleLaunch(false, entry.id)}
                      disabled={!entry.id}
                      title="测试启动此入口"
                      className="p-1 rounded text-[var(--text-secondary)] hover:text-[var(--accent)] hover:bg-[var(--bg-hover)] disabled:opacity-40"
                    >
                      <Play className="w-3 h-3" />
                    </button>
                  </div>
                  {entryDrafts.length > 1 && (
                    <button
                      type="button"
                      onClick={() =>
                        setEntryDrafts((prev) => {
                          const next = prev.filter((_, i) => i !== idx);
                          if (next.length > 0 && !next.some((x) => x.isPrimary)) {
                            next[0] = { ...next[0], isPrimary: true };
                          }
                          return next;
                        })
                      }
                      className="text-[13px] text-[var(--text-muted)] hover:text-[var(--danger)]"
                    >
                      移除
                    </button>
                  )}
                </div>

                <div className="grid grid-cols-3 gap-2">
                  <input
                    value={entry.name}
                    onChange={(e) => updateEntry(idx, { name: e.target.value })}
                    placeholder="入口名称"
                    className="col-span-2 px-2 py-1 rounded text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                  />
                  <select
                    value={entry.entryType}
                    onChange={(e) => updateEntry(idx, { entryType: e.target.value as EntryType })}
                    className="px-2 py-1 rounded text-sm font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                  >
                    <option value="exe">exe</option>
                    <option value="command">command</option>
                    <option value="bat">bat</option>
                    <option value="cmd">cmd</option>
                    <option value="ps1">ps1</option>
                    <option value="lnk">lnk</option>
                  </select>
                </div>

                <div className="flex gap-1.5">
                  <input
                    value={entry.relPath}
                    onChange={(e) => updateEntry(idx, { relPath: e.target.value })}
                    placeholder={
                      entry.entryType === EntryType.EntryCommand
                        ? "启动命令，如 pnpm run dev 或 python main.py"
                        : "相对路径，如 tool.exe"
                    }
                    className="flex-1 px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                  />
                  {entry.entryType !== EntryType.EntryCommand && (
                    <button
                      type="button"
                      title="浏览文件"
                      onClick={async () => {
                        const picked = await AppService.PickEntryFile(app.id);
                        if (picked) updateEntry(idx, { relPath: picked });
                      }}
                      className="px-2 rounded border border-[var(--border)] bg-[var(--bg-surface)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                    >
                      <FileSearch className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>

                <input
                  value={entry.argsTemplate}
                  onChange={(e) => updateEntry(idx, { argsTemplate: e.target.value })}
                  placeholder={
                    entry.entryType === EntryType.EntryCommand
                      ? "附加参数（可选）"
                      : "启动参数，如 --port 8080 --config {{APP_DIR}}\\cfg.json"
                  }
                  className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                />

                <input
                  value={entry.workingDir}
                  onChange={(e) => updateEntry(idx, { workingDir: e.target.value })}
                  placeholder={
                    entry.entryType === EntryType.EntryCommand
                      ? "工作目录（默认项目根目录 {{APP_DIR}}）"
                      : "工作目录（留空则默认入口所在目录）"
                  }
                  className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                />

                {/* Pre-launch build config */}
                <div className="space-y-1.5 pt-1 border-t border-[var(--border)]">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs text-[var(--text-muted)]">前置构建</span>
                    <select
                      value={entry.preLaunchCondition}
                      onChange={(e) => updateEntry(idx, { preLaunchCondition: e.target.value })}
                      className="px-1.5 py-0.5 rounded text-xs bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-secondary)]"
                    >
                      <option value="">不启用</option>
                      <option value="if_missing">产物缺失时自动构建</option>
                      <option value="always">每次启动前构建</option>
                    </select>
                  </div>
                  {entry.preLaunchCondition !== "" && (
                    <div className="space-y-1.5">
                      <input
                        value={entry.preLaunchCommand}
                        onChange={(e) => updateEntry(idx, { preLaunchCommand: e.target.value })}
                        placeholder="构建命令，如 pnpm run build"
                        className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                      />
                      {entry.preLaunchCondition === "if_missing" && (
                        <input
                          value={entry.readyCheckPath}
                          onChange={(e) => updateEntry(idx, { readyCheckPath: e.target.value })}
                          placeholder="产物检测路径，如 out/main/index.js"
                          className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                        />
                      )}
                    </div>
                  )}
                </div>

                {/* Env Vars */}
                <div className="space-y-1">
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-[var(--text-muted)]">环境变量</span>
                    <button
                      type="button"
                      onClick={() =>
                        updateEntry(idx, {
                          envPairs: [...entry.envPairs, { key: "", value: "" }],
                        })
                      }
                      className="text-xs text-[var(--accent)] hover:underline"
                    >
                      + 添加变量
                    </button>
                  </div>
                  {entry.envPairs.map((pair, pIdx) => (
                    <div key={pIdx} className="flex items-center gap-1">
                      <input
                        value={pair.key}
                        onChange={(e) => {
                          const next = [...entry.envPairs];
                          next[pIdx] = { ...pair, key: e.target.value };
                          updateEntry(idx, { envPairs: next });
                        }}
                        placeholder="KEY"
                        className="w-2/5 px-1.5 py-0.5 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)]"
                      />
                      <input
                        value={pair.value}
                        onChange={(e) => {
                          const next = [...entry.envPairs];
                          next[pIdx] = { ...pair, value: e.target.value };
                          updateEntry(idx, { envPairs: next });
                        }}
                        placeholder="VALUE"
                        className="flex-1 px-1.5 py-0.5 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)]"
                      />
                      <button
                        type="button"
                        onClick={() =>
                          updateEntry(idx, {
                            envPairs: entry.envPairs.filter((_, i) => i !== pIdx),
                          })
                        }
                        className="px-1 text-[13px] text-[var(--text-muted)] hover:text-[var(--danger)]"
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>

                <div className="flex flex-wrap items-center gap-3 pt-1">
                  <label className="flex items-center gap-1.5 text-[13px] text-[var(--text-secondary)] cursor-pointer">
                    <input
                      type="checkbox"
                      checked={entry.hideConsole}
                      onChange={(e) => updateEntry(idx, { hideConsole: e.target.checked })}
                      className="accent-[var(--accent)]"
                    />
                    隐藏黑窗
                  </label>
                  <label className="flex items-center gap-1.5 text-[13px] text-[var(--text-secondary)] cursor-pointer">
                    <input
                      type="checkbox"
                      checked={entry.runAsAdmin}
                      onChange={(e) => updateEntry(idx, { runAsAdmin: e.target.checked })}
                      className="accent-[var(--accent)]"
                    />
                    默认管理员
                  </label>
                </div>
              </div>
            ))}

            <button
              type="button"
              disabled={saving}
              onClick={handleSaveEntries}
              className="w-full inline-flex items-center justify-center gap-1.5 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-50"
            >
              <Save className="w-3.5 h-3.5" />
              保存入口配置
            </button>
          </div>
        )}

        {tab === "notes" && (
          <div className="space-y-3 h-full flex flex-col">
            <div className="flex items-center justify-between">
              <span className="text-[13px] text-[var(--text-muted)]">
                支持 Markdown（记录参数用法、注意事项）
              </span>
              <button
                type="button"
                onClick={() => setNotesPreview((v) => !v)}
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-[13px] border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              >
                {notesPreview ? (
                  <>
                    <Edit3 className="w-3 h-3" /> 编辑
                  </>
                ) : (
                  <>
                    <Eye className="w-3 h-3" /> 预览
                  </>
                )}
              </button>
            </div>

            {notesPreview ? (
              <div className="flex-1 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3">
                <SafeMarkdownView content={notesMd} />
              </div>
            ) : (
              <textarea
                value={notesMd}
                onChange={(e) => setNotesMd(e.target.value)}
                rows={13}
                placeholder={"# 使用说明\n- 常用参数: `--port 8080`\n- 配置文件位置: `config.json`"}
                className="flex-1 w-full p-2.5 rounded-lg text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)] resize-none"
              />
            )}

            <button
              type="button"
              disabled={saving}
              onClick={handleSaveBasicOrNotes}
              className="w-full inline-flex items-center justify-center gap-1.5 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-50"
            >
              <Save className="w-3.5 h-3.5" />
              保存笔记
            </button>
          </div>
        )}

        {tab === "logs" && (
          <div className="space-y-2.5 h-full flex flex-col">
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 min-w-0">
                <span
                  className={`px-2 py-0.5 rounded text-xs font-medium border ${
                    logSnap?.status === "building"
                      ? "bg-[var(--accent-soft)] border-[var(--accent)]/30 text-[var(--accent)]"
                      : logSnap?.status === "running"
                      ? "bg-emerald-500/15 border-emerald-500/30 text-emerald-400"
                      : logSnap?.status === "failed"
                      ? "bg-red-500/15 border-red-500/30 text-[var(--danger)]"
                      : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)]"
                  }`}
                >
                  {logSnap?.status === "building"
                    ? "构建中"
                    : logSnap?.status === "running"
                    ? "运行中"
                    : logSnap?.status === "failed"
                    ? "失败"
                    : logSnap?.status === "exited"
                    ? "已退出"
                    : "空闲"}
                </span>
                {logSnap?.lastCommand && (
                  <span
                    className="text-xs font-mono text-[var(--text-muted)] truncate"
                    title={logSnap.lastCommand}
                  >
                    {logSnap.lastCommand}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <button
                  type="button"
                  title="复制日志"
                  onClick={async () => {
                    const text = (logSnap?.lines ?? []).join("\n");
                    if (!text) return;
                    await Clipboard.SetText(text).catch(() => navigator.clipboard?.writeText(text));
                    pushToast("info", "已复制运行日志");
                  }}
                  className="p-1.5 rounded border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                >
                  <Copy className="w-3.5 h-3.5" />
                </button>
                <button
                  type="button"
                  title="清空日志"
                  onClick={() =>
                    AppService.ClearAppLogs(app.id)
                      .then(() =>
                        setLogSnap({
                          appId: app.id,
                          status: isBuilding ? "building" : isRunning ? "running" : "idle",
                          lastCommand: "",
                          updatedAt: Date.now(),
                          lines: [],
                          lastError: "",
                        })
                      )
                      .catch((e) => pushToast("error", String(e)))
                  }
                  className="p-1.5 rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--danger)] hover:bg-[var(--bg-hover)]"
                >
                  <Eraser className="w-3.5 h-3.5" />
                </button>
              </div>
            </div>

            {logSnap?.lastError && (
              <div className="p-2.5 rounded-md border border-red-500/30 bg-red-500/10 text-xs font-mono text-[var(--danger)] whitespace-pre-wrap break-all">
                {logSnap.lastError}
              </div>
            )}

            <div className="flex-1 min-h-[240px] overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--bg-app)] p-2.5 font-mono text-xs text-[var(--text-secondary)] leading-relaxed">
              {(logSnap?.lines ?? []).length === 0 ? (
                <div className="h-full flex items-center justify-center text-[var(--text-muted)] text-center px-4">
                  暂无日志。当入口开启「隐藏控制台黑窗」或执行前置构建时，输出会自动捕获于此。
                </div>
              ) : (
                <pre className="whitespace-pre-wrap break-all">
                  {(logSnap?.lines ?? []).join("\n")}
                  <div ref={logEndRef} />
                </pre>
              )}
            </div>
          </div>
        )}
      </div>
    </aside>
  );
}

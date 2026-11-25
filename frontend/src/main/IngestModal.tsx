import { useEffect, useState } from "react";
import { Archive, Folder, FileCode, Link2, Loader2, X, AlertTriangle, Check, Terminal } from "lucide-react";
import {
  EntryType,
  IngestAction,
  type EntryInput,
  type IngestAnalysis,
  type IngestCommitItem,
} from "../../bindings/oneapp/internal/domain/models.js";
import { formatBytes, splitCommaInput } from "../shared/format.js";
import { AppService, useAppStore } from "../shared/store.js";

interface DraftItem {
  analysis: IngestAnalysis;
  included: boolean;
  name: string;
  action: IngestAction;
  primaryIdx: number;
  extraIndices: number[];
  customCommand: string;
  customPreBuildCommand: string;
  customPreBuildCond: string;
  customReadyCheckPath: string;
  categoryId: string;
  tagsText: string;
  aliasesText: string;
  runAsAdmin: boolean;
  hideConsole: boolean;
}

const KIND_LABELS: Record<string, string> = {
  file: "单文件",
  dir: "文件夹",
  archive: "压缩包",
  shortcut: "快捷方式",
  project: "源码项目",
};

function getItemBadge(a: IngestAnalysis): string {
  if (a.sourceKind === "project") {
    if (a.projectKind === "electron") return "Electron 项目";
    if (a.projectKind === "node") return "Node 项目";
    return "源码项目";
  }
  return KIND_LABELS[a.sourceKind] ?? "项目";
}

export function IngestModal() {
  const pendingIngests = useAppStore((s) => s.pendingIngests);
  const setPendingIngests = useAppStore((s) => s.setPendingIngests);
  const categories = useAppStore((s) => s.categories);
  const settings = useAppStore((s) => s.settings);
  const saveSettings = useAppStore((s) => s.saveSettings);
  const selectApp = useAppStore((s) => s.selectApp);
  const pushToast = useAppStore((s) => s.pushToast);

  const [drafts, setDrafts] = useState<DraftItem[]>([]);
  const [rememberMode, setRememberMode] = useState(false);
  const [committing, setCommitting] = useState(false);

  useEffect(() => {
    if (pendingIngests.length === 0) {
      setDrafts([]);
      return;
    }
    const defaultCat = categories[0]?.id ?? "";
    setDrafts(
      pendingIngests.map((a) => {
        const candidates = a.candidates ?? [];
        let primIdx = candidates.findIndex((c) => c.isPrimary);
        if (primIdx < 0) primIdx = 0;
        const primary = candidates[primIdx];

        const supportsCustomDir = a.sourceKind === "dir" || a.sourceKind === "project";
        const hasError = Boolean(a.error) || (candidates.length === 0 && !supportsCustomDir);

        const extraIndices = candidates
          .map((c, idx) => (idx !== primIdx && c.defaultSelected ? idx : -1))
          .filter((idx) => idx >= 0);

        const primaryType = primary?.entryType;
        const defaultHideConsole = primary
          ? Boolean(
              primary.hideConsole ||
                primaryType === "bat" ||
                primaryType === "cmd" ||
                primaryType === "ps1" ||
                primaryType === "command"
            )
          : true;

        const defaultAction =
          a.suggestedAction ||
          (a.canCopy || a.sourceKind === "archive" ? IngestAction.IngestCopy : IngestAction.IngestLink);

        return {
          analysis: a,
          included: !hasError,
          name: a.suggestedName || "未命名工具",
          action: defaultAction,
          primaryIdx: primIdx,
          extraIndices,
          customCommand: "",
          customPreBuildCommand: "",
          customPreBuildCond: "",
          customReadyCheckPath: "",
          categoryId: defaultCat,
          tagsText: a.projectKind === "electron" ? "Electron" : "",
          aliasesText: "",
          runAsAdmin: false,
          hideConsole: defaultHideConsole,
        };
      })
    );
  }, [pendingIngests, categories]);

  if (pendingIngests.length === 0) {
    return null;
  }

  const updateDraft = (idx: number, patch: Partial<DraftItem>) => {
    setDrafts((prev) => prev.map((d, i) => (i === idx ? { ...d, ...patch } : d)));
  };

  const toggleExtraIndex = (idx: number, candIdx: number) => {
    setDrafts((prev) =>
      prev.map((d, i) => {
        if (i !== idx) return d;
        const exists = d.extraIndices.includes(candIdx);
        return {
          ...d,
          extraIndices: exists
            ? d.extraIndices.filter((x) => x !== candIdx)
            : [...d.extraIndices, candIdx],
        };
      })
    );
  };

  const isDraftValid = (d: DraftItem) => {
    if (!d.included || d.analysis.error) return false;
    const cands = d.analysis.candidates ?? [];
    if (cands.length > 0) return Boolean(cands[d.primaryIdx]?.relPath);
    return (d.analysis.sourceKind === "dir" || d.analysis.sourceKind === "project") && d.customCommand.trim().length > 0;
  };

  const activeDrafts = drafts.filter(isDraftValid);

  const handleCommit = async () => {
    if (activeDrafts.length === 0 || committing) return;
    setCommitting(true);
    try {
      const items: IngestCommitItem[] = activeDrafts.map((d) => {
        const cands = d.analysis.candidates ?? [];
        if (cands.length === 0) {
          const customCmd = d.customCommand.trim();
          const preCmd = d.customPreBuildCommand.trim();
          const customEntry: EntryInput = {
            id: "",
            name: "启动命令",
            entryType: EntryType.EntryCommand,
            relPath: customCmd,
            argsTemplate: "",
            workingDir: "{{APP_DIR}}",
            envVars: {},
            runAsAdmin: d.runAsAdmin,
            hideConsole: d.hideConsole,
            promptArgs: false,
            isPrimary: true,
            preLaunchCommand: preCmd,
            preLaunchCondition: preCmd ? d.customPreBuildCond || "if_missing" : "",
            readyCheckPath: d.customReadyCheckPath.trim(),
          };
          return {
            sourcePath: d.analysis.sourcePath,
            name: d.name.trim() || d.analysis.suggestedName,
            version: d.analysis.version ?? "",
            description: d.analysis.description ?? "",
            action: d.action,
            categoryId: d.categoryId,
            tags: splitCommaInput(d.tagsText),
            aliases: splitCommaInput(d.aliasesText),
            primaryPath: "",
            extraPaths: [],
            entries: [customEntry],
            iconSourcePath: d.analysis.iconSourcePath ?? "",
            argsTemplate: "",
            workingDir: "{{APP_DIR}}",
            runAsAdmin: d.runAsAdmin,
            hideConsole: d.hideConsole,
          };
        }

        const primary = cands[d.primaryIdx] ?? cands[0];
        const ordered = [
          primary,
          ...d.extraIndices
            .filter((ci) => ci !== d.primaryIdx && ci >= 0 && ci < cands.length)
            .map((ci) => cands[ci]),
        ];

        const entries: EntryInput[] = ordered.map((c, idx) => ({
          id: "",
          name: c.name || (idx === 0 ? "主程序" : c.relPath),
          entryType: c.entryType,
          relPath: c.relPath,
          argsTemplate: c.argsTemplate ?? "",
          workingDir: c.workingDir ?? "",
          envVars: {},
          runAsAdmin: idx === 0 ? d.runAsAdmin : false,
          hideConsole:
            idx === 0
              ? d.hideConsole
              : Boolean(
                  c.hideConsole ||
                    c.entryType === EntryType.EntryCommand ||
                    c.entryType === EntryType.EntryBat ||
                    c.entryType === EntryType.EntryCmd ||
                    c.entryType === EntryType.EntryPs1
                ),
          promptArgs: false,
          isPrimary: idx === 0,
          preLaunchCommand: c.preLaunchCommand ?? "",
          preLaunchCondition: c.preLaunchCondition ?? "",
          readyCheckPath: c.readyCheckPath ?? "",
        }));

        return {
          sourcePath: d.analysis.sourcePath,
          name: d.name.trim() || d.analysis.suggestedName,
          version: d.analysis.version ?? "",
          description: d.analysis.description ?? "",
          action: d.action,
          categoryId: d.categoryId,
          tags: splitCommaInput(d.tagsText),
          aliases: splitCommaInput(d.aliasesText),
          primaryPath: primary.relPath,
          extraPaths: [],
          entries,
          iconSourcePath: d.analysis.iconSourcePath ?? "",
          argsTemplate: primary.argsTemplate ?? "",
          workingDir: primary.workingDir ?? "",
          runAsAdmin: d.runAsAdmin,
          hideConsole: d.hideConsole,
        };
      });

      const res = await AppService.CommitIngest(items);
      const okApps = res.apps ?? [];
      const errs = res.errors ?? [];

      if (rememberMode && settings && activeDrafts[0]?.analysis.canMove) {
        await saveSettings({
          ...settings,
          defaultIngestMode: activeDrafts[0].action,
        }).catch(() => undefined);
      }

      if (okApps.length > 0) {
        selectApp(okApps[0].id);
        pushToast("success", `成功入库 ${okApps.length} 个项目`);
      }
      if (errs.length > 0) {
        pushToast("error", errs[0]);
      }
      setPendingIngests([]);
    } catch (err) {
      pushToast("error", `入库失败: ${String(err)}`);
    } finally {
      setCommitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-sm p-4">
      <div className="w-full max-w-3xl max-h-[86vh] flex flex-col rounded-xl bg-[var(--bg-surface)] border border-[var(--border-strong)] shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border)]">
          <div>
            <h2 className="text-base font-semibold text-[var(--text-primary)]">确认入库软件 / 项目</h2>
            <p className="text-sm text-[var(--text-muted)] mt-0.5">
              识别到 {drafts.length} 个项目，可确认启动入口、子入口与托管方式
            </p>
          </div>
          <button
            onClick={() => !committing && setPendingIngests([])}
            className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
            title="取消"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {drafts.map((d, idx) => {
            const a = d.analysis;
            const candidates = a.candidates ?? [];
            const supportsCustomDir = a.sourceKind === "dir" || a.sourceKind === "project";
            const hasErr = Boolean(a.error) || (candidates.length === 0 && !supportsCustomDir);
            const allowCopy = a.canCopy || a.sourceKind === "archive";
            const primaryCand = candidates[d.primaryIdx];

            const Icon =
              a.sourceKind === "archive"
                ? Archive
                : a.sourceKind === "project"
                ? Terminal
                : a.sourceKind === "dir"
                ? Folder
                : a.sourceKind === "shortcut"
                ? Link2
                : FileCode;

            return (
              <div
                key={`${a.sourcePath}-${idx}`}
                className={`rounded-lg border p-3.5 transition-colors ${
                  d.included
                    ? "border-[var(--border-strong)] bg-[var(--bg-elevated)]"
                    : "border-[var(--border)] bg-[var(--bg-surface)] opacity-60"
                }`}
              >
                <div className="flex items-start gap-3">
                  <div className="mt-0.5 p-2 rounded-lg bg-[var(--accent-soft)] text-[var(--accent)]">
                    <Icon className="w-4 h-4" />
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <span className="text-[13px] px-1.5 py-0.5 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-secondary)]">
                          {getItemBadge(a)}
                        </span>
                        {a.version && (
                          <span className="text-[13px] font-mono text-[var(--text-muted)]">
                            v{a.version}
                          </span>
                        )}
                        {a.sizeBytes > 0 && (
                          <span className="text-[13px] text-[var(--text-muted)]">
                            {formatBytes(a.sizeBytes)}
                          </span>
                        )}
                      </div>
                      {!hasErr && (
                        <label className="flex items-center gap-1.5 text-sm text-[var(--text-secondary)] cursor-pointer">
                          <input
                            type="checkbox"
                            checked={d.included}
                            onChange={(e) => updateDraft(idx, { included: e.target.checked })}
                            className="accent-[var(--accent)]"
                          />
                          入库
                        </label>
                      )}
                    </div>

                    <div
                      className="text-[13px] font-mono text-[var(--text-muted)] truncate mt-1"
                      title={a.sourcePath}
                    >
                      {a.sourcePath}
                    </div>

                    {a.projectWarning && (
                      <div className="mt-2 flex items-center gap-2 text-[13px] text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded-md px-2.5 py-1.5">
                        <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
                        <span>{a.projectWarning}</span>
                      </div>
                    )}

                    {hasErr ? (
                      <div className="mt-2.5 flex items-center gap-2 text-sm text-[var(--danger)] bg-red-500/10 border border-red-500/20 rounded-md px-2.5 py-1.5">
                        <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
                        <span>{a.error || "未在目录或压缩包内识别到可执行文件或脚本"}</span>
                      </div>
                    ) : (
                      d.included && (
                        <div className="mt-3 space-y-2.5">
                          <div className="grid grid-cols-2 gap-2.5">
                            <div>
                              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                显示名称
                              </label>
                              <input
                                value={d.name}
                                onChange={(e) => updateDraft(idx, { name: e.target.value })}
                                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                              />
                            </div>
                            <div>
                              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                分类
                              </label>
                              <select
                                value={d.categoryId}
                                onChange={(e) => updateDraft(idx, { categoryId: e.target.value })}
                                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                              >
                                <option value="">未分类</option>
                                {categories.map((c) => (
                                  <option key={c.id} value={c.id}>
                                    {c.name}
                                  </option>
                                ))}
                              </select>
                            </div>
                          </div>

                          <div className="grid grid-cols-2 gap-2.5">
                            {candidates.length > 0 ? (
                              <div>
                                <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                  主启动入口
                                </label>
                                <select
                                  value={d.primaryIdx}
                                  onChange={(e) => {
                                    const nextIdx = Number(e.target.value);
                                    updateDraft(idx, {
                                      primaryIdx: nextIdx,
                                      extraIndices: d.extraIndices.filter((x) => x !== nextIdx),
                                    });
                                  }}
                                  className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                                >
                                  {candidates.map((c, cIdx) => (
                                    <option key={cIdx} value={cIdx}>
                                      {c.name && c.name !== "主程序"
                                        ? `${c.name} (${c.relPath})`
                                        : `${c.relPath} (${c.entryType})`}
                                    </option>
                                  ))}
                                </select>
                                {primaryCand?.note && (
                                  <div className="text-xs text-[var(--text-muted)] mt-1">
                                    {primaryCand.note}
                                  </div>
                                )}
                              </div>
                            ) : (
                              <div>
                                <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                  自定义启动命令
                                </label>
                                <input
                                  value={d.customCommand}
                                  onChange={(e) => updateDraft(idx, { customCommand: e.target.value })}
                                  placeholder="例如 python main.py 或 go run ."
                                  className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
                                />
                              </div>
                            )}

                            <div>
                              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                存储方式
                              </label>
                              <div className="grid grid-cols-3 gap-1 bg-[var(--bg-surface)] p-0.5 rounded-md border border-[var(--border)]">
                                <button
                                  type="button"
                                  disabled={!allowCopy}
                                  onClick={() => updateDraft(idx, { action: IngestAction.IngestCopy })}
                                  className={`py-1 text-[13px] rounded transition-colors disabled:opacity-35 ${
                                    d.action === IngestAction.IngestCopy
                                      ? "bg-[var(--accent)] text-white font-medium"
                                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                                  }`}
                                >
                                  {a.sourceKind === "archive" ? "解压入库" : "复制入库"}
                                </button>
                                <button
                                  type="button"
                                  disabled={!a.canMove}
                                  onClick={() => updateDraft(idx, { action: IngestAction.IngestMove })}
                                  className={`py-1 text-[13px] rounded transition-colors disabled:opacity-35 ${
                                    d.action === IngestAction.IngestMove
                                      ? "bg-[var(--accent)] text-white font-medium"
                                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                                  }`}
                                >
                                  移动入库
                                </button>
                                <button
                                  type="button"
                                  disabled={!a.canLink}
                                  onClick={() => updateDraft(idx, { action: IngestAction.IngestLink })}
                                  className={`py-1 text-[13px] rounded transition-colors disabled:opacity-35 ${
                                    d.action === IngestAction.IngestLink
                                      ? "bg-[var(--accent)] text-white font-medium"
                                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                                  }`}
                                >
                                  仅链接
                                </button>
                              </div>
                              {!allowCopy && !a.canMove && a.canLink && (
                                <div className="text-xs text-[var(--text-muted)] mt-1">
                                  源码项目目录已锁定「仅链接原路径」
                                </div>
                              )}
                            </div>
                          </div>

                          {candidates.length === 0 && supportsCustomDir && (
                            <div className="grid grid-cols-3 gap-2 pt-0.5">
                              <div>
                                <label className="block text-xs text-[var(--text-muted)] mb-1">
                                  前置构建命令（可选）
                                </label>
                                <input
                                  value={d.customPreBuildCommand}
                                  onChange={(e) =>
                                    updateDraft(idx, {
                                      customPreBuildCommand: e.target.value,
                                      customPreBuildCond:
                                        e.target.value.trim() && !d.customPreBuildCond
                                          ? "if_missing"
                                          : d.customPreBuildCond,
                                    })
                                  }
                                  placeholder="如 pnpm run build"
                                  className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                                />
                              </div>
                              <div>
                                <label className="block text-xs text-[var(--text-muted)] mb-1">
                                  构建时机
                                </label>
                                <select
                                  value={d.customPreBuildCond}
                                  onChange={(e) =>
                                    updateDraft(idx, { customPreBuildCond: e.target.value })
                                  }
                                  className="w-full px-2 py-1 rounded text-[13px] bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                                >
                                  <option value="">不启用</option>
                                  <option value="if_missing">产物缺失时构建</option>
                                  <option value="always">每次启动前构建</option>
                                </select>
                              </div>
                              <div>
                                <label className="block text-xs text-[var(--text-muted)] mb-1">
                                  产物检测路径（可选）
                                </label>
                                <input
                                  value={d.customReadyCheckPath}
                                  onChange={(e) =>
                                    updateDraft(idx, { customReadyCheckPath: e.target.value })
                                  }
                                  placeholder="如 out/main/index.js"
                                  className="w-full px-2 py-1 rounded text-[13px] font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                                />
                              </div>
                            </div>
                          )}

                          {candidates.length > 1 && (
                            <div>
                              <label className="block text-[13px] text-[var(--text-muted)] mb-1">
                                附加子入口（可选多选）
                              </label>
                              <div className="flex flex-wrap gap-1.5 max-h-24 overflow-y-auto">
                                {candidates.map((c, cIdx) => {
                                  if (cIdx === d.primaryIdx) return null;
                                  const checked = d.extraIndices.includes(cIdx);
                                  const chipLabel =
                                    c.name && c.name !== c.relPath ? c.name : c.relPath;
                                  return (
                                    <button
                                      key={cIdx}
                                      type="button"
                                      title={c.note ? `${c.note} (${c.relPath})` : c.relPath}
                                      onClick={() => toggleExtraIndex(idx, cIdx)}
                                      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[13px] border transition-colors ${
                                        checked
                                          ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]"
                                          : "bg-[var(--bg-surface)] border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                                      }`}
                                    >
                                      {checked && <Check className="w-3 h-3" />}
                                      <span>{chipLabel}</span>
                                    </button>
                                  );
                                })}
                              </div>
                            </div>
                          )}

                          <div className="grid grid-cols-2 gap-2.5">
                            <input
                              value={d.aliasesText}
                              onChange={(e) => updateDraft(idx, { aliasesText: e.target.value })}
                              placeholder="别名，逗号分隔 (如 jt, snip)"
                              className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
                            />
                            <input
                              value={d.tagsText}
                              onChange={(e) => updateDraft(idx, { tagsText: e.target.value })}
                              placeholder="标签，逗号分隔 (如 自研, 工具)"
                              className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)]"
                            />
                          </div>

                          <div className="flex items-center gap-4 pt-0.5">
                            <label className="flex items-center gap-1.5 text-sm text-[var(--text-secondary)] cursor-pointer">
                              <input
                                type="checkbox"
                                checked={d.hideConsole}
                                onChange={(e) => updateDraft(idx, { hideConsole: e.target.checked })}
                                className="accent-[var(--accent)]"
                              />
                              隐藏控制台黑窗
                            </label>
                            <label className="flex items-center gap-1.5 text-sm text-[var(--text-secondary)] cursor-pointer">
                              <input
                                type="checkbox"
                                checked={d.runAsAdmin}
                                onChange={(e) => updateDraft(idx, { runAsAdmin: e.target.checked })}
                                className="accent-[var(--accent)]"
                              />
                              默认管理员运行
                            </label>
                          </div>
                        </div>
                      )
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <div className="flex items-center justify-between px-5 py-3 border-t border-[var(--border)] bg-[var(--bg-elevated)]">
          <label className="flex items-center gap-2 text-sm text-[var(--text-secondary)] cursor-pointer">
            <input
              type="checkbox"
              checked={rememberMode}
              onChange={(e) => setRememberMode(e.target.checked)}
              className="accent-[var(--accent)]"
            />
            将本次存储方式设为默认
          </label>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={committing}
              onClick={() => setPendingIngests([])}
              className="px-3.5 py-1.5 rounded-md text-sm border border-[var(--border-strong)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
            >
              取消
            </button>
            <button
              type="button"
              disabled={committing || activeDrafts.length === 0}
              onClick={handleCommit}
              className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-45"
            >
              {committing && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
              确认入库 ({activeDrafts.length})
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

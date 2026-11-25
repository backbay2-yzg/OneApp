import { useEffect, useState } from "react";
import { FolderSearch, Loader2, Sparkles, Terminal, X } from "lucide-react";
import type { IngestAnalysis } from "../../bindings/oneapp/internal/domain/models.js";
import { splitCommaInput } from "../shared/format.js";
import { AppService, useAppStore } from "../shared/store.js";

export function CreateCommandModal() {
  const open = useAppStore((s) => s.commandModalOpen);
  const setOpen = useAppStore((s) => s.setCommandModalOpen);
  const setPendingIngests = useAppStore((s) => s.setPendingIngests);
  const categories = useAppStore((s) => s.categories);
  const selectApp = useAppStore((s) => s.selectApp);
  const pushToast = useAppStore((s) => s.pushToast);

  const [rootPath, setRootPath] = useState("");
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [argsTemplate, setArgsTemplate] = useState("");
  const [workingDir, setWorkingDir] = useState("");
  const [preLaunchCondition, setPreLaunchCondition] = useState("");
  const [preLaunchCommand, setPreLaunchCommand] = useState("");
  const [readyCheckPath, setReadyCheckPath] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [aliasesText, setAliasesText] = useState("");
  const [tagsText, setTagsText] = useState("");
  const [description, setDescription] = useState("");
  const [hideConsole, setHideConsole] = useState(true);
  const [runAsAdmin, setRunAsAdmin] = useState(false);

  const [detectedProject, setDetectedProject] = useState<IngestAnalysis | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setRootPath("");
      setName("");
      setCommand("");
      setArgsTemplate("");
      setWorkingDir("");
      setPreLaunchCondition("");
      setPreLaunchCommand("");
      setReadyCheckPath("");
      setCategoryId(categories[0]?.id ?? "");
      setAliasesText("");
      setTagsText("");
      setDescription("");
      setHideConsole(true);
      setRunAsAdmin(false);
      setDetectedProject(null);
    }
  }, [open, categories]);

  const inspectProjectDir = async (dir: string) => {
    if (!dir.trim()) {
      setDetectedProject(null);
      return;
    }
    try {
      const analyses = await AppService.AnalyzePaths([dir.trim()]);
      const first = analyses?.[0];
      if (first && !first.error && (first.projectKind === "electron" || first.projectKind === "node")) {
        setDetectedProject(first);
        if (!name.trim() && first.suggestedName) {
          setName(first.suggestedName);
        }
      } else {
        setDetectedProject(null);
        if (!name.trim()) {
          const base = dir.trim().replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? "";
          if (base) setName(base);
        }
      }
    } catch {
      setDetectedProject(null);
    }
  };

  if (!open) return null;

  const handleBrowseDir = async () => {
    try {
      const picked = await AppService.PickDirectoryPath();
      if (!picked) return;
      setRootPath(picked);
      await inspectProjectDir(picked);
    } catch (err) {
      pushToast("error", String(err));
    }
  };

  const handleImportDetectedProject = () => {
    if (!detectedProject) return;
    setOpen(false);
    setPendingIngests([detectedProject]);
  };

  const handleCreate = async () => {
    if (!rootPath.trim()) {
      pushToast("error", "请先选择项目或工作目录");
      return;
    }
    if (!command.trim()) {
      pushToast("error", "请填写启动命令");
      return;
    }
    if (saving) return;
    setSaving(true);
    try {
      const created = await AppService.CreateCommandApp({
        name: name.trim(),
        rootPath: rootPath.trim(),
        command: command.trim(),
        argsTemplate: argsTemplate.trim(),
        workingDir: workingDir.trim(),
        preLaunchCommand: preLaunchCondition ? preLaunchCommand.trim() : "",
        preLaunchCondition,
        readyCheckPath: preLaunchCondition === "if_missing" ? readyCheckPath.trim() : "",
        categoryId,
        tags: splitCommaInput(tagsText),
        aliases: splitCommaInput(aliasesText),
        description: description.trim(),
        hideConsole,
        runAsAdmin,
      });
      if (created) {
        selectApp(created.id);
      }
      pushToast("success", `已添加项目「${created?.name ?? (name.trim() || "项目")}」`);
      setOpen(false);
    } catch (err) {
      pushToast("error", `创建失败: ${String(err)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-sm p-4">
      <div className="w-full max-w-2xl max-h-[88vh] flex flex-col rounded-xl bg-[var(--bg-surface)] border border-[var(--border-strong)] shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border)]">
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-[var(--accent-soft)] text-[var(--accent)]">
              <Terminal className="w-4 h-4" />
            </div>
            <div>
              <h2 className="text-base font-semibold text-[var(--text-primary)]">添加项目 / 启动命令</h2>
              <p className="text-xs text-[var(--text-muted)]">
                以「仅链接原路径」方式纳管 Electron / Node / Python / Go / Rust 等开发项目
              </p>
            </div>
          </div>
          <button
            type="button"
            disabled={saving}
            onClick={() => setOpen(false)}
            className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-5 space-y-3.5">
          <div>
            <label className="block text-[13px] text-[var(--text-muted)] mb-1">
              项目目录（仅链接原路径）
            </label>
            <div className="flex gap-2">
              <input
                value={rootPath}
                onChange={(e) => setRootPath(e.target.value)}
                onBlur={() => inspectProjectDir(rootPath)}
                placeholder="例如 D:\小说写作台\app"
                className="flex-1 px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
              />
              <button
                type="button"
                onClick={handleBrowseDir}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
              >
                <FolderSearch className="w-4 h-4" />
                浏览目录
              </button>
            </div>
          </div>

          {detectedProject && (
            <div className="rounded-lg border border-[var(--accent)]/40 bg-[var(--accent-soft)] px-3.5 py-2.5 flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 min-w-0">
                <Sparkles className="w-4 h-4 text-[var(--accent)] shrink-0" />
                <div className="text-sm text-[var(--text-primary)] truncate">
                  检测到{" "}
                  <span className="font-semibold">
                    {detectedProject.projectKind === "electron" ? "Electron 项目" : "Node 项目"}
                  </span>
                  （{detectedProject.suggestedName}），可直接自动生成启动与构建入口
                </div>
              </div>
              <button
                type="button"
                onClick={handleImportDetectedProject}
                className="px-3 py-1 rounded-md text-xs font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white shrink-0"
              >
                一键解析导入
              </button>
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">显示名称</label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="留空则默认目录名"
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>
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
          </div>

          <div>
            <label className="block text-[13px] text-[var(--text-muted)] mb-1">启动命令</label>
            <input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="例如 pnpm run dev、python main.py 或 go run ."
              className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">附加参数（可选）</label>
              <input
                value={argsTemplate}
                onChange={(e) => setArgsTemplate(e.target.value)}
                placeholder="例如 --port 3000"
                className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
              />
            </div>
            <div>
              <label className="block text-[13px] text-[var(--text-muted)] mb-1">工作目录（默认项目根）</label>
              <input
                value={workingDir}
                onChange={(e) => setWorkingDir(e.target.value)}
                placeholder="{{APP_DIR}}"
                className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
              />
            </div>
          </div>

          <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3 space-y-2.5">
            <div className="text-[13px] font-medium text-[var(--text-secondary)]">
              前置构建规则（可选）
            </div>
            <div className="grid grid-cols-2 gap-2.5">
              <select
                value={preLaunchCondition}
                onChange={(e) => setPreLaunchCondition(e.target.value)}
                className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
              >
                <option value="">不启用前置构建</option>
                <option value="if_missing">产物缺失时自动构建</option>
                <option value="always">每次启动前构建</option>
              </select>
              {preLaunchCondition !== "" && (
                <input
                  value={preLaunchCommand}
                  onChange={(e) => setPreLaunchCommand(e.target.value)}
                  placeholder="构建命令，如 pnpm run build"
                  className="px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
                />
              )}
            </div>
            {preLaunchCondition === "if_missing" && (
              <input
                value={readyCheckPath}
                onChange={(e) => setReadyCheckPath(e.target.value)}
                placeholder="产物检测路径（相对项目根），如 out/main/index.js"
                className="w-full px-2.5 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text-primary)]"
              />
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <input
              value={aliasesText}
              onChange={(e) => setAliasesText(e.target.value)}
              placeholder="别名，逗号分隔 (如 nw, xzs)"
              className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
            />
            <input
              value={tagsText}
              onChange={(e) => setTagsText(e.target.value)}
              placeholder="标签，逗号分隔 (如 自研, Electron)"
              className="px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
            />
          </div>

          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="简短描述（可选）"
            className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)]"
          />

          <div className="flex items-center gap-5 pt-1">
            <label className="flex items-center gap-1.5 text-sm text-[var(--text-secondary)] cursor-pointer">
              <input
                type="checkbox"
                checked={hideConsole}
                onChange={(e) => setHideConsole(e.target.checked)}
                className="accent-[var(--accent)]"
              />
              隐藏控制台黑窗并捕获运行日志
            </label>
            <label className="flex items-center gap-1.5 text-sm text-[var(--text-secondary)] cursor-pointer">
              <input
                type="checkbox"
                checked={runAsAdmin}
                onChange={(e) => setRunAsAdmin(e.target.checked)}
                className="accent-[var(--accent)]"
              />
              默认管理员运行
            </label>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-[var(--border)] bg-[var(--bg-elevated)]">
          <button
            type="button"
            disabled={saving}
            onClick={() => setOpen(false)}
            className="px-3.5 py-1.5 rounded-md text-sm border border-[var(--border-strong)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
          >
            取消
          </button>
          <button
            type="button"
            disabled={saving || !rootPath.trim() || !command.trim()}
            onClick={handleCreate}
            className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-45"
          >
            {saving && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
            创建并入库
          </button>
        </div>
      </div>
    </div>
  );
}

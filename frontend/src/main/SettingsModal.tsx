import { useEffect, useState } from "react";
import { AlertCircle, CheckCircle2, Keyboard, X } from "lucide-react";
import type { Settings } from "../../bindings/oneapp/internal/config/models.js";
import { useAppStore } from "../shared/store.js";

const HOTKEY_PRESETS = ["Alt+Space", "Alt+Q", "Ctrl+Alt+Space", "Ctrl+Shift+Space"];

export function SettingsModal() {
  const open = useAppStore((s) => s.settingsOpen);
  const setOpen = useAppStore((s) => s.setSettingsOpen);
  const settings = useAppStore((s) => s.settings);
  const saveSettings = useAppStore((s) => s.saveSettings);
  const hotkeyStatus = useAppStore((s) => s.hotkeyStatus);
  const portableRoot = useAppStore((s) => s.portableRoot);
  const libraryRoot = useAppStore((s) => s.libraryRoot);

  const [form, setForm] = useState<Settings | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open && settings) {
      setForm({ ...settings });
    }
  }, [open, settings]);

  if (!open || !form) {
    return null;
  }

  const handleSave = async () => {
    if (saving) return;
    setSaving(true);
    try {
      await saveSettings(form);
      setOpen(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-sm p-4">
      <div className="w-full max-w-xl max-h-[86vh] flex flex-col rounded-xl bg-[var(--bg-surface)] border border-[var(--border-strong)] shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border)]">
          <h2 className="text-base font-semibold text-[var(--text-primary)]">设置与全局热键</h2>
          <button
            onClick={() => setOpen(false)}
            className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-5 space-y-5">
          {/* Hotkey section */}
          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="flex items-center gap-1.5 text-sm font-medium text-[var(--text-primary)]">
                <Keyboard className="w-3.5 h-3.5 text-[var(--accent)]" />
                极速启动条全局快捷键
              </label>
              {hotkeyStatus.registered ? (
                <span className="inline-flex items-center gap-1 text-[13px] text-[var(--success)]">
                  <CheckCircle2 className="w-3 h-3" /> 已绑定 {hotkeyStatus.accelerator}
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 text-[13px] text-[var(--danger)]">
                  <AlertCircle className="w-3 h-3" /> 未就绪 / 冲突
                </span>
              )}
            </div>
            <input
              value={form.launcherHotkey}
              onChange={(e) => setForm({ ...form, launcherHotkey: e.target.value })}
              placeholder="例如 Alt+Space 或 Ctrl+Shift+Space"
              className="w-full px-3 py-1.5 rounded-md text-sm font-mono bg-[var(--bg-elevated)] border border-[var(--border-strong)] text-[var(--text-primary)] focus:outline-none focus:border-[var(--accent)]"
            />
            <div className="flex flex-wrap gap-1.5">
              {HOTKEY_PRESETS.map((preset) => (
                <button
                  key={preset}
                  type="button"
                  onClick={() => setForm({ ...form, launcherHotkey: preset })}
                  className={`px-2 py-0.5 rounded text-[13px] font-mono border transition-colors ${
                    form.launcherHotkey === preset
                      ? "bg-[var(--accent-soft)] border-[var(--accent)] text-[var(--accent)]"
                      : "bg-[var(--bg-elevated)] border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                  }`}
                >
                  {preset}
                </button>
              ))}
            </div>
            {hotkeyStatus.error && (
              <p className="text-[13px] text-[var(--danger)] bg-red-500/10 border border-red-500/20 rounded px-2.5 py-1.5">
                {hotkeyStatus.error}
              </p>
            )}
          </section>

          {/* Ingest & Storage */}
          <section className="space-y-3 pt-3 border-t border-[var(--border)]">
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">
                  默认拖入存储方式
                </label>
                <select
                  value={form.defaultIngestMode}
                  onChange={(e) => setForm({ ...form, defaultIngestMode: e.target.value })}
                  className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border-strong)] text-[var(--text-primary)]"
                >
                  <option value="copy">复制入托管库</option>
                  <option value="move">移动入托管库</option>
                  <option value="link">仅链接原路径</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">
                  界面主题
                </label>
                <select
                  value={form.theme}
                  onChange={(e) => setForm({ ...form, theme: e.target.value })}
                  className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border-strong)] text-[var(--text-primary)]"
                >
                  <option value="system">跟随系统</option>
                  <option value="dark">深色模式</option>
                  <option value="light">浅色模式</option>
                </select>
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">
                托盘左键点击动作
              </label>
              <select
                value={form.trayClickAction}
                onChange={(e) => setForm({ ...form, trayClickAction: e.target.value })}
                className="w-full px-2.5 py-1.5 rounded-md text-sm bg-[var(--bg-elevated)] border border-[var(--border-strong)] text-[var(--text-primary)]"
              >
                <option value="popover">打开托盘快捷浮窗（推荐）</option>
                <option value="main">打开管理主窗口</option>
                <option value="launcher">呼出极速启动条</option>
              </select>
            </div>
          </section>

          {/* Switches */}
          <section className="space-y-2.5 pt-3 border-t border-[var(--border)]">
            <label className="flex items-center justify-between text-sm text-[var(--text-primary)] cursor-pointer">
              <span>拖入文件时静默入库（跳过确认弹窗）</span>
              <input
                type="checkbox"
                checked={form.silentIngest}
                onChange={(e) => setForm({ ...form, silentIngest: e.target.checked })}
                className="accent-[var(--accent)]"
              />
            </label>
            <label className="flex items-center justify-between text-sm text-[var(--text-primary)] cursor-pointer">
              <span>极速启动条显示右侧预览面板</span>
              <input
                type="checkbox"
                checked={form.showLauncherPreview}
                onChange={(e) => setForm({ ...form, showLauncherPreview: e.target.checked })}
                className="accent-[var(--accent)]"
              />
            </label>
            <label className="flex items-center justify-between text-sm text-[var(--text-primary)] cursor-pointer">
              <span>关闭主窗口时最小化到系统托盘</span>
              <input
                type="checkbox"
                checked={form.closeToTray}
                onChange={(e) => setForm({ ...form, closeToTray: e.target.checked })}
                className="accent-[var(--accent)]"
              />
            </label>
            <label className="flex items-center justify-between text-sm text-[var(--text-primary)] cursor-pointer">
              <span>Windows 开机自动启动（托盘静默驻留）</span>
              <input
                type="checkbox"
                checked={form.autoStart}
                onChange={(e) => setForm({ ...form, autoStart: e.target.checked })}
                className="accent-[var(--accent)]"
              />
            </label>
          </section>

          {/* Portable paths */}
          <section className="space-y-1.5 pt-3 border-t border-[var(--border)] text-[13px]">
            <div className="text-[var(--text-muted)]">便携根目录</div>
            <div className="font-mono px-2.5 py-1.5 rounded bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)] truncate">
              {portableRoot}
            </div>
            <div className="text-[var(--text-muted)] pt-1">托管软件库路径</div>
            <div className="font-mono px-2.5 py-1.5 rounded bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-secondary)] truncate">
              {libraryRoot}
            </div>
          </section>
        </div>

        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-[var(--border)] bg-[var(--bg-elevated)]">
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="px-3.5 py-1.5 rounded-md text-sm border border-[var(--border-strong)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
          >
            取消
          </button>
          <button
            type="button"
            disabled={saving}
            onClick={handleSave}
            className="px-4 py-1.5 rounded-md text-sm font-medium bg-[var(--accent)] hover:bg-[var(--accent-hover)] text-white disabled:opacity-50"
          >
            {saving ? "保存中..." : "保存设置"}
          </button>
        </div>
      </div>
    </div>
  );
}

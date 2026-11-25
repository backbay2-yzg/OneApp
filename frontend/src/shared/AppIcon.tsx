import { useState, useEffect } from "react";

interface AppIconProps {
  name: string;
  iconUrl?: string;
  size?: "sm" | "md" | "lg";
  isRunning?: boolean;
}

const PALETTES = [
  "from-indigo-500 to-blue-600",
  "from-emerald-500 to-teal-600",
  "from-amber-500 to-orange-600",
  "from-rose-500 to-pink-600",
  "from-violet-500 to-purple-600",
  "from-cyan-500 to-sky-600",
];

function pickPalette(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = (hash * 31 + name.charCodeAt(i)) | 0;
  }
  return PALETTES[Math.abs(hash) % PALETTES.length];
}

export function AppIcon({ name, iconUrl, size = "md", isRunning = false }: AppIconProps) {
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    setFailed(false);
  }, [iconUrl]);

  const dims =
    size === "sm"
      ? "w-8 h-8 rounded-md text-sm"
      : size === "lg"
      ? "w-12 h-12 rounded-xl text-xl"
      : "w-10 h-10 rounded-lg text-base";

  const letter = (name.trim().charAt(0) || "A").toUpperCase();
  const showImg = Boolean(iconUrl) && !failed;

  return (
    <div className={`relative shrink-0 ${dims}`}>
      {showImg ? (
        <img
          src={iconUrl}
          alt={name}
          onError={() => setFailed(true)}
          className="w-full h-full object-contain rounded-[inherit] bg-[var(--bg-elevated)] p-0.5 border border-[var(--border)]"
          draggable={false}
        />
      ) : (
        <div
          className={`w-full h-full rounded-[inherit] bg-gradient-to-br ${pickPalette(
            name
          )} text-white font-semibold flex items-center justify-center shadow-sm`}
        >
          {letter}
        </div>
      )}
      {isRunning && (
        <span
          title="正在运行"
          className="absolute -bottom-0.5 -right-0.5 w-2.5 h-2.5 rounded-full bg-emerald-400 ring-2 ring-[var(--bg-surface)]"
        />
      )}
    </div>
  );
}

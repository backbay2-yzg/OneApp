export type ThemeMode = "system" | "dark" | "light";

let mediaQueryCleanup: (() => void) | null = null;

export function applyTheme(mode: string): void {
  if (mediaQueryCleanup) {
    mediaQueryCleanup();
    mediaQueryCleanup = null;
  }

  const root = document.documentElement;
  const normalized: ThemeMode =
    mode === "light" || mode === "dark" || mode === "system" ? mode : "system";

  const setDark = (isDark: boolean) => {
    if (isDark) {
      root.classList.add("dark");
      root.dataset.theme = "dark";
    } else {
      root.classList.remove("dark");
      root.dataset.theme = "light";
    }
  };

  if (normalized === "dark") {
    setDark(true);
    return;
  }
  if (normalized === "light") {
    setDark(false);
    return;
  }

  const mql = window.matchMedia("(prefers-color-scheme: dark)");
  setDark(mql.matches);
  const handler = (ev: MediaQueryListEvent) => setDark(ev.matches);
  mql.addEventListener("change", handler);
  mediaQueryCleanup = () => mql.removeEventListener("change", handler);
}

export type Theme = "light" | "dark";

const KEY = "feixi-theme";

// 手动选过就以选的为准，没选过跟着系统。
export function preferredTheme(): Theme {
  const stored = localStorage.getItem(KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

// toggleTheme 一旦按下就把手动选择写进 localStorage，之后不再随系统变。
export function toggleTheme(): Theme {
  const next: Theme = preferredTheme() === "dark" ? "light" : "dark";
  localStorage.setItem(KEY, next);
  applyTheme(next);
  return next;
}

export function watchSystemTheme(onChange: (theme: Theme) => void): () => void {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const handler = () => {
    if (localStorage.getItem(KEY) === null) onChange(media.matches ? "dark" : "light");
  };
  media.addEventListener("change", handler);
  return () => media.removeEventListener("change", handler);
}

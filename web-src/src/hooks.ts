import { useEffect, useRef, useState } from "react";

export type ThemeMode = "dark" | "light";

/** 日间 / 夜间主题切换与持久化 */
export function useTheme(): [ThemeMode, () => void] {
  const [theme, setTheme] = useState<ThemeMode>(() => {
    const saved = localStorage.getItem("cdtd_theme");
    if (saved === "light" || saved === "dark") return saved;
    return "dark";
  });

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
      root.classList.remove("light");
      root.setAttribute("data-theme", "dark");
    } else {
      root.classList.add("light");
      root.classList.remove("dark");
      root.setAttribute("data-theme", "light");
    }
    localStorage.setItem("cdtd_theme", theme);
  }, [theme]);

  const toggle = () => setTheme((t) => (t === "dark" ? "light" : "dark"));
  return [theme, toggle];
}

/** 定时轮询（清理安全） */
export function useInterval(fn: () => void, ms: number) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => {
    const id = setInterval(() => ref.current(), ms);
    return () => clearInterval(id);
  }, [ms]);
}

/** 相对时间 */
export function relTime(iso: string): string {
  const d = Date.now() - new Date(iso).getTime();
  if (d < 60000) return "刚刚";
  if (d < 3600000) return Math.floor(d / 60000) + " 分钟前";
  if (d < 86400000) return Math.floor(d / 3600000) + " 小时前";
  return new Date(iso).toLocaleString("zh-CN", {
    timeZone: "Asia/Shanghai", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false,
  });
}

/** 换班组阶段文案映射 */
export const PHASE_TEXT: Record<string, string> = {
  settled: "已就绪",
  failover: "已转移",
  manual: "手动当班",
  switching: "切换中",
  pending: "待处理",
  breaker: "熔断",
  error: "异常",
};

/** HeroUI 语义色映射（Chip/徽章） */
export function statusTone(status?: string): "success" | "warning" | "danger" | "default" {
  if (status === "Running" || status === "settled" || status === "failover" || status === "manual") return "success";
  if (status === "Starting" || status === "Stopping" || status === "switching" || status === "pending") return "warning";
  if (status === "breaker" || status === "error" || status === "unknown") return "danger";
  return "default";
}

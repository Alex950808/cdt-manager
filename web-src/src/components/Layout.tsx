import type { ReactNode } from "react";
import { Button, Chip, Code, Header, Heading, Paragraph, Separator, Surface, Typography } from "@heroui/react";
import type { ThemeMode } from "../hooks";

export type View = "dashboard" | "servers" | "keepalive" | "shift" | "dns";
export const VIEW_TITLES: Record<View, string> = {
  dashboard: "概览", servers: "服务器", keepalive: "单机保活", shift: "联合换班", dns: "DNS",
};

const NAV: Array<{ key: View; label: string; icon: string; count?: number }> = [
  { key: "dashboard", label: "概览", icon: "▦" },
  { key: "servers", label: "服务器", icon: "▤", count: 0 },
  { key: "keepalive", label: "单机保活", icon: "♡", count: 0 },
  { key: "shift", label: "联合换班", icon: "⇄", count: 0 },
  { key: "dns", label: "DNS", icon: "◍", count: 0 },
];

/** 应用外壳：桌面侧边栏 + 移动端底部导航 */
export default function Layout(props: {
  view: View;
  onNavigate: (v: View) => void;
  counts: Partial<Record<View, number>>;
  actions?: ReactNode;
  onReconcile: () => void;
  onRefresh: () => void;
  onSettings: () => void;
  onLogout: () => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
  children: ReactNode;
}) {
  return (
    <Surface className="min-h-screen w-full max-w-full overflow-x-hidden relative z-10 flex bg-transparent p-0">
      {/* 桌面侧栏 */}
      <Surface className="hidden md:flex sticky top-0 h-screen w-56 shrink-0 flex-col gap-1 px-3 py-5 rounded-none
        border-r border-default-200 bg-surface/60 backdrop-blur-xl">
        <Surface className="flex items-center gap-2.5 px-2 mb-5 bg-transparent p-0">
          <Surface className="w-9 h-9 rounded-xl grid place-items-center text-lg font-bold text-[#06121a]
            bg-linear-to-br from-cyan-400 via-sky-400 to-violet-400 shadow-lg shadow-cyan-500/25 font-mono p-0">⇄</Surface>
          <Surface className="min-w-0 bg-transparent p-0">
            <Heading level={2} className="text-[13px] font-bold truncate">CDT 管理台</Heading>
            <Paragraph size="xs" color="muted" className="text-[10px] tracking-wider">ALIYUN SPOT OPS</Paragraph>
          </Surface>
        </Surface>
        <Surface className="flex flex-col gap-1 bg-transparent p-0">
          {NAV.map((n) => {
            const isActive = props.view === n.key;
            return (
              <Button
                key={n.key}
                variant={isActive ? "secondary" : "ghost"}
                onPress={() => props.onNavigate(n.key)}
                className={`w-full justify-start gap-2.5 px-3 py-2 text-[13px] ${
                  isActive
                    ? "text-sky-600 dark:text-cyan-400 font-semibold bg-sky-500/10 dark:bg-cyan-500/15"
                    : "text-default-600"
                }`}
              >
                <span className="font-mono text-[14px]">{n.icon}</span>
                <span>{n.label}</span>
                {props.counts[n.key] != null && (
                  <Chip
                    size="sm"
                    variant={isActive ? "primary" : "soft"}
                    className="ml-auto num text-[10.5px]"
                  >
                    {props.counts[n.key]}
                  </Chip>
                )}
              </Button>
            );
          })}
        </Surface>
        <Surface className="mt-auto flex flex-col gap-1 pt-4 bg-transparent p-0">
          <Separator className="mb-2 opacity-40" />
          <Button variant="ghost" onPress={props.onToggleTheme}
            className="w-full justify-start gap-2.5 px-3 py-2 text-[13px] text-default-600">
            <Typography type="body-sm" className="font-mono">{props.theme === "dark" ? "☀" : "☾"}</Typography>
            {props.theme === "dark" ? "日间模式" : "夜间模式"}
          </Button>
          <Button variant="ghost" onPress={props.onSettings}
            className="w-full justify-start gap-2.5 px-3 py-2 text-[13px] text-default-600">
            <Typography type="body-sm" className="font-mono">⚙</Typography> 设置
          </Button>
          <Button variant="ghost" onPress={props.onLogout}
            className="w-full justify-start gap-2.5 px-3 py-2 text-[13px] text-default-600">
            <Typography type="body-sm" className="font-mono">⏻</Typography> 退出登录
          </Button>
          <Code className="px-3 pt-2 text-[10px] text-default-400 bg-transparent">cdt-manager-go · v1.0</Code>
        </Surface>
      </Surface>

      <Surface className="min-w-0 flex-1 flex flex-col bg-transparent p-0">
        {/* 页头 */}
        <Header className="sticky top-0 z-30 flex items-center justify-between gap-1.5 sm:gap-3 px-3 sm:px-4 md:px-6 py-2.5 md:py-3 pt-[calc(0.625rem+env(safe-area-inset-top))]
          border-b border-default-200 bg-surface/70 backdrop-blur-xl">
          <Surface className="flex items-center gap-1.5 sm:gap-2 min-w-0 shrink-0 bg-transparent p-0">
            <Surface className="md:hidden w-6 h-6 sm:w-7 sm:h-7 rounded-lg grid place-items-center text-xs sm:text-sm font-bold text-[#06121a]
              bg-linear-to-br from-cyan-400 via-sky-400 to-violet-400 font-mono shrink-0 p-0">⇄</Surface>
            <Heading level={1} className="text-sm sm:text-base md:text-lg font-bold whitespace-nowrap">{VIEW_TITLES[props.view]}</Heading>
          </Surface>
          <Surface className="flex gap-1 sm:gap-2 items-center justify-end shrink-0 bg-transparent p-0">
            <Button size="sm" variant="tertiary" onPress={props.onToggleTheme} aria-label="切换日间/夜间模式" className="h-7 sm:h-8 px-2 sm:px-3 text-xs sm:text-sm min-w-0">
              <Typography type="body-xs" className="sm:hidden">{props.theme === "dark" ? "☀" : "☾"}</Typography>
              <Typography type="body-xs" className="hidden sm:inline">{props.theme === "dark" ? "☀ 日间" : "☾ 夜间"}</Typography>
            </Button>
            <Button size="sm" variant="secondary" onPress={props.onReconcile} className="h-7 sm:h-8 px-2 sm:px-3 text-xs sm:text-sm min-w-0">
              ⟳ 对账
            </Button>
            <Button size="sm" variant="secondary" onPress={props.onRefresh} className="h-7 sm:h-8 px-2 sm:px-3 text-xs sm:text-sm min-w-0">
              ⟳ 刷新
            </Button>
            {props.actions}
            <Button size="sm" variant="ghost" onPress={props.onSettings} className="md:hidden h-7 sm:h-8 px-1.5 min-w-0" aria-label="设置">
              ⚙
            </Button>
            <Button size="sm" variant="ghost" onPress={props.onLogout} className="md:hidden h-7 sm:h-8 px-1.5 min-w-0" aria-label="退出登录">
              ⏻
            </Button>
          </Surface>
        </Header>

        <Surface className="flex-1 px-3 sm:px-4 md:px-6 py-4 md:py-5 pb-24 md:pb-8 min-w-0 bg-transparent">{props.children}</Surface>
      </Surface>

      {/* 移动端底部导航 */}
      <Surface className="md:hidden fixed bottom-0 left-0 right-0 z-40 flex rounded-none p-0
        border-t border-default-200 bg-surface/95 backdrop-blur-xl pb-[env(safe-area-inset-bottom)]">
        {NAV.map((n) => {
          const isActive = props.view === n.key;
          return (
            <button
              key={n.key}
              type="button"
              onClick={() => props.onNavigate(n.key)}
              className={`group flex-1 min-w-0 pt-2 pb-1.5 flex flex-col items-center justify-center gap-0.5 relative transition-all active:scale-95 select-none bg-transparent border-0 cursor-pointer ${
                isActive ? "text-sky-600 dark:text-cyan-400 font-semibold" : "text-default-500 hover:text-default-700 dark:hover:text-default-300"
              }`}
            >
              {/* 顶部高亮指示条 */}
              <div
                className={`absolute top-0 left-3 right-3 h-0.5 rounded-full transition-all ${
                  isActive
                    ? "bg-sky-600 dark:bg-cyan-400 dark:shadow-[0_0_8px_rgba(34,211,238,0.8)] opacity-100"
                    : "bg-transparent opacity-0"
                }`}
              />

              {/* 图标与高亮胶囊底色 */}
              <span
                className={`w-9 h-6 rounded-full flex items-center justify-center font-mono text-base leading-none transition-colors ${
                  isActive
                    ? "bg-sky-500/15 dark:bg-cyan-500/20 text-sky-600 dark:text-cyan-300 font-bold"
                    : "text-default-500 dark:text-default-400 group-hover:text-default-700 dark:group-hover:text-default-200"
                }`}
              >
                {n.icon}
              </span>

              {/* 文本标签 */}
              <span
                className={`text-[10px] leading-tight transition-colors truncate max-w-full px-1 ${
                  isActive
                    ? "text-sky-600 dark:text-cyan-300 font-bold"
                    : "text-default-500 dark:text-default-400 font-normal"
                }`}
              >
                {n.label}
              </span>

              {/* 数量角标 */}
              {props.counts[n.key] != null && (
                <span
                  className={`absolute top-1 right-2 num text-[9px] px-1 min-h-3.5 h-3.5 rounded-full flex items-center justify-center transition-colors ${
                    isActive
                      ? "bg-sky-600 text-white dark:bg-cyan-400 dark:text-slate-950 font-bold"
                      : "bg-default-200 text-default-600 dark:bg-default-100 dark:text-default-400"
                  }`}
                >
                  {props.counts[n.key]}
                </span>
              )}
            </button>
          );
        })}
      </Surface>
    </Surface>
  );
}

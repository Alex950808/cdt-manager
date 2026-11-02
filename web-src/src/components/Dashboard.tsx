import {
  Button, Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle,
  Chip, Code, EmptyState, Heading, Paragraph, ProgressBar, ProgressCircle,
  ScrollShadow, Separator, Surface, Typography,
} from "@heroui/react";
import type { Event, Group, Instance, Keepalive } from "../api";
import { PHASE_TEXT, relTime, statusTone } from "../hooks";

type ChipTone = "default" | "accent" | "success" | "warning" | "danger";

const EV_BADGES: Record<string, [string, ChipTone]> = {
  start: ["♡", "success"],
  shift_done: ["⇄", "accent"],
  manual: ["✋", "accent"],
  recovery: ["⚡", "warning"],
  group_new: ["✦", "success"],
  group_dissolve: ["✕", "default"],
  failover: ["⚡", "warning"],
  shift_failover: ["⚡", "warning"],
  limit_stop: ["⛔", "danger"],
  breaker: ["⛔", "danger"],
  dns: ["◍", "accent"],
  billing: ["¥", "warning"],
  daily_report: ["▤", "accent"],
  error: ["⚠", "danger"],
  warning: ["⚠", "warning"],
  warn: ["⚠", "warning"],
  info: ["✦", "default"],
};

function StatCard(props: { label: string; icon: string; value: string; unit?: string; sub: string; pct: number; tone?: "violet"; ringLabel?: string }) {
  const p = Math.max(0, Math.min(1, props.pct));
  return (
    <Card className="relative overflow-hidden">
      <CardHeader className="pb-0">
        <CardDescription className="text-xs font-semibold flex items-center gap-1.5">
          <Typography className="font-mono" type="body-xs">{props.icon}</Typography> {props.label}
        </CardDescription>
      </CardHeader>
      <CardContent className="gap-1 pt-1 pr-12">
        <Heading level={3} className="num text-xl sm:text-2xl font-bold leading-tight truncate">
          {props.value}
          {props.unit && <Typography type="body-sm" color="muted" className="font-medium ml-1 inline">{props.unit}</Typography>}
        </Heading>
        <Paragraph size="xs" color="muted" className="mt-1 truncate">{props.sub}</Paragraph>
        <Surface className="absolute top-3 right-3 w-11 h-11 grid place-items-center bg-transparent p-0">
          <ProgressCircle
            aria-label={props.label}
            value={Math.round(p * 100)}
            color={props.tone === "violet" ? "accent" : "default"}
            size="md"
            className="w-11 h-11"
          >
            <ProgressCircle.Track className="w-11 h-11">
              <ProgressCircle.TrackCircle />
              <ProgressCircle.FillCircle className={props.tone === "violet" ? "stroke-violet-400" : "stroke-cyan-400"} />
            </ProgressCircle.Track>
          </ProgressCircle>
          <Typography type="body-xs" weight="bold" className="absolute inset-0 grid place-items-center text-[9px]">
            {props.ringLabel ?? Math.round(p * 100) + "%"}
          </Typography>
        </Surface>
      </CardContent>
    </Card>
  );
}

function phaseChip(phase?: string) {
  return <Chip color={statusTone(phase)} size="sm" variant="soft">{PHASE_TEXT[phase || ""] || phase || "—"}</Chip>;
}

function eventStatusChip(status: string) {
  const map: Record<string, [string, "success" | "warning" | "danger"]> = {
    done: ["完成", "success"], running: ["进行中", "warning"], error: ["失败", "danger"],
  };
  const [text, tone] = map[status] || [status, "warning"];
  return <Chip color={tone} size="sm" variant="soft">{text}</Chip>;
}

export default function Dashboard(props: {
  keepalives?: Keepalive[];
  instances: Instance[];
  groups: Group[];
  dnsCount: number;
  events: Event[];
  onClearEvents?: () => void;
}) {
  const list = props.instances;
  const running = list.filter((v) => v.snapshot?.status === "Running").length;
  const kvs = props.keepalives || [];
  const kvRunning = kvs.filter((k) => k.status === "Running").length;
  const enabledGroups = props.groups.filter((g) => g.enabled);
  const perAK = new Map<string, number>();
  let allBelow = true;
  for (const v of list) {
    if (v.snapshot?.traffic_gb != null && !perAK.has(v.access_key_id)) perAK.set(v.access_key_id, v.snapshot.traffic_gb);
    if (v.snapshot?.traffic_gb != null && v.snapshot.traffic_gb >= (v.threshold_gb || 188)) allBelow = false;
  }
  let total = 0;
  perAK.forEach((gb) => (total += gb));

  return (
    <Surface className="flex flex-col gap-5 bg-transparent p-0">
      <Surface className="flex items-center justify-between flex-wrap gap-2 bg-transparent p-0">
        <Code className="num text-xs">
          CST {new Date().toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })}
        </Code>
        <Surface className="flex gap-2 flex-wrap bg-transparent p-0">
          <Chip size="sm" variant="soft" color="accent">DNS {props.dnsCount} 条</Chip>
          <Chip size="sm" variant="soft" color="default">通知见设置</Chip>
        </Surface>
      </Surface>

      <Surface className="grid grid-cols-2 lg:grid-cols-4 gap-3 bg-transparent p-0">
        <StatCard icon="▤" label="服务器" value={String(list.length)} unit="台"
          sub={`${running} 台运行中`} pct={list.length ? running / list.length : 0} />
        <StatCard icon="♡" label="单机保活" value={String(kvs.length)} unit="台"
          sub={kvs.length ? `${kvRunning} 台运行中` : "0 台启用"} pct={kvs.length ? kvRunning / kvs.length : 0} />
        <StatCard icon="⇄" label="联合换班" value={String(enabledGroups.length)} unit="组"
          sub={`${enabledGroups.length} 个启用`} pct={enabledGroups.length ? 1 : 0} tone="violet" />
        <StatCard icon="◑" label="月累计流量" value={perAK.size ? total.toFixed(1) : "--"} unit={perAK.size ? "GB" : undefined}
          sub={perAK.size ? (allBelow ? "全部低于阈值" : "有实例已超阈值") : "--"}
          pct={perAK.size ? (allBelow ? 0 : 0.5) : 0} ringLabel={allBelow ? "稳" : "超"} />
      </Surface>

      <Surface className="flex items-center gap-2 mt-1 bg-transparent p-0">
        <Heading level={2} className="text-sm font-bold">当前当班</Heading>
        <Paragraph size="xs" color="muted">联合换班组实时状态</Paragraph>
      </Surface>

      {enabledGroups.length === 0 && (
        <Card>
          <CardContent>
            <EmptyState className="py-6 text-default-500 text-sm">还没有启用中的换班组</EmptyState>
          </CardContent>
        </Card>
      )}

      {enabledGroups.map((g) => {
        const duty = (g.members_view || []).find((m) => m.duty);
        const inst = duty ? list.find((x) => x.id === duty.instance_id) : undefined;
        const thr = inst?.threshold_gb || 188;
        const used = duty?.traffic_gb ?? null;
        const pct = used != null ? Math.min(100, (used / thr) * 100) : 0;
        const standbys = (g.members_view || []).filter((m) => !m.duty).map((m) => m.name);
        return (
          <Card key={g.id} className="overflow-hidden">
            <CardHeader className="flex flex-row items-center justify-between gap-2 flex-wrap">
              <CardTitle className="text-sm font-bold">⇄ {g.name}</CardTitle>
              {phaseChip(g.phase || g.run?.phase)}
            </CardHeader>
            <CardContent className="gap-3">
              {duty ? (
                <Surface variant="secondary" className="rounded-xl border border-primary-500/20 p-4 flex flex-col gap-2">
                  <Surface className="flex justify-between items-center bg-transparent p-0">
                    <Code className="text-[11px]">
                      {duty.duty_hours ? `${duty.duty_hours} 小时班` : duty.duty_days ? `${duty.duty_days} 天班` : duty.duty_start ? `时刻班 ${duty.duty_start}` : "班次中"}
                    </Code>
                    <Chip size="sm" variant="soft" color="success">当班</Chip>
                  </Surface>
                  <Heading level={3} className="text-xl font-bold">{duty.name}</Heading>
                  <Surface className="flex gap-2 flex-wrap bg-transparent p-0">
                    <Chip size="sm" variant="soft" color={duty.status === "Running" ? "success" : "default"}>{duty.status || "未知"}</Chip>
                    <Chip size="sm" variant="soft" className="num">{inst?.snapshot?.public_ip || "—"}</Chip>
                  </Surface>
                  <ProgressBar aria-label="当班流量占比" value={pct} color="accent" size="sm">
                    <ProgressBar.Track>
                      <ProgressBar.Fill className="bg-linear-to-r from-cyan-400 to-violet-400" />
                    </ProgressBar.Track>
                  </ProgressBar>
                  <Surface className="flex justify-between text-[11px] text-default-500 num bg-transparent p-0">
                    <Typography type="body-xs" color="muted" className="num">{used != null ? used.toFixed(2) + " GB" : "--"}</Typography>
                    <Typography type="body-xs" color="muted" className="num">{used != null ? ((used / thr) * 100).toFixed(1) + "% / 阈值 " + thr + " GB" : "流量读数不可用"}</Typography>
                  </Surface>
                </Surface>
              ) : (
                <Paragraph size="sm" color="muted">当前无人当班</Paragraph>
              )}
            </CardContent>
            <CardFooter className="flex flex-row items-center justify-between text-xs text-default-500 flex-wrap gap-1 pt-0">
              <Paragraph size="xs" color="muted">待机 {standbys.length} 台 · {standbys.join("、") || "—"}</Paragraph>
              {g.next_switch && (
                <Code className="num text-xs">
                  下次 {new Date(g.next_switch).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })}
                </Code>
              )}
            </CardFooter>
          </Card>
        );
      })}

      <Surface className="flex items-center justify-between flex-wrap gap-2 bg-transparent p-0">
        <Surface className="flex items-center flex-wrap gap-2 bg-transparent p-0">
          <Heading level={2} className="text-sm font-bold">最近事件</Heading>
          <Paragraph size="xs" color="muted">换班 · 转移 · 熔断 · 保活 · 操作</Paragraph>
          <Chip size="sm" variant="soft" className="num">⚡ {props.events.length}</Chip>
        </Surface>
        {props.onClearEvents && props.events.length > 0 && (
          <Button size="sm" variant="ghost" onPress={props.onClearEvents}>清空</Button>
        )}
      </Surface>

      <Card>
        <CardContent className="p-0">
          {(props.events || []).length === 0 ? (
            <EmptyState className="py-6 text-default-500 text-sm">暂无事件</EmptyState>
          ) : (
            <ScrollShadow className="max-h-96 px-2">
              {(props.events || []).map((e, idx) => {
                const [icon, tone] = EV_BADGES[e.kind] || EV_BADGES.info;
                return (
                  <Surface key={e.id} className="bg-transparent p-0">
                    {idx > 0 && <Separator className="opacity-40" />}
                    <Surface className="flex gap-2.5 sm:gap-3 px-1.5 sm:px-2 py-2.5 items-start bg-transparent">
                      <Chip size="sm" variant="soft" color={tone} className="w-8 h-8 sm:w-9 sm:h-9 rounded-lg grid place-items-center text-sm sm:text-base font-bold shrink-0">
                        {icon}
                      </Chip>
                      <Surface className="min-w-0 flex-1 bg-transparent p-0">
                        <Surface className="flex items-center gap-1.5 sm:gap-2 flex-wrap bg-transparent p-0">
                          <Typography type="body-sm" weight="semibold" className="break-all">{e.title}</Typography>
                          {eventStatusChip(e.status)}
                          <Code className="num text-[11px] whitespace-nowrap ml-auto">{relTime(e.at)}</Code>
                        </Surface>
                        {(e.detail || e.progress?.length) && (
                          <Surface className="mt-1 flex flex-col gap-0.5 bg-transparent p-0">
                            {[e.detail, ...(e.progress || [])].filter(Boolean).map((l, i) => (
                              <Paragraph key={i} size="xs" color="muted" className="leading-relaxed break-all">{l}</Paragraph>
                            ))}
                          </Surface>
                        )}
                      </Surface>
                    </Surface>
                  </Surface>
                );
              })}
            </ScrollShadow>
          )}
        </CardContent>
      </Card>
    </Surface>
  );
}

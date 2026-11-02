import { useCallback, useEffect, useState } from "react";
import {
  Button, EmptyState, ModalBackdrop, ModalBody, ModalCloseTrigger, ModalContainer, ModalDialog, ModalFooter, ModalHeader, ModalHeading,
  Paragraph, Spinner, Surface, Toast,
} from "@heroui/react";
import { api } from "./api";
import { useInterval, useTheme } from "./hooks";
import Layout, { type View } from "./components/Layout";
import LoginPage from "./components/LoginPage";
import Dashboard from "./components/Dashboard";
import Servers, { ConfirmModal, InstanceDialog, StopDialog } from "./components/Servers";
import KeepaliveView from "./components/Keepalive";
import Shift, { GroupDialog } from "./components/Shift";
import Dns from "./components/Dns";
import SettingsDrawer from "./components/SettingsDrawer";
import type { DnsRecord, Event, Group, Instance, Keepalive as KeepaliveType, Sample, Settings } from "./api";

type Phase = "loading" | "login" | "app";

export default function App() {
  const [theme, toggleTheme] = useTheme();
  const [phase, setPhase] = useState<Phase>("loading");
  const [loginForm, setLoginForm] = useState<"setup" | "account" | "static">("account");
  const [view, setView] = useState<View>("dashboard");

  const [instances, setInstances] = useState<Instance[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [dns, setDns] = useState<DnsRecord[]>([]);
  const [dnsNames, setDnsNames] = useState<string[]>([]);
  const [keepalives, setKeepalives] = useState<KeepaliveType[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [settings, setSettings] = useState<Settings>({});

  const [busy, setBusy] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [editInst, setEditInst] = useState<Instance | null>(null);
  const [instDlgOpen, setInstDlgOpen] = useState(false);
  const [stopInst, setStopInst] = useState<Instance | null>(null);
  const [stopOpen, setStopOpen] = useState(false);
  const [delInst, setDelInst] = useState<Instance | null>(null);
  const [chartInst, setChartInst] = useState<Instance | null>(null);
  const [chartOpen, setChartOpen] = useState(false);
  const [chartSamples, setChartSamples] = useState<Sample[]>([]);
  const [editGroup, setEditGroup] = useState<Group | null>(null);
  const [groupDlgOpen, setGroupDlgOpen] = useState(false);
  const [delGroup, setDelGroup] = useState<Group | null>(null);

  const counts: Partial<Record<View, number>> = {
    servers: instances.length,
    keepalive: keepalives.length,
    shift: groups.length,
    dns: dns.length,
  };

  /* ---- 数据加载 ---- */
  const loadCore = useCallback(async () => {
    const [insts, kvs, gs] = await Promise.all([
      api.get<Instance[]>("/api/instances"),
      api.get<KeepaliveType[]>("/api/keepalives"),
      api.get<Group[]>("/api/groups"),
    ]);
    const safeInsts = Array.isArray(insts) ? insts : [];
    const safeKvs = Array.isArray(kvs) ? kvs : [];
    const safeGs = Array.isArray(gs) ? gs : [];
    setInstances(safeInsts);
    setKeepalives(safeKvs.map((k: KeepaliveType) => ({
      ...k,
      name: safeInsts.find((i) => i.id === k.instance_id)?.name || k.instance_id,
      status: safeInsts.find((i) => i.id === k.instance_id)?.snapshot?.status,
    })));
    setGroups(safeGs);
  }, []);
  const loadEvents = useCallback(async () => {
    const evs = await api.get<Event[]>("/api/events");
    setEvents(Array.isArray(evs) ? evs : []);
  }, []);
  const loadDns = useCallback(async () => {
    const [records, cfg] = await Promise.all([
      api.get<DnsRecord[]>("/api/dns"), api.get<Settings>("/api/settings"),
    ]);
    setDns(Array.isArray(records) ? records : []);
    setDnsNames((() => {
      try {
        const parsed = JSON.parse(cfg?.ddns_records || "[]");
        return Array.isArray(parsed) ? parsed : [];
      } catch {
        return [];
      }
    })());
    setSettings(cfg || {});
  }, []);
  const loadAll = useCallback(async () => {
    await Promise.all([loadCore(), loadEvents(), loadDns()]);
  }, [loadCore, loadEvents, loadDns]);

  /* ---- 启动 / 鉴权 ---- */
  useEffect(() => {
    (async () => {
      try {
        const st = await fetch("/api/setup/status", { cache: "no-store" }).then((r) => r.json());
        if (st.initialized === false) {
          localStorage.removeItem("cdtd_token");
          setLoginForm("setup");
          setPhase("login");
          return;
        }
        if (st.mode === "static") setLoginForm("static");
        else setLoginForm("account");
      } catch { /* 忽略 */ }
      const token = localStorage.getItem("cdtd_token");
      if (!token) { setPhase("login"); return; }
      try {
        await loadAll();
        setPhase("app");
      } catch {
        localStorage.removeItem("cdtd_token");
        setPhase("login");
      }
    })();
    const onUnauthorized = () => setPhase("login");
    window.addEventListener("cdtd:unauthorized", onUnauthorized);
    return () => window.removeEventListener("cdtd:unauthorized", onUnauthorized);
  }, [loadAll]);

  useInterval(() => {
    if (phase !== "app") return;
    loadAll().catch(() => { });
  }, 10000);

  /* ---- 登录 ---- */
  const handleLogin = async (payload: { username?: string; password?: string; token?: string }) => {
    try {
      if (loginForm === "setup") {
        await fetch("/api/setup", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
          .then(async (r) => { if (!r.ok) throw new Error((await r.json()).error || "初始化失败"); });
      }
      const body = loginForm === "static" ? { token: payload.password } : payload;
      const r = await fetch("/api/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const data = await r.json().catch(() => ({}));
      if (!r.ok) return data.error || "登录失败";
      const token = data.token || payload.password;
      if (!token) return "部署未启用鉴权";
      localStorage.setItem("cdtd_token", token);
      await loadAll();
      setPhase("app");
      return null;
    } catch (e) {
      return "网络错误：" + (e as Error).message;
    }
  };

  /* ---- 通用错误兜底 ---- */
  const guard = async (fn: () => Promise<void>) => {
    try { await fn(); } catch (e) { Toast.toast.danger((e as Error).message); }
  };

  /* ---- 服务器操作 ---- */
  const handleAction = (id: string, op: "start" | "stop" | "refresh" | "chart") => {
    const inst = instances.find((v) => v.id === id);
    if (op === "stop") { setStopInst(inst || null); setStopOpen(true); return; }
    if (op === "chart") {
      setChartInst(inst || null);
      api.get<Sample[]>(`/api/instances/${id}/samples`)
        .then((s) => setChartSamples(Array.isArray(s) ? s : []))
        .catch(() => setChartSamples([]));
      setChartOpen(true);
      return;
    }
    setBusy(id);
    guard(async () => {
      if (op === "start") { await api.post(`/api/instances/${id}/start`); Toast.toast.success(`已提交启动：${inst?.name}`); }
      if (op === "refresh") await api.post(`/api/instances/${id}/refresh`);
      await Promise.all([loadCore(), loadEvents()]);
    }).then(() => setBusy(null));
  };
  const handleSaveInst = async (body: Record<string, unknown>, id?: string): Promise<string | null> => {
    try {
      if (id) await api.put(`/api/instances/${id}`, body);
      else await api.post("/api/instances", body);
      setInstDlgOpen(false);
      Toast.toast.success(id ? "实例已更新" : "实例已添加");
      await loadCore();
      return null;
    } catch (e) { return (e as Error).message; }
  };
  const handleStop = async (mode: string) => {
    if (!stopInst) return;
    await guard(async () => {
      const r = await api.post<{ effective_mode: string }>(`/api/instances/${stopInst.id}/stop`, { mode });
      if (mode === "StopCharging" && r.effective_mode === "Stop") {
        Toast.toast.warning("该实例不支持节省停机，已改为普通停机（仍按规格计费）");
      } else if (r.effective_mode === "Stop") {
        Toast.toast.success(`已提交普通停机：${stopInst.name}`);
      } else {
        Toast.toast.success(`已提交节省停机：${stopInst.name}`);
      }
      await Promise.all([loadCore(), loadEvents()]);
    });
    setStopOpen(false);
  };
  const handleDeleteInst = async (inst: Instance) => {
    await guard(async () => {
      await api.del(`/api/instances/${inst.id}`);
      Toast.toast.success("已删除");
      await Promise.all([loadCore(), loadDns(), loadEvents()]);
    });
  };

  /* ---- 换班组操作 ---- */
  const handleSwitch = (g: Group) => {
    setBusy(g.id);
    guard(async () => {
      await api.post(`/api/groups/${g.id}/switch`);
      Toast.toast.success("手动换班已提交");
      await loadCore();
    }).then(() => setBusy(null));
  };
  const handleSaveGroup = async (body: Record<string, unknown>, id?: string): Promise<string | null> => {
    try {
      if (id) await api.put(`/api/groups/${id}`, body);
      else await api.post("/api/groups", body);
      setGroupDlgOpen(false);
      Toast.toast.success("换班组已保存");
      await loadCore();
      return null;
    } catch (e) { return (e as Error).message; }
  };
  const handleDissolve = async (g: Group) => {
    await guard(async () => {
      await api.del(`/api/groups/${g.id}`);
      Toast.toast.success("已解散（组员保持当前状态）");
      await loadCore();
    });
  };

  /* ---- DNS 操作 ---- */
  const handleNamesSave = (names: string[]) => {
    guard(async () => { await api.post("/api/dns/records", { records: names }); await loadDns(); Toast.toast.success("登记已保存"); });
  };
  const handleBind = (record: string, kind: string, targetId: string) => {
    guard(async () => { await api.post(`/api/dns/records/${encodeURIComponent(record)}/bind`, { kind, target_id: targetId }); await loadDns(); });
  };
  const handleDnsDelete = (record: string) => {
    guard(async () => { await api.del(`/api/dns/records/${encodeURIComponent(record)}`); await loadDns(); Toast.toast.success("已删除"); });
  };
  const handleSyncOne = (record: string) => {
    guard(async () => { await api.post(`/api/dns/records/${encodeURIComponent(record)}/sync`); await loadDns(); });
  };
  const handleSaveSettings = (patch: Record<string, string>) => {
    guard(async () => {
      await api.post("/api/settings", patch);
      await loadDns();
      setSettingsOpen(false);
      Toast.toast.success("设置已保存");
    });
  };
  const handleTest = (channel: string) => {
    guard(async () => {
      const r = await api.post<{ status: string; detail?: string }>(`/api/settings/test/${channel}`);
      if (r.status?.startsWith("✅")) Toast.toast.success(`${r.status}${r.detail ? "：" + r.detail : ""}`);
      else Toast.toast.warning(`${r.status}${r.detail ? "：" + r.detail : ""}`);
    });
  };

  /* ---- 渲染 ---- */
  if (phase === "loading") {
    return (
      <Surface className="min-h-screen min-h-[100dvh] flex flex-col items-center justify-center gap-3 pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] bg-transparent">
        <Spinner size="lg" />
        <Paragraph size="sm" color="muted">加载中…</Paragraph>
      </Surface>
    );
  }
  if (phase === "login") {
    return <LoginPage form={loginForm} theme={theme} onToggleTheme={toggleTheme} onSubmit={handleLogin} />;
  }

  const actions = (
    <>
      {view === "servers" && (
        <Button size="sm" variant="primary" className="h-7 sm:h-8 px-2 sm:px-3 text-xs sm:text-sm min-w-0" onPress={() => { setEditInst(null); setInstDlgOpen(true); }}>
          ＋ 服务器
        </Button>
      )}
      {view === "shift" && (
        <Button size="sm" variant="primary" className="h-7 sm:h-8 px-2 sm:px-3 text-xs sm:text-sm min-w-0" onPress={() => { setEditGroup(null); setGroupDlgOpen(true); }}>
          ＋ 换班组
        </Button>
      )}
    </>
  );

  const chartModal = chartOpen ? (
    <ModalBackdrop isOpen isDismissable onOpenChange={(o) => !o && setChartOpen(false)}>
      <ModalContainer placement="center">
        <ModalDialog>
          <ModalHeader className="flex items-center justify-between">
            <ModalHeading>{chartInst?.name} · 流量曲线（近 7 天）</ModalHeading>
            <ModalCloseTrigger />
          </ModalHeader>
          <ModalBody>
            {chartSamples.length < 2 ? (
              <EmptyState className="py-8 text-center">
                <Paragraph size="sm" color="muted">采样收集中：引擎每轮对账落一个点（限频 5 分钟）</Paragraph>
              </EmptyState>
            ) : (
              <ChartSVG samples={chartSamples} />
            )}
          </ModalBody>
          <ModalFooter>
            <Button variant="ghost" onPress={() => setChartOpen(false)}>关闭</Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
    </ModalBackdrop>
  ) : null;

  return (
    <>
      <Toast.Provider />
      <Layout
        view={view}
        onNavigate={setView}
        counts={counts}
        actions={actions}
        theme={theme}
        onToggleTheme={toggleTheme}
        onReconcile={() => guard(async () => { await api.post("/api/reconcile"); Toast.toast.success("已触发一轮对账"); })}
        onRefresh={() => guard(async () => { await loadAll(); Toast.toast.success("数据已刷新"); })}
        onSettings={() => setSettingsOpen(true)}
        onLogout={async () => {
          try { await api.post("/api/auth/logout"); } catch { /* 忽略 */ }
          localStorage.removeItem("cdtd_token");
          setPhase("login");
        }}
      >
        {view === "dashboard" && (
          <Dashboard instances={instances} groups={groups} dnsCount={dns.length}
            events={events} keepalives={keepalives}
            onClearEvents={() => guard(async () => { await api.del("/api/events"); await loadEvents(); Toast.toast.success("事件已清空"); })} />
        )}
        {view === "servers" && (
          <Servers instances={instances} busy={busy} onAction={handleAction}
            onEdit={(v) => { setEditInst(v); setInstDlgOpen(true); }}
            onDelete={(v) => setDelInst(v)} />
        )}
        {view === "keepalive" && (
          <KeepaliveView keepalives={keepalives} instances={instances}
            onAdd={(id) => guard(async () => { await api.post("/api/keepalives", { instance_id: id }); await loadCore(); Toast.toast.success("保活已启用"); })}
            onRemove={(id) => guard(async () => { await api.del(`/api/keepalives/${encodeURIComponent(id)}`); await loadCore(); Toast.toast.success("保活已停用"); })} />
        )}
        {view === "shift" && (
          <Shift groups={groups} busy={busy} onSwitch={handleSwitch}
            onEdit={(g) => { setEditGroup(g); setGroupDlgOpen(true); }}
            onDissolve={(g) => setDelGroup(g)} />
        )}
        {view === "dns" && (
          <Dns records={dns} instances={instances} groups={groups} names={dnsNames}
            onNamesSave={handleNamesSave} onBind={handleBind} onDelete={handleDnsDelete}
            onSyncOne={handleSyncOne}
            onSyncAll={() => guard(async () => { await api.post("/api/dns/sync"); Toast.toast.success("已触发全部同步"); })}
            onTest={() => handleTest("ddns")} />
        )}
      </Layout>

      <SettingsDrawer open={settingsOpen} settings={settings}
        onClose={() => setSettingsOpen(false)} onSave={handleSaveSettings} onTest={handleTest} />

      <InstanceDialog open={instDlgOpen} editing={editInst}
        onClose={() => setInstDlgOpen(false)} onSave={handleSaveInst} />
      {stopOpen && stopInst && (
        <StopDialog open inst={stopInst} onClose={() => setStopOpen(false)} onStop={handleStop} />
      )}
      <GroupDialog open={groupDlgOpen} editing={editGroup} instances={instances}
        onClose={() => setGroupDlgOpen(false)} onSave={handleSaveGroup} />
      {!!delInst && (
        <ConfirmModal open danger title="删除实例"
          message={`确定删除「${delInst.name}」？（只解除管理，不影响云上实例）`}
          onClose={() => setDelInst(null)} onConfirm={() => handleDeleteInst(delInst)} />
      )}
      {!!delGroup && (
        <ConfirmModal open danger title="解散换班组"
          message={`解散「${delGroup.name}」？（不发任何启停指令，组员保持当前状态）`}
          onClose={() => setDelGroup(null)} onConfirm={() => handleDissolve(delGroup)} />
      )}
      {chartModal}
    </>
  );
}

function ChartSVG(props: { samples: Sample[] }) {
  const W = 540, H = 200, PAD = 36;
  const xs = props.samples.map((s) => new Date(s.at).getTime());
  const x0 = xs[0], x1 = xs[xs.length - 1];
  const yMax = Math.max(...props.samples.map((s) => s.gb), 1) * 1.1;
  const px = (t: number) => PAD + ((t - x0) / Math.max(x1 - x0, 1)) * (W - PAD * 2);
  const py = (gb: number) => H - PAD - (gb / yMax) * (H - PAD * 2);
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full rounded-xl border border-default-200 bg-surface-secondary/50">
      {[0, 0.5, 1].map((f) => (
        <g key={f}>
          <line x1={PAD} y1={H - PAD - f * (H - PAD * 2)} x2={W - PAD} y2={H - PAD - f * (H - PAD * 2)} className="stroke-default-300/40" />
          <text x={4} y={H - PAD - f * (H - PAD * 2) + 4} fontSize="9" className="fill-default-500 font-mono">{(yMax * f).toFixed(0)}GB</text>
        </g>
      ))}
      <polyline points={props.samples.map((s) => `${px(new Date(s.at).getTime()).toFixed(1)},${py(s.gb).toFixed(1)}`).join(" ")}
        fill="none" className="stroke-cyan-400" strokeWidth="2" strokeLinejoin="round" />
      {props.samples.map((s, i) => (
        <circle key={i} cx={px(new Date(s.at).getTime()).toFixed(1)} cy={py(s.gb).toFixed(1)} r="2.5" className="fill-cyan-400" />
      ))}
      <text x={PAD} y={H - 8} fontSize="9" className="fill-default-500 font-mono">
        {new Date(x0).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })}
      </text>
      <text x={W - PAD - 76} y={H - 8} fontSize="9" className="fill-default-500 font-mono">
        {new Date(x1).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })}
      </text>
    </svg>
  );
}

import { useState } from "react";
import {
  Alert, Button, Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle,
  Chip, Code, EmptyState, FieldError, Form, Input, Label, ListBox,
  ModalBackdrop, ModalBody, ModalCloseTrigger, ModalContainer, ModalDialog, ModalFooter, ModalHeader, ModalHeading,
  Paragraph, Select, Surface, TextField, Typography,
} from "@heroui/react";
import type { Instance } from "../api";
import { statusTone } from "../hooks";

export const REGIONS: Array<[string, string]> = [
  ["cn-hongkong", "中国香港"], ["cn-hangzhou", "华东1（杭州）"], ["cn-shanghai", "华东2（上海）"],
  ["cn-nanjing", "华东5（南京）"], ["cn-fuzhou", "华东6（福州）"], ["cn-qingdao", "华北1（青岛）"],
  ["cn-beijing", "华北2（北京）"], ["cn-zhangjiakou", "华北3（张家口）"], ["cn-huhehaote", "华北5（呼和浩特）"],
  ["cn-wulanchabu", "华北6（乌兰察布）"], ["cn-shenzhen", "华南1（深圳）"], ["cn-heyuan", "华南2（河源）"],
  ["cn-guangzhou", "华南3（广州）"], ["cn-chengdu", "西南1（成都）"], ["cn-chongqing", "西南2（重庆）"],
  ["ap-southeast-1", "新加坡"], ["ap-southeast-2", "悉尼"], ["ap-southeast-3", "吉隆坡"],
  ["ap-southeast-4", "雅加达"], ["ap-southeast-6", "马尼拉"], ["ap-southeast-7", "曼谷"],
  ["ap-northeast-1", "东京"], ["ap-northeast-2", "首尔"], ["ap-south-1", "孟买"],
  ["us-east-1", "弗吉尼亚"], ["us-west-1", "硅谷"], ["eu-west-1", "伦敦"],
  ["eu-central-1", "法兰克福"], ["me-central-1", "利雅得"], ["me-east-1", "迪拜"],
];

const STATUS_TEXT: Record<string, string> = { Running: "运行中", Starting: "启动中", Stopping: "停止中", Stopped: "已停机" };

export default function Servers(props: {
  instances: Instance[];
  busy: string | null;
  onAction: (id: string, op: "start" | "stop" | "refresh" | "chart") => void;
  onEdit: (inst: Instance) => void;
  onDelete: (inst: Instance) => void;
}) {
  if (!props.instances.length) {
    return (
      <Card>
        <CardContent>
          <EmptyState className="py-10 text-center text-default-500">
            <Paragraph size="sm" color="muted">还没有实例，点击右上角「＋ 服务器」添加</Paragraph>
          </EmptyState>
        </CardContent>
      </Card>
    );
  }
  return (
    <Surface className="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-3 gap-3 bg-transparent p-0">
      {props.instances.map((v) => {
        const s = v.snapshot;
        return (
          <Card key={v.id} className={props.busy === v.id ? "opacity-60 pointer-events-none" : ""}>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <CardTitle className="text-[15px] font-bold">{v.name}</CardTitle>
              <Chip size="sm" variant="soft" color={statusTone(s?.status)}>
                {STATUS_TEXT[s?.status || ""] || "未刷新"}
              </Chip>
            </CardHeader>
            <CardContent className="gap-2.5">
              <CardDescription className="text-xs leading-relaxed break-all flex flex-col gap-0.5">
                <Typography type="body-xs" color="muted">
                  <Code className="num text-xs">{v.instance_id}</Code> @ {v.region_id}（{v.site === "international" ? "国际站" : "中国站"}）
                </Typography>
                <Typography type="body-xs" color="muted">
                  公网 <Code className="num text-xs">{s?.public_ip || "—"}</Code>｜AK <Code className="num text-xs">{v.access_key_id}</Code>/<Code className="num text-xs">{v.secret_masked || "—"}</Code>
                </Typography>
                <Typography type="body-xs" color="muted">
                  刷新于 {s?.refreshed_at ? new Date(s.refreshed_at).toLocaleTimeString("zh-CN", { hour12: false }) : "从未"}
                  {s?.stopped_mode ? ` · ${s.stopped_mode === "StopCharging" ? "节省停机" : "普通停机"}` : ""}
                </Typography>
                {s?.refresh_error && (
                  <Alert status="danger" className="mt-1 py-1.5 px-2.5">
                    <Alert.Indicator />
                    <Alert.Content>
                      <Alert.Description className="text-xs">{s.refresh_error}</Alert.Description>
                    </Alert.Content>
                  </Alert>
                )}
              </CardDescription>
              <Surface className="grid grid-cols-3 gap-1.5 sm:gap-2 bg-transparent p-0">
                <Surface variant="secondary" className="min-w-0 rounded-lg border border-default-200 px-2 sm:px-2.5 py-1.5">
                  <Typography type="body-sm" weight="bold" className="num truncate block">
                    {s?.refresh_error ? "⚠" : s?.traffic_gb == null ? "无明细" : s.traffic_gb.toFixed(2)}
                  </Typography>
                  <Paragraph size="xs" color="muted" className="text-[10px]">当月流量</Paragraph>
                </Surface>
                <Surface variant="secondary" className="min-w-0 rounded-lg border border-default-200 px-2 sm:px-2.5 py-1.5">
                  <Typography type="body-sm" weight="bold" className="num truncate block">
                    {s?.currency ? s.balance.toFixed(2) : "--"}
                  </Typography>
                  <Paragraph size="xs" color="muted" className="text-[10px]">可用余额</Paragraph>
                </Surface>
                <Surface variant="secondary" className="min-w-0 rounded-lg border border-default-200 px-2 sm:px-2.5 py-1.5">
                  <Typography type="body-sm" weight="bold" className="num truncate block">
                    {s?.bill_total != null ? s.bill_total.toFixed(2) : "--"}
                  </Typography>
                  <Paragraph size="xs" color="muted" className="text-[10px]">本月账单</Paragraph>
                </Surface>
              </Surface>
            </CardContent>
            <CardFooter className="flex flex-row flex-wrap gap-1.5 pt-0.5">
              <Button
                size="sm"
                variant="primary"
                isDisabled={s?.status === "Running" || s?.status === "Starting" || s?.status === "Pending"}
                onPress={() => props.onAction(v.id, "start")}
              >
                ▶ 启动
              </Button>
              <Button
                size="sm"
                variant="secondary"
                isDisabled={s?.status === "Stopped" || s?.status === "Stopping"}
                onPress={() => props.onAction(v.id, "stop")}
              >
                ■ 停止
              </Button>
              <Button size="sm" variant="tertiary" onPress={() => props.onAction(v.id, "refresh")}>↻ 刷新</Button>
              <Button size="sm" variant="tertiary" onPress={() => props.onAction(v.id, "chart")}>▤ 曲线</Button>
              <Button size="sm" variant="ghost" onPress={() => props.onEdit(v)}>✎ 编辑</Button>
              <Button size="sm" variant="ghost" onPress={() => props.onDelete(v)}>✕ 删除</Button>
            </CardFooter>
          </Card>
        );
      })}
    </Surface>
  );
}

export function InstanceDialog(props: {
  open: boolean;
  editing: Instance | null;
  onClose: () => void;
  onSave: (body: Record<string, unknown>, id?: string) => Promise<string | null>;
}) {
  const [form, setForm] = useState<Record<string, string>>({});
  const [editingId, setEditingId] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState(false);
  const [err, setErr] = useState("");
  const key = props.editing?.id || "new";
  const [lastKey, setLastKey] = useState("");
  if (props.open && key !== lastKey) {
    setLastKey(key);
    setEditingId(props.editing?.id || null);
    setForm({
      name: props.editing?.name || "", access_key_id: props.editing?.access_key_id || "",
      access_key_secret: "", instance_id: props.editing?.instance_id || "",
      region_id: props.editing?.region_id || "cn-hongkong", site: props.editing?.site || "china",
      threshold_gb: String(props.editing?.threshold_gb || 188), note: props.editing?.note || "",
    });
    setSubmitted(false);
    setErr("");
  }
  if (!props.open && lastKey) {
    setLastKey("");
  }
  const fieldErr = (k: string): string => {
    if (!submitted) return "";
    if (k === "name" && !form.name?.trim()) return "请输入实例名称";
    if (k === "access_key_id" && !editingId && !form.access_key_id?.trim()) return "请输入 AccessKey ID";
    if (k === "access_key_secret" && !editingId && !form.access_key_secret) return "请输入 AccessKey Secret";
    if (k === "instance_id" && !form.instance_id?.trim()) return "请输入实例 ID";
    return "";
  };
  const tf = (label: string, k: string, extra?: { type?: string; autoComplete?: string }) => {
    const errMsg = fieldErr(k);
    const id = `inst-${k}`;
    return (
      <TextField fullWidth isInvalid={!!errMsg}>
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          fullWidth
          type={extra?.type}
          autoComplete={extra?.autoComplete}
          value={form[k] ?? ""}
          onChange={(e) => setForm({ ...form, [k]: e.target.value })}
        />
        <FieldError>{errMsg}</FieldError>
      </TextField>
    );
  };
  return (
    <ModalBackdrop isOpen={props.open} isDismissable onOpenChange={(o) => !o && props.onClose()}>
      <ModalContainer placement="auto">
        <ModalDialog className="sm:max-w-lg">
          <ModalCloseTrigger />
          <ModalHeader>
            <ModalHeading>{editingId ? "编辑实例" : "添加实例"}</ModalHeading>
          </ModalHeader>
          <ModalBody className="p-6">
            <Form id="instance-form" className="flex flex-col gap-4"
              onSubmit={async (e) => {
                e.preventDefault();
                setSubmitted(true);
                if (!form.name?.trim() || !form.instance_id?.trim() || (!editingId && (!form.access_key_id?.trim() || !form.access_key_secret))) {
                  return;
                }
                const error = await props.onSave({
                  name: form.name.trim(), access_key_id: form.access_key_id.trim(),
                  access_key_secret: form.access_key_secret, instance_id: form.instance_id.trim(),
                  region_id: form.region_id, site: form.site,
                  threshold_gb: parseFloat(form.threshold_gb) || 188, note: form.note.trim(),
                }, editingId || undefined);
                setErr(error || "");
              }}>
              {tf("名称", "name")}
              {tf("AccessKey ID", "access_key_id", { autoComplete: "off" })}
              {tf("AccessKey Secret（编辑时留空不修改）", "access_key_secret", { type: "password", autoComplete: "new-password" })}
              <Surface className="grid grid-cols-1 sm:grid-cols-2 gap-3 bg-transparent p-0">
                {tf("实例 ID", "instance_id")}
                <Select
                  fullWidth
                  value={form.region_id || "cn-hongkong"}
                  onChange={(k) => k != null && setForm({ ...form, region_id: String(k) })}
                >
                  <Label>地域</Label>
                  <Select.Trigger className="font-mono text-xs">
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      {REGIONS.map(([v, label]) => (
                        <ListBox.Item key={v} id={v} textValue={`${v}｜${label}`} className="font-mono text-xs">
                          {v}｜{label}
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                      {form.region_id && !REGIONS.some(([v]) => v === form.region_id) && (
                        <ListBox.Item key={form.region_id} id={form.region_id} textValue={`${form.region_id}（自定义）`} className="font-mono text-xs">
                          {form.region_id}（自定义）
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </Surface>
              <Surface className="grid grid-cols-1 sm:grid-cols-2 gap-3 bg-transparent p-0">
                <Select
                  fullWidth
                  value={form.site || "china"}
                  onChange={(k) => k != null && setForm({ ...form, site: String(k) })}
                >
                  <Label>站点</Label>
                  <Select.Trigger>
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      <ListBox.Item id="china" textValue="中国站">
                        中国站
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                      <ListBox.Item id="international" textValue="国际站">
                        国际站
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    </ListBox>
                  </Select.Popover>
                </Select>
                {tf("阈值 GB", "threshold_gb", { type: "number" })}
              </Surface>
              {tf("备注", "note")}
              {err && (
                <Alert status="danger">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Description>{err}</Alert.Description>
                  </Alert.Content>
                </Alert>
              )}
            </Form>
          </ModalBody>
          <ModalFooter>
            <Button variant="ghost" onPress={() => props.onClose()}>取消</Button>
            <Button type="submit" form="instance-form" variant="primary">保存</Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
    </ModalBackdrop>
  );
}

export function StopDialog(props: {
  open: boolean;
  inst: Instance | null;
  onClose: () => void;
  onStop: (mode: string) => void;
}) {
  return (
    <ModalBackdrop isOpen={props.open} isDismissable onOpenChange={(o) => !o && props.onClose()}>
      <ModalContainer placement="center">
        <ModalDialog className="sm:max-w-md">
          <ModalCloseTrigger />
          <ModalHeader>
            <ModalHeading>停止实例</ModalHeading>
          </ModalHeader>
          <ModalBody className="p-6 flex flex-col gap-3">
            <Paragraph size="xs" color="muted">实例：{props.inst?.name}（{props.inst?.instance_id}）</Paragraph>
            <Button variant="primary" fullWidth onPress={() => props.onStop("StopCharging")}>节省停机（不计规格费）</Button>
            <Button variant="secondary" fullWidth onPress={() => props.onStop("Stop")}>普通停机（继续计费）</Button>
          </ModalBody>
          <ModalFooter className="p-6 pt-0">
            <Button variant="ghost" onPress={() => props.onClose()}>取消</Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
    </ModalBackdrop>
  );
}

export function ConfirmModal(props: {
  open: boolean; title: string; message: string; onClose: () => void; onConfirm: () => void; danger?: boolean;
}) {
  return (
    <ModalBackdrop isOpen={props.open} isDismissable onOpenChange={(o) => !o && props.onClose()}>
      <ModalContainer placement="center">
        <ModalDialog className="sm:max-w-md">
          <ModalCloseTrigger />
          <ModalHeader>
            <ModalHeading>{props.title}</ModalHeading>
          </ModalHeader>
          <ModalBody className="p-6">
            <Paragraph size="sm" color="muted">{props.message}</Paragraph>
          </ModalBody>
          <ModalFooter className="p-6 pt-0">
            <Button variant="ghost" onPress={() => props.onClose()}>取消</Button>
            <Button variant={props.danger ? "danger" : "primary"} onPress={() => { props.onConfirm(); props.onClose(); }}>
              确认
            </Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
    </ModalBackdrop>
  );
}

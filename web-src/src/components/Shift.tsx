import { useState } from "react";
import {
  Alert, Button, Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle,
  Checkbox, Chip, Code, EmptyState, FieldError, Form, Input, Label, ListBox,
  ModalBackdrop, ModalBody, ModalCloseTrigger, ModalContainer, ModalDialog, ModalFooter, ModalHeader, ModalHeading,
  Paragraph, Select, Separator, Surface, TextField, Typography,
} from "@heroui/react";
import type { Group, Instance } from "../api";
import { PHASE_TEXT, statusTone } from "../hooks";

function phaseChip(phase?: string) {
  return <Chip size="sm" variant="soft" color={statusTone(phase)}>{PHASE_TEXT[phase || ""] || phase || "—"}</Chip>;
}

export default function Shift(props: {
  groups: Group[];
  busy: string | null;
  onSwitch: (g: Group) => void;
  onEdit: (g: Group) => void;
  onDissolve: (g: Group) => void;
}) {
  if (!props.groups.length) {
    return (
      <Card>
        <CardContent>
          <EmptyState className="py-10 text-center text-default-500">
            <Paragraph size="sm" color="muted">还没有换班组</Paragraph>
          </EmptyState>
        </CardContent>
      </Card>
    );
  }
  return (
    <Surface className="grid grid-cols-1 xl:grid-cols-2 gap-3 bg-transparent p-0">
      {props.groups.map((g) => (
        <Card key={g.id} className={props.busy === g.id ? "opacity-60 pointer-events-none" : ""}>
          <CardHeader className="flex flex-row items-center justify-between flex-wrap gap-2">
            <CardTitle className="text-[15px] font-bold">{g.name}</CardTitle>
            {phaseChip(g.phase || g.run?.phase)}
          </CardHeader>
          <CardContent className="gap-2.5">
            <CardDescription className="text-xs">
              {g.cycle === "month" ? "按天轮换（月）" : "按小时轮换"}｜故障转移{g.failover_enabled ? "开" : "关"}
            </CardDescription>
            <Surface className="flex flex-col gap-1.5 bg-transparent p-0">
              {(g.members_view || []).map((m) => (
                <Surface key={m.instance_id} variant="secondary" className="rounded-lg border border-default-200/60 px-3 py-1.5 flex items-center justify-between">
                  <Typography type="body-sm">
                    {m.duty ? "🟢" : "⚪"} {m.name}（{m.status || "未知"}）
                  </Typography>
                  <Code className="num text-xs">
                    流量 {m.traffic_gb != null ? m.traffic_gb.toFixed(1) + "GB" : "—"}
                  </Code>
                </Surface>
              ))}
            </Surface>
          </CardContent>
          <CardFooter className="flex flex-row flex-wrap gap-1.5 pt-0.5">
            <Button size="sm" variant="primary" onPress={() => props.onSwitch(g)}>⇄ 手动换班</Button>
            <Button size="sm" variant="secondary" onPress={() => props.onEdit(g)}>✎ 编辑</Button>
            <Button size="sm" variant="ghost" onPress={() => props.onDissolve(g)}>✕ 解散</Button>
          </CardFooter>
        </Card>
      ))}
    </Surface>
  );
}

export function GroupDialog(props: {
  open: boolean;
  editing: Group | null;
  instances: Instance[];
  onClose: () => void;
  onSave: (body: Record<string, unknown>, id?: string) => Promise<string | null>;
}) {
  const [form, setForm] = useState<{ name: string; cycle: string; failover: boolean; members: Array<{ instance_id: string; hours: number; days: number; start: string; end: string }> }>({
    name: "", cycle: "day", failover: true, members: [],
  });
  const [lastKey, setLastKey] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [err, setErr] = useState("");
  const key = props.editing?.id || "new";
  if (props.open && key !== lastKey) {
    setLastKey(key);
    setForm({
      name: props.editing?.name || "", cycle: props.editing?.cycle || "day",
      failover: props.editing?.failover_enabled !== false,
      members: (props.editing?.members || [])
        .filter((m) => props.instances.some((v) => v.id === m.instance_id))
        .map((m) => ({
          instance_id: m.instance_id, hours: m.hours || 12, days: m.days ?? 1,
          start: m.start_time || "", end: m.end_time || "",
        })),
    });
    setSubmitted(false);
    setErr("");
  }
  if (!props.open && lastKey) {
    setLastKey("");
  }
  const toggleMember = (id: string) => {
    setForm((f) => {
      if (f.members.some((m) => m.instance_id === id)) {
        return { ...f, members: f.members.filter((m) => m.instance_id !== id) };
      }
      return { ...f, members: [...f.members, { instance_id: id, hours: 12, days: 1, start: "", end: "" }] };
    });
  };
  const isNameInvalid = submitted && !form.name.trim();

  return (
    <ModalBackdrop isOpen={props.open} isDismissable onOpenChange={(o) => !o && props.onClose()}>
      <ModalContainer placement="auto">
        <ModalDialog className="sm:max-w-lg">
          <ModalCloseTrigger />
          <ModalHeader>
            <ModalHeading>{props.editing ? "编辑换班组" : "新建换班组"}</ModalHeading>
          </ModalHeader>
          <ModalBody className="px-6 py-2">
            <Form id="shift-group-form" className="flex flex-col gap-3"
              onSubmit={async (e) => {
                e.preventDefault();
                setSubmitted(true);
                if (!form.name.trim()) return;
                const body = {
                  name: form.name.trim(), cycle: form.cycle, failover_enabled: form.failover,
                  members: form.members.map((m, i) => ({
                    instance_id: m.instance_id, position: i, hours: m.hours, days: m.days,
                    start_time: m.start, end_time: m.end,
                  })),
                };
                const error = await props.onSave(body, props.editing?.id);
                setErr(error || "");
              }}>
              <TextField fullWidth isInvalid={isNameInvalid}>
                <Label htmlFor="group-name">组名</Label>
                <Input
                  id="group-name"
                  fullWidth
                  placeholder="如：主力组"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
                <FieldError>请输入换班组名称</FieldError>
              </TextField>
              <Select
                fullWidth
                value={form.cycle || "day"}
                onChange={(k) => k != null && setForm({ ...form, cycle: String(k) })}
              >
                <Label>周期</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    <ListBox.Item id="day" textValue="按小时轮换">
                      按小时轮换
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                    <ListBox.Item id="month" textValue="按天轮换（月）">
                      按天轮换（月）
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  </ListBox>
                </Select.Popover>
              </Select>
              <Surface className="flex flex-col gap-2 bg-transparent p-0">
                <Label>成员（≥2 台；时刻列选填，全员填写即走时刻表排班）</Label>
                {props.instances.map((v) => {
                  const m = form.members.find((x) => x.instance_id === v.id);
                  const checked = !!m;
                  return (
                    <Surface key={v.id} variant="secondary" className="flex flex-col rounded-medium border border-default-200 px-3 py-2">
                      <Surface className="flex items-center justify-between gap-2 flex-wrap bg-transparent p-0">
                        <Checkbox isSelected={checked} onChange={() => toggleMember(v.id)}>
                          <Checkbox.Content>
                            <Checkbox.Control>
                              <Checkbox.Indicator />
                            </Checkbox.Control>
                            <Label className="text-[13px] font-semibold">{v.name}</Label>
                          </Checkbox.Content>
                        </Checkbox>
                        <Code className="text-[10.5px]">{v.instance_id}</Code>
                      </Surface>
                      {checked && (
                        <>
                          <Separator className="my-2 opacity-50" />
                          <Surface className="flex items-center justify-between gap-x-3 gap-y-1.5 flex-wrap bg-transparent p-0">
                            {!isMonth(form.cycle) && (
                              <Surface className="text-xs text-default-500 flex items-center gap-1.5 bg-transparent p-0">
                                <Typography type="body-xs" color="muted">当班</Typography>
                                <Input
                                  type="number"
                                  min={1}
                                  max={168}
                                  value={String(m!.hours)}
                                  onChange={(e) => setForm({ ...form, members: form.members.map((x) => x.instance_id === v.id ? { ...x, hours: parseInt(e.target.value, 10) || 12 } : x) })}
                                  className="w-16 px-2 py-1 text-center font-mono text-xs"
                                />
                                <Typography type="body-xs" color="muted">小时</Typography>
                              </Surface>
                            )}
                            {isMonth(form.cycle) && (
                              <Surface className="text-xs text-default-500 flex items-center gap-1.5 bg-transparent p-0">
                                <Typography type="body-xs" color="muted">当班</Typography>
                                <Input
                                  type="number"
                                  min={0}
                                  max={31}
                                  value={String(m!.days)}
                                  onChange={(e) => {
                                    const n = parseInt(e.target.value, 10);
                                    const days = Number.isNaN(n) ? 0 : Math.max(0, Math.min(31, n));
                                    setForm({ ...form, members: form.members.map((x) => x.instance_id === v.id ? { ...x, days } : x) });
                                  }}
                                  className="w-16 px-2 py-1 text-center font-mono text-xs"
                                />
                                <Typography type="body-xs" color="muted">天</Typography>
                              </Surface>
                            )}
                            {!isMonth(form.cycle) && (
                              <Surface className="text-xs text-default-500 flex items-center gap-1.5 bg-transparent p-0">
                                <Typography type="body-xs" color="muted">时刻</Typography>
                                <Input
                                  type="time"
                                  value={m!.start}
                                  onChange={(e) => setForm({ ...form, members: form.members.map((x) => x.instance_id === v.id ? { ...x, start: e.target.value } : x) })}
                                  className="w-24 px-1.5 py-1 text-xs"
                                />
                                <Typography type="body-xs" color="muted">–</Typography>
                                <Input
                                  type="time"
                                  value={m!.end}
                                  onChange={(e) => setForm({ ...form, members: form.members.map((x) => x.instance_id === v.id ? { ...x, end: e.target.value } : x) })}
                                  className="w-24 px-1.5 py-1 text-xs"
                                  title="00:00 表达到午夜"
                                />
                              </Surface>
                            )}
                          </Surface>
                        </>
                      )}
                    </Surface>
                  );
                })}
              </Surface>
              <Select
                fullWidth
                value={form.failover ? "1" : "0"}
                onChange={(k) => k != null && setForm({ ...form, failover: String(k) === "1" })}
              >
                <Label>故障转移</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    <ListBox.Item id="1" textValue="启用（超限/起不来自动换人）">
                      启用（超限/起不来自动换人）
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                    <ListBox.Item id="0" textValue="关闭（超限只停当班）">
                      关闭（超限只停当班）
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  </ListBox>
                </Select.Popover>
              </Select>
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
            <Button type="submit" form="shift-group-form" variant="primary">保存</Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
    </ModalBackdrop>
  );
}

function isMonth(cycle: string) { return cycle === "month"; }

import { useState } from "react";
import {
  Button, Card, CardContent, CardHeader, CardTitle,
  DrawerBackdrop, DrawerBody, DrawerCloseTrigger, DrawerContent, DrawerDialog, DrawerFooter, DrawerHeader, DrawerHeading,
  Input, Label, ListBox, Paragraph, Select, Surface, Switch, TextField, Typography,
} from "@heroui/react";

const NOTIFY_SWITCHES = [
  ["notify_on_shift", "换班"], ["notify_on_failover", "故障转移"], ["notify_on_recovery", "抢占恢复"],
  ["notify_on_breaker", "熔断"], ["notify_on_limit_stop", "超限停机"], ["notify_on_error", "换班超时"],
  ["notify_on_keepalive", "保活恢复"], ["daily_report_enabled", "每日报告"],
] as const;

export default function SettingsDrawer(props: {
  open: boolean;
  settings: Record<string, string>;
  onClose: () => void;
  onSave: (patch: Record<string, string>) => void;
  onTest: (channel: string) => void;
}) {
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [switches, setSwitches] = useState<Record<string, boolean>>({});
  const [lastOpen, setLastOpen] = useState(false);
  if (props.open && !lastOpen) {
    setLastOpen(true);
    setDraft({ ...props.settings });
    const sw: Record<string, boolean> = {};
    for (const [k] of NOTIFY_SWITCHES) sw[k] = props.settings[k] === "1";
    setSwitches(sw);
  }
  if (!props.open && lastOpen) setLastOpen(false);

  const get = (k: string) => draft[k] ?? props.settings[k] ?? "";
  const set = (k: string) => (v: string) => setDraft((d) => ({ ...d, [k]: v }));

  const textRow = (label: string, k: string, extra?: { type?: string }) => (
    <Surface className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 bg-transparent p-0">
      <Typography type="body-xs" color="muted" className="sm:w-28 shrink-0">{label}</Typography>
      <TextField fullWidth className="flex-1 min-w-0">
        <Label htmlFor={`setting-${k}`} className="sr-only">{label}</Label>
        <Input
          id={`setting-${k}`}
          fullWidth
          type={extra?.type}
          value={get(k)}
          onChange={(e) => set(k)(e.target.value)}
        />
      </TextField>
    </Surface>
  );
  const selectRow = (label: string, k: string, options: Array<[string, string]>) => (
    <Surface className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 bg-transparent p-0">
      <Typography type="body-xs" color="muted" className="sm:w-28 shrink-0">{label}</Typography>
      <Select
        aria-label={label}
        fullWidth
        className="flex-1 min-w-0"
        value={get(k) || options[0]?.[0] || ""}
        onChange={(v) => v != null && set(k)(String(v))}
      >
        <Select.Trigger>
          <Select.Value />
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            {options.map(([v, l]) => (
              <ListBox.Item key={v} id={v} textValue={l}>
                {l}
                <ListBox.ItemIndicator />
              </ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>
    </Surface>
  );

  return (
    <DrawerBackdrop isOpen={props.open} isDismissable onOpenChange={(o) => !o && props.onClose()}>
      <DrawerContent placement="right">
        <DrawerDialog className="p-0 gap-0 overflow-hidden flex flex-col h-full">
            <DrawerHeader className="shrink-0 flex flex-row items-center justify-between px-5 py-3.5 pt-[calc(0.875rem+env(safe-area-inset-top))] border-b border-default-200 bg-surface/95 backdrop-blur-md z-10">
              <DrawerHeading className="text-base font-bold m-0">设置</DrawerHeading>
              <DrawerCloseTrigger className="static inset-auto" />
            </DrawerHeader>
            <DrawerBody className="flex-1 overflow-y-auto px-5 py-4 flex flex-col gap-3">
              <Card>
                <CardHeader className="pb-1">
                  <CardTitle className="text-xs font-semibold text-default-500 tracking-wider">引擎参数</CardTitle>
                </CardHeader>
                <CardContent className="gap-2.5">
                  {textRow("对账间隔(秒)", "reconcile_interval")}
                  {textRow("账单TTL(秒)", "billing_interval")}
                  {textRow("换班超时(分)", "shift_timeout_min")}
                  {textRow("stall容忍(秒)", "duty_stall_seconds")}
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="flex flex-row items-center justify-between pb-1">
                  <CardTitle className="text-xs font-semibold text-default-500 tracking-wider">CLOUDFLARE DDNS</CardTitle>
                  <Button size="sm" variant="tertiary" onPress={() => props.onTest("ddns")}>测试</Button>
                </CardHeader>
                <CardContent className="gap-2.5">
                  {selectRow("启用", "ddns_enabled", [["0", "关"], ["1", "开"]])}
                  {selectRow("认证方式", "ddns_cf_auth_mode", [["token", "API Token（推荐）"], ["global_key", "Global Key"]])}
                  {textRow("API Token", "ddns_cf_token", { type: "password" })}
                  {textRow("邮箱(GK)", "ddns_cf_email")}
                  {textRow("Global Key", "ddns_cf_key", { type: "password" })}
                  {textRow("Zone ID", "ddns_zone_id")}
                  {textRow("TTL", "ddns_ttl")}
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="flex flex-row items-center justify-between pb-1">
                  <CardTitle className="text-xs font-semibold text-default-500 tracking-wider">TELEGRAM</CardTitle>
                  <Button size="sm" variant="tertiary" onPress={() => props.onTest("telegram")}>测试</Button>
                </CardHeader>
                <CardContent className="gap-2.5">
                  {selectRow("启用", "notify_tg_enabled", [["0", "关"], ["1", "开"]])}
                  {textRow("Bot Token", "notify_tg_token", { type: "password" })}
                  {textRow("Chat ID", "notify_tg_chat_id")}
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="flex flex-row items-center justify-between pb-1">
                  <CardTitle className="text-xs font-semibold text-default-500 tracking-wider">WEBHOOK</CardTitle>
                  <Button size="sm" variant="tertiary" onPress={() => props.onTest("webhook")}>测试</Button>
                </CardHeader>
                <CardContent className="gap-2.5">
                  {selectRow("启用", "notify_wh_enabled", [["0", "关"], ["1", "开"]])}
                  {textRow("URL", "notify_wh_url")}
                  {textRow("Body模板", "notify_wh_body")}
                  <Paragraph size="xs" color="muted">模板变量：{`{title} {text} {kind}`}</Paragraph>
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="pb-1">
                  <CardTitle className="text-xs font-semibold text-default-500 tracking-wider">通知开关</CardTitle>
                </CardHeader>
                <CardContent className="gap-2.5">
                  <Surface className="flex flex-wrap gap-x-4 gap-y-2.5 bg-transparent p-0">
                    {NOTIFY_SWITCHES.map(([k, label]) => (
                      <Switch
                        key={k}
                        size="sm"
                        isSelected={switches[k] ?? false}
                        onChange={(checked) => setSwitches((sw) => ({ ...sw, [k]: checked }))}
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                          <Label className="text-[13px]">{label}</Label>
                        </Switch.Content>
                      </Switch>
                    ))}
                  </Surface>
                </CardContent>
              </Card>
            </DrawerBody>
            <DrawerFooter className="shrink-0 px-5 py-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))] border-t border-default-200 bg-surface z-10">
              <Button variant="primary" fullWidth onPress={() => {
                const patch: Record<string, string> = { ...draft };
                for (const [k] of NOTIFY_SWITCHES) patch[k] = switches[k] ? "1" : "0";
                props.onSave(patch);
              }}>保存设置</Button>
            </DrawerFooter>
        </DrawerDialog>
      </DrawerContent>
    </DrawerBackdrop>
  );
}

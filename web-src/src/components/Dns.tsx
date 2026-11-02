import { useState } from "react";
import {
  Button, Card, CardContent, CardFooter, CardHeader, CardTitle, Chip, Code, EmptyState,
  Input, Label, ListBox, Paragraph, Select, Surface,
  TableBody, TableCell, TableColumn, TableContent, TableHeader, TableRoot, TableRow, TableScrollContainer, TextField, Typography,
} from "@heroui/react";
import type { DnsRecord, Group, Instance } from "../api";

const STATE_TEXT: Record<string, string> = {
  unbound: "⏸ 未绑定", idle: "⏸ 目标未运行", pending: "🟡 待同步", synced: "✅ 已同步", error: "❌ 异常",
};

export default function Dns(props: {
  records: DnsRecord[];
  instances: Instance[];
  groups?: Group[];
  names: string[];
  onNamesSave: (names: string[]) => void;
  onBind: (record: string, kind: string, targetId: string) => void;
  onDelete: (record: string) => void;
  onSyncOne: (record: string) => void;
  onSyncAll: () => void;
  onTest: () => void;
}) {
  const safeNames = Array.isArray(props.names) ? props.names : [];
  const safeRecords = Array.isArray(props.records) ? props.records : [];
  const safeInstances = Array.isArray(props.instances) ? props.instances : [];
  const safeGroups = Array.isArray(props.groups) ? props.groups : [];

  const namesKey = safeNames.join(",");
  const [lastKey, setLastKey] = useState(namesKey);
  const [namesInput, setNamesInput] = useState(() => safeNames.join(", "));
  if (namesKey !== lastKey) {
    setLastKey(namesKey);
    setNamesInput(safeNames.join(", "));
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between flex-wrap gap-2">
        <CardTitle className="text-sm font-bold">Cloudflare DDNS</CardTitle>
        <Surface className="flex gap-2 bg-transparent p-0">
          <Button size="sm" variant="secondary" onPress={props.onSyncAll}>⟳ 全部同步</Button>
          <Button size="sm" variant="tertiary" onPress={props.onTest}>连通测试</Button>
        </Surface>
      </CardHeader>
      <CardContent className="gap-3">
        <Surface className="flex items-center gap-2 flex-wrap bg-transparent p-0">
          <Typography type="body-xs" color="muted" className="w-full sm:w-auto">登记记录名</Typography>
          <TextField
            fullWidth
            className="flex-1 min-w-0 sm:min-w-56"
          >
            <Label htmlFor="dns-records" className="sr-only">登记记录名</Label>
            <Input
              id="dns-records"
              fullWidth
              className="font-mono"
              placeholder="例如：a.example.com, b.example.com"
              value={namesInput}
              onChange={(e) => setNamesInput(e.target.value)}
            />
          </TextField>
          <Button size="sm" variant="primary" onPress={() => {
            props.onNamesSave(namesInput.split(/[,，\s]+/).map((x) => x.trim()).filter(Boolean));
          }}>保存</Button>
        </Surface>

        {/* 移动端卡片视图 */}
        <Surface className="md:hidden flex flex-col gap-2.5 bg-transparent p-0">
          {safeRecords.length === 0 ? (
            <EmptyState className="py-6 text-xs text-default-500">暂无 DNS 记录</EmptyState>
          ) : (
            safeRecords.map((r) => {
              const selectedVal = r.target_kind && r.target_id ? `${r.target_kind}:${r.target_id}` : "none";
              return (
                <Surface key={r.record} variant="secondary" className="rounded-xl border border-default-200 p-3 flex flex-col gap-2">
                  <Surface className="flex items-center justify-between gap-2 flex-wrap bg-transparent p-0">
                    <Code className="num text-xs font-bold break-all">{r.record}</Code>
                    <Typography type="body-xs">{STATE_TEXT[r.state || ""] || r.state}</Typography>
                  </Surface>
                  <Surface className="flex items-center justify-between gap-2 text-xs bg-transparent p-0">
                    <Typography type="body-xs" color="muted">
                      {r.target_kind ? `${r.target_kind === "server" ? "机器" : "组"}：${r.target_name || ""}` : "未绑定目标"}
                    </Typography>
                    <Code className="num text-xs">{r.last_ip || "—"}</Code>
                  </Surface>
                  <Surface className="flex items-center gap-1.5 pt-0.5 bg-transparent p-0">
                    <Select
                      aria-label={`绑定目标 ${r.record}`}
                      className="flex-1 min-w-0"
                      value={selectedVal}
                      onChange={(val) => {
                        if (!val || val === "none") {
                          props.onBind(r.record, "", "");
                        } else {
                          const [kind, id] = String(val).split(":");
                          props.onBind(r.record, kind || "", id || "");
                        }
                      }}
                    >
                      <Select.Trigger className="min-h-8 py-1 text-xs">
                        <Select.Value />
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox>
                          <ListBox.Item id="none" textValue="解绑">
                            解绑
                            <ListBox.ItemIndicator />
                          </ListBox.Item>
                          {safeGroups.map((g) => (
                            <ListBox.Item key={`group:${g.id}`} id={`group:${g.id}`} textValue={`→ 组：${g.name}`}>
                              → 组：{g.name}
                              <ListBox.ItemIndicator />
                            </ListBox.Item>
                          ))}
                          {safeInstances.map((v) => (
                            <ListBox.Item key={`server:${v.id}`} id={`server:${v.id}`} textValue={`→ 机器：${v.name}`}>
                              → 机器：{v.name}
                              <ListBox.ItemIndicator />
                            </ListBox.Item>
                          ))}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                    {r.target_kind && (
                      <Button size="sm" variant="tertiary" onPress={() => props.onSyncOne(r.record)}>同步</Button>
                    )}
                    <Button size="sm" variant="ghost" onPress={() => props.onDelete(r.record)}>✕</Button>
                  </Surface>
                </Surface>
              );
            })
          )}
        </Surface>

        {/* 桌面端表格视图 */}
        <TableRoot className="hidden md:grid">
          <TableScrollContainer>
            <TableContent aria-label="DNS 记录">
              <TableHeader>
                <TableColumn isRowHeader>记录</TableColumn>
                <TableColumn>绑定目标</TableColumn>
                <TableColumn>当前解析</TableColumn>
                <TableColumn>状态</TableColumn>
                <TableColumn>操作</TableColumn>
              </TableHeader>
              <TableBody renderEmptyState={() => <EmptyState className="py-6 text-xs text-default-500">暂无 DNS 记录</EmptyState>}>
                {safeRecords.map((r) => {
                  const selectedVal = r.target_kind && r.target_id ? `${r.target_kind}:${r.target_id}` : "none";
                  return (
                    <TableRow key={r.record} id={r.record}>
                      <TableCell><Code className="num text-xs">{r.record}</Code></TableCell>
                      <TableCell>
                        <Typography type="body-sm">
                          {r.target_kind ? `${r.target_kind === "server" ? "机器" : "组"}：${r.target_name || ""}` : "—"}
                        </Typography>
                      </TableCell>
                      <TableCell><Code className="num text-xs">{r.last_ip || "—"}</Code></TableCell>
                      <TableCell><Typography type="body-sm">{STATE_TEXT[r.state || ""] || r.state}</Typography></TableCell>
                      <TableCell>
                        <Surface className="flex gap-1.5 items-center bg-transparent p-0">
                          {r.target_kind && (
                            <Button size="sm" variant="tertiary" onPress={() => props.onSyncOne(r.record)}>同步</Button>
                          )}
                          <Select
                            aria-label={`绑定目标 ${r.record}`}
                            className="w-44"
                            value={selectedVal}
                            onChange={(val) => {
                              if (!val || val === "none") {
                                props.onBind(r.record, "", "");
                              } else {
                                const [kind, id] = String(val).split(":");
                                props.onBind(r.record, kind || "", id || "");
                              }
                            }}
                          >
                            <Select.Trigger className="min-h-8 py-1 text-xs">
                              <Select.Value />
                              <Select.Indicator />
                            </Select.Trigger>
                            <Select.Popover>
                              <ListBox>
                                <ListBox.Item id="none" textValue="解绑">
                                  解绑
                                  <ListBox.ItemIndicator />
                                </ListBox.Item>
                                {safeGroups.map((g) => (
                                  <ListBox.Item key={`group:${g.id}`} id={`group:${g.id}`} textValue={`→ 组：${g.name}`}>
                                    → 组：{g.name}
                                    <ListBox.ItemIndicator />
                                  </ListBox.Item>
                                ))}
                                {safeInstances.map((v) => (
                                  <ListBox.Item key={`server:${v.id}`} id={`server:${v.id}`} textValue={`→ 机器：${v.name}`}>
                                    → 机器：{v.name}
                                    <ListBox.ItemIndicator />
                                  </ListBox.Item>
                                ))}
                              </ListBox>
                            </Select.Popover>
                          </Select>
                          <Button size="sm" variant="ghost" onPress={() => props.onDelete(r.record)}>✕</Button>
                        </Surface>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </TableContent>
          </TableScrollContainer>
        </TableRoot>
      </CardContent>
      <CardFooter className="pt-0">
        <Paragraph size="xs" color="muted">绑定必须是显式的：未绑定的记录不会被自动同步。A 记录需预先在 Cloudflare 创建；删除仅解除管理。</Paragraph>
      </CardFooter>
    </Card>
  );
}

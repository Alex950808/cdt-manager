import { useState } from "react";
import {
  Button, Card, CardContent, CardFooter, CardHeader, CardTitle, Chip, Code, EmptyState,
  ListBox, Paragraph, Select, Surface,
  TableBody, TableCell, TableColumn, TableContent, TableHeader, TableRoot, TableRow, TableScrollContainer, Typography,
} from "@heroui/react";
import type { Instance, Keepalive } from "../api";
import { statusTone } from "../hooks";

export default function Keepalive(props: {
  keepalives: Keepalive[];
  instances: Instance[];
  onAdd: (instanceId: string) => void;
  onRemove: (instanceId: string) => void;
}) {
  const safeInstances = Array.isArray(props.instances) ? props.instances : [];
  const safeKeepalives = Array.isArray(props.keepalives) ? props.keepalives : [];
  const available = safeInstances.filter((v) => !safeKeepalives.some((k) => k.instance_id === v.id));
  const [pickedId, setPickedId] = useState<string>("");
  const effectivePick = available.some((v) => v.id === pickedId) ? pickedId : (available[0]?.id || "");

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between flex-wrap gap-2">
        <CardTitle className="text-sm font-bold">单机保活 · 停了就自动拉起</CardTitle>
        {available.length > 0 && (
          <Surface className="flex gap-2 items-center w-full sm:w-auto bg-transparent p-0">
            <Select
              aria-label="选择保活实例"
              className="flex-1 min-w-0 sm:w-60"
              value={effectivePick}
              onChange={(k) => k != null && setPickedId(String(k))}
            >
              <Select.Trigger>
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {available.map((v) => (
                    <ListBox.Item key={v.id} id={v.id} textValue={`${v.name}（${v.instance_id}）`}>
                      {v.name}（{v.instance_id}）
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
            <Button size="sm" variant="primary" onPress={() => {
              if (effectivePick) props.onAdd(effectivePick);
            }}>启用</Button>
          </Surface>
        )}
      </CardHeader>
      <CardContent className="gap-3">
        {/* 移动端卡片视图 */}
        <Surface className="md:hidden flex flex-col gap-2 bg-transparent p-0">
          {safeKeepalives.length === 0 ? (
            <EmptyState className="py-6 text-xs text-default-500">暂无保活实例</EmptyState>
          ) : (
            safeKeepalives.map((k) => (
              <Surface key={k.instance_id} variant="secondary" className="rounded-xl border border-default-200 px-3 py-2.5 flex items-center justify-between gap-2 flex-wrap">
                <Surface className="flex items-center gap-2 min-w-0 bg-transparent p-0">
                  <Typography type="body-sm" weight="semibold" className="truncate">{k.name}</Typography>
                  <Chip size="sm" variant="soft" color={statusTone(k.status)}>{k.status || "未知"}</Chip>
                  <Code className="num text-xs">重试 {k.tries || 0}</Code>
                </Surface>
                <Button size="sm" variant="ghost" onPress={() => props.onRemove(k.instance_id)}>停用</Button>
              </Surface>
            ))
          )}
        </Surface>

        {/* 桌面端表格视图 */}
        <TableRoot className="hidden md:grid">
          <TableScrollContainer>
            <TableContent aria-label="保活列表">
              <TableHeader>
                <TableColumn isRowHeader>实例</TableColumn>
                <TableColumn>状态</TableColumn>
                <TableColumn>重试</TableColumn>
                <TableColumn>操作</TableColumn>
              </TableHeader>
              <TableBody renderEmptyState={() => <EmptyState className="py-6 text-xs text-default-500">暂无保活实例</EmptyState>}>
                {safeKeepalives.map((k) => (
                  <TableRow key={k.instance_id} id={k.instance_id}>
                    <TableCell><Typography type="body-sm">{k.name}</Typography></TableCell>
                    <TableCell>
                      <Chip size="sm" variant="soft" color={statusTone(k.status)}>{k.status || "未知"}</Chip>
                    </TableCell>
                    <TableCell><Code className="num text-xs">{k.tries || 0}</Code></TableCell>
                    <TableCell>
                      <Button size="sm" variant="ghost" onPress={() => props.onRemove(k.instance_id)}>停用</Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </TableContent>
          </TableScrollContainer>
        </TableRoot>
      </CardContent>
      <CardFooter className="pt-0">
        <Paragraph size="xs" color="muted">加入启用中的换班组（≥2 成员）后，保活自动让位。</Paragraph>
      </CardFooter>
    </Card>
  );
}

import { useState } from "react";
import {
  Button, Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle,
  Chip, Code, FieldError, Form, Input, Label, Separator, Surface, TextField, Typography,
} from "@heroui/react";
import type { ThemeMode } from "../hooks";

type LoginForm = "setup" | "account" | "static";

export default function LoginPage(props: {
  form: LoginForm;
  theme?: ThemeMode;
  onToggleTheme?: () => void;
  onSubmit: (payload: { username?: string; password?: string; token?: string }) => Promise<string | null>;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [serverErr, setServerErr] = useState("");
  const [busy, setBusy] = useState(false);

  const isUsernameInvalid =
    props.form !== "static" && submitted && username.trim().length === 0;
  const isPasswordInvalid =
    (submitted && password.length === 0) || !!serverErr;

  const passwordErrorText = serverErr
    ? serverErr
    : props.form === "static"
      ? "请输入访问口令"
      : "请输入密码";

  const subtitle =
    props.form === "setup" ? "首次使用 · 初始化管理员账号"
      : props.form === "static" ? "静态访问口令认证" : "登录以管理阿里云抢占式实例与换班调度";

  const modeBadge =
    props.form === "setup" ? "初始化向导"
      : props.form === "static" ? "口令保护" : "管理员登录";

  return (
    <Surface className="min-h-screen min-h-[100dvh] w-full flex flex-col justify-center items-center relative z-10 px-4 py-6 pt-[calc(1.5rem+env(safe-area-inset-top))] pb-[calc(1.5rem+env(safe-area-inset-bottom))] pl-[calc(1rem+env(safe-area-inset-left))] pr-[calc(1rem+env(safe-area-inset-right))] box-border bg-transparent">
      {props.onToggleTheme && (
        <aside className="fixed top-0 right-0 z-20 pt-[calc(0.75rem+env(safe-area-inset-top))] pr-[calc(1rem+env(safe-area-inset-right))] hidden sm:block">
          <Button
            size="sm"
            variant="secondary"
            onPress={props.onToggleTheme}
            className="h-8 px-3 text-xs shadow-md bg-surface/80 backdrop-blur-md border border-default-200/80"
          >
            {props.theme === "dark" ? "☀ 日间" : "☾ 夜间"}
          </Button>
        </aside>
      )}
      <Card className="w-full max-w-[400px] border border-default-200/80 bg-surface/85 backdrop-blur-2xl shadow-2xl shadow-black/20 p-2">
        <CardHeader className="flex flex-col items-start gap-3 px-5 pt-5 pb-3">
          <Surface className="w-full flex items-center justify-between bg-transparent p-0">
            <Surface className="flex items-center gap-3 bg-transparent p-0">
              <Surface className="w-10 h-10 rounded-xl grid place-items-center text-lg font-bold text-[#06121a] bg-linear-to-br from-cyan-400 via-sky-400 to-violet-400 shadow-md shadow-cyan-500/20 font-mono shrink-0 p-0">
                ⇄
              </Surface>
              <Surface className="flex flex-col bg-transparent p-0">
                <CardTitle className="text-base font-bold tracking-tight leading-snug">CDT 管理台</CardTitle>
                <Typography type="body-xs" color="muted" className="text-[10px] tracking-widest uppercase font-mono">
                  ALIYUN SPOT OPS
                </Typography>
              </Surface>
            </Surface>
            <Surface className="flex items-center gap-2 bg-transparent p-0">
              <Chip size="sm" variant="soft" color="accent" className="text-[11px]">
                {modeBadge}
              </Chip>
              {props.onToggleTheme && (
                <Button
                  size="sm"
                  variant="tertiary"
                  onPress={props.onToggleTheme}
                  aria-label="切换日间/夜间模式"
                  className="sm:hidden h-7 px-2 text-xs min-w-0"
                >
                  {props.theme === "dark" ? "☀" : "☾"}
                </Button>
              )}
            </Surface>
          </Surface>
          <CardDescription className="text-xs text-default-500">
            {subtitle}
          </CardDescription>
        </CardHeader>

        <Separator className="opacity-40 mx-5 w-auto" />

        <CardContent className="px-5 pt-4 pb-3">
          <Form
            className="flex flex-col gap-4"
            onSubmit={async (e) => {
              e.preventDefault();
              setSubmitted(true);
              if (props.form !== "static" && !username.trim()) return;
              if (!password) return;

              setBusy(true);
              setServerErr("");
              const payload = props.form === "static" ? { token: password }
                : { username: username.trim(), password };
              const error = await props.onSubmit(payload);
              if (error) {
                setServerErr(error);
              }
              setBusy(false);
            }}
          >
            {props.form !== "static" && (
              <TextField fullWidth isInvalid={isUsernameInvalid}>
                <Label htmlFor="login-username" className="text-xs font-medium text-default-600 mb-1">
                  管理员账号
                </Label>
                <Input
                  id="login-username"
                  fullWidth
                  placeholder="请输入用户名（如 admin）"
                  autoComplete="username"
                  autoFocus
                  value={username}
                  onChange={(e) => {
                    setUsername(e.target.value);
                    if (serverErr) setServerErr("");
                  }}
                  className="h-10 rounded-xl text-sm"
                />
                <FieldError>请输入管理员账号</FieldError>
              </TextField>
            )}
            <TextField fullWidth type="password" isInvalid={isPasswordInvalid}>
              <Label htmlFor="login-password" className="text-xs font-medium text-default-600 mb-1">
                {props.form === "static" ? "访问口令" : props.form === "setup" ? "设置密码" : "登录密码"}
              </Label>
              <Input
                id="login-password"
                fullWidth
                type="password"
                placeholder={props.form === "static" ? "请输入访问口令" : "请输入密码"}
                autoComplete={props.form === "setup" ? "new-password" : "current-password"}
                autoFocus={props.form === "static"}
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  if (serverErr) setServerErr("");
                }}
                className="h-10 rounded-xl text-sm"
              />
              <FieldError>{passwordErrorText}</FieldError>
            </TextField>
            <Button
              type="submit"
              variant="primary"
              fullWidth
              isDisabled={busy}
              className="mt-1 h-10 rounded-xl font-semibold text-sm shadow-md shadow-sky-500/20"
            >
              {busy ? "正在验证…" : props.form === "setup" ? "创建管理员并登录" : "立即登录"}
            </Button>
          </Form>
        </CardContent>

        <CardFooter className="px-5 pt-1 pb-4 flex items-center justify-between">
          <Code className="text-[10.5px] text-default-400 bg-transparent px-0">cdt-manager-go · v1.0</Code>
          <Typography type="body-xs" color="muted" className="text-[11px]">
            安全会话保护
          </Typography>
        </CardFooter>
      </Card>
    </Surface>
  );
}

"use client";

import { type ReactNode, useEffect, useState } from "react";

import { Plus, Trash2 } from "lucide-react";

import { CHANNEL_FIELDS, KIND_LABEL, parseKV } from "@/app/(main)/system/notify/_components/channel-fields";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { type OrgAPIError, type OrgNotificationDraft, type OrgNotificationOptions, orgAPI } from "@/lib/org";

import type { Draft } from "./form";

// Form values stay editable; conversion happens only at the save/test boundary.
export function notificationPayload(draft: OrgNotificationDraft): OrgNotificationDraft {
  const config = { ...draft.config };
  for (const field of CHANNEL_FIELDS[draft.kind] || []) {
    const value = config[field.key];
    if (field.kind === "select" && (value === undefined || value === "")) {
      config[field.key] = field.options?.[0]?.value;
      continue;
    }
    if (value === undefined || (typeof value === "string" && value.startsWith("__masked__"))) continue;
    if (field.kind === "number") config[field.key] = Number(value);
    else if (field.kind === "kv" && typeof value === "string") config[field.key] = value.trim() ? parseKV(value) : "";
    else if (field.kind === "list" && typeof value === "string")
      config[field.key] = value.split(/[\s,，]+/).filter(Boolean);
    else if (typeof value === "string" && field.kind !== "password") config[field.key] = value.trim();
  }
  return { ...draft, name: draft.name.trim(), config };
}

export function NotificationChannels({
  kind,
  draft,
  prefix,
  change,
  errors,
  disabled,
}: {
  kind: "department" | "business_line";
  draft: Draft;
  prefix: string;
  change: (patch: Partial<Draft>) => void;
  errors: Record<string, string>;
  disabled: boolean;
}) {
  const [options, setOptions] = useState<OrgNotificationOptions>();
  const [loadError, setLoadError] = useState("");
  const [reload, setReload] = useState(0);
  const [testing, setTesting] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; message: string }>>({});
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    let active = true;
    if (reload > 0) setLoadError("");
    void orgAPI.notificationChannels().then(
      (data) => {
        if (active) {
          setOptions(data);
          setLoadError("");
        }
      },
      (error: Error) => {
        if (active) setLoadError(error.message);
      },
    );
    return () => {
      active = false;
    };
  }, [reload]);

  function update(channel: OrgNotificationDraft, patch: Partial<OrgNotificationDraft>) {
    const next = { ...channel, ...patch };
    const rest = draft.channel_drafts.filter((item) => item.client_key !== channel.client_key);
    const stored = options?.channels.find((item) => item.id === next.id);
    const payload = notificationPayload(next);
    const unchanged =
      stored &&
      payload.name === stored.name &&
      payload.kind === stored.kind &&
      payload.enabled === stored.enabled &&
      JSON.stringify(payload.config) === JSON.stringify(notificationPayload({ ...next, config: stored.config }).config);
    change({
      channel_drafts: unchanged ? rest : [...rest, next],
      shared_channel_confirmed_ids: unchanged
        ? draft.shared_channel_confirmed_ids.filter((id) => id !== next.id)
        : draft.shared_channel_confirmed_ids,
    });
    setTestResults((old) => {
      const copy = { ...old };
      delete copy[channel.client_key];
      return copy;
    });
    setLocalErrors((old) => {
      const copy = { ...old };
      for (const key of Object.keys(copy))
        if (key.startsWith(`channel_drafts.${channel.client_key}.`)) delete copy[key];
      return copy;
    });
  }

  async function test(channel: OrgNotificationDraft) {
    setTesting(channel.client_key);
    try {
      const result = await orgAPI.testNotificationChannel(notificationPayload(channel));
      setTestResults((old) => ({
        ...old,
        [channel.client_key]: {
          ok: result.ok,
          message: result.ok
            ? `${options?.test_mode === "fake" ? "本地测试通过，未发送真实通知" : "测试消息已发出"}（${result.latency_ms} ms）`
            : result.message || "测试失败，请检查配置后重试",
        },
      }));
    } catch (e) {
      const error = e as OrgAPIError;
      setTestResults((old) => ({ ...old, [channel.client_key]: { ok: false, message: error.message } }));
      setLocalErrors((old) => ({ ...old, ...error.fields }));
    } finally {
      setTesting(null);
    }
  }

  const channels: OrgNotificationDraft[] = [
    ...(options?.channels || []).map(
      (channel) =>
        draft.channel_drafts.find((item) => item.id === channel.id) || {
          client_key: `channel-${channel.id}`,
          id: channel.id,
          expected_updated_at: channel.updated_at,
          name: channel.name,
          kind: channel.kind,
          enabled: channel.enabled,
          config: channel.config,
        },
    ),
    ...draft.channel_drafts.filter((channel) => !channel.id),
  ];
  const fieldErrors = { ...errors, ...localErrors };
  return (
    <section className="space-y-3 border-t pt-5">
      <div className="flex items-center justify-between gap-3">
        <h3 className="font-medium">通知渠道</h3>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled || !options}
          onClick={() => {
            const client_key = crypto.randomUUID();
            change({
              channel_drafts: [
                ...draft.channel_drafts,
                { client_key, name: "", kind: "wecom", enabled: true, config: {} },
              ],
            });
            requestAnimationFrame(() =>
              document.getElementById(`${prefix}-channel_drafts.${client_key}.name`)?.focus(),
            );
          }}
        >
          <Plus className="size-3.5" />
          新增渠道
        </Button>
      </div>
      <p className="text-muted-foreground text-xs">勾选后配置，随本页“保存”一起生效。取消编辑不会改动渠道。</p>
      {loadError && (
        <div role="alert" className="flex items-center justify-between gap-3 text-destructive text-xs">
          <span>通知渠道读取失败：{loadError}</span>
          <Button type="button" size="sm" variant="outline" onClick={() => setReload((value) => value + 1)}>
            重试
          </Button>
        </div>
      )}
      {!options && !loadError && (
        <div role="status" className="h-20 animate-pulse rounded-lg bg-muted" aria-label="正在读取通知渠道" />
      )}
      {options && !channels.length && <p className="text-muted-foreground text-xs">还没有渠道，可直接在这里新增。</p>}
      {channels.map((channel) => {
        const channelId = channel.id ?? 0;
        const stored = options?.channels.find((item) => item.id === channel.id);
        const selected = !channel.id || draft.channel_ids.includes(channel.id);
        const changed = !channel.id || draft.channel_drafts.some((item) => item.id === channel.id);
        const base = `channel_drafts.${channel.client_key}`;
        const secretKeys =
          stored?.secret_keys || options?.kinds.find((item) => item.kind === channel.kind)?.secret_keys || [];
        const otherDepartments =
          stored?.usage.departments.filter((item) => kind !== "department" || item.id !== draft.id) || [];
        const otherLines =
          stored?.usage.business_lines.filter((item) => kind !== "business_line" || item.id !== draft.id) || [];
        const shared =
          !!stored &&
          (stored.usage.global || otherDepartments.length > 0 || otherLines.length > 0 || stored.usage.schedules > 0);
        const configError = fieldErrors[`${base}.config`];
        const testDisabled = disabled || testing !== null;
        return (
          <div key={channel.client_key} className="rounded-lg border">
            <div className="flex items-center justify-between gap-2 px-3 py-3">
              <label className="flex min-w-0 items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={selected}
                  disabled={disabled || !channel.id}
                  onChange={(event) =>
                    change({
                      channel_ids: event.target.checked
                        ? [...draft.channel_ids, channelId]
                        : draft.channel_ids.filter((id) => id !== channel.id),
                    })
                  }
                />
                <span className="truncate">{channel.name || "新渠道"}</span>
                {!channel.enabled && (
                  <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-muted-foreground text-xs">已停用</span>
                )}
                <span className="shrink-0 text-muted-foreground text-xs">
                  {KIND_LABEL[channel.kind] || channel.kind}
                </span>
              </label>
              {!channel.id && (
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  disabled={disabled}
                  aria-label="移除未保存的新渠道"
                  onClick={() =>
                    change({
                      channel_drafts: draft.channel_drafts.filter((item) => item.client_key !== channel.client_key),
                    })
                  }
                >
                  <Trash2 className="size-3.5" />
                </Button>
              )}
            </div>
            {selected && (
              <div className="space-y-4 border-t px-3 py-4">
                <div className="grid gap-3 sm:grid-cols-[1fr_140px]">
                  <div className="space-y-2">
                    <Label htmlFor={`${prefix}-${base}.name`}>渠道名称</Label>
                    <Input
                      id={`${prefix}-${base}.name`}
                      value={channel.name}
                      maxLength={100}
                      disabled={disabled}
                      aria-invalid={!!fieldErrors[`${base}.name`]}
                      onChange={(event) => update(channel, { name: event.target.value })}
                      onBlur={() =>
                        setLocalErrors((old) => ({
                          ...old,
                          [`${base}.name`]: channel.name.trim() ? "" : "请填写便于识别的渠道名称",
                        }))
                      }
                    />
                    {fieldErrors[`${base}.name`] && (
                      <p role="alert" className="text-destructive text-xs">
                        {fieldErrors[`${base}.name`]}
                      </p>
                    )}
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor={`${prefix}-${base}.kind`}>渠道类型</Label>
                    <select
                      id={`${prefix}-${base}.kind`}
                      className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                      value={channel.kind}
                      aria-invalid={!!fieldErrors[`${base}.kind`]}
                      disabled={disabled || !!channel.id}
                      onChange={(event) => update(channel, { kind: event.target.value, config: {} })}
                    >
                      {options?.kinds.map((item) => (
                        <option key={item.kind} value={item.kind}>
                          {KIND_LABEL[item.kind] || item.kind}
                        </option>
                      ))}
                    </select>
                    {fieldErrors[`${base}.kind`] && (
                      <p role="alert" className="text-destructive text-xs">
                        {fieldErrors[`${base}.kind`]}
                      </p>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Switch
                    id={`${prefix}-${base}.enabled`}
                    checked={channel.enabled}
                    disabled={disabled}
                    onCheckedChange={(enabled) => update(channel, { enabled })}
                  />
                  <Label htmlFor={`${prefix}-${base}.enabled`}>启用此渠道</Label>
                </div>
                <div id={`${prefix}-${base}.config`} tabIndex={-1} className="space-y-4">
                  {(CHANNEL_FIELDS[channel.kind] || []).map((field) => {
                    const id = `${prefix}-${base}.config.${field.key}`;
                    const value = channel.config[field.key];
                    const secret = secretKeys.includes(field.key);
                    const masked = typeof value === "string" && value.startsWith("__masked__");
                    let text = value == null ? "" : String(value);
                    if (Array.isArray(value)) text = value.join(", ");
                    else if (typeof value === "object" && value && field.kind === "kv")
                      text = Object.entries(value)
                        .map(([key, item]) => `${key}=${item}`)
                        .join("\n");
                    if (masked) text = "";
                    const setValue = (next: unknown) =>
                      update(channel, { config: { ...channel.config, [field.key]: next } });
                    let control: ReactNode;
                    let inputType = field.kind === "number" ? "number" : "text";
                    if (secret || field.kind === "password") inputType = "password";
                    if (field.kind === "switch")
                      control = (
                        <Switch id={id} checked={value === true} disabled={disabled} onCheckedChange={setValue} />
                      );
                    else if (field.kind === "select")
                      control = (
                        <select
                          id={id}
                          value={text || field.options?.[0]?.value}
                          disabled={disabled}
                          className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                          onChange={(event) => setValue(event.target.value)}
                        >
                          {field.options?.map((item) => (
                            <option key={item.value} value={item.value}>
                              {item.label}
                            </option>
                          ))}
                        </select>
                      );
                    else if (field.kind === "textarea" || field.kind === "kv")
                      control = (
                        <Textarea
                          id={id}
                          value={text}
                          disabled={disabled}
                          placeholder={masked ? "已保存；填入新值可替换" : field.placeholder}
                          onChange={(event) => setValue(event.target.value)}
                        />
                      );
                    else
                      control = (
                        <Input
                          id={id}
                          value={text}
                          disabled={disabled}
                          type={inputType}
                          autoComplete={secret ? "new-password" : undefined}
                          placeholder={masked ? "已保存；填入新值可替换" : field.placeholder}
                          onChange={(event) => setValue(event.target.value)}
                        />
                      );
                    return (
                      <div key={field.key} className="space-y-2">
                        <div className="flex items-center justify-between gap-2">
                          <Label htmlFor={id}>{field.label}</Label>
                          {masked && (
                            <Button
                              type="button"
                              size="sm"
                              variant="ghost"
                              disabled={disabled}
                              onClick={() => setValue("")}
                            >
                              清除已保存值
                            </Button>
                          )}
                        </div>
                        {control}
                        {masked ? (
                          <p className="text-muted-foreground text-xs">已保存的凭据不会显示。未修改时保留原值。</p>
                        ) : (
                          field.help && <p className="text-muted-foreground text-xs">{field.help}</p>
                        )}
                        {channel.kind === "email" && field.key === "to" && (
                          <p className="text-muted-foreground text-xs">
                            {!stored || stored.org_managed
                              ? "这里填写测试收件人；业务通知按当前负责人和关联研发组发送。"
                              : "这里填写通用推送及测试收件人；部门报告另按当前负责人和关联研发组发送。"}
                          </p>
                        )}
                      </div>
                    );
                  })}
                  {configError && (
                    <p role="alert" className="text-destructive text-xs">
                      {configError}
                    </p>
                  )}
                </div>
                {shared && changed && channel.id && (
                  <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-xs">
                    <p className="font-medium">这是共享渠道，保存会同时修改以下使用范围：</p>
                    {stored?.usage.global && <p>该渠道的通用漏洞推送</p>}
                    {!!otherDepartments.length && <p>部门：{otherDepartments.map((item) => item.name).join("、")}</p>}
                    {!!otherLines.length && <p>业务线：{otherLines.map((item) => item.name).join("、")}</p>}
                    {!!stored?.usage.schedules && <p>{stored.usage.schedules} 个报告计划</p>}
                    <label className="flex items-start gap-2">
                      <input
                        id={`${prefix}-${base}.shared`}
                        type="checkbox"
                        checked={draft.shared_channel_confirmed_ids.includes(channel.id)}
                        disabled={disabled}
                        onChange={(event) =>
                          change({
                            shared_channel_confirmed_ids: event.target.checked
                              ? [...draft.shared_channel_confirmed_ids, channelId]
                              : draft.shared_channel_confirmed_ids.filter((id) => id !== channel.id),
                          })
                        }
                      />
                      我已确认以上共享影响，修改原渠道
                    </label>
                    {fieldErrors[`${base}.shared`] && (
                      <p role="alert" className="text-destructive">
                        {fieldErrors[`${base}.shared`]}
                      </p>
                    )}
                  </div>
                )}
                <div className="space-y-2 border-t pt-3">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={testDisabled}
                    onClick={() => void test(channel)}
                  >
                    {testing === channel.client_key ? "测试中…" : "发送测试消息"}
                  </Button>
                  {options?.test_mode === "fake" && (
                    <p className="text-muted-foreground text-xs">
                      本地预览为模拟测试，不会对外发送消息，也不会保存配置。
                    </p>
                  )}
                  {options?.test_mode === "live" && (
                    <p className="text-muted-foreground text-xs">按当前填写的地址发送测试消息；不会保存配置。</p>
                  )}
                  {testResults[channel.client_key] && (
                    <p
                      role="status"
                      className={`text-xs ${testResults[channel.client_key].ok ? "text-emerald-700" : "text-destructive"}`}
                    >
                      {testResults[channel.client_key].message}
                    </p>
                  )}
                </div>
              </div>
            )}
          </div>
        );
      })}
      {fieldErrors.channel_ids && (
        <p role="alert" className="text-destructive text-xs">
          {fieldErrors.channel_ids}
        </p>
      )}
    </section>
  );
}

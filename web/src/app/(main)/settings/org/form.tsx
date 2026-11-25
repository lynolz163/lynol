"use client";

import { useState } from "react";

import { Check, ChevronsUpDown, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import type { OrgDirectory, OrgKind, OrgNotificationDraft, OrgSLA } from "@/lib/org";

import { NotificationChannels } from "./notification-channels";

export type EditKind = Exclude<OrgKind, "fallback">;
export const kindNames: Record<EditKind, string> = {
  department: "部门",
  business_line: "业务线",
  person: "人员",
  dev_group: "研发组",
};
export interface Draft {
  id: number;
  revision: number;
  name: string;
  code: string;
  email: string;
  department_id: number | null;
  dev_group_id: number | null;
  head_id: number | null;
  primary_id: number | null;
  backup_id: number | null;
  group_ids: number[];
  channel_ids: number[];
  channel_drafts: OrgNotificationDraft[];
  shared_channel_confirmed_ids: number[];
  sla: OrgSLA | null;
  active: boolean;
}
export function createDraft(data: Partial<Draft> = {}): Draft {
  return {
    id: 0,
    revision: 0,
    name: "",
    code: "",
    email: "",
    department_id: null,
    dev_group_id: null,
    head_id: null,
    primary_id: null,
    backup_id: null,
    group_ids: [],
    channel_ids: [],
    channel_drafts: [],
    shared_channel_confirmed_ids: [],
    sla: null,
    active: true,
    ...data,
  };
}
export function automaticCode(name: string, kind: EditKind) {
  const ascii = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  return (
    ascii ||
    (name.trim()
      ? `${kind === "department" ? "dept" : "line"}-${Array.from(name.trim())
          .slice(0, 12)
          .map((c) => c.codePointAt(0)!.toString(36))
          .join("")}`
      : "")
  );
}
export function validateDraft(kind: EditKind, d: Draft): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!d.name.trim()) errors.name = `请填写${kind === "person" ? "姓名" : `${kindNames[kind]}名称`}`;
  if ((kind === "department" || kind === "business_line") && !/^[a-zA-Z0-9_-]+$/.test(d.code))
    errors.code = "请使用英文字母、数字、短横线或下划线填写编码";
  if ((kind === "business_line" || kind === "dev_group") && !d.department_id)
    errors.department_id = "请选择所属部门，也可以就地新增部门";
  if (kind === "person" && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(d.email.trim()))
    errors.email = "请填写完整邮箱，例如 name@example.com";
  if (kind === "business_line") {
    if (d.primary_id && d.primary_id === d.backup_id) errors.backup_id = "备选负责人需要与主负责人不同";
    if (d.sla) {
      const values = [d.sla.critical, d.sla.high, d.sla.medium, d.sla.low];
      if (values.some((n) => !Number.isInteger(n) || n < 1 || n > 365)) errors.sla = "各等级请填写 1～365 的整数天数";
      else if (values.some((n, i) => i > 0 && n < values[i - 1])) errors.sla = "请按严重 ≤ 高危 ≤ 中危 ≤ 低危设置天数";
    }
  }
  for (const channel of d.channel_drafts) {
    if (channel.id && !d.channel_ids.includes(channel.id)) continue;
    if (!channel.name.trim()) errors[`channel_drafts.${channel.client_key}.name`] = "请填写便于识别的渠道名称";
  }
  return errors;
}

export interface ChoiceOption {
  id: number;
  name: string;
  detail?: string;
}
export function SearchChoice({
  id,
  label,
  value,
  options,
  onChange,
  onAdd,
  addLabel,
  multiple = false,
  disabled = false,
  placeholder = "暂不设置",
  invalid = false,
}: {
  id?: string;
  label: string;
  value: number[];
  options: ChoiceOption[];
  onChange: (ids: number[]) => void;
  onAdd?: () => void;
  addLabel?: string;
  multiple?: boolean;
  disabled?: boolean;
  placeholder?: string;
  invalid?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const labels = value.map((key) => options.find((p) => p.id === key)?.name || `#${key}`);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          id={id}
          variant="outline"
          role="combobox"
          aria-label={label}
          aria-expanded={open}
          aria-invalid={invalid}
          disabled={disabled}
          className="h-auto min-h-9 w-full justify-between text-left font-normal"
        >
          <span className={labels.length ? "truncate" : "text-muted-foreground"}>
            {labels.length ? labels.join("、") : placeholder}
          </span>
          <ChevronsUpDown className="ml-2 size-3.5 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-64 p-1">
        <Command>
          <CommandInput placeholder={`搜索${label}`} aria-label={`搜索${label}`} />
          <CommandList>
            <CommandEmpty>没有匹配项</CommandEmpty>
            <CommandGroup>
              {!multiple && (
                <CommandItem
                  value="none 暂不设置"
                  onSelect={() => {
                    onChange([]);
                    setOpen(false);
                  }}
                >
                  暂不设置
                </CommandItem>
              )}
              {options.map((p) => (
                <CommandItem
                  key={p.id}
                  value={`${p.id} ${p.name} ${p.detail || ""}`}
                  onSelect={() => {
                    onChange(
                      multiple ? (value.includes(p.id) ? value.filter((i) => i !== p.id) : [...value, p.id]) : [p.id],
                    );
                    if (!multiple) setOpen(false);
                  }}
                >
                  <Check className={`size-4 ${value.includes(p.id) ? "opacity-100" : "opacity-0"}`} />
                  <span className="min-w-0">
                    <span className="block truncate">{p.name}</span>
                    {p.detail && <span className="block truncate text-muted-foreground text-xs">{p.detail}</span>}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
        {onAdd && (
          <div className="mt-1 border-t pt-1">
            <Button
              variant="ghost"
              className="w-full justify-start text-primary"
              onClick={() => {
                setOpen(false);
                onAdd();
              }}
            >
              <Plus className="size-4" />
              {addLabel ?? "新增"}
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

export function EditorFields({
  kind,
  draft,
  directory,
  errors,
  prefix,
  codeManual,
  setCodeManual,
  change,
  blur,
  onInline,
  disabled = false,
}: {
  kind: EditKind;
  draft: Draft;
  directory: OrgDirectory;
  errors: Record<string, string>;
  prefix: string;
  codeManual: boolean;
  setCodeManual: (manual: boolean) => void;
  change: (patch: Partial<Draft>) => void;
  blur: (field: string) => void;
  onInline?: (kind: EditKind, field: keyof Draft) => void;
  disabled?: boolean;
}) {
  const [otherPeople, setOtherPeople] = useState(false);
  const people = directory.people.filter(
    (p) =>
      p.active &&
      (kind === "department" || otherPeople || !draft.department_id || p.department_id === draft.department_id),
  );
  const personOptions = (selected: number | null, exclude: number | null = null) =>
    directory.people
      .filter((p) => p.id !== exclude && (people.includes(p) || p.id === selected))
      .map((p) => ({ id: p.id, name: p.name + (p.active ? "" : "（已停用）"), detail: p.email }));
  const groups = directory.groups.filter((g) => g.active && g.department_id === draft.department_id);
  const field = (key: string) => `${prefix}-${key}`;
  const error = (key: string) =>
    errors[key] ? (
      <p id={`${field(key)}-error`} role="alert" className="text-destructive text-xs">
        {errors[key]}
      </p>
    ) : null;
  const personChoice = (key: "head_id" | "primary_id" | "backup_id", label: string, exclude: number | null = null) => (
    <div className="space-y-2">
      <Label htmlFor={field(key)}>{label}</Label>
      <SearchChoice
        id={field(key)}
        label={label}
        value={draft[key] ? [draft[key]!] : []}
        options={personOptions(draft[key], exclude)}
        onChange={(ids) =>
          change({
            [key]: ids[0] || null,
            ...(key === "primary_id" && ids[0] === draft.backup_id ? { backup_id: null } : {}),
          })
        }
        onAdd={onInline ? () => onInline("person", key) : undefined}
        addLabel="新增人员"
        disabled={disabled}
        invalid={!!errors[key]}
      />
      {error(key)}
    </div>
  );
  return (
    <fieldset disabled={disabled} className="space-y-6">
      {kind !== "department" && (
        <div className="space-y-2">
          <Label htmlFor={field("department_id")}>
            所属部门{kind !== "person" && <span className="ml-1 text-destructive">*</span>}
          </Label>
          <SearchChoice
            id={field("department_id")}
            label="所属部门"
            value={draft.department_id ? [draft.department_id] : []}
            options={directory.departments
              .filter((d) => d.active || d.id === draft.department_id)
              .map((d) => ({ id: d.id, name: d.name + (d.active ? "" : "（已停用）") }))}
            onChange={(ids) =>
              change({
                department_id: ids[0] || null,
                dev_group_id: null,
                group_ids: [],
                ...(kind === "business_line" ? { primary_id: null, backup_id: null } : {}),
              })
            }
            disabled={disabled ? true : (kind === "business_line" || kind === "dev_group") && draft.id > 0}
            onAdd={onInline ? () => onInline("department", "department_id") : undefined}
            addLabel="新增部门"
            invalid={!!errors.department_id}
            placeholder="选择所属部门"
          />
          {kind === "dev_group" && draft.id > 0 && (
            <p className="text-muted-foreground text-xs">
              研发组归属部门保持不变；跨部门调整请先转移成员，再新建研发组。
            </p>
          )}
          {kind === "business_line" && draft.id > 0 && (
            <p className="text-muted-foreground text-xs">迁部门请使用抽屉底部的“迁移业务线”，以保留归属历史。</p>
          )}
          {error("department_id")}
        </div>
      )}
      <div className="grid gap-5 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor={field("name")}>
            {kind === "person" ? "姓名" : `${kindNames[kind]}名称`}
            <span className="text-destructive">*</span>
          </Label>
          <Input
            id={field("name")}
            value={draft.name}
            maxLength={100}
            aria-invalid={!!errors.name}
            aria-describedby={errors.name ? `${field("name")}-error` : undefined}
            onChange={(e) =>
              change({
                name: e.target.value,
                ...(!codeManual && !draft.id ? { code: automaticCode(e.target.value, kind) } : {}),
              })
            }
            onBlur={() => blur("name")}
            placeholder={kind === "person" ? "填写姓名" : "例如：核心业务"}
          />
          {error("name")}
        </div>
        {(kind === "department" || kind === "business_line") && (
          <div className="space-y-2">
            <Label htmlFor={field("code")}>
              编码<span className="text-destructive">*</span>
            </Label>
            <Input
              id={field("code")}
              value={draft.code}
              maxLength={100}
              aria-invalid={!!errors.code}
              onChange={(e) => {
                setCodeManual(true);
                change({ code: e.target.value });
              }}
              onBlur={() => blur("code")}
              placeholder="根据名称自动生成"
            />
            {error("code")}
          </div>
        )}
        {kind === "person" && (
          <div className="space-y-2">
            <Label htmlFor={field("email")}>
              邮箱<span className="text-destructive">*</span>
            </Label>
            <Input
              id={field("email")}
              type="email"
              value={draft.email}
              maxLength={254}
              aria-invalid={!!errors.email}
              onChange={(e) => change({ email: e.target.value })}
              onBlur={() => blur("email")}
              placeholder="name@example.com"
            />
            {error("email")}
          </div>
        )}
      </div>
      {kind === "person" && (
        <div className="space-y-2">
          <Label htmlFor={field("dev_group_id")}>所属研发组</Label>
          <SearchChoice
            id={field("dev_group_id")}
            label="研发组"
            value={draft.dev_group_id ? [draft.dev_group_id] : []}
            options={groups.map((g) => ({ id: g.id, name: g.name }))}
            onChange={(ids) => change({ dev_group_id: ids[0] || null })}
            onAdd={onInline && draft.department_id ? () => onInline("dev_group", "dev_group_id") : undefined}
            addLabel="新增研发组"
            disabled={disabled ? true : !draft.department_id}
          />
          {error("dev_group_id")}
        </div>
      )}
      {kind === "department" && (
        <section className="space-y-4 border-t pt-5">
          <h3 className="font-medium">部门负责人</h3>
          {personChoice("head_id", "部门负责人")}
        </section>
      )}
      {kind === "department" && (
        <NotificationChannels
          kind="department"
          draft={draft}
          prefix={prefix}
          change={change}
          errors={errors}
          disabled={disabled}
        />
      )}
      {kind === "business_line" && (
        <>
          <section className="space-y-4 border-t pt-5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="font-medium">负责人</h3>
              <label className="flex items-center gap-2 text-muted-foreground text-xs">
                <input type="checkbox" checked={otherPeople} onChange={(e) => setOtherPeople(e.target.checked)} />
                显示其他部门人员
              </label>
            </div>
            {personChoice("primary_id", "主负责人")}
            {personChoice("backup_id", "备选负责人", draft.primary_id)}
            <div className="space-y-2">
              <Label htmlFor={field("group_ids")}>关联研发组</Label>
              <SearchChoice
                id={field("group_ids")}
                label="研发组"
                value={draft.group_ids}
                options={groups.map((g) => ({ id: g.id, name: g.name }))}
                onChange={(ids) => change({ group_ids: ids })}
                multiple
                onAdd={onInline ? () => onInline("dev_group", "group_ids") : undefined}
                addLabel="新增研发组"
                disabled={disabled ? true : !draft.department_id}
                placeholder="添加研发组"
              />
              {error("group_ids")}
              <p className="text-muted-foreground text-xs">通知邮件会抄送所选研发组的已启用成员。</p>
            </div>
          </section>
          <NotificationChannels
            kind="business_line"
            draft={draft}
            prefix={prefix}
            change={change}
            errors={errors}
            disabled={disabled}
          />
          <section className="space-y-4 border-t pt-5">
            <div className="flex items-center justify-between">
              <h3 className="font-medium">修复 SLA</h3>
              <div className="flex items-center gap-2">
                <Label htmlFor={field("inherit")}>沿用全局</Label>
                <Switch
                  id={field("inherit")}
                  checked={!draft.sla}
                  onCheckedChange={(v) => change({ sla: v ? null : { ...directory.sla } })}
                />
              </div>
            </div>
            {draft.sla && (
              <>
                <div className="grid grid-cols-2 gap-4">
                  {(
                    [
                      ["critical", "严重"],
                      ["high", "高危"],
                      ["medium", "中危"],
                      ["low", "低危"],
                    ] as const
                  ).map(([key, title]) => (
                    <div key={key} className="space-y-2">
                      <Label htmlFor={field(key)}>{title}</Label>
                      <div className="relative">
                        <Input
                          id={field(key)}
                          type="number"
                          min={1}
                          max={365}
                          step={1}
                          value={draft.sla?.[key] ?? ""}
                          onChange={(e) => change({ sla: { ...draft.sla!, [key]: Number(e.target.value) } })}
                          onBlur={() => blur("sla")}
                          aria-invalid={!!errors.sla}
                          className="pr-8"
                        />
                        <span className="absolute top-2 right-3 text-muted-foreground text-xs">天</span>
                      </div>
                    </div>
                  ))}
                </div>
                {error("sla")}
              </>
            )}
          </section>
          <section className="space-y-2 border-t pt-5">
            <h3 className="font-medium">归属规则</h3>
            <p className="text-muted-foreground text-xs">
              {draft.id
                ? "归属规则在“资产归属 → 归属规则”中独立配置。请先保存或取消当前编辑，再切换标签；保存业务线不会自动应用规则到存量资产。"
                : "先保存业务线，再到“资产归属 → 归属规则”配置匹配规则。"}
            </p>
          </section>
        </>
      )}
    </fieldset>
  );
}

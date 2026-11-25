"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import Link from "next/link";

import {
  ArrowRight,
  ArrowUpDown,
  Building2,
  Check,
  ChevronRight,
  Download,
  History,
  Loader2,
  Pencil,
  Plus,
  Search,
  ShieldCheck,
  Upload,
  Users,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type OrgAPIError,
  type OrgAudit,
  type OrgCommand,
  type OrgCSVPreview,
  type OrgDirectory,
  type OrgImpact,
  type OrgScope,
  orgAPI,
} from "@/lib/org";
import { refreshOrganizationStatistics } from "@/lib/org-statistics";
import { radixOverlayWasOpenAtPointerDown } from "@/lib/utils";

import { OrgAssets } from "./assets";
import { OrgAssignmentJobs } from "./assignment";
import { createDraft, type Draft, type EditKind, EditorFields, kindNames, SearchChoice, validateDraft } from "./form";
import { notificationPayload } from "./notification-channels";

type Editor = { kind: EditKind; draft: Draft; initial: Draft; codeManual: boolean; errors: Record<string, string> };
type ConfirmAction = {
  command: OrgCommand;
  impact: OrgImpact;
  label: string;
  scope: OrgScope;
  receiver: number | null;
  error: string;
};
type Tab = "departments" | "lines" | "people" | "assets";
const roleNames: Record<string, string> = {
  primary: "业务线负责人",
  owner: "业务线负责人",
  backup: "备选负责人",
  head: "部门负责人",
  department_head: "部门负责人",
  fallback: "兜底处理人",
  business_line_owner: "业务线负责人",
  business_line_backup: "备选负责人",
};
const actionNames: Record<string, string> = {
  create: "新增",
  save: "保存",
  update: "修改",
  disable: "停用",
  migrate: "迁移",
  undo: "撤销",
  import: "导入",
};
const auditFieldNames: Record<string, string> = {
  name: "名称",
  code: "编码",
  email: "邮箱",
  active: "启用状态",
  department_id: "所属部门",
  dev_group_id: "所属研发组",
  head_id: "部门负责人",
  primary_id: "主负责人",
  owner_id: "负责人",
  backup_id: "备选负责人",
  group_ids: "关联研发组",
  channel_ids: "通知渠道",
  sla: "SLA",
  scope: "生效范围",
};
const tableClass =
  "w-full text-left text-sm [&_th]:px-4 [&_th]:py-3 [&_th]:whitespace-nowrap [&_th]:font-medium [&_th_button]:whitespace-nowrap [&_th]:text-muted-foreground [&_td]:px-4 [&_td]:py-3.5";

export default function OrgPage() {
  const [directory, setDirectory] = useState<OrgDirectory>();
  const [loadError, setLoadError] = useState("");
  const [tab, setTab] = useState<Tab>("departments");
  const [department, setDepartment] = useState<number | null>(null);
  const [group, setGroup] = useState<number | null>(null);
  const [query, setQuery] = useState("");
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [sort, setSort] = useState<{ key: "name" | "department" | "open"; direction: number }>({
    key: "name",
    direction: 1,
  });
  const [editor, setEditor] = useState<Editor | null>(null);
  const [inline, setInline] = useState<(Editor & { target: keyof Draft }) | null>(null);
  const [discard, setDiscard] = useState<"main" | "inline" | null>(null);
  const [focusField, setFocusField] = useState("name");
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const [inlineError, setInlineError] = useState("");
  const [confirmation, setConfirmation] = useState<ConfirmAction | null>(null);
  const [highlight, setHighlight] = useState("");
  const [fallback, setFallback] = useState<number | null>(null);
  const [fallbackSaving, setFallbackSaving] = useState(false);
  const [audit, setAudit] = useState<{
    kind: EditKind;
    id: number;
    name: string;
    items?: OrgAudit[];
    error?: string;
  } | null>(null);
  const [csvOpen, setCSVOpen] = useState(false);
  const [csv, setCSV] = useState("");
  const [csvName, setCSVName] = useState("");
  const [csvDepartment, setCSVDepartment] = useState<number | null>(null);
  const [csvPreview, setCSVPreview] = useState<OrgCSVPreview | null>(null);
  const [csvError, setCSVError] = useState("");
  const [csvBusy, setCSVBusy] = useState(false);
  const [csvStep, setCSVStep] = useState(1);
  const [csvKey, setCSVKey] = useState("");
  const opener = useRef<HTMLElement | null>(null);
  const auditOpener = useRef<HTMLElement | null>(null);
  const serverFallback = useRef<number | null>(null);
  const inlineReturnField = useRef<keyof Draft>("name");
  const mainForm = useRef<HTMLDivElement>(null);
  const inlineForm = useRef<HTMLDivElement>(null);
  const continueButton = useRef<HTMLButtonElement>(null);
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async (resetFallback = false) => {
    const data = await orgAPI.directory();
    setDirectory(data);
    setLoadError("");
    const previousFallback = serverFallback.current;
    serverFallback.current = data.fallback_id;
    setFallback((current) => (resetFallback || current === previousFallback ? data.fallback_id : current));
    return data;
  }, []);
  useEffect(() => {
    const readURL = () => {
      const q = new URLSearchParams(window.location.search);
      const next = q.get("tab");
      setTab(next === "lines" || next === "people" || next === "assets" ? next : "departments");
      setDepartment(Number(q.get("department")) || null);
      setGroup(Number(q.get("group")) || null);
    };
    readURL();
    void load(true).catch((e: Error) => setLoadError(e.message));
    window.addEventListener("popstate", readURL);
    return () => {
      window.removeEventListener("popstate", readURL);
      if (highlightTimer.current) clearTimeout(highlightTimer.current);
    };
  }, [load]);

  function navigate(nextTab: Tab, nextDepartment = department, nextGroup = group) {
    setTab(nextTab);
    setDepartment(nextDepartment);
    setGroup(nextGroup);
    const url = new URL(window.location.href);
    url.searchParams.set("tab", nextTab);
    for (const [key, value] of [
      ["department", nextDepartment],
      ["group", nextGroup],
    ] as const) {
      if (value) url.searchParams.set(key, String(value));
      else url.searchParams.delete(key);
    }
    if (nextTab === "assets") url.searchParams.set("unassigned", "true");
    else url.searchParams.delete("unassigned");
    window.history.replaceState(null, "", url);
  }
  function openEditor(kind: EditKind, value: Partial<Draft> = {}, focus = "name") {
    opener.current = document.activeElement as HTMLElement;
    const draft = createDraft({ department_id: department, ...value });
    setEditor({ kind, draft, initial: structuredClone(draft), codeManual: !!draft.id, errors: {} });
    setFormError("");
    setFocusField(focus);
  }
  function updateDraft(patch: Partial<Draft>, child = false) {
    const apply = <T extends Editor>(current: T | null): T | null => {
      if (!current) return current;
      const errors = { ...current.errors };
      for (const key of Object.keys(patch)) {
        for (const field of Object.keys(errors)) {
          if (field === key || field.startsWith(`${key}.`)) delete errors[field];
          if (key === "shared_channel_confirmed_ids" && field.endsWith(".shared")) delete errors[field];
        }
      }
      return { ...current, draft: { ...current.draft, ...patch }, errors };
    };
    if (child) {
      setInline(apply);
      setInlineError("");
    } else {
      setEditor(apply);
      setFormError("");
    }
  }
  function dirty(current: Editor | null) {
    return !!current && JSON.stringify(current.draft) !== JSON.stringify(current.initial);
  }
  function closeEditor(child = false) {
    if (saving) return;
    if (dirty(child ? inline : editor)) {
      setDiscard(child ? "inline" : "main");
      return;
    }
    if (child) setInline(null);
    else setEditor(null);
  }
  function openInline(kind: EditKind, target: keyof Draft) {
    if (!editor) return;
    inlineReturnField.current = target;
    const draft = createDraft({
      department_id: editor.draft.department_id || (editor.kind === "department" ? editor.draft.id || null : null),
    });
    setInline({ kind, target, draft, initial: structuredClone(draft), codeManual: false, errors: {} });
    setInlineError("");
  }
  async function blur(field: string, child = false) {
    const current = child ? inline : editor;
    if (!current) return;
    const local = validateDraft(current.kind, current.draft);
    let message = local[field];
    if (!message && (field === "name" || field === "email")) {
      try {
        const check = await orgAPI.check({
          kind: current.kind,
          id: String(current.draft.id),
          department_id: String(current.draft.department_id || 0),
          name: current.draft.name.trim(),
          email: current.draft.email.trim(),
        });
        message = check.errors[field];
      } catch {
        return;
      } // The save endpoint remains authoritative if the optional blur check is unavailable.
    }
    const apply = <T extends Editor>(state: T | null): T | null => {
      if (
        !state ||
        state.kind !== current.kind ||
        state.draft[field as keyof Draft] !== current.draft[field as keyof Draft]
      )
        return state;
      const errors = { ...state.errors };
      if (message) errors[field] = message;
      else delete errors[field];
      return { ...state, errors };
    };
    if (child) setInline(apply);
    else setEditor(apply);
  }
  function command(current: Editor): OrgCommand {
    const { channel_drafts, shared_channel_confirmed_ids, ...data } = current.draft;
    return {
      kind: current.kind,
      action: "save",
      id: current.draft.id,
      revision: current.draft.revision,
      data,
      channel_drafts: channel_drafts
        .filter((channel) => !channel.id || data.channel_ids.includes(channel.id))
        .map(notificationPayload),
      shared_channel_confirmed_ids,
      scope: "future_findings",
      idempotency_key: crypto.randomUUID(),
    };
  }
  function showErrors(errors: Record<string, string>, child = false) {
    if (child) setInline((s) => s && { ...s, errors });
    else setEditor((s) => s && { ...s, errors });
    requestAnimationFrame(() => {
      const form = child ? inlineForm.current : mainForm.current;
      const key = Object.keys(errors)[0];
      const candidate = document.getElementById(`${child ? "inline" : "org"}-${key === "sla" ? "critical" : key}`);
      const target = candidate && form?.contains(candidate) ? candidate : null;
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
      target?.focus();
    });
  }
  function saved(
    result: { operation_id: string; entity_id: number },
    kind: EditKind,
    message: string,
    allowUndo = true,
  ) {
    setHighlight(`${kind}:${result.entity_id}`);
    if (highlightTimer.current) clearTimeout(highlightTimer.current);
    highlightTimer.current = setTimeout(() => setHighlight(""), 4500);
    if (!allowUndo) {
      toast.success(message, { duration: 4000 });
      return;
    }
    const savedToast = toast.success(message, {
      duration: 8000,
      action: {
        label: "撤销",
        onClick: () => {
          void orgAPI
            .undo(result.operation_id)
            .then(() => {
              toast.dismiss(savedToast);
              refreshOrganizationStatistics();
              return load(true);
            })
            .then(() => toast.success("已撤销，变更记录已保留", { duration: 4000 }))
            .catch((e: Error) => toast.error(e.message));
        },
      },
    });
  }
  async function submit(cmd: OrgCommand, child = false) {
    setSaving(true);
    try {
      const result = await orgAPI.change(cmd);
      refreshOrganizationStatistics();
      await load(cmd.kind === "fallback").catch(() => toast.warning("变更已保存，列表暂时未刷新，请稍后重新加载"));
      if (child && inline) {
        const target = inline.target;
        setEditor(
          (current) =>
            current && {
              ...current,
              draft: {
                ...current.draft,
                [target]: target === "group_ids" ? [...current.draft.group_ids, result.entity_id] : result.entity_id,
                ...(target === "department_id" ? { dev_group_id: null, group_ids: [] } : {}),
              },
            },
        );
        setInline(null);
      } else {
        setEditor(null);
        setConfirmation(null);
      }
      saved(
        result,
        cmd.kind as EditKind,
        `${kindNames[cmd.kind as EditKind]}已${cmd.action === "disable" ? "停用" : cmd.action === "migrate" ? "迁移" : "保存"}`,
        cmd.action === "save",
      );
    } catch (e) {
      const error = e as OrgAPIError;
      if (confirmation && !child) setConfirmation((s) => s && { ...s, error: error.message });
      else if (child) setInlineError(error.message);
      else setFormError(error.message);
      if (Object.keys(error.fields || {}).length) showErrors(error.fields, child);
    } finally {
      setSaving(false);
    }
  }
  async function prepareSave(child = false) {
    const current = child ? inline : editor;
    if (!current || saving) return;
    const errors = validateDraft(current.kind, current.draft);
    if (Object.keys(errors).length) {
      showErrors(errors, child);
      return;
    }
    const cmd = command(current);
    const ownerChanged =
      current.draft.id > 0 &&
      (current.draft.primary_id !== current.initial.primary_id ||
        current.draft.backup_id !== current.initial.backup_id ||
        current.draft.head_id !== current.initial.head_id);
    if (!child && ownerChanged) {
      setSaving(true);
      try {
        const impact = await orgAPI.impact(cmd);
        setConfirmation({
          command: cmd,
          impact,
          label: "确认负责人变更",
          scope: "future_findings",
          receiver: null,
          error: "",
        });
      } catch (e) {
        setFormError((e as Error).message);
      } finally {
        setSaving(false);
      }
    } else await submit(cmd, child);
  }
  async function prepareAction(action: "disable" | "migrate") {
    if (!editor || saving) return;
    setSaving(true);
    setFormError("");
    try {
      const cmd = { ...command({ ...editor, draft: editor.initial }), action, scope: "all_open" as const };
      const impact = await orgAPI.impact(cmd);
      let receiver: number | null = null;
      if (editor.kind === "person") {
        const backup = directory?.business_lines.find(
          (line) =>
            line.primary_id === editor.draft.id &&
            line.backup_id &&
            directory.people.some((p) => p.id === line.backup_id && p.active),
        );
        receiver = backup?.backup_id || null;
      }
      setConfirmation({
        command: cmd,
        impact,
        label: action === "migrate" ? "迁移业务线" : `停用${kindNames[editor.kind]}`,
        scope: "all_open",
        receiver,
        error: "",
      });
    } catch (e) {
      setFormError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }
  async function openHistory(kind: EditKind, id: number, name: string, trigger: HTMLElement) {
    auditOpener.current = trigger;
    setAudit({ kind, id, name });
    try {
      const data = await orgAPI.history(kind, id);
      setAudit({ kind, id, name, items: data.items });
    } catch (e) {
      setAudit({ kind, id, name, error: (e as Error).message });
    }
  }
  async function saveFallback() {
    if (!directory || !fallback) return;
    setFallbackSaving(true);
    try {
      await orgAPI.change({
        kind: "fallback",
        action: "save",
        id: 1,
        revision: directory.config_revision,
        data: { owner_id: fallback },
        scope: "all_open",
        confirm_impact: true,
        idempotency_key: crypto.randomUUID(),
      });
      await load(true);
      refreshOrganizationStatistics();
      toast.success("全局兜底处理人已保存", { duration: 4000 });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setFallbackSaving(false);
    }
  }
  async function previewCSV(text = csv) {
    if (!text.trim()) {
      setCSVError("请先上传 CSV 文件");
      return;
    }
    setCSVBusy(true);
    setCSVError("");
    try {
      setCSVPreview(await orgAPI.importPreview(text, csvDepartment));
      setCSVStep(3);
      setCSVKey(crypto.randomUUID());
    } catch (e) {
      setCSVError((e as Error).message);
    } finally {
      setCSVBusy(false);
    }
  }
  async function importCSV() {
    setCSVBusy(true);
    setCSVError("");
    try {
      const result = await orgAPI.importPeople(csv, csvDepartment, csvKey);
      refreshOrganizationStatistics();
      await load();
      setCSVStep(4);
      toast.success(`已导入：新增 ${result.created} 人，更新 ${result.updated} 人`, { duration: 5000 });
    } catch (e) {
      setCSVError((e as Error).message);
    } finally {
      setCSVBusy(false);
    }
  }
  function toggleSort(key: "name" | "department" | "open") {
    setSort((s) => ({ key, direction: s.key === key ? -s.direction : 1 }));
  }
  const personName = (id: number | null) => directory?.people.find((p) => p.id === id)?.name || "未设置";
  const departmentName = (id: number | null) => directory?.departments.find((d) => d.id === id)?.name || "未归属部门";

  if (!directory)
    return (
      <div className="mx-auto max-w-7xl space-y-6 p-4 sm:p-6">
        <h1 className="font-semibold text-2xl">部门与业务线</h1>
        {loadError ? (
          <div role="alert" className="rounded-xl border p-6">
            <p className="mb-3 text-destructive">{loadError}</p>
            <Button variant="outline" onClick={() => void load(true).catch((e: Error) => setLoadError(e.message))}>
              重新加载
            </Button>
          </div>
        ) : (
          <div role="status" aria-label="正在加载组织配置" className="space-y-4">
            <div className="h-10 w-96 max-w-full animate-pulse rounded-lg bg-muted" />
            <div className="grid gap-5 md:grid-cols-[240px_1fr]">
              <div className="h-80 animate-pulse rounded-xl bg-muted" />
              <div className="space-y-4 rounded-xl border p-6">
                {[1, 2, 3, 4, 5].map((i) => (
                  <div key={i} className="h-9 animate-pulse rounded bg-muted" />
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    );

  const canEdit = directory.can_edit;
  const selectedDepartment = directory.departments.find((d) => d.id === department);
  const displayLines = directory.business_lines
    .filter(
      (line) =>
        (!department || line.department_id === department) &&
        (!onlyMissing || !line.primary_id) &&
        `${line.name} ${line.code} ${departmentName(line.department_id)}`.toLowerCase().includes(query.toLowerCase()),
    )
    .sort(
      (a, b) =>
        sort.direction *
        (sort.key === "open"
          ? a.open - b.open
          : sort.key === "department"
            ? departmentName(a.department_id).localeCompare(departmentName(b.department_id), "zh")
            : a.name.localeCompare(b.name, "zh")),
    );
  const displayPeople = directory.people.filter(
    (p) =>
      (!department || p.department_id === department) &&
      (!group || p.dev_group_id === group) &&
      `${p.name} ${p.email}`.toLowerCase().includes(query.toLowerCase()),
  );
  const sideGroups = directory.groups.filter((g) => !department || g.department_id === department);
  const editReason = canEdit ? undefined : "需要管理员权限才能修改组织配置";
  const highlightClass = (kind: EditKind, id: number) =>
    highlight === `${kind}:${id}` ? "bg-emerald-50 dark:bg-emerald-950/30" : "hover:bg-muted/50";
  const departmentRail = (
    <aside className="space-y-1 rounded-xl border bg-card p-3 md:self-start">
      <div className="px-3 py-2 font-medium text-muted-foreground text-xs">部门</div>
      <button
        type="button"
        className={`flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-sm ${!department ? "bg-primary/10 font-medium text-primary" : "hover:bg-muted"}`}
        onClick={() => navigate(tab, null, null)}
      >
        <span>全部部门</span>
        <span className="text-xs tabular-nums">{directory.departments.length}</span>
      </button>
      {directory.departments.map((d) => (
        <div
          key={d.id}
          className={`flex w-full items-start justify-between gap-2 rounded-lg px-3 py-3 text-left transition-colors ${department === d.id ? "bg-primary/10 text-primary" : "hover:bg-muted"}`}
        >
          <button
            type="button"
            className="min-w-0 flex-1 rounded text-left focus-visible:outline-2 focus-visible:outline-primary"
            onClick={() => navigate(tab, d.id, null)}
          >
            <span className="block truncate font-medium">
              {d.name}
              {!d.active && <span className="ml-1 text-muted-foreground text-xs">已停用</span>}
            </span>
            <span className="mt-1 block text-muted-foreground text-xs">{personName(d.head_id)}</span>
          </button>
          <Link
            href={`/function/findings?department_id=${d.id}&active_only=true`}
            aria-label={`查看${d.name}的 ${d.open} 个待处置漏洞`}
            className="mt-1 rounded text-primary text-xs tabular-nums underline decoration-primary/40 underline-offset-4 hover:decoration-primary focus-visible:outline-2 focus-visible:outline-primary"
          >
            {d.open}
          </Link>
        </div>
      ))}
      <div className="mt-3 border-t pt-3">
        <button
          type="button"
          className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-amber-700 text-xs hover:bg-amber-50 dark:text-amber-400"
          onClick={() => navigate("assets", null, null)}
        >
          <span>未归属部门 · {directory.unassigned_open} 个待处置</span>
          <ChevronRight className="size-3.5" />
        </button>
      </div>
    </aside>
  );
  const lineTable = (
    <div className="overflow-x-auto rounded-xl border bg-card">
      <table className={tableClass}>
        <thead className="border-b bg-muted/30">
          <tr>
            <th>
              <button type="button" className="flex items-center gap-1" onClick={() => toggleSort("name")}>
                业务线
                <ArrowUpDown className="size-3" />
              </button>
            </th>
            <th>
              <button type="button" className="flex items-center gap-1" onClick={() => toggleSort("department")}>
                所属部门
                <ArrowUpDown className="size-3" />
              </button>
            </th>
            <th>主负责人 / 备选</th>
            <th>研发组</th>
            <th>
              <button type="button" className="flex items-center gap-1" onClick={() => toggleSort("open")}>
                待处置
                <ArrowUpDown className="size-3" />
              </button>
            </th>
            <th>
              <span className="sr-only">操作</span>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {displayLines.map((line) => (
            <tr
              key={line.id}
              tabIndex={0}
              aria-label={`编辑业务线 ${line.name}`}
              className={`cursor-pointer transition-colors ${highlightClass("business_line", line.id)}`}
              onClick={() => openEditor("business_line", line)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && e.target === e.currentTarget) openEditor("business_line", line);
              }}
            >
              <td className="min-w-40">
                <span className="font-medium">{line.name}</span>
                {!line.active && (
                  <Badge variant="secondary" className="ml-2 text-xs">
                    已停用
                  </Badge>
                )}
                <span className="mt-1 block font-mono text-muted-foreground text-xs">{line.code}</span>
              </td>
              <td className="whitespace-nowrap">{departmentName(line.department_id)}</td>
              <td className="min-w-36">
                {line.primary_id ? (
                  <span>{personName(line.primary_id)}</span>
                ) : (
                  <button
                    type="button"
                    className="rounded bg-amber-50 px-2 py-0.5 text-amber-700 text-xs dark:bg-amber-950/30 dark:text-amber-400"
                    onClick={(e) => {
                      e.stopPropagation();
                      openEditor("business_line", line, "primary_id");
                    }}
                  >
                    未设置
                  </button>
                )}
                <span className="mt-1 block text-muted-foreground text-xs">
                  备选：{line.backup_id ? personName(line.backup_id) : "未设置"}
                </span>
              </td>
              <td className="min-w-32 text-muted-foreground text-xs">
                {line.group_ids
                  .map((id) => directory.groups.find((g) => g.id === id)?.name)
                  .filter(Boolean)
                  .join("、") || "—"}
              </td>
              <td className="tabular-nums">
                <Link
                  href={`/function/findings?department_id=${line.department_id}&business_line_id=${line.id}&active_only=true`}
                  aria-label={`查看${line.name}的 ${line.open} 个待处置漏洞`}
                  className="inline-flex min-h-8 min-w-8 items-center rounded text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary focus-visible:outline-2 focus-visible:outline-primary"
                  onClick={(event) => event.stopPropagation()}
                >
                  {line.open}
                </Link>
              </td>
              <td>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  disabled={!canEdit}
                  title={editReason || `编辑${line.name}`}
                  aria-label={`编辑${line.name}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    openEditor("business_line", line);
                  }}
                >
                  <Pencil className="size-3.5" />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!displayLines.length && (
        <div className="p-12 text-center text-muted-foreground">
          <Building2 className="mx-auto mb-3 size-8 opacity-40" />
          <p>没有符合条件的业务线</p>
          <Button variant="link" disabled={!canEdit} title={editReason} onClick={() => openEditor("business_line")}>
            新增业务线
          </Button>
        </div>
      )}
    </div>
  );

  return (
    <div className="mx-auto max-w-[1440px] space-y-6 p-4 sm:p-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <span className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
            <Building2 className="size-5" />
          </span>
          <div>
            <h1 className="font-semibold text-xl tracking-tight sm:text-2xl">部门与业务线</h1>
            <p className="mt-0.5 text-muted-foreground text-xs">组织与责任配置</p>
          </div>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link href="/function/remediation">
            漏洞治理
            <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      </header>
      {!canEdit && (
        <p className="rounded-lg bg-amber-50 px-4 py-3 text-amber-800 text-sm">
          当前为只读访问，配置修改需要管理员权限。
        </p>
      )}
      <Tabs
        value={tab}
        onValueChange={(value) => {
          navigate(value as Tab, department, null);
          setQuery("");
        }}
      >
        <TabsList variant="line" className="h-11 w-full justify-start gap-2 overflow-x-auto border-b">
          <TabsTrigger value="departments" className="flex-none px-4">
            部门
            <Badge variant="secondary" className="ml-1">
              {directory.departments.length}
            </Badge>
          </TabsTrigger>
          <TabsTrigger value="lines" className="flex-none px-4">
            业务线
          </TabsTrigger>
          <TabsTrigger value="people" className="flex-none px-4">
            人员与团队
          </TabsTrigger>
          <TabsTrigger value="assets" className="flex-none px-4">
            资产归属
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <OrgAssignmentJobs
        onChanged={() => {
          refreshOrganizationStatistics();
          return load();
        }}
      />
      {tab !== "assets" && directory.unassigned_open > 0 && (
        <button
          type="button"
          onClick={() => navigate("assets", null, null)}
          className="flex w-full items-center justify-between rounded-lg border border-amber-200/70 bg-amber-50/60 px-4 py-3 text-amber-800 text-sm dark:border-amber-800 dark:bg-amber-950/20 dark:text-amber-300"
        >
          <span>{directory.unassigned_open} 个漏洞未归属部门</span>
          <span className="flex items-center gap-1 text-xs">
            查看未归属资产
            <ArrowRight className="size-3.5" />
          </span>
        </button>
      )}
      {tab === "departments" && (
        <div className="grid items-start gap-5 md:grid-cols-[240px_minmax(0,1fr)]">
          {departmentRail}
          <main className="min-w-0 space-y-5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 className="font-medium text-lg">{selectedDepartment ? selectedDepartment.name : "全部部门"}</h2>
              <Button size="sm" disabled={!canEdit} title={editReason} onClick={() => openEditor("department")}>
                <Plus className="size-4" />
                新增部门
              </Button>
            </div>
            {selectedDepartment ? (
              <section className="flex flex-wrap items-center justify-between gap-4 rounded-xl border bg-card p-5">
                <div>
                  <p className="mb-2 text-muted-foreground text-xs">部门负责人</p>
                  {selectedDepartment.head_id ? (
                    <p className="font-medium">{personName(selectedDepartment.head_id)}</p>
                  ) : (
                    <button
                      type="button"
                      className="rounded bg-amber-50 px-2 py-1 text-amber-700 text-sm"
                      onClick={() => openEditor("department", selectedDepartment, "head_id")}
                    >
                      未设置
                    </button>
                  )}
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={(event) =>
                      void openHistory(
                        "department",
                        selectedDepartment.id,
                        selectedDepartment.name,
                        event.currentTarget,
                      )
                    }
                  >
                    <History className="size-3.5" />
                    变更记录
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!canEdit}
                    title={editReason}
                    onClick={() => openEditor("department", selectedDepartment)}
                  >
                    <Pencil className="size-3.5" />
                    编辑部门
                  </Button>
                </div>
              </section>
            ) : (
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {directory.departments.map((d) => (
                  <div
                    key={d.id}
                    className={`flex items-end justify-between gap-3 rounded-xl border bg-card p-4 transition-colors ${highlightClass("department", d.id)}`}
                  >
                    <button
                      type="button"
                      onClick={() => navigate("departments", d.id, null)}
                      className="min-w-0 flex-1 rounded text-left focus-visible:outline-2 focus-visible:outline-primary"
                    >
                      <span className="mb-3 flex items-center justify-between gap-2">
                        <span className="font-medium">{d.name}</span>
                        {!d.active && <Badge variant="secondary">已停用</Badge>}
                      </span>
                      <span className="text-muted-foreground text-xs">{personName(d.head_id)}</span>
                    </button>
                    <Link
                      href={`/function/findings?department_id=${d.id}&active_only=true`}
                      aria-label={`查看${d.name}的 ${d.open} 个待处置漏洞`}
                      className="shrink-0 rounded text-primary text-xs tabular-nums underline decoration-primary/40 underline-offset-4 hover:decoration-primary focus-visible:outline-2 focus-visible:outline-primary"
                    >
                      {d.open} 个待处置
                    </Link>
                  </div>
                ))}
                {!directory.departments.length && (
                  <div className="col-span-full rounded-xl border border-dashed p-10 text-center text-muted-foreground">
                    先新增一个部门，也可以在新建业务线时就地创建。
                  </div>
                )}
              </div>
            )}
            <div className="flex items-center justify-between">
              <h3 className="font-medium">{selectedDepartment ? "下属业务线" : "业务线"}</h3>
              <Button
                variant="outline"
                size="sm"
                disabled={!canEdit}
                title={editReason}
                onClick={() => openEditor("business_line")}
              >
                <Plus className="size-3.5" />
                新增业务线
              </Button>
            </div>
            {lineTable}
          </main>
        </div>
      )}
      {tab === "lines" && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <div className="w-44">
                <SearchChoice
                  label="部门筛选"
                  value={department ? [department] : []}
                  options={directory.departments.map((d) => ({ id: d.id, name: d.name }))}
                  placeholder="全部部门"
                  onChange={(ids) => navigate("lines", ids[0] || null, null)}
                />
              </div>
              {directory.business_lines.length > 10 && (
                <>
                  <div className="relative">
                    <Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" />
                    <Input
                      aria-label="搜索业务线"
                      placeholder="搜索名称或编码"
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                      className="pl-9"
                    />
                  </div>
                  <label className="flex items-center gap-2 text-muted-foreground text-sm">
                    <input type="checkbox" checked={onlyMissing} onChange={(e) => setOnlyMissing(e.target.checked)} />
                    只看缺负责人
                  </label>
                </>
              )}
            </div>
            <Button size="sm" disabled={!canEdit} title={editReason} onClick={() => openEditor("business_line")}>
              <Plus className="size-4" />
              新增业务线
            </Button>
          </div>
          {lineTable}
        </div>
      )}
      {tab === "people" && (
        <div className="grid items-start gap-5 md:grid-cols-[240px_minmax(0,1fr)]">
          <div className="space-y-4">
            {departmentRail}
            <aside className="rounded-xl border bg-card p-3">
              <div className="mb-2 flex items-center justify-between px-2">
                <h3 className="font-medium text-muted-foreground text-xs">研发组</h3>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="新增研发组"
                  disabled={!canEdit}
                  title={editReason}
                  onClick={() => openEditor("dev_group")}
                >
                  <Plus className="size-3.5" />
                </Button>
              </div>
              <button
                type="button"
                onClick={() => navigate("people", department, null)}
                className={`w-full rounded-lg px-3 py-2 text-left text-sm ${!group ? "bg-primary/10 text-primary" : "hover:bg-muted"}`}
              >
                全部人员
              </button>
              {sideGroups.map((g) => (
                <div
                  key={g.id}
                  className={`flex items-center rounded-lg ${group === g.id ? "bg-primary/10 text-primary" : "hover:bg-muted"}`}
                >
                  <button
                    type="button"
                    className="flex flex-1 items-center justify-between px-3 py-2 text-left text-sm"
                    onClick={() => navigate("people", department, g.id)}
                  >
                    <span>
                      {g.name}
                      {!g.active && "（已停用）"}
                    </span>
                    <span className="text-muted-foreground text-xs">{g.members}</span>
                  </button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={!canEdit}
                    title={editReason || `编辑${g.name}`}
                    aria-label={`编辑研发组${g.name}`}
                    onClick={() => openEditor("dev_group", g)}
                  >
                    <Pencil className="size-3" />
                  </Button>
                </div>
              ))}
            </aside>
          </div>
          <main className="min-w-0 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="relative">
                <Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" />
                <Input
                  aria-label="搜索人员"
                  placeholder="搜索姓名或邮箱"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  className="pl-9"
                />
              </div>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!canEdit}
                  title={editReason}
                  onClick={() => {
                    setCSVOpen(true);
                    setCSVDepartment(department);
                    setCSVStep(1);
                    setCSVPreview(null);
                    setCSVError("");
                    setCSV("");
                    setCSVName("");
                  }}
                >
                  <Upload className="size-3.5" />
                  导入 CSV
                </Button>
                <Button
                  size="sm"
                  disabled={!canEdit}
                  title={editReason}
                  onClick={() => openEditor("person", { dev_group_id: group })}
                >
                  <Plus className="size-4" />
                  新增人员
                </Button>
              </div>
            </div>
            <div className="overflow-x-auto rounded-xl border bg-card">
              <table className={tableClass}>
                <thead className="border-b bg-muted/30">
                  <tr>
                    <th>人员</th>
                    <th>所属部门 / 研发组</th>
                    <th>责任角色</th>
                    <th>待处置</th>
                    <th>
                      <span className="sr-only">操作</span>
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {displayPeople.map((p) => (
                    <tr
                      key={p.id}
                      className={`cursor-pointer transition-colors ${highlightClass("person", p.id)}`}
                      tabIndex={0}
                      aria-label={`编辑人员 ${p.name}`}
                      onClick={() => openEditor("person", p)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && e.target === e.currentTarget) openEditor("person", p);
                      }}
                    >
                      <td className="min-w-48">
                        <div className="flex items-center gap-3">
                          <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
                            {p.name.slice(0, 1)}
                          </span>
                          <span>
                            <span className="font-medium">{p.name}</span>
                            {!p.active && (
                              <Badge variant="secondary" className="ml-2">
                                已停用
                              </Badge>
                            )}
                            <span className="mt-1 block text-muted-foreground text-xs">{p.email}</span>
                          </span>
                        </div>
                      </td>
                      <td className="min-w-36">
                        {departmentName(p.department_id)}
                        <span className="mt-1 block text-muted-foreground text-xs">
                          {directory.groups.find((g) => g.id === p.dev_group_id)?.name || "未加入研发组"}
                        </span>
                      </td>
                      <td className="max-w-60">
                        <div className="flex flex-wrap gap-1">
                          {p.roles.length ? (
                            [...new Set(p.roles.map((r) => roleNames[r.kind] || r.kind))].map((role) => (
                              <Badge key={role} variant="secondary" className="text-xs">
                                {role}
                              </Badge>
                            ))
                          ) : (
                            <span className="text-muted-foreground text-xs">成员</span>
                          )}
                        </div>
                      </td>
                      <td className="tabular-nums">
                        <Link
                          href={`/function/findings?owner_id=${p.id}&active_only=true`}
                          aria-label={`查看${p.name}负责的 ${p.open} 个待处置漏洞`}
                          className="inline-flex min-h-8 min-w-8 items-center rounded text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary focus-visible:outline-2 focus-visible:outline-primary"
                          onClick={(event) => event.stopPropagation()}
                        >
                          {p.open}
                        </Link>
                      </td>
                      <td>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          disabled={!canEdit}
                          title={editReason || `编辑${p.name}`}
                          aria-label={`编辑${p.name}`}
                          onClick={(e) => {
                            e.stopPropagation();
                            openEditor("person", p);
                          }}
                        >
                          <Pencil className="size-3.5" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {!displayPeople.length && (
                <div className="p-12 text-center text-muted-foreground">
                  <Users className="mx-auto mb-3 size-8 opacity-40" />
                  <p>没有符合条件的人员</p>
                </div>
              )}
            </div>
          </main>
        </div>
      )}
      {tab === "assets" && (
        <OrgAssets
          directory={directory}
          onChanged={() => {
            refreshOrganizationStatistics();
            return load();
          }}
        />
      )}
      {tab !== "assets" && (
        <section className="rounded-xl border bg-card p-5">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex gap-3">
              <ShieldCheck className="mt-0.5 size-5 text-muted-foreground" />
              <div>
                <h2 className="font-medium">全局兜底处理人</h2>
                <p className="mt-1 text-muted-foreground text-xs">
                  业务线主负责人 → 备选负责人 → 部门负责人 → 兜底处理人
                </p>
              </div>
            </div>
            <div className="w-full sm:w-64">
              <SearchChoice
                label="全局兜底处理人"
                value={fallback ? [fallback] : []}
                options={directory.people
                  .filter((p) => p.active)
                  .map((p) => ({ id: p.id, name: p.name, detail: p.email }))}
                onChange={(ids) => setFallback(ids[0] || null)}
                disabled={!canEdit || fallbackSaving}
                placeholder="选择已启用人员"
              />
            </div>
          </div>
          {fallback !== directory.fallback_id && (
            <div className="mt-4 flex items-center justify-end gap-2 border-t pt-4">
              <span className="mr-auto text-muted-foreground text-xs">保存后对所有部门生效</span>
              <Button
                variant="outline"
                size="sm"
                disabled={fallbackSaving}
                onClick={() => setFallback(directory.fallback_id)}
              >
                取消
              </Button>
              <Button size="sm" disabled={!fallback || fallbackSaving} onClick={() => void saveFallback()}>
                {fallbackSaving ? "保存中…" : "保存"}
              </Button>
            </div>
          )}
        </section>
      )}
      <Sheet
        open={!!editor}
        onOpenChange={(open) => {
          if (!open) closeEditor();
        }}
      >
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-[600px]"
          showCloseButton={false}
          onInteractOutside={(event) => {
            // Nested dialogs/popovers portal outside the drawer. Their focus restoration
            // must not be interpreted as another request to discard this draft.
            const target = event.detail.originalEvent.target as Element | null;
            const onOverlay = !!target?.closest?.("[data-slot='sheet-overlay']");
            if (!onOverlay || inline || discard || confirmation || audit || radixOverlayWasOpenAtPointerDown()) {
              event.preventDefault();
            }
          }}
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            const first = mainForm.current?.querySelector<HTMLElement>(`#org-${focusField}`);
            first?.focus();
          }}
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            opener.current?.focus();
          }}
        >
          <SheetHeader className="shrink-0 border-b px-6 py-5">
            <div className="flex items-center justify-between gap-4">
              <SheetTitle>
                {editor ? `${editor.draft.id ? "编辑" : "新增"}${kindNames[editor.kind]}` : "组织配置"}
              </SheetTitle>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="关闭编辑抽屉"
                onClick={() => closeEditor()}
                disabled={saving}
              >
                <X className="size-4" />
              </Button>
            </div>
            <SheetDescription>
              {editor?.draft.id ? "修改将在保存后生效，历史归属记录保留。" : "填写信息后保存。"}
            </SheetDescription>
          </SheetHeader>
          <div ref={mainForm} className="flex-1 overflow-y-auto px-6 py-6">
            {formError && (
              <p role="alert" className="mb-5 rounded-lg bg-destructive/10 p-3 text-destructive text-sm">
                {formError}
              </p>
            )}
            {editor && (
              <EditorFields
                kind={editor.kind}
                draft={editor.draft}
                directory={directory}
                prefix="org"
                errors={editor.errors}
                codeManual={editor.codeManual}
                setCodeManual={(manual) => setEditor((s) => s && { ...s, codeManual: manual })}
                change={(patch) => updateDraft(patch)}
                blur={(field) => void blur(field)}
                onInline={openInline}
                disabled={saving || !canEdit}
              />
            )}
            {editor?.kind === "person" && editor.draft.id > 0 && (
              <div className="mt-6 space-y-3 border-t pt-5">
                <h3 className="font-medium">当前责任角色</h3>
                {directory.people
                  .find((p) => p.id === editor.draft.id)
                  ?.roles.map((r) => (
                    <div key={`${r.kind}:${r.object_id}`} className="flex items-center justify-between text-sm">
                      <span>
                        {r.name}
                        <span className="ml-2 text-muted-foreground text-xs">{roleNames[r.kind] || r.kind}</span>
                      </span>
                      <span className="text-muted-foreground text-xs">{r.open} 个待处置</span>
                    </div>
                  ))}
                {!directory.people.find((p) => p.id === editor.draft.id)?.roles.length && (
                  <p className="text-muted-foreground text-xs">成员，无负责人角色。</p>
                )}
              </div>
            )}
            {editor && editor.draft.id > 0 && (
              <div className="mt-6 flex flex-wrap gap-2 border-t pt-5">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={(event) =>
                    void openHistory(editor.kind, editor.draft.id, editor.draft.name, event.currentTarget)
                  }
                >
                  <History className="size-3.5" />
                  变更记录
                </Button>
                {editor.kind === "business_line" && editor.draft.active && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!canEdit || saving || dirty(editor)}
                    title={dirty(editor) ? "请先保存或取消当前修改" : editReason}
                    onClick={() => void prepareAction("migrate")}
                  >
                    迁移业务线
                  </Button>
                )}
                {editor.draft.active && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="text-destructive"
                    disabled={
                      !canEdit ||
                      saving ||
                      dirty(editor) ||
                      (editor.kind === "dev_group" && !!directory.groups.find((g) => g.id === editor.draft.id)?.members)
                    }
                    title={
                      !canEdit
                        ? editReason
                        : dirty(editor)
                          ? "请先保存或取消当前修改"
                          : editor.kind === "dev_group" &&
                              directory.groups.find((g) => g.id === editor.draft.id)?.members
                            ? "研发组仍有成员，请先将成员移走"
                            : undefined
                    }
                    onClick={() => void prepareAction("disable")}
                  >
                    停用{kindNames[editor.kind]}
                  </Button>
                )}
                {!editor.draft.active && <Badge variant="secondary">已停用，历史记录保留</Badge>}
              </div>
            )}
          </div>
          <footer className="flex shrink-0 items-center justify-end gap-3 border-t bg-background px-6 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
            <Button variant="outline" onClick={() => closeEditor()} disabled={saving}>
              取消
            </Button>
            <Button
              onClick={() => void prepareSave()}
              disabled={saving || !canEdit || !dirty(editor)}
              title={editReason}
            >
              {saving ? (
                <>
                  <Loader2 className="size-4 animate-spin" />
                  保存中…
                </>
              ) : (
                "保存"
              )}
            </Button>
          </footer>
        </SheetContent>
      </Sheet>

      <Dialog
        open={!!inline}
        onOpenChange={(open) => {
          if (!open) closeEditor(true);
        }}
      >
        <DialogContent
          className="sm:max-w-lg"
          showCloseButton={false}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            document.getElementById(`org-${inlineReturnField.current}`)?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>{inline ? `新增${kindNames[inline.kind]}` : "新增"}</DialogTitle>
            <DialogDescription>保存后自动选中，原表单内容会保留。</DialogDescription>
          </DialogHeader>
          <div ref={inlineForm}>
            {inlineError && (
              <p role="alert" className="mb-4 text-destructive text-sm">
                {inlineError}
              </p>
            )}
            {inline && (
              <EditorFields
                kind={inline.kind}
                draft={inline.draft}
                directory={directory}
                prefix="inline"
                errors={inline.errors}
                codeManual={inline.codeManual}
                setCodeManual={(manual) => setInline((s) => s && { ...s, codeManual: manual })}
                change={(patch) => updateDraft(patch, true)}
                blur={(field) => void blur(field, true)}
                disabled={saving}
              />
            )}
          </div>
          <div className="mt-3 flex justify-end gap-2 border-t pt-4">
            <Button variant="outline" onClick={() => closeEditor(true)} disabled={saving}>
              取消
            </Button>
            <Button disabled={saving || !dirty(inline)} onClick={() => void prepareSave(true)}>
              {saving ? "保存中…" : "保存并选中"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!discard}
        onOpenChange={(open) => {
          if (!open) setDiscard(null);
        }}
      >
        <DialogContent
          showCloseButton={false}
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            continueButton.current?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const form = inline ? inlineForm.current : mainForm.current;
            if (editor) form?.querySelector<HTMLElement>("input:not(:disabled), button:not(:disabled)")?.focus();
            else opener.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>放弃修改？</DialogTitle>
            <DialogDescription>尚未保存的内容将丢失。</DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button ref={continueButton} variant="outline" onClick={() => setDiscard(null)}>
              继续编辑
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (discard === "inline") setInline(null);
                else setEditor(null);
                setDiscard(null);
              }}
            >
              放弃
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!confirmation}
        onOpenChange={(open) => {
          if (!open && !saving) setConfirmation(null);
        }}
      >
        <DialogContent className="sm:max-w-lg" showCloseButton={!saving}>
          {confirmation &&
            (() => {
              const c = confirmation;
              const changeOwner = c.command.action === "save";
              const person = c.command.kind === "person";
              const migration = c.command.action === "migrate";
              const receivingDepartment = migration || c.command.kind === "department";
              const needsReceiver =
                !changeOwner &&
                (migration ||
                  c.impact.open > 0 ||
                  c.impact.assets > 0 ||
                  c.impact.business_lines.length > 0 ||
                  c.impact.roles.length > 0);
              const options = person
                ? directory.people
                    .filter((p) => p.active && p.id !== c.command.id)
                    .map((p) => ({ id: p.id, name: p.name, detail: p.email }))
                : receivingDepartment
                  ? directory.departments
                      .filter((d) => d.active && d.id !== (migration ? editor?.draft.department_id : c.command.id))
                      .map((d) => ({ id: d.id, name: d.name }))
                  : directory.business_lines
                      .filter((line) => line.active && line.id !== c.command.id)
                      .map((line) => ({ id: line.id, name: line.name, detail: departmentName(line.department_id) }));
              const actionLabel = changeOwner
                ? "保存负责人变更"
                : migration
                  ? `迁移 ${c.impact.assets} 个资产`
                  : c.command.kind === "department" && needsReceiver
                    ? "迁移并停用"
                    : needsReceiver
                      ? `停用并转交 ${c.impact.open} 个漏洞`
                      : `停用${kindNames[c.command.kind as EditKind]}`;
              return (
                <>
                  <DialogHeader>
                    <DialogTitle>{c.label}</DialogTitle>
                    <DialogDescription>
                      {changeOwner
                        ? `将影响 ${c.impact.open} 个待处置漏洞的处理人，历史事件归属保持不变。`
                        : `涉及 ${c.impact.assets} 个资产、${c.impact.open} 个待处置漏洞。变更会保留生效时间和交接记录。`}
                    </DialogDescription>
                  </DialogHeader>
                  {(c.impact.business_lines.length > 0 || c.impact.roles.length > 0) && (
                    <div className="max-h-44 space-y-2 overflow-y-auto rounded-lg bg-muted/40 p-3">
                      {[...c.impact.business_lines, ...c.impact.roles].map((role, index) => (
                        <div
                          key={`${role.kind}:${role.object_id}:${index}`}
                          className="flex items-center justify-between gap-3 text-sm"
                        >
                          <span>
                            {role.name}
                            <span className="ml-2 text-muted-foreground text-xs">
                              {roleNames[role.kind] || role.kind}
                            </span>
                          </span>
                          <span className="text-muted-foreground text-xs">{role.open} 个待处置</span>
                        </div>
                      ))}
                    </div>
                  )}
                  {changeOwner ? (
                    <fieldset className="space-y-3">
                      <legend className="mb-3 font-medium text-sm">生效范围</legend>
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3">
                        <input
                          type="radio"
                          name="scope"
                          className="mt-1"
                          checked={c.scope === "future_findings"}
                          onChange={() => setConfirmation({ ...c, scope: "future_findings" })}
                        />
                        <span>
                          <span className="block text-sm">仅对新漏洞生效</span>
                          <span className="mt-1 block text-muted-foreground text-xs">已发现漏洞继续沿用原负责人。</span>
                        </span>
                      </label>
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3">
                        <input
                          type="radio"
                          name="scope"
                          className="mt-1"
                          checked={c.scope === "all_open"}
                          onChange={() => setConfirmation({ ...c, scope: "all_open" })}
                        />
                        <span>
                          <span className="block text-sm">同时更新存量待处置漏洞</span>
                          <span className="mt-1 block text-muted-foreground text-xs">
                            已发生事件与已生成报告保留原归属。
                          </span>
                        </span>
                      </label>
                    </fieldset>
                  ) : (
                    needsReceiver && (
                      <div className="space-y-2">
                        <Label>{person ? "交接人" : receivingDepartment ? "接收部门" : "接收业务线"}</Label>
                        <SearchChoice
                          label={person ? "交接人" : receivingDepartment ? "接收部门" : "接收业务线"}
                          value={c.receiver ? [c.receiver] : []}
                          options={options}
                          onChange={(ids) => setConfirmation({ ...c, receiver: ids[0] || null, error: "" })}
                          placeholder="请选择接收方"
                        />
                        {person && c.receiver && (
                          <p className="text-muted-foreground text-xs">
                            若交接人已是某业务线备选，将提升为主负责人并清空该备选位置。
                          </p>
                        )}
                      </div>
                    )
                  )}
                  {c.error && (
                    <p role="alert" className="text-destructive text-sm">
                      {c.error}
                    </p>
                  )}
                  <div className="mt-2 flex justify-end gap-2">
                    <Button variant="outline" disabled={saving} onClick={() => setConfirmation(null)}>
                      返回编辑
                    </Button>
                    <Button
                      variant={changeOwner || migration ? "default" : "destructive"}
                      disabled={saving || (needsReceiver && !c.receiver)}
                      onClick={() =>
                        void submit({
                          ...c.command,
                          scope: c.scope,
                          receiver_id: c.receiver || undefined,
                          confirm_impact: true,
                        })
                      }
                    >
                      {saving ? "处理中…" : actionLabel}
                    </Button>
                  </div>
                </>
              );
            })()}
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!audit}
        onOpenChange={(open) => {
          if (!open) setAudit(null);
        }}
      >
        <DialogContent
          className="sm:max-w-2xl"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            auditOpener.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>{audit?.name} · 变更记录</DialogTitle>
            <DialogDescription>操作人、变更内容与生效时间</DialogDescription>
          </DialogHeader>
          {audit?.error ? (
            <p role="alert" className="text-destructive">
              {audit.error}
            </p>
          ) : !audit?.items ? (
            <div className="h-40 animate-pulse rounded-lg bg-muted" />
          ) : !audit.items.length ? (
            <p className="py-8 text-center text-muted-foreground">暂无变更记录</p>
          ) : (
            <ol className="max-h-[60vh] space-y-5 overflow-y-auto">
              {audit.items.map((item) => (
                <li key={item.id} className="rounded-lg border p-4">
                  <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
                    <span className="font-medium">
                      {item.actor_label} · {actionNames[item.action] || item.action}
                    </span>
                    <time className="text-muted-foreground text-xs">
                      生效于 {new Date(item.effective_at).toLocaleString("zh-CN")}
                    </time>
                  </div>
                  <div className="mt-3 space-y-2 text-xs">
                    {Object.keys({ ...item.old_value, ...item.new_value })
                      .filter((key) => {
                        if (!(key in auditFieldNames)) return false;
                        const values = [item.old_value[key], item.new_value[key]].map((value) => {
                          if (value == null || value === "") return null;
                          if (Array.isArray(value)) return value.length ? [...value].sort() : null;
                          if (typeof value === "object" && !Object.keys(value).length) return null;
                          return value;
                        });
                        return JSON.stringify(values[0]) !== JSON.stringify(values[1]);
                      })
                      .map((key) => {
                        const show = (value: unknown) => {
                          if (value == null || value === "" || (Array.isArray(value) && !value.length)) return "未设置";
                          if (key === "group_ids" && Array.isArray(value))
                            return value
                              .map((id) => {
                                const group = directory.groups.find((g) => g.id === id);
                                return group ? `${group.name}${group.active ? "" : "（已停用）"}` : `研发组 #${id}`;
                              })
                              .join("、");
                          if (key === "channel_ids" && Array.isArray(value))
                            return value.map((id) => `渠道 #${id}`).join("、");
                          if (key === "scope")
                            return value === "future_findings" ? "仅对新漏洞生效" : "同时处理存量漏洞";
                          if (key === "sla" && typeof value === "object")
                            return Object.entries(value)
                              .map(
                                ([level, days]) =>
                                  `${({ critical: "严重", high: "高危", medium: "中危", low: "低危" } as Record<string, string>)[level] ?? level} ${days} 天`,
                              )
                              .join(" · ");
                          if (typeof value === "boolean") return value ? "启用" : "停用";
                          if (key === "department_id" && typeof value === "number") return departmentName(value);
                          if (
                            ["head_id", "primary_id", "owner_id", "backup_id"].includes(key) &&
                            typeof value === "number"
                          )
                            return personName(value);
                          if (key === "dev_group_id" && typeof value === "number")
                            return directory.groups.find((g) => g.id === value)?.name || `#${value}`;
                          return typeof value === "object" ? JSON.stringify(value) : String(value);
                        };
                        return (
                          <div key={key} className="grid grid-cols-[90px_1fr] gap-2">
                            <span className="text-muted-foreground">{auditFieldNames[key] || key}</span>
                            <span className="break-all">
                              {show(item.old_value[key])} → {show(item.new_value[key])}
                            </span>
                          </div>
                        );
                      })}
                  </div>
                </li>
              ))}
            </ol>
          )}
        </DialogContent>
      </Dialog>

      <Dialog
        open={csvOpen}
        onOpenChange={(open) => {
          if (!csvBusy) setCSVOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-3xl" showCloseButton={!csvBusy}>
          <DialogHeader>
            <DialogTitle>批量导入人员</DialogTitle>
            <DialogDescription>先预览并核对，再确认写入。按邮箱匹配已有人员。</DialogDescription>
          </DialogHeader>
          <ol className="grid grid-cols-4 gap-2 text-xs">
            {["下载模板", "上传文件", "预览检查", "确认导入"].map((title, index) => (
              <li
                key={title}
                className={`rounded-lg px-2 py-2 text-center ${csvStep === index + 1 ? "bg-primary/10 font-medium text-primary" : "bg-muted/50 text-muted-foreground"}`}
              >
                {index + 1}. {title}
              </li>
            ))}
          </ol>
          {csvStep < 3 && (
            <div className="space-y-5 rounded-lg border p-4">
              <a
                download="人员导入模板.csv"
                href={`data:text/csv;charset=utf-8,${encodeURIComponent("\uFEFFname,email,department,dev_group\n张三,zhangsan@example.com,,\n")}`}
                onClick={() => setCSVStep(2)}
                className="inline-flex items-center gap-2 text-primary text-sm underline underline-offset-4"
              >
                <Download className="size-4" />
                下载 CSV 模板
              </a>
              <div className="space-y-2">
                <Label>默认所属部门（文件未填写时使用）</Label>
                <SearchChoice
                  label="默认所属部门"
                  value={csvDepartment ? [csvDepartment] : []}
                  options={directory.departments.filter((d) => d.active).map((d) => ({ id: d.id, name: d.name }))}
                  onChange={(ids) => setCSVDepartment(ids[0] || null)}
                />
              </div>
              <label className="flex cursor-pointer flex-col items-center rounded-lg border border-dashed px-5 py-7 text-sm hover:bg-muted/30">
                <Upload className="mb-3 size-6 text-muted-foreground" />
                <span>{csvName || "选择 CSV 文件"}</span>
                <span className="mt-1 text-muted-foreground text-xs">UTF-8 编码，最多 1 MB</span>
                <input
                  type="file"
                  accept=".csv,text/csv"
                  className="mt-3 max-w-full text-xs"
                  aria-label="上传人员 CSV"
                  disabled={csvBusy}
                  onChange={(e) => {
                    const file = e.target.files?.[0];
                    if (!file) return;
                    if (file.size > 1024 * 1024) {
                      setCSVError("文件超过 1 MB，请拆分后上传");
                      return;
                    }
                    setCSVName(file.name);
                    setCSVStep(2);
                    setCSVError("");
                    void file
                      .text()
                      .then((text) => setCSV(text))
                      .catch(() => setCSVError("读取文件失败，请重新选择 UTF-8 CSV 文件"));
                  }}
                />
              </label>
            </div>
          )}
          {csvStep === 3 && csvPreview && (
            <>
              <div className="flex gap-3">
                <Badge variant="secondary">新增 {csvPreview.created}</Badge>
                <Badge variant="secondary">更新 {csvPreview.updated}</Badge>
                <Badge variant={csvPreview.errors ? "destructive" : "outline"}>错误 {csvPreview.errors}</Badge>
              </div>
              <div className="max-h-[45vh] overflow-auto rounded-lg border">
                <table className={tableClass}>
                  <thead className="sticky top-0 bg-background">
                    <tr>
                      <th>行</th>
                      <th>操作</th>
                      <th>姓名 / 邮箱</th>
                      <th>检查结果</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y">
                    {csvPreview.rows.map((row) => (
                      <tr key={row.row}>
                        <td>{row.row}</td>
                        <td>{{ create: "新增", update: "更新", error: "错误" }[row.action]}</td>
                        <td>
                          {row.data?.name}
                          <span className="block text-muted-foreground text-xs">{row.data?.email}</span>
                        </td>
                        <td className={row.error ? "text-destructive" : "text-muted-foreground"}>
                          {row.error || row.warning || "可导入"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
          {csvStep === 4 && (
            <div className="py-9 text-center">
              <span className="mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-emerald-100 text-emerald-700">
                <Check className="size-6" />
              </span>
              <p className="font-medium">导入完成</p>
              <p className="mt-2 text-muted-foreground text-sm">人员列表已更新，变更已写入记录。</p>
            </div>
          )}
          {csvError && (
            <p role="alert" className="text-destructive text-sm">
              {csvError}
            </p>
          )}
          <div className="flex justify-end gap-2">
            {csvStep === 3 && (
              <Button
                variant="outline"
                disabled={csvBusy}
                onClick={() => {
                  setCSVStep(2);
                  setCSVPreview(null);
                }}
              >
                重新上传
              </Button>
            )}
            <Button variant="outline" disabled={csvBusy} onClick={() => setCSVOpen(false)}>
              {csvStep === 4 ? "关闭" : "取消"}
            </Button>
            {csvStep < 3 && (
              <Button disabled={!csv || csvBusy} onClick={() => void previewCSV()}>
                {csvBusy ? "检查中…" : "预览导入"}
              </Button>
            )}
            {csvStep === 3 && (
              <Button
                disabled={
                  csvBusy || !csvPreview || csvPreview.errors > 0 || csvPreview.created + csvPreview.updated === 0
                }
                onClick={() => void importCSV()}
              >
                {csvBusy ? "导入中…" : `确认导入 ${(csvPreview?.created || 0) + (csvPreview?.updated || 0)} 人`}
              </Button>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

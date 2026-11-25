"use client";

import { useEffect, useRef, useState } from "react";

import Link from "next/link";

import {
  ArrowRight,
  Building2,
  Check,
  Download,
  Mail,
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
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { findingSeverityLabels } from "@/lib/finding-metrics";
import {
  type BusinessLine,
  type ResponsibilityDirectory,
  type ResponsibilityParty,
  responsibilityLink,
} from "@/lib/responsibility";
import type { NotificationChannel } from "@/lib/types";

const newLine = (): BusinessLine => ({
  id: 0,
  name: "",
  code: "",
  aliases: [],
  primary_id: null,
  backup_id: null,
  team_ids: [],
  channel_ids: [],
  sla: null,
  rules: [],
  active: true,
  open: 0,
});
const newParty = (kind: "person" | "team"): ResponsibilityParty => ({
  id: 0,
  kind,
  name: "",
  aliases: [],
  email: "",
  team_id: null,
  lead_id: null,
  active: true,
  open: 0,
});

function PersonChoice({
  label,
  value,
  people,
  onChange,
  empty = "暂不设置",
}: {
  label: string;
  value: number | null;
  people: ResponsibilityParty[];
  onChange: (id: number | null) => void;
  empty?: string;
}) {
  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <Select value={value ? String(value) : "none"} onValueChange={(v) => onChange(v === "none" ? null : Number(v))}>
        <SelectTrigger aria-label={label} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">{empty}</SelectItem>
          {people
            .filter((p) => p.active || p.id === value)
            .map((p) => (
              <SelectItem key={p.id} value={String(p.id)}>
                {p.name}
                {!p.active && "（已停用）"}
              </SelectItem>
            ))}
        </SelectContent>
      </Select>
    </div>
  );
}

export default function ResponsibilityPage() {
  const [directory, setDirectory] = useState<ResponsibilityDirectory>();
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [tab, setTab] = useState("lines");
  const [query, setQuery] = useState("");
  const [team, setTeam] = useState<number | null>(null);
  const [line, setLine] = useState<BusinessLine | null>(null);
  const [party, setParty] = useState<ResponsibilityParty | null>(null);
  const [fallback, setFallback] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [csvRows, setCSVRows] = useState<BusinessLine[] | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  async function load() {
    const [data, notifications] = await Promise.all([api.responsibilityDirectory(), api.notifyChannels()]);
    setDirectory(data);
    setFallback(data.fallback_id);
    setChannels(notifications);
    setError("");
  }
  // biome-ignore lint/correctness/useExhaustiveDependencies: load initial directory once.
  useEffect(() => {
    void load().catch((e: Error) => setError(e.message));
  }, []);

  async function saveLine() {
    if (!line) return;
    setSaving(true);
    try {
      const result = await api.saveBusinessLine(line);
      await load();
      setLine(null);
      toast.success("业务线已保存");
      if (!result.metrics_updated) toast.warning("归属已保存，请到漏洞治理页更新统计");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  }
  async function saveParty() {
    if (!party) return;
    setSaving(true);
    try {
      const result = await api.saveResponsibilityParty(party);
      await load();
      setParty(null);
      toast.success(
        result.result.transferred ? `已保存，${result.result.transferred} 个待处置漏洞已转交` : "人员与团队配置已保存",
      );
      if (!result.metrics_updated) toast.warning("请到漏洞治理页更新统计");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  }
  async function applyRules() {
    setSaving(true);
    try {
      const { result } = await api.applyResponsibilityRules();
      await load();
      toast.success(`已归属 ${result.assigned} 个漏洞；${result.unmatched} 个未匹配，${result.conflicts} 个规则冲突`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  }
  async function readCSV(file: File) {
    if (file.size > 65536) {
      toast.error("CSV 文件最大 64 KB");
      return;
    }
    try {
      const text = (await file.text()).replace(/^\uFEFF/, "");
      const rows: string[][] = [];
      let row: string[] = [],
        cell = "",
        quoted = false;
      for (let i = 0; i < text.length; i++) {
        const c = text[i];
        if (c === '"') {
          if (quoted && text[i + 1] === '"') {
            cell += '"';
            i++;
          } else quoted = !quoted;
        } else if (c === "," && !quoted) {
          row.push(cell.trim());
          cell = "";
        } else if ((c === "\n" || c === "\r") && !quoted) {
          row.push(cell.trim());
          if (row.some(Boolean)) rows.push(row);
          row = [];
          cell = "";
          if (c === "\r" && text[i + 1] === "\n") i++;
        } else cell += c;
      }
      if (quoted) throw new Error("CSV 引号未闭合");
      row.push(cell.trim());
      if (row.some(Boolean)) rows.push(row);
      const header = rows.shift() ?? [];
      const nameIndex = header.findIndex((v) => ["name", "名称", "业务线"].includes(v));
      const codeIndex = header.findIndex((v) => ["code", "标识"].includes(v));
      if (nameIndex < 0 || codeIndex < 0 || rows.length < 1 || rows.length > 100)
        throw new Error("请使用 name,code 两列，填写 1 至 100 条业务线");
      const names = new Set<string>(),
        codes = new Set<string>();
      const imported = rows.map((values, i) => {
        const name = values[nameIndex] ?? "",
          code = values[codeIndex] ?? "";
        if (!name || name.length > 120 || name.startsWith("__") || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(code))
          throw new Error(`第 ${i + 2} 行名称或标识格式不正确`);
        if (
          names.has(name) ||
          codes.has(code) ||
          directory?.business_lines.some((b) => b.name === name || b.aliases.includes(name) || b.code === code)
        )
          throw new Error(`第 ${i + 2} 行名称或标识重复`);
        names.add(name);
        codes.add(code);
        return { ...newLine(), name, code };
      });
      setCSVRows(imported);
    } catch (e) {
      toast.error((e as Error).message);
    }
  }
  async function importCSV() {
    if (!csvRows) return;
    setSaving(true);
    let count = 0;
    try {
      for (const row of csvRows) {
        await api.saveBusinessLine(row);
        count++;
      }
      setCSVRows(null);
      toast.success(`已导入 ${count} 条业务线`);
    } catch (e) {
      setCSVRows(csvRows.slice(count));
      toast.error(`已导入 ${count} 条，余下未导入：${(e as Error).message}`);
    } finally {
      await load().catch((e: Error) => setError(e.message));
      setSaving(false);
    }
  }

  const parties = directory?.parties ?? [];
  const people = parties.filter((p) => p.kind === "person");
  const teams = parties.filter((p) => p.kind === "team");
  const nameOf = (id: number | null) => parties.find((p) => p.id === id)?.name ?? "—";
  const lines = directory?.business_lines ?? [];
  const visibleLines = lines.filter((b) =>
    `${b.name} ${b.code} ${nameOf(b.primary_id)}`.toLowerCase().includes(query.toLowerCase()),
  );
  const visiblePeople = people.filter(
    (p) => (team === null || p.team_id === team) && `${p.name} ${p.email}`.toLowerCase().includes(query.toLowerCase()),
  );

  return (
    <div className="mx-auto w-full max-w-[1500px] space-y-6 p-2 md:p-3">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
            <Building2 className="size-5" />
          </div>
          <div>
            <h1 className="font-semibold text-xl tracking-tight">业务线与负责人</h1>
            <p className="mt-1 text-muted-foreground text-xs">归属配置</p>
          </div>
          {process.env.NEXT_PUBLIC_MOCK === "1" && <Badge variant="secondary">示例数据</Badge>}
        </div>
        <Button variant="outline" asChild>
          <Link href="/function/remediation">
            漏洞治理
            <ArrowRight className="ml-2 size-4" />
          </Link>
        </Button>
      </div>
      {error && (
        <div
          role="alert"
          className="flex items-center justify-between rounded-lg border bg-destructive/5 p-4 text-destructive text-sm"
        >
          {error}
          <Button variant="outline" onClick={() => void load().catch((e: Error) => setError(e.message))}>
            重试
          </Button>
        </div>
      )}
      <Tabs
        value={tab}
        onValueChange={(v) => {
          setTab(v);
          setQuery("");
        }}
      >
        <TabsList>
          <TabsTrigger value="lines">
            <Building2 className="mr-1.5 size-4" />
            业务线 <span className="ml-1.5 text-xs opacity-60">{lines.length}</span>
          </TabsTrigger>
          <TabsTrigger value="people">
            <Users className="mr-1.5 size-4" />
            人员与团队
          </TabsTrigger>
        </TabsList>
      </Tabs>
      {!directory && !error && (
        <div className="rounded-xl border p-12 text-center text-muted-foreground text-sm">正在加载归属配置…</div>
      )}
      {directory && (
        <>
          {(directory.unassigned > 0 || directory.ownerless > 0) && (
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg border border-amber-200 bg-amber-50/70 px-4 py-3 text-sm dark:border-amber-900 dark:bg-amber-950/20">
              <ShieldCheck className="size-4 text-amber-600" />
              <Link className="hover:underline" href={responsibilityLink("findings", "__none__")}>
                <strong>{directory.unassigned}</strong> 个漏洞业务线未归属
              </Link>
              <Link
                className="text-muted-foreground hover:underline"
                href={responsibilityLink("findings", "", "__none__")}
              >
                {directory.ownerless} 个负责人未指派
              </Link>
              {directory.conflicts > 0 && (
                <span className="text-amber-700">其中 {directory.conflicts} 个资产规则冲突</span>
              )}
              <Button variant="ghost" size="sm" className="ml-auto" disabled={saving} onClick={() => void applyRules()}>
                按规则归属
                <ArrowRight className="ml-1 size-3.5" />
              </Button>
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="relative w-full sm:w-72">
              <Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" />
              <Input
                aria-label="搜索归属配置"
                placeholder={tab === "lines" ? "搜索业务线、标识或负责人" : "搜索姓名或邮箱"}
                className="pl-9"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            <div className="flex gap-2">
              {tab === "lines" ? (
                <>
                  <input
                    ref={fileInput}
                    type="file"
                    accept=".csv,text/csv"
                    className="hidden"
                    aria-label="导入业务线 CSV"
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (file) void readCSV(file);
                      e.target.value = "";
                    }}
                  />
                  <Button variant="outline" onClick={() => fileInput.current?.click()}>
                    <Upload className="mr-1.5 size-4" />
                    导入 CSV
                  </Button>
                  <Button onClick={() => setLine(newLine())}>
                    <Plus className="mr-1.5 size-4" />
                    新增业务线
                  </Button>
                </>
              ) : (
                <>
                  <Button variant="outline" onClick={() => setParty(newParty("team"))}>
                    <Plus className="mr-1.5 size-4" />
                    新建团队
                  </Button>
                  <Button onClick={() => setParty(newParty("person"))}>
                    <Plus className="mr-1.5 size-4" />
                    添加人员
                  </Button>
                </>
              )}
            </div>
          </div>
          {tab === "lines" ? (
            <div className="overflow-x-auto rounded-xl border">
              <table className="w-full min-w-[820px] text-left text-sm">
                <thead className="bg-muted/45 text-muted-foreground">
                  <tr>
                    {["业务线", "主负责人", "备份负责人", "开发团队", "通知渠道", "待处置", ""].map((title, i) => (
                      <th key={title || i} className="px-5 py-3 font-medium">
                        {title}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {visibleLines.map((b) => (
                    <tr key={b.id} className={`transition-colors hover:bg-muted/30 ${!b.active ? "opacity-60" : ""}`}>
                      <td className="px-5 py-5">
                        <button
                          type="button"
                          onClick={() =>
                            setLine({ ...b, rules: b.rules.map((rule) => ({ ...rule, key: crypto.randomUUID() })) })
                          }
                          className="flex items-center gap-2 font-medium hover:underline"
                        >
                          {b.name}
                          {!b.active && <Badge variant="secondary">已停用</Badge>}
                        </button>
                        <div className="mt-1 font-mono text-muted-foreground text-xs">{b.code}</div>
                      </td>
                      <td className="px-5 py-4">
                        <span className="inline-flex items-center gap-2">
                          {b.primary_id && (
                            <span className="flex size-7 items-center justify-center rounded-full bg-primary/10 text-primary text-xs">
                              {nameOf(b.primary_id).slice(0, 1)}
                            </span>
                          )}
                          {nameOf(b.primary_id)}
                        </span>
                      </td>
                      <td className="px-5 py-4 text-muted-foreground">{nameOf(b.backup_id)}</td>
                      <td className="px-5 py-4">
                        <div className="flex max-w-48 flex-wrap gap-1">
                          {b.team_ids.length
                            ? b.team_ids.map((id) => (
                                <Badge key={id} variant="secondary" className="font-normal">
                                  {nameOf(id)}
                                </Badge>
                              ))
                            : "—"}
                        </div>
                      </td>
                      <td className="px-5 py-4">
                        <div className="flex flex-wrap gap-1">
                          {b.channel_ids.length ? (
                            b.channel_ids.map((id) => (
                              <Badge variant="outline" key={id} className="font-normal">
                                {channels.find((c) => c.id === id)?.kind === "email" ? "邮件" : "企业微信"}
                              </Badge>
                            ))
                          ) : (
                            <span className="text-muted-foreground text-xs">全局渠道</span>
                          )}
                        </div>
                      </td>
                      <td className="px-5 py-4">
                        <Link
                          href={responsibilityLink("findings", b.name)}
                          className={`tabular-nums hover:underline ${b.open ? "font-semibold text-primary" : "text-muted-foreground"}`}
                        >
                          {b.open}
                        </Link>
                      </td>
                      <td className="px-5 py-4">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`编辑${b.name}`}
                          onClick={() =>
                            setLine({ ...b, rules: b.rules.map((rule) => ({ ...rule, key: crypto.randomUUID() })) })
                          }
                        >
                          <Pencil className="size-3.5" />
                          <span className="ml-1">编辑</span>
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {!visibleLines.length && (
                <div className="p-12 text-center text-muted-foreground text-sm">
                  {lines.length ? "没有匹配的业务线" : "添加业务线，开始管理漏洞归属"}
                </div>
              )}
            </div>
          ) : (
            <div className="grid gap-5 lg:grid-cols-[180px_minmax(0,1fr)]">
              <aside className="space-y-2 rounded-xl border p-3">
                <div className="px-3 py-2 font-medium text-muted-foreground text-xs">团队</div>
                <button
                  type="button"
                  onClick={() => setTeam(null)}
                  className={`flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-sm ${team === null ? "bg-primary/10 font-medium text-primary" : "hover:bg-muted"}`}
                >
                  <span>全部人员</span>
                  <span className="text-xs">{people.length}</span>
                </button>
                {teams.map((t) => (
                  <div key={t.id} className="group flex items-center">
                    <button
                      type="button"
                      onClick={() => setTeam(t.id)}
                      className={`flex min-w-0 flex-1 items-center justify-between rounded-lg px-3 py-2.5 text-sm ${team === t.id ? "bg-primary/10 font-medium text-primary" : "hover:bg-muted"}`}
                    >
                      <span className="truncate">
                        {t.name}
                        {!t.active && " · 停用"}
                      </span>
                      <span className="ml-2 text-xs">{people.filter((p) => p.team_id === t.id).length}</span>
                    </button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7"
                      aria-label={`编辑团队${t.name}`}
                      onClick={() => setParty(structuredClone(t))}
                    >
                      <Pencil className="size-3" />
                    </Button>
                  </div>
                ))}
              </aside>
              <div className="overflow-x-auto rounded-xl border">
                <table className="w-full min-w-[620px] text-left text-sm">
                  <thead className="bg-muted/45 text-muted-foreground">
                    <tr>
                      {["人员", "角色", "所属团队", "负责业务线", ""].map((title, i) => (
                        <th key={title || i} className="px-4 py-3 font-medium">
                          {title}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y">
                    {visiblePeople.map((p) => {
                      const primary = lines.filter((b) => b.primary_id === p.id),
                        backup = lines.filter((b) => b.backup_id === p.id);
                      return (
                        <tr key={p.id} className={`hover:bg-muted/30 ${p.active ? "" : "opacity-55"}`}>
                          <td className="px-4 py-5">
                            <div className="flex items-center gap-2.5">
                              <span className="flex size-9 items-center justify-center rounded-full bg-muted font-medium">
                                {p.name.slice(0, 1)}
                              </span>
                              <div>
                                <div className="font-medium">
                                  {p.name}
                                  {!p.active && <span className="ml-2 text-muted-foreground text-xs">已停用</span>}
                                </div>
                                <div className="mt-1 text-muted-foreground text-xs">{p.email || "未填写邮箱"}</div>
                              </div>
                            </div>
                          </td>
                          <td className="px-4 py-4">
                            <div className="flex flex-wrap gap-1">
                              {primary.length > 0 && <Badge variant="secondary">主负责人</Badge>}
                              {backup.length > 0 && <Badge variant="outline">备份</Badge>}
                              {directory.fallback_id === p.id && <Badge variant="outline">兜底</Badge>}
                              {teams.some((t) => t.lead_id === p.id) && <Badge variant="outline">团队负责人</Badge>}
                              {!primary.length &&
                                !backup.length &&
                                directory.fallback_id !== p.id &&
                                !teams.some((t) => t.lead_id === p.id) && (
                                  <span className="text-muted-foreground">成员</span>
                                )}
                            </div>
                          </td>
                          <td className="px-4 py-4">{nameOf(p.team_id)}</td>
                          <td className="px-4 py-4">
                            <div className="flex max-w-48 flex-wrap gap-1.5">
                              {[...primary, ...backup.filter((b) => b.primary_id !== p.id)].map((b) => (
                                <Link
                                  key={b.id}
                                  href={responsibilityLink("remediation", b.name)}
                                  className="text-primary text-xs hover:underline"
                                >
                                  {b.name}
                                </Link>
                              ))}
                              {!primary.length && !backup.length && <span className="text-muted-foreground">—</span>}
                            </div>
                          </td>
                          <td className="px-4 py-4">
                            <Button
                              size="icon"
                              variant="ghost"
                              aria-label={`编辑人员${p.name}`}
                              onClick={() => setParty(structuredClone(p))}
                            >
                              <Pencil className="size-3.5" />
                            </Button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                {!visiblePeople.length && (
                  <div className="p-12 text-center text-muted-foreground text-sm">暂无匹配人员</div>
                )}
              </div>
            </div>
          )}
          {tab === "people" && (
            <Card className="shadow-none">
              <CardContent className="flex flex-wrap items-end gap-5 p-5">
                <div className="min-w-56 flex-1">
                  <div className="mb-1 flex items-center gap-2 font-medium text-sm">
                    <ShieldCheck className="size-4" />
                    兜底负责人
                  </div>
                  <p className="text-muted-foreground text-xs">
                    新漏洞未命中业务线时自动指派；人员停用后，待处置漏洞转交给可用负责人。
                  </p>
                </div>
                <div className="w-52">
                  <PersonChoice
                    label="兜底人员"
                    value={fallback || null}
                    people={people}
                    onChange={(id) => setFallback(id ?? 0)}
                  />
                </div>
                <Button
                  variant="outline"
                  disabled={saving || fallback === directory.fallback_id}
                  onClick={async () => {
                    setSaving(true);
                    try {
                      await api.saveResponsibilityFallback(fallback);
                      await load();
                      toast.success("兜底负责人已保存");
                    } catch (e) {
                      toast.error((e as Error).message);
                    } finally {
                      setSaving(false);
                    }
                  }}
                >
                  保存
                </Button>
              </CardContent>
            </Card>
          )}
        </>
      )}

      <Sheet
        open={line !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setLine(null);
        }}
      >
        <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-[580px]">
          <SheetHeader className="border-b px-6 py-5">
            <SheetTitle>{line?.id ? "编辑业务线" : "新增业务线"}</SheetTitle>
            <SheetDescription>{line?.id ? line.code : "设置负责人、修复时限与资产归属规则"}</SheetDescription>
          </SheetHeader>
          {line && directory && (
            <>
              <div className="flex-1 space-y-7 overflow-y-auto px-6 py-6">
                <section className="space-y-4">
                  <h2 className="font-semibold text-sm">基本信息</h2>
                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <Label htmlFor="line-name">业务线名称</Label>
                      <Input
                        id="line-name"
                        maxLength={120}
                        value={line.name}
                        onChange={(e) => setLine({ ...line, name: e.target.value })}
                        placeholder="例如：支付业务"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="line-code">唯一标识</Label>
                      <Input
                        id="line-code"
                        maxLength={64}
                        value={line.code}
                        onChange={(e) => setLine({ ...line, code: e.target.value })}
                        placeholder="payment"
                      />
                    </div>
                  </div>
                  <div className="flex items-center justify-between">
                    <Label htmlFor="line-active">启用业务线</Label>
                    <Switch
                      id="line-active"
                      checked={line.active}
                      onCheckedChange={(active) => setLine({ ...line, active })}
                    />
                  </div>
                </section>
                <section className="space-y-4 border-t pt-5">
                  <h2 className="font-semibold text-sm">负责人</h2>
                  <div className="grid grid-cols-2 gap-4">
                    <PersonChoice
                      label="主负责人"
                      value={line.primary_id}
                      people={people}
                      onChange={(primary_id) => setLine({ ...line, primary_id })}
                    />
                    <PersonChoice
                      label="备份负责人"
                      value={line.backup_id}
                      people={people}
                      onChange={(backup_id) => setLine({ ...line, backup_id })}
                    />
                  </div>
                  <Label>开发团队</Label>
                  <div className="flex flex-wrap gap-2">
                    {teams
                      .filter((t) => t.active || line.team_ids.includes(t.id))
                      .map((t) => (
                        <Button
                          key={t.id}
                          size="sm"
                          variant={line.team_ids.includes(t.id) ? "secondary" : "outline"}
                          onClick={() =>
                            setLine({
                              ...line,
                              team_ids: line.team_ids.includes(t.id)
                                ? line.team_ids.filter((id) => id !== t.id)
                                : [...line.team_ids, t.id],
                            })
                          }
                        >
                          {line.team_ids.includes(t.id) && <Check className="mr-1 size-3" />}
                          {t.name}
                        </Button>
                      ))}
                    {!teams.length && <span className="text-muted-foreground text-xs">先在“人员与团队”添加团队</span>}
                  </div>
                </section>
                <section className="space-y-3 border-t pt-5">
                  <div className="flex items-center justify-between">
                    <h2 className="font-semibold text-sm">通知渠道</h2>
                    <Link href="/system/notify" className="text-primary text-xs hover:underline">
                      管理渠道 ↗
                    </Link>
                  </div>
                  <div className="space-y-2">
                    {channels
                      .filter(
                        (c) => ["email", "wecom"].includes(c.kind) && (c.enabled || line.channel_ids.includes(c.id)),
                      )
                      .map((c) => (
                        <label
                          key={c.id}
                          className="flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5 text-sm"
                        >
                          <input
                            type="checkbox"
                            checked={line.channel_ids.includes(c.id)}
                            onChange={(e) =>
                              setLine({
                                ...line,
                                channel_ids: e.target.checked
                                  ? [...line.channel_ids, c.id]
                                  : line.channel_ids.filter((id) => id !== c.id),
                              })
                            }
                          />
                          <Mail className="size-4 text-muted-foreground" />
                          <span>
                            {c.name}
                            {!c.enabled && "（已停用）"}
                          </span>
                          <Badge variant="outline" className="ml-auto font-normal">
                            {c.kind === "email" ? "邮件" : "企业微信"}
                          </Badge>
                        </label>
                      ))}
                  </div>
                  <p className="text-muted-foreground text-xs">
                    留空使用全局通知渠道；选中后仅向这些渠道推送，并遵循渠道筛选规则。
                  </p>
                </section>
                <section className="space-y-4 border-t pt-5">
                  <div className="flex items-center justify-between">
                    <h2 className="font-semibold text-sm">修复 SLA</h2>
                    <div className="flex items-center gap-2">
                      <Label htmlFor="inherit-sla" className="text-xs">
                        继承全局
                      </Label>
                      <Switch
                        id="inherit-sla"
                        checked={!line.sla}
                        onCheckedChange={(inherit) => setLine({ ...line, sla: inherit ? null : { ...directory.sla } })}
                      />
                    </div>
                  </div>
                  <div className="grid grid-cols-4 gap-3">
                    {Object.entries(findingSeverityLabels).map(([key, label]) => (
                      <div key={key} className="space-y-2">
                        <Label htmlFor={`sla-${key}`} className="text-xs">
                          {label}
                        </Label>
                        <div className="relative">
                          <Input
                            id={`sla-${key}`}
                            type="number"
                            min={1}
                            max={365}
                            disabled={!line.sla}
                            className="pr-7"
                            value={(line.sla ?? directory.sla)[key as keyof typeof directory.sla]}
                            onChange={(e) =>
                              setLine({
                                ...line,
                                sla: { ...(line.sla ?? directory.sla), [key]: Number(e.target.value) },
                              })
                            }
                          />
                          <span className="absolute top-2.5 right-2 text-muted-foreground text-xs">天</span>
                        </div>
                      </div>
                    ))}
                  </div>
                  <p className="text-muted-foreground text-xs">
                    从首次发现开始计时。变更从保存时生效，已关闭记录的达标结果保留。
                  </p>
                </section>
                <section className="space-y-3 border-t pt-5">
                  <div className="flex items-center justify-between">
                    <h2 className="font-semibold text-sm">资产归属规则</h2>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() =>
                        setLine({
                          ...line,
                          rules: [...line.rules, { kind: "domain", value: "", key: crypto.randomUUID() }],
                        })
                      }
                    >
                      <Plus className="mr-1 size-3.5" />
                      添加规则
                    </Button>
                  </div>
                  {line.rules.map((rule, index) => (
                    <div key={rule.key} className="flex gap-2">
                      <Select
                        value={rule.kind}
                        onValueChange={(kind: "domain" | "cidr") =>
                          setLine({ ...line, rules: line.rules.map((r, i) => (i === index ? { ...r, kind } : r)) })
                        }
                      >
                        <SelectTrigger aria-label={`规则 ${index + 1} 类型`} className="w-28 shrink-0">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="domain">域名</SelectItem>
                          <SelectItem value="cidr">IP 网段</SelectItem>
                        </SelectContent>
                      </Select>
                      <Input
                        aria-label={`规则 ${index + 1} 内容`}
                        value={rule.value}
                        placeholder={rule.kind === "domain" ? "*.example.com" : "10.0.0.0/24"}
                        onChange={(e) =>
                          setLine({
                            ...line,
                            rules: line.rules.map((r, i) => (i === index ? { ...r, value: e.target.value } : r)),
                          })
                        }
                      />
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={`删除规则 ${index + 1}`}
                        onClick={() => setLine({ ...line, rules: line.rules.filter((_, i) => i !== index) })}
                      >
                        <X className="size-4" />
                      </Button>
                    </div>
                  ))}
                  {!line.rules.length && (
                    <div className="rounded-lg border border-dashed py-5 text-center text-muted-foreground text-xs">
                      尚未设置资产规则
                    </div>
                  )}
                  <p className="text-muted-foreground text-xs">
                    新漏洞按关联资产匹配。域名支持 *.example.com（子域名）；多业务线命中时保留待人工归属。
                  </p>
                </section>
                {line.id > 0 && (
                  <Link
                    href={responsibilityLink("remediation", line.name)}
                    className="inline-flex items-center gap-2 text-primary text-sm"
                  >
                    查看业务线治理统计
                    <ArrowRight className="size-4" />
                  </Link>
                )}
              </div>
              <div className="flex justify-end gap-2 border-t px-6 py-4">
                <Button variant="outline" disabled={saving} onClick={() => setLine(null)}>
                  取消
                </Button>
                <Button disabled={saving || !line.name.trim() || !line.code.trim()} onClick={() => void saveLine()}>
                  {saving ? "保存中…" : "保存业务线"}
                </Button>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>

      <Dialog
        open={party !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setParty(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              {party?.id ? "编辑" : "添加"}
              {party?.kind === "team" ? "团队" : "人员"}
            </DialogTitle>
            <DialogDescription>
              {party?.kind === "team"
                ? "维护团队与团队负责人"
                : "停用后，待处置漏洞转交给可用的业务线负责人，再由兜底负责人承接。"}
            </DialogDescription>
          </DialogHeader>
          {party && (
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="party-name">{party.kind === "team" ? "团队名称" : "姓名"}</Label>
                <Input
                  id="party-name"
                  value={party.name}
                  maxLength={120}
                  onChange={(e) => setParty({ ...party, name: e.target.value })}
                />
              </div>
              {party.kind === "person" ? (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="party-email">邮箱</Label>
                    <Input
                      id="party-email"
                      type="email"
                      value={party.email}
                      onChange={(e) => setParty({ ...party, email: e.target.value })}
                      placeholder="name@example.com"
                    />
                  </div>
                  <PersonChoice
                    label="所属团队"
                    value={party.team_id}
                    people={teams}
                    onChange={(team_id) => setParty({ ...party, team_id })}
                  />
                </>
              ) : (
                <PersonChoice
                  label="团队负责人"
                  value={party.lead_id}
                  people={people}
                  onChange={(lead_id) => setParty({ ...party, lead_id })}
                />
              )}
              <div className="flex items-center justify-between py-2">
                <Label htmlFor="party-active">启用{party.kind === "team" ? "团队" : "人员"}</Label>
                <Switch
                  id="party-active"
                  checked={party.active}
                  onCheckedChange={(active) => setParty({ ...party, active })}
                />
              </div>
              {party.id === directory?.fallback_id && (
                <p className="text-muted-foreground text-xs">此人员是兜底负责人，停用前请先指定新的兜底人员。</p>
              )}
              <div className="flex justify-end gap-2 border-t pt-4">
                <Button variant="outline" disabled={saving} onClick={() => setParty(null)}>
                  取消
                </Button>
                <Button disabled={saving || !party.name.trim()} onClick={() => void saveParty()}>
                  {saving ? "保存中…" : "保存"}
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog
        open={csvRows !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setCSVRows(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>导入业务线</DialogTitle>
            <DialogDescription>
              将创建 {csvRows?.length ?? 0} 条业务线，负责人和资产规则可在导入后配置。
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-72 divide-y overflow-auto rounded-lg border">
            {csvRows?.map((b) => (
              <div key={b.code} className="flex justify-between px-4 py-2 text-sm">
                <span>{b.name}</span>
                <span className="font-mono text-muted-foreground">{b.code}</span>
              </div>
            ))}
          </div>
          <Button disabled={saving} onClick={() => void importCSV()}>
            {saving ? "导入中…" : "确认导入"}
          </Button>
        </DialogContent>
      </Dialog>
      {tab === "lines" && (
        <a
          href="data:text/csv;charset=utf-8,%EF%BB%BFname%2Ccode%0A%E6%94%AF%E4%BB%98%E4%B8%9A%E5%8A%A1%2Cpayment%0A"
          download="business-lines-template.csv"
          className="inline-flex items-center gap-1.5 text-muted-foreground text-xs hover:underline"
        >
          <Download className="size-3" />
          下载导入模板
        </a>
      )}
    </div>
  );
}

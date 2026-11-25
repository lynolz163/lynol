"use client";

import { useEffect, useState } from "react";

import Link from "next/link";

import { Download, Plus, Settings2 } from "lucide-react";
import { toast } from "sonner";

import { type FindingGuideTopic, FindingMetricInfo } from "@/components/finding-metrics-guide";
import { FindingMetricsScope } from "@/components/finding-metrics-scope";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import {
  emptyFindingFilter,
  type FindingMetricsFilter,
  type FindingReportSchedule,
  type FindingResponsibilityOptions,
  findingDeliveryLabels,
  GOVERNANCE_MOCK,
  type SavedFindingReport,
} from "@/lib/finding-metrics";
import { downloadGovernanceWorkbook, findingFilterLabel } from "@/lib/governance-export";
import type { NotificationChannel } from "@/lib/types";

export function FindingReportsPanel({
  options,
  onExplain,
}: {
  options: FindingResponsibilityOptions;
  onExplain: (topic: FindingGuideTopic) => void;
}) {
  const [filter, setFilter] = useState<FindingMetricsFilter>({ ...emptyFindingFilter });
  const [generateOpen, setGenerateOpen] = useState(false);
  const [period, setPeriod] = useState<"weekly" | "monthly">("weekly");
  const [historyKind, setHistoryKind] = useState("all");
  const [schedules, setSchedules] = useState<FindingReportSchedule[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [reports, setReports] = useState<SavedFindingReport[]>([]);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [departmentQuery, setDepartmentQuery] = useState("");
  useEffect(() => {
    let active = true;
    Promise.all([api.findingReportSchedules(), api.notifyChannels(), api.findingReports()]).then(
      ([s, c, r]) => {
        if (active) {
          setSchedules(
            s.items.map((schedule) =>
              GOVERNANCE_MOCK
                ? schedule
                : { ...schedule, scope_mode: "departments", department_ids: schedule.department_ids || [] },
            ),
          );
          setChannels(c.filter((ch) => ["email", "wecom"].includes(ch.kind)));
          setReports(r.items);
        }
      },
      (reason) => {
        if (active) setError((reason as Error).message);
      },
    );
    return () => {
      active = false;
    };
  }, []);
  function change(kind: string, patch: Partial<FindingReportSchedule>) {
    setSchedules((items) => items.map((item) => (item.kind === kind ? { ...item, ...patch } : item)));
  }
  async function save(schedule: FindingReportSchedule) {
    if (!GOVERNANCE_MOCK && !schedule.department_ids?.length) {
      toast.error("请至少选择一个部门，再保存计划");
      return;
    }
    setBusy(schedule.kind);
    try {
      await api.saveFindingReportSchedule(
        GOVERNANCE_MOCK ? schedule : { ...schedule, scope_mode: "departments", channel_id: null },
      );
      toast.success(schedule.enabled ? "报告计划已启用" : "报告计划已保存，当前停用");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy("");
    }
  }
  async function generate(kind: "weekly" | "monthly") {
    setBusy(`generate-${kind}`);
    try {
      await api.generateFindingReport(kind, filter);
      setReports((await api.findingReports()).items);
      setGenerateOpen(false);
      toast.success("报告已保存至历史");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy("");
    }
  }
  async function download(id: number) {
    setBusy(`download-${id}`);
    try {
      const report = await api.findingReport(id);
      if (!report.current) throw new Error("报告内容不存在");
      downloadGovernanceWorkbook(
        report.current,
        report.from,
        report.to,
        report.filter,
        report.previous,
        options.directory,
      );
    } catch (reason) {
      toast.error((reason as Error).message);
    } finally {
      setBusy("");
    }
  }
  return (
    <div className="space-y-5">
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      <Card>
        <CardHeader className="flex flex-wrap items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-1">
            报告历史
            <FindingMetricInfo topic="reports" onOpen={onExplain} />
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <Dialog open={generateOpen} onOpenChange={setGenerateOpen}>
              <DialogTrigger asChild>
                <Button>
                  <Plus />
                  生成报告
                </Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>生成报告</DialogTitle>
                  <DialogDescription>
                    {GOVERNANCE_MOCK
                      ? "保存所选范围的完整周期报告。"
                      : "选择一个部门，保存完整周期报告。多部门可通过定时计划分别生成。"}
                  </DialogDescription>
                </DialogHeader>
                <div className="space-y-4">
                  <Select value={period} onValueChange={(value) => setPeriod(value as "weekly" | "monthly")}>
                    <SelectTrigger aria-label="生成报告周期" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="weekly">上一个完整自然周</SelectItem>
                      <SelectItem value="monthly">上一个完整自然月</SelectItem>
                    </SelectContent>
                  </Select>
                  <div className="flex flex-wrap gap-2">
                    <FindingMetricsScope
                      filter={filter}
                      options={options}
                      onChange={setFilter}
                      labelPrefix="生成报告"
                    />
                  </div>
                  <Button
                    disabled={!!busy || (!GOVERNANCE_MOCK && !filter.department_id)}
                    onClick={() => void generate(period)}
                  >
                    {busy.startsWith("generate-") ? "生成中…" : "确认生成"}
                  </Button>
                </div>
              </DialogContent>
            </Dialog>
            <Sheet>
              <SheetTrigger asChild>
                <Button variant="outline">
                  <Settings2 />
                  定时设置
                </Button>
              </SheetTrigger>
              <SheetContent className="overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
                <SheetHeader>
                  <SheetTitle>定时周报 / 月报</SheetTitle>
                  <SheetDescription>
                    {GOVERNANCE_MOCK ? "按计划生成，并发送到所选通知渠道。" : "按部门分别生成，推送给各部门负责人。"}
                  </SheetDescription>
                </SheetHeader>
                <div className="space-y-4 px-4 pb-6">
                  {schedules.map((schedule) => (
                    <Card key={schedule.kind}>
                      <CardHeader>
                        <CardTitle className="flex items-center justify-between">
                          {schedule.kind === "weekly" ? "定时周报" : "定时月报"}
                          <Switch
                            aria-label={schedule.kind === "weekly" ? "启用周报" : "启用月报"}
                            checked={schedule.enabled}
                            onCheckedChange={(enabled) => change(schedule.kind, { enabled })}
                          />
                        </CardTitle>
                        <CardDescription>
                          汇总上一个完整{schedule.kind === "weekly" ? "自然周" : "自然月"}。
                        </CardDescription>
                      </CardHeader>
                      <CardContent className="space-y-4">
                        <div className="flex flex-wrap items-center gap-2">
                          {schedule.kind === "weekly" ? (
                            <Select
                              value={String(schedule.weekday)}
                              onValueChange={(value) => change(schedule.kind, { weekday: Number(value) })}
                            >
                              <SelectTrigger aria-label="周报发送日" className="w-32">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {["一", "二", "三", "四", "五", "六", "日"].map((day, index) => (
                                  <SelectItem key={day} value={String(index + 1)}>
                                    每周{day}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          ) : (
                            <label htmlFor="monthly-report-day" className="flex items-center gap-2 text-sm">
                              每月
                              <Input
                                id="monthly-report-day"
                                aria-label="月报发送日"
                                type="number"
                                min={1}
                                max={28}
                                className="w-20"
                                value={schedule.monthday}
                                onChange={(event) => change(schedule.kind, { monthday: Number(event.target.value) })}
                              />
                              日
                            </label>
                          )}
                          <Input
                            aria-label={schedule.kind === "weekly" ? "周报发送时间" : "月报发送时间"}
                            type="time"
                            className="w-32"
                            value={`${String(schedule.hour).padStart(2, "0")}:${String(schedule.minute).padStart(2, "0")}`}
                            onChange={(event) => {
                              const [hour, minute] = event.target.value.split(":").map(Number);
                              change(schedule.kind, { hour, minute });
                            }}
                          />
                          <span className="text-muted-foreground text-xs">北京时间</span>
                        </div>
                        {GOVERNANCE_MOCK && (
                          <Select
                            value={schedule.channel_id ? String(schedule.channel_id) : "none"}
                            onValueChange={(value) =>
                              change(schedule.kind, { channel_id: value === "none" ? null : Number(value) })
                            }
                          >
                            <SelectTrigger
                              aria-label={schedule.kind === "weekly" ? "周报接收渠道" : "月报接收渠道"}
                              className="w-full"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="none">选择邮件或企业微信渠道</SelectItem>
                              {channels.map((channel) => (
                                <SelectItem key={channel.id} value={String(channel.id)} disabled={!channel.enabled}>
                                  {channel.name}
                                  {channel.enabled ? "" : "（已停用）"}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                        {GOVERNANCE_MOCK && !channels.length && (
                          <p className="text-muted-foreground text-xs">
                            尚无可用渠道。先在{" "}
                            <Link href="/system/notify" className="text-primary underline">
                              通知推送
                            </Link>{" "}
                            配置邮件或企业微信。
                          </p>
                        )}
                        <div className="space-y-2">
                          <p className="font-medium text-xs">报告范围</p>
                          {!GOVERNANCE_MOCK && (
                            <div className="space-y-3 rounded-lg border p-3">
                              <div className="flex items-center justify-between gap-2">
                                <span className="font-medium text-sm">部门（可多选）</span>
                                <Button
                                  variant="link"
                                  size="sm"
                                  className="h-auto p-0"
                                  onClick={() =>
                                    change(schedule.kind, {
                                      department_ids:
                                        options.directory?.departments
                                          .filter((department) => department.active)
                                          .map((department) => department.id) || [],
                                      filter: {
                                        ...schedule.filter,
                                        department_id: undefined,
                                        business_line_id: undefined,
                                        owner_id: undefined,
                                      },
                                    })
                                  }
                                >
                                  选择全部启用部门
                                </Button>
                              </div>
                              <Input
                                aria-label={`${schedule.kind === "weekly" ? "周报" : "月报"}部门搜索`}
                                placeholder="搜索部门"
                                value={departmentQuery}
                                onChange={(event) => setDepartmentQuery(event.target.value)}
                              />
                              <div className="max-h-40 space-y-2 overflow-y-auto">
                                {options.directory?.departments
                                  .filter(
                                    (department) =>
                                      (department.active || schedule.department_ids?.includes(department.id)) &&
                                      department.name.toLowerCase().includes(departmentQuery.toLowerCase()),
                                  )
                                  .map((department) => (
                                    <label key={department.id} className="flex items-center gap-2 text-sm">
                                      <input
                                        type="checkbox"
                                        checked={schedule.department_ids?.includes(department.id) || false}
                                        onChange={(event) => {
                                          const ids = event.target.checked
                                            ? [...(schedule.department_ids || []), department.id]
                                            : (schedule.department_ids || []).filter((id) => id !== department.id);
                                          change(schedule.kind, {
                                            department_ids: ids,
                                            filter: {
                                              ...schedule.filter,
                                              department_id: ids.length === 1 ? ids[0] : undefined,
                                              business_line_id: undefined,
                                              owner_id: undefined,
                                            },
                                          });
                                        }}
                                      />
                                      {department.name}
                                      {!department.active && <span className="text-muted-foreground">（已停用）</span>}
                                    </label>
                                  ))}
                              </div>
                              <p className="text-muted-foreground text-xs">
                                已选 {schedule.department_ids?.length || 0}{" "}
                                个部门，将分别生成报告。缺少负责人或可用渠道时会保留报告并标记无法投递。
                              </p>
                            </div>
                          )}
                          <div className="flex flex-wrap gap-2">
                            <FindingMetricsScope
                              filter={
                                GOVERNANCE_MOCK
                                  ? schedule.filter
                                  : {
                                      ...schedule.filter,
                                      department_id:
                                        schedule.department_ids?.length === 1 ? schedule.department_ids[0] : undefined,
                                    }
                              }
                              options={options}
                              hideDepartment={!GOVERNANCE_MOCK}
                              hideResponsibility={!GOVERNANCE_MOCK && schedule.department_ids?.length !== 1}
                              onChange={(value) => change(schedule.kind, { filter: value })}
                              labelPrefix={schedule.kind === "weekly" ? "周报计划" : "月报计划"}
                            />
                          </div>
                        </div>
                        <Button
                          disabled={!!busy || (!GOVERNANCE_MOCK && !schedule.department_ids?.length)}
                          onClick={() => void save(schedule)}
                        >
                          {busy === schedule.kind ? "保存中…" : "保存计划"}
                        </Button>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              </SheetContent>
            </Sheet>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-muted-foreground text-xs">按生成时的归属统计，已保存报告不随组织调整重算。</p>
          <div className="flex items-center justify-between gap-2">
            <Select value={historyKind} onValueChange={setHistoryKind}>
              <SelectTrigger aria-label="历史报告类型" className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部报告</SelectItem>
                <SelectItem value="weekly">周报</SelectItem>
                <SelectItem value="monthly">月报</SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="ghost"
              disabled={!!busy}
              onClick={() =>
                api.findingReports().then(
                  (result) => setReports(result.items),
                  (reason) => toast.error((reason as Error).message),
                )
              }
            >
              刷新历史
            </Button>
          </div>
          <Table className="min-w-[760px]">
            <TableHeader>
              <TableRow>
                <TableHead>报告周期</TableHead>
                <TableHead>筛选范围</TableHead>
                <TableHead>生成时间</TableHead>
                <TableHead>推送状态</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {reports
                .filter((report) => historyKind === "all" || report.kind === historyKind)
                .map((report) => (
                  <TableRow key={report.id}>
                    <TableCell>
                      <Link
                        href={`/remediation-report?id=${report.id}`}
                        target="_blank"
                        rel="noreferrer"
                        className="font-medium text-primary hover:underline"
                      >
                        {report.kind === "weekly" ? "周报" : "月报"} · {report.from} — {report.to}
                      </Link>
                      {(report.attribution_version ?? 0) === 0 && (
                        <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-muted-foreground text-xs">
                          旧口径
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="max-w-64 whitespace-normal text-xs">
                      {findingFilterLabel(report.filter, options.directory)}
                    </TableCell>
                    <TableCell className="text-xs">{new Date(report.generated_at).toLocaleString("zh-CN")}</TableCell>
                    <TableCell>
                      <span>{findingDeliveryLabels[report.delivery_state] ?? report.delivery_state}</span>
                      {(report.delivery_error || report.delivery_context?.message) && (
                        <p
                          className={`max-w-56 whitespace-normal text-xs ${report.delivery_state === "failed" ? "text-destructive" : "text-muted-foreground"}`}
                        >
                          {report.delivery_error || report.delivery_context?.message}
                        </p>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button asChild size="sm" variant="ghost">
                          <Link href={`/remediation-report?id=${report.id}`} target="_blank" rel="noreferrer">
                            查看
                          </Link>
                        </Button>
                        <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => void download(report.id)}>
                          <Download />
                          {busy === `download-${report.id}` ? "下载中…" : "Excel"}
                        </Button>
                        {["failed", "skipped"].includes(report.delivery_state) && report.delivery_id && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={process.env.NEXT_PUBLIC_MOCK === "1"}
                            title={process.env.NEXT_PUBLIC_MOCK === "1" ? "本地预览不发送真实通知" : undefined}
                            onClick={() => {
                              const deliveryID = report.delivery_id;
                              if (deliveryID === null) return;
                              void api
                                .notifyRetryDelivery(deliveryID)
                                .then(() => api.findingReports())
                                .then(
                                  (result) => setReports(result.items),
                                  (reason) => toast.error((reason as Error).message),
                                );
                            }}
                          >
                            重试推送
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              {!reports.some((report) => historyKind === "all" || report.kind === historyKind) && (
                <TableRow>
                  <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                    暂无报告，点击“生成报告”创建。
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

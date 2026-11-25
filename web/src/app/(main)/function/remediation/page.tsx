"use client";

import { useEffect, useState } from "react";

import Link from "next/link";

import { ArrowDown, ArrowUp, ChevronDown, Download, RefreshCw, Settings2 } from "lucide-react";
import { Bar, CartesianGrid, Cell, ComposedChart, Line, XAxis, YAxis } from "recharts";
import { toast } from "sonner";

import { FindingGroupMetrics } from "@/components/finding-group-metrics";
import {
  type FindingGuideTopic,
  FindingMetricInfo,
  FindingMetricsGuide,
  type FindingMetricTopic,
} from "@/components/finding-metrics-guide";
import { FindingMetricsScope } from "@/components/finding-metrics-scope";
import { FindingOverduePanel } from "@/components/finding-overdue-panel";
import { FindingReportsPanel } from "@/components/finding-reports-panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ChartContainer, ChartTooltip } from "@/components/ui/chart";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import {
  emptyFindingFilter,
  type FindingMetricsFilter,
  type FindingMetricsReport,
  type FindingMetricTrend,
  type FindingResponsibilityOptions,
  findingMetricDate,
  findingMetricDuration,
  findingSeverityLabels,
  findingSLATotal,
  findingStageLabels,
  GOVERNANCE_MOCK,
} from "@/lib/finding-metrics";
import { downloadGovernanceWorkbook } from "@/lib/governance-export";
import { type OrgDirectory, orgAPI } from "@/lib/org";

const grades = ["critical", "high", "medium", "low"] as const;
const severityColors = { critical: "#c63c4e", high: "#e58242", medium: "#d9ac3e", low: "#4f81ca" };
const chartConfig = {
  new: { label: "新增", color: "#5684d1" },
  closed: { label: "关闭", color: "#36a28a" },
  open: { label: "期末存量", color: "#c48a35" },
};
const percent = (numerator: number, denominator: number) =>
  denominator ? `${((100 * numerator) / denominator).toFixed(1)}%` : "—";

function groupTrend(rows: FindingMetricTrend[], granularity: string) {
  const groups = new Map<
    string,
    FindingMetricTrend & { from: string; to: string; end: string; partial: boolean; ongoing: boolean }
  >();
  const today = findingMetricDate(new Date());
  for (const row of rows) {
    let key = row.day;
    let end = row.day;
    if (granularity === "month") {
      key = `${row.day.slice(0, 7)}-01`;
      const date = new Date(`${key}T00:00:00Z`);
      date.setUTCMonth(date.getUTCMonth() + 1);
      date.setUTCDate(0);
      end = date.toISOString().slice(0, 10);
    }
    if (granularity === "week") {
      const date = new Date(`${row.day}T00:00:00Z`);
      date.setUTCDate(date.getUTCDate() - ((date.getUTCDay() + 6) % 7));
      key = date.toISOString().slice(0, 10);
      date.setUTCDate(date.getUTCDate() + 6);
      end = date.toISOString().slice(0, 10);
    }
    const previous = groups.get(key);
    groups.set(key, {
      day: key,
      new: (previous?.new ?? 0) + row.new,
      closed: (previous?.closed ?? 0) + row.closed,
      reopened: (previous?.reopened ?? 0) + row.reopened,
      open: row.open,
      overdue: row.overdue,
      from: previous?.from ?? row.day,
      to: row.day,
      end,
      partial: false,
      ongoing: false,
    });
  }
  return [...groups.values()].map((group) => ({
    ...group,
    partial: group.from !== group.day || group.to !== group.end || group.to === today,
    ongoing: group.to === today,
  }));
}

function MetricsDashboard({
  report,
  previous,
  granularity,
  onGranularityChange,
  onExplain,
  directory,
}: {
  report: FindingMetricsReport;
  directory?: OrgDirectory;
  previous?: FindingMetricsReport;
  granularity: string;
  onGranularityChange: (value: string) => void;
  onExplain: (topic: FindingGuideTopic) => void;
}) {
  const { summary } = report;
  const closure = report.durations.find((item) => item.key === "closure");
  const trend = groupTrend(report.trend, granularity);
  const conclusive = summary.retest_failed + summary.retest_passed;
  const prior = previous?.generated_at ? previous.summary : undefined;
  const countChange = (value: number, old: number | undefined) => (old === undefined ? null : value - old);
  const rateChange = (value: number, total: number, old: number | undefined, oldTotal: number | undefined) => {
    if (!total || !oldTotal || old === undefined) return null;
    return Number(((100 * value) / total - (100 * old) / oldTotal).toFixed(1));
  };
  const cards: {
    label: string;
    value: string | number;
    change: number | null;
    improvement?: "up" | "down";
    rate?: boolean;
    topic: FindingMetricTopic;
    detail?: string;
  }[] = [
    { label: "新增漏洞", value: summary.new, change: countChange(summary.new, prior?.new), topic: "new" },
    {
      label: "完成关闭",
      value: summary.closed,
      change: countChange(summary.closed, prior?.closed),
      topic: "closed",
      improvement: "up",
      detail: summary.reopened ? `重开 ${summary.reopened}` : undefined,
    },
    {
      label: "待处置存量",
      value: report.stock_known ? summary.open : "—",
      change: report.stock_known ? countChange(summary.open, previous?.stock_known ? prior?.open : undefined) : null,
      topic: "open",
      improvement: "down",
    },
    {
      label: "已逾期",
      value: report.stock_known ? summary.overdue : "—",
      change: report.stock_known
        ? countChange(summary.overdue, previous?.stock_known ? prior?.overdue : undefined)
        : null,
      topic: "overdue",
      improvement: "down",
    },
    {
      label: "SLA 达标率",
      value: report.stock_known ? percent(summary.sla_met, findingSLATotal(summary)) : "—",
      change:
        report.stock_known && previous?.stock_known
          ? rateChange(
              summary.sla_met,
              findingSLATotal(summary),
              prior?.sla_met,
              prior ? findingSLATotal(prior) : undefined,
            )
          : null,
      improvement: "up",
      rate: true,
      topic: "sla",
      detail: `${report.stock_known ? `${summary.sla_met} / ${findingSLATotal(summary)} · ` : ""}已关闭口径 ${percent(summary.sla_met, summary.sla_closed)}`,
    },
    {
      label: "复测不通过率",
      value: percent(summary.retest_failed, conclusive),
      change: rateChange(
        summary.retest_failed,
        conclusive,
        prior?.retest_failed,
        prior ? prior.retest_failed + prior.retest_passed : undefined,
      ),
      topic: "retest",
      improvement: "down",
      rate: true,
      detail: `${summary.retest_failed} / ${conclusive}${summary.retest_other ? ` · 无明确结论 ${summary.retest_other}` : ""}`,
    },
  ];
  const gradeData = grades.map((grade) => {
    const items = report.distribution.filter((item) => item.severity === grade);
    const met = items.reduce((sum, item) => sum + item.sla_met, 0);
    const total = items.reduce((sum, item) => sum + findingSLATotal(item), 0);
    let progressColor = "bg-muted";
    if (total > 0) {
      progressColor = "bg-red-500";
      if (met / total >= 0.8) progressColor = "bg-amber-500";
      if (met === total) progressColor = "bg-emerald-500";
    }
    return {
      grade,
      open: items.reduce((sum, item) => sum + item.open, 0),
      overdue: items.reduce((sum, item) => sum + item.overdue, 0),
      met,
      total,
      progressColor,
      ages: [0, 1, 2].map((index) => items.reduce((sum, item) => sum + item.ages[index], 0)),
    };
  });
  const classData = [
    ...report.distribution.reduce((groups, item) => {
      const key = item.vulnclass || "未分类";
      groups.set(key, (groups.get(key) ?? 0) + item.open);
      return groups;
    }, new Map<string, number>()),
  ]
    .filter(([, count]) => count > 0)
    .sort((a, b) => b[1] - a[1]);
  const stages = report.durations.filter((item) =>
    ["dispatch", "development", "waiting", "verification"].includes(item.key),
  );
  const maxDuration = Math.max(1, ...stages.map((item) => item.p90_hours ?? 0));
  const stageTotal = stages.reduce((sum, item) => sum + (item.average_hours ?? 0), 0);
  const bottleneck = [...stages]
    .filter((item) => item.average_hours !== null)
    .sort((a, b) => (b.average_hours ?? 0) - (a.average_hours ?? 0))[0];
  const rework = report.durations.find((item) => item.key === "rework");
  const missingDepartment = report.attribution_version
    ? report.distribution.filter((item) => !item.department_id).reduce((sum, item) => sum + item.open, 0)
    : 0;
  const missingBusiness = report.distribution
    .filter((item) => (report.attribution_version ? !item.business_line_id : !item.business_line))
    .reduce((sum, item) => sum + item.open, 0);
  const missingAssignee = report.distribution
    .filter((item) => (report.attribution_version ? !item.owner_id : !item.assignee))
    .reduce((sum, item) => sum + item.open, 0);
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {cards.map(({ label, value, change, improvement, rate, topic, detail }) => {
          let changeColor = "text-muted-foreground";
          if (change && improvement) {
            changeColor =
              change > 0 === (improvement === "up")
                ? "text-emerald-700 dark:text-emerald-400"
                : "text-red-600 dark:text-red-400";
          }
          return (
            <Card key={label} className="min-w-0 gap-2 py-4">
              <CardHeader className="flex items-center justify-between gap-1 px-4">
                <CardDescription className="whitespace-nowrap text-xs">{label}</CardDescription>
                <FindingMetricInfo topic={topic} onOpen={onExplain} />
              </CardHeader>
              <CardContent className="space-y-1 px-4">
                <p className="font-semibold text-2xl tabular-nums tracking-tight xl:text-3xl">{value}</p>
                <p className={`flex items-center gap-1 text-xs tabular-nums ${changeColor}`}>
                  {change === null ? (
                    "暂无可比数据"
                  ) : (
                    <>
                      {change > 0 && <ArrowUp aria-hidden className="size-3" />}
                      {change < 0 && <ArrowDown aria-hidden className="size-3" />}
                      较上期 {change > 0 ? "+" : ""}
                      {change}
                      {rate ? " 个百分点" : ""}
                      {change !== 0 && improvement && (
                        <span className="sr-only">{change > 0 === (improvement === "up") ? "改善" : "恶化"}</span>
                      )}
                    </>
                  )}
                </p>
                {detail && <p className="text-muted-foreground text-xs tabular-nums">{detail}</p>}
              </CardContent>
            </Card>
          );
        })}
      </div>
      {((report.stock_known && (missingDepartment > 0 || missingBusiness > 0 || missingAssignee > 0)) ||
        (report.data_quality && Object.values(report.data_quality).some((value) => value > 0))) && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border border-amber-500/25 bg-amber-500/5 px-4 py-2 text-xs">
          <span className="flex items-center gap-1 font-medium">
            数据质量 <FindingMetricInfo topic="dataQuality" onOpen={onExplain} />
          </span>
          {report.stock_known && missingDepartment > 0 && (
            <Link href="/settings/org?tab=assets&asset_status=unassigned" className="hover:underline">
              部门未归属 <strong>{missingDepartment}</strong>
            </Link>
          )}
          {report.stock_known && missingBusiness > 0 && (
            <Link href="/settings/org?tab=assets&asset_status=unassigned" className="hover:underline">
              业务线未归属 <strong className="tabular-nums">{missingBusiness}</strong>
            </Link>
          )}
          {report.stock_known && missingAssignee > 0 && (
            <Link href="/settings/org?tab=people" className="hover:underline">
              负责人未指派 <strong className="tabular-nums">{missingAssignee}</strong>
            </Link>
          )}
          {report.stock_known && (missingDepartment > 0 || missingBusiness > 0 || missingAssignee > 0) && (
            <span className="text-muted-foreground">未归属不豁免 SLA</span>
          )}
          {!!report.data_quality?.unknown_attribution && (
            <span>归属未知 {report.data_quality.unknown_attribution} 条记录</span>
          )}
          {!!report.data_quality?.missing_sla && <span>缺少 SLA {report.data_quality.missing_sla} 条记录</span>}
          {!!report.data_quality?.missing_discovery && (
            <span>缺少发现时间 {report.data_quality.missing_discovery} 条记录</span>
          )}
          {!!report.data_quality?.missing_snapshot_days && (
            <span>缺少日快照 {report.data_quality.missing_snapshot_days} 天</span>
          )}
        </div>
      )}
      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1.7fr)_minmax(300px,1fr)]">
        <Card className="min-w-0">
          <CardHeader className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-1">
              新增 / 关闭 / 存量趋势
              <FindingMetricInfo topic="trend" onOpen={onExplain} />
            </CardTitle>
            <Select value={granularity} onValueChange={onGranularityChange}>
              <SelectTrigger aria-label="趋势周期" className="w-24">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="day">按日</SelectItem>
                <SelectItem value="week">按周</SelectItem>
                <SelectItem value="month">按月</SelectItem>
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex gap-5 text-xs">
              {Object.entries(chartConfig).map(([key, item]) => (
                <span key={key} className="inline-flex items-center gap-1.5">
                  <span aria-hidden className="size-2 rounded-sm" style={{ background: item.color }} />
                  {item.label}
                </span>
              ))}
            </div>
            <ChartContainer config={chartConfig} className="h-64 w-full">
              <ComposedChart accessibilityLayer data={trend} margin={{ left: 0, right: 12, top: 10 }} maxBarSize={28}>
                <defs>
                  {(["new", "closed"] as const).map((key) => (
                    <pattern
                      key={key}
                      id={`partial-${key}`}
                      width="6"
                      height="6"
                      patternUnits="userSpaceOnUse"
                      patternTransform="rotate(45)"
                    >
                      <rect width="6" height="6" fill={chartConfig[key].color} fillOpacity={0.22} />
                      <line
                        x1="0"
                        x2="0"
                        y1="0"
                        y2="6"
                        stroke={chartConfig[key].color}
                        strokeWidth="2"
                        strokeOpacity={0.6}
                      />
                    </pattern>
                  ))}
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="day"
                  axisLine={false}
                  tickLine={false}
                  minTickGap={30}
                  tickFormatter={(day: string) =>
                    `${day.slice(5)}${trend.find((item) => item.day === day)?.partial ? "*" : ""}`
                  }
                />
                <YAxis allowDecimals={false} axisLine={false} tickLine={false} width={34} />
                <ChartTooltip
                  content={({ active, payload }) => {
                    const point = payload?.[0]?.payload as (typeof trend)[number] | undefined;
                    if (!active || !point) return null;
                    return (
                      <div className="space-y-2 rounded-lg border bg-background px-3 py-2 text-xs shadow-xl">
                        <p className="font-medium">
                          {point.from} 至 {point.to}
                        </p>
                        {point.partial && (
                          <p className="text-muted-foreground">
                            {point.ongoing ? "进行中" : "非完整周期"} · 实际覆盖{" "}
                            {Math.round((Date.parse(point.to) - Date.parse(point.from)) / 86400000) + 1} 天
                            {point.ongoing ? "（当日未结束）" : ""}
                          </p>
                        )}
                        {Object.entries(chartConfig).map(([key, item]) => (
                          <p key={key} className="flex min-w-44 items-center gap-2">
                            <span className="size-2 rounded-sm" style={{ background: item.color }} />
                            {item.label}
                            <span className="ml-auto font-medium tabular-nums">
                              {point[key as "new" | "closed" | "open"] ?? "—"}
                            </span>
                          </p>
                        ))}
                      </div>
                    );
                  }}
                />
                <Bar dataKey="new" fill="var(--color-new)" radius={[3, 3, 0, 0]} isAnimationActive={false}>
                  {trend.map((point) => (
                    <Cell key={point.day} fill={point.partial ? "url(#partial-new)" : chartConfig.new.color} />
                  ))}
                </Bar>
                <Bar dataKey="closed" fill="var(--color-closed)" radius={[3, 3, 0, 0]} isAnimationActive={false}>
                  {trend.map((point) => (
                    <Cell key={point.day} fill={point.partial ? "url(#partial-closed)" : chartConfig.closed.color} />
                  ))}
                </Bar>
                <Line
                  dataKey="open"
                  type="linear"
                  stroke="var(--color-open)"
                  strokeWidth={2.5}
                  dot={false}
                  connectNulls={false}
                  isAnimationActive={false}
                />
              </ComposedChart>
            </ChartContainer>
            {trend.some((point) => point.partial) && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground text-xs">
                {trend
                  .filter((point) => point.partial)
                  .map((point) => (
                    <span key={point.day}>
                      * {point.from.slice(5)}～{point.to.slice(5)} · {point.ongoing ? "进行中" : "非完整周期"}
                    </span>
                  ))}
              </div>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-1">
              各等级 SLA 达标
              <FindingMetricInfo topic="sla" onOpen={onExplain} />
            </CardTitle>
            <span className="whitespace-nowrap text-muted-foreground text-xs">目标 100%</span>
          </CardHeader>
          <CardContent className="space-y-5">
            {!gradeData.some((item) => item.total > 0 || item.open > 0) && (
              <p className="py-8 text-center text-muted-foreground text-sm">暂无数据</p>
            )}
            {gradeData
              .filter((item) => item.total > 0 || item.open > 0)
              .map(({ grade, met, total, open, overdue, progressColor }) => (
                <div
                  key={grade}
                  className={`space-y-2 rounded-lg p-3 ${grade === "critical" && open > 0 && report.stock_known ? "border border-red-500/25 bg-red-500/5" : "bg-muted/35"}`}
                >
                  <div className="flex justify-between text-xs">
                    <span>
                      {findingSeverityLabels[grade]}{" "}
                      <span className="text-muted-foreground">
                        · {report.has_custom_sla ? "含业务线自定义时限" : `${report.sla?.[grade] ?? "—"} 天`}
                      </span>
                    </span>
                    <span className="tabular-nums">
                      {report.stock_known ? percent(met, total) : "—"}{" "}
                      <span className="text-muted-foreground">
                        {report.stock_known && total > 0 ? `(${met}/${total})` : ""}
                      </span>
                    </span>
                  </div>
                  <div aria-hidden className="relative h-1.5 rounded bg-muted">
                    <div
                      className={`h-full rounded ${progressColor}`}
                      style={{ width: `${report.stock_known && total ? (100 * met) / total : 0}%` }}
                    />
                    <span className="absolute -top-1 right-0 h-3.5 border-foreground/60 border-r border-dashed" />
                  </div>
                  <div className="flex gap-4 text-xs">
                    <span
                      className={
                        grade === "critical" && open > 0 ? "font-medium text-red-600" : "text-muted-foreground"
                      }
                    >
                      待处置 {report.stock_known ? open : "—"}
                    </span>
                    <span className={overdue > 0 ? "font-medium text-red-600" : "text-muted-foreground"}>
                      逾期 {report.stock_known ? overdue : "—"}
                    </span>
                  </div>
                </div>
              ))}
          </CardContent>
        </Card>
      </div>
      <FindingOverduePanel report={report} directory={directory} onExplain={onExplain} />
      <div className="grid items-start gap-5 lg:grid-cols-2">
        <Card className="min-w-0">
          <CardHeader>
            <CardTitle className="flex items-center gap-1">
              阶段耗时
              <FindingMetricInfo topic="duration" onOpen={onExplain} />
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="mb-5 grid grid-cols-2 gap-4 rounded-lg bg-muted/40 p-4">
              <div>
                <p className="text-muted-foreground text-xs">整体闭环 · 平均</p>
                <p className="mt-1 font-semibold text-2xl tabular-nums">
                  {findingMetricDuration(closure?.average_hours ?? null)}
                </p>
              </div>
              <div>
                <p className="text-muted-foreground text-xs">整体闭环 · P90</p>
                <p className="mt-1 font-semibold text-2xl tabular-nums">
                  {findingMetricDuration(closure?.p90_hours ?? null)}
                </p>
              </div>
            </div>
            <Table className="min-w-[480px]">
              <TableHeader>
                <TableRow>
                  <TableHead>阶段</TableHead>
                  <TableHead className="hidden sm:table-cell">P90 时长对比</TableHead>
                  <TableHead className="text-right">平均</TableHead>
                  <TableHead className="text-right">P90</TableHead>
                  <TableHead className="text-right">样本</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {stages.map((item) => (
                  <TableRow
                    key={item.key}
                    className={item.key === bottleneck?.key ? "bg-amber-500/10 hover:bg-amber-500/15" : ""}
                  >
                    <TableCell className="whitespace-normal text-xs">
                      {findingStageLabels[item.key]}
                      {item.key === bottleneck?.key && stageTotal > 0 && (
                        <span className="mt-1 block font-medium text-amber-700 dark:text-amber-400">
                          瓶颈 · 占阶段均值 {((100 * (item.average_hours ?? 0)) / stageTotal).toFixed(1)}%
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="hidden w-24 sm:table-cell">
                      <div aria-hidden className="h-1.5 overflow-hidden rounded bg-muted">
                        <div
                          className={`h-full rounded ${item.key === bottleneck?.key ? "bg-amber-500" : "bg-blue-500/75"}`}
                          style={{ width: `${(100 * (item.p90_hours ?? 0)) / maxDuration}%` }}
                        />
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {findingMetricDuration(item.average_hours)}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {findingMetricDuration(item.p90_hours)}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">{item.count}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <div className="mt-5 space-y-3 border-t pt-4">
              <h3 className="flex items-center gap-1 font-medium text-sm">
                修复质量与返工 <FindingMetricInfo topic="quality" onOpen={onExplain} />
              </h3>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                <div>
                  <p className="text-muted-foreground text-xs">一次修复通过率</p>
                  <p className="mt-1 font-semibold text-xl tabular-nums">
                    {percent(summary.first_retest_passed, summary.first_retest_total)}
                  </p>
                  <p className="mt-1 text-muted-foreground text-xs">
                    {report.quality_known || summary.first_retest_total > 0
                      ? `${summary.first_retest_passed} / ${summary.first_retest_total}${report.quality_known ? "" : " · 已记录样本"}`
                      : "尚无统计数据"}
                  </p>
                </div>
                <div>
                  <p className="text-muted-foreground text-xs">平均返工轮次</p>
                  <p className="mt-1 font-semibold text-xl tabular-nums">
                    {summary.verified_fixes ? `${(summary.rework_rounds / summary.verified_fixes).toFixed(2)} 轮` : "—"}
                  </p>
                  <p className="mt-1 text-muted-foreground text-xs">
                    {report.quality_known || summary.verified_fixes > 0
                      ? `${summary.rework_rounds} 次返工 / ${summary.verified_fixes} 次修复${report.quality_known ? "" : " · 已记录样本"}`
                      : "尚无统计数据"}
                  </p>
                </div>
                <div>
                  <p className="text-muted-foreground text-xs">平均返工耗时</p>
                  <p className="mt-1 font-semibold text-xl tabular-nums">
                    {findingMetricDuration(rework?.average_hours ?? null)}
                  </p>
                  <p className="mt-1 text-muted-foreground text-xs">
                    {report.quality_known || (rework?.count ?? 0) > 0
                      ? `${rework?.count ?? 0} 个已完成返工周期${report.quality_known ? "" : " · 已记录样本"}`
                      : "尚无统计数据"}
                  </p>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1">
              待处置账龄与分布
              <FindingMetricInfo topic="age" onOpen={onExplain} />
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="space-y-2">
              <div
                aria-hidden
                className="flex h-6 gap-px overflow-hidden rounded text-center font-medium text-white text-xs"
              >
                {gradeData
                  .filter((item) => item.open > 0)
                  .map(({ grade, open }) => (
                    <div
                      key={grade}
                      className="flex min-w-0 items-center justify-center"
                      style={{ flex: open, background: severityColors[grade] }}
                    >
                      {summary.open && open / summary.open >= 0.06 ? open : ""}
                    </div>
                  ))}
              </div>
              <div className="flex flex-wrap gap-2 text-xs">
                {gradeData.map(({ grade, open }) => (
                  <span key={grade} className="inline-flex items-center gap-1.5 rounded bg-muted/50 px-2 py-1">
                    <span aria-hidden className="size-2 rounded-sm" style={{ background: severityColors[grade] }} />
                    {findingSeverityLabels[grade]}{" "}
                    <span className="font-medium tabular-nums">{report.stock_known ? open : "—"}</span>
                  </span>
                ))}
              </div>
            </div>
            <Table className="min-w-[360px]">
              <TableHeader>
                <TableRow>
                  <TableHead>等级 × 账龄</TableHead>
                  <TableHead className="text-right">&lt;7 天</TableHead>
                  <TableHead className="text-right">7–&lt;30 天</TableHead>
                  <TableHead className="text-right">≥30 天</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {gradeData.map(({ grade, ages }) => (
                  <TableRow key={grade}>
                    <TableCell className="text-xs">{findingSeverityLabels[grade]}</TableCell>
                    {ages.map((count, index) => (
                      <TableCell
                        key={["young", "middle", "old"][index]}
                        className="p-1 text-right text-xs tabular-nums"
                      >
                        <span
                          className="block rounded px-3 py-2"
                          style={{
                            background:
                              report.stock_known && count > 0
                                ? `rgba(198,60,78,${0.06 + (0.26 * count) / Math.max(1, ...gradeData.flatMap((item) => item.ages))})`
                                : undefined,
                          }}
                        >
                          {report.stock_known ? count : "—"}
                        </span>
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
                <TableRow className="font-medium">
                  <TableCell className="text-xs">合计</TableCell>
                  {summary.ages.map((count, index) => (
                    <TableCell key={["young", "middle", "old"][index]} className="text-right text-xs tabular-nums">
                      {report.stock_known ? count : "—"}
                    </TableCell>
                  ))}
                </TableRow>
              </TableBody>
            </Table>
            <div className="space-y-3 border-t pt-4">
              <p className="text-sm">漏洞类型</p>
              {classData
                .slice(0, 8)
                .concat(
                  classData.length > 8
                    ? [["其他类型", classData.slice(8).reduce((sum, [, count]) => sum + count, 0)]]
                    : [],
                )
                .map(([name, count]) => (
                  <div key={name} className="space-y-1.5 text-xs">
                    <div className="flex justify-between gap-3">
                      <span className="truncate" title={name}>
                        {name}
                      </span>
                      <span className="shrink-0 tabular-nums">
                        {count} · {percent(count, summary.open)}
                      </span>
                    </div>
                    <div aria-hidden className="h-1.5 rounded bg-muted">
                      <div
                        className="h-full rounded bg-blue-500/70"
                        style={{ width: `${summary.open ? (100 * count) / summary.open : 0}%` }}
                      />
                    </div>
                  </div>
                ))}
              {!classData.length && <p className="text-muted-foreground text-xs">暂无可展示的类型</p>}
            </div>
          </CardContent>
        </Card>
      </div>
      <FindingGroupMetrics report={report} onExplain={onExplain} />
    </div>
  );
}

export default function RemediationPage() {
  const [range, setRange] = useState(() => {
    const to = findingMetricDate(new Date());
    return { from: new Date(Date.parse(to) - 29 * 86400000).toISOString().slice(0, 10), to };
  });
  const [preset, setPreset] = useState("30");
  const [filter, setFilter] = useState<FindingMetricsFilter>({ ...emptyFindingFilter });
  const [section, setSection] = useState("metrics");
  const [scopeOptions, setScopeOptions] = useState<FindingResponsibilityOptions>({ business_lines: [], assignees: [] });
  const [guideTopic, setGuideTopic] = useState<FindingGuideTopic | null>(null);
  const [granularity, setGranularity] = useState("week");
  const [data, setData] = useState<{ current: FindingMetricsReport; previous?: FindingMetricsReport }>();
  const [error, setError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [revision, setRevision] = useState(0);
  const report = data?.current;
  const validRange = Boolean(
    range.from &&
      range.to &&
      range.from <= range.to &&
      Date.parse(range.to) - Date.parse(range.from) <= 365 * 86400000 &&
      range.to <= findingMetricDate(new Date()),
  );

  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    setFilter({
      ...emptyFindingFilter,
      department_id: q.has("department_id") ? Number(q.get("department_id")) : undefined,
      business_line_id: q.has("business_line_id") ? Number(q.get("business_line_id")) : undefined,
      owner_id: q.has("owner_id") ? Number(q.get("owner_id")) : undefined,
      business_line: q.get("business_line") ?? "",
      assignee: q.get("assignee") ?? "",
    });
    if (GOVERNANCE_MOCK)
      api.findingResponsibilityOptions().then(setScopeOptions, () => toast.error("读取归属筛选失败"));
    else
      orgAPI.directory().then(
        (directory) => setScopeOptions({ business_lines: [], assignees: [], directory }),
        () => toast.error("读取组织筛选失败"),
      );
  }, []);
  useEffect(() => {
    const update = () => {
      setRevision((value) => value + 1);
      if (!GOVERNANCE_MOCK)
        void orgAPI
          .directory()
          .then((directory) => setScopeOptions({ business_lines: [], assignees: [], directory }))
          .catch(() => toast.error("读取组织筛选失败"));
    };
    window.addEventListener("org-statistics-refreshed", update);
    window.addEventListener("focus", update);
    return () => {
      window.removeEventListener("org-statistics-refreshed", update);
      window.removeEventListener("focus", update);
    };
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: revision reloads snapshots after an explicit refresh.
  useEffect(() => {
    setData(undefined);
    setError("");
    if (!validRange) return;
    let active = true;
    const start = Date.parse(range.from);
    const days = (Date.parse(range.to) - start) / 86400000 + 1;
    const priorFrom = new Date(start - days * 86400000).toISOString().slice(0, 10);
    const priorTo = new Date(start - 86400000).toISOString().slice(0, 10);
    Promise.all([
      api.findingMetrics(range.from, range.to, filter),
      api.findingMetrics(priorFrom, priorTo, filter),
    ]).then(
      ([current, previous]) => {
        if (active)
          setData({
            current,
            previous: current.trend.length === days && previous.trend.length === days ? previous : undefined,
          });
      },
      (reason: Error) => {
        if (active) setError(reason.message);
      },
    );
    return () => {
      active = false;
    };
  }, [range.from, range.to, filter, validRange, revision]);

  async function refresh() {
    setRefreshing(true);
    setError("");
    try {
      await api.refreshFindingMetrics();
      setRevision((value) => value + 1);
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setRefreshing(false);
    }
  }
  function changePreset(value: string) {
    setPreset(value);
    if (value === "custom") return;
    const to = findingMetricDate(new Date());
    setRange({
      from:
        value === "month"
          ? `${to.slice(0, 7)}-01`
          : new Date(Date.parse(to) - (Number(value) - 1) * 86400000).toISOString().slice(0, 10),
      to,
    });
  }

  return (
    <main data-content-padding="false" className="h-[calc(100svh-3rem)] min-w-0 overflow-y-auto">
      <div className="mx-auto w-full min-w-0 max-w-[1440px] space-y-5 p-4 lg:p-6">
        <header className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="font-semibold text-2xl tracking-tight">漏洞治理</h1>
            {GOVERNANCE_MOCK && <Badge variant="secondary">示例数据</Badge>}
            {section === "metrics" && report?.generated_at && (
              <span className="text-muted-foreground text-xs">
                更新于 {new Date(report.generated_at).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            {section === "metrics" && report?.generated_at && validRange && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline">
                    <Download />
                    导出
                    <ChevronDown />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    onSelect={() =>
                      downloadGovernanceWorkbook(
                        report,
                        range.from,
                        range.to,
                        filter,
                        undefined,
                        scopeOptions.directory,
                      )
                    }
                  >
                    导出 Excel
                  </DropdownMenuItem>
                  <DropdownMenuItem asChild>
                    <Link
                      target="_blank"
                      rel="noreferrer"
                      href={`/remediation-report?${new URLSearchParams(
                        Object.entries({ ...range, ...filter })
                          .filter(([, value]) => value !== undefined && value !== null)
                          .map(([key, value]) => [key, String(value)]),
                      )}`}
                    >
                      PDF / 打印
                    </Link>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            <Button
              variant="outline"
              size="icon"
              aria-label="SLA 规则 / 指标说明"
              title="SLA 规则 / 指标说明"
              onClick={() => setGuideTopic("rules")}
            >
              <Settings2 />
            </Button>
            {section === "metrics" && (
              <Button disabled={refreshing} onClick={() => void refresh()}>
                <RefreshCw className={refreshing ? "animate-spin" : ""} />
                {refreshing ? "更新中…" : "更新统计"}
              </Button>
            )}
          </div>
        </header>
        <a href="/settings/org" className="ml-auto text-muted-foreground text-xs hover:underline">
          配置部门与负责人 ↗
        </a>
        <Tabs value={section} onValueChange={setSection}>
          <TabsList>
            <TabsTrigger value="metrics">治理仪表盘</TabsTrigger>
            <TabsTrigger value="reports">周报 / 月报</TabsTrigger>
          </TabsList>
        </Tabs>
        <FindingMetricsGuide
          topic={guideTopic}
          onTopicChange={setGuideTopic}
          onSaved={refresh}
          historySince={report?.history_since}
        />
        <div hidden={section !== "reports"}>
          <FindingReportsPanel options={scopeOptions} onExplain={setGuideTopic} />
        </div>
        {section === "metrics" && (
          <>
            <div className="sticky top-0 z-20 flex items-center gap-2 overflow-x-auto rounded-xl border bg-background/95 p-2 shadow-sm backdrop-blur-sm">
              <Select value={preset} onValueChange={changePreset}>
                <SelectTrigger aria-label="日期范围" className="w-28 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="7">近 7 天</SelectItem>
                  <SelectItem value="30">近 30 天</SelectItem>
                  <SelectItem value="month">本月</SelectItem>
                  <SelectItem value="custom">自定义</SelectItem>
                </SelectContent>
              </Select>
              {preset === "custom" ? (
                <>
                  <Input
                    aria-label="开始日期"
                    type="date"
                    value={range.from}
                    max={range.to}
                    className="w-36 shrink-0"
                    onChange={(event) => setRange({ ...range, from: event.target.value })}
                  />
                  <span className="text-muted-foreground text-xs">至</span>
                  <Input
                    aria-label="结束日期"
                    type="date"
                    value={range.to}
                    min={range.from}
                    max={findingMetricDate(new Date())}
                    className="w-36 shrink-0"
                    onChange={(event) => setRange({ ...range, to: event.target.value })}
                  />
                </>
              ) : (
                <span className="shrink-0 whitespace-nowrap px-1 text-muted-foreground text-xs tabular-nums">
                  {range.from} — {range.to}
                </span>
              )}
              <FindingMetricsScope
                filter={filter}
                options={{
                  ...scopeOptions,
                  historical_line_ids: report?.filter_options?.business_line_ids,
                  historical_owner_ids: report?.filter_options?.owner_ids,
                }}
                onChange={(value) => {
                  setFilter(value);
                  const url = new URL(window.location.href);
                  for (const key of ["department_id", "business_line_id", "owner_id", "severity"] as const) {
                    if (value[key] !== undefined && value[key] !== null && value[key] !== "")
                      url.searchParams.set(key, String(value[key]));
                    else url.searchParams.delete(key);
                  }
                  window.history.replaceState(null, "", url);
                }}
              />
            </div>
            {!validRange && (
              <p role="alert" className="text-destructive text-sm">
                请选择截至今天、跨度不超过一年的有效日期范围。
              </p>
            )}
            {error && (
              <p role="alert" className="text-destructive text-sm">
                {error}
              </p>
            )}
            {validRange && !error && !report && (
              <div role="status" className="py-16 text-center text-muted-foreground">
                正在读取每日汇总…
              </div>
            )}
            {validRange &&
              !error &&
              report &&
              (report.generated_at ? (
                <MetricsDashboard
                  report={report}
                  directory={scopeOptions.directory}
                  previous={data?.previous}
                  granularity={granularity}
                  onGranularityChange={setGranularity}
                  onExplain={setGuideTopic}
                />
              ) : (
                <Card>
                  <CardContent className="py-8 text-center text-muted-foreground">
                    所选日期暂无汇总。点击“更新统计”生成首份快照。
                  </CardContent>
                </Card>
              ))}
          </>
        )}
      </div>
    </main>
  );
}

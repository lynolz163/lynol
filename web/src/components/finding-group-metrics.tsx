"use client";

import { useState } from "react";

import Link from "next/link";

import { type FindingGuideTopic, FindingMetricInfo } from "@/components/finding-metrics-guide";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { type FindingMetricsReport, findingMetricDuration, findingSLATotal } from "@/lib/finding-metrics";

export function FindingGroupMetrics({
  report,
  onExplain,
}: {
  report: FindingMetricsReport;
  onExplain: (topic: FindingGuideTopic) => void;
}) {
  const [dimension, setDimension] = useState<"department" | "business_line" | "assignee">("department");
  const dimensions = (["department", "business_line", "assignee"] as const).filter(
    (key) => report.groups.filter((group) => group.dimension === key).length > 1,
  );
  if (!dimensions.length) return null;
  const selected = dimensions.includes(dimension) ? dimension : dimensions[0];
  const groups = report.groups.filter((group) => group.dimension === selected);
  const labels = { department: "部门", business_line: "业务线", assignee: "负责人" };
  const missing = { department: "部门未归属", business_line: "业务线未归属", assignee: "负责人未指派" };
  return (
    <Card className="min-w-0">
      <CardHeader className="flex flex-wrap items-start justify-between gap-3">
        <CardTitle className="flex items-center gap-1">
          {labels[selected]}对比
          <FindingMetricInfo topic="groups" onOpen={onExplain} />
        </CardTitle>
        {dimensions.length > 1 && (
          <div className="flex gap-1">
            {dimensions.map((key) => (
              <Button
                key={key}
                size="sm"
                variant={selected === key ? "default" : "outline"}
                onClick={() => setDimension(key)}
              >
                {labels[key]}
              </Button>
            ))}
          </div>
        )}
      </CardHeader>
      <CardContent>
        <Table className="min-w-[720px]">
          <TableHeader>
            <TableRow>
              <TableHead>{labels[selected]}</TableHead>
              <TableHead className="text-right">待处置</TableHead>
              <TableHead className="text-right">逾期</TableHead>
              <TableHead className="text-right">逾期率</TableHead>
              <TableHead className="text-right">期间关闭</TableHead>
              <TableHead className="text-right">SLA 全量达标</TableHead>
              <TableHead className="text-right">平均闭环</TableHead>
              <TableHead className="text-right">P90</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((group) => (
              <TableRow key={group.id === undefined ? group.name : `${group.dimension}:${group.id}`}>
                <TableCell className="font-medium">
                  {group.name ? (
                    <>
                      {group.name}
                      {group.active === false && <span className="ml-1 text-muted-foreground text-xs">（已停用）</span>}
                    </>
                  ) : (
                    <Link
                      href={
                        selected === "assignee"
                          ? "/settings/org?tab=people"
                          : "/settings/org?tab=assets&asset_status=unassigned"
                      }
                      className="text-primary hover:underline"
                    >
                      {missing[selected]}
                    </Link>
                  )}
                </TableCell>
                <TableCell className="text-right">{report.stock_known ? group.open : "—"}</TableCell>
                <TableCell className={`text-right ${group.overdue > 0 ? "text-red-600" : "text-muted-foreground"}`}>
                  {report.stock_known ? group.overdue : "—"}
                </TableCell>
                <TableCell className={`text-right ${group.overdue > 0 ? "text-red-600" : "text-muted-foreground"}`}>
                  {report.stock_known && group.open ? `${((100 * group.overdue) / group.open).toFixed(1)}%` : "—"}
                </TableCell>
                <TableCell className="text-right">{group.closed}</TableCell>
                <TableCell className="text-right">
                  {report.stock_known && findingSLATotal(group)
                    ? `${((100 * group.sla_met) / findingSLATotal(group)).toFixed(1)}% (${group.sla_met}/${findingSLATotal(group)})`
                    : "—"}
                </TableCell>
                <TableCell className="text-right">{findingMetricDuration(group.average_hours)}</TableCell>
                <TableCell className="text-right">{findingMetricDuration(group.p90_hours)}</TableCell>
              </TableRow>
            ))}
            {!groups.length && (
              <TableRow>
                <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                  当前筛选下暂无分组数据
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

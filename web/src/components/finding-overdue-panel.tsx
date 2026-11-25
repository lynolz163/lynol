"use client";

import { useRef, useState } from "react";

import Link from "next/link";

import { ArrowUpRight, X } from "lucide-react";

import { type FindingGuideTopic, FindingMetricInfo } from "@/components/finding-metrics-guide";
import { StatusBadge } from "@/components/status-badge";
import { TablePagination } from "@/components/table-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { FindingMetricsReport } from "@/lib/finding-metrics";
import { findingOrganizationName } from "@/lib/governance-export";
import type { OrgDirectory } from "@/lib/org";

function OverdueTable({
  items,
  directory,
  emptyMessage,
}: {
  items: FindingMetricsReport["overdue"];
  directory?: OrgDirectory;
  emptyMessage: string;
}) {
  return (
    <Table className="min-w-[680px] table-fixed" aria-label="逾期漏洞明细">
      <TableHeader className="sticky top-0 z-10 bg-card">
        <TableRow>
          <TableHead className="w-[36%]">漏洞 / 编号</TableHead>
          <TableHead className="w-20">等级</TableHead>
          <TableHead>业务线 / 负责人</TableHead>
          <TableHead className="w-24 text-right">存活</TableHead>
          <TableHead className="w-24 text-right">逾期</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((item) => {
          const organization = [
            ...(item.department_id !== undefined || item.department
              ? [findingOrganizationName("department", item.department_id, item.department || "", directory)]
              : []),
            findingOrganizationName(
              "business_line",
              item.business_line_id,
              item.business_line || "业务线未归属",
              directory,
            ),
            findingOrganizationName("assignee", item.owner_id, item.assignee || "负责人未指派", directory),
          ].join(" / ");
          return (
            <TableRow key={item.id} className="hover:bg-muted/60">
              <TableCell>
                <div className="flex min-w-0 items-center gap-2">
                  {item.available ? (
                    <Link
                      className="flex min-w-0 items-center hover:underline"
                      href={`/function/findings/detail?id=${item.id}`}
                      title={item.name}
                    >
                      <span className="truncate">{item.name}</span>
                      <ArrowUpRight className="ml-1 size-3 shrink-0" />
                    </Link>
                  ) : (
                    <span className="truncate" title={item.name}>
                      {item.name}
                    </span>
                  )}
                  <span className="shrink-0 text-muted-foreground text-xs">#{item.id}</span>
                </div>
              </TableCell>
              <TableCell>
                <StatusBadge domain="severity" value={item.severity} />
              </TableCell>
              <TableCell className="text-muted-foreground text-xs">
                <div className="truncate" title={organization}>
                  {organization}
                </div>
              </TableCell>
              <TableCell className="text-right tabular-nums">{Number(item.age_days.toFixed(1))} 天</TableCell>
              <TableCell className="text-right text-red-600 tabular-nums">
                +{Number(item.overdue_days.toFixed(1))} 天
              </TableCell>
            </TableRow>
          );
        })}
        {!items.length && (
          <TableRow>
            <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
              {emptyMessage}
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}

export function FindingOverduePanel({
  report,
  directory,
  onExplain,
}: {
  report: FindingMetricsReport;
  directory?: OrgDirectory;
  onExplain: (topic: FindingGuideTopic) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const scrollArea = useRef<HTMLDivElement>(null);
  const keyword = query.trim().toLocaleLowerCase().replace(/^#/, "");
  const matching = report.overdue.filter(
    (item) => !keyword || `${item.name} ${item.id}`.toLocaleLowerCase().includes(keyword),
  );
  const safePage = Math.min(page, Math.max(1, Math.ceil(matching.length / pageSize)));
  const incomplete = report.stock_known && report.overdue.length < report.summary.overdue;
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle className="flex items-center gap-1">
          逾期漏洞
          <Badge variant="secondary" className="ml-1">
            {report.stock_known ? report.summary.overdue : "—"}
          </Badge>
          <FindingMetricInfo topic="overdue" onOpen={onExplain} />
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="[&>div]:max-h-[420px] [&>div]:overflow-auto">
          <OverdueTable
            items={report.overdue.slice(0, 10)}
            directory={directory}
            emptyMessage={report.stock_known ? "当前筛选下没有逾期漏洞" : "该日期暂无完整的存量快照"}
          />
        </div>
        {report.overdue.length > 10 && (
          <Sheet
            open={open}
            onOpenChange={(value) => {
              setOpen(value);
              if (value) {
                setQuery("");
                setPage(1);
              }
            }}
          >
            <div className="mt-3 flex items-center justify-between gap-3">
              <p className="text-muted-foreground text-xs">仅显示逾期最久的 10 条</p>
              <SheetTrigger asChild>
                <Button variant="ghost" size="sm">
                  查看全部（{report.overdue.length}）
                </Button>
              </SheetTrigger>
            </div>
            <SheetContent
              className="gap-0 overflow-hidden data-[side=right]:w-full data-[side=right]:sm:max-w-5xl"
              showCloseButton={false}
            >
              <SheetHeader className="shrink-0 border-b px-6 py-5 pr-14">
                <SheetTitle>逾期漏洞 · {report.overdue.length} 条明细</SheetTitle>
                <SheetDescription>按当前筛选范围的期末快照展示，逾期最久的排在前面。</SheetDescription>
              </SheetHeader>
              <SheetClose asChild>
                <Button variant="ghost" size="icon-sm" className="absolute top-4 right-4" aria-label="关闭逾期漏洞列表">
                  <X />
                </Button>
              </SheetClose>
              <div className="shrink-0 space-y-2 px-6 py-4">
                <Input
                  aria-label="搜索逾期漏洞名称或编号"
                  placeholder="搜索漏洞名称或编号"
                  value={query}
                  onChange={(event) => {
                    setQuery(event.target.value);
                    setPage(1);
                    scrollArea.current?.firstElementChild?.scrollTo({ top: 0 });
                  }}
                />
                {incomplete && (
                  <p className="text-muted-foreground text-xs">
                    该快照保留 {report.overdue.length} 条可查看明细，统计总数为 {report.summary.overdue} 条。
                  </p>
                )}
              </div>
              <div ref={scrollArea} className="min-h-0 flex-1 px-6 [&>div]:h-full [&>div]:overflow-auto">
                <OverdueTable
                  items={matching.slice((safePage - 1) * pageSize, safePage * pageSize)}
                  directory={directory}
                  emptyMessage="没有匹配的逾期漏洞"
                />
              </div>
              <div className="shrink-0" aria-live="polite">
                <TablePagination
                  page={safePage}
                  pageSize={pageSize}
                  total={matching.length}
                  onPageChange={(value) => {
                    setPage(value);
                    scrollArea.current?.firstElementChild?.scrollTo({ top: 0 });
                  }}
                  onPageSizeChange={(value) => {
                    setPageSize(value);
                    setPage(1);
                    scrollArea.current?.firstElementChild?.scrollTo({ top: 0 });
                  }}
                />
              </div>
            </SheetContent>
          </Sheet>
        )}
        {incomplete && (
          <p className="mt-2 text-muted-foreground text-xs">
            该快照保留 {report.overdue.length} 条可查看明细，统计总数为 {report.summary.overdue} 条。
          </p>
        )}
      </CardContent>
    </Card>
  );
}

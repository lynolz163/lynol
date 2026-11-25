"use client";

import { Suspense, useEffect, useState } from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { type FindingMetricsFilter, type FindingMetricsReport, GOVERNANCE_MOCK } from "@/lib/finding-metrics";
import { downloadGovernanceWorkbook, findingFilterLabel, governanceSheets } from "@/lib/governance-export";
import { type OrgDirectory, orgAPI } from "@/lib/org";

function Report() {
  const q = useSearchParams();
  const [data, setData] = useState<{
    title: string;
    from: string;
    to: string;
    filter: FindingMetricsFilter;
    current: FindingMetricsReport;
    previous?: FindingMetricsReport;
    attribution_version?: number;
  }>();
  const [error, setError] = useState("");
  const [directory, setDirectory] = useState<OrgDirectory>();
  useEffect(() => {
    if (GOVERNANCE_MOCK) return;
    let active = true;
    void orgAPI
      .directory()
      .then((value) => {
        if (active) setDirectory(value);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  const id = q.get("id"),
    from = q.get("from") ?? "",
    to = q.get("to") ?? "",
    severity = q.get("severity") ?? "",
    businessLine = q.get("business_line") ?? "",
    assignee = q.get("assignee") ?? "",
    departmentID = q.has("department_id") ? Number(q.get("department_id")) : undefined,
    lineID = q.has("business_line_id") ? Number(q.get("business_line_id")) : undefined,
    ownerID = q.has("owner_id") ? Number(q.get("owner_id")) : undefined;
  useEffect(() => {
    let active = true;
    async function load() {
      if (id) {
        const report = await api.findingReport(Number(id));
        if (!report.current) throw new Error("报告内容不存在");
        return {
          title: `漏洞治理${report.kind === "weekly" ? "周报" : "月报"}`,
          from: report.from,
          to: report.to,
          filter: report.filter,
          current: report.current,
          previous: report.previous,
          attribution_version: report.attribution_version,
        };
      }
      const filter = {
        severity,
        business_line: businessLine,
        assignee,
        department_id: departmentID,
        business_line_id: lineID,
        owner_id: ownerID,
      };
      const current = await api.findingMetrics(from, to, filter);
      return { title: "漏洞治理报告", from, to, filter, current };
    }
    load().then(
      (value) => {
        if (active) setData(value);
      },
      (reason) => {
        if (active) setError((reason as Error).message);
      },
    );
    return () => {
      active = false;
    };
  }, [id, from, to, severity, businessLine, assignee, departmentID, lineID, ownerID]);
  if (error)
    return (
      <main className="p-8">
        <p role="alert">{error}</p>
        <Link href="/function/remediation">返回漏洞治理</Link>
      </main>
    );
  if (!data)
    return (
      <p role="status" className="p-8">
        正在读取报告…
      </p>
    );
  const sheets = governanceSheets(data.current, data.from, data.to, data.filter, data.previous, directory);
  return (
    <main className="governance-print mx-auto max-w-6xl space-y-6 bg-white p-6 text-slate-900 md:p-10">
      <style>{`@page{size:A4 landscape;margin:12mm}@media print{html,body{background:white!important;overflow:visible!important;height:auto!important}.governance-print{max-width:none!important;padding:0!important;font-size:10px!important}.governance-print .no-print{display:none!important}.governance-print section{break-inside:auto}.governance-print tr{break-inside:avoid}.governance-print thead{display:table-header-group}.governance-print h2{break-after:avoid}}`}</style>
      <div className="no-print flex flex-wrap gap-2">
        <Button asChild variant="outline">
          <Link href="/function/remediation">返回仪表盘</Link>
        </Button>
        <Button onClick={() => window.print()}>打印 / 保存 PDF</Button>
        <Button
          variant="outline"
          onClick={() =>
            downloadGovernanceWorkbook(data.current, data.from, data.to, data.filter, data.previous, directory)
          }
        >
          导出 Excel
        </Button>
      </div>
      <header className="space-y-2 border-slate-300 border-b pb-5">
        <h1 className="font-semibold text-3xl">{data.title}</h1>
        <p>
          {data.from} 至 {data.to} · 北京时间
        </p>
        <p className="text-slate-600 text-sm">{findingFilterLabel(data.filter, directory)}</p>
        {id && (
          <p className="text-slate-600 text-xs">
            {(data.attribution_version ?? 0) === 0
              ? "旧口径报告 · 保留生成时结果"
              : "按生成时的归属统计 · 已保存报告不重算"}
          </p>
        )}
        {GOVERNANCE_MOCK && <p className="text-blue-700 text-sm">本地示例数据 · 用于验收，不代表真实漏洞统计</p>}
        {!data.current.stock_known && (
          <p className="text-amber-700 text-sm">该期间的完整状态历史尚未采集，存量和逾期数据未知。</p>
        )}
      </header>
      {sheets.map((sheet) => (
        <section key={sheet.name} className="space-y-3">
          <h2 className="font-semibold text-lg">{sheet.name}</h2>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-xs">
              <thead>
                <tr>
                  {sheet.rows[0].map((cell) => (
                    <th key={cell} className="border border-slate-300 bg-slate-100 p-2 font-semibold">
                      {cell}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {sheet.rows.slice(1).map((row, rowIndex) => (
                  <tr key={`${sheet.name}:${rowIndex}:${row[0]}`}>
                    {row.map((cell, col) => (
                      <td key={sheet.rows[0][col]} className="border border-slate-200 p-2 align-top">
                        {typeof cell === "number" && !Number.isInteger(cell) ? cell.toFixed(2) : cell}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ))}
    </main>
  );
}

export default function RemediationReportPage() {
  return (
    <Suspense fallback={<p className="p-8">正在读取报告…</p>}>
      <Report />
    </Suspense>
  );
}

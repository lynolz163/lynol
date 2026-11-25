"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { FindingHistoryEvent } from "@/lib/finding-metrics";
import { statusMeta } from "@/lib/status";
import type { Finding } from "@/lib/types";

const eventLabels: Record<string, string> = {
  discovered: "发现漏洞",
  sla_changed: "SLA 规则调整",
  assignment_changed: "归属调整",
  dispatched: "漏洞下发",
  developer_fixed: "开发反馈已修复",
  retest_requested: "发起复测",
  retest_started: "开始复测",
  retest_completed: "复测结束",
  status_changed: "状态变更",
  classification_changed: "分类调整",
  baseline: "历史状态基线",
  removed: "删除漏洞",
};
const verdictLabels: Record<string, string> = { fixed: "确认修复", reproduced: "仍可复现", inconclusive: "无明确结论" };

export function FindingHistoryPanel({
  finding,
  onHistory,
}: {
  finding: Finding;
  onHistory: (events: FindingHistoryEvent[]) => void;
}) {
  const [events, setEvents] = useState<FindingHistoryEvent[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);
  useEffect(() => {
    let active = true;
    api.findingHistory(finding.id).then(
      ({ items }) => {
        if (!active) return;
        setEvents(items);
        setMore(items.length === 50);
        setLoading(false);
        setError("");
        onHistory(items);
      },
      () => {
        if (active) {
          setLoading(false);
          setError("读取流转历史失败");
        }
      },
    );
    return () => {
      active = false;
    };
  }, [finding, onHistory]);

  async function loadMore() {
    setLoading(true);
    try {
      const { items } = await api.findingHistory(finding.id, events[events.length - 1].id);
      setEvents((previous) => [...previous, ...items]);
      setMore(items.length === 50);
      setError("");
    } catch {
      setError("读取更早记录失败，请重试");
    } finally {
      setLoading(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">流转历史</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <p role="alert" className="text-destructive text-xs">
            {error}
          </p>
        )}
        <ol className="max-h-96 space-y-4 overflow-y-auto">
          {events.map((event) => (
            <li key={event.id} className="border-muted border-l-2 pl-3 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="font-medium">{eventLabels[event.kind] ?? event.kind}</span>
                <time className="text-muted-foreground" dateTime={event.occurred_at}>
                  {new Date(event.occurred_at).toLocaleString("zh-CN")}
                </time>
              </div>
              {event.kind === "assignment_changed" && (
                <p className="mt-1">
                  业务线：{event.metadata.old_business_line || "业务线未归属"} →{" "}
                  {event.metadata.business_line || "业务线未归属"}
                  ；负责人：{event.metadata.old_assignee || "负责人未指派"} →{" "}
                  {event.metadata.assignee || "负责人未指派"}
                </p>
              )}
              {event.new_status && (
                <p className="mt-1">
                  {event.old_status && `${statusMeta("finding", event.old_status).label} → `}
                  {statusMeta("finding", event.new_status).label}
                </p>
              )}
              {event.kind === "retest_completed" && (
                <p className="mt-1">
                  {event.metadata.status === "completed"
                    ? (verdictLabels[event.metadata.verdict ?? ""] ?? "无明确结论")
                    : "中止或执行失败"}
                </p>
              )}
              <p className="mt-1 text-muted-foreground">
                {event.actor}
                {event.source === "backfill" && " · 根据已有时间回填"}
                {event.source === "baseline" && " · 当时状态已知，原变更时间未知"}
              </p>
            </li>
          ))}
        </ol>
        {loading && !events.length && <p className="text-muted-foreground text-xs">正在读取历史…</p>}
        {!loading && !events.length && !error && <p className="text-muted-foreground text-xs">暂无流转记录</p>}
        {more && (
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void loadMore()}>
            更早记录
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

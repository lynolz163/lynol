import {
  type FindingHistoryEvent,
  type FindingMetricBucket,
  type FindingMetricGroup,
  type FindingMetricsFilter,
  type FindingMetricsReport,
  type FindingReportSchedule,
  type FindingSLA,
  findingMetricDate,
  type SavedFindingReport,
} from "../finding-metrics";
import type { Finding, FindingRetest, Severity } from "../types";

export let mockFindingSLA: FindingSLA = { critical: 3, high: 7, medium: 30, low: 90 };
const initialSLA = { ...mockFindingSLA };
export function saveMockFindingSLA(rules: FindingSLA) {
  mockFindingSLA = { ...rules };
  return mockFindingSLA;
}

const dayMs = 86400000;
const today = findingMetricDate(new Date());
const reference = Date.parse(`${today}T12:00:00+08:00`);
const grades: Severity[] = ["critical", "high", "medium", "low"];
const samples = Array.from({ length: 96 }, (_, index) => {
  const discovered = reference - (((index * 7) % 55) + 1) * dayMs;
  const duration = (1 + ((index * 11) % 24)) * dayMs;
  const candidate = discovered + duration;
  return {
    excluded: false,
    id: `example-${index}`,
    name: ["访问权限配置复核", "敏感信息暴露", "安全响应头缺失", "接口权限校验", "输入校验缺陷"][index % 5],
    severity: grades[index % 4],
    business_line: ["核心业务", "数据平台", "基础设施", ""][index % 4],
    assignee: ["研发一组", "平台组", "研发二组", ""][Math.floor(index / 4) % 4],
    discovered,
    closed: index % 5 !== 0 && candidate < reference ? candidate : null,
    duration,
  };
});

export const governancePreviewFindings: Finding[] = samples.map((item, index) => ({
  id: item.id,
  finding_id: item.id,
  name: item.name,
  vulnclass: item.name,
  severity: item.severity,
  status: item.closed ? "fixed" : "confirmed",
  business_line: item.business_line,
  assignee: item.assignee,
  summary: "用于验收业务线归属和漏洞治理的示例数据。",
  evidence: "本地示例，不对应真实漏洞。",
  ts: new Date(item.discovered).toISOString(),
  verified_fixed_at: item.closed ? new Date(item.closed).toISOString() : undefined,
  dispatched_at: new Date(item.discovered + item.duration * 0.04).toISOString(),
  developer_fixed_at: item.closed ? new Date(item.discovered + item.duration * 0.7).toISOString() : undefined,
  assets: [
    {
      id: `preview-asset-${index}`,
      type: "domain",
      label: ["api.core.example.com", "db.data.example.com", "ops.infra.example.com", "new.core.example.com"][
        index % 4
      ],
    },
  ],
}));
let businessPolicies: Record<string, FindingSLA> = {};
export function syncMockGovernanceFindings(findings: Finding[], policies: Record<string, FindingSLA>) {
  businessPolicies = policies;
  for (const finding of findings) {
    let sample = samples.find((item) => item.id === finding.id);
    const closed = ["fixed", "resolved"].includes(finding.status)
      ? Date.parse(finding.verified_fixed_at ?? finding.ts)
      : null;
    if (!sample) {
      sample = {
        excluded: false,
        id: finding.id,
        name: finding.name ?? finding.vulnclass,
        severity: finding.severity,
        business_line: finding.business_line ?? "",
        assignee: finding.assignee ?? "",
        discovered: Date.parse(finding.ts),
        closed,
        duration: closed ? Math.max(0, closed - Date.parse(finding.ts)) : 7 * dayMs,
      };
      samples.push(sample);
    }
    sample.excluded = !["pending", "in_progress", "confirmed", "fixed", "resolved"].includes(finding.status);
    sample.business_line = finding.business_line ?? "";
    sample.assignee = finding.assignee ?? "";
    sample.closed = closed;
  }
}

function emptyBucket(severity = "", vulnclass = ""): FindingMetricBucket {
  return {
    severity,
    vulnclass,
    business_line: "",
    assignee: "",
    new: 0,
    closed: 0,
    reopened: 0,
    open: 0,
    overdue: 0,
    sla_met: 0,
    sla_closed: 0,
    retest_passed: 0,
    retest_failed: 0,
    retest_other: 0,
    first_retest_passed: 0,
    first_retest_total: 0,
    verified_fixes: 0,
    rework_rounds: 0,
    ages: [0, 0, 0],
  };
}

export function mockFindingMetrics(q: URLSearchParams): FindingMetricsReport {
  const from = q.get("from") ?? today;
  const to = q.get("to") ?? today;
  const selected = samples.filter(
    (item) =>
      !item.excluded &&
      ["severity", "business_line", "assignee"].every((key) => {
        const value = q.get(key),
          actual = item[key as "severity" | "business_line" | "assignee"];
        return !value || value === actual || (value === "__none__" && actual === "");
      }),
  );
  const summary = emptyBucket();
  const reportSLA = to === today ? mockFindingSLA : initialSLA;
  const report: FindingMetricsReport = {
    generated_at: new Date().toISOString(),
    history_since: new Date(reference - 60 * dayMs).toISOString(),
    stock_known: true,
    quality_known: true,
    summary,
    trend: [],
    durations: [],
    distribution: [],
    groups: [],
    overdue: [],
    sla: reportSLA,
    has_custom_sla: selected.some((item) => Boolean(businessPolicies[item.business_line])),
  };
  const stageValues = {
    dispatch: [] as number[],
    development: [] as number[],
    waiting: [] as number[],
    verification: [] as number[],
    closure: [] as number[],
    rework: [] as number[],
  };
  const groups = new Map<string, FindingMetricBucket>();
  const start = Date.parse(`${from}T00:00:00+08:00`);
  const end = Date.parse(`${to}T23:59:59+08:00`);
  for (const item of selected) {
    const key = `${item.severity}:${item.name}:${item.business_line}:${item.assignee}`;
    let bucket = groups.get(key);
    if (!bucket) {
      bucket = { ...emptyBucket(item.severity, item.name), business_line: item.business_line, assignee: item.assignee };
      groups.set(key, bucket);
    }
    if (item.discovered >= start && item.discovered <= end) {
      summary.new++;
      bucket.new++;
    }
    if (item.closed && item.closed >= start && item.closed <= end) {
      summary.closed++;
      bucket.closed++;
      summary.sla_closed++;
      bucket.sla_closed++;
      summary.retest_passed++;
      bucket.retest_passed++;
      summary.verified_fixes++;
      bucket.verified_fixes++;
      if (item.severity === "critical") {
        summary.rework_rounds++;
        bucket.rework_rounds++;
        stageValues.rework.push((item.closed - item.discovered - dayMs) / 3600000);
      }
      const policy = findingMetricDate(new Date(item.closed)) === today ? mockFindingSLA : initialSLA;
      if (item.duration <= policy[item.severity] * dayMs) {
        summary.sla_met++;
        bucket.sla_met++;
      }
      stageValues.closure.push(item.duration / 3600000);
    }
    for (const [key, fraction, previous] of [
      ["dispatch", 0.04, 0],
      ["development", 0.7, 0.04],
      ["waiting", 0.85, 0.7],
      ["verification", 1, 0.85],
    ] as const) {
      const at = item.discovered + item.duration * fraction;
      if (at >= start && at <= end && at <= reference && (key !== "verification" || item.closed)) {
        stageValues[key].push((item.duration * (fraction - previous)) / 3600000);
      }
    }
    const failedAt = item.discovered + dayMs;
    if (item.severity === "critical" && failedAt >= start && failedAt <= end) {
      summary.retest_failed++;
      bucket.retest_failed++;
    }
    const firstVerdictAt = item.severity === "critical" ? failedAt : item.closed;
    if (firstVerdictAt && firstVerdictAt >= start && firstVerdictAt <= end) {
      summary.first_retest_total++;
      bucket.first_retest_total++;
      if (item.severity !== "critical") {
        summary.first_retest_passed++;
        bucket.first_retest_passed++;
      }
    }
    if (item.discovered > end || (item.closed && item.closed <= end)) continue;
    const age = (Math.min(end, reference) - item.discovered) / dayMs;
    summary.open++;
    bucket.open++;
    let ageIndex = 0;
    if (age >= 30) ageIndex = 2;
    else if (age >= 7) ageIndex = 1;
    summary.ages[ageIndex]++;
    bucket.ages[ageIndex]++;
    const slaDays =
      (to === today ? businessPolicies[item.business_line] : undefined)?.[item.severity] ?? reportSLA[item.severity];
    if (age > slaDays) {
      summary.overdue++;
      bucket.overdue++;
      report.overdue.push({
        business_line: item.business_line,
        assignee: item.assignee,
        id: item.id,
        name: item.name,
        severity: item.severity,
        age_days: age,
        overdue_days: age - slaDays,
        available: true,
      });
    }
  }
  for (let time = start; time <= end; time += dayMs) {
    const boundary = time + dayMs;
    const active = selected.filter((item) => item.discovered < boundary && (!item.closed || item.closed >= boundary));
    const policy = findingMetricDate(new Date(time)) === today ? mockFindingSLA : initialSLA;
    report.trend.push({
      day: findingMetricDate(new Date(time)),
      new: selected.filter((item) => item.discovered >= time && item.discovered < boundary).length,
      closed: selected.filter((item) => item.closed && item.closed >= time && item.closed < boundary).length,
      reopened: 0,
      open: active.length,
      overdue: active.filter(
        (item) =>
          (boundary - item.discovered) / dayMs >
          ((findingMetricDate(new Date(time)) === today ? businessPolicies[item.business_line] : undefined)?.[
            item.severity
          ] ?? policy[item.severity]),
      ).length,
    });
  }
  report.distribution = [...groups.values()];
  for (const dimension of ["business_line", "assignee"] as const) {
    const names = [...new Set(selected.map((item) => item[dimension]))];
    for (const name of names) {
      const buckets = report.distribution.filter((item) => item[dimension] === name);
      const durations = selected
        .filter((item) => item[dimension] === name && item.closed && item.closed >= start && item.closed <= end)
        .map((item) => item.duration / 3600000)
        .sort((a, b) => a - b);
      const sum = (key: "open" | "overdue" | "closed" | "sla_met" | "sla_closed") =>
        buckets.reduce((total, item) => total + item[key], 0);
      const group: FindingMetricGroup = {
        dimension,
        name,
        open: sum("open"),
        overdue: sum("overdue"),
        closed: sum("closed"),
        sla_met: sum("sla_met"),
        sla_closed: sum("sla_closed"),
        average_hours: durations.length ? durations.reduce((a, b) => a + b, 0) / durations.length : null,
        p90_hours: durations.length ? durations[Math.ceil(durations.length * 0.9) - 1] : null,
      };
      report.groups.push(group);
    }
  }
  report.groups.sort(
    (a, b) =>
      a.dimension.localeCompare(b.dimension) ||
      b.overdue - a.overdue ||
      b.open - a.open ||
      a.name.localeCompare(b.name),
  );
  report.overdue.sort((a, b) => b.overdue_days - a.overdue_days);
  report.durations = Object.entries(stageValues).map(([key, values]) => {
    values.sort((a, b) => a - b);
    return {
      key,
      count: values.length,
      average_hours: values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null,
      p90_hours: values.length ? values[Math.ceil(values.length * 0.9) - 1] : null,
    };
  });
  return report;
}

let nextHistoryID = 1;
const history = new Map<string, FindingHistoryEvent[]>();
const retestKeys = new Set<string>();

function addEvent(
  finding: Finding,
  kind: string,
  at: string,
  source: FindingHistoryEvent["source"],
  extra: Partial<FindingHistoryEvent> = {},
) {
  const items = history.get(finding.id) ?? [];
  items.push({
    id: nextHistoryID++,
    finding_id: finding.id,
    kind,
    old_status: "",
    new_status: "",
    actor: "本地示例",
    occurred_at: at,
    source,
    metadata: {},
    ...extra,
  });
  history.set(finding.id, items);
}

export function mockFindingHistory(finding: Finding, retests: FindingRetest[] = []) {
  if (!history.has(finding.id)) {
    addEvent(finding, "discovered", finding.ts, "backfill", { new_status: "pending" });
    if (finding.dispatched_at) addEvent(finding, "dispatched", finding.dispatched_at, "backfill");
    if (finding.developer_fixed_at) addEvent(finding, "developer_fixed", finding.developer_fixed_at, "backfill");
    addEvent(finding, "baseline", new Date().toISOString(), "baseline", { new_status: finding.status });
  }
  for (const retest of retests) {
    for (const [kind, at] of [
      ["retest_requested", retest.created_at],
      ["retest_started", retest.started_at],
      ["retest_completed", retest.finished_at],
    ] as const) {
      const key = `${finding.id}:${retest.id}:${kind}`;
      if (!at || retestKeys.has(key)) continue;
      addEvent(finding, kind, at, "recorded", {
        actor: "模拟复测",
        metadata: { status: retest.status, verdict: retest.verdict },
      });
      retestKeys.add(key);
    }
  }
  return [...(history.get(finding.id) ?? [])].sort(
    (a, b) => Date.parse(b.occurred_at) - Date.parse(a.occurred_at) || b.id - a.id,
  );
}

export function recordMockFindingChange(before: Finding, after: Finding) {
  mockFindingHistory(before);
  if (before.business_line !== after.business_line || before.assignee !== after.assignee)
    addEvent(after, "assignment_changed", new Date().toISOString(), "recorded", {
      metadata: {
        business_line: after.business_line ?? "",
        assignee: after.assignee ?? "",
        old_business_line: before.business_line ?? "",
        old_assignee: before.assignee ?? "",
      },
    });
  if (before.status !== after.status)
    addEvent(after, "status_changed", new Date().toISOString(), "recorded", {
      old_status: before.status,
      new_status: after.status,
    });
  if (before.severity !== after.severity)
    addEvent(after, "classification_changed", new Date().toISOString(), "recorded", {
      metadata: { severity: after.severity },
    });
  if (after.dispatched_at && before.dispatched_at !== after.dispatched_at)
    addEvent(after, "dispatched", after.dispatched_at, "recorded");
  if (after.developer_fixed_at && before.developer_fixed_at !== after.developer_fixed_at)
    addEvent(after, "developer_fixed", after.developer_fixed_at, "recorded");
}

export function canRecordMockMilestone(finding: Finding, field: "dispatched_at" | "developer_fixed_at") {
  const at = finding[field];
  return (
    !at ||
    mockFindingHistory(finding).some(
      (event) =>
        Date.parse(event.occurred_at) > Date.parse(at) &&
        ((event.kind === "status_changed" &&
          ["fixed", "resolved"].includes(event.old_status) &&
          !["fixed", "resolved"].includes(event.new_status)) ||
          (event.kind === "retest_completed" &&
            event.metadata.status === "completed" &&
            event.metadata.verdict === "reproduced")),
    )
  );
}

export function validateMockMilestoneTime(finding: Finding, field: "dispatched_at" | "developer_fixed_at", at: string) {
  const time = Date.parse(at);
  const reset = mockFindingHistory(finding).find(
    (event) =>
      (event.kind === "status_changed" &&
        ["fixed", "resolved"].includes(event.old_status) &&
        !["fixed", "resolved"].includes(event.new_status)) ||
      (event.kind === "retest_completed" &&
        event.metadata.status === "completed" &&
        event.metadata.verdict === "reproduced"),
  );
  const cycle = Math.max(Date.parse(finding.ts), reset ? Date.parse(reset.occurred_at) : 0);
  const dispatched = Date.parse(finding.dispatched_at ?? "");
  const fixed = Date.parse(finding.developer_fixed_at ?? "");
  if (!Number.isFinite(time) || time > Date.now()) throw new Error("处置时间无效或晚于当前时间");
  if (
    time < cycle ||
    (field === "developer_fixed_at" && dispatched >= cycle && time < dispatched) ||
    (field === "dispatched_at" && fixed >= cycle && time > fixed)
  )
    throw new Error(`漏洞 #${finding.id}：时间不能早于发现或本轮重开，下发时间不能晚于本轮开发反馈修复时间`);
}

export const mockReportSchedules: FindingReportSchedule[] = (["weekly", "monthly"] as const).map((kind) => ({
  kind,
  enabled: false,
  weekday: 1,
  monthday: 1,
  hour: 9,
  minute: 0,
  channel_id: null,
  filter: { severity: "", business_line: "", assignee: "" },
}));
// Reports open in another tab; persist preview snapshots so those links remain usable.
const reportStorageKey = "artex.preview.governance.reports.v1";
export const mockSavedReports: SavedFindingReport[] =
  typeof window === "undefined" ? [] : JSON.parse(localStorage.getItem(reportStorageKey) ?? "[]");
export function mockGenerateFindingReport(kind: "weekly" | "monthly", filter: FindingMetricsFilter) {
  const boundary = new Date(`${today}T00:00:00Z`);
  if (kind === "monthly") boundary.setUTCDate(1);
  else boundary.setUTCDate(boundary.getUTCDate() - ((boundary.getUTCDay() + 6) % 7));
  const from = new Date(boundary),
    prior = new Date(boundary);
  if (kind === "monthly") {
    from.setUTCMonth(from.getUTCMonth() - 1);
    prior.setUTCMonth(prior.getUTCMonth() - 2);
  } else {
    from.setUTCDate(from.getUTCDate() - 7);
    prior.setUTCDate(prior.getUTCDate() - 14);
  }
  const iso = (date: Date) => date.toISOString().slice(0, 10);
  const to = new Date(boundary.getTime() - dayMs),
    previousTo = new Date(from.getTime() - dayMs);
  const existing = mockSavedReports.find(
    (report) =>
      report.kind === kind && report.from === iso(from) && JSON.stringify(report.filter) === JSON.stringify(filter),
  );
  if (existing) return { id: existing.id };
  const report: SavedFindingReport = {
    id: mockSavedReports.length + 1,
    kind,
    from: iso(from),
    to: iso(to),
    generated_at: new Date().toISOString(),
    filter: { ...filter },
    current: structuredClone(
      mockFindingMetrics(
        new URLSearchParams({
          from: iso(from),
          to: iso(to),
          severity: filter.severity,
          business_line: filter.business_line,
          assignee: filter.assignee,
        }),
      ),
    ),
    previous: structuredClone(
      mockFindingMetrics(
        new URLSearchParams({
          from: iso(prior),
          to: iso(previousTo),
          severity: filter.severity,
          business_line: filter.business_line,
          assignee: filter.assignee,
        }),
      ),
    ),
    delivery_state: "generated",
    delivery_error: "",
    delivery_id: null,
  };
  mockSavedReports.unshift(report);
  if (typeof window !== "undefined") localStorage.setItem(reportStorageKey, JSON.stringify(mockSavedReports));
  return { id: report.id };
}
mockGenerateFindingReport("weekly", { severity: "", business_line: "", assignee: "" });
mockGenerateFindingReport("monthly", { severity: "", business_line: "", assignee: "" });

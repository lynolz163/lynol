import type { OrgDirectory } from "./org";
import type { Severity } from "./types";

export const GOVERNANCE_MOCK =
  process.env.NEXT_PUBLIC_MOCK === "1" && process.env.NEXT_PUBLIC_GOVERNANCE_REAL_API !== "1";

export type FindingSLA = Record<Severity, number>;

export interface FindingHistoryEvent {
  recorded_at?: string;
  asset_id?: number | null;
  department_id?: number | null;
  business_line_id?: number | null;
  owner_id?: number | null;
  attribution_version?: number;
  attribution_quality?: string;
  owner_resolution?: string;
  sla_days?: number | null;
  id: number;
  finding_id: string;
  kind: string;
  old_status: string;
  new_status: string;
  actor: string;
  occurred_at: string;
  source: "recorded" | "backfill" | "baseline";
  metadata: {
    severity?: string;
    vulnclass?: string;
    status?: string;
    verdict?: string;
    business_line?: string;
    assignee?: string;
    old_business_line?: string;
    old_assignee?: string;
  };
}

export interface FindingMetricBucket {
  department_id?: number | null;
  business_line_id?: number | null;
  owner_id?: number | null;
  department?: string;
  business_line: string;
  assignee: string;
  severity: string;
  vulnclass: string;
  new: number;
  closed: number;
  reopened: number;
  open: number;
  overdue: number;
  sla_met: number;
  sla_closed: number;
  retest_passed: number;
  retest_failed: number;
  retest_other: number;
  first_retest_passed: number;
  first_retest_total: number;
  verified_fixes: number;
  rework_rounds: number;
  ages: [number, number, number];
}

export interface FindingMetricTrend {
  day: string;
  new: number;
  closed: number;
  reopened: number;
  open: number | null;
  overdue: number | null;
}

export interface FindingMetricsReport {
  attribution_version?: number;
  snapshot_ids?: number[];
  data_quality?: {
    unknown_attribution: number;
    missing_sla: number;
    missing_discovery: number;
    missing_snapshot_days: number;
  };
  filter_options?: { business_line_ids: number[]; owner_ids: number[] };
  has_custom_sla?: boolean;
  groups: FindingMetricGroup[];
  generated_at: string | null;
  history_since: string | null;
  stock_known: boolean;
  quality_known: boolean;
  summary: FindingMetricBucket;
  trend: FindingMetricTrend[];
  durations: { key: string; count: number; average_hours: number | null; p90_hours: number | null }[];
  distribution: FindingMetricBucket[];
  overdue: {
    department_id?: number | null;
    business_line_id?: number | null;
    owner_id?: number | null;
    department?: string;
    business_line: string;
    assignee: string;
    id: string;
    name: string;
    severity: string;
    age_days: number;
    overdue_days: number;
    available: boolean;
  }[];
  sla: FindingSLA | null;
}

export const findingSeverityLabels = { critical: "严重", high: "高危", medium: "中危", low: "低危" };
export const findingStageLabels: Record<string, string> = {
  dispatch: "发现 / 重开 → 下发",
  development: "下发 → 开发反馈修复",
  waiting: "开发反馈修复 → 发起复测",
  verification: "发起复测 → 验证通过",
  closure: "发现 → 关闭",
  rework: "首次复测不通过 → 验证通过",
};

export function findingSLATotal(bucket: { sla_closed: number; overdue: number }) {
  return bucket.sla_closed + bucket.overdue;
}

export function findingMetricDate(date: Date) {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Shanghai" }).format(date);
}

export function findingMetricDuration(hours: number | null) {
  if (hours === null) return "—";
  if (hours < 1) return `${Math.round(hours * 60)} 分钟`;
  if (hours < 24) return `${Number(hours.toFixed(1))} 小时`;
  return `${Number((hours / 24).toFixed(1))} 天`;
}

export interface FindingMetricsFilter {
  department_id?: number | null;
  business_line_id?: number | null;
  owner_id?: number | null;
  severity: string;
  business_line: string;
  assignee: string;
}
export interface FindingMetricGroup {
  dimension: "department" | "business_line" | "assignee";
  id?: number | null;
  active?: boolean;
  name: string;
  open: number;
  overdue: number;
  closed: number;
  sla_met: number;
  sla_closed: number;
  average_hours: number | null;
  p90_hours: number | null;
}
export interface FindingReportSchedule {
  department_ids?: number[];
  scope_mode?: "departments";
  kind: "weekly" | "monthly";
  enabled: boolean;
  weekday: number;
  monthday: number;
  hour: number;
  minute: number;
  channel_id: number | null;
  filter: FindingMetricsFilter;
}
export interface SavedFindingReport {
  attribution_version?: number;
  attribution_basis?: string;
  department_id?: number | null;
  source_snapshot_ids?: number[];
  delivery_context?: {
    state: "ready" | "unavailable" | "queued";
    department_id: number;
    head_id: number | null;
    head_name: string;
    head_email: string;
    channel_ids: number[];
    message: string;
  };
  id: number;
  kind: "weekly" | "monthly";
  from: string;
  to: string;
  generated_at: string;
  filter: FindingMetricsFilter;
  current?: FindingMetricsReport;
  previous?: FindingMetricsReport;
  delivery_state: string;
  delivery_error: string;
  delivery_id: number | null;
}
export interface FindingResponsibilityOptions {
  directory?: OrgDirectory;
  historical_owner_ids?: number[];
  historical_line_ids?: number[];
  business_lines: string[];
  assignees: string[];
}
export const emptyFindingFilter: FindingMetricsFilter = { severity: "", business_line: "", assignee: "" };
export const findingDeliveryLabels: Record<string, string> = {
  unavailable: "未配置投递",
  ready: "可投递",
  queued: "等待推送",
  generated: "已生成",
  pending: "等待推送",
  sending: "正在推送",
  sent: "已推送",
  failed: "推送失败",
  skipped: "已跳过",
};
